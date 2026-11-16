package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// 斗鱼扫码登录。
//
// 协议（与网页端 passport 一致）：
//  1. POST passport.douyu.com/scan/generateCode 拿 scan code 与二维码内容；
//  2. 轮询 passport.douyu.com/japi/scan/auth?time=&code=：
//     -2 未扫码、1 已扫码、0 已确认、-1 失效；
//  3. 已确认时 data.url 是登录回调地址，用 JSONP 请求它拿最终 Set-Cookie；
//  4. 登录 Cookie 里的 dy_did 必须和取流用的 did 是同一个，否则斗鱼按游客
//     处理、清晰度会被降。所以申请二维码时就带上本进程的 did。
const (
	douyuQRGenerateURL = "https://passport.douyu.com/scan/generateCode"
	douyuQRCheckURL    = "https://passport.douyu.com/japi/scan/auth"

	// douyuQRSignReferer 是 passport 登录页，缺它会被当成外站请求。
	douyuQRSignReferer = "https://passport.douyu.com/index/login?type=login&client_id=1"

	// douyuQRDefaultTTL 是服务端没给有效期时的兜底。
	douyuQRDefaultTTL = 300 * time.Second
)

// douyuCookieOrder 是落盘时的 cookie 顺序：登录字段在前，好看也好排查。
var douyuCookieOrder = []string{
	"dy_auth", "acf_auth", "acf_jwt_token", "dy_did", "acf_did",
	"LTP0", "acf_uid", "acf_stk", "acf_username", "acf_nickname",
}

// douyuAuthDriver 是斗鱼的扫码协议。
type douyuAuthDriver struct {
	prov *douyuProvider

	// 一次一轮登录，状态机保证这一点，所以这一轮的中间产物直接放这儿。
	pendingPath    string
	pendingCookies []*http.Cookie
}

func (d *douyuAuthDriver) headers() header {
	return header{
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9,en;q=0.7",
		"Origin":          "https://passport.douyu.com",
		"Referer":         douyuQRSignReferer,
		// 带了 did，登录态才会绑到这个设备号上；不带也能登录，
		// 但之后取流用的 did 对不上，会被按游客降档。
		"Cookie": "dy_did=" + d.prov.did + "; acf_did=" + d.prov.did,
	}
}

func (d *douyuAuthDriver) beginQR(ctx context.Context) ([]byte, string, time.Duration, error) {
	form := url.Values{}
	form.Set("client_id", "1")
	form.Set("isMultiAccount", "0")

	body, err := d.prov.api.postForm(ctx, "申请登录二维码", douyuQRGenerateURL,
		strings.NewReader(form.Encode()), d.headers())
	if err != nil {
		return nil, "", 0, err
	}

	var payload struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  *struct {
			Code   string `json:"code"`
			Expire int    `json:"expire"`
			URL    string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", 0, fmt.Errorf("解析二维码响应失败: %w", err)
	}
	if payload.Error != 0 {
		return nil, "", 0, fmt.Errorf("error=%d (%s)", payload.Error, payload.Msg)
	}
	if payload.Data == nil || payload.Data.Code == "" || payload.Data.URL == "" {
		return nil, "", 0, fmt.Errorf("平台没有返回二维码内容")
	}

	png, err := qrcode.Encode(payload.Data.URL, qrcode.Medium, 256)
	if err != nil {
		return nil, "", 0, fmt.Errorf("生成二维码图片失败: %w", err)
	}
	ttl := time.Duration(payload.Data.Expire) * time.Second
	if ttl <= 0 {
		ttl = douyuQRDefaultTTL
	}
	return png, payload.Data.Code, ttl, nil
}

func (d *douyuAuthDriver) pollQR(ctx context.Context, key string) (qrPoll, error) {
	target := fmt.Sprintf("%s?time=%d&code=%s",
		douyuQRCheckURL, time.Now().UnixMilli(), url.QueryEscape(key))
	body, cookies, err := d.prov.api.getWithCookies(ctx, "查询扫码状态", target, d.headers())
	if err != nil {
		return qrPoll{}, err
	}

	var payload struct {
		Error int    `json:"error"`
		Msg   string `json:"msg"`
		Data  any    `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return qrPoll{}, fmt.Errorf("解析扫码状态失败: %w", err)
	}

	message := douyuText(payload.Data)
	switch payload.Error {
	case 0:
		data, ok := payload.Data.(map[string]any)
		if !ok {
			return qrPoll{}, fmt.Errorf("平台返回了无法识别的登录数据")
		}
		path, _ := data["url"].(string)
		if strings.TrimSpace(path) == "" {
			return qrPoll{}, fmt.Errorf("平台没有返回登录回调地址")
		}
		// 回调地址和这一步的 cookie 都要留到 finishQR 用。
		d.pendingPath = path
		d.pendingCookies = cookies
		return qrPoll{state: qrConfirmed}, nil
	case 1:
		return qrPoll{state: qrScanned, msg: firstNonEmpty(message, payload.Msg, "已扫码，请在手机上确认")}, nil
	case -2:
		return qrPoll{state: qrPending}, nil
	case -1:
		return qrPoll{state: qrExpired}, nil
	default:
		return qrPoll{}, fmt.Errorf("扫码失败（%d）：%s", payload.Error, firstNonEmpty(message, payload.Msg))
	}
}

// finishQR 走一次登录回调，收集 Set-Cookie 拼成会话。
func (d *douyuAuthDriver) finishQR(ctx context.Context) (authUser, error) {
	if d.pendingPath == "" {
		return authUser{}, fmt.Errorf("缺少登录回调地址")
	}
	loginURL := douyuCallbackURL(d.pendingPath, time.Now().UnixMilli())
	body, cookies, err := d.prov.api.getWithCookies(ctx, "登录回调", loginURL, d.headers())
	if err != nil {
		return authUser{}, err
	}

	payload, err := douyuJSONPObject(body)
	if err != nil {
		return authUser{}, err
	}
	if code, _ := douyuIntOK(payload["error"]); code != 0 {
		return authUser{}, fmt.Errorf("登录失败：%s", firstNonEmpty(douyuText(payload["msg"]), "平台拒绝"))
	}

	fields := map[string]string{}
	douyuMergeCookies(fields, d.pendingCookies, false)
	douyuMergeCookies(fields, cookies, true)
	if fields["dy_did"] == "" {
		fields["dy_did"] = d.prov.did
	}
	if fields["acf_did"] == "" {
		fields["acf_did"] = d.prov.did
	}
	cookie := buildCookieString(fields, douyuCookieOrder)
	if !douyuCookieHasLogin(cookie) {
		return authUser{}, fmt.Errorf("登录成功但未拿到有效 Cookie")
	}
	d.prov.setAuthCookie(cookie)

	user := authUser{
		UID:    fields["acf_uid"],
		Name:   douyuCookieUserName(cookie),
		Avatar: douyuCookieAvatar(cookie),
	}
	// 回调里偶尔会带昵称和头像，有就用更准的那份。
	if data, ok := payload["data"].(map[string]any); ok {
		user.Name = firstNonEmpty(
			douyuText(data["nickname"]), douyuText(data["nickName"]),
			douyuText(data["nick_name"]), douyuText(data["username"]),
			douyuText(data["userName"]), user.Name,
		)
		user.Avatar = douyuAvatar(httpsURL(firstNonEmpty(
			douyuText(data["avatar"]), douyuText(data["avatar_url"]), douyuText(data["avatarUrl"]),
			// 回调 payload 不带头像时，登录 cookie 里的 acf_avatar 是稳定来源。
			user.Avatar,
		)))
	}
	return user, nil
}

// verifyQR 校验存下来的会话。
//
// 斗鱼没有像 B 站 nav 那种「顺手就能问一句」的接口，这里只做本地判断：
// cookie 里有没有那对登录字段。宁可偶尔多留一次过期会话（下次取流会
// 发现并降级），也不要在启动时多发一个请求去赌风控。
func (d *douyuAuthDriver) verifyQR(context.Context) (authUser, error) {
	return douyuVerifyUser(d.cookieString())
}

// douyuVerifyUser 从存下来的 cookie 恢复展示信息。
// 单独抽出来是为了测试：verifyQR 依赖真 provider，测试里造不出来。
func douyuVerifyUser(raw string) (authUser, error) {
	if !douyuCookieHasLogin(raw) {
		return authUser{}, errSessionInvalid
	}
	return authUser{
		Name:   douyuCookieUserName(raw),
		Avatar: douyuCookieAvatar(raw),
	}, nil
}

// douyuCookieAvatar 从登录 cookie 的 acf_avatar 读头像地址。
//
// 扫码回调的 payload 里偶尔也带头像，但那个不稳定；登录 cookie 里
// acf_avatar 是服务端固定下发的，启动恢复登录态时也能读到，不会把
// 已存下来的头像写空。值可能是 URL 编码过的，和昵称一样解一层。
func douyuCookieAvatar(raw string) string {
	value := douyuParseCookieFields(raw)["acf_avatar"]
	if decoded, err := url.QueryUnescape(value); err == nil {
		value = decoded
	}
	// acf_avatar 的文件名常以 `_` 结尾、没有扩展名（实测 ...ec_），
	// 直接用会 404。douyuAvatar 就是为这种形状写的：补 _big.jpg。
	return douyuAvatar(httpsURL(strings.TrimSpace(value)))
}

func (d *douyuAuthDriver) logoutQR() { d.prov.setAuthCookie("") }

func (d *douyuAuthDriver) loadCookies(raw string) { d.prov.setAuthCookie(raw) }

func (d *douyuAuthDriver) cookieString() string { return d.prov.authCookieString() }

// newDouyuAuth 给斗鱼 provider 挂上扫码登录。
func newDouyuAuth(p *douyuProvider) *qrAuth {
	return newQRAuth(Douyu, &douyuAuthDriver{prov: p})
}

// 下面几个是 douyuProvider 转给 qrAuth 的壳，让 provider 满足 Authenticator。

func (p *douyuProvider) Auth() AuthState { return p.auth.Auth() }

func (p *douyuProvider) BeginLogin() { p.auth.BeginLogin() }

func (p *douyuProvider) RefreshQRCode() { p.auth.RefreshQRCode() }

func (p *douyuProvider) Logout() { p.auth.Logout() }

func (p *douyuProvider) SetAuthListener(fn func()) { p.auth.SetAuthListener(fn) }

// ---------------------------------------------------------------- cookie 工具

// setAuthCookie 换掉登录态。空串表示退出登录。
func (p *douyuProvider) setAuthCookie(raw string) {
	p.authMu.Lock()
	p.authCookie = raw
	p.authMu.Unlock()
}

// authCookieString 返回登录 cookie（不含 did 兜底），供落盘用。
func (p *douyuProvider) authCookieString() string {
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	return p.authCookie
}

// douyuCookieHeader 拼请求用的 Cookie 头。
//
// did 永远是当前请求的那个（取流时可能来自签名响应），登录字段有就带上。
// 只发登录 cookie、不发 did 会被判成异常客户端；反过来只发 did 就是游客。
func (p *douyuProvider) douyuCookieHeader(did string) string {
	fields := douyuParseCookieFields(p.authCookieString())
	fields["dy_did"] = did
	fields["acf_did"] = did
	return buildCookieString(fields, douyuCookieOrder)
}

func douyuParseCookieFields(raw string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	return out
}

func buildCookieString(fields map[string]string, order []string) string {
	parts := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, name := range order {
		if v := fields[name]; v != "" {
			parts = append(parts, name+"="+v)
			seen[name] = true
		}
	}
	rest := make([]string, 0, len(fields))
	for name, v := range fields {
		if seen[name] || v == "" {
			continue
		}
		rest = append(rest, name+"="+v)
	}
	sort.Strings(rest)
	return strings.Join(append(parts, rest...), "; ")
}

// douyuCookieHasLogin 报告 cookie 里有没有可用的登录字段。
//
// H5 会话必须成对出现：只有 acf_auth / acf_jwt_token 其中之一时，
// 取流接口会直接 403，不能算可用登录。
func douyuCookieHasLogin(raw string) bool {
	fields := douyuParseCookieFields(raw)
	h5 := fields["acf_auth"] != "" && fields["acf_jwt_token"] != ""
	return h5 || fields["dy_auth"] != ""
}

// douyuCookieUserName 从登录 cookie 里取昵称，取不到返回空串。
func douyuCookieUserName(raw string) string {
	fields := douyuParseCookieFields(raw)
	name := firstNonEmpty(fields["acf_nickname"], fields["acf_username"])
	if name == "" {
		return ""
	}
	if decoded, err := url.QueryUnescape(name); err == nil {
		return strings.TrimSpace(decoded)
	}
	return name
}

func douyuMergeCookies(dst map[string]string, cookies []*http.Cookie, override bool) {
	for _, ck := range cookies {
		if ck.Name == "" || ck.Value == "" {
			continue
		}
		// 只收斗鱼域下的，别的域（比如 CDN）的 cookie 带上去反而是噪音。
		if ck.Domain != "" && !strings.Contains(strings.ToLower(ck.Domain), "douyu.com") {
			continue
		}
		if !override {
			if _, exists := dst[ck.Name]; exists {
				continue
			}
		}
		dst[ck.Name] = ck.Value
	}
}

// douyuCallbackURL 把回调地址补成完整 URL 并带上 JSONP 参数。
func douyuCallbackURL(raw string, nowMs int64) string {
	login := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(login, "//"):
		login = "https:" + login
	case strings.HasPrefix(login, "http://"), strings.HasPrefix(login, "https://"):
	default:
		login = "https://passport.douyu.com/" + strings.TrimLeft(login, "/")
	}
	sep := "?"
	if strings.Contains(login, "?") {
		sep = "&"
	}
	return login + sep + "callback=appClient_json_callback&_=" + strconv.FormatInt(nowMs, 10)
}

// douyuJSONPObject 从 JSONP 响应里抠出对象部分再解析。
func douyuJSONPObject(body []byte) (map[string]any, error) {
	start := bytes.IndexByte(body, '{')
	end := bytes.LastIndexByte(body, '}')
	if start < 0 || end < start {
		return nil, fmt.Errorf("登录回调不是 JSONP 对象")
	}
	var payload map[string]any
	if err := json.Unmarshal(body[start:end+1], &payload); err != nil {
		return nil, fmt.Errorf("解析登录回调失败: %w", err)
	}
	return payload, nil
}

// 编译期确认斗鱼实现了登录接口。
var _ Authenticator = (*douyuProvider)(nil)
