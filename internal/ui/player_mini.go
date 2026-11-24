package ui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// ---------------------------------------------------------------- 小窗播放

// 小窗的初始尺寸与最小尺寸：16:9，和画面比例一致。
// 500 宽足够看清画面又不挡别的窗口；缩到 240 宽仍然能用（浏览器 PiP 也是这个量级）。
const (
	miniW    = 500
	miniH    = 281
	miniMinW = 240
	miniMinH = 135
)

// miniOpen 报告小窗是否还活着。
//
// 不依赖「关窗时清指针」的回调：窗口可能是被 Alt+F4 或系统关掉的，
// 每次直接问窗口自己有没有被销毁，指针就永远不会指向一个死窗口。
func (a *App) miniOpen() bool {
	return a.Mini != nil && !a.Mini.IsDestroyed()
}

// togglePip 开关小窗播放。
//
// 浏览器那边的 Picture-in-Picture 是由系统托管的视频浮窗；
// 原生这边没有那套 API，就真开一个无边框置顶窗口来装画面。
//
// 开小窗时主窗口收起来——小窗接管画面，原来的界面留着只会碍事；
// 关掉小窗（点叉、Alt+F4、系统菜单都算）再把主界面还回来。
func (a *App) togglePip() {
	if a.miniOpen() {
		a.Mini.Close()
		return
	}
	// 主窗口要是正全屏着，先退出来：隐藏一个全屏窗口，恢复时的窗口状态不可预期
	if a.Win != nil {
		a.WinFull = false
		if windowFullScreen(a.Win) {
			a.Win.SetFullScreen(false)
		}
	}
	mini := mygo.NewWindow(mygo.WindowOptions{
		Title:     "ALIVE 小窗",
		Width:     miniW,
		Height:    miniH,
		MinWidth:  miniMinW,
		MinHeight: miniMinH,
		Frameless: true,
		// 小窗的全部意义就是浮在别的窗口上面
		AlwaysOnTop:     true,
		BackgroundColor: "#000000",
		Content:         ui.View(a.MiniPlayer),
	})
	a.Mini = mini
	// 还原挂在窗口的关闭事件上，而不是只挂在自己的关闭按钮上：
	// 用户 Alt+F4 关掉小窗时，主界面一样得回来。
	mini.OnClosed(func() { a.showMain() })
	if a.Win != nil {
		a.Win.Hide()
	}
	// 主窗口被小窗换下去：墙这一屏整体看不见了。Shell 不再重建，走不到
	// watchHide，所以这里主动标一次——开了「暂离时省流量」才会真的回收。
	a.WatchHidden()
}

// showMain 把主窗口重新显示出来并聚焦。
func (a *App) showMain() {
	if a.Win == nil || a.Win.IsDestroyed() {
		return
	}
	a.Win.Update(func() {
		a.Win.Show()
		a.Win.Focus()
		a.WatchShown()
	})
}

// MiniPlayer 是小窗的根视图。
//
// 整个画面就是拖动区（我的窗口移动不了比什么都难受），
// 控制条浮在底部、指针进来才淡入——系统的 PiP 也是鼠标移上去才出控件。
func (a *App) MiniPlayer(c *ui.Context) {
	// 小窗存在的时候主窗口是收起来的，播放页不再构建——那边每帧做的
	// syncPlayer / trackFirstFrame 全部停摆，得在这里接上：
	//   syncPlayer    暂停键翻了焦点会话的播放意图之后真的要推给播放器
	//   trackFirstFrame  首帧预算、地址到期、断流看门狗
	a.syncPlayer()
	a.trackFirstFrame()

	root := ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Clip()
	hovered := root.Hovered()

	root.Children(func() {
		// 画面：和播放页同一条优先级——有播放器就画真流，首帧没到（或者
		// 播放器还没建出来）再用封面顶着。
		//
		// Video 必须画：MyGo 的播放器只在「窗口可见、并且这一帧把它画出来」
		// 时推进，mpv 的画面也是那时才被取走。之前这里只有封面，主窗口一藏，
		// 全应用就没有任何一帧在显示这个播放器，画面自然永远是静止的封面。
		p := a.media.eng()
		if p != nil {
			ui.Video(c, p).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
		}
		if p == nil || !p.Ready() {
			w, _ := c.Size()
			if bmp := media.Default.Bitmap(a.currentCover(), w); bmp != nil {
				ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
			} else {
				// 封面还在路上：给小窗自己排一次重绘，否则它不会跟着缓存刷新
				c.After(200 * time.Millisecond)
			}
		}

		// 拖动区：铺满整个画面。控制条后画，所以仍然可点。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).DragWindow()

		// 底部控制条：半透明长条，和主控制台同一套材质。
		//
		// 浏览器的 PiP 由系统托管，里面一个自绘控件都没有；原生版的窗口
		// 没有系统给的关闭键，所以留最少的两个：播放/暂停、关闭。别的都不放，
		// 小窗越小越不碍事。
		bar := ui.Row(c).Absolute().Left(8).Right(8).Bottom(8).Height(36).
			Padding(4, 8, 4, 8).Radius(12).Gap(8).AlignItems(ui.Center).
			Background(ui.RGBA(9, 10, 12, 0.62)).
			Border(1, theme.MediaHair)
		op := bar.AnimateTo("mini-bar", boolF(hovered), theme.EaseOut(160*time.Millisecond))
		bar.Opacity(op)
		// 淡出时不应该还能点到（看不见的按钮最烦人）
		if !hovered {
			bar.PassThrough()
		}

		bar.Children(func() {
			playIcon := iconPlay
			if a.focusPlaying() {
				playIcon = iconPause
			}
			playLabel := "播放"
			if a.focusPlaying() {
				playLabel = "暂停"
			}
			a.miniBtn(c, playIcon, playLabel, 20, func() {
				a.setFocusPlaying(!a.focusPlaying())
			})

			ui.Box(c).Grow(1)

			a.miniBtn(c, iconPlayerBack, "关闭小窗", 16, func() {
				if a.Mini != nil {
					a.Mini.Close()
				}
			})
		})
	})

	// 续帧：播放器停帧不会主动报错，只靠用户操作触发帧会漏掉故障。
	// 节拍和播放页一致——等首帧时催得紧，出图之后降成健康检查。
	if p := a.media.eng(); a.focusPlaying() && p != nil && p.Err() == nil {
		if !a.media.firstFrame() {
			c.After(firstFramePollInterval)
		} else {
			c.After(playbackHealthPollInterval)
		}
	}
}

// miniBtn 是小窗控制条上的一枚图标按钮：无底色，hover 才亮。
func (a *App) miniBtn(c *ui.Context, ico *ui.SVG, label string, size float32, on func()) {
	btn := ui.Box(c).Size(28, 28).Radius(8).Center().
		Cursor(ui.CursorPointer).Label(label)
	if btn.Hovered() {
		btn.Background(theme.MediaHair)
	}
	if btn.Clicked() {
		on()
	}
	col := theme.MediaTextBody
	if btn.Hovered() {
		col = ui.Hex("#ffffff")
	}
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(size).TextColor(col)
	})
}

// invalidateWindows 让主窗口和小窗都重画一遍：两边读的是同一份播放状态。
func (a *App) invalidateWindows() {
	if a.Win != nil {
		a.Win.Invalidate()
	}
	if a.miniOpen() {
		a.Mini.Invalidate()
	}
}

// boolF 把布尔转成动画目标值。
func boolF(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

// ------------------------------------------------- 全屏下的窗口按钮（Win11 样式）

// 10×10 的 1px 描边，和系统标题栏按钮同尺寸同线宽。
// 这几个不是 lucide：都是手写 SVG 去对齐 Win11 自己的 caption 按钮。
func captionSVG(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10" fill="none" ` +
			`stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

var (
	captionMin     = captionSVG(`<path d="M0 5h10"/>`)
	captionMax     = captionSVG(`<rect x="0.5" y="0.5" width="9" height="9" rx="1.5"/>`)
	captionRestore = captionSVG(`<path d="M2.5 2.5V1.5A1 1 0 0 1 3.5.5h5A1 1 0 0 1 9.5 1.5v5a1 1 0 0 1-1 1h-1"/><rect x="0.5" y="3.5" width="6" height="6" rx="1"/>`)
	captionClose   = captionSVG(`<path d="M0 0l10 10M10 0L0 10"/>`)
)

// playerCaption 是两种全屏下贴在画面右上角的三个窗口按钮。
//
// 全屏时应用自己的标题栏被画面盖住了，最小化 / 最大化 / 关闭得有地方可去。
// 尺寸 46×32、无底色，hover 一层白 10%，关闭键 hover 转 Win11 的 #C42B1C。
func (a *App) playerCaption(c *ui.Context) {
	if !a.playerFullscreen() || a.Win == nil {
		return
	}
	maximized := a.Win.IsMaximized()

	row := ui.Row(c).Absolute().Top(0).Right(0).Height(32)
	a.chromeFade(row)
	row.Children(func() {
		a.captionBtn(c, captionMin, "最小化", false, func() { a.Win.Minimize() })
		icon := captionMax
		if maximized {
			icon = captionRestore
		}
		a.captionBtn(c, icon, "最大化", false, func() {
			if maximized {
				a.Win.Unmaximize()
			} else {
				a.Win.Maximize()
			}
		})
		a.captionBtn(c, captionClose, "关闭窗口", true, func() { a.Win.Close() })
	})
}

// captionBtn 是一枚 caption 按钮。close 决定 hover 是不是 Win11 的关闭红。
func (a *App) captionBtn(c *ui.Context, ico *ui.SVG, label string, close bool, on func()) {
	w := float32(46)
	btn := ui.Box(c).Width(w).Height(32).Center().
		Cursor(ui.CursorPointer).Label(label)
	hovered := btn.Hovered()
	switch {
	case hovered && close:
		btn.Background(ui.Hex("#c42b1c"))
	case hovered:
		btn.Background(theme.MediaHair)
	}
	if btn.Clicked() {
		on()
	}
	col := theme.MediaText
	if hovered {
		col = ui.Hex("#ffffff")
	}
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(10).TextColor(col)
	})
}
