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

	// 下面几个都是开关，按住不放不该连续触发。
	//
	// ShortcutRepeated 说的是「刚刚命中的那个键是不是自动重复」，所以必须
	// 先调 Shortcut 再读它。提前读出来拿到的是本帧更早的快捷（或上一帧）
	// 留下的状态，通常恒为 false，闸门形同虚设：按住空格时每个自动重复
	// 都会被当成一次新按下。
	if c.Shortcut(0, ui.KeySpace) || c.Shortcut(0, ui.KeyK) {
		if !c.ShortcutRepeated() {
			a.Playing = !a.Playing
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
	// Esc 和左上角的叉一样是明确退出：关引擎、断弹幕，不留后台声音。
	// 只按 Esc 退全屏的那两条分支已经在上面的 return 里走掉了。
	a.exitPlayer()
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
	if a.Win != nil && a.Win.IsFullScreen() {
		a.Win.SetFullScreen(false)
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
	full := a.Win.IsFullScreen()
	if !full {
		a.WinFull = false
	}
	a.Win.SetFullScreen(!full)
}

// toggleMute 静音 / 取消静音：记住静音前的音量，恢复时回到那个值。
func (a *App) toggleMute() {
	if a.Volume > 0 {
		a.volumeBeforeMute = a.Volume
		a.setVolume(0)
		a.watchSyncActiveAudio()
		return
	}
	if a.volumeBeforeMute > 0 {
		a.setVolume(a.volumeBeforeMute)
		a.watchSyncActiveAudio()
		return
	}
	a.setVolume(0.6)
	a.watchSyncActiveAudio()
}
