package live

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"

	"alive/internal/diag"
)

// B 站取流。
//
// 主路径是 play-gateway/master/url（见 bilibili_master.go）：一次返回全部
// 档位和 CDN 线路，且不校验登录。getRoomPlayInfo 只用来拿流名和 g_qn_desc，
// 不再用它列档位——那个接口在匿名会话下恒定只按 250 下发。
//
// 旧的逐档请求逻辑保留在 biliFetchTiersByQN，仅作为 master 失效时的兜底。
//
// 完整的调研结论（登录门槛在前端、线路语义、稳定性实测）见
// internal/live/README.md。
const (
	qnDolby  = 30000 // 杜比
	qn4K     = 20000 // 4K
	qn2K     = 15000 // 2K
	qnOrigin = 10000 // 原画
	qnBluray = 400   // 蓝光
	qnUltra  = 250   // 超清
	qnHigh   = 150   // 高清
	qnSmooth = 80    // 流畅
)

// biliQNLabels 是网页端下拉里那套「分辨率 + 档位名」。
//
// 服务端 g_qn_desc 只给「原画 / 蓝光 / 超清」这类短名，2K 那档甚至直接叫
// "2K"。qn→分辨率是 B 站固定的对照表，不随房间变化，网页端也是这么拼的。
var biliQNLabels = map[int64]string{
	qnDolby:  "杜比",
	qn4K:     "4K 极清",
	qn2K:     "2K 极清",
	qnOrigin: "1080P 原画",
	qnBluray: "1080P 蓝光",
	qnUltra:  "720P 超清",
	qnHigh:   "480P 高清",
	qnSmooth: "360P 流畅",
}

// biliQualityLabel 给档位取显示名；表里没有的档位退回服务端的描述。
func biliQualityLabel(qn int64, desc string) string {
	if label, ok := biliQNLabels[qn]; ok {
		return label
	}
	if desc != "" {
		return desc
	}
	return "qn" + strconv.FormatInt(qn, 10)
}

// biliPlayURL 是 playinfo 里解析出的一个可播地址。
type biliPlayURL struct {
	URL      string
	Host     string // CDN 节点域名，线路就是按它分的
	Format   string
	Protocol string
	Codec    string // avc / hevc / av1，选流时优先兼容性好的
	// Pathway 是 PATHWAY-ID 声明的节点主机名；逐档兜底路径没有这份声明，留空。
	Pathway string
	QN      int64
	// 以下是 master 播放列表声明的规格；兜底路径拿不到，保留零值。
	Width     int
	Height    int
	Bitrate   int64
	FrameRate float64
}

// biliTier 是一个真实下发得到的清晰度档位。
//
// Width/Height/Bitrate/FrameRate 是 master 播放列表声明的规格，挂在档位上
// 而不是地址上：菜单要显示它们，而地址按需解析、解析出来的候选不带这些值。
type biliTier struct {
	QN        int64
	Width     int
	Height    int
	Bitrate   int64
	FrameRate float64
	URLs      []biliPlayURL
}

// biliPlayInfo 是一次取流的解析结果。
type biliPlayInfo struct {
	// Tiers 按 qn 降序，只含服务端真的给了码流的档位。
	Tiers []biliTier
	// QNDesc 是 g_qn_desc 的 qn→短名，给标签兜底用。
	QNDesc map[int64]string
	// Pathways 是 master 播放列表声明的 CDN 节点优先级链。
	// 逐档回退路径没有这份数据，为空。
	Pathways []string
	Live     bool
}

// Stream 返回房间详情和平台真实可播的档位、线路。
//
// 菜单用：只拿档位骨架（名称、分辨率、线路链），**不解析任何地址**。
// 真正要播哪一档由 StreamForPlayback 单独解析。
//
// 以前这里会把每个「档位 × 线路」都展开成媒体地址，一个房间十几个请求；
// 观测墙同时开 10 格就是一百多个请求，全挤在同一个限流器后面，整墙卡在
// 「正在拉取直播流」。地址对菜单没有意义，不该在列菜单时付这份钱。
func (p *bilibiliProvider) Stream(ctx context.Context, roomID, line string) (Stream, error) {
	out, info, err := p.biliStreamSkeleton(ctx, roomID)
	if err != nil || info == nil {
		return out, err
	}
	// 菜单保留全部档位，地址留空：切档时 StreamForPlayback 再解析。
	for _, tier := range info.Tiers {
		out.Variants = append(out.Variants, biliTierVariant(out.RoomID, tier, info.QNDesc, biliPlayURL{}))
	}
	return out, nil
}

// StreamForPlayback 只解析选中那一档的地址，供播放页和墙格起播。
//
// 这是播放路径唯一的取流入口。它只发一次 getRoomPlayInfo(qn=目标档)，
// 拿到的就是这一档的 FLV/HLS 候选，不需要 master、不需要展开。
//
// quality 是菜单标签（"1080P 原画"），空值或未命中时取最高档——和进房间的
// 默认选中一致。服务端降档时 selected 反映实际档位，不冒充请求的那档。
func (p *bilibiliProvider) StreamForPlayback(ctx context.Context, roomID, line, quality string) (Stream, Variant, error) {
	out, info, err := p.biliStreamSkeleton(ctx, roomID)
	if err != nil || info == nil || !out.Live {
		return out, Variant{}, err
	}

	// 菜单：全部档位、地址留空。
	for _, tier := range info.Tiers {
		out.Variants = append(out.Variants, biliTierVariant(out.RoomID, tier, info.QNDesc, biliPlayURL{}))
	}

	target := biliPickTierByLabel(info.Tiers, info.QNDesc, quality)
	if target == nil {
		return out, Variant{}, &UpstreamError{
			Platform: Bilibili, Op: "取流",
			Message: "平台没有返回可播放的清晰度",
		}
	}

	realID, _ := strconv.ParseInt(out.RoomID, 10, 64)
	// 从目标档往下逐档试：服务端不按请求的 qn 下发（匿名会话常见——
	// current_qn 恒为 250）时，继续往低档找，而不是把「这一档没有地址」
	// 直接甩给用户。菜单里还有更低档，播比不播重要。
	picked, pickedURL, err := biliResolveDown(info.Tiers, target, line, func(qn int64) ([]biliPlayURL, error) {
		return p.biliTierURLs(ctx, realID, qn)
	})
	if err != nil {
		return out, Variant{}, err
	}
	if picked.QN != target.QN {
		diag.L().Info().Str("room", out.RoomID).Int64("want_qn", target.QN).Int64("got_qn", picked.QN).Msg("B 站取流降级")
	}

	selected := biliTierVariant(out.RoomID, *picked, info.QNDesc, pickedURL)

	// 把解析出来的地址填回菜单对应项：调用方拿着 Stream 切档时不必再请求。
	for i := range out.Variants {
		if out.Variants[i].ID == selected.ID {
			out.Variants[i] = selected
		}
	}
	return out, selected, nil
}

// biliResolveDown 从 target 档往下逐档解析地址，返回第一个真的能播的档。
//
// fetch 返回某一档的可播地址；空切片表示服务端没按这一档下发。只往下降，
// 不会为了凑一个地址反而升档——用户选的是上限，不是下限。
func biliResolveDown(tiers []biliTier, target *biliTier, line string, fetch func(qn int64) ([]biliPlayURL, error)) (*biliTier, biliPlayURL, error) {
	var firstErr error
	for i := range tiers {
		tier := &tiers[i]
		if tier.QN > target.QN {
			continue
		}
		urls, err := fetch(tier.QN)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if u, ok := biliPickByLine(urls, strings.TrimSpace(line)); ok {
			return tier, u, nil
		}
	}
	if firstErr != nil {
		return nil, biliPlayURL{}, firstErr
	}
	return nil, biliPlayURL{}, &UpstreamError{
		Platform: Bilibili, Op: "取流",
		Message: "这一档及更低的档位都没有可播放的地址",
	}
}

// biliStreamSkeleton 取房间详情 + 档位骨架（无地址）。
//
// 返回的 info 里 Tiers 的 URLs 一律为空——master 那条路只用来拿档位名、
// 分辨率和线路优先级链，地址一律由 StreamForPlayback 单独解析。
func (p *bilibiliProvider) biliStreamSkeleton(ctx context.Context, roomID string) (Stream, *biliPlayInfo, error) {
	room, err := p.RoomInfo(ctx, roomID)
	if err != nil {
		return Stream{}, nil, err
	}
	out := Stream{
		RoomID:   room.RoomID,
		Platform: Bilibili,
		Title:    room.Title,
		Nickname: room.Nickname,
		Avatar:   room.Avatar,
		Live:     room.Live,
	}
	if !room.Live {
		return out, nil, nil
	}

	realID, err := strconv.ParseInt(room.RoomID, 10, 64)
	if err != nil || realID <= 0 {
		return Stream{}, nil, &UpstreamError{
			Platform: Bilibili, Op: "取流",
			Message: fmt.Sprintf("真实房间号 %q 不合法", room.RoomID),
		}
	}

	info, err := p.biliFetchPlayInfo(ctx, realID)
	if err != nil {
		return Stream{}, nil, err
	}
	if !info.Live {
		out.Live = false
		return out, nil, nil
	}

	// 线路 = master 播放列表的 PATHWAY 优先级链，ID 已经归一成节点主机名。
	//
	// 节点数量和族由服务端实时决定（同一房间实测见过 4 个 204 族、2 个 208 族、
	// 单个 209），所以只按返回顺序摆，不排序、不硬编码。第一个是主线。
	for i, id := range info.Pathways {
		label := "备线" + strconv.Itoa(i)
		if i == 0 {
			label = "主线"
		}
		out.Lines = append(out.Lines, Line{ID: id, Label: label})
	}
	if len(out.Lines) == 0 {
		// 没有 pathway 链时只能从档位自带的节点收，去重后按平台顺序摆。
		seen := map[string]bool{}
		for _, tier := range info.Tiers {
			for _, u := range tier.URLs {
				if u.Host == "" {
					continue
				}
				key := biliLineFamily(u.Host)
				if seen[key] {
					continue
				}
				seen[key] = true
				label := "备线" + strconv.Itoa(len(out.Lines))
				if len(out.Lines) == 0 {
					label = "主线"
				}
				out.Lines = append(out.Lines, Line{ID: u.Host, Label: label})
			}
		}
	}

	// 不额外造「自动」档：网页端那一项是把服务端的默认档又列了一遍，
	// 选它和选对应档位是同一路流。菜单按 qn 降序，第一档就是能拿到的
	// 最高档，进房间默认选它。
	return out, info, nil
}

// biliPickTierByLabel 按菜单标签找档位；空标签或没命中时取最高档。
//
// 比对用的是 biliQualityLabel(qn, desc)——菜单标签的同一个求值点。只查
// biliQNLabels 固定表的话，表外 qn（标签来自服务端 desc）永远匹配不上，
// 用户点到那一档会被静默换成最高档。
func biliPickTierByLabel(tiers []biliTier, desc map[int64]string, quality string) *biliTier {
	if len(tiers) == 0 {
		return nil
	}
	want := strings.TrimSpace(quality)
	if want != "" {
		for i := range tiers {
			if biliQualityLabel(tiers[i].QN, desc[tiers[i].QN]) == want {
				return &tiers[i]
			}
		}
	}
	return &tiers[0]
}

// biliTierVariant 把一个档位转成播放页的选项。
//
// 规格（分辨率 / 码率 / 帧率）取自档位本身——它是 master 播放列表的
// RESOLUTION / BANDWIDTH / FRAME-RATE，菜单要用；url 为空时就是「只有菜单
// 项、还没解析地址」的状态。
//
// Codec 只有解析过地址才知道（同一个 qn 可能同时有 avc / hevc 分支），
// 所以从 url 对应那条候选里读；没有地址就留空。
func biliTierVariant(roomID string, tier biliTier, desc map[int64]string, u biliPlayURL) Variant {
	v := Variant{
		ID:        biliQNSlug(tier.QN),
		Label:     biliQualityLabel(tier.QN, desc[tier.QN]),
		URL:       u.URL,
		Width:     tier.Width,
		Height:    tier.Height,
		Bitrate:   tier.Bitrate,
		FrameRate: tier.FrameRate,
		Codec:     u.Codec,
		// m3u8 本身不要 Referer，但里面的 .ts 分片会 403
		Headers: biliStreamHeaders(roomID),
		Expires: expiryFromQuery(u.URL, "expires"),
	}
	return v
}

// biliStreamHeaders 是回源要带的头。
//
// m3u8 本身不带 Referer 也能拉到，但里面的 .ts 分片会 403——ffmpeg 拉到
// 播放列表后卡在第一个分片上，表现就是播几秒就停。UA 一起给，CDN 会看它。
func biliStreamHeaders(roomID string) []string {
	return []string{
		"Referer: https://live.bilibili.com/" + roomID,
		"User-Agent: " + desktopUA,
	}
}

// biliPlayInfoURL 是取流信息接口。
//
// 这里不走库的 GetRoomPlayInfo：它的参数结构里没有 eotf / supported_drms /
// web_location，而网页端会带这三个，带上之后响应里的档位才全。
const biliPlayInfoURL = "https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo"

// biliRequestFn 是 getRoomPlayInfo 的实际请求实现。
//
// 抽成包级变量只为了测试能构造「HTTP 200 但 data 缺失」这一形状：库的
// Execute 这时返回 (nil, nil) 而不是错误，调用方必须自己判 nil。生产代码
// 永远走默认实现。
var biliRequestFn = func(ctx context.Context, api *biblib.Client, realID, qn int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
	return biblib.Execute[*bilibiliLive.GetRoomPlayInfoResult](ctx, api,
		"GET", biliPlayInfoURL,
		bilibiliLive.GetRoomPlayInfoParam{
			RoomID: realID,
			// Protocol 0 = http_stream（FLV），1 = http_hls。
			Protocol: "0,1",
			// Format 0 = flv，1 = ts，2 = fmp4。
			//
			// 三个都要：FLV 是首选（单连接，见 biliPickPlayable 的说明），
			// HLS 两档留作兜底——有些房间只给 HLS，不能把路走死。
			Format: "0,1,2",
			// Codec 0 = avc，1 = hevc，2 = av1。
			//
			// 不能像参照实现那样只要 avc：B 站的 1080P 原画常常只有 HEVC
			// 分支，只要 avc 会把最高档整档丢掉。
			Codec:    "0,1,2",
			Qn:       int(qn),
			Platform: "web",
			Ptype:    8,
			Dolby:    5,
			Panorama: 1,
		},
		biblib.FillParam("eotf", "0,1,2"),
		biblib.FillParam("supported_drms", "0,1,2,3"),
		biblib.FillParam("web_location", "444.8"),
	)
}

// biliRequest 按网页端参数请求一次 getRoomPlayInfo。
func (p *bilibiliProvider) biliRequest(ctx context.Context, realID, qn int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
	return biliRequestFn(ctx, p.api, realID, qn)
}

// biliFetchPlayInfo 取房间的档位骨架：档位名、分辨率、线路链。
//
// **不解析任何播放地址。** 地址由 StreamForPlayback 按需单独取，一次只解析
// 用户真正要播的那一档。以前这里会把每个「档位 × 线路」都展开成媒体地址，
// 一个房间十几个请求；观测墙同时开 10 格就是一百多个，全挤在同一个限流器
// 后面，整墙卡在「正在拉取直播流」。列菜单不需要地址，这份钱不该付。
//
// master/url 一次就能给出全部档位和 PATHWAY 链，是骨架的唯一来源。它不可用
// 时才退回 getRoomPlayInfo 的 accept_qn 列表——那份列表只有档位码，没有
// 分辨率，够列出菜单。
func (p *bilibiliProvider) biliFetchPlayInfo(ctx context.Context, realID int64) (*biliPlayInfo, error) {
	probe, err := p.biliRequest(ctx, realID, 0)
	if err != nil {
		return nil, p.wrap("取流信息", err)
	}
	// 库在 HTTP 200 但响应里没有 data 时返回 (nil, nil)：这不是错误，是
	// 「上游没给取流信息」。按错误交给上层显示错误态——取流跑在后台
	// goroutine 上，这里少一个判断就是整个客户端被 nil 解引用带走。
	if probe == nil {
		return nil, &UpstreamError{
			Platform: Bilibili, Op: "取流信息",
			Message: "平台没有返回取流信息",
		}
	}

	out := &biliPlayInfo{QNDesc: map[int64]string{}, Live: probe.LiveStatus == 1}
	if probe.PlayURLInfo == nil || probe.PlayURLInfo.PlayURL == nil {
		return out, nil
	}
	playurl := probe.PlayURLInfo.PlayURL
	for _, item := range playurl.GQNDesc {
		out.QNDesc[int64(item.Qn)] = item.Desc
	}
	if !out.Live {
		return out, nil
	}

	if name := biliStreamName(playurl); name != "" {
		master, err := p.biliMasterPlayInfo(ctx, realID, name, out.QNDesc)
		if err == nil && master != nil && len(master.Tiers) > 0 {
			// 骨架不要地址：master 给的是 HLS，而播放走 FLV，留着只会误导。
			for i := range master.Tiers {
				master.Tiers[i].URLs = nil
			}
			return master, nil
		}
		diag.L().Debug().Str("stream", name).Dict(diag.ErrField, diag.Err(err)).Msg("B 站 master 播放列表不可用，回退档位表")
	}

	// 兜底：从 accept_qn 拼出档位骨架，没有地址也没有分辨率。
	for _, qn := range biliAcceptQNs(playurl) {
		out.Tiers = append(out.Tiers, biliTier{QN: qn})
	}
	return out, nil
}

// biliStreamSuffixQN 从 base_url 的流名后缀推断这个分支名过去对应的档位。
//
// 只作旁证，不能拿来否决服务端的档位声明：同一个后缀在不同房间、不同编码
// 分支下会指向不同档位。仓库实测的 master（见 bilibili_line_test.go 的
// fixture）把 BILI-QN=10000 / 原画 / 1080x1920 挂在 live_demo_bluray 上，
// 同一条直播的蓝光档才是 _400——_bluray 是分支名，不是档位承诺。
//
// 认不出来返回 0。
func biliStreamSuffixQN(url string) int64 {
	lower := strings.ToLower(url)
	if i := strings.IndexByte(lower, '?'); i >= 0 {
		lower = lower[:i]
	}
	switch {
	case strings.Contains(lower, "_dolby"):
		return qnDolby
	case strings.Contains(lower, "_uhd") || strings.Contains(lower, "_4k"):
		return qn4K
	case strings.Contains(lower, "_2k"):
		return qn2K
	case strings.Contains(lower, "_prohevc"), strings.Contains(lower, "_origin"):
		return qnOrigin
	case strings.Contains(lower, "_bluray"), strings.Contains(lower, "_4000"):
		return qnBluray
	case strings.Contains(lower, "_minihevc"), strings.Contains(lower, "_2500"):
		return qnUltra
	case strings.Contains(lower, "_1500"), strings.Contains(lower, "_high"):
		return qnHigh
	case strings.Contains(lower, "_1000"), strings.Contains(lower, "_smooth"):
		return qnSmooth
	}
	return 0
}

// biliTierURLs 请求某一档，只收「服务端真的按这档下发」的地址。
//
// 判据是 current_qn 必须等于请求的 qn，且地址上的 expected_qn（降档承诺，
// 有的话）也必须等于 qn。current_qn 说明服务端按这档回，expected_qn 说明
// 它准备按这档下发；两者才是档位证据。
//
// 流名后缀（_bluray / _2500 / _prohevc）只记日志、不参与判定。后缀描述的是
// 编码 / 分支，不是档位：实测 master 把原画挂在 _bluray 分支上，把它当成
// 蓝光(400) 会删掉原画档唯一合法的地址。
func (p *bilibiliProvider) biliTierURLs(ctx context.Context, realID, qn int64) ([]biliPlayURL, error) {
	res, err := p.biliRequest(ctx, realID, qn)
	if err != nil {
		return nil, err
	}
	// 同 biliFetchPlayInfo：data 缺失时库返回 (nil, nil)，判 err 不够。
	if res == nil {
		return nil, &UpstreamError{
			Platform: Bilibili, Op: "取流",
			Message: "平台没有返回这一档的取流信息",
		}
	}
	if res.PlayURLInfo == nil || res.PlayURLInfo.PlayURL == nil {
		return nil, nil
	}

	var urls []biliPlayURL
	for _, stream := range res.PlayURLInfo.PlayURL.Stream {
		for _, format := range stream.Format {
			for _, codec := range format.Codec {
				if int64(codec.CurrentQn) != qn {
					continue
				}
				for _, u := range codec.URLInfo {
					full := biliJoinURL(u.Host, codec.BaseURL, u.Extra)
					if full == "" {
						continue
					}
					// 后缀只作旁证：_bluray 是分支名，master 实测把原画
					// （BILI-QN=10000）挂在它上面。拿它推翻服务端 current_qn
					// 明确声明的档位，会把原画唯一合法的地址删掉，整档变成
					// 「没有可播放的地址」。地址里有签名 token，不进日志。
					if suffix := biliStreamSuffixQN(full); suffix > 0 && suffix != qn {
						diag.L().Debug().Int64("qn", qn).Int64("suffix_qn", suffix).Msg("B 站流名后缀与档位不符，以服务端声明为准")
					}
					if expected := biliExpectedQN(full); expected > 0 && expected != qn {
						continue
					}
					urls = append(urls, biliPlayURL{
						URL:      full,
						Host:     biliShortHost(u.Host),
						Format:   format.FormatName,
						Protocol: stream.ProtocolName,
						Codec:    codec.CodecName,
						QN:       qn,
					})
				}
			}
		}
	}
	return urls, nil
}

// biliAcceptQNs 收集所有 codec 声明的可请求档位，降序去重。
func biliAcceptQNs(playurl *bilibiliLive.GetRoomPlayInfoPlayURL) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, 8)
	for _, stream := range playurl.Stream {
		for _, format := range stream.Format {
			for _, codec := range format.Codec {
				for _, raw := range codec.AcceptQn {
					qn := int64(raw)
					if qn > 0 && !seen[qn] {
						seen[qn] = true
						out = append(out, qn)
					}
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
	return out
}

// biliExpectedQN 从播放地址的 query 里取 expected_qn。
//
// 这是服务端「准备下发」的档位，和 codec 上的 current_qn 不一定一致：
// 匿名会话请求 1080P 原画时，current_qn 写 10000，地址里却带着
// expected_qn=250，实际播出来就是 720P。没有这个参数时返回 0，表示
// 这条地址没带降档承诺，按 current_qn 处理。
func biliExpectedQN(raw string) int64 {
	i := strings.IndexByte(raw, '?')
	if i < 0 {
		return 0
	}
	for _, part := range strings.Split(raw[i+1:], "&") {
		if !strings.HasPrefix(part, "expected_qn=") {
			continue
		}
		n, err := strconv.ParseInt(strings.TrimPrefix(part, "expected_qn="), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// biliPickByLine 先按用户选的线路过滤，再在这一档里挑最可能能播的那条。
//
// 匹配走 biliPathwayMatch：节点主机名、整条节点地址、裸节点 ID 三种写法都认。
// 旧实现只比主机名，优先级链下发裸 ID（204-4）时永远匹配不上，线路就成了装饰。
//
// 这一档在选中线路上没有候选时仍然返回该档最好的那条：宁可给一个别家节点的
// 地址，也不让菜单空掉。这是原有行为，保持不变。
func biliPickByLine(urls []biliPlayURL, want string) (biliPlayURL, bool) {
	if len(urls) == 0 {
		return biliPlayURL{}, false
	}
	if strings.TrimSpace(want) != "" {
		matched := make([]biliPlayURL, 0, len(urls))
		for _, u := range urls {
			if biliOnLine(u, want) {
				matched = append(matched, u)
			}
		}
		if len(matched) > 0 {
			return biliPickPlayable(matched)
		}
	}
	return biliPickPlayable(urls)
}

// biliOnLine 报告一条候选是否落在用户选中的线路上。
//
// master 路径靠 PATHWAY-ID 声明的节点判断；逐档兜底路径没有这份声明，
// 只能退回地址自己的主机名。
func biliOnLine(u biliPlayURL, want string) bool {
	if u.Pathway != "" && biliPathwayMatch(u.Pathway, want) {
		return true
	}
	return biliPathwayMatch(u.Host, want)
}

// biliLineFamily 把同一个 CDN 节点的主备域名归成一条线路。
//
// B 站给一档会同时下发 gotcha209 和 gotcha209b 两个域名，它们是同一台
// 节点的两条入口，网页端的「线路」下拉按节点算，所以这里也按节点归并，
// 否则下拉里会出现一堆只差一个 b 的重复项。
func biliLineFamily(host string) string {
	h := strings.TrimSuffix(host, ".bilivideo.com")
	if len(h) > 1 && strings.HasSuffix(h, "b") {
		h = h[:len(h)-1]
	}
	return h
}

// biliShortHost 去掉协议和尾部斜杠，留下可比较、可展示的节点域名。
func biliShortHost(raw string) string {
	h := strings.TrimSpace(raw)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimSuffix(h, "/")
}

// biliJoinURL 拼 host + base_url + extra。
func biliJoinURL(host, base, extra string) string {
	if host == "" || base == "" {
		return ""
	}
	full := strings.TrimSuffix(host, "/") + base
	if extra != "" {
		// base_url 里可能已经带了 ?，extra 也可能是 ?a=1 或 a=1 两种形态
		if !strings.HasPrefix(extra, "?") && !strings.Contains(base, "?") {
			full += "?"
		} else if strings.HasPrefix(extra, "?") && strings.Contains(base, "?") {
			extra = strings.TrimPrefix(extra, "?")
			full += "&"
		}
		full += extra
	}
	return strings.Replace(full, "http://", "https://", 1)
}

// biliPickPlayable 从一组地址里挑一个最可能直接能播的。
//
// 打分顺序：**FLV 优先**，其次是 avc（系统解码器一定有，hevc 要看有没有装
// HEVC 视频扩展），最后才是节点偏好。
//
// 为什么 FLV 优先而不是 HLS：libmpv 的嵌入式用法强制自定义 io_open，mpv
// 因此主动关掉 hls 的连接复用（它自己的日志：
// 「hls: Disabling http_persistent due to custom io_open」），于是每个 1 秒
// 分片都要新建一条 TCP+TLS 连接，实测单片 380ms，而复用只要 40ms。分片
// 取数追不上实时，playlist 滚过去就跳分片、丢关键帧、HEVC 参考链崩。
//
// FLV 是单条长连接：从头到尾一条 TCP，io_open 只发生一次，上述问题不存在。
// 实测同一房间 1080P（B 站 263605，2026-11-21，各 60 秒）：
//
//	HLS(fMP4)  缓冲 4–23 秒，avsync 漂到 8 秒，首帧 7.7 秒
//	FLV        缓冲 0 秒，avsync 0，首帧 1.4 秒
//
// 参照实现 dart_simple_live（同样是 media_kit/libmpv）也只用 format 0,2，
// 明确排除 HLS。
func biliPickPlayable(urls []biliPlayURL) (biliPlayURL, bool) {
	if len(urls) == 0 {
		return biliPlayURL{}, false
	}
	best := biliPlayURL{}
	bestScore := -1 << 30
	for _, u := range urls {
		score := 0
		// FLV 单连接，是直播的首选形态。
		if biliIsFLV(u) {
			score += 200
		}
		if strings.EqualFold(u.Codec, "avc") {
			score += 40
		}
		// d1--cn 系节点实测最稳
		if strings.Contains(u.URL, "d1--cn") {
			score += 20
		}
		if strings.Contains(u.URL, "mcdn") {
			score -= 5
		}
		if score > bestScore {
			best, bestScore = u, score
		}
	}
	if bestScore == -1<<30 {
		return biliPlayURL{}, false
	}
	return best, true
}

// biliIsFLV 判断地址是不是 FLV 直播流。
func biliIsFLV(u biliPlayURL) bool {
	if strings.EqualFold(u.Format, "flv") {
		return true
	}
	return strings.Contains(strings.ToLower(u.URL), ".flv")
}

// biliIsHLS 判断地址是不是 m3u8。
func biliIsHLS(u biliPlayURL) bool {
	name := strings.ToLower(u.Format)
	if name == "hls" || strings.Contains(name, "ts") || strings.Contains(name, "fmp4") {
		return true
	}
	return strings.Contains(u.URL, ".m3u8")
}

// biliQNSlug 给档位一个稳定的 id，跨平台保持同一套说法。
func biliQNSlug(qn int64) string {
	switch qn {
	case qnDolby:
		return "dolby"
	case qn4K:
		return "uhd"
	case qn2K:
		return "qhd"
	case qnOrigin:
		return "source"
	case qnBluray:
		return "bluray"
	case qnUltra:
		return "high"
	case qnHigh:
		return "standard"
	case qnSmooth:
		return "low"
	default:
		return "qn" + strconv.FormatInt(qn, 10)
	}
}

// 编译期确认 B 站实现了取流接口。
var _ Streamer = (*bilibiliProvider)(nil)

// 播放路径走按需解析：列菜单不解析地址，只解析真正要播的那一档。
// 完整档位解析（每档一次请求）会让观测墙同时开十几格时把限流器挤爆。
var _ PlaybackStreamer = (*bilibiliProvider)(nil)
