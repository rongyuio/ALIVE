package live

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

// TestHuyaRoomsCachedSliceIsolated 盯住「缓存里的 Rooms 切片不能被调用方改坏」。
//
// cache 存 Page 时，页里的 Rooms 和缓存共用同一块底层数组：命中缓存后直接
// 交出去，调用方 append 或者改 Rooms[0] 写的就是缓存本身，下一次命中拿到的
// 就是脏数据。修后命中缓存先复制一份。
func TestHuyaRoomsCachedSliceIsolated(t *testing.T) {
	var calls int32
	transport := emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/liveHttpUI/getLiveList" {
			return nil, fmt.Errorf("unexpected mock request path: %s", r.URL.Path)
		}
		atomic.AddInt32(&calls, 1)
		return emptyStreamResponse(r, huyaListSample), nil
	})
	p := &huyaProvider{
		api:   &apiClient{platform: Huya, http: &http.Client{Transport: transport}, ua: desktopUA},
		rooms: newCache[Page](),
		lim:   newLimiter(2, 0),
	}

	ctx := context.Background()
	first, err := p.Rooms(ctx, "1", 1, 40)
	if err != nil {
		t.Fatalf("第一页取数失败: %v", err)
	}
	if len(first.Rooms) == 0 {
		t.Fatal("样本没有解析出房间")
	}
	const want = "男刀来了"
	if first.Rooms[0].Title != want {
		t.Fatalf("样本首条标题 = %q，期望 %q", first.Rooms[0].Title, want)
	}

	// 调用方改自己拿到的那一份。
	first.Rooms[0].Title = "被调用方改坏了"

	second, err := p.Rooms(ctx, "1", 1, 40)
	if err != nil {
		t.Fatalf("第二页取数失败: %v", err)
	}
	if got := second.Rooms[0].Title; got != want {
		t.Fatalf("第二次命中缓存读到 %q，缓存里的切片被调用方改坏了", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("上游请求 %d 次，第二次应命中缓存", got)
	}
}
