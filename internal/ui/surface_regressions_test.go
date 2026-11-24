package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 关闭播放页回墙格：离场页不能整页淡出。
//
// closeFocus 的 action 会清掉焦点；舞台在这一帧只能靠 exitMatchKey 拿
// Match 身份。清早了，Router 的 TransitionMatch 会把整块不透明播放页
// 按透明度淡出叠在墙上——正是 0010 和 surfaces.md 明令禁止的灰雾。
func TestCloseFromWallKeepsTheLeavingPageHidden(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	useProvider(t, plainProvider{})
	oldEngine := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return &trackSpy{} }
	t.Cleanup(func() { engineOf = oldEngine })
	steadySession(t, app, rooms[0])
	app.watch.init()
	app.watch.sessions[watchKey(rooms[1])] = &roomSession{
		key: watchKey(rooms[1]), room: rooms[1], offline: true, err: "主播暂时没有开播",
	}
	tt.Frame()

	// 取样点在飞行格子之外：A 在离场页顶部，B 在底部控制条那一带。
	// 先记下墙自己的底色，后面按它比较，不写死主题颜色。
	img := tt.Image()
	refA := img.RGBAAt(700, 80)
	refB := img.RGBAAt(1000, 870)

	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(app.now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	app.resizedAt = time.Now().Add(-time.Second)
	tt.Frame()

	if err := tt.Click("退出直播间"); err != nil {
		t.Fatalf("点不到退出直播间：%v", err)
	}

	// 前几帧整块飞行画面还盖在取样点上（它从舞台矩形起飞），等它缩过
	// 这两个点之后再比。第 4 帧起：修好的代码是干净墙底色；没修的代码
	// 仍是被整页淡出压暗的播放页。
	wallFrames := 0
	checked := 0
	for i := 0; i < 20 && checked < 6; i++ {
		tt.Frame()
		if app.Router.Path() != routePlayer {
			if wallFrames >= 4 {
				img := tt.Image()
				a := img.RGBAAt(700, 80)
				b := img.RGBAAt(1000, 870)
				// 允许主题底色有轻微渐变；整页淡出会把这两个点压暗 80+。
				if absDiff(a.R, refA.R) > 40 || absDiff(a.G, refA.G) > 40 || absDiff(a.B, refA.B) > 40 {
					t.Fatalf("第 %d 个墙帧 (700,80)=%d,%d,%d，墙底色是 %d,%d,%d：离场播放页还盖在墙上",
						wallFrames, a.R, a.G, a.B, refA.R, refA.G, refA.B)
				}
				if absDiff(b.R, refB.R) > 40 || absDiff(b.G, refB.G) > 40 || absDiff(b.B, refB.B) > 40 {
					t.Fatalf("第 %d 个墙帧 (1000,870)=%d,%d,%d，墙底色是 %d,%d,%d：离场播放页还盖在墙上",
						wallFrames, b.R, b.G, b.B, refB.R, refB.G, refB.B)
				}
				checked++
			}
			wallFrames++
		}
		tt.Advance(16 * time.Millisecond)
	}
	if checked == 0 {
		t.Fatal("关闭后没有回到墙页，取样帧为 0")
	}
	if app.Router.Path() != routeFollows || app.FollowsSection != followsSectionWatch {
		t.Fatalf("关闭后没有回到观测墙：%q/%d", app.Router.Path(), app.FollowsSection)
	}
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// 结束这一路（岛上的 X）：卡片不是落点，硬切。
//
// surfaces.md 的操作表：X / 全屏返回钮只有墙格 > 硬切，卡片和岛都不再
// 代表它。leaveClose 传 keepsFocus=false，但 pictureOn 的卡片分支不看它；
// 结果是为一块已经随会话结束的画面演 280ms 的收尾。
func TestCloseFromCardCutsHard(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.Router = ui.NewRouter(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Millisecond)
	tt.Frame()
	app.resizedAt = time.Now().Add(-time.Second)

	room := app.Streamers[0]
	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点卡片没有进播放页：%q", app.Router.Path())
	}
	if app.cardRectFor(watchKey(room), routeHome, app.FollowsSection).W <= 0 {
		t.Fatal("卡片没有记账，这条用例没走到卡片落点")
	}

	app.closeFocus()
	tt.Frame()
	if app.surfaceFlight != nil {
		t.Fatalf("结束这一路还在飞：%+v", app.surfaceFlight)
	}
	if app.focusKey() != "" {
		t.Fatalf("结束这一路焦点还在：%q", app.focusKey())
	}
	if app.Router.Path() != routeHome {
		t.Fatalf("结束之后路由是 %q，期望首页", app.Router.Path())
	}
}

// arm 之后路由已经不在播放页：旧 action 不能再执行一次 Back。
func TestLateExitActionIsDiscardedOffPlayer(t *testing.T) {
	app := NewMockApp()
	app.Router = ui.NewRouter(routeHome)
	app.Router.Push(routeFollows)
	app.buildSeq = 10
	app.exitArm = true
	app.exitArmAt = 8
	called := false
	app.exitAction = func() {
		called = true
		app.Router.Back()
	}

	app.tickExitArm()

	if called {
		t.Fatal("迟到的退场动作被执行了")
	}
	if app.Router.Path() != routeFollows {
		t.Fatalf("路由被迟到的动作改了：%q", app.Router.Path())
	}
	if app.exitAction != nil || app.exitMatchKey != "" {
		t.Fatalf("退场状态没有清干净：action=%v key=%q", app.exitAction != nil, app.exitMatchKey)
	}
}

// 仍在播放页时，第二拍照常执行。
func TestExitArmStillFiresOnPlayer(t *testing.T) {
	app := NewMockApp()
	app.Router = ui.NewRouter(routePlayer)
	app.buildSeq = 10
	app.exitArm = true
	app.exitArmAt = 8
	called := false
	app.exitAction = func() { called = true }

	app.tickExitArm()

	if !called {
		t.Fatal("播放页上的第二拍没有执行")
	}
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("第二拍没有切到 Match：%v", app.Router.Transition)
	}
}

// 退场窗口还没过时切顶栏 tab：不能把 Match 带进这次换页。
//
// Router.Transition 是路由器级字段，Push 不消费也不复位；上一个退场
// 窗口留下的 Match 会让关注墙和首页做整页交叉淡化（0010 禁止）。
func TestTabSwitchAfterExitDoesNotInheritMatch(t *testing.T) {
	app := NewMockApp()
	app.Router = ui.NewRouter(routeFollows)
	app.Router.Transition = ui.TransitionMatch
	app.routeTransitionUntil = time.Now().Add(time.Second)
	app.surfaceFlight = &surfaceFlight{kind: flightLeave}

	app.pushPage(routeHome)

	if app.Router.Path() != routeHome {
		t.Fatalf("换页失败：%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("tab 换页继承了 %v：内容层会做交叉淡化", app.Router.Transition)
	}
	if !app.routeTransitionUntil.IsZero() {
		t.Fatal("上一个退场窗口没有被清掉")
	}
	if app.surfaceFlight != nil {
		t.Fatalf("上一页的飞行没有作废：%+v", app.surfaceFlight)
	}
}

// 应用内全屏切换不带动画。
//
// Match 常挂后，Navbar 撤掉、左右 padding 归零会让整个播放页外壳被
// Snappy 做 0.5s 的尺寸/位置弹簧，视频逐帧重排。全屏边沿要像窗口 resize
// 一样把 matchable 的 400ms 抑制拨起来。
func TestWinFullToggleDoesNotAnimateTheShell(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	app.resizedAt = time.Now().Add(-time.Second)
	tt.Frame()

	if err := tt.Click("窗口全屏"); err != nil {
		t.Fatalf("点不到窗口全屏：%v", err)
	}
	if !app.WinFull {
		t.Fatal("点了窗口全屏但状态没翻")
	}
	// 点击发生在构建中途：第一帧换版式，第二帧 Bounds 才反映新矩形。
	tt.Frame()
	tt.Frame()
	tt.Frame()
	r1 := app.playerStageBounds
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	r2 := app.playerStageBounds
	if r1 != r2 {
		t.Fatalf("全屏切换后舞台还在动：%v → %v（应一帧到位）", r1, r2)
	}
	if r1.X != 0 || r1.Y != 0 {
		t.Fatalf("全屏后舞台没有贴到窗口左上：%v", r1)
	}
}

// resize 后 400ms 内退出墙格：exitArm 补的那一拍必须真的让格子接住舞台。
//
// 墙格原本也被 matchable() 挡着，所以这段窗口里退出会退化成整页淡出。
// 过渡窗口（routeTransitionUntil 非零）要放行两侧的 Match。
func TestExitToWallFliesAfterResize(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(app.now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	// 刚 resize：matchable() 为假，只有 exitArm 能救。
	app.resizedAt = time.Now()
	tt.Frame()
	if app.matchable() {
		t.Fatal("场景没有落在 resize 窗口里")
	}

	tt.Key(0, ui.KeyEscape)
	for i := 0; i < 6; i++ {
		tt.Frame()
		if app.Router.Path() != routePlayer {
			break
		}
		tt.Advance(16 * time.Millisecond)
	}
	if app.Router.Path() == routePlayer {
		t.Fatal("Esc 之后还停在播放页")
	}
	first, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("回到墙上找不到那一格")
	}
	tt.Advance(160 * time.Millisecond)
	tt.Frame()
	settled, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("飞行途中那一格不见了")
	}
	if first.W <= settled.W {
		t.Fatalf("resize 窗口内退出没有飞回格子：%v → %v", first, settled)
	}
}

// 飞行途中换了页（不经过 pushPage 的 Router.Back / 直接 Push），
// 那块画面属于上一页的落点，必须作废。
func TestFlightCancelledByRouteChange(t *testing.T) {
	app, tt := cardFlightApp(t, cardFlightRooms("卡", "英雄联盟", 4))
	room := app.Streamers[0]
	focusWithPlayer(t, app, room)
	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.surfaceFlight == nil || app.surfaceFlight.to != pictureCard {
		t.Fatalf("没有起飞到卡片：%+v", app.surfaceFlight)
	}

	// MyGo Router 自带的后退键/鼠标后退就是这种：不经过 pushPage。
	app.Router.Push(routeFollows)
	tt.Frame()
	if app.surfaceFlight != nil {
		t.Fatalf("换页后飞行还在：%+v", app.surfaceFlight)
	}
}
