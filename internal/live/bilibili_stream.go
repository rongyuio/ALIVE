package live

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
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
type biliTier struct {
	QN   int64
	URLs []biliPlayURL
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
func (p *bilibiliProvider) Stream(ctx context.Context, roomID, line string) (Stream, error) {
	// 房间详情直接复用 RoomInfo：它已经处理了短号→真实 room_id 的两跳，
	// 播放页和卡片上看到的也就是同一份数据。
	room, err := p.RoomInfo(ctx, roomID)
	if err != nil {
		return Stream{}, err
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
		return out, nil
	}

	realID, err := strconv.ParseInt(room.RoomID, 10, 64)
	if err != nil || realID <= 0 {
		return Stream{}, &UpstreamError{
			Platform: Bilibili, Op: "取流",
			Message: fmt.Sprintf("真实房间号 %q 不合法", room.RoomID),
		}
	}

	info, err := p.biliFetchPlayInfo(ctx, realID)
	if err != nil {
		return Stream{}, err
	}
	if !info.Live || len(info.Tiers) == 0 {
		out.Live = false
		return out, nil
	}

	// 线路 = master 播放列表的 PATHWAY 优先级链，ID 已经归一成节点主机名。
	//
	// 节点数量和族由服务端实时决定（同一房间实测见过 4 个 204 族、2 个 208 族、
	// 单个 209），所以只按返回顺序摆，不排序、不硬编码。第一个是主线。
	//
	// 回退路径没有 pathway 信息，只能从地址里收 HLS 节点——那收出来的是
	// 协议族（ts / fmp4），不是网页端意义上的线路，保留作最后兜底。
	if len(info.Pathways) > 0 {
		for i, id := range info.Pathways {
			label := "备线" + strconv.Itoa(i)
			if i == 0 {
				label = "主线"
			}
			out.Lines = append(out.Lines, Line{ID: id, Label: label})
		}
	} else {
		seen := map[string]bool{}
		hosts := make([]string, 0, 8)
		for _, tier := range info.Tiers {
			for _, u := range tier.URLs {
				if u.Host == "" || !biliIsHLS(u) {
					continue
				}
				key := biliLineFamily(u.Host)
				if seen[key] {
					continue
				}
				seen[key] = true
				hosts = append(hosts, u.Host)
			}
		}
		for i, h := range hosts {
			label := "备线" + strconv.Itoa(i)
			if i == 0 {
				label = "主线"
			}
			out.Lines = append(out.Lines, Line{ID: h, Label: label})
		}
	}

	want := strings.TrimSpace(line)
	for _, tier := range info.Tiers {
		u, ok := biliPickByLine(tier.URLs, want)
		if !ok {
			continue
		}
		out.Variants = append(out.Variants, biliPlaybackVariant(room.RoomID, tier.QN, info.QNDesc, u))
	}

	// 不额外造「自动」档：网页端那一项是把服务端的默认档又列了一遍，
	// 选它和选对应档位是同一路流。菜单按 qn 降序，第一档就是能拿到的
	// 最高档，进房间默认选它。

	if len(out.Variants) == 0 {
		out.Live = false
	}
	return out, nil
}

// biliPlaybackVariant 把一个档位地址转成播放页的选项。
//
// 分辨率、码率、帧率都来自 master 播放列表的 RESOLUTION / BANDWIDTH /
// FRAME-RATE，是上游声明值；兜底路径没有这些数据时保持零值。
func biliPlaybackVariant(roomID string, qn int64, desc map[int64]string, u biliPlayURL) Variant {
	return Variant{
		ID:        biliQNSlug(qn),
		Label:     biliQualityLabel(qn, desc[qn]),
		URL:       u.URL,
		Width:     u.Width,
		Height:    u.Height,
		Bitrate:   u.Bitrate,
		FrameRate: u.FrameRate,
		// m3u8 本身不要 Referer，但里面的 .ts 分片会 403
		Headers: biliStreamHeaders(roomID),
		Codec:   u.Codec,
	}
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

// biliVariantConcurrency 同时拉几档：再多容易把上游惹毛。
const biliVariantConcurrency = 4

// biliRequest 按网页端参数请求一次 getRoomPlayInfo。
func (p *bilibiliProvider) biliRequest(ctx context.Context, realID, qn int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	return biblib.Execute[*bilibiliLive.GetRoomPlayInfoResult](ctx, p.api,
		"GET", biliPlayInfoURL,
		bilibiliLive.GetRoomPlayInfoParam{
			RoomID:   realID,
			Protocol: "0,1",
			Format:   "0,1,2",
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

// biliFetchPlayInfo 取房间的全部档位。
//
// 主路径是 master 播放列表：一次请求拿回全部档位和线路，且不校验登录——
// 未登录也能看到平台实际开放的最高档。网页端是播放器前端拦的，接口和
// CDN 都不拦。详见 internal/live/README.md。
//
// master 不可用时回退到逐档请求。那条路在匿名会话下只能拿到一档
// （服务端恒定按 250 下发），但总比什么都没有好。
func (p *bilibiliProvider) biliFetchPlayInfo(ctx context.Context, realID int64) (*biliPlayInfo, error) {
	probe, err := p.biliRequest(ctx, realID, 0)
	if err != nil {
		return nil, p.wrap("取流信息", err)
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
			return master, nil
		}
		slog.Debug("B 站 master 播放列表不可用，回退逐档请求", "stream", name, "error", err)
	}

	return p.biliFetchTiersByQN(ctx, realID, playurl, out)
}

// biliFetchTiersByQN 是 master 接口失效时的兜底：逐档请求，只收服务端
// 真的按这档下发的地址。
//
// 匿名会话下这个接口恒定按 250 下发——请求 15000 / 10000 / 400 回来的
// current_qn 全是 250，外加一条 suffix=prohdr 的壳子（current_qn 写
// 10000 但 expected_qn 仍是 250）。所以 5 次请求最后只会留下一档。
func (p *bilibiliProvider) biliFetchTiersByQN(ctx context.Context, realID int64, playurl *bilibiliLive.GetRoomPlayInfoPlayURL, out *biliPlayInfo) (*biliPlayInfo, error) {
	targets := biliAcceptQNs(playurl)
	if len(targets) == 0 {
		return out, nil
	}

	var (
		mu    sync.Mutex
		tiers = map[int64][]biliPlayURL{}
		wg    sync.WaitGroup
		sem   = make(chan struct{}, biliVariantConcurrency)
	)
	for _, qn := range targets {
		wg.Add(1)
		go func(q int64) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			urls, err := p.biliTierURLs(ctx, realID, q)
			if err != nil {
				slog.Debug("B 站单档请求失败", "qn", q, "error", err)
				return
			}
			if len(urls) == 0 {
				return
			}
			mu.Lock()
			tiers[q] = urls
			mu.Unlock()
		}(qn)
	}
	wg.Wait()

	qns := make([]int64, 0, len(tiers))
	for qn := range tiers {
		qns = append(qns, qn)
	}
	sort.Slice(qns, func(i, j int) bool { return qns[i] > qns[j] })
	for _, qn := range qns {
		out.Tiers = append(out.Tiers, biliTier{QN: qn, URLs: tiers[qn]})
	}
	return out, nil
}

// biliTierURLs 请求某一档，只收「服务端真的按这档下发」的地址。
//
// 判据是两个都要等于请求的 qn：
//   - current_qn 是这条码流标称的档位；
//   - expected_qn 是服务端准备下发的档位。
//
// 匿名会话请求 1080P 时 current_qn 会写 10000，但地址里的 expected_qn
// 是 250——实际播出来还是 720P。这种壳子必须丢掉，否则菜单写着 1080P、
// 点进去画质不动。登录之后服务端对同一档的 expected_qn 会给到请求值，
// 那一档自己就出现了，这边不需要判断登录态。
func (p *bilibiliProvider) biliTierURLs(ctx context.Context, realID, qn int64) ([]biliPlayURL, error) {
	res, err := p.biliRequest(ctx, realID, qn)
	if err != nil {
		return nil, err
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
// 打分顺序：HLS 优先，其次是 avc（系统解码器一定有，hevc 要看有没有装
// HEVC 视频扩展），最后才是节点偏好。
func biliPickPlayable(urls []biliPlayURL) (biliPlayURL, bool) {
	if len(urls) == 0 {
		return biliPlayURL{}, false
	}
	best := biliPlayURL{}
	bestScore := -1 << 30
	for _, u := range urls {
		score := 0
		if biliIsHLS(u) {
			score += 100
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
