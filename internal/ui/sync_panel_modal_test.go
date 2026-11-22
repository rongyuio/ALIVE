package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 数据同步面板自称模态：打开时背后的播放快捷键必须让路。
//
// 遮罩原先只是普通 Absolute Box，指针被挡住了，键盘没有：Space 会穿到
// Shell 末尾的 shellKeys，把背后正在播的那一路暂停。Modal 才能让窗口级
// 快捷键等待。
func TestSyncPanelBlocksPlayerShortcuts(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	app.Playing = false
	app.SyncOpen = true

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if app.Playing {
		t.Fatal("数据同步面板打开时，空格仍然穿透去切换了播放状态")
	}

	// Esc 由面板自己消费：一次只关这一层，不会继续退全屏或回列表。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.SyncOpen {
		t.Fatal("Esc 没有关掉数据同步面板")
	}
	if app.Router.Path() != routePlayer {
		t.Fatal("关面板的 Esc 同时把用户退出了播放页")
	}
}
