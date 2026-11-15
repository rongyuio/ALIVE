package live

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 斗鱼取流。
//
// 流程与 baseline 对齐（同一套 pure_live 结论）：
//   - websec/getEncryption 拿加密描述符，缓存 5 分钟，失败强制刷新重试一次；
//   - 用 MD5 链算 auth，POST getH5PlayV1，表单里必须带
//     enc_data / tt / did / auth / cdn / rate / hevc / fa / ive / ver / iar；
//   - 每个进程一个随机 did，Cookie 里同时发 dy_did / acf_did；
//   - multirates 保持平台顺序，rate 是「请求码」不是码率，不能按数值排序；
//   - 每个档位按 CDN 逐个解析，按服务端确认的实际 rate 分组，
//     避免把不同实际档位的地址混进同一个选项；
//   - 只有 ws CDN 的直链会带 expire=300，补 expire=0 让 CDN 按默认时长签发。
const (
	// did 走 query，服务端按它识别游客。
	douyuEncryptionURL = "https://www.douyu.com/wgapi/livenc/liveweb/websec/getEncryption?did=%s"
	// 房间号走路径。
	douyuH5PlayURL = "https://www.douyu.com/lapi/live/getH5PlayV1/%s"
	// 不指定线路时用它，这是网页端默认的 ws-h5 节点。
	douyuDefaultCDN = "ws-h5"

	douyuEncCacheMax = 5 * time.Minute
	// 距到期不足 30 秒就重新取，避免请求途中描述符过期。
	douyuEncGuard = 30 * time.Second
)

// douyuEncryption 是缓存的签名材料。
type douyuEncryption struct {
	did       string
	key       string
	randStr   string
	encData   string
	encTime   int
	isSpecial int
	expiryAt  int64
}

// douyuSigner 缓存加密描述符。
//
// 描述符和 did 绑定、5 分钟内可复用。不做缓存的话，每个清晰度、
// 每个 CDN 都要多打一次 getEncryption，既慢又容易被风控盯上。
type douyuSigner struct {
	mu      sync.Mutex
	enc     *douyuEncryption
	fetched time.Time
	did     string
}

// douyuPlayData 是 getH5PlayV1 的 data 对象。
//
// 数值字段用 any：同一个字段在不同房间会以数字或字符串下发。
type douyuPlayData struct {
	RoomID    any    `json:"room_id"`
	RtmpURL   string `json:"rtmp_url"`
	RtmpLive  string `json:"rtmp_live"`
	RtmpCDN   string `json:"rtmp_cdn"`
	FlvURL    any    `json:"flv_url"`
	Player1   any    `json:"player_1"`
	StreamURL any    `json:"stream_url"`
	URL       any    `json:"url"`
	Rate      any    `json:"rate"`

	CdnsWithName []struct {
		Name string `json:"name"`
		CDN  string `json:"cdn"`
	} `json:"cdnsWithName"`

	Multirates []struct {
		Name    string `json:"name"`
		Rate    any    `json:"rate"`
		Bit     any    `json:"bit"`
		HighBit any    `json:"highBit"`
	} `json:"multirates"`
}

type douyuPlayResponse struct {
	Error int            `json:"error"`
	Code  int            `json:"code"`
	Msg   string         `json:"msg"`
	Data  *douyuPlayData `json:"data"`
}

// douyuRate 是一个清晰度档位（斗鱼叫 rate）。
type douyuRate struct {
	Name string
	Rate int
	Bit  int
}

// douyuQualityURLs 是一个档位的解析结果。
type douyuQualityURLs struct {
	URLs        []string
	AppliedRate *int
	Unconfirmed bool
}

// Stream 返回房间详情和全部清晰度 / 线路。
func (p *douyuProvider) Stream(ctx context.Context, roomID, line string) (Stream, error) {
	// 房间详情复用 RoomInfo：它已经把 show_status / 轮播都判好了，
	// 播放页和卡片上看到的也就是同一份结论。
	room, err := p.RoomInfo(ctx, roomID)
	if err != nil {
		return Stream{}, err
	}
	out := Stream{
		RoomID:   room.RoomID,
		Platform: Douyu,
		Title:    room.Title,
		Nickname: room.Nickname,
		Avatar:   room.Avatar,
		Live:     room.Live,
	}
	if !room.Live {
		return out, nil
	}

	rates, cdns, err := p.douyuPlayQualities(ctx, room.RoomID)
	if err != nil {
		return Stream{}, err
	}
	out.Lines = cdns
	resolveOrder := douyuOrderCDNs(cdns, line)

	type qualityResult struct {
		variant Variant
		err     error
	}
	results := make([]qualityResult, len(rates))

	var wg sync.WaitGroup
	for i, rate := range rates {
		wg.Add(1)
		go func(idx int, requested douyuRate) {
			defer wg.Done()
			urls, applied, unconfirmed, err := p.douyuResolveQuality(ctx, room.RoomID, requested, resolveOrder)
			if err != nil {
				results[idx] = qualityResult{err: err}
				return
			}
			if len(urls) == 0 {
				results[idx] = qualityResult{err: fmt.Errorf("清晰度 %s 没有可用地址", douyuRateLabel(requested))}
				return
			}
			// 服务端回填的 rate 才是实际下发的档位：请求档没被接受时，
			// 地址来自另一个档，菜单不能继续沿用请求档的名字。
			actual := requested
			if applied != nil && *applied >= 0 {
				actual = douyuRateByCode(rates, *applied)
			}
			variant := douyuNewVariant(room.RoomID, actual, urls[0])
			if unconfirmed {
				// 没有 rate 回填就无法确认实际档位，不能拿请求档（可能是最高档）
				// 冒充已确认结果；只保留地址，标签退到「默认」。
				variant.ID = ""
				variant.Label = "默认"
			}
			results[idx] = qualityResult{variant: variant}
		}(i, rate)
	}
	wg.Wait()

	var firstErr error
	confirmed := make(map[string]bool, len(rates))
	unconfirmed := make([]Variant, 0, len(rates))
	seenURL := make(map[string]bool, len(rates))
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		if r.variant.ID == "" {
			if seenURL[r.variant.URL] {
				continue
			}
			seenURL[r.variant.URL] = true
			unconfirmed = append(unconfirmed, r.variant)
			continue
		}
		// 多个请求档可能落到同一个实际档位，只保留先出现的一条。
		if confirmed[r.variant.ID] {
			continue
		}
		confirmed[r.variant.ID] = true
		out.Variants = append(out.Variants, r.variant)
	}
	if len(out.Variants) == 0 {
		// 一个确认档都没有时才用未确认地址兜底。
		out.Variants = append(out.Variants, unconfirmed...)
	}
	if len(out.Variants) == 0 {
		if firstErr != nil {
			return Stream{}, firstErr
		}
		return Stream{}, fmt.Errorf("房间 %s 没有可用的直播流", room.RoomID)
	}
	return out, nil
}

// StreamForPlayback 只解析当前档位的首选线路，供播放页按需起播。
//
// 斗鱼每个「档位 × 线路」都要单独请求一次签名接口，全部解析会让起播白等
// 好几秒；这里先请求选中线路，服务端确认的就是这一档时立即返回，不行再
// 按顺序回退到下一条线路。其余档位只返回目录，URL 留空。
func (p *douyuProvider) StreamForPlayback(ctx context.Context, roomID, line, quality string) (Stream, Variant, error) {
	room, err := p.RoomInfo(ctx, roomID)
	if err != nil {
		return Stream{}, Variant{}, err
	}
	out := Stream{
		RoomID:   room.RoomID,
		Platform: Douyu,
		Title:    room.Title,
		Nickname: room.Nickname,
		Avatar:   room.Avatar,
		Live:     room.Live,
	}
	if !room.Live {
		return out, Variant{}, nil
	}

	rates, cdns, err := p.douyuPlayQualities(ctx, room.RoomID)
	if err != nil {
		return Stream{}, Variant{}, err
	}
	out.Lines = cdns
	for _, rate := range rates {
		// 目录保持平台下发的完整档位，地址等真正选中时再解析。
		out.Variants = append(out.Variants, douyuNewVariant(room.RoomID, rate, ""))
	}

	requested := douyuPickRate(rates, quality)
	resolveOrder := douyuOrderCDNs(cdns, line)

	var (
		lastErr     error
		fallback    *Variant
		unconfirmed *Variant
	)
	for _, cdn := range resolveOrder {
		data, err := p.douyuPlayData(ctx, room.RoomID, requested.Rate, cdn.ID)
		if err != nil {
			lastErr = err
			continue
		}
		playURL, err := douyuParsePlayURL(data)
		if err != nil {
			lastErr = err
			continue
		}
		playURL = douyuExpirePatch(playURL)
		// 斗鱼请求固定 hevc=0，拿到的是 H.264，转封装可以直接 copy。
		headers := douyuStreamHeaders(room.RoomID)

		applied, known := douyuIntOK(data.Rate)
		if !known || applied < 0 {
			// 服务端没给实际档位：地址可用，但不能冒充请求的档位。
			if unconfirmed == nil {
				v := Variant{URL: playURL, Headers: headers, Codec: "avc"}
				unconfirmed = &v
			}
			continue
		}
		variant := douyuNewVariant(room.RoomID, douyuRateByCode(rates, applied), playURL)
		if applied == requested.Rate {
			info, selected := douyuFillSelected(out, variant)
			return info, selected, nil
		}
		if fallback == nil {
			fallback = &variant
		}
	}
	if fallback != nil {
		info, selected := douyuFillSelected(out, *fallback)
		return info, selected, nil
	}
	if unconfirmed != nil {
		// 未确认地址没有可用档位信息，不塞进目录，只交给播放器。
		return out, *unconfirmed, nil
	}
	if lastErr != nil {
		return Stream{}, Variant{}, lastErr
	}
	return Stream{}, Variant{}, fmt.Errorf("房间 %s 没有可用的直播流", room.RoomID)
}

// douyuPickRate 按菜单标签选档；空值或没命中时用平台默认档（下发顺序第一项）。
func douyuPickRate(rates []douyuRate, quality string) douyuRate {
	want := strings.TrimSpace(quality)
	if want != "" {
		for _, rate := range rates {
			if strings.EqualFold(douyuRateLabel(rate), want) {
				return rate
			}
		}
		// 菜单里的档位理论上都来自同一份列表；走到这里说明请求的标签
		// 和服务端当前档位对不上了（多半是菜单过期）。记录下来，
		// 上层还会用“点选 vs 实际”的对比给用户提示。
		slog.Info("斗鱼请求的清晰度不在当前档位表里，改用默认档", "requested", want, "fallback", douyuRateLabel(rates[0]))
	}
	if len(rates) > 0 {
		return rates[0]
	}
	return douyuRate{Name: "默认", Rate: -1}
}

// douyuRateByCode 在平台下发的档位表里找实际 rate 对应的名字。
// 找不到时只带 code，标签用「rate N」，绝不借用请求档的名字。
func douyuRateByCode(rates []douyuRate, code int) douyuRate {
	for _, rate := range rates {
		if rate.Rate == code {
			return rate
		}
	}
	return douyuRate{Name: "rate " + strconv.Itoa(code), Rate: code}
}

// douyuNewVariant 按实际档位生成一个选项。
func douyuNewVariant(roomID string, rate douyuRate, playURL string) Variant {
	return Variant{
		ID:    strconv.Itoa(rate.Rate),
		Label: douyuRateLabel(rate),
		URL:   playURL,
		// 斗鱼的 FLV/HLS 都校验 Referer，不带给 ffmpeg 就是 403
		Headers: douyuStreamHeaders(roomID),
		Codec:   "avc",
	}
}

// douyuFillSelected 把确认到的地址填回档位目录，一起返回。
func douyuFillSelected(info Stream, selected Variant) (Stream, Variant) {
	for i := range info.Variants {
		if info.Variants[i].ID == selected.ID {
			info.Variants[i].URL = selected.URL
			return info, info.Variants[i]
		}
	}
	info.Variants = append(info.Variants, selected)
	return info, selected
}

// douyuStreamHeaders 是回源要带的头。ffmpeg 通过 -headers 拿到它们。
func douyuStreamHeaders(roomID string) []string {
	return []string{
		"Referer: https://www.douyu.com/" + roomID,
		"User-Agent: " + desktopUA,
	}
}

// ---------------------------------------------------------------- 清晰度 / 线路

func (p *douyuProvider) douyuPlayQualities(ctx context.Context, roomID string) ([]douyuRate, []Line, error) {
	// rate=-1 / cdn="" 就是「把全部档位和线路都列出来」
	data, err := p.douyuPlayData(ctx, roomID, -1, "")
	if err != nil {
		return nil, nil, err
	}
	return douyuParseRates(data), douyuParseCDNs(data), nil
}

// douyuParseRates 保持斗鱼下发顺序。
//
// rate 是请求码不是码率：原画常见 0、蓝光4M 为 4、超清 3、高清 2，
// 按数值排序会把顺序弄乱。
func douyuParseRates(data *douyuPlayData) []douyuRate {
	rates := make([]douyuRate, 0, len(data.Multirates))
	seen := make(map[int]bool, len(data.Multirates))
	for _, item := range data.Multirates {
		rate, ok := douyuIntOK(item.Rate)
		if !ok || seen[rate] {
			continue
		}
		seen[rate] = true
		bit, _ := douyuIntOK(item.Bit)
		rates = append(rates, douyuRate{Name: strings.TrimSpace(item.Name), Rate: rate, Bit: bit})
	}
	if len(rates) == 0 {
		rate, ok := douyuIntOK(data.Rate)
		if !ok {
			rate = -1
		}
		rates = append(rates, douyuRate{Name: "默认", Rate: rate})
	}
	return rates
}

// douyuParseCDNs 整理线路：
//   - rtmp_cdn 若不在 cdnsWithName 里，插到最前面；
//   - scdn* 排到最后，其余保持平台顺序；
//   - 一条都没有时留一个空 id，让服务端自己选默认线路。
func douyuParseCDNs(data *douyuPlayData) []Line {
	out := make([]Line, 0, len(data.CdnsWithName)+1)
	seen := make(map[string]bool, len(data.CdnsWithName)+1)

	add := func(cdn, label string) {
		cdn = strings.TrimSpace(cdn)
		if cdn == "" || seen[cdn] {
			return
		}
		seen[cdn] = true
		label = strings.TrimSpace(label)
		if label == "" {
			label = cdn
		}
		out = append(out, Line{ID: cdn, Label: label})
	}

	for _, item := range data.CdnsWithName {
		add(item.CDN, item.Name)
	}
	if current := strings.TrimSpace(data.RtmpCDN); current != "" && !seen[current] {
		out = append([]Line{{ID: current, Label: current}}, out...)
	}
	if len(out) == 0 {
		out = append(out, Line{ID: "", Label: "默认"})
	}

	sort.SliceStable(out, func(i, j int) bool {
		iScdn := strings.HasPrefix(strings.ToLower(out[i].ID), "scdn")
		jScdn := strings.HasPrefix(strings.ToLower(out[j].ID), "scdn")
		return !iScdn && jScdn
	})
	return out
}

// douyuOrderCDNs 把用户选中的线路挪到最前；只影响解析顺序，
// 界面上展示的线路列表仍保持平台顺序。
func douyuOrderCDNs(cdns []Line, line string) []Line {
	wanted := strings.ToLower(strings.TrimSpace(line))
	if wanted == "" {
		return cdns
	}
	index := -1
	for i, item := range cdns {
		if strings.ToLower(item.ID) == wanted || strings.ToLower(item.Label) == wanted {
			index = i
			break
		}
	}
	if index <= 0 {
		return cdns
	}
	ordered := make([]Line, 0, len(cdns))
	ordered = append(ordered, cdns[index])
	ordered = append(ordered, cdns[:index]...)
	ordered = append(ordered, cdns[index+1:]...)
	return ordered
}

// douyuResolveQuality 解析一个档位：逐 CDN 请求，按服务端确认的实际 rate 分组。
//
// 优先要请求的档位组；否则取第一个有确认的组；全都未确认时用未知组。
func (p *douyuProvider) douyuResolveQuality(ctx context.Context, roomID string, requested douyuRate, cdns []Line) ([]string, *int, bool, error) {
	type group struct {
		rate  int
		known bool
		urls  []string
	}
	groups := make([]group, 0, len(cdns))
	index := make(map[int]int, len(cdns))
	var lastErr error

	for _, cdn := range cdns {
		data, err := p.douyuPlayData(ctx, roomID, requested.Rate, cdn.ID)
		if err != nil {
			lastErr = err
			continue
		}
		playURL, err := douyuParsePlayURL(data)
		if err != nil {
			lastErr = err
			continue
		}
		playURL = douyuExpirePatch(playURL)

		key, known := -1, false
		if applied, ok := douyuIntOK(data.Rate); ok && applied >= 0 {
			key, known = applied, true
		}
		idx, ok := index[key]
		if !ok {
			idx = len(groups)
			index[key] = idx
			groups = append(groups, group{rate: key})
		}
		if known {
			groups[idx].known = true
		}
		if !douyuContains(groups[idx].urls, playURL) {
			groups[idx].urls = append(groups[idx].urls, playURL)
		}
	}

	if len(groups) == 0 {
		if lastErr != nil {
			return nil, nil, true, lastErr
		}
		return nil, nil, true, fmt.Errorf("斗鱼没有返回可用的直播流")
	}
	if requested.Rate >= 0 {
		if idx, ok := index[requested.Rate]; ok {
			rate := groups[idx].rate
			return groups[idx].urls, &rate, false, nil
		}
	}
	for _, item := range groups {
		if item.known {
			rate := item.rate
			return item.urls, &rate, false, nil
		}
	}
	return groups[0].urls, nil, true, nil
}

// ---------------------------------------------------------------- 单次取流

// douyuPlayData 发起一次 getH5PlayV1；失败时强制刷新签名描述符重试一次。
func (p *douyuProvider) douyuPlayData(ctx context.Context, roomID string, rate int, cdn string) (*douyuPlayData, error) {
	if strings.TrimSpace(roomID) == "" {
		return nil, fmt.Errorf("live: 斗鱼房间号为空")
	}

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		form, did, err := p.douyuSign(ctx, roomID, rate, cdn, attempt > 0)
		if err != nil {
			lastErr = err
			continue
		}

		release, err := p.lim.acquire(ctx)
		if err != nil {
			return nil, err
		}
		body, err := p.api.postForm(ctx, "取流", fmt.Sprintf(douyuH5PlayURL, roomID),
			strings.NewReader(form), p.douyuHeaders(roomID, did))
		release()
		if err != nil {
			lastErr = err
			continue
		}

		var payload douyuPlayResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			lastErr = fmt.Errorf("解析取流响应: %w", err)
			continue
		}
		code := payload.Error
		if code == 0 {
			code = payload.Code
		}
		if code != 0 {
			// 业务错误里最常见的是「描述符过期」，重试一次能自愈
			lastErr = fmt.Errorf("斗鱼取流返回 error=%d msg=%s（rate=%d cdn=%s）", code, payload.Msg, rate, cdn)
			continue
		}
		if payload.Data == nil {
			lastErr = fmt.Errorf("斗鱼取流没有返回 data（rate=%d cdn=%s）", rate, cdn)
			continue
		}
		return payload.Data, nil
	}
	return nil, lastErr
}

// douyuSign 生成 getH5PlayV1 的表单。
//
// 字段值与 baseline 逐一对应；Go 的 url.Values 会把 key 排序，
// 而斗鱼只校验字段值，不校验顺序。
func (p *douyuProvider) douyuSign(ctx context.Context, roomID string, rate int, cdn string, force bool) (string, string, error) {
	enc, err := p.douyuEncryption(ctx, force)
	if err != nil {
		return "", "", err
	}

	ts := time.Now().Unix()
	suffix := ""
	if enc.isSpecial != 1 {
		// 非特殊房间的 auth 要把房间号和秒级时间戳拼进后缀
		suffix = roomID + strconv.FormatInt(ts, 10)
	}
	secret := enc.randStr
	for i := 0; i < enc.encTime; i++ {
		secret = douyuMD5(secret + enc.key)
	}

	form := url.Values{}
	form.Set("enc_data", enc.encData)
	form.Set("tt", strconv.FormatInt(ts, 10))
	form.Set("did", enc.did)
	form.Set("auth", douyuMD5(secret+enc.key+suffix))
	form.Set("cdn", cdn)
	form.Set("rate", strconv.Itoa(rate))
	form.Set("hevc", "0")
	form.Set("fa", "0")
	form.Set("ive", "0")
	form.Set("ver", "Douyu_new")
	form.Set("iar", "0")
	return form.Encode(), enc.did, nil
}

// douyuEncryption 取/复用加密描述符。
func (p *douyuProvider) douyuEncryption(ctx context.Context, force bool) (*douyuEncryption, error) {
	s := p.signer
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	did := p.did
	if !force && s.enc != nil && s.did == did &&
		now.Sub(s.fetched) < douyuEncCacheMax &&
		s.enc.expiryAt > now.Unix()+int64(douyuEncGuard/time.Second) &&
		s.enc.encTime >= 1 && s.enc.encTime <= 16 &&
		s.enc.key != "" && s.enc.randStr != "" && s.enc.encData != "" {
		return s.enc, nil
	}

	release, err := p.lim.acquire(ctx)
	if err != nil {
		return nil, err
	}
	body, err := p.api.get(ctx, "取流签名", fmt.Sprintf(douyuEncryptionURL, did), p.douyuHeaders("", did))
	release()
	if err != nil {
		return nil, err
	}

	var payload struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  *struct {
			Key       string `json:"key"`
			RandStr   string `json:"rand_str"`
			EncTime   int    `json:"enc_time"`
			EncData   string `json:"enc_data"`
			IsSpecial int    `json:"is_special"`
			ExpireAt  int64  `json:"expire_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("解析取流签名: %w", err)
	}
	if payload.Error != 0 {
		return nil, fmt.Errorf("斗鱼签名接口返回 error=%d msg=%s", payload.Error, payload.Msg)
	}
	d := payload.Data
	if d == nil || d.Key == "" || d.RandStr == "" || d.EncData == "" {
		return nil, fmt.Errorf("斗鱼签名接口没有返回有效参数")
	}
	if d.EncTime < 1 || d.EncTime > 16 {
		return nil, fmt.Errorf("斗鱼签名接口 enc_time 非法: %d", d.EncTime)
	}
	if d.ExpireAt <= now.Unix()+int64(douyuEncGuard/time.Second) {
		return nil, fmt.Errorf("斗鱼签名描述符已过期")
	}

	s.enc = &douyuEncryption{
		did:       did,
		key:       d.Key,
		randStr:   d.RandStr,
		encData:   d.EncData,
		encTime:   d.EncTime,
		isSpecial: d.IsSpecial,
		expiryAt:  d.ExpireAt,
	}
	s.fetched = now
	s.did = did
	return s.enc, nil
}

// douyuHeaders 是取流相关请求的头。
//
// Cookie 里 did 必须一直在（dy_did / acf_did 两个都发，少一个会被判成
// 异常客户端）；登录之后再叠上登录字段，清晰度才按账号给。
func (p *douyuProvider) douyuHeaders(roomID, did string) header {
	referer := "https://www.douyu.com/"
	if roomID != "" {
		referer = "https://www.douyu.com/" + roomID
	}
	return header{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.7",
		"Origin":          "https://www.douyu.com",
		"Referer":         referer,
		"Cookie":          p.douyuCookieHeader(did),
	}
}

// ---------------------------------------------------------------- 地址清洗

func douyuParsePlayURL(data *douyuPlayData) (string, error) {
	live := strings.TrimSpace(html.UnescapeString(data.RtmpLive))
	if douyuIsPlayable(live) {
		return live, nil
	}
	for _, base := range []string{
		strings.TrimSpace(html.UnescapeString(data.RtmpURL)),
		strings.TrimSpace(html.UnescapeString(douyuAnyString(data.FlvURL))),
	} {
		if base == "" || live == "" {
			continue
		}
		combined := strings.TrimRight(base, "/") + "/" + strings.TrimLeft(live, "/")
		if douyuIsPlayable(combined) {
			return combined, nil
		}
	}
	for _, candidate := range []string{
		douyuAnyString(data.Player1),
		douyuAnyString(data.StreamURL),
		douyuAnyString(data.URL),
	} {
		value := strings.TrimSpace(html.UnescapeString(candidate))
		if douyuIsPlayable(value) {
			return value, nil
		}
	}
	if flv := strings.TrimSpace(html.UnescapeString(douyuAnyString(data.FlvURL))); douyuIsDirectMedia(flv) {
		return flv, nil
	}
	return "", fmt.Errorf("斗鱼取流响应里没有可播放地址")
}

func douyuIsPlayable(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "rtmp":
		return true
	default:
		return false
	}
}

func douyuIsDirectMedia(value string) bool {
	if !douyuIsPlayable(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	return strings.HasSuffix(path, ".flv") || strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".mp4")
}

// douyuExpirePatch 是续期补丁：ws CDN 的直链只签 300 秒，
// 补 expire=0 让 CDN 按默认时长重新签发，避免播五分钟就断。
func douyuExpirePatch(rawURL string) string {
	if strings.Contains(rawURL, "expire=300") && strings.Contains(rawURL, "fcdn=ws") {
		return rawURL + "&expire=0"
	}
	return rawURL
}

// ---------------------------------------------------------------- 小工具

func douyuMD5(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func douyuRateLabel(rate douyuRate) string {
	name := strings.TrimSpace(rate.Name)
	if name == "" || strings.EqualFold(name, "default") {
		return "默认"
	}
	return name
}

// douyuIntOK 把 JSON 里的数字/字符串统一取成 int，并报告字段是否真的存在。
//
// 不能用 douyuInteger：它把「字段缺失」也返回 0，
// 而这里 0 是合法档位（原画的 rate 就是 0），必须区分。
func douyuIntOK(v any) (int, bool) {
	switch x := v.(type) {
	case nil:
		return 0, false
	case int:
		return x, true
	case int64:
		return int(x), true
	case float64:
		return int(x), true
	case json.Number:
		n, err := x.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// douyuAnyString 把 JSON 里的 string/number 统一取成字符串。
func douyuAnyString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	}
	return ""
}

func douyuContains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

// 编译期确认斗鱼实现了取流接口。
var (
	_ Streamer         = (*douyuProvider)(nil)
	_ PlaybackStreamer = (*douyuProvider)(nil)
)
