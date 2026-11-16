package live

import (
	"context"
	"sort"
	"strconv"
	"strings"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// B 站 master 播放列表取流。
//
// 网页播放器不靠 getRoomPlayInfo 列档位。那个接口是「单档下发」的：
// 匿名会话无论 qn 传什么，expected_quality 恒为 250，所有 codec 的
// current_qn 也恒为 250（外加一条 suffix=prohdr 的 hdr 壳子，其
// expected_qn 仍是 250）。逐档请求 5 次，过滤后只剩一档——这是之前
// 菜单里只有 720P 的根因。
//
// 真正的入口是 play-gateway/master/url：返回一份多码率 master m3u8，
// 一次给出全部档位、官方中文名、分辨率、带宽和 CDN pathway。它不要
// cookie、不要签名，只要 Referer 和桌面 UA。
//
// 注意：接口不校验登录，但网页端前端会拦——未登录时菜单仍摆出全部
// 档位，点 720P 以上直接弹登录、不发取流请求。alive 直接走接口，
// 因此未登录也能看到平台实际开放的最高档，这是有意为之。
//
// 完整调研结论见 internal/live/README.md。
const biliMasterURL = "https://api.live.bilibili.com/xlive/play-gateway/master/url"

// biliMasterQn 是 master/url 的 qn 参数。
//
// 传什么值都返回同一份全档位列表，这里固定 250，与网页端进房时的取值一致。
const biliMasterQn = 250

// biliMasterParam 是 master/url 的请求参数。
type biliMasterParam struct {
	Cid        int64  `request:"cid"`
	Mid        int    `request:"mid"`
	Pt         string `request:"pt"`
	P2PType    int    `request:"p2p_type"`
	Net        int    `request:"net"`
	FreeType   int    `request:"free_type"`
	Build      int    `request:"build"`
	Feature    int    `request:"feature"`
	Qn         int    `request:"qn"`
	DrmType    string `request:"drm_type"`
	CamID      int    `request:"cam_id"`
	StreamName string `request:"stream_name"`
}

// biliMasterTier 是 master 播放列表里的一个码流条目。
type biliMasterTier struct {
	QN        int64
	Display   string
	Width     int
	Height    int
	Bandwidth int64
	URL       string
	Pathway   string
	// Codec 来自 CODECS 声明，比按地址猜可靠；缺失时才回退 biliMasterCodec。
	Codec     string
	FrameRate float64
}

// biliStreamName 从 playinfo 反推不带编码后缀的流名。
//
// 流名有两种形态，不能按结构猜：
//
//	live_50329118_9516950_2500     → live_50329118_9516950
//	live_57bf9_SAe1Weh_27l4b_2500  → live_57bf9_SAe1Weh_27l4b
//
// 唯一可靠的规则是「去掉末尾的 _<suffix>」——suffix 在同一条 codec 的
// url_info[].extra 里有明码。削掉之后各编码分支的流名会收敛成同一个，
// 所以优先取 suffix 为纯数字的那条（2500 / 4000 这些标准转码档），
// 避开 live_<room_id>_bs_<id> 这种另一路信号。
func biliStreamName(playurl *bilibiliLive.GetRoomPlayInfoPlayURL) string {
	if playurl == nil {
		return ""
	}
	var fallback string
	for _, stream := range playurl.Stream {
		for _, format := range stream.Format {
			for _, codec := range format.Codec {
				if len(codec.URLInfo) == 0 {
					continue
				}
				name, suffix := biliSplitStreamName(codec.BaseURL, codec.URLInfo[0].Extra)
				if name == "" {
					continue
				}
				if biliAllDigits(suffix) {
					return name
				}
				if fallback == "" {
					fallback = name
				}
			}
		}
	}
	return fallback
}

// biliSplitStreamName 从 base_url + extra 还原流名，并返回被削掉的 suffix。
func biliSplitStreamName(base, extra string) (name, suffix string) {
	base = strings.TrimSpace(base)
	if i := strings.IndexByte(base, '?'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, "/")
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".flv"), ".m3u8")
	suffix = biliQueryParam(extra, "suffix")
	if suffix != "" {
		base = strings.TrimSuffix(base, "_"+suffix)
	}
	return base, suffix
}

// biliAllDigits 报告 s 是否非空且全为数字。
func biliAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// biliQueryParam 从 query 串里取一个参数值；空串表示没有。
func biliQueryParam(query, key string) string {
	query = strings.TrimPrefix(query, "?")
	for _, part := range strings.Split(query, "&") {
		name, value, ok := strings.Cut(part, "=")
		if ok && name == key {
			return value
		}
	}
	return ""
}

// biliMasterPlayInfo 请求 master 播放列表并解析成档位表。
func (p *bilibiliProvider) biliMasterPlayInfo(ctx context.Context, realID int64, streamName string, desc map[int64]string) (*biliPlayInfo, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	body, err := biblib.ExecuteRaw(ctx, p.api, "GET", biliMasterURL, biliMasterParam{
		Cid:        realID,
		Mid:        0,
		Pt:         "web",
		P2PType:    1,
		Net:        0,
		FreeType:   0,
		Build:      0,
		Feature:    0,
		Qn:         biliMasterQn,
		DrmType:    "0,1,2,3",
		CamID:      0,
		StreamName: streamName,
	})
	if err != nil {
		return nil, p.wrap("取播放列表", err)
	}

	tiers, pathways := biliParseMaster(body)
	if len(tiers) == 0 {
		return nil, nil
	}

	out := &biliPlayInfo{QNDesc: desc, Live: true}

	grouped := make(map[int64][]biliPlayURL, 8)
	for _, t := range tiers {
		if t.QN <= 0 || t.URL == "" {
			continue
		}
		grouped[t.QN] = append(grouped[t.QN], biliPlayURLFromTier(t))
	}

	qns := make([]int64, 0, len(grouped))
	for qn := range grouped {
		qns = append(qns, qn)
	}
	sort.Slice(qns, func(i, j int) bool { return qns[i] > qns[j] })
	for _, qn := range qns {
		out.Tiers = append(out.Tiers, biliTier{QN: qn, URLs: grouped[qn]})
	}

	// 线路 = PATHWAY 优先级链，节点 ID 统一归一成主机名后去重。
	//
	// 优先级链的写法不止一种：实测有整条节点地址
	// （https://cn-hnld-ct-01-18.bilivideo.com），也有裸节点 ID（204-4,204b,204-2）。
	// 码流条目自己的 PATHWAY-ID 只用来补链里缺的节点，顺序仍以链为准。
	out.Pathways = biliPathwayList(tiers, pathways)
	return out, nil
}

// biliParseMaster 解析 master m3u8，返回码流条目与 pathway 优先级链。
func biliParseMaster(body []byte) (tiers []biliMasterTier, pathways []string) {
	lines := strings.Split(string(body), "\n")
	var pending biliMasterAttr
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "#EXT-X-CONTENT-STEERING:"):
			attr := biliParseAttrList(strings.TrimPrefix(line, "#EXT-X-CONTENT-STEERING:"))
			for _, host := range strings.Split(attr["BILI-PATHWAY-PRIORITY"], ",") {
				if host = strings.TrimSpace(host); host != "" {
					pathways = append(pathways, host)
				}
			}
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pending = biliParseAttrList(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
		case strings.HasPrefix(line, "#"):
			continue
		default:
			// 上一行不是 STREAM-INF 的裸 URL 不是码流条目，跳过。
			if pending == nil {
				continue
			}
			tier := biliMasterTier{
				URL:     line,
				Display: pending["BILI-DISPLAY"],
				Pathway: pending["PATHWAY-ID"],
			}
			tier.QN, _ = strconv.ParseInt(pending["BILI-QN"], 10, 64)
			tier.Bandwidth, _ = strconv.ParseInt(pending["BANDWIDTH"], 10, 64)
			if w, h, ok := strings.Cut(pending["RESOLUTION"], "x"); ok {
				tier.Width, _ = strconv.Atoi(w)
				tier.Height, _ = strconv.Atoi(h)
			}
			tier.Codec = biliMasterCodecFromCodecs(pending["CODECS"])
			tier.FrameRate, _ = strconv.ParseFloat(pending["FRAME-RATE"], 64)
			tiers = append(tiers, tier)
			pending = nil
		}
	}
	return tiers, pathways
}

// biliMasterAttr 是 m3u8 属性列表。
type biliMasterAttr map[string]string

// biliParseAttrList 解析 `KEY=VALUE,KEY="VALUE,WITH,COMMA"` 形式的属性列表。
//
// 不能直接按逗号 split：CODECS 的值本身带逗号且被引号包着。
func biliParseAttrList(list string) biliMasterAttr {
	out := make(biliMasterAttr, 8)
	var key, value strings.Builder
	inKey, inQuote := true, false
	flush := func() {
		if key.Len() > 0 {
			out[strings.TrimSpace(key.String())] = value.String()
		}
		key.Reset()
		value.Reset()
		inKey = true
	}
	for i := 0; i < len(list); i++ {
		switch c := list[i]; {
		case c == '"':
			inQuote = !inQuote
		case c == ',' && !inQuote:
			flush()
		case c == '=' && inKey && !inQuote:
			inKey = false
		default:
			if inKey {
				key.WriteByte(c)
			} else {
				value.WriteByte(c)
			}
		}
	}
	flush()
	return out
}

// biliPlayURLFromTier 把 master 条目转成统一的可播地址。
//
// 编码优先用 CODECS 声明：AV1 / HEVC 决定转封装能不能直接 copy，按地址
// 猜错会把不兼容的码流塞进 MPEG-TS。声明缺失时才回退地址判断。
func biliPlayURLFromTier(t biliMasterTier) biliPlayURL {
	codec := t.Codec
	if codec == "" {
		codec = biliMasterCodec(t.URL)
	}
	return biliPlayURL{
		URL:  t.URL,
		Host: biliURLHost(t.URL),
		// 线路以 STREAM-INF 的 PATHWAY-ID 为准，和线路菜单用同一套节点标识；
		// 缺失时才退回地址自己的主机名，见 biliOnLine。
		Pathway:   biliPathwayID(t.Pathway),
		Format:    "hls",
		Protocol:  "http_hls",
		Codec:     codec,
		QN:        t.QN,
		Width:     t.Width,
		Height:    t.Height,
		Bitrate:   t.Bandwidth,
		FrameRate: t.FrameRate,
	}
}

// biliMasterCodecFromCodecs 从 EXT-X-STREAM-INF 的 CODECS 属性判断视频编码。
//
// CODECS 里音频（mp4a）可能排在前面，只认第一个能识别的视频编码；全都认不
// 出来时返回空串，交给 biliMasterCodec 按地址兜底。
func biliMasterCodecFromCodecs(codecs string) string {
	for _, raw := range strings.Split(codecs, ",") {
		name := strings.ToLower(strings.TrimSpace(raw))
		switch {
		case strings.HasPrefix(name, "avc1"), strings.HasPrefix(name, "avc3"):
			return "avc"
		case strings.HasPrefix(name, "hvc1"), strings.HasPrefix(name, "hev1"),
			strings.HasPrefix(name, "dvh1"), strings.HasPrefix(name, "dvhe"):
			// 这些杜比视界标识使用 HEVC，按 HEVC 归类。
			return "hevc"
		case strings.HasPrefix(name, "av01"):
			return "av1"
		}
	}
	return ""
}

// biliMasterCodec 从地址判断视频编码。
//
// AVC 分支能直接 -c copy，AV1 / HEVC 要转码，这个标记会一路传到播放层。
func biliMasterCodec(url string) string {
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, "av1"):
		return "av1"
	case strings.Contains(lower, "hevc"):
		return "hevc"
	default:
		return "avc"
	}
}

// biliURLHost 从完整地址里取主机名。
func biliURLHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

// biliPathwayID 把线路引用归一成节点主机名。
//
// 同一个节点在服务端有两种写法：整条地址（https://d1--cn-gotcha204b.bilivideo.com）
// 和裸节点 ID（204-4）。整条地址只取主机名；裸 ID 没有可剥的部分，原样返回，
// 匹配时再由 biliPathwayMatch 拿主机名里的短标识去对。
func biliPathwayID(raw string) string {
	return strings.ToLower(biliURLHost(strings.TrimSpace(raw)))
}

// biliPathwayToken 取节点主机名里 gotcha 之后的短标识（204-4 / 204b / 209）。
//
// 这是裸节点 ID 的唯一来源：链里写 204-4 时，主机名是
// d1--cn-gotcha204-4.bilivideo.com。名字里没有 gotcha 的节点返回空串，
// 这类节点只能按完整主机名匹配。
func biliPathwayToken(host string) string {
	const marker = "gotcha"
	host = strings.ToLower(strings.TrimSpace(host))
	i := strings.Index(host, marker)
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(host[i+len(marker):], ".bilivideo.com")
}

// biliPathwayMatch 报告节点主机名 host 是否属于用户选中的线路 want。
//
// want 可能是主机名、整条地址或裸节点 ID，三种都得认——少认一种，
// 菜单里选中的线路就会静默失效。
func biliPathwayMatch(host, want string) bool {
	// 两边都归一成节点主机名：调用方可能给主机名、整条地址或裸节点 ID。
	host = biliPathwayID(host)
	want = biliPathwayID(want)
	if host == "" || want == "" {
		return false
	}
	if host == want {
		return true
	}
	// 同一个节点的两种写法：一侧是裸节点 ID，另一侧是带 gotcha 的主机名。
	// 两侧都要能当裸 ID 用，否则「哪一侧写裸 ID」会决定匹配成败。
	hostNode := biliPathwayToken(host)
	if hostNode == "" && !strings.Contains(host, ".") {
		hostNode = host
	}
	wantNode := biliPathwayToken(want)
	if wantNode == "" && !strings.Contains(want, ".") {
		wantNode = want
	}
	return hostNode != "" && hostNode == wantNode
}

// biliPathwayList 生成线路菜单的 ID 列表。
//
// 先按优先级链的顺序收；链里有、但没有码流落上去的节点也留着——选中这类线路时
// 该档没有对应候选，会退回该档的第一条地址，见 biliPickByLine。
// 链里没有、码流条目自己声明了的节点接在后面，这样优先级链缺失时菜单也不会整块没有线路。
func biliPathwayList(tiers []biliMasterTier, priority []string) []string {
	seen := make(map[string]bool, len(priority)+4)
	tokens := make(map[string]bool, len(priority)+4)
	out := make([]string, 0, len(priority)+4)
	add := func(raw string) {
		id := biliPathwayID(raw)
		if id == "" || seen[id] {
			return
		}
		// 链和码流条目可能用两种写法指同一个节点（204-4 与 d1--cn-gotcha204-4...），
		// 同一个节点的两种写法只摆一条线路。
		token := biliPathwayToken(id)
		if token == "" && !strings.Contains(id, ".") {
			// 裸节点 ID 自己就是短标识：链给 204-4、码流条目给完整地址时，
			// 两者指的是同一台节点，只能算一条线路。
			token = id
		}
		if token != "" && tokens[token] {
			return
		}
		seen[id] = true
		if token != "" {
			tokens[token] = true
		}
		out = append(out, id)
	}
	for _, item := range priority {
		add(item)
	}
	for _, t := range tiers {
		if t.QN <= 0 || t.URL == "" {
			continue
		}
		if t.Pathway != "" {
			add(t.Pathway)
			continue
		}
		add(t.URL)
	}
	return out
}
