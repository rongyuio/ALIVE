package live

import (
	"context"
	"os"
	"testing"
	"time"
)

// fakeRenewDriver 在续期请求「在飞」时执行一个钩子，用来复刻
// 「续期请求还没回来，用户已经点了退出登录」的时序。
type fakeRenewDriver struct {
	cookies   string
	loggedOut bool
	when      func()
}

func (d *fakeRenewDriver) beginQR(context.Context) ([]byte, string, time.Duration, error) {
	return nil, "", 0, errVerifyUnused
}
func (d *fakeRenewDriver) pollQR(context.Context, string) (qrPoll, error) {
	return qrPoll{}, errVerifyUnused
}
func (d *fakeRenewDriver) finishQR(context.Context) (authUser, error) {
	return authUser{}, errVerifyUnused
}
func (d *fakeRenewDriver) verifyQR(context.Context) (authUser, error) {
	return authUser{}, errVerifyUnused
}
func (d *fakeRenewDriver) logoutQR()            { d.loggedOut = true; d.cookies = "" }
func (d *fakeRenewDriver) loadCookies(c string) { d.cookies = c }
func (d *fakeRenewDriver) cookieString() string { return d.cookies }
func (d *fakeRenewDriver) renew(ctx context.Context, sess authSession) (string, string, error) {
	return d.forceRenew(ctx, sess)
}
func (d *fakeRenewDriver) forceRenew(context.Context, authSession) (string, string, error) {
	if d.when != nil {
		d.when()
	}
	return "SESSDATA=new", "new-token", nil
}

// 续期结果回来之前用户退出了登录：结果必须整份丢弃。
//
// commitRenew 不核对代次的话，Logout 清掉的 cookie 和删掉的会话文件会被
// 服务端仍有效的续期票据重新写回去；界面是登出态，下次启动 restore 又把
// 账号点亮——和 verify 那条迟到回调是同一个形状。
func TestRenewDropsResultAfterLogout(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(a *qrAuth, drv *fakeRenewDriver)
	}{
		{"RenewNow", func(a *qrAuth, drv *fakeRenewDriver) { _ = a.RenewNow(context.Background()) }},
		{"renewOnce", func(a *qrAuth, drv *fakeRenewDriver) { a.renewOnce(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useTempConfigDir(t)
			path := writeTestSession(t, Bilibili)

			drv := &fakeRenewDriver{cookies: "SESSDATA=old"}
			a := &qrAuth{platform: Bilibili, drv: drv}
			a.renewer = drv
			a.state.LoggedIn = true
			drv.when = func() { a.Logout() }

			tc.run(a, drv)

			if drv.cookies != "" {
				t.Fatalf("退出登录后迟到的续期把 cookie 写回了驱动：%q", drv.cookies)
			}
			if _, err := os.Stat(path); err == nil {
				t.Fatal("退出登录后迟到的续期把会话文件写回去了")
			}
			if got := a.Auth(); got.LoggedIn {
				t.Fatalf("退出登录后迟到的续期又把登录态点亮了：%+v", got)
			}
		})
	}
}
