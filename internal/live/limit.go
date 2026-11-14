package live

import (
	"context"
	"sync"
	"time"
)

// limiter 控制一个平台的请求节奏：最多若干并发，两次请求之间至少间隔 gap；
// 被风控后进入冷却期，冷却结束前所有请求都在这里等。
type limiter struct {
	gap time.Duration
	sem chan struct{}

	mu   sync.Mutex
	next time.Time // 下一次可以发请求的时间
	cool time.Time // 风控冷却截止时间
}

func newLimiter(concurrency int, gap time.Duration) *limiter {
	if concurrency < 1 {
		concurrency = 1
	}
	return &limiter{gap: gap, sem: make(chan struct{}, concurrency)}
}

// acquire 等到可以发请求，返回的 release 必须在请求结束后调用。
func (l *limiter) acquire(ctx context.Context) (func(), error) {
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	for {
		l.mu.Lock()
		wait := time.Until(l.next)
		if until := time.Until(l.cool); until > wait {
			wait = until
		}
		l.mu.Unlock()
		if wait <= 0 {
			break
		}
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-l.sem
			return nil, ctx.Err()
		}
	}

	l.mu.Lock()
	l.next = time.Now().Add(l.gap)
	l.mu.Unlock()
	return func() { <-l.sem }, nil
}

// block 让这个平台进入冷却期（被风控时调用）。
func (l *limiter) block(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if until := time.Now().Add(d); until.After(l.cool) {
		l.cool = until
	}
}
