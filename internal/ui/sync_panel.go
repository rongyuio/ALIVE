package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// 数据同步面板的尺寸。
const (
	syncDialogW float32 = 520
	syncBtnH    float32 = 32
	syncTabH    float32 = 34
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
	// 层交给 components.Dialog：Modal 让窗口级快捷键等待（否则遮罩挡住指针
	// 时 Space / F / M 仍会穿透到 Shell 末尾的 shellKeys，对背后正在播的那
	// 一路生效）、点面板不关、Esc 与点外部关闭，它全自带。
	//
	// 关闭时要把挂起的导入计划一起收掉，而 Dialog 只写 *open、不给回调，
	// 所以在调用前后自己比一次。
	wasOpen := a.SyncOpen
	components.Dialog(c, &a.SyncOpen, components.DialogOptions{Label: "数据同步"},
		func(panel ui.Element) {
			panel = orPanel(panel, theme.BgElevated)
			panel.Width(syncDialogW).Radius(theme.RadiusMD).
				Border(1, theme.Border).
				// --shadow-lg：0 38px 72px -30px rgba(0,0,0,.85)
				Shadow(0, 38, 72, -30, theme.ShadowModal).Shadow(0, 6, 16, 0, theme.ShadowContact)
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
	if wasOpen && !a.SyncOpen {
		a.SyncPlan = nil
	}
}

// syncHeader 是标题行：圆角图标块 + 标题 + 关闭键。
func (a *App) syncHeader(c *ui.Context) {
	ui.Row(c).Padding(theme.Space4, theme.Space4, theme.Space2, theme.Space4).
		Gap(theme.Space3).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(32, 32).Radius(theme.RadiusMD).Center().
			Background(theme.AccentSoft).Children(func() {
			components.Icon(c, iconMonitorSmartphone).FontSize(17).TextColor(theme.AccentInk)
		})

		components.Text(c, "数据同步").FontSize(theme.TextMD).FontWeight(600).
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
			components.Icon(c, iconX).FontSize(16).TextColor(col)
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
		// 着色只在这一处动一次：hoverInk 里那条弹簧已经把颜色算出来了，
		// 图标和文字直接吃结果。以前它们各自又对 col 跑了一遍 AnimateColor，
		// 于是换页签时颜色要过两层弹簧——第一层还没走完，第二层的目标已经在
		// 动，看着比平台 tab 慢半拍还发飘。平台 tab 那边一直是单层的
		// （home.go:1480「col := hoverInk(...)」→ TextColor(col)）。
		col := hoverInk(tab, "sync-tab-ink-"+t.id, tab.Hovered(), rest, on)
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			components.Icon(c, t.ico).FontSize(syncTabIcoW).TextColor(col)
			txt := components.Text(c, t.label).FontSize(theme.TextXS).TextColor(col)
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
			a.syncButton(c, syncActExport, iconUpload, "保存到文件", true, a.exportConfig, nil)
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
			// 没有待导入的文件时，页面只有一个入口。不摆空状态的图标框和
			// 「尚未选择文件」：这张面孔里唯一的动作就是下面那颗按钮，
			// 再来一块灰底框加一句它自己的说明，全是废话。
			ui.Column(c).Center().Padding(theme.Space2, 0, theme.Space1, 0).
				Children(func() {
					a.syncButton(c, syncActImport, iconDownload, "选择文件", true, a.pickImportFile, nil)
				})
			return
		}

		a.syncPlanCard(c, plan)
		ui.Row(c).Gap(theme.Space2).Justify(ui.End).Children(func() {
			a.syncButton(c, syncActImport, iconDownload, "重新选择", false, a.pickImportFile, nil)
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
				components.Text(c, strconv.Itoa(it.n)).FontSize(theme.TextLG).FontWeight(600).
					TextColor(theme.TextPrimary)
				components.Text(c, it.label).FontSize(theme.Text2XS).TextColor(theme.TextMeta)
			})
		}
	})
}

// syncPlanCard 是待确认导入的改动清单：文件名、新增概要、逐条覆盖项。
func (a *App) syncPlanCard(c *ui.Context, p *importPlan) {
	ui.Column(c).Gap(theme.Space2).Padding(theme.Space3).Radius(theme.RadiusMD).
		Background(theme.FillQuaternary).Children(func() {
		components.Text(c, p.FileName).FontSize(theme.TextSM).FontWeight(600).
			TextColor(theme.TextPrimary).SingleLine()

		if p.empty() {
			components.Text(c, "没有可导入的内容：这些数据本机已经有了").
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
		components.Text(c, tag).FontSize(theme.Text2XS).FontWeight(600).TextColor(tint).
			Width(24).SingleLine()
		components.Text(c, text).FontSize(theme.TextXS).TextColor(theme.TextSecondary).Grow(1)
	})
}

// syncFeedback 画上一次动作的结果。导出、导入两页共用，切换页签时清掉。
func (a *App) syncFeedback(c *ui.Context) {
	if a.SyncError != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Start).Children(func() {
			components.Icon(c, iconAlert).FontSize(14).TextColor(theme.DangerInk)
			components.Text(c, a.SyncError).FontSize(theme.TextXS).TextColor(theme.DangerInk).Grow(1)
		})
		return
	}
	if a.SyncNotice == "" {
		return
	}
	ui.Row(c).Gap(8).Padding(8, 10, 8, 10).Radius(theme.RadiusMD).
		Background(theme.AccentSofter).Border(1, theme.AccentSoft).
		AlignItems(ui.Center).Children(func() {
		components.Icon(c, iconOkCircle).FontSize(15).TextColor(theme.AccentInk)
		components.Text(c, a.SyncNotice).FontSize(theme.TextXS).TextColor(theme.TextSecondary).Grow(1)
	})
}

// syncHint 是诊断页签的一行灰色说明。
func (a *App) syncHint(c *ui.Context, text string) {
	components.Text(c, text).FontSize(theme.Text2XS).TextColor(theme.TextMeta)
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
	// 常态底色和悬停色一起算、一次赋值：Background 是赋值不是叠加，
	// 先写 Accent / FillTertiary、再被 hoverSurface 覆盖，会让同一个按钮
	// 忙碌时实心、空闲时透明（两种长相）。
	restBg, hoverBg := theme.FillTertiary, theme.FillSecondary
	if primary {
		restBg, hoverBg = theme.Accent, theme.AccentHover
	}
	if a.SyncBusy && !stoppable {
		btn.Background(restBg)
		btn.Opacity(0.5).Cursor(ui.CursorDefault)
	} else {
		btn.Background(hoverSurfaceRest(btn, "sync-btn-bg", btn.Hovered(), restBg, hoverBg))
		btn.Cursor(ui.CursorPointer)
		if !primary {
			btn.Border(1, theme.Border)
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
		// 用挂钟，不用 c.Now()。ProfStarted 是在**回调里**用 time.Now() 存的，
		// 构建帧的时钟在 Tester 里跟它差几十年：left 恒被夹到 0，永远显示
		// 「停止剖析 · 0s」。倒计时是给人看的真实时间，两头都用挂钟才对。
		text = profileStopLabel(a.ProfStarted, time.Now())
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
				components.Icon(c, iconStop).FontSize(13).TextColor(col)
			case mine:
				// 跑着的时候图标转起来，兼作进度提示
				components.Icon(c, iconSync).FontSize(14).TextColor(col).Rotate(
					btn.Loop("sync-btn-spin", theme.SpinPeriod, ui.Linear) * 360)
			default:
				components.Icon(c, ico).FontSize(14).TextColor(col)
			}
			components.Text(c, text).FontSize(theme.TextXS).FontWeight(600).TextColor(col)
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
