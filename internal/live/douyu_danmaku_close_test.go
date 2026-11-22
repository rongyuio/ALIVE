package live

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// douyuTestConn 只记录是否被关闭；读永远立刻失败。
type douyuTestConn struct {
	closed atomic.Bool
}

func (c *douyuTestConn) Read([]byte) (int, error)    { return 0, net.ErrClosed }
func (c *douyuTestConn) Write(b []byte) (int, error) { return len(b), nil }
func (c *douyuTestConn) Close() error {
	c.closed.Store(true)
	return nil
}
func (c *douyuTestConn) LocalAddr() net.Addr              { return nil }
func (c *douyuTestConn) RemoteAddr() net.Addr             { return nil }
func (c *douyuTestConn) SetDeadline(time.Time) error      { return nil }
func (c *douyuTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *douyuTestConn) SetWriteDeadline(time.Time) error { return nil }

// TestDouyuSetConnAfterCloseClosesNewConn 看住 dial 与 Close 的竞争窗口。
//
// Close 的顺序是先 cancel 再 closeConn。若它恰好落在 douyuDial 成功返回、
// setConn 还没挂上去的那一瞬间，closeConn 看到的是 nil，什么都没关；随后
// 新连接被挂上、登录包照发，读循环也不知道 ctx 已经取消——只要房间还在推
// 消息，这个会话就会在 Close 返回之后继续收包、继续 onMessage，违反
// types.go 里「Close 之后不会再有任何回调」的约定，TCP 和 goroutine 也
// 一直不释放。
//
// 修法是在挂载与 ctx 检查的同一把锁里判定：取消后的新连接自己关掉。
func TestDouyuSetConnAfterCloseClosesNewConn(t *testing.T) {
	s := &douyuDanmaku{roomID: "1", done: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.cancel() // 模拟 Close 已经跑在 setConn 前面

	conn := &douyuTestConn{}
	s.setConn(conn) // 旧签名没有返回值，按语句调用才能让改动前后都编译

	if !conn.closed.Load() {
		t.Fatal("ctx 已取消，新连接必须被关闭，不能留给读循环")
	}
	s.mu.Lock()
	got := s.conn
	s.mu.Unlock()
	if got != nil {
		t.Fatal("ctx 已取消时不该把新连接挂到会话上")
	}
}

// TestDouyuSetConnStoresWhileActive 反向约束：会话还活着时连接必须正常挂上。
func TestDouyuSetConnStoresWhileActive(t *testing.T) {
	s := &douyuDanmaku{roomID: "1", done: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	t.Cleanup(s.cancel)

	conn := &douyuTestConn{}
	s.setConn(conn)

	if conn.closed.Load() {
		t.Fatal("会话还活着，不该关闭新连接")
	}
	s.mu.Lock()
	got := s.conn
	s.mu.Unlock()
	if got != conn {
		t.Fatal("新连接没有挂到会话上")
	}
}
