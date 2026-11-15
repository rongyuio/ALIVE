package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
)

// 抖音取流。
//
// 链路与房间信息同一套两步：房间页拿内部 roomId，再调 reflow 拿房间对象；
// 区别只是这里要读 room.stream_url。
//
// 实测结论（与参考实现一致，2026-11）：
//   - 原画**不在** flv_pull_url 里，它只出现在
//     live_core_sdk_data.pull_data.stream_data 这段内嵌 JSON 的 data.origin.main；
//   - flv_pull_url 只有 FULL_HD1 / HD1 / SD1 / SD2 四个转码档，而且
//     SD1 实际是最低的标清、SD2 才是高清（名字与档位错位）；
//   - FLV 直连必须带 Referer，否则 CDN 返回 403，所以 Variants 统一给 Headers。
// 抖音没有多线路概念：Stream 不给 Lines，控制条上的线路控件就不会出现。

// douyinStreamHeaders 是回源需要的请求头。抖音的 FLV / HLS 都校验 Referer。
var douyinStreamHeaders = []string{"Referer: " + douyinReferer}

// douyinQuality 是一个可用的清晰度档位。
type douyinQuality struct {
	Key    string // 上游档位 key（origin / uhd / hd / sd / ld）
	Label  string // 展示名
	ID     string // 跨平台稳定 id
	URL    string
	Format string // flv / hls
	// Codec 是上游 sdk_params 里识别到的编码；识别不到时留空。
	Codec string
}

// douyinTierStream 是 stream_data 里的一个清晰度档位。
type douyinTierStream struct {
	FLV        string
	HLS        string
	Codec      string // sdk_params.VCodec：h264 / h265
	Resolution string
}

// douyinIsHEVC 判断这一档是不是 H.265。
//
// 原画几乎总是 H.265，而解码器不一定跟得上（系统缺 HEVC 扩展时播不了），
// 所以原画是 HEVC 时不能默认顶在第一档——默认档要让给 H.264 转码档。
// 归一化只走 douyinVariantCodec，判断别名不另写一份。
func (t *douyinTierStream) douyinIsHEVC() bool {
	return douyinVariantCodec(t.Codec) == "hevc"
}

// douyinVariantCodec 把 sdk_params.VCodec 归一成 relay 认的编码名。
// 识别不了时返回空串，转封装层仍按 H.264 处理。
func douyinVariantCodec(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "h264", "264", "avc", "avc1":
		return "avc"
	case "h265", "265", "hevc", "hvc1":
		return "hevc"
	case "av1", "av01":
		return "av1"
	default:
		return ""
	}
}

// 抖音的取流优先给 HTTP-FLV。
//
// 网页端就是这么做的：fetch 一条 FLV 连续流喂给 MSE，边收边解边出帧，没有分片边界，
// 收到第一个关键帧就能显示。我们曾经在同档有 HLS 时优先选 HLS，理由是为 Media
// Foundation「省去不必要的重新切片」——那是代理时代的取舍。自解码路径下 ffmpeg
// 直接读 FLV 就行，不需要切片；而 HLS 要先解包 master（抖音有的房间套三层），再按
// 分片节奏吐数据，两头都更慢。
//
// 只有 FLV 缺失时才退回 HLS。

// douyinTiers 是抖音的画质阶梯，从高到低。
//
// 中文名对齐服务端 options.qualities[].name（实测：ld=标清 / sd=高清 /
// hd=超清 / uhd=蓝光，origin=原画）。md（流畅）不进菜单——摆一档没人会点的
// 最低画质只会让菜单变长。
var douyinTiers = []struct {
	Key   string
	Label string
	ID    string
}{
	{"origin", "原画", "source"},
	{"uhd", "蓝光", "bluray"},
	{"hd", "超清", "high"},
	{"sd", "高清", "standard"},
	{"ld", "标清", "low"},
}

// douyinFlvPullKeyTier 把 flv_pull_url 的 key 映射到画质档位。
//
// 只在地址里判不出档位时才用这张表。实测对应关系（文件名后缀可交叉验证）：
//
//	FULL_HD1 → _uhd.flv（蓝光）
//	HD1      → _hd.flv （超清）
//	SD1      → _ld.flv （标清，1Mbps）  ← 名字里是 SD，实际是最低档
//	SD2      → _sd.flv （高清，2Mbps）
var douyinFlvPullKeyTier = map[string]string{
	"FULL_HD1": "uhd",
	"HD1":      "hd",
	"SD1":      "ld",
	"SD2":      "sd",
}

/* ---------------------------------------------------------------- 接口 */

// Stream 返回房间详情和全部可播档位。
func (p *douyinProvider) Stream(ctx context.Context, roomID, line string) (Stream, error) {
	// 抖音没有线路概念，line 只是为了满足接口契约。
	_ = line

	webRID := douyinNormalizeRoomID(roomID)
	if webRID == "" {
		return Stream{}, &UpstreamError{
			Platform: Douyin, Op: "取流",
			Message: fmt.Sprintf("房间号 %q 无效", strings.TrimSpace(roomID)),
		}
	}

	// web_rid → 内部 roomId 不随开播状态变化，复用 RoomInfo 的同一份缓存，
	// 省掉每次取流都下载一遍 1MB 的房间页。
	ref, err := p.ids.load(webRID, douyinRoomIDTTL, func() (douyinRoomRef, error) {
		return p.douyinResolveRoom(ctx, webRID)
	})
	if err != nil {
		return Stream{}, err
	}

	room, err := p.douyinFetchStreamRoom(ctx, ref.InternalID)
	if err != nil {
		return Stream{}, err
	}

	out := Stream{
		RoomID:   webRID,
		Platform: Douyin,
		Title:    strings.TrimSpace(room.Title),
		Nickname: strings.TrimSpace(room.Owner.Nickname),
		Avatar:   httpsURL(firstNonEmpty(room.Owner.AvatarThumb.first(), room.Owner.AvatarMed.first())),
		// 抖音没有多线路：不给 Lines，界面上就不会出现线路控件。
		// status 语义：2=直播中，4=已结束/未开播；未知值按未开播处理。
		Live: room.Status == 2,
	}
	if !out.Live {
		// status 缺失（0）时退回「有没有可播地址」兜底，与列表的宽松语义一致。
		out.Live = room.Status == 0 && len(douyinPickQualities(&room)) > 0
		if !out.Live {
			return out, nil
		}
	}

	quals := douyinOrderForPlayback(room.StreamURL.parseStreamData(), douyinPickQualities(&room))
	for _, q := range quals {
		out.Variants = append(out.Variants, Variant{
			ID:    q.ID,
			Label: q.Label,
			URL:   httpsURL(q.URL),
			// FLV / HLS 都校验 Referer，缺了就是 403。
			Headers: douyinStreamHeaders,
			Codec:   q.Codec,
		})
	}
	if len(out.Variants) == 0 {
		// 缺少地址不代表下播，保留上游状态，让播放层报告取流失败。
		slog.Debug("抖音取流没有可用清晰度", "room", webRID)
	}
	return out, nil
}

// douyinFetchStreamRoom 用内部 roomId 取房间对象（含 stream_url）。
//
// 参数顺序固定：签名是对这份原始串做哈希的。
func (p *douyinProvider) douyinFetchStreamRoom(ctx context.Context, internalID string) (douyinStreamRoom, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return douyinStreamRoom{}, err
	}
	defer release()

	params := [][2]string{
		{"type_id", "0"},
		{"live_id", "1"},
		{"version_code", douyinReflowVersionCode},
		{"app_id", douyinReflowAppID},
		{"room_id", internalID},
		{"msToken", douyinMsToken()},
	}

	var resp douyinStreamResponse
	target := douyinSignedURL(douyinURLReflow, params, p.api.ua)
	if err := p.api.getJSON(ctx, "取流信息", target, &resp, nil); err != nil {
		return douyinStreamRoom{}, p.wrap("取流信息", err)
	}
	return douyinParseStreamRoom(resp)
}

// douyinParseStreamRoom 解析响应并做业务错误分类（拆出来是为了离线测试）。
func douyinParseStreamRoom(resp douyinStreamResponse) (douyinStreamRoom, error) {
	if resp.StatusCode == douyinReflowInvalidRoom {
		return douyinStreamRoom{}, &UpstreamError{
			Platform: Douyin, Op: "取流",
			Message: "房间不存在或已失效",
		}
	}
	if resp.StatusCode != 0 {
		return douyinStreamRoom{}, &UpstreamError{
			Platform: Douyin, Op: "取流",
			Message: fmt.Sprintf("status_code=%d %s", resp.StatusCode, resp.StatusMsg),
		}
	}

	var data douyinStreamData
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return douyinStreamRoom{}, &UpstreamError{
				Platform: Douyin, Op: "取流",
				Message: "响应解析失败: " + err.Error(),
			}
		}
	}
	if data.Room == nil {
		return douyinStreamRoom{}, &UpstreamError{
			Platform: Douyin, Op: "取流",
			Message: "响应里没有房间数据",
		}
	}
	return *data.Room, nil
}

/* ------------------------------------------------------------ 响应结构 */

type douyinStreamResponse struct {
	StatusCode int             `json:"status_code"`
	StatusMsg  string          `json:"status_msg"`
	Data       json.RawMessage `json:"data"`
}

type douyinStreamData struct {
	Room *douyinStreamRoom `json:"room"`
}

// douyinStreamRoom 只取播放页需要的字段。
type douyinStreamRoom struct {
	Title     string           `json:"title"`
	Status    int              `json:"status"`
	Owner     douyinOwner      `json:"owner"`
	StreamURL *douyinStreamURL `json:"stream_url"`
}

// douyinStreamURL 是 stream_url 对象。
//
// flv_pull_url / hls_pull_url_map 只有转码档，原画在 live_core_sdk_data 的
// 内嵌 stream_data 里（或者旧版结构的 pull_datas），两个来源都要兼容。
type douyinStreamURL struct {
	FlvPullURL map[string]string `json:"flv_pull_url"`
	HLSPullURL map[string]string `json:"hls_pull_url_map"`
	// 新版接口：pull_data.stream_data
	LiveCoreSDKData *struct {
		PullData *struct {
			StreamData string `json:"stream_data"`
		} `json:"pull_data"`
	} `json:"live_core_sdk_data"`
	// 部分版本：pull_datas[<key>].stream_data
	PullDatas map[string]*struct {
		StreamData string `json:"stream_data"`
	} `json:"pull_datas"`
}

// streamDataJSON 取内嵌的 stream_data 原始 JSON。
func (s *douyinStreamURL) streamDataJSON() string {
	if s == nil {
		return ""
	}
	if s.LiveCoreSDKData != nil && s.LiveCoreSDKData.PullData != nil {
		if raw := strings.TrimSpace(s.LiveCoreSDKData.PullData.StreamData); raw != "" {
			return raw
		}
	}
	// 各 key 的 stream_data 内容一致，取第一个非空的即可。
	for _, entry := range s.PullDatas {
		if entry != nil {
			if raw := strings.TrimSpace(entry.StreamData); raw != "" {
				return raw
			}
		}
	}
	return ""
}

// parseStreamData 把 stream_data 解析成「档位 key → 流」。
//
// 这份内嵌 JSON 才是清晰度的权威来源：它每一档都带服务端给的 sdk_params
// （VCodec / resolution），可以据此排对阶梯并判断是不是 HEVC。
func (s *douyinStreamURL) parseStreamData() map[string]*douyinTierStream {
	raw := s.streamDataJSON()
	if raw == "" {
		return nil
	}
	var outer struct {
		Data map[string]struct {
			Main struct {
				FLV       string `json:"flv"`
				HLS       string `json:"hls"`
				SDKParams string `json:"sdk_params"`
			} `json:"main"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		slog.Debug("抖音 stream_data 解析失败，退回 flv_pull_url", "error", err)
		return nil
	}
	out := make(map[string]*douyinTierStream, len(outer.Data))
	for key, entry := range outer.Data {
		flv := strings.TrimSpace(entry.Main.FLV)
		hls := strings.TrimSpace(entry.Main.HLS)
		if flv == "" && hls == "" {
			continue
		}
		item := &douyinTierStream{FLV: flv, HLS: hls}
		if params := strings.TrimSpace(entry.Main.SDKParams); params != "" {
			var p struct {
				VCodec     string `json:"VCodec"`
				Resolution string `json:"resolution"`
			}
			if json.Unmarshal([]byte(params), &p) == nil {
				item.Codec = p.VCodec
				item.Resolution = p.Resolution
			}
		}
		out[key] = item
	}
	return out
}

/* -------------------------------------------------------------- 映射 */

// douyinTierFromURL 从取流地址里判断画质档位。
//
// 后缀可能在文件名里（.../stream-1_uhd.flv），也可能在目录名里
// （HLS 的 .../stream-1_or4/index.m3u8），所以从路径最后两段往前找。
func douyinTierFromURL(raw string) string {
	u := raw
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	segments := strings.Split(strings.Trim(u, "/"), "/")
	for i := len(segments) - 1; i >= 0 && i >= len(segments)-2; i-- {
		seg := segments[i]
		seg = strings.TrimSuffix(seg, ".flv")
		seg = strings.TrimSuffix(seg, ".m3u8")
		idx := strings.LastIndex(seg, "_")
		if idx < 0 {
			continue
		}
		switch suffix := seg[idx+1:]; suffix {
		case "or4", "origin":
			return "origin"
		case "uhd", "hd", "sd", "ld", "md":
			return suffix
		}
	}
	return ""
}

// douyinTierQuality 把一个档位转成可播档位。协议选择见上面 douyinPrefersHLS 那段说明：
// 优先 FLV，只有 FLV 缺失才用 HLS。
func douyinTierQuality(tier, label, id string, stream *douyinTierStream) (douyinQuality, bool) {
	if stream == nil {
		return douyinQuality{}, false
	}
	hevc := stream.douyinIsHEVC()
	url, format := stream.FLV, "flv"
	if url == "" {
		url, format = stream.HLS, "hls"
	}
	if url == "" {
		return douyinQuality{}, false
	}
	if hevc {
		label = label + " · H.265"
	}
	return douyinQuality{
		Key: tier, Label: label, ID: id, URL: url, Format: format,
		Codec: douyinVariantCodec(stream.Codec),
	}, true
}

// douyinPickQualities 从房间数据里挑出可用清晰度，按画质从高到低排列。
//
// 优先用 stream_data（权威阶梯、带编码信息），它缺档时再用 flv_pull_url 补齐。
func douyinPickQualities(room *douyinStreamRoom) []douyinQuality {
	if room == nil || room.StreamURL == nil {
		return nil
	}
	su := room.StreamURL
	streams := su.parseStreamData()
	pullURL := su.FlvPullURL
	if len(streams) == 0 && len(pullURL) == 0 && len(su.HLSPullURL) == 0 {
		return nil
	}

	byTier := make(map[string]*douyinTierStream, len(streams))
	for tier, stream := range streams {
		if tier == "ao" { // 纯音频通道，不进画质菜单
			continue
		}
		byTier[tier] = stream
	}

	// flv_pull_url 兜底：老响应没有 stream_data，或者某档只在这里给。
	for key, u := range pullURL {
		if strings.TrimSpace(u) == "" {
			continue
		}
		tier := douyinTierFromURL(u)
		if tier == "" {
			tier = douyinFlvPullKeyTier[key]
		}
		if tier == "" || byTier[tier] != nil {
			continue
		}
		byTier[tier] = &douyinTierStream{FLV: u}
	}
	for _, u := range su.HLSPullURL {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		tier := douyinTierFromURL(u)
		if tier == "" {
			continue
		}
		// 独立地址表没有编码声明，不能借用另一来源同名档位的编码。
		if byTier[tier] != nil {
			continue
		}
		byTier[tier] = &douyinTierStream{HLS: u}
	}

	out := make([]douyinQuality, 0, len(byTier))
	for _, t := range douyinTiers {
		q, ok := douyinTierQuality(t.Key, t.Label, t.ID, byTier[t.Key])
		if !ok {
			continue
		}
		out = append(out, q)
	}

	// 完全没有阶梯信息时，退回「最高可用档位叫原画」。
	if len(out) == 0 {
		keys := make([]string, 0, len(pullURL))
		for k, v := range pullURL {
			if strings.TrimSpace(v) != "" {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, douyinQuality{
				Key: k, Label: k, ID: strings.ToLower(k), URL: pullURL[k], Format: "flv",
			})
		}
		if len(out) > 0 {
			out[0].Label = "原画"
		}
	}

	return out
}

// douyinOrderForPlayback 调整档位顺序，保证第一个（默认档）能播。
//
// 播放页只取 Variants[0]。原画是 H.265 时把它挪到第二位并标注编码，
// 默认档退回最高的 H.264 转码档——缺 HEVC 解码器的机器才不至于黑屏。
func douyinOrderForPlayback(streams map[string]*douyinTierStream, ladder []douyinQuality) []douyinQuality {
	if len(ladder) == 0 {
		return ladder
	}
	origin := streams["origin"]
	if origin == nil || !origin.douyinIsHEVC() {
		return ladder
	}

	for i, q := range ladder {
		if q.Key != "origin" || i == 1 {
			continue
		}
		q.Label = "原画 · H.265"
		rest := make([]douyinQuality, 0, len(ladder))
		rest = append(rest, ladder[:i]...)
		rest = append(rest, ladder[i+1:]...)
		pos := 1
		if len(rest) < pos {
			pos = len(rest)
		}
		rest = append(rest, douyinQuality{})
		copy(rest[pos+1:], rest[pos:])
		rest[pos] = q
		return rest
	}
	return ladder
}

// 编译期确认抖音实现了取流接口。
var _ Streamer = (*douyinProvider)(nil)
