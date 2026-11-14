package ui

import (
	"fmt"

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

// palettePanel 是六套配色的选择面板。
//
// 布局对齐 baseline：352 宽、两列、每格上方一条三色预览带，
// 下面写配色名和明暗；选中项用强调色描边 + 右上角一枚勾。
func (a *App) palettePanel(c *ui.Context) {
	name := theme.Current().Name
	if cur := theme.Find(a.PaletteID); cur != nil {
		name = cur.Name
	}

	ui.Column(c).Gap(10).Children(func() {
		// ---- 标题行
		ui.Row(c).Padding(2, 4, 8, 4).Children(func() {
			ui.Text(c, "主题配色").FontSize(theme.TextSM).FontWeight(600).
				TextColor(theme.TextPrimary)
			ui.Box(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%d 套 · 当前 %s", len(theme.Palettes), name)).
				FontSize(theme.Text2XS).TextColor(theme.TextTertiary).SingleLine()
		})

		// ---- 两列配色格
		ui.Grid(c).Width(palettePanelW - 24).Columns(2).Gap(6).Children(func() {
			for i := range theme.Palettes {
				a.paletteItem(c, &theme.Palettes[i])
			}
		})
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
			mode := "深色"
			if !p.Dark {
				mode = "浅色"
			}
			ui.Text(c, mode).FontSize(theme.Text2XS).TextColor(theme.TextTertiary)
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
