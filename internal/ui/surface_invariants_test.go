package ui

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
	"alive/internal/watch"
)

// 表面模型的七条不变量。完整表述见 doc/architecture/surfaces.md。
//
// 这批测试是阶段 2（合并所有权）的护栏：它们先在改动前的代码上跑红，
// 红出来的每一条都对应一个真实缺陷。修完之后它们必须全绿，并且以后
// 每加一条路径都要能被它们挡住。

// fakeSession 在墙的会话表里放一个"已经出画"的格子，带一个纯 Go 播放器。
//
// 不经过取流：这批测试验的是所有权与表面，不是网络。音量必须和真实开格
// 一致从 0 起（watchOpenSession 里就是 SetVolume(0)），否则"至多一路出声"
// 的判据会被夹具自己的初值污染。
func fakeSession(t *testing.T, a *App, room Streamer) *wallSession {
	t.Helper()
	a.watch.init()
	p := gifPlayer(t)
	p.SetVolume(0)
	s := &wallSession{
		key: watchKey(room), room: room,
		player: p, track: &trackSpy{},
		firstFrame: true, renderW: 640, renderH: 360,
	}
	a.watch.sessions[s.key] = s
	return s
}

// audibleCount 数此刻真正在出声的播放器：音量 > 0。
//
// 这是"至多一路出声"唯一的判据来源：不看 a.Volume、不看 WatchActive，
// 直接问播放器。状态字段可以撒谎，音量不会。
func audibleCount(a *App) int {
	n := 0
	seen := map[*ui.VideoPlayer]bool{}
	if p := a.media.player; p != nil && !seen[p] {
		seen[p] = true
		if p.Volume() > 0 {
			n++
		}
	}
	for _, s := range a.watch.sessions {
		if s.player == nil || seen[s.player] {
			continue
		}
		seen[s.player] = true
		if s.player.Volume() > 0 {
			n++
		}
	}
	return n
}

// stubEngines 让 engineOf 把测试里的纯 Go 播放器当成真引擎。
//
// 生产实现是类型断言到 *playback.Engine；测试里的播放器是 GIF 解码的，
// 断言必然失败，于是 attachMedia 会走"没有引擎，整路重开"那条分支——
// 那不是要验的路径。
func stubEngines(t *testing.T) *trackSpy {
	t.Helper()
	spy := &trackSpy{}
	old := engineOf
	engineOf = func(p *ui.VideoPlayer) videoTrack {
		if p == nil {
			return nil
		}
		return spy
	}
	t.Cleanup(func() { engineOf = old })
	return spy
}

// wallSessionOf 返回墙为这一间持有的会话（不论有没有播放器）。
func wallSessionOf(a *App, key string) *wallSession { return a.watch.sessions[key] }

// 不变量 1：至多一路出声。
func TestInvariantAtMostOneAudible(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()

	// 逐个换声音焦点（墙上唯一不跳路由的入口），每一步之后都必须只有一路出声。
	for i, r := range rooms {
		listenOnWall(t, tt, r)
		if n := audibleCount(app); n > 1 {
			t.Fatalf("激活第 %d 格之后有 %d 路在出声", i, n)
		}
	}
}

// 不变量 2：出声必有播放器，且焦点指向的那一间真的在出声。
func TestInvariantAudibleImpliesFocus(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	fakeSession(t, app, rooms[1])
	tt.Frame()

	listenOnWall(t, tt, rooms[0])

	key := app.focusKey()
	if key != watchKey(rooms[0]) {
		t.Fatalf("焦点是 %q，期望 %q", key, watchKey(rooms[0]))
	}
	p := app.watchPlayer(key)
	if p == nil {
		t.Fatal("焦点指向的会话没有播放器")
	}
	if p.Volume() <= 0 {
		t.Fatalf("焦点指向的会话音量是 %v，没有在出声", p.Volume())
	}
}

// 不变量 3：一个房间至多一个引擎。
//
// 这条直接对应缺陷 1：列表进播放页 → 顶栏回首页 → 进墙，同一间房会被
// 媒体层和墙的会话各持一份。判据是"墙不该为媒体层已经持有的房间再建会话"。
func TestInvariantOneEnginePerRoom(t *testing.T) {
	rooms := watchTestRooms(1)
	app := NewMockApp()
	key := watchKey(rooms[0])
	app.WatchRooms = rooms

	// 模拟"从列表进过播放页再离开"：媒体层持有这一间，在后台出声。
	app.media.roomKey = key
	app.media.player = gifPlayer(t)
	app.media.background = true
	app.Player = rooms[0]
	app.Playing = true
	app.Volume = 0.6

	// 进墙。直接调 watchShow：这条验的是"墙要不要为这一间建会话"，
	// 不需要跑整棵视图树（真实窗口 + 定时器会让 Tester 的 settle 转不停）。
	app.Win = &mygo.Window{}
	oldNative, oldRun, oldProv := openNative, runOnMain, providerFor
	openNative = func(_ context.Context, _ string, _ playback.Options) (*playback.Engine, error) {
		return nil, playback.ErrUnsupported
	}
	runOnMain = func(*mygo.Window, func()) bool { return true }
	providerFor = func(live.Platform) (live.Provider, bool) { return surfaceProvider{}, true }
	t.Cleanup(func() { openNative, runOnMain, providerFor = oldNative, oldRun, oldProv })

	app.Router = ui.NewRouter(routeFollows)
	app.FollowsSection = followsSectionWatch
	app.watchShow(map[string]watch.Rect{key: {W: 640, H: 360}})

	if s := wallSessionOf(app, key); s != nil {
		t.Fatal("墙上为媒体层已经持有的房间又建了一个会话：同一间房两个所有者")
	}
}

// 不变量 4：从墙放大进播放页，按 X 结束焦点，但房间还在盒里 → 会话存活。
//
// 对应缺陷 2：回到墙上这一格黑了，要重新取流。
func TestInvariantCloseKeepsSessionInBox(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	key := watchKey(rooms[0])
	sess := fakeSession(t, app, rooms[0])
	tt.Frame()

	// 双击进播放页（两次 Click 就是一次双击）。
	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("双击没有进播放页，route=%q", app.Router.Path())
	}
	if app.media.player != sess.player {
		t.Fatal("进播放页之后媒体层拿到的不是墙上那一路")
	}

	// 按 X：结束焦点。
	app.closeMedia()

	if !app.watchHas(key) {
		t.Fatal("房间还在盒里，watchHas 却是假")
	}
	if app.watchPlayer(key) == nil {
		t.Fatal("按 X 之后墙上这一格没有任何画面：会话被关掉了")
	}
}

// 不变量 5：墙上换焦点，上一间静音保留，不关闭。
//
// 对应缺陷 3。
func TestInvariantFocusSwitchKeepsPrevious(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	first := fakeSession(t, app, rooms[0])
	second := fakeSession(t, app, rooms[1])
	tt.Frame()

	listenOnWall(t, tt, rooms[0])
	if app.media.player != first.player {
		t.Fatal("第一间没有进媒体层")
	}

	listenOnWall(t, tt, rooms[1])

	if app.watchPlayer(watchKey(rooms[0])) != first.player {
		t.Fatal("换焦点之后上一间的播放器不见了：它还在盒里")
	}
	if app.watchPlayer(watchKey(rooms[1])) != second.player {
		t.Fatal("新的焦点没有拿到第二间的播放器")
	}
	if audibleCount(app) > 1 {
		t.Fatalf("换焦点之后有 %d 路在出声", audibleCount(app))
	}
}

// 不变量 6：墙不可见后，会话在保留窗口内不关闭。
//
// 对应缺陷 4：去首页看一眼再回来，整面墙重新 buffering。
func TestInvariantWallHideKeepsSessionsWithinWindow(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()

	// 离开墙：去首页。
	app.Router.Push(routeHome)
	app.watchHide()
	tt.Frame()

	kept := 0
	for _, room := range rooms {
		if app.watchPlayer(watchKey(room)) != nil {
			kept++
		}
	}
	if kept != len(rooms) {
		t.Fatalf("离开墙之后只剩 %d/%d 路会话：保留窗口内不该关闭", kept, len(rooms))
	}
}

// 不变量 7：弹幕只属于全尺寸视图。
func TestInvariantDanmakuOnlyOnFullSurface(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	key := watchKey(rooms[0])
	fakeSession(t, app, rooms[0])
	tt.Frame()

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := app.surfaceFor(key); got != surfaceFull {
		t.Fatalf("进播放页后表面是 %v，期望 full", got)
	}
	if app.media.danmaku != nil {
		t.Fatal("墙上就有弹幕连接：弹幕只属于全尺寸视图")
	}

	// 离开：弹幕必须断。
	app.Router.Push(routeHome)
	app.detachMedia()
	tt.Frame()
	if app.media.danmaku != nil {
		t.Fatal("离开播放页之后弹幕连接还在")
	}
}

// 岛形态与实际声音归属一致：焦点非空时岛上必须是那一间，不能缩成时钟。
func TestInvariantIslandMatchesFocus(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	// 媒体层持有另一间（不在盒里），在后台出声。
	app.media.roomKey = "bilibili|9999"
	app.media.player = gifPlayer(t)
	app.media.player.SetVolume(0.6)
	app.media.background = true
	app.Player = Streamer{Platform: "bilibili", RoomID: "9999", Name: "盒子外的主播"}
	app.Playing = true
	app.Volume = 0.6

	if got := app.focusKey(); got != "bilibili|9999" {
		t.Fatalf("焦点是 %q，期望 bilibili|9999", got)
	}
	if got := app.islandFormOf(); got != islandListening {
		t.Fatalf("声音在响，岛形态是 %v，期望 listening", got)
	}

	// 界面上的岛必须能被点回那一间，而不是停在时钟。
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find("回到直播间"); !ok {
		t.Fatalf("有声音焦点时岛没有回直播间的热区：被画成了时钟")
	}
}

// 静态检查：呈现尺寸只能从 presentSizeFor / applyPresent 推给引擎。
//
// 第二套机制一旦回潮，墙 → 播放页就会重新停在格子分辨率（bf2a98a 那次）。
func TestNoSecondPresentMechanism(t *testing.T) {
	assertOnlyIn(t, "SetPresentSize", map[string][]string{
		"player_media.go": {"presentSizeFor", "applyPresent", "applyPresentForce"},
		"watch_media.go":  {"watchShow", "watchOpenSession"},
	})
}

// assertOnlyIn 扫源码：某个方法只允许出现在指定的函数里。
//
// 和 label_test.go 同一个路子：要求写成静态测试，不写成文档里的口头约定。
func assertOnlyIn(t *testing.T, method string, allowed map[string][]string) {
	t.Helper()
	for file, funcs := range allowed {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, src, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ok := make(map[string]bool, len(funcs))
		for _, n := range funcs {
			ok[n] = true
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, isFn := n.(*ast.FuncDecl)
			if !isFn {
				return true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && sel.Sel.Name == method {
					if !ok[fn.Name.Name] {
						t.Errorf("%s:%d %s 里调了 %s：呈现尺寸只能从 presentSizeFor 求解、从一个入口推送",
							file, fset.Position(call.Pos()).Line, fn.Name.Name, method)
					}
				}
				return true
			})
			return false
		})
	}
}

// 从墙离开后点岛：回到墙，不是跳进播放页。
//
// 用户离开的正是那面墙，那里还有它的格子。直接跳进播放页等于替他做了一个
// 没要求的决定，而且会让"我离开的那一屏去哪了"没有答案。
func TestIslandReturnsToWallWhenFocusIsInBox(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	fakeSession(t, app, rooms[0])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	// 离开墙，去首页。焦点还在那一间，它仍在盒里。
	app.Router.Push(routeHome)
	tt.Frame()
	if got := app.islandFormOf(); got != islandListening {
		t.Fatalf("离开墙之后岛形态是 %v，期望 listening", got)
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("点岛回到了 %q，期望观测墙", got)
	}
	if app.FollowsSection != followsSectionWatch {
		t.Fatalf("回到关注页却不在观测分区（%d）", app.FollowsSection)
	}
	if app.focusKey() != key {
		t.Fatalf("回墙之后焦点变成了 %q", app.focusKey())
	}
}

// 焦点不在盒里时点岛仍然回播放页：它没有别的容身处。
func TestIslandReturnsToPlayerWhenFocusOutsideBox(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "777", Name: "盒子外"}
	app.media.roomKey = "douyu|777"
	app.media.background = true
	app.Playing = true
	app.Volume = 0.6
	app.Router.Push(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("点岛回到了 %q，期望播放页", got)
	}
}

// 保留窗口到期之后才回收。
//
// 这条是"资源策略与可见性分离"的另一半：窗口内不关，窗口外必须关，
// 否则 12 路解码会一直挂着。
func TestWatchReapsAfterRetention(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()

	app.Router.Push(routeHome)
	app.watchHide()

	// 窗口内：一拍都不该关。
	app.watchReap(time.Now())
	kept := 0
	for _, r := range rooms {
		if app.watchPlayer(watchKey(r)) != nil {
			kept++
		}
	}
	if kept != len(rooms) {
		t.Fatalf("保留窗口内就回收了：只剩 %d/%d", kept, len(rooms))
	}

	// 窗口外：非焦点全部关掉。
	app.watchReap(time.Now().Add(watchRetention + time.Second))
	if n := len(app.watch.sessions); n != 0 {
		t.Fatalf("窗口到期之后还剩 %d 路会话", n)
	}
}

// 取消观测发声之后，声音必须真的停，而且不能被下一帧装回来。
//
// 这条覆盖的是"取流还没回来"那一刻：墙上的会话在，引擎还没有。此时
// watchReleaseActive 没有东西可交接，只能把这一轮取流收掉；如果只清了
// WatchActive、没清 media.roomKey，下一帧 watchShow 会看到"媒体层有房间、
// 焦点空着"，把焦点重新认领回来——声音永远停不掉。
func TestDeactivateStopsSoundWhileLoading(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	key := watchKey(rooms[0])

	// 这一格正在取流：会话在，播放器还没有。
	app.watch.init()
	app.watch.sessions[key] = &wallSession{key: key, room: rooms[0], loading: true}
	app.media.roomKey = key
	app.media.loading = true
	app.WatchActive = key
	app.Player = rooms[0]
	app.Playing = true
	app.Volume = 0.6
	tt.Frame()

	app.watchDeactivate()
	tt.Frame()

	if app.WatchActive != "" {
		t.Fatalf("取消发声之后焦点还是 %q", app.WatchActive)
	}
	if app.media.roomKey != "" {
		t.Fatalf("取消发声之后媒体层还握着 %q：下一帧会被认领回来", app.media.roomKey)
	}
	if app.islandFormOf() != islandIdle {
		t.Fatalf("取消发声之后岛是 %v，期望时钟", app.islandFormOf())
	}

	// 再多走几帧：任何"自动认领焦点"的路径都不许把它装回来。
	for range 10 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if app.focusKey() != "" {
		t.Fatalf("十帧之后焦点又变成 %q：声音被装回来了", app.focusKey())
	}
	if n := audibleCount(app); n != 0 {
		t.Fatalf("十帧之后有 %d 路在出声", n)
	}
}

// 同样的路径，引擎已经出来时也必须真的停。
func TestDeactivateStopsSoundWithEngine(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if audibleCount(app) != 1 {
		t.Fatalf("激活之后有 %d 路在出声", audibleCount(app))
	}

	app.watchDeactivate()
	for range 10 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
	if n := audibleCount(app); n != 0 {
		t.Fatalf("取消发声之后还有 %d 路在出声", n)
	}
	if app.focusKey() != "" {
		t.Fatalf("取消发声之后焦点是 %q", app.focusKey())
	}
}

// 从墙上放大进播放页，按 X 退回墙：声音停、岛回时钟。
//
// 这条走的是真实路径（墙 → 双击 → closeMedia → Back）。焦点判据里那个
// a.Player 兜底会把"正在看的房间"当成"正在出声的焦点"：closeMedia 已经
// 把 WatchActive 和 media.roomKey 都清了，焦点却又从 a.Player 拼了回来，
// 岛于是继续展开显示那一间，而声音早就静了。
func TestLeavingPlayerFromWallClearsIsland(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	if audibleCount(app) != 1 {
		t.Fatalf("进播放页之后有 %d 路在出声", audibleCount(app))
	}

	// 按 X：和 playerTopbar 的返回钮同一条路径。
	app.closeMedia()
	app.Router.Back()
	for range 10 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}

	if n := audibleCount(app); n != 0 {
		t.Fatalf("退回墙之后还有 %d 路在出声", n)
	}
	if got := app.islandFormOf(); got != islandIdle {
		t.Fatalf("退回墙之后岛是 %v，期望时钟：声音已经停了", got)
	}
	if _, ok := tt.Find("回到直播间"); ok {
		t.Fatal("退回墙之后岛上还有回直播间的热区")
	}
}
