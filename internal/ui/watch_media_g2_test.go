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
// 于是 K 的取流结果回来仍被 applyMedia 接纳，在 K 的格子里按 a.Volume 出声，
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

	// K 已经被接管进媒体层，正在重新加载：房间记号在、引擎还没有。
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	app.media.roomKey = keyA
	app.media.loading = true
	app.media.player = nil
	app.media.ctx, app.media.cancel = ctx, cancel
	app.media.seq = 7
	app.WatchActive = keyA
	app.Player = rooms[0]
	app.Playing = true
	app.Volume = 0.6
	app.watch.init()
	app.watch.sessions[keyA] = &wallSession{key: keyA, room: rooms[0], loading: true}

	// 这一轮取流交给后台时打包出来的凭据。
	pending := playbackSession{seq: 7, ctx: ctx, id: "取流#7", start: time.Now()}

	app.watchActivate(rooms[1])

	if app.media.roomKey == keyA {
		t.Fatalf("激活另一格之后媒体层还握着 %q：这一轮取流没有作废", keyA)
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
	if app.WatchActive != keyB {
		t.Fatalf("焦点是 %q，期望 %q", app.WatchActive, keyB)
	}
}

// 缺陷 B：进播放页看另一间时，不能顺手关掉刚交还墙上的那一间。
//
// watchDropActive 先 closeMedia(A)，closeMedia 因 A 在盒里把引擎交还成墙上
// 的静音预览；紧接着它又无条件 watchCloseSession(sessions[A])，把刚交还的
// 会话连引擎一起删了。A 回墙要重新取流，违反 surfaces.md「在盒里 → 静音保留」。
func TestWatchDropActiveKeepsSessionInBox(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	keyA := watchKey(rooms[0])
	first := fakeSession(t, app, rooms[0])
	fakeSession(t, app, rooms[1])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.media.player != first.player {
		t.Fatal("墙上激活之后引擎没有进媒体层")
	}

	// 回首页，再从列表进另一间的播放页：PlayerPage 会调 watchDropActive。
	oldProv := providerFor
	providerFor = func(live.Platform) (live.Provider, bool) { return plainProvider{}, true }
	t.Cleanup(func() { providerFor = oldProv })

	app.Router.Push(routeHome)
	app.watchHide()
	tt.Frame()
	app.Player = rooms[1]
	app.Router.Push(routePlayer)
	tt.Frame()

	s := app.watch.sessions[keyA]
	if s == nil {
		t.Fatal("进另一间播放页之后，盒里 A 的会话被删掉了")
	}
	if s.player != first.player {
		t.Fatal("A 的会话换了引擎：原来那一枚被关掉了")
	}
	if !first.player.Playing() {
		t.Fatal("A 的引擎被关闭了：回墙要重新取流")
	}
	if first.player.Volume() != 0 {
		t.Fatalf("A 还在出声：volume=%v", first.player.Volume())
	}
}

// 缺陷 C：watchReleaseActive 覆盖 map 条目时必须关掉旧会话。
//
// 直接覆盖会把旧条目连同引擎一起漏掉，墙和媒体层都不再持有它，没有任何
// 地方能再关。正常路径由 G1 从 ensureMedia 那一侧堵住，这里是兜底。
func TestWatchReleaseActiveClosesOverwrittenSession(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	rooms := watchTestRooms(1)
	app := NewMockApp()
	app.WatchRooms = rooms
	key := watchKey(rooms[0])

	app.watch.init()
	app.watch.rects[key] = watch.Rect{W: 640, H: 360}
	stale := &wallSession{key: key, room: rooms[0], player: gifPlayer(t)}
	stalePlayer := stale.player
	app.watch.sessions[key] = stale

	held := gifPlayer(t)
	app.media.player = held
	app.media.roomKey = key

	app.watchReleaseActive()

	got := app.watch.sessions[key]
	if got == nil || got.player != held {
		t.Fatal("交还失败：墙上没有拿到媒体层那一枚引擎")
	}
	if stalePlayer.Playing() {
		t.Fatal("旧条目被直接覆盖，它的引擎还活着：没有任何地方能再关它")
	}
}
