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
		grouped[t.QN] = append(grouped[t.QN], biliPlayURL{
			URL:      t.URL,
			Host:     biliURLHost(t.URL),
			Format:   "hls",
			Protocol: "http_hls",
			Codec:    biliMasterCodec(t.URL),
			QN:       t.QN,
		})
	}

	qns := make([]int64, 0, len(grouped))
	for qn := range grouped {
		qns = append(qns, qn)
	}
	sort.Slice(qns, func(i, j int) bool { return qns[i] > qns[j] })
	for _, qn := range qns {
		out.Tiers = append(out.Tiers, biliTier{QN: qn, URLs: grouped[qn]})
	}

	// 线路取 CONTENT-STEERING 的 PATHWAY 优先级链。节点数量和族都由服务端
	// 实时决定（实测同一房间见过 4 个 204 族、2 个 208 族、单个 209），
	// 所以这里只按返回顺序收，不做任何硬编码。
	//
	// 某个 pathway 不一定覆盖所有档位（250 档在 204-4，400 档在 204-1），
	// 选流时匹配不到会退回该档的第一条，见 biliPickByLine。
	seen := make(map[string]bool, len(pathways))
	for _, pathway := range pathways {
		host := biliURLHost(pathway)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		out.Pathways = append(out.Pathways, host)
	}
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

// biliMasterCodec 从地址判断视频编码。
//
// AVC 分支能直接 -c copy，AV1 / HEVC 要转码，这个标记会一路传到 relay。
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
