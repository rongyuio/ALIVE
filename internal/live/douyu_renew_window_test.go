package live

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// douyuSwapTransport 在第 n 次取流请求的途中换掉 cookie。
//
// 复刻线上的时序：请求已经带着旧票发出去了，等 403 回来时另一路刚好续期
// 成功——此刻 authCookieString() 已经是新票，但这次失败用的是旧票。
type douyuSwapTransport struct {
	inner *douyuTicketTransport
	at    int // 第几次取流请求到达时换票（1 起）
	swap  func()

	mu    sync.Mutex
	count int
}

func (f *douyuSwapTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/lapi/live/getH5PlayV1/") {
		f.mu.Lock()
		f.count++
		now := f.count
		f.mu.Unlock()
		if now == f.at {
			f.swap()
		}
	}
	return f.inner.RoundTrip(req)
}

// TestDouyuLateRenewKeepsLoggedInRetry 看住「迟到的 403 不能把刚续好的票判死」。
//
// 用旧票发请求、请求途中另一路续期成功，403 才回来。此时 douyuRenewAuth
// 读到的 seen 已经是新票，它和当前 cookie 相同，于是撞上续期冷却、返回
// errDouyuRenewCooling。旧实现据此直接走游客兜底，并用 douyuMarkGuest 把
// 整个 provider 降级十分钟——账号明明已经续好，观感却是清晰度全没了。
//
// 正确判据是「失败请求用的票」与「现在的票」是否一致：不一致说明 403 是
// 针对旧票的，拿新票再试一次即可，既不该降级也不该记冷却。
func TestDouyuLateRenewKeepsLoggedInRetry(t *testing.T) {
	tr := &douyuTicketTransport{
		inner:  &douyuFakeTransport{applied: func(requested int, _ string) (int, bool) { return requested, true }},
		reject: douyuJWTName + "=old",
	}

	var p *douyuProvider
	// douyuPlayAttempts 对失败会自愈重试一次；把换票放在第二次请求途中，
	// 才能让两次尝试都带着旧票失败、而处理结果时票已经是新的。
	swap := &douyuSwapTransport{inner: tr, at: 2, swap: func() {
		p.setAuthCookie("acf_auth=fresh; " + douyuJWTName + "=fresh; dy_did=did-test; acf_did=did-test")
	}}
	p = newDouyuIsolatedProvider(t, swap)
	p.setAuthCookie(douyuStaleLoginCookie)

	renews := 0
	p.forceRenew = func(context.Context) error { renews++; return nil }

	// 另一路刚刚续期成功：renewAt 刚被写过，这个窗口里再续会撞冷却。
	p.renewMu.Lock()
	p.renewAt = time.Now()
	p.renewMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := p.douyuPlayData(ctx, "123", 0, "ws")
	if err != nil {
		t.Fatalf("新票可用时不该整站失败：%v", err)
	}
	if data == nil {
		t.Fatal("没有拿到取流数据")
	}
	if renews != 0 {
		t.Errorf("票据已经换过，不该再撞 passport：强制续期 %d 次", renews)
	}
	p.stateMu.Lock()
	guest := p.guestUntil
	p.stateMu.Unlock()
	if !guest.IsZero() {
		t.Error("续期已经成功，不该把 provider 降级成游客")
	}
	identities := tr.identities()
	if len(identities) < 3 {
		t.Fatalf("取流请求只有 %d 个，测试前提不成立", len(identities))
	}
	for i, login := range identities {
		if !login {
			t.Fatalf("第 %d 个取流请求降成了游客；应拿新票继续用登录态", i)
		}
	}
}
