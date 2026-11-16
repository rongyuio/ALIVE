package ui

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// ---------------------------------------------------------------- 小窗播放

// 小窗的初始尺寸与最小尺寸：16:9，和画面比例一致，
// 缩到 320 宽仍然能用（baseline 的浏览器 PiP 最小差不多也是这个量级）。
const (
	miniW    = 400
	miniH    = 225
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
// baseline 走浏览器的 Picture-in-Picture（一个由系统托管的视频浮窗）；
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
		if a.Win.IsFullScreen() {
			a.Win.SetFullScreen(false)
		}
	}
	mini := mygo.NewWindow(mygo.WindowOptions{
		Title:     "alive 小窗",
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
}

// showMain 把主窗口重新显示出来并聚焦。
func (a *App) showMain() {
	if a.Win == nil || a.Win.IsDestroyed() {
		return
	}
	a.Win.Update(func() {
		a.Win.Show()
		a.Win.Focus()
	})
}

// MiniPlayer 是小窗的根视图。
//
// 整个画面就是拖动区（我的窗口移动不了比什么都难受），
// 控制条浮在底部、指针进来才淡入——baseline 的 PiP 也是鼠标移上去才出控件。
func (a *App) MiniPlayer(c *ui.Context) {
	s := a.current()
	root := ui.Box(c).Fill().Background(ui.RGB(0, 0, 0)).Clip()
	hovered := root.Hovered()

	root.Children(func() {
		// 画面：真流接上之前用封面顶着，和播放页同一张图、同一份缓存
		w, _ := c.Size()
		if bmp := media.Default.Bitmap(s.CoverURL, w); bmp != nil {
			ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
		} else {
			// 封面还在路上：给小窗自己排一次重绘，否则它不会跟着缓存刷新
			c.After(200 * time.Millisecond)
		}

		// 拖动区：铺满整个画面。控制条后画，所以仍然可点。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).DragWindow()

		// 底部控制条：半透明长条，和主控制台同一套材质。
		//
		// baseline 的 PiP 由浏览器托管，里面一个自绘控件都没有；原生版的窗口
		// 没有系统给的关闭键，所以留最少的两个：播放/暂停、关闭。别的都不放，
		// 小窗越小越不碍事。
		bar := ui.Row(c).Absolute().Left(8).Right(8).Bottom(8).Height(36).
			Padding(4, 8, 4, 8).Radius(12).Gap(8).AlignItems(ui.Center).
			Background(ui.RGBA(9, 10, 12, 0.62)).
			Border(1, ui.RGBA(255, 255, 255, 0.12))
		op := bar.AnimateTo("mini-bar", boolF(hovered), theme.EaseOut(160*time.Millisecond))
		bar.Opacity(op)
		// 淡出时不应该还能点到（看不见的按钮最烦人）
		if !hovered {
			bar.PassThrough()
		}

		bar.Children(func() {
			playIcon := iconPlay
			if a.Playing {
				playIcon = iconPause
			}
			playLabel := "播放"
			if a.Playing {
				playLabel = "暂停"
			}
			a.miniBtn(c, playIcon, playLabel, 20, func() {
				a.Playing = !a.Playing
				a.markDirty()
				a.invalidateWindows()
			})

			ui.Box(c).Grow(1)

			a.miniBtn(c, iconPlayerBack, "关闭小窗", 16, func() {
				if a.Mini != nil {
					a.Mini.Close()
				}
			})
		})
	})
}

// miniBtn 是小窗控制条上的一枚图标按钮：无底色，hover 才亮。
func (a *App) miniBtn(c *ui.Context, ico *ui.SVG, label string, size float32, on func()) {
	btn := ui.Box(c).Size(28, 28).Radius(8).Center().
		Cursor(ui.CursorPointer).Label(label)
	if btn.Hovered() {
		btn.Background(ui.RGBA(255, 255, 255, 0.12))
	}
	if btn.Clicked() {
		on()
	}
	col := ui.RGBA(255, 255, 255, 0.82)
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
// 这几个不是 lucide：baseline 手写了 SVG 去对齐 Win11 自己的 caption 按钮。
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
		btn.Background(ui.RGBA(255, 255, 255, 0.10))
	}
	if btn.Clicked() {
		on()
	}
	col := ui.RGBA(255, 255, 255, 0.92)
	if hovered {
		col = ui.Hex("#ffffff")
	}
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(10).TextColor(col)
	})
}
