package live

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	bilibiliLogin "github.com/rongyuio/bilibili/login"
)

// B 站 cookie 续期：走网页端的 cookie/refresh 三步。
//
// 和斗鱼不同，B 站不能靠「访问一下」续期：SESSDATA 是服务端签发的，
// 到期只能靠 refresh_token 换新的。alive 用的 bilibili v0.14.0 提供了
// 完整三步（cookie/info → correspond 页取 csrf → cookie/refresh），这里接上。
//
// refresh_token 在扫码确认时由 qrDriver 存下来（见 biliAuthDriver.refreshTok），
// 跟着会话文件落盘。没有它就没法续——静默跳过，等用户重新扫码后才有。
//
// 闸门读 SESSDATA 第二段的服务端到期时刻（和 ryDailyCheckin 同一套解析）：
// 剩不到 7 天才刷。B 站签发 180 天，所以正常节奏是一年刷两次。
const biliRenewBefore = 7 * 24 * time.Hour

// 没有 refresh_token 是老会话的正常状态（这个字段是后来才存的），
// 不该每次启动都报一条「续期失败」——只有真需要续时才提醒重扫。
var errNoBiliRefreshToken = errNoRenewNeeded{"会话里没有 refresh_token"}

// biliRenew 实现 cookieRenewer。
func (d *biliAuthDriver) renew(ctx context.Context, sess authSession) (string, string, error) {
	if sess.RefreshToken == "" {
		return "", "", errNoBiliRefreshToken
	}
	if !biliRenewDue(sess.Cookies, time.Now()) {
		return "", "", errNoRenewNeeded{"SESSDATA 还早到期"}
	}

	info, err := d.lg.GetWebCookieRefreshInfo(ctx)
	if err != nil {
		return "", "", fmt.Errorf("查询刷新信息失败: %w", err)
	}
	if info == nil || !info.Refresh {
		return "", "", errNoRenewNeeded{"服务端说不需要刷新"}
	}

	csrf, err := d.lg.GetWebCookieRefreshCsrf(ctx,
		bilibiliLogin.GetWebCookieRefreshCsrfParam{Timestamp: info.Timestamp})
	if err != nil {
		return "", "", fmt.Errorf("取刷新口令失败: %w", err)
	}

	result, err := d.lg.RefreshCookie(ctx, bilibiliLogin.RefreshCookieParam{
		RefreshCsrf:  csrf.RefreshCsrf,
		Source:       "main_web",
		RefreshToken: sess.RefreshToken,
	})
	if err != nil {
		return "", "", fmt.Errorf("刷新 cookie 失败: %w", err)
	}
	if result == nil || result.RefreshToken == "" {
		return "", "", fmt.Errorf("刷新接口没回新的 refresh_token")
	}

	// RefreshCookie 已经把新 cookie 合并进 api 的会话（Set-Cookie），
	// 从那里重新导出即可，不要自己拼。
	return d.cookieString(), result.RefreshToken, nil
}

// biliRenewDue 读 SESSDATA 的到期时刻，判断要不要刷。
// 读不出来时返回 true（fail-open）：多刷一次没坏处，漏刷会断登录。
func biliRenewDue(raw string, now time.Time) bool {
	exp, ok := biliSESSDATAExpiry(raw)
	if !ok {
		return true
	}
	return !exp.After(now.Add(biliRenewBefore))
}

// biliSESSDATAExpiry 从 cookie 串里解出 SESSDATA 的服务端到期时刻。
//
// SESSDATA 形状是 `<token>,<到期 unix 秒>,<签名>*<前缀>`；逗号在跨域
// 跳转地址里可能是 `%2C`，所以两种形态都试。alive 的 cookie 串来自
// GetCookiesString（换行分隔），也可能来自手填的分号串，两种都兼容。
func biliSESSDATAExpiry(raw string) (time.Time, bool) {
	for _, line := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == ';' }) {
		part := strings.TrimSpace(line)
		if !strings.HasPrefix(part, "SESSDATA=") {
			continue
		}
		value := strings.TrimPrefix(part, "SESSDATA=")
		if decoded, err := url.QueryUnescape(value); err == nil {
			value = decoded
		}
		fields := strings.Split(value, ",")
		if len(fields) < 2 {
			return time.Time{}, false
		}
		exp, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || exp <= 0 {
			return time.Time{}, false
		}
		return time.Unix(exp, 0), true
	}
	return time.Time{}, false
}
