package ui

import (
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
	// 系统全屏的上一帧状态：handleEscape 用它认「这次 Esc 是不是已经被窗口拿去退全屏」。
	wasFull := a.wasOSFullScreen
	a.wasOSFullScreen = windowFullScreen(a.Win)

	// Esc 只认一次按键，但不需要自己去重：框架保证一次按键只投递一次
	// （ui/input.go 的 engine.shortcut 命中后把它从 delivered 里摘掉）。以前这里
	// 拿 200ms 时间窗口去重，多此一举，还吞掉了手快时的第二次 Esc。
	if c.Shortcut(0, ui.KeyEscape) {
		a.handleEscape(wasFull)
		return
	}

	if a.Router.Path() != routePlayer {
		return
	}
	// 播放页没有输入框，但浮层里的控件可能出现；有焦点在文本上时一律让开
	if c.FocusTakesText() {
		return
	}

	// 下面几个都是开关，按住不放不该连续触发。
	//
	// ShortcutRepeated 说的是「刚刚命中的那个键是不是自动重复」，所以必须
	// 先调 Shortcut 再读它。提前读出来拿到的是本帧更早的快捷（或上一帧）
	// 留下的状态，通常恒为 false，闸门形同虚设：按住空格时每个自动重复
	// 都会被当成一次新按下。
	if c.Shortcut(0, ui.KeySpace) || c.Shortcut(0, ui.KeyK) {
		if !c.ShortcutRepeated() {
			a.setFocusPlaying(!a.focusPlaying())
		}
		return
	}
	if c.Shortcut(0, ui.KeyF) {
		if !c.ShortcutRepeated() {
			a.toggleFullscreen()
		}
		return
	}
	if c.Shortcut(0, ui.KeyM) {
		if !c.ShortcutRepeated() {
			a.toggleMute()
		}
		return
	}
	// Shift + / 就是 ?
	if c.Shortcut(ui.Shift, ui.KeySlash) {
		if !c.ShortcutRepeated() {
			a.ShortcutsOpen = !a.ShortcutsOpen
		}
		return
	}
}

// handleEscape 是 Esc 的层级：一次只关一层，从最内层往外。
//
// 浮层（PopoverBase）比这里更早拿到 Esc——它们在构建时就消费了，
// 所以能走到这里说明浮层已经关掉或不在了。
func (a *App) handleEscape(wasFull bool) {
	if a.SyncOpen {
		a.SyncOpen = false
		return
	}
	if a.ShortcutsOpen {
		a.ShortcutsOpen = false
		return
	}
	if a.Router == nil {
		return
	}
	if a.Router.Path() != routePlayer {
		// 第二档：结束这一路——和岛上那颗 X 同一个动作。焦点在观测墙里时
		// 只收声、格子留成静音预览；不在墙上就连引擎一起关。没有焦点时
		// closeFocus 什么都不做。
		a.closeFocus()
		return
	}
	// 窗口全屏是我们自己的状态，一定要在系统全屏之前退：
	// 系统全屏可能已经被窗口自己消费掉，错过这里就再没有入口了。
	if a.WinFull {
		a.WinFull = false
		return
	}
	// 全屏时 Esc 只退出全屏，绝不顺手把用户踢出直播间
	if windowFullScreen(a.Win) {
		windowSetFullScreen(a.Win, false)
		return
	}
	// 同一次按键可能先被窗口消费成「退出全屏」，随后又送到这里：上一帧还在系统
	// 全屏、这一帧不在了，就是那种情况。用状态认，比按时间窗口猜可靠；也不会
	// 连带吞掉用户紧接着按的第二下。
	if wasFull {
		return
	}
	// 第一档：收进岛。引擎、取流、声音全留着，岛不动，接着出声；
	// 再按一次才结束这一路。左上角的叉才是「退出直播间」。
	a.collapsePlayer()
}

// toggleWinFull 切换应用内窗口全屏（铺满应用窗口、
// 任务栏仍在，不动系统窗口状态）。
//
// 两种全屏互斥。系统全屏里点这颗按钮，先退出系统全屏再进窗口全屏——不然两个
// 状态同时为真，画面看不出变化，退出系统全屏后还会留着一层没人收的全屏壳。
func (a *App) toggleWinFull() {
	if a.WinFull {
		a.WinFull = false
		return
	}
	if windowFullScreen(a.Win) {
		windowSetFullScreen(a.Win, false)
	}
	a.WinFull = true
}

// toggleFullscreen 切换系统原生全屏。
//
// 进系统全屏前先清掉窗口全屏（原因见 toggleWinFull）：两个状态互斥，退出
// 系统全屏之后不会再掉回一层窗口全屏。
func (a *App) toggleFullscreen() {
	if a.Win == nil {
		return
	}
	full := windowFullScreen(a.Win)
	if !full {
		a.WinFull = false
	}
	windowSetFullScreen(a.Win, !full)
}

// toggleMute 静音 / 取消静音：记住静音前的音量，恢复时回到那个值。
func (a *App) toggleMute() {
	if v := a.focusVolume(); v > 0 {
		a.volumeBeforeMute = v
		a.setVolume(0)
		return
	}
	if a.volumeBeforeMute > 0 {
		a.setVolume(a.volumeBeforeMute)
		return
	}
	a.setVolume(0.6)
}
