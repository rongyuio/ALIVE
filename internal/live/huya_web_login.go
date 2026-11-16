package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 虎牙的浏览器登录。
//
// 登录页是官网首页：虎牙没有独立的 passport 页面，登录是首页右上角的
// 弹窗，所以注入一段脚本把入口点开，用户打开面板就直接看到二维码。
//
// 会话判据来自 UDB 的登录 cookie：udb_uid 与 udb_biztoken 同时存在才算
// 登录，缺一个都会被取流接口按游客处理。这两个字段由 webview 的 cookie
// store 下发，含 HttpOnly，所以必须走 mygo.Cookies 而不是 document.cookie。

const (
	// huyaCheckLoginURL 是网页端的登录态查询（JSONP）。
	// 它同时是续期入口：带有效 cookie 请求时服务端会重新下发一整套
	// 登录 cookie，把有效期往后推。
	huyaCheckLoginURL = "https://l.huya.com/udb_web/udbport2.php"
	huyaHomeURL       = "https://www.huya.com/"
	// huyaLoginURL 是登录用的页面：首页会拉起直播间，/l 是直播列表，
	// 元素还是多；/myfollow 更轻，登录入口一样在顶栏。
	huyaLoginURL = "https://www.huya.com/myfollow"
)

// huyaCookieOrder 是落盘时的字段顺序：登录字段在前，好看也好排查。
var huyaCookieOrder = []string{
	"udb_uid", "udb_biztoken", "udb_passport", "username", "nickname",
	"yyuid", "udb_guiddata", "guid", "sdid", "udb_deviceid",
}

// huyaOpenScript 只做一件事：把登录入口点开。
//
// 登录框在 aq.huya.com 的 iframe 里，点开之后它会盖在页面上，用户在主
// 区域里直接扫码。不做裁剪，页面怎么改版都不影响。
const huyaOpenScript = `(function(){
  if (window !== window.top) return;
  var tries = 0;
  var t = setInterval(function(){
    tries++;
    if (tries > 120) { clearInterval(t); return; }
    var fs = document.querySelectorAll('iframe');
    for (var j = 0; j < fs.length; j++) {
      if ((fs[j].src || '').indexOf('udb_login') >= 0) { clearInterval(t); return; }
    }
    var els = document.querySelectorAll('a,div,span,button');
    for (var i = 0; i < els.length; i++) {
      if ((els[i].textContent || '').trim() !== '登录') continue;
      var r = els[i].getBoundingClientRect();
      if (r.width > 0 && r.width < 200 && r.height > 0 && r.height < 80) { els[i].click(); return; }
    }
  }, 400);
})();`

// huyaReadyScript 判断页面加载完了没有。
//
// 只看 readyState：登录框长什么样、在哪个 iframe 里都不管——那是平台
// 自己的事，我们只负责别让用户看到白屏。
const huyaReadyScript = `document.readyState === 'complete' ? 'yes' : 'no'`

func huyaWebLoginSpec() WebLoginSpec {
	return WebLoginSpec{
		URL:         huyaLoginURL,
		Domain:      "huya.com",
		Isolate:     huyaOpenScript,
		ReadyScript: huyaReadyScript,
		Detect:      huyaDetectCookie,
	}
}

// huyaDetectCookie 从 cookie 里认出登录态。
//
// UDB 的登录字段成对出现：只有 udb_uid 没有 udb_biztoken 是半个会话，
// 取流会被降档，不能算登录。
func huyaDetectCookie(fields map[string]string) (BrowserUser, bool) {
	uid := firstNonEmpty(fields["udb_uid"], fields["yyuid"])
	if uid == "" || fields["udb_biztoken"] == "" {
		return BrowserUser{}, false
	}
	name := firstNonEmpty(fields["nickname"], fields["username"])
	if decoded, err := url.QueryUnescape(name); err == nil {
		name = decoded
	}
	avatar := fields["avatar"]
	if decoded, err := url.QueryUnescape(avatar); err == nil {
		avatar = decoded
	}
	return BrowserUser{UID: uid, Name: strings.TrimSpace(name), Avatar: strings.TrimSpace(avatar)}, true
}

// huyaVerifyCookie 用存下来的 cookie 问一次登录态。
//
// 顺带把昵称和头像补上：cookie 里不一定有，checkLogin 一定有。
func huyaVerifyCookie(ctx context.Context, cookies string) (authUser, error) {
	payload, err := huyaCheckLogin(ctx, cookies)
	if err != nil {
		return authUser{}, err
	}
	if !payload.LoggedIn {
		return authUser{}, errSessionInvalid
	}
	name := firstNonEmpty(payload.Nick, payload.UserName)
	avatar := httpsURL(payload.Logo)
	// checkLogin 只给 uid 和账号名，昵称要单独问一次，头像走用户接口。
	// 两个都是锦上添花：拿不到就退回账号名，不影响登录本身。
	if nick, err := huyaCheckUserNick(ctx, cookies); err == nil && nick != "" {
		name = nick
	}
	if avatar == "" {
		if a, err := huyaUserAvatar(ctx, int64(payload.UID), cookies); err == nil {
			avatar = a
		}
	}
	user := authUser{
		UID:    strconv.FormatInt(int64(payload.UID), 10),
		Name:   name,
		Avatar: avatar,
	}
	if user.UID == "0" {
		user.UID = ""
	}
	return user, nil
}

// huyaCheckLoginResult 是 checkLogin 的 JSONP 载荷。
type huyaCheckLoginResult struct {
	LoggedIn bool      `json:"isLogined"`
	UID      jsonInt64 `json:"uid"`
	UserName string    `json:"userName"`
	Nick     string    `json:"userNick"`
	Logo     string    `json:"userLogo"`
}

// jsonInt64 接受数字或字符串两种写法。
//
// 虎牙同一个 uid 在不同接口里写法不一样：checkLogin 给字符串
// "2332420325"，getUserInfo 给数字。写死 int64 会让解析整个失败，
// 昵称头像也就补不上。
type jsonInt64 int64

func (n *jsonInt64) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" || s == `""` {
		*n = 0
		return nil
	}
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*n = jsonInt64(v)
	return nil
}

// huyaCheckLogin 发一次 checkLogin 并解析 JSONP。
//
// 这个接口同时是续期入口，所以把它和「需要哪些 cookie」分开：
// verify 与 renew 都走这里，只有 cookie 的来源不同。
func huyaCheckLogin(ctx context.Context, cookies string) (huyaCheckLoginResult, error) {
	target := fmt.Sprintf("%s?m=HuyaLogin&do=checkLogin&callback=aliveCb&_=%d",
		huyaCheckLoginURL, time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return huyaCheckLoginResult{}, err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Referer", huyaHomeURL)
	req.Header.Set("Cookie", cookies)

	resp, err := huyaHTTPClient.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return huyaCheckLoginResult{}, ctxErr
		}
		return huyaCheckLoginResult{}, fmt.Errorf("查询登录态失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return huyaCheckLoginResult{}, fmt.Errorf("读取登录态响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return huyaCheckLoginResult{}, fmt.Errorf("查询登录态失败: HTTP %d", resp.StatusCode)
	}

	s := string(body)
	open, closeIdx := strings.Index(s, "("), strings.LastIndex(s, ")")
	if open < 0 || closeIdx <= open {
		return huyaCheckLoginResult{}, errors.New("登录态响应不是 JSONP")
	}
	var out huyaCheckLoginResult
	if err := json.Unmarshal([]byte(s[open+1:closeIdx]), &out); err != nil {
		return huyaCheckLoginResult{}, fmt.Errorf("解析登录态失败: %w", err)
	}
	return out, nil
}

// huyaHTTPClient 是登录态查询用的客户端：不复用 apiClient，因为要单独
// 收 Set-Cookie（续期靠它）。
var huyaHTTPClient = &http.Client{Timeout: httpTimeout}

/* --------------------------------------------------- provider 上的挂载 */

func (p *huyaProvider) webLogin() *WebLogin { return p.login }

// setAuthCookie 换掉登录态。空串表示退出登录。
func (p *huyaProvider) setAuthCookie(raw string) {
	p.authMu.Lock()
	p.authCookie = raw
	p.authMu.Unlock()
}

// authCookieString 返回当前登录 cookie。
func (p *huyaProvider) authCookieString() string {
	p.authMu.RLock()
	defer p.authMu.RUnlock()
	return p.authCookie
}

// huyaCheckUserNick 取用户昵称。
//
// checkLogin 的 userNick 常年是空的，网页端也是单独问这个接口
// （headerfunction 里的 TT.login.getUserNick）。
func huyaCheckUserNick(ctx context.Context, cookies string) (string, error) {
	target := fmt.Sprintf("%s?m=HuyaOutside&do=checkUserNick&callback=aliveCb&_=%d",
		huyaCheckLoginURL, time.Now().UnixMilli())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Referer", huyaHomeURL)
	req.Header.Set("Cookie", cookies)
	resp, err := huyaHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	s := string(body)
	open, closeIdx := strings.Index(s, "("), strings.LastIndex(s, ")")
	if open < 0 || closeIdx <= open {
		return "", errors.New("昵称响应不是 JSONP")
	}
	inner := strings.TrimSpace(s[open+1 : closeIdx])
	// 两种形状都见过：直接一个字符串，或者 {"userNick": "..."}。
	if strings.HasPrefix(inner, "\"") {
		var nick string
		if err := json.Unmarshal([]byte(inner), &nick); err == nil {
			return strings.TrimSpace(nick), nil
		}
		return "", errors.New("昵称解析失败")
	}
	var out struct {
		Nick  string `json:"userNick"`
		Nick2 string `json:"nick"`
	}
	if err := json.Unmarshal([]byte(inner), &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(firstNonEmpty(out.Nick, out.Nick2)), nil
}

// huyaUserAvatar 取头像地址。
func huyaUserAvatar(ctx context.Context, uid int64, cookies string) (string, error) {
	if uid <= 0 {
		return "", errors.New("没有 uid")
	}
	target := fmt.Sprintf("https://user.huya.com/user/getUserInfo?uid=%d", uid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Referer", huyaHomeURL)
	req.Header.Set("Cookie", cookies)
	resp, err := huyaHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		Code int `json:"code"`
		Data struct {
			Avatar string `json:"avatar"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	if out.Code != 200 || out.Data.Avatar == "" {
		return "", errors.New("头像接口没有返回地址")
	}
	return httpsURL(out.Data.Avatar), nil
}
