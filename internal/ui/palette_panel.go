package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 配色面板的尺寸与锚点。
//
// baseline 里面板是按钮 wrapper 的绝对定位子元素（top: 100% + 10, right: 0），
// 原生这边面板画在 Shell 最上层（否则会被页面内容盖住），所以位置按
// 「按钮右边缘距窗口右边多远」直接算：padding 14 + 窗口按钮 118 + 间隔 8
// + 分隔线 1 + 间隔 8 ≈ 149。
const (
	palettePanelW = 352
)

// paletteButton 是工具组里的配色按钮：38×38，图标 + 右下角一枚当前强调色的小点。
func (a *App) paletteButton(c *ui.Context) {
	btn := ui.Box(c).Size(38, 38).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label("配色面板")
	hovered := btn.Hovered()
	if a.PaletteOpen || hovered {
		btn.Background(theme.FillTertiary)
	}
	if btn.Clicked() {
		a.PaletteOpen = !a.PaletteOpen
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if a.PaletteOpen || hovered {
			col = theme.TextPrimary
		}
		ui.Icon(c, iconPalette).FontSize(18).TextColor(col)

		// 小色点：用 1.5px 的地面色描边充当 box-shadow 的那圈隔离
		ui.Box(c).Absolute().Right(9).Bottom(8).Size(6, 6).Radius(theme.RadiusFull).
			Background(theme.Accent).Border(1.5, theme.BgPrimary)
	})

	ui.PopoverBase(c, btn, &a.PaletteOpen, func(panel ui.Element) {
		panel.Width(palettePanelW).Padding(12).Radius(theme.RadiusXL).
			Background(theme.BgElevated).Border(1, theme.Border).
			Label("配色浮层").
			// --shadow-lg: 0 38px 72px -30px rgba(0,0,0,.85)
			Shadow(0, 38, 72, -30, theme.ShadowLg).
			Margin(10, 0, 0, 0).
			AttachTo(btn, ui.AnchorBottomRight, ui.AnchorTopRight)
		a.palettePanel(c)
	})
}

// 夜 / 日 / 跟随系统的三种选择。取值会落盘，改名要连带迁移。
const (
	themeModeSystem = "system"
	themeModeDark   = "dark"
	themeModeLight  = "light"
)

// themeWantsDark 报告当前选择希望用深色还是浅色配色。
func (a *App) themeWantsDark(systemDark bool) bool {
	switch a.ThemeMode {
	case themeModeDark:
		return true
	case themeModeLight:
		return false
	default:
		return systemDark
	}
}

// syncThemeMode 让当前配色跟上「夜 / 日 / 跟随系统」的选择。
//
// 同色系日夜成对存在（青柠 · 夜 / 青柠 · 日），所以只切换同一色的另一半，
// 不把用户选的色系换掉。跟随系统时每帧按系统外观校正，系统切主题不用重启。
func (a *App) syncThemeMode(systemDark bool) {
	a.systemDark = systemDark
	want := a.themeWantsDark(systemDark)
	cur := theme.Find(a.PaletteID)
	if cur == nil {
		if want {
			a.PaletteID = theme.Palettes[0].ID
		} else {
			a.PaletteID = theme.Counterpart(theme.Palettes[0].ID)
		}
		return
	}
	if cur.Dark != want {
		a.PaletteID = theme.Counterpart(cur.ID)
	}
}

// palettePanel 是六套配色的选择面板。
//
// 布局对齐 baseline：352 宽、两列、每格上方一条三色预览带，
// 下面写配色名和明暗；选中项用强调色描边 + 右上角一枚勾。
func (a *App) palettePanel(c *ui.Context) {
	dark := theme.Current().Dark

	ui.Column(c).Gap(10).Children(func() {
		// ---- 标题行：左边标题，右边夜 / 日 / 跟随系统
		ui.Row(c).Padding(2, 4, 8, 4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "主题配色").FontSize(theme.TextSM).FontWeight(600).
				TextColor(theme.TextPrimary)
			ui.Box(c).Grow(1)
			a.themeModeSwitch(c)
		})

		// ---- 两列配色格：只列当前这一侧的三套，日夜由上面的按钮决定
		ui.Grid(c).Width(palettePanelW - 24).Columns(2).Gap(6).Children(func() {
			for _, p := range theme.Side(dark) {
				a.paletteItem(c, p)
			}
		})
	})
}

// themeModeSwitch 是标题行右侧的三段切换：日 / 夜 / 跟随系统。
func (a *App) themeModeSwitch(c *ui.Context) {
	ui.Row(c).Gap(2).Padding(2).Radius(theme.RadiusFull).
		Background(theme.FillQuaternary).Children(func() {
		for _, opt := range []struct{ id, label string }{
			{themeModeLight, "日"},
			{themeModeDark, "夜"},
			{themeModeSystem, "跟随系统"},
		} {
			active := a.ThemeMode == opt.id || (a.ThemeMode == "" && opt.id == themeModeSystem)
			btn := ui.Box(c).Height(22).Padding(0, 10, 0, 10).Radius(theme.RadiusFull).
				Center().Cursor(ui.CursorPointer).Label("配色模式 " + opt.label)
			switch {
			case active:
				btn.Background(theme.Accent)
			case btn.Hovered():
				btn.Background(theme.FillTertiary)
			}
			if btn.Clicked() {
				a.ThemeMode = opt.id
				// 立即按新选择换到同色系的另一半，面板里的格子跟着换。
				// 「跟随系统」用最近一帧读到的外观，不用等下一帧。
				a.syncThemeMode(a.systemDark)
				a.markDirty()
			}
			btn.Children(func() {
				col := theme.TextSecondary
				if active {
					col = theme.AccentText
				}
				ui.Text(c, opt.label).FontSize(theme.Text2XS).FontWeight(600).
					TextColor(col).SingleLine()
			})
		}
	})
}

// paletteItem 是配色面板里的一格。
func (a *App) paletteItem(c *ui.Context, p *theme.Palette) {
	active := p.ID == a.PaletteID

	item := ui.Column(c).Width((palettePanelW-24-6)/2).Gap(6).Padding(7).
		Radius(theme.RadiusMD).Background(theme.FillQuaternary).
		Border(1, ui.Transparent).Cursor(ui.CursorPointer)
	if active {
		item.Border(1, theme.Accent).Background(theme.FillTertiary)
	} else if item.Hovered() {
		item.BorderColor(theme.BorderStrong).Background(theme.FillTertiary)
	}
	if item.Clicked() {
		a.PaletteID = p.ID
		a.PaletteOpen = false
		a.markDirty()
	}

	item.Children(func() {
		// 三色预览带：背景 / 表面 / 强调色，等分
		swatch := ui.Row(c).Height(30).Radius(7)
		swatch.ClipInset(0, 0, 0, 0, 7)
		swatch.Children(func() {
			for _, hex := range p.Swatch {
				ui.Box(c).Grow(1).Height(30).Background(ui.Hex(hex))
			}
		})

		ui.Column(c).Gap(1).Children(func() {
			ui.Text(c, p.Name).FontSize(theme.TextXS).FontWeight(600).
				SingleLine().TextColor(theme.TextPrimary)
			// 深浅由上面的切换决定，这里不再重复一遍「深色 / 浅色」。
			ui.Text(c, p.Description).FontSize(theme.Text2XS).TextColor(theme.TextTertiary)
		})

		if active {
			badge := ui.Box(c).Absolute().Top(11).Right(11).Size(18, 18).Radius(9).
				Background(theme.Accent).Center()
			badge.Children(func() {
				ui.Icon(c, iconCheck).FontSize(10).TextColor(theme.AccentText)
			})
		}
	})
}
