package ui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// shellKeys 处理全局键盘，覆盖关注面板和播放页。
//
// 调用点在 Shell 的末尾，也就是整棵视图构建完之后。这个位置是有意的：
// 浮层的 Esc 由 PopoverBase 在构建时消费（OverlayShortcut），如果这里先跑，
// 一次 Esc 会同时关掉浮层和它外面的东西。输入框同理——顶栏搜索、分区搜索、
// 关注面板搜索都在 OnTextKey 里自己处理并吃掉按键，轮不到这里。
//
// 一次 Esc 只做一件事，从最内层往外：浮层 → 关注面板 → 全屏 → 返回列表。
func (a *App) shellKeys(c *ui.Context) {
	// Esc 只认一次按键。
	//
	// 视图一帧会构建多趟（测量、布局、绘制），按键在每一趟里都重新投递，
	// 不挡住的话一次 Esc 会先关掉弹层、紧接着又在下一趟把用户送回列表——
	// 按一下退两层。200ms 的窗口足够覆盖同一次按键的所有投递，
	// 人也不会快到在这个间隔里连按两次。
	if c.Shortcut(0, ui.KeyEscape) {
		if time.Since(a.lastEscape) >= 200*time.Millisecond {
			a.lastEscape = time.Now()
			a.handleEscape()
		}
		return
	}

	if a.Router.Path() != routePlayer {
		return
	}
	// 播放页没有输入框，但浮层里的控件可能出现；有焦点在文本上时一律让开
	if c.FocusTakesText() {
		return
	}

	// 下面几个都是开关，按住不放不该连续触发
	repeat := c.ShortcutRepeated()
	if c.Shortcut(0, ui.KeySpace) || c.Shortcut(0, ui.KeyK) {
		if !repeat {
			a.Playing = !a.Playing
		}
		return
	}
	if c.Shortcut(0, ui.KeyF) {
		if !repeat {
			a.toggleFullscreen()
		}
		return
	}
	if c.Shortcut(0, ui.KeyM) {
		if !repeat {
			a.toggleMute()
		}
		return
	}
	// Shift + / 就是 ?
	if c.Shortcut(ui.Shift, ui.KeySlash) {
		if !repeat {
			a.ShortcutsOpen = !a.ShortcutsOpen
		}
		return
	}
}

// handleEscape 是 Esc 的层级：一次只关一层，从最内层往外。
//
// 浮层（PopoverBase）比这里更早拿到 Esc——它们在构建时就消费了，
// 所以能走到这里说明浮层已经关掉或不在了。
func (a *App) handleEscape() {
	if a.SyncOpen {
		a.SyncOpen = false
		return
	}
	if a.ShortcutsOpen {
		a.ShortcutsOpen = false
		return
	}
	if a.Router.Path() != routePlayer {
		return
	}
	// 窗口全屏是我们自己的状态，一定要在系统全屏之前退：
	// 系统全屏可能已经被窗口自己消费掉，错过这里就再没有入口了。
	if a.WinFull {
		a.WinFull = false
		a.lastFullscreenExit = time.Now()
		return
	}
	// 全屏时 Esc 只退出全屏，绝不顺手把用户踢回列表
	if a.Win != nil && a.Win.IsFullScreen() {
		a.Win.SetFullScreen(false)
		a.lastFullscreenExit = time.Now()
		return
	}
	// 同一次按键可能先被窗口消费成「退出全屏」，再传到这里
	if time.Since(a.lastFullscreenExit) < 500*time.Millisecond {
		return
	}
	a.Router.Back()
}

// toggleFullscreen 切换窗口全屏。
func (a *App) toggleFullscreen() {
	if a.Win == nil {
		return
	}
	a.Win.SetFullScreen(!a.Win.IsFullScreen())
}

// toggleMute 静音 / 取消静音：记住静音前的音量，恢复时回到那个值。
func (a *App) toggleMute() {
	if a.Volume > 0 {
		a.volumeBeforeMute = a.Volume
		a.Volume = 0
		return
	}
	if a.volumeBeforeMute > 0 {
		a.Volume = a.volumeBeforeMute
		return
	}
	a.Volume = 0.6
}
