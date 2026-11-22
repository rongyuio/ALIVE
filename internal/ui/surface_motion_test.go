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
