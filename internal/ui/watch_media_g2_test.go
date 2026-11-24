package ui

import (
	"context"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"alive/internal/live"
	"alive/internal/watch"
)

// G2：观测墙会话交接的三条缺陷。每条都在修复前跑红。
//
// 这批测试只碰所有权，不碰取流：需要"正在取流"的中间态时直接摆出
// media.loading=true / player=nil 的样子，用 applyMedia 验旧结果还认不认。

// plainProvider 只实现 live.Provider。
//
// 用来让 startStream 在"这个平台不支持取流"那一句同步返回：测试不该为了
// 摆一个媒体层状态而真的派一个取流 goroutine 出去。
type plainProvider struct{}

func (plainProvider) Platform() live.Platform { return live.Bilibili }
func (plainProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (plainProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (plainProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

// 缺陷 A：active 正在重新加载时点另一格，旧的那一轮取流必须作废。
//
// 复现的是"媒体层握着 K、引擎还没出来"。watchActivate 调 watchReleaseActive，
// 而后者在 player==nil 时直接返回：不 cancel、不推进 seq、不清 roomKey。
// 于是 K 的取流结果回来仍被 applyMedia 接纳，在 K 的格子里按 app.focusVolume() 出声，
// 焦点却已经在 C 上，墙上控件也关不掉它。
func TestWatchActivateInvalidatesPendingActiveFetch(t *testing.T) {
	rooms := watchTestRooms(2)
	app, _ := newWatchMock(t, rooms)
	keyA, keyB := watchKey(rooms[0]), watchKey(rooms[1])

	// applyMedia 的接纳判断要在主线程回调里跑，这里让"主线程"同步执行。
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		fn()
		return true
	}
	t.Cleanup(func() { runOnMain = oldRun })

	// K 是焦点，正在重新加载：房间在、引擎还没有。
	a := focusForTest(app, rooms[0])
	a.loading = true
	app.setVolume(0.6)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	app.media.ctx, app.media.cancel = ctx, cancel
	app.media.seq = 7

	// 这一轮取流交给后台时打包出来的凭据。
	pending := playbackSession{seq: 7, ctx: ctx, id: "取流#7", start: time.Now()}

	app.watchActivate(rooms[1])

	if app.focusKey() == keyA {
		t.Fatalf("激活另一格之后焦点还是 %q：这一轮取流没有作废", keyA)
	}
	if ctx.Err() == nil {
		t.Fatal("旧取流没有被取消：结果回来还会被当成当前这一轮")
	}
	if app.media.seq == pending.seq {
		t.Fatalf("seq 没推进（还是 %d）：旧结果回来仍会被接纳", pending.seq)
	}

	// 旧取流的结果回来了：必须被判成过期。
	accepted, staled := false, false
	app.applyMedia(pending, func() { accepted = true }, func() { staled = true })
	if accepted {
		t.Fatal("旧取流的结果仍被接纳：它会在旧格子里出声")
	}
	if !staled {
		t.Fatal("旧结果既没被接纳也没被判过期：资源没有回收路径")
	}
	if app.focusKey() != keyB {
		t.Fatalf("焦点是 %q，期望 %q", app.focusKey(), keyB)
	}
}

// 换焦点不能关掉盒里那一间：A 在墙上还要继续做静音预览。
//
// 以前 watchDropActive 先 closeMedia(A) 交还引擎，紧接着又无条件
// watchCloseSession 把它删了，A 回墙要重新取流。现在换焦点只经过
// setFocus 一个写者：在盒里 → 静音留着，不在盒里 → 关掉。
func TestWatchDropActiveKeepsSessionInBox(t *testing.T) {
	rooms := watchTestRooms(2)
	app, _ := newWatchMock(t, rooms)
	a := fakeSession(t, app, rooms[0])
	focusForTest(app, rooms[0])

	app.openRoom(rooms[1])

	if app.focusKey() != watchKey(rooms[1]) {
		t.Fatalf("焦点没有换到第二间：%q", app.focusKey())
	}
	got := app.watch.sessions[watchKey(rooms[0])]
	if got != a {
		t.Fatal("换焦点之后 A 的会话不在册了：它还在盒里")
	}
	if a.player == nil {
		t.Fatal("换焦点之后 A 的引擎被关了：它该留在墙上做静音预览")
	}
}

// 关会话必须把引擎一起关掉，并把焦点清干净。
//
// 以前 watchReleaseActive 直接覆盖 map 条目，旧条目连同引擎一起漏掉，
// 墙和媒体层都不再持有它，没有任何地方能再关。现在销毁只有 closeSession
// 一个入口，删表项前先 Close。
func TestCloseSessionClosesEngine(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	rooms := watchTestRooms(1)
	app := NewMockApp()
	app.WatchRooms = rooms
	key := watchKey(rooms[0])

	app.watch.init()
	app.watch.rects[key] = watch.Rect{W: 640, H: 360}
	s := &roomSession{key: key, room: rooms[0], player: gifPlayer(t)}
	focusSessionForTest(app, s)
	p := s.player

	app.closeSession(s)

	if app.watch.sessions[key] != nil {
		t.Fatal("关掉会话之后它还在表里")
	}
	if app.focusKey() != "" {
		t.Fatalf("关掉会话之后焦点还是 %q", app.focusKey())
	}
	if s.player != nil {
		t.Fatal("关掉会话之后会话里还挂着播放器")
	}
	if p.Playing() {
		t.Fatal("关掉会话之后引擎还在播")
	}
}
