package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 数据同步面板的尺寸。
const (
	syncDialogW float32 = 520
	syncBtnH    float32 = 32
	syncTabH    float32 = 28
	// 页签行的几何：图标宽、图标与文字间距、左右 padding、页签间距。
	syncTabIcoW float32 = 14
	syncTabGap  float32 = 2
	syncTabPadH float32 = 14
)

// 面板里正在跑的动作。三个动作共用一个 SyncBusy 位（同一时刻只允许一个），
// 但按钮要知道忙的是不是自己：忙的那个转圈并显示进度，其余的只是禁用。
const (
	syncActExport  = "export"
	syncActImport  = "import"
	syncActProfile = "profile"
)

// openSyncPanel 打开面板，并清掉上一次留下的页签、预览和反馈。
func (a *App) openSyncPanel() {
	a.SyncOpen = true
	a.SyncTab = "export"
	a.SyncNotice = ""
	a.SyncError = ""
	a.SyncPath = ""
	a.SyncPlan = nil
	a.SyncCounts = a.configCounts()
	a.DiagGroups, a.DiagEmpty = nil, false
}

// syncOverlay 是顶栏「数据同步」的模态弹窗。
//
// 结构：标题行 + 导出 / 导入 / 诊断页签 + 内容区。导出页列本机条目并保存
// 成文件；导入页选完文件先列改动、确认后才落地。
func (a *App) syncOverlay(c *ui.Context) {
	// Modal 让窗口级快捷键等待：否则遮罩挡住指针，Space / F / M 仍会
	// 穿透到 Shell 末尾的 shellKeys，对背后正在播的那一路生效。
	// Esc 也由这一层自己消费，和 PopoverBase / DialogBase 的层级一致，
	// 不再依赖 handleEscape 恰好先看 SyncOpen。
	bg := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(ui.RGBA(4, 5, 7, 0.72)).Center().Modal().Label("关闭数据同步")
	if bg.Clicked() || bg.OverlayShortcut(0, ui.KeyEscape) {
		a.SyncOpen = false
		a.SyncPlan = nil
	}

	bg.Children(func() {
		panel := ui.Column(c).Width(syncDialogW).Radius(theme.RadiusMD).
			Background(theme.BgElevated).Border(1, theme.Border).
			Label("数据同步").
			// --shadow-lg：0 38px 72px -30px rgba(0,0,0,.85)
			Shadow(0, 38, 72, -30, theme.ShadowModal).Shadow(0, 6, 16, 0, theme.ShadowContact)
		// 点面板本身不该把它关掉
		if panel.Clicked() {
			a.SyncOpen = true
		}
		panel.Children(func() {
			a.syncHeader(c)
			a.syncTabs(c)
			switch a.SyncTab {
			case "import":
				a.syncImportBody(c)
			case "diag":
				a.syncDiagBody(c)
			default:
				a.syncExportBody(c)
			}
		})
	})
}

// syncHeader 是标题行：圆角图标块 + 标题 + 关闭键。
func (a *App) syncHeader(c *ui.Context) {
	ui.Row(c).Padding(theme.Space4, theme.Space4, theme.Space2, theme.Space4).
		Gap(theme.Space3).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(32, 32).Radius(theme.RadiusMD).Center().
			Background(theme.AccentSoft).Children(func() {
			ui.Icon(c, iconMonitorSmartphone).FontSize(17).TextColor(theme.AccentInk)
		})

		ui.Text(c, "数据同步").FontSize(theme.TextMD).FontWeight(600).
			LetterSpacing(-0.01).TextColor(theme.TextPrimary).Grow(1)

		close := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).Center().
			Background(theme.FillQuaternary).Cursor(ui.CursorPointer).Label("关闭")
		close.Background(hoverSurface(close, "sync-close-bg", close.Hovered(), theme.FillTertiary))
		if close.Clicked() {
			a.SyncOpen = false
			a.SyncPlan = nil
		}
		close.Children(func() {
			col := hoverInk(close, "sync-close-ink", close.Hovered(), theme.TextTertiary, theme.TextPrimary)
			ui.Icon(c, iconX).FontSize(16).TextColor(col)
		})
	})
}

// syncTabItem 是页签行里的一项。
type syncTabItem struct {
	id    string
	ico   *ui.SVG
	label string
}

// syncTabs 是导出 / 导入 / 诊断三枚页签。
//
// 和顶栏平台 tab、一二三级分区同一套语言（见 nav_motion.go）：一枚滑块停在
// 选中项下面，换页签时它滑过去，标签着色跟着弹簧走。旧实现是旧高亮瞬灭、
// 新高亮瞬亮，位置关系断了，看一眼还以为没切。
func (a *App) syncTabs(c *ui.Context) {
	tabs := []syncTabItem{
		{"export", iconUpload, "导出"},
		{"import", iconDownload, "导入"},
		{"diag", iconActivity, "诊断"},
	}

	// 页签宽度按内容量：图标 + 间距 + 文字 + 左右 padding。三枚都是两字，
	// 但量一次比重写一个「正好等于」的常数更稳——换文案时不用回来改数。
	box := rowBox{x: make([]float32, len(tabs)), w: make([]float32, len(tabs))}
	var x float32
	for i, t := range tabs {
		w := syncTabIcoW + 5 + textWidth(c, t.label, theme.TextXS, 600) + 2*syncTabPadH
		box.x[i], box.w[i] = x, w
		x += w + syncTabGap
	}
	selected := 0
	for i, t := range tabs {
		if t.id == a.SyncTab {
			selected = i
			break
		}
	}

	ui.Row(c).Padding(0, theme.Space4, 0, theme.Space4).Children(func() {
		ui.Row(c).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
			Background(theme.FillQuaternary).Children(func() {
			ui.Row(c).Width(box.last()).Height(syncTabH).Children(func() {
				// 滑块先画、压在 tab 下面：位置关系连续，眼睛跟着一枚东西走。
				slider(c, "sync-tab", box, selected, syncTabH, theme.RadiusFull, theme.Accent)
				ui.Row(c).Fill().ZIndex(1).Gap(syncTabGap).Children(func() {
					for i, t := range tabs {
						a.syncTab(c, t, box.w[i], i == selected)
					}
				})
			})
		})
	})
}

// syncTab 是一枚页签。切换时清掉上一页的反馈和待确认的导入，免得旧提示
// 或旧预览跟着串页。
func (a *App) syncTab(c *ui.Context, t syncTabItem, w float32, active bool) {
	tab := ui.Box(c).Width(w).Height(syncTabH).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(t.label)
	if tab.Clicked() && !active {
		a.SyncTab = t.id
		a.SyncNotice = ""
		a.SyncError = ""
		a.SyncPath = ""
		a.SyncPlan = nil
		if t.id == "diag" {
			a.DiagGroups, a.DiagEmpty = diagnosticGroups()
		}
	}

	tab.Children(func() {
		rest, on := theme.TextTertiary, theme.TextPrimary
		if active {
			rest, on = theme.AccentText, theme.AccentText
		}
		col := hoverInk(tab, "sync-tab-ink-all", tab.Hovered(), rest, on)
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ico := ui.Icon(c, t.ico).FontSize(syncTabIcoW)
			ico.TextColor(ico.AnimateColor("sync-tab-ico-"+t.id, col, theme.Slide()))
			txt := ui.Text(c, t.label).FontSize(theme.TextXS)
			txt.TextColor(txt.AnimateColor("sync-tab-ink-"+t.id, col, theme.Slide()))
			if active {
				txt.FontWeight(600)
			} else {
				txt.FontWeight(500)
			}
		})
	})
}

// syncExportBody 是导出页签：本机条目数 + 保存文件。
func (a *App) syncExportBody(c *ui.Context) {
	ui.Column(c).Padding(theme.Space3, theme.Space4, theme.Space4, theme.Space4).
		Gap(theme.Space3).Children(func() {
		a.syncSnapshot(c)
		a.syncFeedback(c)
		ui.Row(c).Justify(ui.End).Children(func() {
			a.syncButton(c, syncActExport, iconUpload, "保存到文件…", true, a.exportConfig, nil)
		})
	})
}

// syncImportBody 是导入页签：没选文件时只有一个入口；选完先列改动，
// 确认才写进本机。
func (a *App) syncImportBody(c *ui.Context) {
	ui.Column(c).Padding(theme.Space3, theme.Space4, theme.Space4, theme.Space4).
		Gap(theme.Space3).Children(func() {
		plan := a.SyncPlan
		if plan == nil {
			a.syncFeedback(c)
			// 没有待导入的文件时，页面只有一个入口。刚做完一次导入时不再
			// 顶一句「尚未选择文件」——结果提示已经说明了状态。
			ui.Column(c).Center().Gap(theme.Space3).Padding(theme.Space2, 0, theme.Space1, 0).
				Children(func() {
					if a.SyncNotice == "" && a.SyncError == "" {
						ui.Box(c).Size(40, 40).Radius(theme.RadiusFull).Center().
							Background(theme.FillQuaternary).Children(func() {
							ui.Icon(c, iconDownload).FontSize(19).TextColor(theme.TextTertiary)
						})
						ui.Text(c, "尚未选择文件").FontSize(theme.TextXS).TextColor(theme.TextTertiary)
					}
					a.syncButton(c, syncActImport, iconDownload, "选择文件…", true, a.pickImportFile, nil)
				})
			return
		}

		a.syncPlanCard(c, plan)
		ui.Row(c).Gap(theme.Space2).Justify(ui.End).Children(func() {
			a.syncButton(c, syncActImport, iconDownload, "重新选择…", false, a.pickImportFile, nil)
			a.syncButton(c, syncActImport, iconX, "取消", false, a.cancelImport, nil)
			if !plan.empty() {
				a.syncButton(c, syncActImport, iconCheck, "确认导入", true, a.confirmImport, nil)
			}
		})
	})
}

// syncSnapshot 是导出页上的本机条目数：用户要知道这份文件里有多少东西。
func (a *App) syncSnapshot(c *ui.Context) {
	items := []struct {
		n     int
		label string
	}{
		{a.SyncCounts.Follows, "关注"},
		{a.SyncCounts.Subs, "订阅"},
		{a.SyncCounts.Watch, "观测墙"},
		{a.SyncCounts.Filters, "屏蔽词"},
	}
	ui.Row(c).Height(64).Radius(theme.RadiusMD).Background(theme.FillQuaternary).
		AlignItems(ui.Center).Children(func() {
		for i, it := range items {
			if i > 0 {
				ui.Box(c).Width(1).Height(28).Background(theme.Separator)
			}
			ui.Column(c).Grow(1).Center().Gap(2).Children(func() {
				ui.Text(c, strconv.Itoa(it.n)).FontSize(theme.TextLG).FontWeight(600).
					TextColor(theme.TextPrimary)
				ui.Text(c, it.label).FontSize(theme.Text2XS).TextColor(theme.TextMeta)
			})
		}
	})
}

// syncPlanCard 是待确认导入的改动清单：文件名、新增概要、逐条覆盖项。
func (a *App) syncPlanCard(c *ui.Context, p *importPlan) {
	ui.Column(c).Gap(theme.Space2).Padding(theme.Space3).Radius(theme.RadiusMD).
		Background(theme.FillQuaternary).Children(func() {
		ui.Text(c, p.FileName).FontSize(theme.TextSM).FontWeight(600).
			TextColor(theme.TextPrimary).SingleLine()

		if p.empty() {
			ui.Text(c, "没有可导入的内容：这些数据本机已经有了").
				FontSize(theme.TextXS).TextColor(theme.TextTertiary)
			return
		}
		if p.Adds != "" {
			syncChangeLine(c, "新增", p.Adds, theme.SuccessInk)
		}
		for _, line := range p.Overwrites {
			syncChangeLine(c, "覆盖", line, theme.DangerInk)
		}
	})
}

// syncChangeLine 是清单里的一行：左侧一枚短标签，右侧具体内容。
func syncChangeLine(c *ui.Context, tag, text string, tint ui.Color) {
	ui.Row(c).Gap(theme.Space2).AlignItems(ui.Start).Children(func() {
		ui.Text(c, tag).FontSize(theme.Text2XS).FontWeight(600).TextColor(tint).
			Width(24).SingleLine()
		ui.Text(c, text).FontSize(theme.TextXS).TextColor(theme.TextSecondary).Grow(1)
	})
}

// syncFeedback 画上一次动作的结果。导出、导入两页共用，切换页签时清掉。
func (a *App) syncFeedback(c *ui.Context) {
	if a.SyncError != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, iconAlert).FontSize(14).TextColor(theme.DangerInk)
			ui.Text(c, a.SyncError).FontSize(theme.TextXS).TextColor(theme.DangerInk).Grow(1)
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

// syncHint 是诊断页签的一行灰色说明。
func (a *App) syncHint(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(theme.Text2XS).TextColor(theme.TextMeta)
}

// syncButton 是面板里的胶囊按钮：32 高、圆角 full、12px 半粗。
//
// primary 是 accent 底 + accent-text 字，其余是 fill-tertiary 底加描边。
// action 说明这枚按钮属于哪个动作：只有自己忙时图标才转、文字才换成进度；
// 别的动作忙时它同样禁用（同一时刻只跑一个），但保持原样——用户要能看出
// 「现在跑的是哪一件」。
//
// onBusy 非空表示这件动作忙的时候按钮不锁死，而是变成「停止」并调用它。
// 剖析要等 30 秒，用户点错了或者只想看个大概，必须能中途停。
func (a *App) syncButton(c *ui.Context, action string, ico *ui.SVG, label string, primary bool, on, onBusy func()) {
	mine := a.SyncBusy && a.SyncBusyAction == action
	stoppable := mine && onBusy != nil
	// Label 跟着屏幕文字走，但倒计时每秒变、ref 会跟着失效，所以停住态用
	// 固定前缀「停止剖析」，自动化按它点得到。
	btnLabel := label
	if stoppable {
		btnLabel = "停止剖析"
	}
	btn := ui.Box(c).Height(syncBtnH).Padding(0, 14, 0, 14).Radius(theme.RadiusFull).
		Center().Label(btnLabel)
	if primary {
		btn.Background(theme.Accent)
	} else {
		btn.Background(theme.FillTertiary).Border(1, theme.Border)
	}
	if a.SyncBusy && !stoppable {
		btn.Opacity(0.5).Cursor(ui.CursorDefault)
	} else {
		btn.Cursor(ui.CursorPointer)
		if primary {
			btn.Background(hoverSurface(btn, "sync-btn-bg", btn.Hovered(), theme.AccentHover))
		} else {
			btn.Background(hoverSurface(btn, "sync-btn-bg", btn.Hovered(), theme.FillSecondary))
		}
		switch {
		case stoppable:
			if btn.Clicked() {
				onBusy()
			}
		case !a.SyncBusy:
			if btn.Clicked() {
				on()
			}
		}
	}

	// 剖析要 30 秒，转圈说明不了还剩多久；文字直接倒计时。时间取构建帧的
	// 时钟（c.Now()），Tester 才能用 Advance 把它推到想要的位置。
	text := label
	if stoppable {
		text = profileStopLabel(a.ProfStarted, c.Now())
	}

	btn.Children(func() {
		col := theme.TextPrimary
		if primary {
			col = theme.AccentText
		}
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			switch {
			case stoppable:
				// 方块停止图标：告诉用户这一下是按下去停，不是再转一圈。
				ui.Icon(c, iconStop).FontSize(13).TextColor(col)
			case mine:
				// 跑着的时候图标转起来，兼作进度提示
				ui.Icon(c, iconSync).FontSize(14).TextColor(col).Rotate(
					btn.Loop("sync-btn-spin", theme.SpinPeriod, ui.Linear) * 360)
			default:
				ui.Icon(c, ico).FontSize(14).TextColor(col)
			}
			ui.Text(c, text).FontSize(theme.TextXS).FontWeight(600).TextColor(col)
		})
	})
}

// profileStopLabel 是剖析期间按钮上的文字。向上取整：开始显示 30s，最后
// 不足 1 秒显示 1s，不会出现已经停了却写着 0s 的那一帧。
func profileStopLabel(start, now time.Time) string {
	left := int(start.Add(profileWindow).Sub(now).Seconds())
	if left < 0 {
		left = 0
	}
	if start.Add(profileWindow).Sub(now) > time.Duration(left)*time.Second {
		left++
	}
	return fmt.Sprintf("停止剖析 · %ds", left)
}
