package live

import (
	"context"
	"net/http"
	"strconv"
	"time"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLogin "github.com/rongyuio/bilibili/login"
)

// B 站扫码登录。
//
// 扫码拿到的 SESSDATA 必须落回**取流用的同一个** bilibili.Client：换实例
// 或者存了不用，界面显示已登录、实际请求还是匿名——这是最难查的一类错位。
//
// 但别把「拿不到 2K」归因到没登录：取流接口本身不校验登录，匿名也能拿到
// 平台开放的最高档。网页端超过 720P 弹登录是播放器前端的行为，接口和 CDN
// 都不拦。详见 internal/live/README.md。
const (
	// biliQRCodeURL 是扫码状态轮询接口。
	//
	// 库里有 LoginWithQRCode 把它包好了，但那个函数会把「已扫码待确认」
	// 这一态吞掉、一路阻塞到出结果，界面上就少了一句反馈。这里借 Execute
	// 走同一条请求链路自己轮询：登录成功的 Set-Cookie 仍然会合并进
	// client 的会话，和用库的封装等价。
	biliQRCodeURL = "https://passport.bilibili.com/x/passport-login/web/qrcode/poll"

	// biliQRLifetime 是 B 站二维码的有效期。
	biliQRLifetime = 180 * time.Second
)

// biliSessionCookies 是登录态相关的 cookie 名，退出登录时逐个清掉。
var biliSessionCookies = []string{
	"SESSDATA", "bili_jct", "DedeUserID", "DedeUserID__ckMd5", "sid",
}

// biliAuthDriver 是 B 站的扫码协议。
type biliAuthDriver struct {
	prov *bilibiliProvider
	lg   *bilibiliLogin.Client
}

func (d *biliAuthDriver) beginQR(ctx context.Context) ([]byte, string, time.Duration, error) {
	qr, err := d.lg.GetQRCode(ctx)
	if err != nil {
		return nil, "", 0, err
	}
	png, err := qr.Encode()
	if err != nil {
		return nil, "", 0, err
	}
	return png, qr.QrcodeKey, biliQRLifetime, nil
}

func (d *biliAuthDriver) pollQR(ctx context.Context, key string) (qrPoll, error) {
	poll, err := biblib.Execute[*bilibiliLogin.QRCodeLoginResult](ctx,
		d.lg.Client, "GET", biliQRCodeURL,
		bilibiliLogin.QRCodeLoginParam{QrcodeKey: key})
	if err != nil {
		return qrPoll{}, err
	}
	switch poll.Code {
	case 86101: // 未扫码
		return qrPoll{state: qrPending}, nil
	case 86090: // 已扫码未确认
		return qrPoll{state: qrScanned, msg: "已扫码，请在手机上确认"}, nil
	case 86038: // 二维码已失效
		return qrPoll{state: qrExpired}, nil
	case 0:
		return qrPoll{state: qrConfirmed}, nil
	default:
		return qrPoll{}, &UpstreamError{
			Platform: Bilibili, Op: "扫码登录",
			Message: poll.Message,
		}
	}
}

// finishQR 登录成功后拉一次账号信息：一是拿头像，二是确认会话真的生效了
// ——只看 0 返回码不够。
func (d *biliAuthDriver) finishQR(ctx context.Context) (authUser, error) {
	return d.navUser(ctx)
}

func (d *biliAuthDriver) verifyQR(ctx context.Context) (authUser, error) {
	return d.navUser(ctx)
}

func (d *biliAuthDriver) navUser(ctx context.Context) (authUser, error) {
	nav, err := d.lg.GetNavInfo(ctx)
	if err != nil {
		return authUser{}, err
	}
	if nav == nil || !nav.IsLogin {
		return authUser{}, errSessionInvalid
	}
	return authUser{
		UID:    strconv.FormatInt(nav.Mid, 10),
		Name:   nav.Uname,
		Avatar: biliAvatar(nav.Face),
	}, nil
}

func (d *biliAuthDriver) logoutQR() {
	d.prov.api.SetCookies(deadBiliCookies())
}

func (d *biliAuthDriver) loadCookies(raw string) { d.prov.api.SetCookiesString(raw) }

func (d *biliAuthDriver) cookieString() string { return d.prov.api.GetCookiesString() }

// deadBiliCookies 构造一组「立即过期」的 cookie：库的 SetCookies 按名字
// 匹配删除，MaxAge 为负就会把同名项摘掉。
func deadBiliCookies() []*http.Cookie {
	out := make([]*http.Cookie, 0, len(biliSessionCookies))
	for _, name := range biliSessionCookies {
		out = append(out, &http.Cookie{Name: name, Value: "", MaxAge: -1})
	}
	return out
}

// newBiliAuth 给 B 站 provider 挂上扫码登录。
func newBiliAuth(p *bilibiliProvider) *qrAuth {
	return newQRAuth(Bilibili, &biliAuthDriver{prov: p, lg: bilibiliLogin.New(p.api)})
}

// 下面几个是 bilibiliProvider 转给 qrAuth 的壳，让 provider 直接满足
// Authenticator——上层拿到 provider 就能登录，不用再摸内部字段。

func (p *bilibiliProvider) Auth() AuthState { return p.auth.Auth() }

func (p *bilibiliProvider) BeginLogin() { p.auth.BeginLogin() }

func (p *bilibiliProvider) RefreshQRCode() { p.auth.RefreshQRCode() }

func (p *bilibiliProvider) Logout() { p.auth.Logout() }

func (p *bilibiliProvider) SetAuthListener(fn func()) { p.auth.SetAuthListener(fn) }

// 编译期确认 B 站实现了登录接口。
var _ Authenticator = (*bilibiliProvider)(nil)
