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
