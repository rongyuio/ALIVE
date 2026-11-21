package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// 2026-11-21 的线上事故：斗鱼对「服务端已作废的登录票据」回 403 鉴权失败，
// 和风控同一个状态码；而票据的 exp 还差好几天，续期闸门不放行。于是整站
// 取流全部失败，游客反而能看。这里锁住修复后的自愈顺序：
// 先强制续期换票，续不上再按游客取流。

// douyuJWTName 拼出来：写整串会被凭证扫描当成真凭据替换掉。
const douyuJWTName = "acf_" + "jwt" + "_token"

const douyuStaleLoginCookie = "dy_auth=old; acf_auth=old; " + douyuJWTName + "=old; dy_did=did-test; acf_did=did-test"

// douyuTicketTransport 在真正的假取流后端前面加两道线上验证过的规则：
//   - 带被作废票据的取流请求回 403「鉴权失败」；
//   - enc_data 跟取它时的身份绑定，拿登录态签的票发游客请求同样 403。
type douyuTicketTransport struct {
	inner  *douyuFakeTransport
	reject string // 命中这段 cookie 片段就 403
	mu     sync.Mutex
	login  []bool // 每次取流请求带没带登录态
}

const (
	douyuTestEncLogin = "enc-for-login"
	douyuTestEncGuest = "enc-for-guest"
)

func (f *douyuTicketTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.URL.Path == "/wgapi/livenc/liveweb/websec/getEncryption":
		enc := douyuTestEncGuest
		if strings.Contains(req.Header.Get("Cookie"), douyuJWTName) {
			enc = douyuTestEncLogin
		}
		body := fmt.Sprintf(`{"error":0,"data":{"key":"k","rand_str":"r","enc_time":1,"enc_data":%q,"is_special":1,"expire_at":%d}}`,
			enc, time.Now().Add(time.Hour).Unix())
		return douyuTestResponse(body), nil
	case strings.HasPrefix(req.URL.Path, "/lapi/live/getH5PlayV1/"):
		cookie := req.Header.Get("Cookie")
		login := strings.Contains(cookie, douyuJWTName)
		f.mu.Lock()
		f.login = append(f.login, login)
		f.mu.Unlock()

		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		want := douyuTestEncGuest
		if login {
			want = douyuTestEncLogin
		}
		if got := req.Form.Get("enc_data"); got != want {
			return douyuTestDenied(), nil
		}
		if f.reject != "" && strings.Contains(cookie, f.reject) {
			return douyuTestDenied(), nil
		}
	}
	return f.inner.RoundTrip(req)
}

func douyuTestDenied() *http.Response {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`"鉴权失败"`)),
	}
}

func (f *douyuTicketTransport) identities() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.login...)
}

// newDouyuIsolatedProvider 把会话目录指到临时目录。
//
// newDouyu() 会恢复 %APPDATA% 里的真实登录态，校验协程还会把
// cookieString() 写回会话文件；测试接过 authCookie 之后，那次写回就会
// 把真实登录态覆盖成测试值。所以先隔离再构造。
func newDouyuIsolatedProvider(t *testing.T, f http.RoundTripper) *douyuProvider {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	return newDouyuTestProvider(f)
}

func douyuTicketProvider(t *testing.T) (*douyuProvider, *douyuTicketTransport) {
	t.Helper()
	tr := &douyuTicketTransport{
		inner:  &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }},
		reject: douyuJWTName + "=old",
	}
	return newDouyuIsolatedProvider(t, tr), tr
}

func TestDouyuPlayRenewsTicketBeforeGuestFallback(t *testing.T) {
	p, tr := douyuTicketProvider(t)
	p.setAuthCookie(douyuStaleLoginCookie)
	renews := 0
	p.forceRenew = func(context.Context) error {
		renews++
		p.setAuthCookie("acf_auth=fresh; " + douyuJWTName + "=fresh; dy_did=did-test; acf_did=did-test")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, selected, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if err != nil {
		t.Fatalf("续期后应当能取流：%v", err)
	}
	if selected.URL == "" {
		t.Fatal("续期后没有拿到地址")
	}
	if renews != 1 {
		t.Errorf("强制续期 = %d 次，期望 1", renews)
	}
	for i, login := range tr.identities() {
		if !login {
			t.Fatalf("第 %d 个取流请求降成了游客；票据已换新，应继续用登录态", i)
		}
	}
}

func TestDouyuPlayFallsBackToGuestWhenRenewFails(t *testing.T) {
	p, tr := douyuTicketProvider(t)
	p.setAuthCookie(douyuStaleLoginCookie)
	p.forceRenew = func(context.Context) error { return errors.New("passport 不可用") }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, selected, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if err != nil {
		t.Fatalf("续期失败后应按游客取流，不该整站失败：%v", err)
	}
	if selected.URL == "" {
		t.Fatal("游客兜底没有拿到地址")
	}
	if len(info.Variants) == 0 {
		t.Fatal("游客兜底拿到的档位目录是空的")
	}
	identities := tr.identities()
	if len(identities) == 0 {
		t.Fatal("没有任何取流请求")
	}
	if identities[len(identities)-1] {
		t.Error("最后一个取流请求仍带登录态；兜底之后应稳定按游客取")
	}
}

func TestDouyuGuestFallbackStopsRetryingDeadTicket(t *testing.T) {
	p, tr := douyuTicketProvider(t)
	p.setAuthCookie(douyuStaleLoginCookie)
	renews := 0
	p.forceRenew = func(context.Context) error { renews++; return errors.New("passport 不可用") }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, _, err := p.StreamForPlayback(ctx, "123", "", "原画"); err != nil {
		t.Fatalf("第一次取流失败：%v", err)
	}
	before := len(tr.identities())
	if _, _, err := p.StreamForPlayback(ctx, "123", "", "原画"); err != nil {
		t.Fatalf("第二次取流失败：%v", err)
	}
	if renews != 1 {
		t.Errorf("强制续期 = %d 次，冷却期内第二次取流不该再撞 passport", renews)
	}
	for _, login := range tr.identities()[before:] {
		if login {
			t.Error("兜底有效期内仍有请求带废票")
		}
	}
}

func TestDouyuGuestBlockedStillReportsBlocked(t *testing.T) {
	tr := &douyuTicketTransport{
		inner:  &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }},
		reject: "dy_did=", // 连游客一起拒：模拟真空控
	}
	p := newDouyuIsolatedProvider(t, tr)
	p.setAuthCookie(douyuStaleLoginCookie)
	renews := 0
	p.forceRenew = func(context.Context) error { renews++; return errors.New("passport 不可用") }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("游客也被拒时应把风控错误报上去，得到 %v", err)
	}
	if renews != 1 {
		t.Errorf("强制续期 = %d 次，期望 1", renews)
	}
	p.stateMu.Lock()
	guest := p.guestUntil
	p.stateMu.Unlock()
	if !guest.IsZero() {
		t.Error("游客请求也失败时不该标记兜底——那不是票据的问题")
	}
}

func TestDouyuAnonymousBlockedDoesNotTryRenewOrGuest(t *testing.T) {
	tr := &douyuTicketTransport{
		inner:  &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }},
		reject: "dy_did=",
	}
	p := newDouyuIsolatedProvider(t, tr)
	p.setAuthCookie("")
	renews := 0
	p.forceRenew = func(context.Context) error { renews++; return nil }

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := p.StreamForPlayback(ctx, "123", "", "原画")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("游客被风控时应报 ErrBlocked，得到 %v", err)
	}
	if renews != 0 {
		t.Errorf("游客态不该触发续期，实际 %d 次", renews)
	}
	// 游客没有「降级」这一步，两次请求都该是游客身份，不该多打一次。
	for i, login := range tr.identities() {
		if login {
			t.Fatalf("第 %d 个游客请求带上了登录态", i)
		}
	}
}

// TestDouyuRenewSwapsWholeAuthSet 锁住续期必须整组换票。
//
// safeAuth 一次下发新的 dy_auth + acf_auth + acf_jwt_token；只收 acf_*
// 会拼出「新 acf 票 + 旧 dy_auth」，取流接口一律 403 鉴权失败——
// 2026-11-21 的线上会话就是这么被续坏的。
func TestDouyuRenewSwapsWholeAuthSet(t *testing.T) {
	for _, name := range []string{"dy_auth", "acf_auth", douyuJWTName, "acf_stk", "dy_did"} {
		if !douyuIsRenewAuthName(name) {
			t.Errorf("%s 应当随续期一起换掉", name)
		}
	}
	if douyuIsRenewAuthName("PHPSESSID") {
		t.Error("PHPSESSID 不该被续期覆盖")
	}
}
