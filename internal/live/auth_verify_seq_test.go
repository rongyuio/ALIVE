package live

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// fakeRaceVerifyDriver 在「校验请求在飞」的那一刻执行退出登录，
// 复刻「启动 nav 请求在飞 → 用户点退出 → nav 才返回成功」的时序。
type fakeRaceVerifyDriver struct {
	loggedOut bool
	when      func()
}

func (d *fakeRaceVerifyDriver) beginQR(context.Context) ([]byte, string, time.Duration, error) {
	return nil, "", 0, errVerifyUnused
}
func (d *fakeRaceVerifyDriver) pollQR(context.Context, string) (qrPoll, error) {
	return qrPoll{}, errVerifyUnused
}
func (d *fakeRaceVerifyDriver) finishQR(context.Context) (authUser, error) {
	return authUser{}, errVerifyUnused
}
func (d *fakeRaceVerifyDriver) verifyQR(context.Context) (authUser, error) {
	d.when()
	return authUser{Name: "ry", UID: "42", Avatar: "https://example/face.jpg"}, nil
}
func (d *fakeRaceVerifyDriver) logoutQR()            { d.loggedOut = true }
func (d *fakeRaceVerifyDriver) loadCookies(string)   {}
func (d *fakeRaceVerifyDriver) cookieString() string { return "" }
func (d *fakeRaceVerifyDriver) refreshToken() string { return "" }

// TestQRAuthVerifyDropsResultAfterLogout 看住「退出登录之后，迟到的校验结果必须作废」。
//
// verify 启动时不做代次记录，成功分支用 a.currentSeq() 去调 update——那是
// 「调用那一刻的最新代次」，Logout 之后它照样等于 a.seq，检查形同虚设。
// 结果就是：用户已经退出，正在飞的 nav 返回成功后又把会话文件写回去、
// 把界面点亮成已登录，而后续请求带的是已被清空的 cookie。
func TestQRAuthVerifyDropsResultAfterLogout(t *testing.T) {
	useTempConfigDir(t)
	path := writeTestSession(t, Bilibili)

	drv := &fakeRaceVerifyDriver{}
	a := &qrAuth{platform: Bilibili, drv: drv}
	a.state.LoggedIn = true
	a.state.Name = "老用户"
	drv.when = func() { a.Logout() }

	a.verify()

	if !drv.loggedOut {
		t.Error("Logout 应清掉平台客户端的登录态")
	}
	if got := a.Auth(); got.LoggedIn || got.Name != "" || got.Avatar != "" {
		t.Fatalf("退出登录后迟到的校验把账号又点亮了：%+v", got)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("退出登录后迟到的校验又把会话文件写回去了")
	}
}

// errVerifyUnused 只是让未实现的驱动方法返回一个明确错误。
var errVerifyUnused = errors.New("测试不走这条路径")
