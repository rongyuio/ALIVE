package live

import (
	"sync"
	"time"
)

// cache 是带 TTL 的缓存，并合并同一 key 的并发请求：
// 切分区时多个 goroutine 同时要同一份数据，只会真的打一次上游。
type cache[T any] struct {
	mu       sync.Mutex
	items    map[string]cacheItem[T]
	inflight map[string]*flight[T]
}

type cacheItem[T any] struct {
	val T
	exp time.Time
}

type flight[T any] struct {
	done chan struct{}
	val  T
	err  error
}

func newCache[T any]() *cache[T] {
	return &cache[T]{
		items:    map[string]cacheItem[T]{},
		inflight: map[string]*flight[T]{},
	}
}

// drop 丢掉一个 key。部分结果不能留在缓存里：下一次同一个 cursor 要真的
// 重试没取到的区间。
func (c *cache[T]) drop(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

func (c *cache[T]) load(key string, ttl time.Duration, fn func() (T, error)) (T, error) {
	c.mu.Lock()
	if item, ok := c.items[key]; ok && time.Now().Before(item.exp) {
		c.mu.Unlock()
		return item.val, nil
	}
	if f, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		<-f.done
		return f.val, f.err
	}
	f := &flight[T]{done: make(chan struct{})}
	c.inflight[key] = f
	c.mu.Unlock()

	f.val, f.err = fn()

	c.mu.Lock()
	if f.err == nil {
		// 只缓存成功结果：失败让下一次重试真的重试。
		c.items[key] = cacheItem[T]{val: f.val, exp: time.Now().Add(ttl)}
	}
	delete(c.inflight, key)
	c.mu.Unlock()
	close(f.done)

	return f.val, f.err
}
