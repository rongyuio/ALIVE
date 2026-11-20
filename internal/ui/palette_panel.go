package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 配色面板的尺寸与锚点。
//
// 面板挂在配色按钮下方（top: 100% + 10, right: 0），
// 原生这边面板画在 Shell 最上层（否则会被页面内容盖住），所以位置按
// 「按钮右边缘距窗口右边多远」直接算：padding 14 + 窗口按钮 118 + 间隔 8
// + 分隔线 1 + 间隔 8 ≈ 149。
const (
	palettePanelW = 352
)

// paletteButton 是工具组里的配色按钮：chromeH 见方，图标 + 右下角一枚当前强调色的小点。
func (a *App) paletteButton(c *ui.Context) {
	btn := ui.Box(c).Size(chromeH, chromeH).Radius(theme.RadiusFull).
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
			Shadow(0, 38, 72, -30, theme.ShadowModal).Shadow(0, 6, 16, 0, theme.ShadowContact).
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
	// 旧版落盘的 ID 在这里归一成新 ID；下一次保存写的才是新值。
	a.PaletteID = cur.ID
	if cur.Dark != want {
		a.PaletteID = theme.Counterpart(cur.ID)
	}
}

// palettePanel 是六套配色的选择面板。
//
// 布局：352 宽、两列、每格上方一条三色预览带，
// 下面写配色名和明暗；选中项用强调色描边 + 右上角一枚勾。
func (a *App) palettePanel(c *ui.Context) {
	dark := theme.Current().Dark

	ui.Column(c).Gap(10).Children(func() {
		// ---- 标题行：左边标题，右边夜 / 日 / 跟随系统
		ui.Row(c).Padding(2, 4, 8, 4).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "主题配色").FontSize(theme.TextSM).FontWeight(600).
				TextColor(theme.TextPrimary)
			ui.Box(c).Grow(1)
			a.themeModeButton(c)
		})

		// ---- 两列配色格：只列当前这一侧的三套，日夜由上面的按钮决定
		ui.Grid(c).Width(palettePanelW - 24).Columns(2).Gap(6).Children(func() {
			for _, p := range theme.Side(dark) {
				a.paletteItem(c, p)
			}
		})
	})
}

// themeModeButton 是标题行右侧的圆形按钮：一个按钮循环切换
// 日 → 夜 → 跟随系统，当前状态由图标表达。
//
// 三个状态各占一个按钮会挤掉标题、而且三种选择本来就是同一件事的三种取值，
// 圆形按钮 + 图标更像一个开关，不用读字。
func (a *App) themeModeButton(c *ui.Context) {
	icon, label := themeModeIcon(a.ThemeMode)
	btn := ui.Box(c).Size(26, 26).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).
		Label("配色模式 " + label).
		Tooltip("配色模式：" + label + "（点击切换）").
		Background(theme.FillQuaternary)
	if btn.Hovered() {
		btn.Background(theme.FillTertiary)
	}
	if btn.Clicked() {
		a.ThemeMode = themeModeNext(a.ThemeMode)
		// 立即按新选择换到同色系的另一半，面板里的格子跟着换。
		a.syncThemeMode(a.systemDark)
		a.markDirty()
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if btn.Hovered() {
			col = theme.TextPrimary
		}
		ui.Icon(c, icon).FontSize(15).TextColor(col)
	})
}

// themeModeNext 是按钮的切换顺序：日 → 夜 → 跟随系统 → 日。
func themeModeNext(mode string) string {
	switch mode {
	case themeModeLight:
		return themeModeDark
	case themeModeDark:
		return themeModeSystem
	default:
		return themeModeLight
	}
}

// themeModeIcon 返回某个模式对应的图标与文案。
func themeModeIcon(mode string) (*ui.SVG, string) {
	switch mode {
	case themeModeLight:
		return iconSun, "日"
	case themeModeDark:
		return iconMoon, "夜"
	default:
		return iconSystemTheme, "跟随系统"
	}
}

// paletteItem 是配色面板里的一格。
func (a *App) paletteItem(c *ui.Context, p *theme.Palette) {
	active := p.ID == a.PaletteID

	item := ui.Column(c).Width((palettePanelW-24-6)/2).Gap(6).Padding(7).
		Radius(theme.RadiusMD).Background(theme.FillQuaternary).
		Border(1, ui.Transparent).Cursor(ui.CursorPointer).Label("配色:" + p.Name)
	if active {
		item.Border(1, theme.AccentLine).Background(theme.FillTertiary)
	} else if item.Hovered() {
		item.BorderColor(theme.BorderStrong).Background(theme.FillTertiary)
	}
	if item.Clicked() {
		a.PaletteID = p.ID
		a.PaletteOpen = false
		a.markDirty()
	}

	item.Children(func() {
		// 三色预览带：地 / 文字 / 强调色，等分
		swatch := ui.Row(c).Height(30).Radius(7)
		swatch.ClipInset(0, 0, 0, 0, 7)
		swatch.Children(func() {
			for _, hex := range p.Swatch {
				ui.Box(c).Grow(1).Height(30).Background(ui.Hex(hex))
			}
		})

		ui.Column(c).Gap(1).Children(func() {
			// 只留配色名：深浅由上面的圆形按钮决定，说明文字在面板宽度里
			// 只会折成两行，压住下一格。
			ui.Text(c, p.Name).FontSize(theme.TextXS).FontWeight(600).
				SingleLine().TextColor(theme.TextPrimary)
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
