package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 配色面板必须贴着配色按钮的右边缘展开：
// baseline 是 `right: 0`（按钮 wrapper 的绝对定位子元素），宽度 352。
func TestPalettePopoverAnchorsToButton(t *testing.T) {
	app := NewMockApp()
	app.PaletteOpen = true
	tt := ui.NewTester(app.Shell, 1440, 900)

	btn, ok := tt.Find("配色面板")
	if !ok {
		t.Fatal("找不到配色按钮")
	}
	panel, ok := tt.Find("配色浮层")
	if !ok {
		t.Fatal("找不到配色面板")
	}

	if panel.W != palettePanelW {
		t.Errorf("面板宽 %.1f，期望 %v", panel.W, palettePanelW)
	}
	if got, want := panel.X+panel.W, btn.X+btn.W; got != want {
		t.Errorf("面板右边缘 %.1f，按钮右边缘 %.1f：浮层没有右对齐按钮", got, want)
	}
}
