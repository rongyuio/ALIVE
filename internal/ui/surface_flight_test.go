package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 卡片 → 播放页那一块「正在飞的画面」：从卡片矩形长到舞台矩形，走完就消失。
//
// 岛和墙格不再走这里——它们用元素级的 Match + Fly（见 island.go 的
// islandMatchKey、watch.go 的墙格），两端各挂一次身份，框架负责交叉淡化。
//
// 用 tt.Advance 一步步走、按 Label 读矩形：不靠真窗口，也不靠连拍截图。
func TestCardFlightGrowsToStageAndEnds(t *testing.T) {
	a := newApp()
	a.Platform = "bilibili"
	a.Router = ui.NewRouter(routePlayer)
	f := &surfaceFlight{
		kind: flightEnter, key: "k",
		from: pictureCard, to: picturePlayer, pixels: pixelsCover,
		page: pageKey(a.Platform, routePlayer, a.FollowsSection),
	}
	// 卡片矩形直接给死：这一条测的是"从哪儿长到哪儿"，不是卡片怎么量出来。
	f.fromRect = ui.Rect{X: 40, Y: 40, W: 200, H: 120}
	a.surfaceFlight = f

	tt := ui.NewTester(a.surfaceFlightView, 1200, 700)
	tt.Frame()
	start, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatal("第一帧找不到飞行块")
	}
	if start.W != f.fromRect.W || start.H != f.fromRect.H {
		t.Fatalf("起飞第一帧 %+v：不是卡片矩形 %+v", start, f.fromRect)
	}
	if f.toRect.W <= 0 || f.toRect.H <= 0 {
		t.Fatalf("落点矩形没求出来：%+v", f.toRect)
	}

	last := start
	elapsed := time.Duration(0)
	for elapsed < theme.MotionSurface {
		tt.Advance(70 * time.Millisecond)
		elapsed += 70 * time.Millisecond
		if a.surfaceFlight == nil {
			break
		}
		r, ok := tt.Find("表面过渡中的画面")
		if !ok {
			break
		}
		if r.W < last.W || r.H < last.H {
			t.Fatalf("%v 时反而变小了：%+v → %+v", elapsed, last, r)
		}
		last = r
	}
	if last.W <= start.W {
		t.Fatalf("走完还停在 %+v：没长到舞台", last)
	}

	tt.Advance(2 * theme.MotionSurface)
	if a.surfaceFlight != nil {
		t.Error("过了 3 倍时长，a.surfaceFlight 还没清：过渡态会一直挂着")
	}
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Error("过了 3 倍时长，飞行块还在画")
	}
}

// reduced motion：不飞。路由已经切走，这就是硬切。
func TestCardFlightSkippedUnderReducedMotion(t *testing.T) {
	a := newApp()
	a.Platform = "bilibili"
	a.Router = ui.NewRouter(routePlayer)
	a.surfaceFlight = &surfaceFlight{
		kind: flightEnter, key: "k", from: pictureCard, to: picturePlayer,
		fromRect: ui.Rect{X: 40, Y: 40, W: 200, H: 120},
	}
	tt := ui.NewTesterWith(a.surfaceFlightView, 1200, 700, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
	tt.Frame()
	if a.surfaceFlight != nil {
		t.Error("reduced motion 下还在飞")
	}
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Error("reduced motion 下还画了飞行块")
	}
}
