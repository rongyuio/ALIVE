package live

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestStreamFetchPathsAcquireLimiter 盯住「取流请求也要过限流闸门」。
//
// 判据不是「有没有调用 acquire」而是可观察行为：limiter 已经在风控冷却里，
// 取流请求就必须在闸门上等到 ctx 到期，一个字节都不发出去。修前这三条
// 路径直接打上游，冷却形同虚设。
func TestStreamFetchPathsAcquireLimiter(t *testing.T) {
	noCall := emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
		t.Errorf("限流冷却中不该发出上游请求: %s %s", r.Method, r.URL)
		return nil, fmt.Errorf("不该发出请求")
	})
	cooling := func() *limiter {
		lim := newLimiter(1, 0)
		lim.block(time.Hour)
		return lim
	}
	withTimeout := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 120*time.Millisecond)
	}

	t.Run("虎牙取流档案", func(t *testing.T) {
		p := &huyaProvider{
			api: &apiClient{platform: Huya, http: &http.Client{Transport: noCall}, ua: desktopUA},
			lim: cooling(),
		}
		ctx, cancel := withTimeout()
		defer cancel()
		if _, err := p.huyaFetchStreamProfile(ctx, "1"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("应卡在闸门上直到 ctx 超时，实际: %v", err)
		}
	})

	t.Run("虎牙取流鉴权", func(t *testing.T) {
		p := &huyaProvider{
			api: &apiClient{platform: Huya, http: &http.Client{Transport: noCall}, ua: desktopUA},
			lim: cooling(),
		}
		ctx, cancel := withTimeout()
		defer cancel()
		if _, err := p.huyaCdnToken(ctx, "https://cdn.example/x", "stream"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("应卡在闸门上直到 ctx 超时，实际: %v", err)
		}
	})

	t.Run("抖音取流信息", func(t *testing.T) {
		p := newDouyin()
		p.api.http = &http.Client{Transport: noCall}
		p.lim = cooling()
		ctx, cancel := withTimeout()
		defer cancel()
		if _, err := p.douyinFetchStreamRoom(ctx, "1234567890123456789"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("应卡在闸门上直到 ctx 超时，实际: %v", err)
		}
	})
}
