package live

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLogin "github.com/rongyuio/bilibili/login"
)

// fakeVerifyDriver 只实现启动校验这条路径用得到的方法。
type fakeVerifyDriver struct {
	verifyErr error
	loggedOut bool
}

func (d *fakeVerifyDriver) beginQR(context.Context) ([]byte, string, time.Duration, error) {
	return nil, "", 0, errors.New("测试不走扫码")
}

func (d *fakeVerifyDriver) pollQR(context.Context, string) (qrPoll, error) {
	return qrPoll{}, errors.New("测试不走扫码")
}

func (d *fakeVerifyDriver) finishQR(context.Context) (authUser, error) {
	return authUser{}, errors.New("测试不走扫码")
}

func (d *fakeVerifyDriver) verifyQR(context.Context) (authUser, error) {
	return authUser{}, d.verifyErr
}

func (d *fakeVerifyDriver) logoutQR() { d.loggedOut = true }

func (d *fakeVerifyDriver) loadCookies(string) {}

func (d *fakeVerifyDriver) cookieString() string { return "SESSDATA=old" }

// fakeTimeout 是一个满足 net.Error 的超时错误。
type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

// useTempConfigDir 把 os.UserConfigDir 指到临时目录，避免碰真实会话文件。
func useTempConfigDir(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
}

// writeTestSession 在临时配置目录里放一份会话文件，返回它的路径。
func writeTestSession(t *testing.T, p Platform) string {
	t.Helper()
	path := authSessionPath(p)
	if path == "" {
		t.Fatal("临时配置目录不可用")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"cookies":"SESSDATA=old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestQRAuthVerifyErrorClassification 看住「启动校验只删真失效的会话」。
//
// verifyQR 对 B 站是真实的 GetNavInfo 请求：断网、超时、5xx 都会返回普通
// error。把这些当会话失效处理，等于离线启动一次就静默删掉用户的登录态，
// 与 renewOnce「续不上就下次再试，绝不删会话」的约定相反。
func TestQRAuthVerifyErrorClassification(t *testing.T) {
	cases := []struct {
		name         string
		err          error
		wantLoggedIn bool
		wantSession  bool
		wantLogout   bool
	}{
		{"会话失效", errSessionInvalid, false, false, true},
		{"包装后的会话失效", fmt.Errorf("nav: %w", errSessionInvalid), false, false, true},
		{"网络超时", fakeTimeout{}, true, true, false},
		{"网络错误", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, true, true, false},
		{"服务端 5xx", errors.New("GET /nav: HTTP 500"), true, true, false},
		{"响应解码失败", errors.New("decode: unexpected end of JSON input"), true, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTempConfigDir(t)
			path := writeTestSession(t, Bilibili)

			drv := &fakeVerifyDriver{verifyErr: tc.err}
			a := &qrAuth{platform: Bilibili, drv: drv}
			a.state.LoggedIn = true
			a.state.Name = "老用户"

			a.verify()

			if got := a.Auth().LoggedIn; got != tc.wantLoggedIn {
				t.Fatalf("LoggedIn=%v，期望 %v（err=%v）", got, tc.wantLoggedIn, tc.err)
			}
			if _, err := os.Stat(path); (err == nil) != tc.wantSession {
				t.Fatalf("会话文件存在=%v，期望 %v（err=%v）", err == nil, tc.wantSession, tc.err)
			}
			if drv.loggedOut != tc.wantLogout {
				t.Fatalf("logoutQR 调用=%v，期望 %v", drv.loggedOut, tc.wantLogout)
			}
		})
	}
}

// TestBiliNavUserSessionInvalid 看住 B 站的哨兵接线。
//
// 服务端明确回「账号未登录」(-101) 或 isLogin=false 才算会话失效；
// 断网、超时这类错误必须原样返回，否则 verify 会把它们误判成失效。
func TestBiliNavUserSessionInvalid(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		netErr   error
		wantSess bool
		wantErr  bool
		wantName string
	}{
		{name: "code -101 账号未登录", body: `{"code":-101,"message":"账号未登录"}`, wantSess: true, wantErr: true},
		{name: "code 0 但 isLogin=false", body: `{"code":0,"data":{"isLogin":false}}`, wantSess: true, wantErr: true},
		// data 缺失只说明平台没给账号信息，不是「不认这个会话」；当成失效
		// 会重演「模糊错误删掉可用登录」。
		{name: "code 0 但 data 为 null", body: `{"code":0,"data":null}`, wantErr: true},
		{name: "code 0 已登录", body: `{"code":0,"data":{"isLogin":true,"mid":42,"uname":"ry"}}`, wantName: "ry"},
		{name: "网络错误", netErr: errors.New("dial tcp: network unreachable"), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			api := biblib.New()
			api.Resty().SetTransport(emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
				if tc.netErr != nil {
					return nil, tc.netErr
				}
				return emptyStreamResponse(r, tc.body), nil
			}))
			d := &biliAuthDriver{lg: bilibiliLogin.New(api)}

			user, err := d.navUser(context.Background())
			if got := errors.Is(err, errSessionInvalid); got != tc.wantSess {
				t.Fatalf("errors.Is(err, errSessionInvalid)=%v，期望 %v（err=%v）", got, tc.wantSess, err)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v，期望有错误=%v", err, tc.wantErr)
			}
			if tc.wantName != "" && user.Name != tc.wantName {
				t.Fatalf("Name=%q，期望 %q", user.Name, tc.wantName)
			}
		})
	}
}

// TestBiliPollQRNilResult 看住扫码轮询的空返回。
//
// 库在 HTTP 200、code 0、data 缺失时返回 (nil, nil)；pollQR 直接读 poll.Code
// 就是后台 goroutine 里的 nil 解引用，panic 会带走整个进程。
func TestBiliPollQRNilResult(t *testing.T) {
	api := biblib.New()
	api.Resty().SetTransport(emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
		return emptyStreamResponse(r, `{"code":0,"data":null}`), nil
	}))
	d := &biliAuthDriver{lg: bilibiliLogin.New(api)}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("扫码轮询空返回导致 panic: %v", r)
		}
	}()
	if _, err := d.pollQR(context.Background(), "key"); err == nil {
		t.Fatal("空扫码状态应当返回错误")
	}
}
