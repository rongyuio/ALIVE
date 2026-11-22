package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 快捷键说明浮层也是模态：打开时背后的播放快捷键必须让路。
//
// 遮罩原先只是普通 Absolute Box，Space / K / F / M 会穿到 Shell 末尾的
// shellKeys，对背后正在播的那一路生效。
func TestShortcutsOverlayBlocksPlayerShortcuts(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	app.Playing = false
	app.ShortcutsOpen = true

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if app.Playing {
		t.Fatal("快捷键说明打开时，空格仍然穿透去切换了播放状态")
	}

	// Esc 由浮层自己消费：一次只关这一层，不会继续退全屏或回列表。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.ShortcutsOpen {
		t.Fatal("Esc 没有关掉快捷键说明")
	}
	if app.Router.Path() != routePlayer {
		t.Fatal("关浮层的 Esc 同时把用户退出了播放页")
	}
}
