package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 控制条的三档尺寸必须和 PlayerControls.module.css 的两个媒体查询对齐：
// 常态 12/10/96，≤1100 间距收 8、滑条 90，≤980 滑条 72 且百分比让位。
// 这几档是纯数值，用 tester 直接读，不依赖真实窗口。
func TestControlMetrics(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want controlMetrics
	}{
		{"宽屏", 1440, 900, controlMetrics{12, 12, 10, 96, true}},
		{"1101 仍是宽屏", 1101, 900, controlMetrics{12, 12, 10, 96, true}},
		{"1100 收到第一档", 1100, 900, controlMetrics{8, 8, 8, 90, true}},
		{"981 仍是第一档", 981, 900, controlMetrics{8, 8, 8, 90, true}},
		{"980 收到第二档", 980, 900, controlMetrics{8, 8, 8, 72, false}},
		{"最小窗口", 900, 600, controlMetrics{8, 8, 8, 72, false}},
	}

	for _, tc := range cases {
		var got controlMetrics
		ui.NewTester(func(c *ui.Context) {
			got = metricsFor(c)
			ui.Box(c).Fill()
		}, tc.w, tc.h)
		if got != tc.want {
			t.Errorf("%s（%d 宽）：得到 %+v，期望 %+v", tc.name, tc.w, got, tc.want)
		}
	}
}

// 复现 playerDropdown 的悬停路径：确认 MyGo 里「指针移到 Row 上」真的
// 会让 Hovered() 在重建帧里为 true。
func TestHoverOpensDropdown(t *testing.T) {
	var open bool
	var h hoverClose
	tt := ui.NewTester(func(c *ui.Context) {
		btn := ui.Row(c).Height(30).Padding(0, 6, 0, 6).Gap(6).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("清晰度")
		h.hover(c, btn.Hovered(), &open)
		btn.Children(func() {
			ui.Text(c, "原画").FontSize(12)
		})
		ui.PopoverBase(c, btn, &open, func(panel ui.Element) {
			panel.Width(130).Padding(10).AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
			h.hover(c, panel.Hovered(), &open)
			panel.Children(func() {
				ui.Text(c, "蓝光").FontSize(13)
			})
		})
	}, 600, 400)

	r, ok := tt.Find("清晰度")
	if !ok {
		t.Fatal("找不到触发条")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if !open {
		t.Fatalf("悬停没有打开下拉（bounds=%+v）", r)
	}
	tt.Advance(600 * time.Millisecond)
	if !open {
		t.Fatal("悬停中被关掉了")
	}
	tt.Move(500, 380)
	tt.Advance(600 * time.Millisecond)
	// 关门的判定发生在「到点后的第一帧」里；真实窗口因为弹幕一直在跑
	// 帧，测试得自己推一帧。
	tt.Frame()
	if open {
		t.Fatal("离开后没有关闭")
	}
}

// 双击画面切窗口全屏（baseline 的 onStageDoubleClick）：
// 画面空白处生效，控制条上的按钮不参与——连点一颗按钮不该把画面顶成全屏。
func TestDoubleClickStageTogglesWindowFullscreen(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进入播放页，路由为 %q", app.Router.Path())
	}
	// 播放页是从右侧滑进来的，动画没走完之前整页都还在窗口外
	tt.Advance(1500 * time.Millisecond)

	doubleClick := func(x, y float32) {
		for range 2 {
			tt.Press(x, y)
			tt.Release(x, y)
		}
	}

	r, ok := tt.Find("重新加载")
	if !ok {
		t.Fatal("控制条没有渲染出来")
	}
	doubleClick(r.X+r.W/2, r.Y+r.H/2)
	if app.WinFull {
		t.Fatal("双击控制条按钮误切了窗口全屏")
	}

	// 画面中央没有任何控件，命中链最里层就是 stage
	doubleClick(700, 450)
	if !app.WinFull {
		t.Fatal("双击画面没有进入窗口全屏")
	}

	doubleClick(700, 450)
	if app.WinFull {
		t.Fatal("再次双击画面没有退出窗口全屏")
	}
}

// 窗口全屏时顶部那条拖窗条压在画面上，顶栏控件必须仍在它之上。
// MyGo 的命中链只取最上层那个元素，拖窗条不能把按钮盖掉。
func TestFullscreenTopChromeStaysClickable(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(1500 * time.Millisecond)
	app.WinFull = true
	tt.Frame()

	if err := tt.Click("主播信息"); err != nil {
		t.Fatalf("窗口全屏下点不到头像：%v", err)
	}
	if !app.InfoOpen {
		t.Fatal("点头像没有展开信息岛")
	}
}

// 悬浮 chrome 的自动隐藏（baseline 的 chromeHidden）：指针静止 2.8 秒后
// 淡出，一动就回来；暂停时一律常显。
func TestChromeAutoHidesWhenIdle(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(1500 * time.Millisecond)

	// 指针停在画面中间：不在顶栏、也不在控制条那两条带上
	tt.Move(700, 450)
	tt.Advance(3 * time.Second)
	if !app.ChromeHidden {
		t.Fatal("指针静止 2.8 秒后 chrome 没有淡出")
	}

	tt.Move(704, 454)
	if app.ChromeHidden {
		t.Fatal("指针一动 chrome 没有回来")
	}

	// 暂停时不隐藏：那会儿人往往离开鼠标去干别的
	app.Playing = false
	tt.Advance(3 * time.Second)
	if app.ChromeHidden {
		t.Fatal("暂停时 chrome 不该淡出")
	}
}

// 进房间默认播第一档：菜单按 qn 降序，第一档就是能拿到的最高档；
// 按标签选档位时命中对应项。
func TestPickVariantDefaultsToTop(t *testing.T) {
	variants := []live.Variant{
		{ID: "source", Label: "1080P 原画"},
		{ID: "high", Label: "720P 超清"},
	}
	if got := defaultVariantLabel(variants); got != "1080P 原画" {
		t.Errorf("默认档位应是最高的第一档，得到 %q", got)
	}
	if v := pickVariant(variants, "720P 超清"); v.ID != "high" {
		t.Errorf("按标签选档位应命中 high，得到 %q", v.ID)
	}
}

// 播放页顶栏高亮跟着主播所属平台走，而不是进播放页之前那个列表的平台。
//
// 从订阅页点开别家主播最容易撞上：a.Platform 还是订阅页原来那个平台的，
// 顶栏却应该显示主播自己的平台。
func TestPlayerTabFollowsStreamerPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "douyin"
	app.Player = Streamer{Platform: "bilibili", RoomID: "170001", Name: "某主播"}
	app.Router.Push(routePlayer)

	if got := app.activeTab(); got != "bilibili" {
		t.Fatalf("顶栏高亮的是 %q，期望 %q", got, "bilibili")
	}
}

// 没有主播上下文时回落到当前平台，不能把顶栏高亮清空。
func TestPlayerTabFallsBackToPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "huya"
	app.Player = Streamer{}
	app.PlayerIndex = -1
	app.Router.Push(routePlayer)

	if got := app.activeTab(); got != "huya" {
		t.Fatalf("顶栏高亮的是 %q，期望 %q", got, "huya")
	}
}
