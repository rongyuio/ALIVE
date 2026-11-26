package live

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// douyuLeakyTransport 在假 transport 外面记并发。
//
// 斗鱼一次 Stream 会并发打「档位 × 线路」十几个取流请求，这些请求都必须
// 从 limiter 的闸门过；不过闸门时同时在飞的数量就是档位数×线路数。
type douyuLeakyTransport struct {
	douyuFakeTransport
	delay       time.Duration
	inFlight    int32
	maxInFlight int32
}

func (t *douyuLeakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cur := atomic.AddInt32(&t.inFlight, 1)
	for {
		old := atomic.LoadInt32(&t.maxInFlight)
		if cur <= old || atomic.CompareAndSwapInt32(&t.maxInFlight, old, cur) {
			break
		}
	}
	defer atomic.AddInt32(&t.inFlight, -1)

	if t.delay > 0 {
		time.Sleep(t.delay)
	}
	return t.douyuFakeTransport.RoundTrip(req)
}

// TestDouyuStreamRequestsObeyLimiter 盯住「取流请求走限流」这条不变量。
//
// 修前：douyuPlayData / douyuSign 里的上游请求完全不碰 p.lim，一次 Stream
// 的 9 个请求同时飞出去。修后：每个请求先 acquire 再发，同时在飞 ≤ limiter
// 并发。档位 × 线路的枚举语义不变，请求数仍是 9。
func TestDouyuStreamRequestsObeyLimiter(t *testing.T) {
	f := &douyuLeakyTransport{delay: 15 * time.Millisecond}
	f.applied = func(requested int, _ string) (int, bool) { return requested, true }
	p := newDouyuTestProvider(f)
	p.lim = newLimiter(2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Stream(ctx, "123", ""); err != nil {
		t.Fatalf("取流失败: %v", err)
	}

	if got := atomic.LoadInt32(&f.maxInFlight); got > 2 {
		t.Fatalf("同时在飞 %d 个取流请求，超过 limiter 并发 2", got)
	}
	if got := atomic.LoadInt32(&f.maxInFlight); got < 2 {
		t.Fatalf("最大并发只有 %d，请求没有真的并发跑，测不到闸门", got)
	}
	if got := f.playCount(); got != 9 {
		t.Errorf("取流请求 = %d，档位×线路应仍是 9", got)
	}
}

// douyuStreamBlockedTransport 让房间详情正常、其余上游请求一律 403。
//
// 只有取流路径回 403 才能单独验证「403 进冷却」：房间详情本来就走 wrap。
type douyuStreamBlockedTransport struct{}

func (douyuStreamBlockedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/betard/") {
		return douyuTestResponse(douyuTestRoomBody), nil
	}
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader("forbidden")),
		Request:    req,
	}, nil
}

// TestDouyuStreamBlockedEntersCooldown 盯住「403 要让平台冷下来」。
//
// 修前 403 只是被原样上抛（错误文本里带 ErrBlocked），limiter 不知道，
// 下一次刷新继续齐发撞风控。修后错误经 p.wrap 归档，limiter 进入冷却。
func TestDouyuStreamBlockedEntersCooldown(t *testing.T) {
	p := newDouyuTestProvider(douyuStreamBlockedTransport{})
	p.lim = newLimiter(2, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.Stream(ctx, "123", "")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("取流 403 应归类为 ErrBlocked，实际: %v", err)
	}

	p.lim.mu.Lock()
	cool := time.Until(p.lim.cool)
	p.lim.mu.Unlock()
	if cool <= 0 {
		t.Fatal("取流 403 之后 limiter 没有进冷却")
	}
	if cool > douyuBlockCool+time.Second {
		t.Fatalf("冷却时长 %v 超过 douyuBlockCool", cool)
	}
}
