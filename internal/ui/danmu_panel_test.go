package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 右侧弹幕面板按 baseline 的默认宽度 196 展开，且必须和舞台分列。
func TestDanmuPanelWidth(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)

	r, ok := tt.Find("弹幕列表")
	if !ok {
		t.Fatal("找不到弹幕面板")
	}
	if r.W != app.DanmuPanelW {
		t.Errorf("面板宽 %.1f，期望 %.1f", r.W, app.DanmuPanelW)
	}
	if r.X <= 0 || r.X+r.W > 1440 {
		t.Errorf("面板位置越界：%+v", r)
	}
}
