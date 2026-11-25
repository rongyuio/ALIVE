package ui

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
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
func fakeSession(t *testing.T, a *App, room Streamer) *roomSession {
	t.Helper()
	a.watch.init()
	p := gifPlayer(t)
	p.SetVolume(0)
	s := &roomSession{
		key: watchKey(room), room: room,
		player: p, track: &trackSpy{},
		firstFrame: true,
	}
	a.watch.sessions[s.key] = s
	return s
}

// audibleCount 数此刻真正在出声的播放器：音量 > 0。
//
// 这是"至多一路出声"唯一的判据来源：不看 a.focusVolume()、不看 WatchActive，
// 直接问播放器。状态字段可以撒谎，音量不会。
func audibleCount(a *App) int {
	n := 0
	seen := map[*ui.VideoPlayer]bool{}
	if p := a.media.eng(); p != nil && !seen[p] {
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

// roomSessionOf 返回墙为这一间持有的会话（不论有没有播放器）。
func roomSessionOf(a *App, key string) *roomSession { return a.watch.sessions[key] }

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

	// 模拟"从列表进过播放页再离开"：这一间在后台出声。
	sess := focusWithPlayer(t, app, rooms[0])
	app.setFocusPlaying(true)
	app.setVolume(0.6)

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

	if s := roomSessionOf(app, key); s != sess {
		t.Fatal("墙上为焦点那一间换了一个会话：同一间房两个所有者")
	}
}

// 不变量 4：从墙放大进播放页，按 X 结束焦点，但房间还在墙上 → 会话存活。
//
// 对应缺陷 2：回到墙上这一格黑了，要重新取流。
func TestInvariantCloseKeepsSessionInBox(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	key := watchKey(rooms[0])
	sess := fakeSession(t, app, rooms[0])
	tt.Frame()

	// 双击进播放页。
	label := watchTileLabel(rooms[0])
	doubleClick(t, tt, label)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("双击没有进播放页，route=%q", app.Router.Path())
	}
	if app.media.eng() != sess.player {
		t.Fatal("进播放页之后媒体层拿到的不是墙上那一路")
	}

	// 按 X：结束焦点。
	app.closeFocus()

	if !app.watchHas(key) {
		t.Fatal("房间还在墙上，watchHas 却是假")
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
	if app.media.eng() != first.player {
		t.Fatal("第一间没有进媒体层")
	}

	listenOnWall(t, tt, rooms[1])

	if app.watchPlayer(watchKey(rooms[0])) != first.player {
		t.Fatal("换焦点之后上一间的播放器不见了：它还在墙上")
	}
	if app.watchPlayer(watchKey(rooms[1])) != second.player {
		t.Fatal("新的焦点没有拿到第二间的播放器")
	}
	if audibleCount(app) > 1 {
		t.Fatalf("换焦点之后有 %d 路在出声", audibleCount(app))
	}
}

// 不变量 6：墙上的预览由导航决定停不停。
//
// 进播放页（背后还是这面墙）不停；去首页才是离开墙，非焦点会话停掉，
// 焦点留着出声（它就是后台收听）。
func TestInvariantWallPreviewsFollowNavigation(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()
	app.setFocus(app.watch.sessions[watchKey(rooms[0])])
	tt.Frame()

	openTile(t, tt, rooms[0])
	if n := len(app.watch.sessions); n != len(rooms) {
		t.Fatalf("进播放页把墙上的预览停了：只剩 %d/%d", n, len(rooms))
	}

	app.pushPage(routeHome)
	tt.Frame()
	if n := len(app.watch.sessions); n != 1 {
		t.Fatalf("离开墙之后还剩 %d 路会话，期望只留焦点那一路", n)
	}
	if app.watch.sessions[watchKey(rooms[0])] == nil {
		t.Fatal("离开墙把焦点那一间也停了：它还要出声")
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
	doubleClick(t, tt, label)
	tt.Frame()

	if got := app.surfaceFor(key); got != surfaceFull {
		t.Fatalf("进播放页后表面是 %v，期望 full", got)
	}
	if app.media.danmaku != nil {
		t.Fatal("墙上就有弹幕连接：弹幕只属于全尺寸视图")
	}

	// 离开：弹幕必须断。
	app.Router.Push(routeHome)
	app.backgroundEnter()
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

	// 焦点是另一间（不在墙上），在后台出声。
	focusWithPlayer(t, app, Streamer{Platform: "bilibili", RoomID: "9999", Name: "墙外的主播"})
	app.setFocusPlaying(true)
	app.setVolume(0.6)

	if got := app.focusKey(); got != "bilibili|9999" {
		t.Fatalf("焦点是 %q，期望 bilibili|9999", got)
	}
	if got := app.islandFormOf(); got != islandOpen {
		t.Fatalf("声音在响，岛形态是 %v，期望 open", got)
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
		// setPresent 是唯一写者：它在写之前从 presentSizeForSession 求值，
		// 并按档位记一条 present.change。其它函数只能调 setPresent，不能
		// 自己推引擎。
		"player_media.go": {"setPresent"},
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

// forEachProductionFunc 遍历包内所有非测试文件的函数体。
//
// 静态规则只读生产代码：测试里为了造场景可以随便写，规则管的是产品代码
// 会不会长出第二套机制。
func forEachProductionFunc(t *testing.T, check func(fn *ast.FuncDecl, file string, fset *token.FileSet)) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, isFn := n.(*ast.FuncDecl)
			if !isFn {
				return true
			}
			check(fn, name, fset)
			return false
		})
	}
}

// 静态检查：焦点只有一个写者。
//
// a.focus 是「哪一间在播」的唯一真相。允许写它的只有 setFocus（换焦点）和
// closeSession（销毁会话时保证焦点不悬空）。别处直接赋值就是在造第二份真相
// ——以前 a.Player / WatchActive / media.roomKey 三份就是这么烂掉的。
func TestNoSecondFocusWriter(t *testing.T) {
	forEachProductionFunc(t, func(fn *ast.FuncDecl, file string, fset *token.FileSet) {
		if fn.Name.Name == "setFocus" || fn.Name.Name == "closeSession" {
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, isAs := n.(*ast.AssignStmt)
			if !isAs {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, isSel := lhs.(*ast.SelectorExpr)
				if isSel && sel.Sel.Name == "focus" {
					t.Errorf("%s:%d %s 里直接写了 a.focus：焦点只能经 setFocus 改",
						file, fset.Position(as.Pos()).Line, fn.Name.Name)
				}
			}
			return true
		})
	})
}

// 静态检查：引擎音量只有一个写者。
//
// 目标值永远由「是不是焦点」决定（焦点 = 自己的音量，其余 = 0）。散着写
// 就会出现墙格预览在响、后台让位的一路没静音这类组合——那正是旧模型里
// a.Volume / wall.volumes / player.Playing() 三份真相互相追的来源。
func TestNoSecondVolumeWriter(t *testing.T) {
	forEachProductionFunc(t, func(fn *ast.FuncDecl, file string, fset *token.FileSet) {
		if fn.Name.Name == "applySessionVolume" {
			return
		}
		// attachPlayer 是唯一允许的第二处：它只把新播放器的记录值对齐到
		// 引擎的起播静音（StartSilent = volume 0），不决定任何目标音量。
		// 只许字面量 0；别的值仍然只能经 applySessionVolume。
		if fn.Name.Name == "attachPlayer" {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || sel.Sel.Name != "SetVolume" {
					return true
				}
				lit, isLit := call.Args[0].(*ast.BasicLit)
				if !isLit || lit.Value != "0" {
					t.Errorf("%s:%d attachPlayer 只能把音量对齐到字面量 0",
						file, fset.Position(call.Pos()).Line)
				}
				return true
			})
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if isSel && sel.Sel.Name == "SetVolume" {
				t.Errorf("%s:%d %s 里直接调了 SetVolume：音量只能经 applySessionVolume 推",
					file, fset.Position(call.Pos()).Line, fn.Name.Name)
			}
			return true
		})
	})
}

// 静态检查：会话的播放器只能经 attachPlayer 挂上。
//
// 挂播放器必须同时把记录音量对齐到引擎的起播静音（0），否则目标音量为 1
// 时单一写者会跳过第一次推送，引擎永远停在 0（见 attachPlayer 注释）。
// dropEngine 摘播放器是另一条合法路径。
func TestNoDirectPlayerAttach(t *testing.T) {
	forEachProductionFunc(t, func(fn *ast.FuncDecl, file string, fset *token.FileSet) {
		switch fn.Name.Name {
		case "attachPlayer", "dropEngine":
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			as, isAssign := n.(*ast.AssignStmt)
			if !isAssign {
				return true
			}
			for _, lhs := range as.Lhs {
				sel, isSel := lhs.(*ast.SelectorExpr)
				if isSel && sel.Sel.Name == "player" {
					t.Errorf("%s:%d %s 里直接给 player 赋值：只能经 attachPlayer / dropEngine",
						file, fset.Position(as.Pos()).Line, fn.Name.Name)
				}
			}
			return true
		})
	})
}

// 静态检查：会话的引擎只有一个关闭入口。
//
// s.player.Close() 和 m.eng().Close() 都只许出现在 dropEngine 里。后台
// 还没交付的引擎（adoptPlayback 拒收、runHotSwap 失败）是另一种对象，
// 它们从来没挂到会话上，由创建者自己关。
func TestNoSecondEngineCloser(t *testing.T) {
	forEachProductionFunc(t, func(fn *ast.FuncDecl, file string, fset *token.FileSet) {
		if fn.Name.Name == "dropEngine" {
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			sel, isSel := call.Fun.(*ast.SelectorExpr)
			if !isSel || sel.Sel.Name != "Close" {
				return true
			}
			owned := false
			if inner, ok := sel.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "player" {
				owned = true
			}
			if inner, ok := sel.X.(*ast.CallExpr); ok {
				if f, ok := inner.Fun.(*ast.SelectorExpr); ok && f.Sel.Name == "eng" {
					owned = true
				}
			}
			if owned {
				t.Errorf("%s:%d %s 里直接关了会话的引擎：只能经 dropEngine",
					file, fset.Position(call.Pos()).Line, fn.Name.Name)
			}
			return true
		})
	})
}

// 静态检查：平行的焦点 / 音量 / 播放态字段不许回潮。
//
// 这几条字段各自都曾是「第二份真相」。删掉之后如果有人顺手加回来，岛、墙、
// 播放页就会重新各读各的。用反射查结构，比 grep 名字可靠。
func TestNoParallelPlayerFields(t *testing.T) {
	app := reflect.TypeOf(App{})
	for _, name := range []string{
		"Player", "PlayerIndex", "PlayerRemote",
		"Volume", "Playing", "WatchActive", "WatchPending",
	} {
		if _, ok := app.FieldByName(name); ok {
			t.Errorf("App 上又出现了 %s：它该由 a.focus / roomSession 推导", name)
		}
	}
	session := reflect.TypeOf(roomSession{})
	if _, ok := session.FieldByName("active"); ok {
		t.Error("roomSession.active 回来了：焦点就是 active，不需要另存一份")
	}
}

// 从墙离开后点岛：回到墙，不是跳进播放页。
//
// 用户离开的正是那面墙，那里还有它的格子。直接跳进播放页等于替他做了一个
// 没要求的决定，而且会让"我离开的那一屏去哪了"没有答案。
func TestIslandReturnsToTheRoomEvenWhenItIsInBox(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	fakeSession(t, app, rooms[0])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	// 离开墙，去首页。焦点还在那一间，它仍在墙上。
	app.Router.Push(routeHome)
	tt.Frame()
	if got := app.islandFormOf(); got != islandOpen {
		t.Fatalf("离开墙之后岛形态是 %v，期望 open", got)
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("点岛回到了 %q，期望播放页", got)
	}
	if app.focusKey() != key {
		t.Fatalf("点岛打开的是 %q，期望 %q", app.focusKey(), key)
	}
	if app.focusKey() != key {
		t.Fatalf("回墙之后焦点变成了 %q", app.focusKey())
	}
}

// 焦点不在墙上时点岛仍然回播放页：它没有别的容身处。
func TestIslandReturnsToPlayerWhenFocusOutsideBox(t *testing.T) {
	app := NewMockApp()
	focusWithPlayer(t, app, Streamer{Platform: "douyu", RoomID: "777", Name: "墙外"})
	app.setFocusPlaying(true)
	app.setVolume(0.6)
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

// 最小化不算离开墙：会话不拆，只停渲染。
//
// 离开墙（去首页 / 别的分区）才停，那条路由 watchPark 管。这里守的是
// watchHide 本身不做关闭——它只是标一个不可见时刻。
func TestWatchHiddenKeepsSessions(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()

	app.WatchHidden()
	tt.Frame()
	if n := len(app.watch.sessions); n != len(rooms) {
		t.Fatalf("最小化把会话拆了：只剩 %d/%d", n, len(rooms))
	}
}

// 取消观测发声之后，声音必须真的停，而且不能被下一帧装回来。
//
// 这条覆盖的是"取流还没回来"那一刻：墙上的会话在，引擎还没有。结束焦点
// 必须把这一轮取流一起作废；只清焦点、留着会话，下一帧墙的同步仍会把它
// 当成"在墙上的预览"，焦点也可能被重新装回来——声音永远停不掉。
func TestDeactivateStopsSoundWhileLoading(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	stubEngines(t)
	key := watchKey(rooms[0])

	// 这一格正在取流：会话在，播放器还没有。
	app.watch.init()
	app.watch.sessions[key] = &roomSession{key: key, room: rooms[0], loading: true}
	app.media.setLoading(true)
	focusForTest(app, rooms[0])
	app.setFocusPlaying(true)
	app.setVolume(0.6)
	tt.Frame()

	app.closeFocus()
	tt.Frame()

	if app.wallActiveKey() != "" {
		t.Fatalf("取消发声之后焦点还是 %q", app.wallActiveKey())
	}
	if app.media.roomKey() != "" {
		t.Fatalf("取消发声之后媒体层还握着 %q：下一帧会被认领回来", app.media.roomKey())
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

	app.closeFocus()
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
	doubleClick(t, tt, label)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	if audibleCount(app) != 1 {
		t.Fatalf("进播放页之后有 %d 路在出声", audibleCount(app))
	}

	// 按 X：和 playerTopbar 的返回钮同一条路径。
	app.closeFocus()
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
