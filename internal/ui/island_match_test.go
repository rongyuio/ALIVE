package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// islandMatchKey 决定岛这一刻带不带共享身份。它是「一帧里只有一个元素带
// 同一个 Match」这条硬约束的唯一执行点：带错了，后建的那个元素会被冻在
// 源尺寸（探针实测：舞台停在 {0,0,800,380} 不动）。
func TestIslandMatchKeyOnlyWhenIslandHoldsThePicture(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Live: true}

	build := func(t *testing.T) *App {
		t.Helper()
		a := newApp()
		a.Platform = room.Platform
		a.resizedAt = a.now().Add(-time.Hour) // 窗口已稳定
		a.focus = &roomSession{key: watchKey(room), room: room, loading: true}
		return a
	}

	t.Run("没有焦点：不挂", func(t *testing.T) {
		a := newApp()
		a.resizedAt = a.now().Add(-time.Hour)
		a.Router = ui.NewRouter(routeHome)
		if got := a.islandMatchKey(true); got != "" {
			t.Fatalf("没有焦点也挂了身份：%q", got)
		}
	})

	t.Run("岛不是展开态：不挂", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routeHome)
		if got := a.islandMatchKey(false); got != "" {
			t.Fatalf("时钟岛挂了身份：%q", got)
		}
	})

	t.Run("在播放页：身份归舞台", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routePlayer)
		if got := a.islandMatchKey(true); got != "" {
			t.Fatalf("播放页上岛也挂了身份，舞台会被冻住：%q", got)
		}
	})

	t.Run("在观测墙上这一间有格子：身份归格子", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routeFollows)
		a.FollowsSection = followsSectionWatch
		a.WatchRooms = []Streamer{room}
		if got := a.islandMatchKey(true); got != "" {
			t.Fatalf("墙上格子也挂了身份，格子会被冻住：%q", got)
		}
	})

	// 这一条钉的是真实报障：首页上这一间有卡片时画面归卡片，岛却还挂着身份，
	// 于是点卡片进播放页那一刻，舞台从**岛**的矩形起飞（不是从卡片），飞行块
	// 和岛同时动 —— 用户看到「点封面，岛也跟着动画」。
	t.Run("当前页上有这一间的卡片：身份归卡片", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routeHome)
		a.gridDone = map[string]uint64{pageKey(a.Platform, routeHome, a.FollowsSection): 1}
		a.cardRects = map[string]cardRect{
			watchKey(room): {
				rect: ui.Rect{X: 100, Y: 200, W: 200, H: 120},
				page: pageKey(a.Platform, routeHome, a.FollowsSection),
				gen:  1,
			},
		}
		if got := a.islandMatchKey(true); got != "" {
			t.Fatalf("卡片在场时岛也挂了身份，点卡片会让舞台从岛起飞：%q", got)
		}
	})

	t.Run("别处后台收听：岛接手", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routeHome)
		if got := a.islandMatchKey(true); got != watchKey(room) {
			t.Fatalf("岛该带这一间的身份，得到 %q", got)
		}
	})

	t.Run("刚改过窗口尺寸：不挂", func(t *testing.T) {
		a := build(t)
		a.Router = ui.NewRouter(routeHome)
		a.resizedAt = a.now() // 刚刚 resize
		if got := a.islandMatchKey(true); got != "" {
			t.Fatalf("resize 窗口里挂了身份，岛会每帧弹：%q", got)
		}
	})
}

// 岛 ↔ 播放页走的是元素级 Match + Fly，不再是手搓的像素飞行块。
// 这条钉住「计划层」的选择：这两个表面之间的过渡不该排出一块飞行块。
func TestIslandAndPlayerUseElementMatch(t *testing.T) {
	for _, c := range []struct {
		name     string
		from, to picture
	}{
		{"岛 → 播放页", pictureIsland, picturePlayer},
		{"播放页 → 岛", picturePlayer, pictureIsland},
	} {
		t.Run(c.name, func(t *testing.T) {
			plan := planTransition(c.from, c.to, true, true, true)
			if plan.kind != transitionElement {
				t.Fatalf("排出来的是 %v：岛和播放页该走元素级 Match", plan.kind)
			}
		})
	}
	// 卡片两端的内容不同种（封面 vs 视频），仍然走飞行块。
	if plan := planTransition(pictureCard, picturePlayer, true, true, true); plan.kind != transitionFly {
		t.Fatalf("卡片 → 播放页排成了 %v，该是飞行块", plan.kind)
	}
	// 墙格两端画的是同一个播放器，走 Router 的 Match。
	if plan := planTransition(pictureTile, picturePlayer, true, true, true); plan.kind != transitionMatch {
		t.Fatalf("墙格 → 播放页排成了 %v，该是 Router Match", plan.kind)
	}
}
