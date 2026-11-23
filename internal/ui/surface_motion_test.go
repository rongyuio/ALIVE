package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 表面之间的连续过渡：墙格 → 播放页，同一间房不该硬切。
//
// 判据分两层，各自都有分辨力：
//
//  1. 双击之后路由器处于 TransitionMatch——硬切就是 TransitionNone；
//  2. 起飞那一帧，另一格的位置仍然是墙的内容（红封面），不是播放舞台的
//     黑底——说明墙没有被瞬间抽走，飞行元素是长在它上面的。
//
// 播放器先暂停，避免 GIF 自己换帧污染第二层判据。
//
// 先红后绿过：把 pushRoute 换回 Router.Push，第 1 层直接失败。
func TestWallToPlayerIsContinuous(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r).player.Pause()
	}
	tt.Frame()

	// 另一格：不参与这次跳转，但落在播放舞台的范围之内。
	other, ok := tt.Find(watchTileLabel(rooms[2]))
	if !ok {
		t.Fatal("找不到第三格")
	}
	probe := image.Pt(int(other.X+other.W/2), int(other.Y+other.H/2))

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("没有进播放页：route=%q", got)
	}

	// 1. 走的是连续过渡，不是硬切。
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("进播放页时路由器是 %v，期望 TransitionMatch：这一下是硬切",
			app.Router.Transition)
	}

	// 2. 起飞帧墙还在下面。
	if c := tt.Image().RGBAAt(probe.X, probe.Y); isStageInk(c) {
		t.Fatalf("起飞帧在另一格的位置已经是舞台底色 %v：墙被瞬间抽走了", c)
	}
}

// 退出播放页必须走同一条连续过渡：进去是放大，出来就该是缩回去。
//
// 这条以前不存在，而且它不是"忘了调 pushRoute"那么简单：三个退出入口
// 都先 closeMedia() 再 Router.Back()，而 closeMedia 会把焦点清空——
// continuousRoute 判 focusKey() != "" 于是永远为假，pushRoute 根本不会
// 把路由器切到 Match。所以修法不是补一句调用，是换顺序：先把"这一趟要
// 不要连续"定下来，再结束焦点，再退。
//
// 判据：退出的那一帧路由器处于 TransitionMatch。硬切就是 None。
//
// 先红后绿过：把 exitPlayer 换回 closeMedia + Router.Back，第一条直接失败。
func TestPlayerToWallIsContinuous(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r).player.Pause()
	}
	tt.Frame()

	// 双击进播放页。
	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	// 进场的 Match 已经结束，路由器应当回到 None。
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("进场过渡没有收尾：%v", app.Router.Transition)
	}

	// 按岛上的 X 退出。
	if err := tt.Click("退出直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("退出后没有回墙：route=%q", got)
	}
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("退出播放页时路由器是 %v，期望 TransitionMatch：这一下是硬切",
			app.Router.Transition)
	}

	// 收尾之后恢复，别把顶栏 tab 一起带滑。
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("退出过渡结束后路由器还停在 %v", app.Router.Transition)
	}
}

// 退出过渡必须留住 Match 身份：closeMedia 清掉焦点之后，舞台还要认得出自己。
//
// 上一条只断言路由器的模式，那还可能是"Match 但没有可匹配的元素"——退化成
// 交叉淡化。这条验的是机制本身：退出的那一刻，舞台的 Match 身份必须仍然是
// 这一间，而且这一间确实还在观测盒里（墙上有格子能接住这次飞行）。
//
// 先红后绿过：把 exitPlayer 里的 exitMatchKey 去掉，stageMatchKey() 会落到
// 已经清空的焦点上，返回空串。
func TestPlayerExitKeepsMatchIdentity(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r).player.Pause()
	}
	tt.Frame()

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	key := watchKey(rooms[0])
	app.exitPlayer()

	if got := app.stageMatchKey(); got != key {
		t.Fatalf("退出之后舞台的 Match 身份是 %q，期望 %q：源元素没身份，飞不起来", got, key)
	}
	if !app.watchHas(key) {
		t.Fatal("这一间不在观测盒里：墙上没有格子接住这次收缩")
	}
}

// 从列表进播放页、再退出，不连续。
//
// 这是和上一条配套的边界：连续过渡的前提是"退回去的那一页还画着这一间"。
// 从首页卡片进来的，退回去是首页——首页没有这一间的画面可接。给它加交叉
// 淡化就是在内容层上动，0010 明确禁止。所以这条必须硬切。
//
// 判据是反向的：路由器在退出那一帧仍然是 TransitionNone。如果哪天有人
// 把连续判据放宽成"只要在播放页就连续"，这条会红。
func TestListToPlayerExitsHard(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "42", Name: "甲"}
	app.media.roomKey = "douyu|42"
	app.Router.Push(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.pushRoute(routePlayer)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	app.exitPlayer()
	if app.Router.Path() != routeHome {
		t.Fatalf("退出后没有回首页：route=%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("从列表进的播放页退出时播了过渡（%v）：退回去的首页没有画面可接",
			app.Router.Transition)
	}
}

// isStageInk 报告这个像素是不是播放舞台的黑底。
//
// 舞台是 ui.RGB(0,0,0)；留一点余量，抗锯齿边缘不算。
func isStageInk(c color.RGBA) bool { return c.R < 12 && c.G < 12 && c.B < 12 }

// 转场结束后路由器必须恢复成"不播页面过渡"。
//
// 不恢复的话，之后每次点顶栏 tab 都会滑一下——那正是 d488013 关掉的东西。
func TestRouteTransitionRestores(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	app.media.roomKey = "douyu|1"
	app.Router.Push(routePlayer)
	app.pushRoute(routeHome)

	if app.Router.Transition != ui.TransitionNone {
		// 从播放页离开本来就是"表面之间"，这一下应该是连续的。
		t.Logf("离开播放页的过渡是 %v（期望 Match）", app.Router.Transition)
	}

	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("转场结束后路由器还停在 %v：之后切 tab 会滑一下", app.Router.Transition)
	}
}

// 顶栏 tab 之间不播过渡：方向模型与平级路由不匹配（d488013）。
func TestTabSwitchStaysInstant(t *testing.T) {
	app := NewMockApp()
	app.Router.Push(routeFollows)
	app.pushRoute(routeHome)
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("顶栏 tab 之间播了页面过渡：%v", app.Router.Transition)
	}
}
