package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 全屏时 Shell 整块不构建顶栏，配色 / 登录按钮（浮层的锚点）一起消失。
//
// 开关状态留在 App 上不清，退出全屏后按钮重建，PopoverBase 看到 open
// 仍为 true，浮层会在没有任何点击的情况下自己弹出来。Navbar 对
// SearchOpen 有「按钮不在就收掉」的处理，全屏这条路以前漏了。
func TestTopbarPopoversCloseWhenFullscreen(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if err := tt.Click("配色面板"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.PaletteOpen {
		t.Fatal("点击没有打开配色浮层")
	}

	app.AuthOpen = true
	app.WinFull = true
	tt.Frame()
	if app.PaletteOpen {
		t.Fatal("进入全屏后配色浮层的开关还留着")
	}
	if app.AuthOpen {
		t.Fatal("进入全屏后登录浮层的开关还留着")
	}
	if _, ok := tt.Find("配色浮层"); ok {
		t.Fatal("全屏下还在画配色浮层")
	}

	app.WinFull = false
	tt.Frame()
	if _, ok := tt.Find("配色浮层"); ok {
		t.Fatal("退出全屏后配色浮层自己弹回来了")
	}
}
