package live

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 斗鱼 cookie 续期：走网页端的 safeAuth 静默重登。
//
// 机制与「访问一下首页」不同：passport.douyu.com/wgapi/member/passport/safeAuth
// 带一个真的 did 请求，服务端会回一整套登录 cookie，把票据有效期推到 7 天。
// 参考 ryDailyCheckin/platform/douyu/keepalive.go 的实测结论（2026-10-09）：
//   - 必须带 callback 参数，否则回首页 HTML、不下发 Set-Cookie；
//   - did 不能用 acf_devid 顶替，必须是 did SDK 领的那个；
//   - 手动跟 302：登录 cookie 分散种在跳转链的每一跳上；
//   - 服务端回 {"error":0} 却一条认证 cookie 都不下发 = 没续上，当失败。
//
// 闸门读 cookie 里两个登录 JWT 的 exp（本地 base64，零网络）。读不出来时
// fail-open 当「要续」——省下的只是一次请求，判错的方向却是断登录。
const (
	douyuSafeAuthURL = "https://passport.douyu.com/wgapi/member/passport/safeAuth"
	douyuDIDURL      = "https://passport.douyu.com/lapi/did/api/get"
	douyuHomeURL     = "https://www.douyu.com/"
	douyuKeepAliveCB = "aliveKeepAlive"
	douyuMaxHops     = 6
	douyuRenewBefore = 72 * time.Hour
	douyuSafeAuthTO  = 15 * time.Second
	douyuMaxSafeBody = 1 << 16
)

var douyuRenewJWTs = []string{"acf_jwt_token", "acf_dmjwt_token"}

// douyuRenew 实现 cookieRenewer。
func (d *douyuAuthDriver) renew(ctx context.Context, sess authSession) (string, string, error) {
	if due, why := douyuRenewDue(sess.Cookies, time.Now()); !due {
		return "", "", errNoRenewNeeded{why}
	}
	cookies := douyuParseCookieFields(sess.Cookies)
	did := cookies["dy_did"]
	issued := false
	if did == "" {
		got, err := d.fetchDID(ctx, sess.Cookies)
		if err != nil {
			return "", "", fmt.Errorf("领 did 失败: %w", err)
		}
		did, issued = got, true
	}
	got, err := d.safeAuth(ctx, sess.Cookies, did)
	if err != nil {
		return "", "", err
	}
	// 认证字段一条都没回来 = 没续上，不能当成功（did 不对就是这形状）。
	if len(refreshedDouyuAuthNames(got)) == 0 {
		return "", "", fmt.Errorf("safeAuth 回了 ok 但没下发认证 cookie（did 可能不对）")
	}
	for _, c := range got {
		if !douyuIsRenewAuthName(c.Name) || c.Value == "" {
			continue
		}
		cookies[c.Name] = c.Value
	}
	if issued {
		cookies["dy_did"] = did
	}
	updated := buildCookieString(cookies, douyuCookieOrder)
	if cookies["acf_uid"] == "" || cookies["acf_ltkid"] == "" {
		return "", "", fmt.Errorf("续期后 cookie 缺少 acf_uid/acf_ltkid，登录态已失效")
	}
	if updated == sess.Cookies {
		return "", "", nil
	}
	return updated, "", nil
}

// errNoRenewNeeded 是「还没到续期时间」的内部信号，调用方按成功处理。
type errNoRenewNeeded struct{ why string }

func (e errNoRenewNeeded) Error() string { return "还不到续期时间: " + e.why }

// douyuRenewDue 读两个登录 JWT 的 exp，判断要不要续。
func douyuRenewDue(raw string, now time.Time) (bool, string) {
	values := douyuParseCookieFields(raw)
	soonest := time.Time{}
	for _, name := range douyuRenewJWTs {
		tok := values[name]
		if tok == "" {
			return true, fmt.Sprintf("cookie 里没有 %s", name)
		}
		exp, err := douyuJWTExpiry(tok)
		if err != nil {
			return true, fmt.Sprintf("%s 解不出 exp: %v", name, err)
		}
		if !exp.After(now) {
			return true, fmt.Sprintf("%s 已过期", name)
		}
		if soonest.IsZero() || exp.Before(soonest) {
			soonest = exp
		}
	}
	if left := soonest.Sub(now); left > douyuRenewBefore {
		return false, fmt.Sprintf("%s 到期，还剩 %s", soonest.Format("2006-01-02 15:04"), left.Round(time.Hour))
	}
	return true, fmt.Sprintf("快到期了，还剩 %s", soonest.Sub(now).Round(time.Hour))
}

// douyuJWTExpiry 从 JWT payload 读 exp（本地，零网络）。
func douyuJWTExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("不是 JWT（%d 段）", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		pad := strings.Repeat("=", (4-len(parts[1])%4)%4)
		payload, err = base64.URLEncoding.DecodeString(parts[1] + pad)
		if err != nil {
			return time.Time{}, fmt.Errorf("payload 解不开: %w", err)
		}
	}
	var claims struct {
		Exp json.Number `json:"exp"`
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return time.Time{}, fmt.Errorf("payload 不是 JSON: %w", err)
	}
	if claims.Exp == "" {
		return time.Time{}, errors.New("payload 里没有 exp")
	}
	sec, err := strconv.ParseInt(claims.Exp.String(), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("exp 不是整数秒: %w", err)
	}
	return time.Unix(sec, 0), nil
}

// safeAuth 手动跟跳转，收下链上每一跳的 Set-Cookie。
func (d *douyuAuthDriver) safeAuth(ctx context.Context, cookie, did string) ([]*http.Cookie, error) {
	u := douyuSafeAuthURL + "?client_id=1" +
		"&redirect_url=" + url.QueryEscape(douyuHomeURL) +
		"&did=" + url.QueryEscape(did) +
		"&t=" + strconv.FormatInt(time.Now().UnixMilli(), 10) +
		"&callback=" + douyuKeepAliveCB

	client := &http.Client{
		Timeout:       douyuSafeAuthTO,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	var all []*http.Cookie
	for hop := 0; hop < douyuMaxHops; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("续期请求构造失败: %w", err)
		}
		douyuRenewHeaders(req, cookie)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("续期请求失败: %w", err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, douyuMaxSafeBody))
		_ = resp.Body.Close()
		all = append(all, resp.Cookies()...)

		loc := resp.Header.Get("Location")
		if resp.StatusCode/100 != 3 || loc == "" {
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("续期失败: HTTP %d", resp.StatusCode)
			}
			break
		}
		next, err := url.Parse(loc)
		if err != nil {
			return nil, fmt.Errorf("续期跳转地址解不开: %w", err)
		}
		base, _ := url.Parse(u)
		u = base.ResolveReference(next).String()
	}
	return all, nil
}

// fetchDID 领一个 did——网页 douyu-user-sdk.js 每次加载走的那条 JSONP。
func (d *douyuAuthDriver) fetchDID(ctx context.Context, cookie string) (string, error) {
	u := douyuDIDURL + "?client_id=1&callback=" + douyuKeepAliveCB
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	douyuRenewHeaders(req, cookie)
	client := &http.Client{Timeout: douyuSafeAuthTO}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, douyuMaxSafeBody))

	s := string(body)
	open, closeIdx := strings.Index(s, "("), strings.LastIndex(s, ")")
	if open < 0 || closeIdx <= open {
		return "", fmt.Errorf("did 接口没回 JSONP（HTTP %d）", resp.StatusCode)
	}
	var out struct {
		Error int `json:"error"`
		Data  struct {
			DID string `json:"did"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(s[open+1:closeIdx]), &out); err != nil {
		return "", fmt.Errorf("did 响应解不开: %w", err)
	}
	if out.Error != 0 || out.Data.DID == "" {
		return "", fmt.Errorf("did 接口回了 error=%d", out.Error)
	}
	return out.Data.DID, nil
}

func douyuRenewHeaders(req *http.Request, cookie string) {
	req.Header.Set("User-Agent", desktopUA)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", douyuHomeURL)
	req.Header.Set("Sec-Fetch-Dest", "script")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Cookie", cookie)
}

func douyuIsRenewAuthName(name string) bool {
	return strings.HasPrefix(name, "acf_") ||
		name == "dy_did" || name == "dy_accounts_main" || name == "LTP0"
}

func refreshedDouyuAuthNames(cookies []*http.Cookie) []string {
	seen := map[string]bool{}
	for _, c := range cookies {
		if c != nil && douyuIsRenewAuthName(c.Name) {
			seen[c.Name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
