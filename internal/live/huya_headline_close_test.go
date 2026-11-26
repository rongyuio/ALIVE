package live

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// huyaTwoHeadlinesPayload 造一份含两条留言的 getHeadLineMessageBoard 响应。
func huyaTwoHeadlinesPayload() []byte {
	w := newHuyaTarsWriter()
	w.putHead(0, huyaTarsStructBegin) // 服务端外套的一层
	w.putHead(1, huyaTarsStructBegin) // tMessageBoardPanel
	w.putHead(1, huyaTarsList)        // vGameEventMessageBoardInfo
	w.writeInt32(0, 2)
	for i, id := range []int64{11, 12} {
		w.putHead(0, huyaTarsStructBegin)
		w.putHead(0, huyaTarsStructBegin) // tMessageUser
		w.writeString(1, fmt.Sprintf("观众%d", i+1))
		w.putHead(0, huyaTarsStructEnd)
		w.writeString(1, fmt.Sprintf("留言%d", i+1))
		w.writeInt32(5, 42)    // iCountDown
		w.writeInt64(9, id)    // lMessageId
		w.writeInt32(11, 2)    // iLevel
		w.writeInt32(12, 3000) // iCostPay：分币
		w.putHead(0, huyaTarsStructEnd)
	}
	w.putHead(0, huyaTarsStructEnd) // 面板结束
	w.putHead(0, huyaTarsStructEnd) // 外套结束
	return w.bytes()
}

// huyaWupHTTPBody 把一份业务响应封成 Wup over HTTP 的响应体：
// [int32 总长][sBuffer: map{"tRsp": 业务响应}]。
func huyaWupHTTPBody(payload []byte) []byte {
	sbuf := newHuyaTarsWriter()
	sbuf.putHead(0, huyaTarsMap)
	sbuf.writeInt32(0, 1)
	sbuf.writeString(0, "tRsp")
	sbuf.writeBytes(1, payload)

	packet := newHuyaTarsWriter()
	packet.writeBytes(7, sbuf.bytes())
	body := packet.bytes()

	total := uint32(len(body) + 4)
	out := []byte{byte(total >> 24), byte(total >> 16), byte(total >> 8), byte(total)}
	return append(out, body...)
}

// huyaWupHTTPTransport 固定回同一份 Wup 响应，不联网。
type huyaWupHTTPTransport struct{ body []byte }

func (t huyaWupHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/octet-stream"}},
		Body:       io.NopCloser(bytes.NewReader(t.body)),
		Request:    r,
	}, nil
}

// TestHuyaHeadlineStopsAfterClose 盯住「Close 之后不会再有留言回调」。
//
// 修前 emit 循环只看 fetch 的 error、不看 ctx，且 Close 不等轮询 goroutine：
// 在第一条留言的回调里 Close，第二条照样会打上来。修后每条 emit 前检查
// ctx，Close 等轮询真正退出。
func TestHuyaHeadlineStopsAfterClose(t *testing.T) {
	var (
		calls      int32
		afterClose int32
		closed     int32
	)

	s := &huyaDanmaku{
		provider: &huyaProvider{
			api: &apiClient{
				platform: Huya,
				http:     &http.Client{Transport: huyaWupHTTPTransport{body: huyaWupHTTPBody(huyaTwoHeadlinesPayload())}},
				ua:       desktopUA,
			},
			lim: newLimiter(2, 0),
		},
		channelID: 123,
		seenHead:  map[int64]struct{}{},
		done:      make(chan struct{}),
		pollDone:  make(chan struct{}),
	}
	// 这条测试只跑「上头条」轮询，弹幕主循环不启动，直接标它已退出，
	// 免得 Close 在 s.done 上白等满超时。
	close(s.done)
	s.onMessage = func(Danmu) {
		if atomic.AddInt32(&calls, 1) == 1 {
			s.Close()
			atomic.StoreInt32(&closed, 1)
			return
		}
		if atomic.LoadInt32(&closed) == 1 {
			atomic.AddInt32(&afterClose, 1)
		}
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())

	go s.pollHeadlines()
	select {
	case <-s.pollDone:
	case <-time.After(15 * time.Second):
		t.Fatal("Close 之后留言轮询没有退出")
	}

	if got := atomic.LoadInt32(&afterClose); got != 0 {
		t.Fatalf("Close 之后又回调了 %d 次", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("回调 %d 次，期望 1 次：第二条应在 Close 之后被拦住", got)
	}
}
