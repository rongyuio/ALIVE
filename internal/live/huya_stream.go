package live

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"alive/internal/diag"
)

// 虎牙取流。
//
// 链路与参考实现一致，分两跳：
//  1. profileRoom（带 showSecret=1）拿到房间信息与播放候选（线路 + 流名 + 频道 id）；
//  2. WUP(RPC) 的 getCdnTokenInfoEx 换 token，再用 token 重算 anti-code 拼出地址。
//
// 三个档位共用同一条流：虎牙的清晰度只是同一地址加不同的 &ratio=码率。
// 所以 token 与 anti-code 只算一次，档位之间只是 ratio 不同。
const (
	huyaStreamProfileURL = "https://mp.huya.com/cache.php?m=Live&do=profileRoom&roomid=%s&showSecret=1"

	// WUP 网关。参考实现用的就是 http；换 https 没有实测依据，不擅自改。
	huyaWupURL     = "http://wup.huya.com"
	huyaWupServant = "liveui"
	huyaWupFunc    = "getCdnTokenInfoEx"

	// 这套客户端标识是照抄参考实现的，虎牙对 UA 与来源页敏感。
	huyaWupUA       = "HYSDK(Windows,30000002)_APP(pc_exe&7080000&official)_SDK(trans&2.34.0.5795)"
	huyaWupOrigin   = "https://m.huya.com/"
	huyaWupReferer  = "https://m.huya.com/"
	huyaWupClientUA = "pc_exe&7060000&official"
	huyaWupAppID    = 66
	huyaWupMaxBody  = 4096

	// 回源播放必须带 Referer 与桌面 UA，缺了虎牙会回 403。
	huyaStreamReferer = "https://www.huya.com/"

	// 码率缺失时的兜底值，与参考实现一致。
	huyaBitrateFallbackHD = 4000
	huyaBitrateFallbackSD = 2000
)

// huyaStreamProfile 只声明取流用到的字段。
type huyaStreamProfile struct {
	LiveData struct {
		Introduction string `json:"introduction"`
		RoomName     string `json:"roomName"`
		Nick         string `json:"nick"`
		Avatar180    string `json:"avatar180"`
		BitRateInfo  string `json:"bitRateInfo"`
	} `json:"liveData"`
	ProfileInfo struct {
		Nick      string `json:"nick"`
		Avatar180 string `json:"avatar180"`
	} `json:"profileInfo"`
	Stream *struct {
		BaseStreamInfoList []huyaStreamCandidate `json:"baseSteamInfoList"`
		Flv                struct {
			RateArray []struct {
				BitRate int32 `json:"iBitRate"`
			} `json:"rateArray"`
		} `json:"flv"`
	} `json:"stream"`
}

// huyaStreamCandidate 是一条播放候选（字段名拼写错误是虎牙自己的）。
type huyaStreamCandidate struct {
	CdnType    string      `json:"sCdnType"`
	FlvURL     string      `json:"sFlvUrl"`
	StreamName string      `json:"sStreamName"`
	ChannelID  huyaFlexInt `json:"lChannelId"`
}

// huyaFlexInt 兼容虎牙返回的 2.367547387e+09 这类浮点大整数。
type huyaFlexInt int64

func (f *huyaFlexInt) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "null" || text == "" {
		*f = 0
		return nil
	}
	text = strings.Trim(text, `"`)
	if v, err := strconv.ParseInt(text, 10, 64); err == nil {
		*f = huyaFlexInt(v)
		return nil
	}
	// 浮点形式：float64 的尾数足够覆盖虎牙的 channelId（< 2^53）
	if v, err := strconv.ParseFloat(text, 64); err == nil {
		*f = huyaFlexInt(int64(v))
		return nil
	}
	*f = 0
	return nil
}

// huyaVariant 是一个清晰度档位。
type huyaVariant struct {
	ID    string
	Label string
	// Rate 为 0 表示原画：不带 ratio 参数。
	Rate int32
}

// Stream 返回房间详情和全部可播档位。
func (p *huyaProvider) Stream(ctx context.Context, roomID, line string) (Stream, error) {
	id, err := normalizeHuyaRoomID(roomID)
	if err != nil {
		return Stream{}, err
	}

	profile, err := p.huyaFetchStreamProfile(ctx, id)
	if err != nil {
		return Stream{}, err
	}

	out := Stream{
		RoomID:   id,
		Platform: Huya,
		Title:    firstNonEmpty(profile.LiveData.Introduction, profile.LiveData.RoomName),
		Nickname: firstNonEmpty(profile.ProfileInfo.Nick, profile.LiveData.Nick),
		Avatar: httpsURL(firstNonEmpty(
			profile.ProfileInfo.Avatar180,
			profile.LiveData.Avatar180,
		)),
	}
	if strings.TrimSpace(out.Nickname) == "" {
		out.Nickname = "房间 " + id
	}

	candidates := huyaStreamCandidates(profile)
	out.Live = len(candidates) > 0
	for _, candidate := range candidates {
		out.Lines = append(out.Lines, Line{
			ID:    strings.ToLower(candidate.CdnType),
			Label: strings.ToUpper(candidate.CdnType),
		})
	}
	if !out.Live {
		return out, nil
	}

	selected := huyaSelectCandidate(candidates, line)

	// token 与清晰度无关，取一次给所有档位复用：多打几次 WUP 只会更容易被限流。
	token, err := p.huyaCdnToken(ctx, selected.FlvURL, selected.StreamName)
	if err != nil {
		return Stream{}, p.wrap("取流鉴权", err)
	}
	anti, err := huyaBuildAntiCode(selected.StreamName, int64(selected.ChannelID), token)
	if err != nil {
		return Stream{}, p.wrap("取流鉴权", err)
	}

	base := huyaStreamBaseURL(selected, anti)
	// 回源要带 Referer/UA，播放器直连时缺了会被 403。
	headers := []string{"Referer: " + huyaStreamReferer, "User-Agent: " + desktopUA}
	for _, variant := range huyaStreamVariants(profile) {
		playURL := base
		if variant.Rate > 0 {
			playURL += "&ratio=" + strconv.FormatInt(int64(variant.Rate), 10)
		}
		out.Variants = append(out.Variants, Variant{
			ID:      variant.ID,
			Label:   variant.Label,
			URL:     playURL,
			Headers: headers,
			// 地址固定带 codec=264，转封装可以直接 copy。
			Codec:   "avc",
			Expires: expiryFromQuery(playURL, "wsTime"),
		})
	}
	return out, nil
}

// huyaFetchStreamProfile 拉一次房间档案（含 stream 段）。
//
// 与 huya_room.go 的 RoomInfo 是同一个接口，区别只在 showSecret=1——
// 不带它拿不到播放候选。
func (p *huyaProvider) huyaFetchStreamProfile(ctx context.Context, roomID string) (*huyaStreamProfile, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return nil, err
	}
	target := fmt.Sprintf(huyaStreamProfileURL, url.QueryEscape(roomID))
	body, err := p.api.get(ctx, "取流档案", target, nil)
	release()
	if err != nil {
		return nil, p.wrap("取流档案", err)
	}

	var env huyaProfileEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		// 正常响应一定是 JSON；解不出来基本是被换成了验证页或空页
		return nil, p.wrap("取流档案", fmt.Errorf("%w: 房间档案不是 JSON: %v", ErrBlocked, err))
	}
	if env.Status != 200 {
		switch env.Status {
		case 403, 412, 429:
			return nil, p.wrap("取流档案", fmt.Errorf("%w: 房间档案 status=%d", ErrBlocked, env.Status))
		}
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = fmt.Sprintf("status=%d", env.Status)
		}
		return nil, p.wrap("取流档案", &UpstreamError{Platform: Huya, Op: "取流档案", Message: msg})
	}

	var profile huyaStreamProfile
	if err := json.Unmarshal(env.Data, &profile); err != nil {
		// status=200 但 data 不是对象：房间没有档案，按「不存在」上抛
		return nil, p.wrap("取流档案", &UpstreamError{
			Platform: Huya, Op: "取流档案",
			Message: fmt.Sprintf("房间 %s 不存在或没有数据", roomID),
		})
	}
	return &profile, nil
}

// huyaStreamCandidates 提取可用的播放候选，按线路优先级排序。
func huyaStreamCandidates(profile *huyaStreamProfile) []huyaStreamCandidate {
	if profile == nil || profile.Stream == nil {
		return nil
	}
	items := profile.Stream.BaseStreamInfoList
	if len(items) == 0 {
		return nil
	}

	// 有的候选不带 lChannelId，用第一条的补上：频道 id 对整场直播是同一个。
	fallback := items[0].ChannelID
	out := make([]huyaStreamCandidate, 0, len(items))
	for _, item := range items {
		if item.CdnType == "" || item.FlvURL == "" || item.StreamName == "" {
			continue
		}
		if item.ChannelID == 0 {
			item.ChannelID = fallback
		}
		if item.ChannelID == 0 {
			continue
		}
		out = append(out, item)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return huyaCDNPriority(out[i].CdnType) < huyaCDNPriority(out[j].CdnType)
	})
	return out
}

// huyaCDNPriority 线路优先级：tx > al > hs > 其它（与参考实现一致）。
func huyaCDNPriority(cdn string) int {
	switch strings.ToLower(cdn) {
	case "tx":
		return 0
	case "al":
		return 1
	case "hs":
		return 2
	default:
		return 3
	}
}

// huyaSelectCandidate 按线路选择候选；没指定或匹配不上时用第一条。
func huyaSelectCandidate(candidates []huyaStreamCandidate, line string) huyaStreamCandidate {
	target := strings.ToLower(strings.TrimSpace(line))
	if target != "" {
		for _, candidate := range candidates {
			if strings.ToLower(candidate.CdnType) == target {
				return candidate
			}
		}
		if len(candidates) > 0 {
			// 线路菜单也来自同一份候选列表，走到这里说明菜单过期了。
			diag.L().Info().Str("requested", target).Str("fallback", candidates[0].CdnType).Msg("虎牙线路未命中，改用第一条")
		}
	}
	return candidates[0]
}

// huyaAvailableBitrates 取出可用码率，升序去重。
//
// liveData.bitRateInfo 是内嵌 JSON 字符串，比 stream.flv.rateArray 更全；
// 两个都缺时调用方用兜底值。
func huyaAvailableBitrates(profile *huyaStreamProfile) []int32 {
	if profile == nil {
		return nil
	}

	rates := make([]int32, 0, 4)
	if raw := strings.TrimSpace(profile.LiveData.BitRateInfo); raw != "" {
		var arr []struct {
			BitRate int32 `json:"iBitRate"`
		}
		if err := json.Unmarshal([]byte(raw), &arr); err == nil {
			for _, item := range arr {
				if item.BitRate > 0 {
					rates = append(rates, item.BitRate)
				}
			}
		}
	}
	if len(rates) == 0 && profile.Stream != nil {
		for _, item := range profile.Stream.Flv.RateArray {
			if item.BitRate > 0 {
				rates = append(rates, item.BitRate)
			}
		}
	}

	sort.Slice(rates, func(i, j int) bool { return rates[i] < rates[j] })
	deduped := rates[:0]
	for i, rate := range rates {
		if i == 0 || rate != rates[i-1] {
			deduped = append(deduped, rate)
		}
	}
	return deduped
}

// huyaStreamVariants 生成档位：原画、最大码率的高清、最小码率的标清。
//
// 只有一个码率时，「高清 N K」和「标清 N K」指的是同一条流（同一个 ratio
// 参数、同一个地址）：只出一个高清项，菜单里不该出现两个 Rate 相同的档位。
func huyaStreamVariants(profile *huyaStreamProfile) []huyaVariant {
	bitrates := huyaAvailableBitrates(profile)
	if len(bitrates) == 1 {
		return []huyaVariant{
			{ID: "source", Label: "原画"},
			{ID: "high", Label: fmt.Sprintf("高清 %dK", bitrates[0]), Rate: bitrates[0]},
		}
	}
	high := int32(huyaBitrateFallbackHD)
	sd := int32(huyaBitrateFallbackSD)
	if len(bitrates) > 0 {
		sd = bitrates[0]
		high = bitrates[len(bitrates)-1]
	}
	return []huyaVariant{
		{ID: "source", Label: "原画"},
		{ID: "high", Label: fmt.Sprintf("高清 %dK", high), Rate: high},
		{ID: "standard", Label: fmt.Sprintf("标清 %dK", sd), Rate: sd},
	}
}

// huyaStreamBaseURL 拼出原画地址（anti-code + codec），其它档位在此基础上加 ratio。
func huyaStreamBaseURL(candidate huyaStreamCandidate, anti string) string {
	base := strings.TrimRight(candidate.FlvURL, "/") + "/" + candidate.StreamName + ".flv?" + anti + "&codec=264"
	return huyaEnforceHTTPS(base)
}

/* ------------------------------------------------------------- WUP（取 token） */

// huyaCdnToken 调 getCdnTokenInfoEx 换播放鉴权 token。
func (p *huyaProvider) huyaCdnToken(ctx context.Context, flvURL, streamName string) (string, error) {
	// 换 token 也是一次上游取流请求，跟列表、档案共用同一个闸门。
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	packet := huyaEncodeWupRequest(1, huyaWupServant, huyaWupFunc,
		huyaEncodeCdnTokenParam(flvURL, streamName))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, huyaWupURL, bytes.NewReader(packet))
	if err != nil {
		return "", fmt.Errorf("构造 WUP 请求失败: %w", err)
	}
	req.Header.Set("User-Agent", huyaWupUA)
	req.Header.Set("Origin", huyaWupOrigin)
	req.Header.Set("Referer", huyaWupReferer)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", "application/octet-stream")

	// 直接复用 apiClient 的 http.Client：超时与连接池和其它虎牙请求一致。
	// 这个接口要发二进制 body 和专用 UA，走不了 apiClient.get。
	resp, err := p.api.http.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("WUP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, huyaWupMaxBody))
	if err != nil {
		return "", fmt.Errorf("WUP 读取响应失败: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusPreconditionFailed, http.StatusTooManyRequests:
		return "", fmt.Errorf("%w: WUP HTTP %d", ErrBlocked, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", &UpstreamError{
			Platform: Huya, Op: "取流鉴权", Status: resp.StatusCode, Message: snippet(body),
		}
	}

	ret, payload, desc := huyaDecodeWupResponse(body)
	if ret != 0 {
		if desc == "" {
			desc = fmt.Sprintf("WUP ret=%d", ret)
		}
		return "", &UpstreamError{Platform: Huya, Op: "取流鉴权", Message: desc}
	}

	token, err := huyaDecodeTokenFromTup(payload)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", &UpstreamError{Platform: Huya, Op: "取流鉴权", Message: "WUP 没有返回可用 token"}
	}
	return token, nil
}

// huyaEncodeCdnTokenParam 组装 TUP 的 tReq（GetCdnTokenExReq）。
func huyaEncodeCdnTokenParam(flvURL, streamName string) []byte {
	req := newHuyaTarsWriter()
	req.writeStructBegin(0)
	req.writeString(0, flvURL)
	req.writeString(1, streamName)
	req.writeInt32(2, 0)    // iLoopTime
	req.writeStructBegin(3) // tUserId
	req.writeInt64(0, 0)
	req.writeString(1, "")
	req.writeString(2, "")
	req.writeString(3, huyaWupClientUA)
	req.writeString(4, "")
	req.writeInt32(5, 0)
	req.writeString(6, "")
	req.writeString(7, "")
	req.writeStructEnd()
	req.writeInt32(4, huyaWupAppID)
	req.writeStructEnd()

	// UniAttribute：map<string, bytes>{"tReq": req}
	tup := newHuyaTarsWriter()
	tup.writeBytesMap(0, "tReq", req.bytes())
	return tup.bytes()
}

// huyaEncodeWupRequest 组装 TARS 请求包（对齐参考实现的 encode_tars_request_packet）。
func huyaEncodeWupRequest(requestID int32, servant, fn string, tupPayload []byte) []byte {
	w := newHuyaTarsWriter()
	w.writeInt16(1, 3) // iVersion
	w.writeInt8(2, 0)  // cPacketType
	w.writeInt32(3, 0) // iMessageType
	w.writeInt32(4, requestID)
	w.writeString(5, servant)
	w.writeString(6, fn)
	w.writeBytes(7, tupPayload)
	w.writeInt32(8, 0) // iTimeout
	w.writeEmptyMap(9)
	w.writeEmptyMap(10)

	body := w.bytes()
	total := uint32(len(body) + 4)
	out := make([]byte, 0, len(body)+4)
	out = append(out, byte(total>>24), byte(total>>16), byte(total>>8), byte(total))
	return append(out, body...)
}

// huyaDecodeWupResponse 解开 4 字节长度前缀 + ResponsePacket。
//
// 返回 (ret, tupPayload, resultDesc)。
func huyaDecodeWupResponse(buf []byte) (int32, []byte, string) {
	payload := buf
	if len(buf) >= 4 {
		declared := int(uint32(buf[0])<<24 | uint32(buf[1])<<16 | uint32(buf[2])<<8 | uint32(buf[3]))
		if declared >= 4 && declared <= len(buf) {
			payload = buf[4:declared]
		}
	}

	// 标准布局：iRet(5) + sBuffer(6) + status(7) + sResultDesc(8)。
	//
	// 不能先用 find 做「布局探测」：find 只定位头部不消费值，
	// 后续读取会落在上一个字段的值中间，整体错位。
	r := newHuyaTarsReader(payload)
	ret := r.readInt32Tag(5, -1)
	if sBuffer, err := r.readBytesTag(6); err == nil {
		return ret, sBuffer, r.readStringTag(8, "")
	}

	// 退化布局（虎牙偶尔回「请求式」包）：sBuffer 在 tag 7
	r2 := newHuyaTarsReader(payload)
	if sBuffer, err := r2.readBytesTag(7); err == nil {
		return 0, sBuffer, ""
	}
	return -1, nil, ""
}

// huyaDecodeTokenFromTup 从 UniAttribute 里取出 flvToken。
// 兼容两种布局：map<string,bytes> 与 map<string,map<string,bytes>>。
func huyaDecodeTokenFromTup(payload []byte) (string, error) {
	if len(payload) == 0 {
		return "", fmt.Errorf("WUP 返回空 payload")
	}

	if m, ok := huyaDecodeStringBytesMap(payload); ok {
		if v, exists := m["tRsp"]; exists {
			if token := huyaDecodeTokenStruct(v); token != "" {
				return token, nil
			}
		}
	}

	// 复杂布局：tRsp → map<string,bytes>
	outer, ok := huyaDecodeStringBytesMapReader(newHuyaTarsReader(payload))
	if !ok {
		return "", fmt.Errorf("无法解析 WUP 响应")
	}
	inner, exists := outer["tRsp"]
	if !exists {
		return "", fmt.Errorf("WUP 响应缺少 tRsp")
	}
	if m, ok := huyaDecodeStringBytesMap(inner); ok {
		if v, ok2 := m["GetCdnTokenExResp"]; ok2 {
			if token := huyaDecodeTokenStruct(v); token != "" {
				return token, nil
			}
		}
		for _, v := range m {
			if token := huyaDecodeTokenStruct(v); token != "" {
				return token, nil
			}
		}
	}
	if token := huyaDecodeTokenStruct(inner); token != "" {
		return token, nil
	}
	return "", nil
}

func huyaDecodeStringBytesMap(payload []byte) (map[string][]byte, bool) {
	return huyaDecodeStringBytesMapReader(newHuyaTarsReader(payload))
}

func huyaDecodeStringBytesMapReader(r *huyaTarsReader) (map[string][]byte, bool) {
	head, err := r.takeHead()
	if err != nil || head.typ != huyaTarsMap {
		return nil, false
	}
	count := int(r.readInt32(0, 0))
	if count < 0 || count > 64 {
		return nil, false
	}
	out := make(map[string][]byte, count)
	for i := 0; i < count; i++ {
		key := r.readStringTag(0, "")
		value, err := r.readBytesTag(1)
		if err != nil {
			return nil, false
		}
		out[key] = value
	}
	return out, true
}

// huyaDecodeTokenStruct 解 GetCdnTokenExResp（tag0 = sFlvToken）。
func huyaDecodeTokenStruct(payload []byte) string {
	r := newHuyaTarsReader(payload)
	if err := r.readStructBegin(0); err != nil {
		return ""
	}
	return strings.TrimSpace(r.readStringTag(0, ""))
}

/* --------------------------------------------------------------- anti-code */

// huyaBuildAntiCode 用 token 重新构造播放地址的鉴权串。
//
// 逐行对齐参考实现（对齐 pure_live）：
//
//	seqid      = presenterUID + 当前毫秒
//	secretHash = md5("seqid|ctype|platformId")
//	wsSecret   = md5("<fm 前缀>_<calcUid>_<streamName>_<secretHash>_<wsTime>")
//
// fm 前缀来自上游 token 里的 fm（base64 解码后按 '_' 切第一段）。
func huyaBuildAntiCode(streamName string, presenterUID int64, token string) (string, error) {
	sanitized := strings.ReplaceAll(token, "&amp;", "&")
	trimmed := strings.TrimLeft(sanitized, "?&")

	params, err := url.ParseQuery(trimmed)
	if err != nil {
		return "", fmt.Errorf("解析 anti-code 失败: %w", err)
	}

	fm := params.Get("fm")
	if fm == "" {
		// 上游已经给了可用的 query，直接用
		return trimmed, nil
	}

	ctype := params.Get("ctype")
	if ctype == "" {
		ctype = "huya_pc_exe"
	}

	platformID, _ := strconv.ParseInt(params.Get("t"), 10, 64)
	isWap := platformID == 103

	seqID := presenterUID + huyaCurrentMillis()
	secretHash := huyaMD5Hex(fmt.Sprintf("%d|%s|%d", seqID, ctype, platformID))

	convertUID := huyaRotl32By8(presenterUID)
	calcUID := convertUID
	if isWap {
		calcUID = presenterUID
	}

	fmDecoded, err := url.QueryUnescape(fm)
	if err != nil {
		return "", fmt.Errorf("fm 解码失败: %w", err)
	}
	fmPlain, err := huyaBase64Decode(fmDecoded)
	if err != nil {
		return "", fmt.Errorf("fm base64 解码失败: %w", err)
	}
	secretPrefix := strings.SplitN(string(fmPlain), "_", 2)[0]
	if secretPrefix == "" {
		return "", fmt.Errorf("无法从 fm 推导 wsSecret 前缀")
	}

	wsTime := params.Get("wsTime")
	if wsTime == "" {
		return "", fmt.Errorf("anti-code 缺少 wsTime")
	}
	fs := params.Get("fs")
	if fs == "" {
		return "", fmt.Errorf("anti-code 缺少 fs")
	}

	secretStr := fmt.Sprintf("%s_%d_%s_%s_%s", secretPrefix, calcUID, streamName, secretHash, wsTime)
	wsSecret := huyaMD5Hex(secretStr)

	wsTimeInt, _ := strconv.ParseInt(strings.TrimSpace(wsTime), 16, 64)
	ct := int64((float64(wsTimeInt) + rand.Float64()) * 1000)
	uuid := uint32((float64(ct%10000000000) + rand.Float64()) * 1000)
	uuid %= 0xFFFFFFFF

	parts := make([]string, 0, 10)
	add := func(k, v string) { parts = append(parts, k+"="+v) }
	add("wsSecret", wsSecret)
	add("wsTime", wsTime)
	add("seqid", strconv.FormatInt(seqID, 10))
	add("ctype", ctype)
	add("ver", "1")
	add("fs", fs)
	add("fm", url.QueryEscape(fm))
	add("t", strconv.FormatInt(platformID, 10))
	if isWap {
		add("uid", strconv.FormatInt(presenterUID, 10))
		add("uuid", strconv.FormatUint(uint64(uuid), 10))
	} else {
		add("u", strconv.FormatInt(convertUID, 10))
	}
	return strings.Join(parts, "&"), nil
}

/* ------------------------------------------------------------------ 小工具 */

func huyaMD5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// huyaCurrentMillis 当前毫秒时间戳（anti-code 的 seqid 基准）。
func huyaCurrentMillis() int64 { return time.Now().UnixMilli() }

// huyaRotl32By8 把低 32 位循环左移 8 位（对齐参考实现的 rotl32_by8_in_i64）。
func huyaRotl32By8(value int64) int64 {
	low := uint32(uint64(value) & 0xFFFFFFFF)
	rotated := low<<8 | low>>24
	high := value & ^int64(0xFFFFFFFF)
	return high | int64(rotated)
}

// huyaEnforceHTTPS 上游给的是 http://，桌面端统一用 https。
func huyaEnforceHTTPS(raw string) string {
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

// huyaBase64Decode 标准 base64 解码（fm 字段是标准 base64，兼容 URL-safe 变体）。
func huyaBase64Decode(s string) ([]byte, error) {
	std := strings.NewReplacer("-", "+", "_", "/").Replace(s)
	if m := len(std) % 4; m != 0 {
		std += strings.Repeat("=", 4-m)
	}
	return base64.StdEncoding.DecodeString(std)
}

// 编译期确认虎牙实现了取流接口。
var _ Streamer = (*huyaProvider)(nil)
