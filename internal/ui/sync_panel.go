package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 数据同步面板的度量，取自 baseline 的 LanSyncModal.module.css。
const (
	syncDialogW float32 = 520
	syncBtnH    float32 = 32
	syncTabH    float32 = 28
)

// openSyncPanel 打开面板，并清掉上一次留下的页签和反馈。
func (a *App) openSyncPanel() {
	a.SyncOpen = true
	a.SyncTab = "export"
	a.SyncNotice = ""
	a.SyncError = ""
}

// syncOverlay 是顶栏「数据同步」的模态弹窗。
//
// 结构对齐 baseline 的 LanSyncModal：标题行 + 导出/导入两个页签 + 内容区。
// 局域网共享（二维码、一键导入）还没做，所以两个页签里各自只有一条本地
// 文件的路；补局域网时在对应 actionRow 里加按钮即可。
func (a *App) syncOverlay(c *ui.Context) {
	bg := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(ui.RGBA(4, 5, 7, 0.72)).Center().Label("关闭数据同步")
	if bg.Clicked() {
		a.SyncOpen = false
	}

	bg.Children(func() {
		panel := ui.Column(c).Width(syncDialogW).Radius(theme.RadiusMD).
			Background(theme.BgElevated).Border(1, theme.Border).
			Label("数据同步").
			// baseline --shadow-lg：0 38px 72px -30px rgba(0,0,0,.85)
			Shadow(0, 38, 72, -30, theme.ShadowLg)
		// 点面板本身不该把它关掉
		if panel.Clicked() {
			a.SyncOpen = true
		}
		panel.Children(func() {
			a.syncHeader(c)
			a.syncTabs(c)
			if a.SyncTab == "import" {
				a.syncImportBody(c)
			} else {
				a.syncExportBody(c)
			}
		})
	})
}

// syncHeader 是标题行：圆角图标块 + 标题 / 副标题 + 关闭键。
func (a *App) syncHeader(c *ui.Context) {
	ui.Row(c).Padding(14, 16, 10, 16).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(32, 32).Radius(theme.RadiusMD).Center().
			Background(theme.AccentSoft).Children(func() {
			ui.Icon(c, iconMonitorSmartphone).FontSize(17).TextColor(theme.AccentInk)
		})

		ui.Column(c).Grow(1).Gap(1).Children(func() {
			ui.Text(c, "数据同步").FontSize(theme.TextMD).FontWeight(600).
				LetterSpacing(-0.01).TextColor(theme.TextPrimary)
			ui.Text(c, "同步关注、订阅、主题与弹幕设置").FontSize(theme.Text2XS).
				TextColor(theme.TextTertiary).SingleLine()
		})

		close := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).Center().
			Background(theme.FillQuaternary).Cursor(ui.CursorPointer).Label("关闭")
		if close.Hovered() {
			close.Background(theme.FillTertiary)
		}
		if close.Clicked() {
			a.SyncOpen = false
		}
		close.Children(func() {
			col := theme.TextTertiary
			if close.Hovered() {
				col = theme.TextPrimary
			}
			ui.Icon(c, iconX).FontSize(16).TextColor(col)
		})
	})
}

// syncTabs 是导出 / 导入两枚页签。
func (a *App) syncTabs(c *ui.Context) {
	ui.Row(c).Padding(0, 16, 0, 16).Children(func() {
		ui.Row(c).Gap(2).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
			Background(theme.FillQuaternary).Border(1, theme.Border).
			Children(func() {
				a.syncTab(c, "export", iconUpload, "导出")
				a.syncTab(c, "import", iconDownload, "导入")
			})
	})
}

// syncTab 是一枚页签。切换时清掉上一页的反馈，免得旧提示跟着串页。
func (a *App) syncTab(c *ui.Context, id string, ico *ui.SVG, label string) {
	active := a.SyncTab == id
	if a.SyncTab == "" && id == "export" {
		active = true
	}

	tab := ui.Row(c).Height(syncTabH).Padding(0, 14, 0, 14).Gap(5).
		Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).Label(label)
	if active {
		tab.Background(theme.Accent)
	}
	if tab.Clicked() && !active {
		a.SyncTab = id
		a.SyncNotice = ""
		a.SyncError = ""
	}

	tab.Children(func() {
		col := theme.TextTertiary
		if active {
			col = theme.AccentText
		} else if tab.Hovered() {
			col = theme.TextPrimary
		}
		ui.Icon(c, ico).FontSize(14).TextColor(col)
		txt := ui.Text(c, label).FontSize(theme.TextXS).TextColor(col)
		if active {
			txt.FontWeight(600)
		} else {
			txt.FontWeight(500)
		}
	})
}

// syncExportBody 是导出页签：说明 + 导出 JSON。
func (a *App) syncExportBody(c *ui.Context) {
	ui.Column(c).Padding(12, 16, 16, 16).Gap(10).Children(func() {
		a.syncHint(c, "导出一份 JSON 快照，包含关注、订阅、主题配色、清晰度与弹幕设置。")

		ui.Row(c).Gap(8).Children(func() {
			a.syncButton(c, iconDownload, "导出 JSON", true, a.exportConfig)
		})

		a.syncFeedback(c)
	})
}

// syncImportBody 是导入页签：说明 + 导入 JSON。
func (a *App) syncImportBody(c *ui.Context) {
	ui.Column(c).Padding(12, 16, 16, 16).Gap(10).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			a.syncButton(c, iconDownload, "导入 JSON", true, a.importConfig)
		})

		a.syncFeedback(c)

		a.syncHint(c, "关注和订阅按增量合并，不会删掉本机已有的；主题、清晰度、音量与弹幕设置以文件为准。")
		a.syncHint(c, "也能读 DTV 旧版导出的配置文件。")
	})
}

// syncFeedback 画上一次导出 / 导入的结果。两页共用，切换页签时会清掉。
func (a *App) syncFeedback(c *ui.Context) {
	if a.SyncError != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, iconAlert).FontSize(14).TextColor(theme.Danger)
			ui.Text(c, a.SyncError).FontSize(theme.TextXS).TextColor(theme.Danger).Grow(1)
		})
		return
	}
	if a.SyncNotice == "" {
		return
	}
	ui.Row(c).Gap(8).Padding(8, 10, 8, 10).Radius(theme.RadiusMD).
		Background(theme.AccentSofter).Border(1, theme.AccentSoft).
		AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconOkCircle).FontSize(15).TextColor(theme.AccentInk)
		ui.Text(c, a.SyncNotice).FontSize(theme.TextXS).TextColor(theme.TextSecondary).Grow(1)
	})
}

// syncHint 是内容区的一行灰色说明。
func (a *App) syncHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(theme.Text2XS).TextColor(theme.TextMeta)
}

// syncButton 是面板里的胶囊按钮：32 高、圆角 full、12px 半粗。
//
// primary 是 accent 底 + accent-text 字，其余是 fill-tertiary 底加描边。
// busy 时按 baseline 的 :disabled 处理：透明度 .5、光标回箭头、不响应点击。
func (a *App) syncButton(c *ui.Context, ico *ui.SVG, label string, primary bool, on func()) {
	btn := ui.Box(c).Height(syncBtnH).Padding(0, 14, 0, 14).Radius(theme.RadiusFull).
		Center().Label(label)
	if primary {
		btn.Background(theme.Accent)
	} else {
		btn.Background(theme.FillTertiary).Border(1, theme.Border)
	}
	if a.SyncBusy {
		btn.Opacity(0.5).Cursor(ui.CursorDefault)
	} else {
		btn.Cursor(ui.CursorPointer)
		if btn.Hovered() && primary {
			btn.Background(theme.AccentHover)
		} else if btn.Hovered() {
			btn.Background(theme.FillSecondary)
		}
		if btn.Clicked() {
			on()
		}
	}

	btn.Children(func() {
		col := theme.TextPrimary
		if primary {
			col = theme.AccentText
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			if a.SyncBusy {
				// 读写文件期间给一个转圈，替代 baseline 的 Loader2
				ui.Icon(c, iconSync).FontSize(14).TextColor(col).Rotate(
					btn.Loop("sync-btn-spin", 900*time.Millisecond, ui.Linear) * 360)
			} else {
				ui.Icon(c, ico).FontSize(14).TextColor(col)
			}
			ui.Text(c, label).FontSize(theme.TextXS).FontWeight(600).TextColor(col)
		})
	})
}
