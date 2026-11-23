package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 按下即响应：指针按住的那一帧，控件内部必须已经暗下去。
//
// 判据取像素亮度，不取精确颜色：按下遮罩是在现有底色上压一层黑，
// 具体值随主题变，但"变暗"这件事不变。
func TestPressFeedbackDimsOnPress(t *testing.T) {
	down := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Background(theme.BgPrimary).Children(func() {
			btn := ui.Box(c).Absolute().Left(40).Top(40).Size(160, 40).
				Radius(theme.RadiusMD).Background(theme.FillTertiary).
				Cursor(ui.CursorPointer).Label("探针按钮")
			if btn.Pressed() {
				down = true
			}
			btn.Children(func() {
				pressDim(c, down, theme.RadiusMD)
			})
		})
	}, 300, 200)
	tt.Frame()

	sum := func() int {
		c := tt.Image().RGBAAt(120, 60)
		return int(c.R) + int(c.G) + int(c.B)
	}
	rest := sum()

	tt.Press(120, 60)
	tt.Frame()
	if got := sum(); got >= rest {
		t.Fatalf("按住时亮度没有下降：静止 %d，按住 %d", rest, got)
	}

	tt.Release(120, 60)
	down = false
	tt.Frame()
	if got := sum(); got != rest {
		t.Fatalf("松开后没有回到静止亮度：静止 %d，现在 %d", rest, got)
	}
}

// 遮罩不许吃点击：它盖在按钮上，但按下的那一下必须还能落到按钮本身。
func TestPressDimPassesInputThrough(t *testing.T) {
	clicked := false
	tt := ui.NewTester(func(c *ui.Context) {
		btn := ui.Box(c).Fill().Background(theme.FillTertiary).
			Cursor(ui.CursorPointer).Label("探针按钮")
		if btn.Clicked() {
			clicked = true
		}
		btn.Children(func() {
			pressDim(c, true, theme.RadiusMD)
		})
	}, 300, 200)
	tt.Frame()

	if err := tt.Click("探针按钮"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !clicked {
		t.Fatal("按下遮罩吃掉了点击：按钮收不到这次点击")
	}
}
