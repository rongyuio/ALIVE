package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 过渡模型的真值表。
//
// 它就是"动画该怎么走"的唯一答案：同样的输入永远同样的输出。任何调用点
// （卡片、搜索、岛、墙格、Esc、X、顶栏 tab）都只能组装输入，不许自己再判。
func TestPlanTransition(t *testing.T) {
	cases := []struct {
		name       string
		from, to   picture
		fromOK     bool
		toOK       bool
		live       bool
		wantKind   transitionKind
		wantPixels transitionPixels
		wantReason string
	}{
		{"卡片→播放页，还没画面", pictureCard, picturePlayer, true, true, false, transitionFly, pixelsCover, ""},
		{"卡片→播放页，已有画面", pictureCard, picturePlayer, true, true, true, transitionFly, pixelsLive, ""},
		{"岛→播放页，已有画面", pictureIsland, picturePlayer, true, true, true, transitionFly, pixelsLive, ""},
		{"墙格→播放页", pictureTile, picturePlayer, true, true, true, transitionMatch, pixelsLive, ""},
		{"播放页→墙格", picturePlayer, pictureTile, true, true, true, transitionMatch, pixelsLive, ""},
		{"播放页→卡片", picturePlayer, pictureCard, true, true, true, transitionFly, pixelsLive, ""},
		{"播放页→岛", picturePlayer, pictureIsland, true, true, true, transitionFly, pixelsLive, ""},
		{"播放页→没有落点（结束/全屏）", picturePlayer, pictureNone, true, false, true, transitionCut, 0, "no-geometry"},
		{"卡片不在屏上→播放页（房间号搜索）", pictureCard, picturePlayer, false, true, false, transitionCut, 0, "no-geometry"},
		{"同一个表面", picturePlayer, picturePlayer, true, true, true, transitionCut, 0, "same-surface"},
	}
	for _, c := range cases {
		got := planTransition(c.from, c.to, c.fromOK, c.toOK, c.live)
		if got.kind != c.wantKind || got.pixels != c.wantPixels || got.reason != c.wantReason {
			t.Errorf("%s: got {kind:%d pixels:%d reason:%q} want {kind:%d pixels:%d reason:%q}",
				c.name, got.kind, got.pixels, got.reason, c.wantKind, c.wantPixels, c.wantReason)
		}
	}
}

// 同一张卡片、同一个操作，收起之后再打开必须和第一次一样有动画。
//
// 旧模型按「这一间有没有在出图的引擎」分叉：第一次飞封面，收起（引擎留着）
// 再打开就硬切。统一模型下唯一变化的是飞行块里画活画面还是封面。
func TestReopenSameCardAnimatesAgain(t *testing.T) {
	app, tt := cardFlightApp(t, cardFlightRooms("再", "英雄联盟", 3))
	room := app.Streamers[0]
	label := "直播间:" + room.Name
	tt.Frame()

	if err := tt.Click(label); err != nil {
		t.Fatalf("第一次点卡片失败: %v", err)
	}
	tt.Frame()
	first := app.surfaceFlight
	if first == nil || first.pixels != pixelsCover {
		t.Fatalf("第一次打开没有封面飞行：%+v", first)
	}

	// 收起：这一路留在后台收听（引擎还在）。
	app.collapsePlayer()
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if app.Router.Path() == routePlayer {
		t.Fatal("收起之后还停在播放页")
	}
	if app.surfaceFlight != nil {
		t.Fatalf("收起之后飞行没有收尾：%+v", app.surfaceFlight)
	}

	// 再打开同一张卡片：必须和第一次一样飞，只是这次画活画面。
	if err := tt.Click(label); err != nil {
		t.Fatalf("第二次点卡片失败: %v", err)
	}
	tt.Frame()
	second := app.surfaceFlight
	if second == nil {
		t.Fatal("再次打开没有动画：这就是「同一个动作时有时无」")
	}
	if second.from != pictureCard {
		t.Fatalf("再次打开不是从卡片起飞：%+v", second)
	}
	// pixels 取决于这一间此刻有没有出图：夹具里的播放器没有真引擎，
	// 这一条只证明"飞不飞"一致；画活画面那条由 TestCardToLivePlayerFliesTheLivePicture 守。
}

// X 结束这一路：没有收尾动画（墙格除外，画面本来就在格子里继续）。
func TestCloseFocusDoesNotAnimate(t *testing.T) {
	app, tt := cardFlightApp(t, cardFlightRooms("关", "英雄联盟", 3))
	room := app.Streamers[0]
	tt.Frame()
	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatalf("点卡片失败: %v", err)
	}
	tt.Frame()
	app.surfaceFlight = nil

	app.closeFocus()
	tt.Frame()
	if app.surfaceFlight != nil {
		t.Fatalf("明确关闭还安排了收尾动画：%+v", app.surfaceFlight)
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("明确关闭用了 %v", app.Router.Transition)
	}
}
