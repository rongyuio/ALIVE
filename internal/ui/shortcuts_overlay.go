package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// shortcuts 是播放页的快捷键说明。
var shortcuts = []struct{ key, action string }{
	{"Space / K", "播放 / 暂停"},
	{"F", "进入 / 退出全屏"},
	{"M", "静音 / 取消静音"},
	{"Esc", "退出全屏，或返回上一页"},
	{"?", "打开 / 关闭本说明"},
}

// shortcutsOverlay 是 ? 唤出的快捷键说明：居中弹层，点空白或 Esc 关掉。
func (a *App) shortcutsOverlay(c *ui.Context) {
	bg := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(ui.RGBA(0, 0, 0, 0.55)).Center().Label("关闭快捷键说明")
	if bg.Clicked() {
		a.ShortcutsOpen = false
	}
	bg.Children(func() {
		panel := ui.Column(c).Width(420).Padding(20, 20, 18, 20).
			Radius(theme.RadiusXL).Background(theme.BgElevated).
			Border(1, theme.Border).
			Shadow(0, 24, 60, -24, theme.ShadowPopover)
		// 点面板本身不该关掉它
		if panel.Clicked() {
			a.ShortcutsOpen = true
		}
		panel.Children(func() {
			ui.Text(c, "快捷键").FontSize(theme.TextLG).FontWeight(600).
				TextColor(theme.TextPrimary)
			ui.Column(c).Gap(6).Padding(0, 0, 0, 0).Children(func() {
				for i, s := range shortcuts {
					a.shortcutRow(c, s.key, s.action, i == len(shortcuts)-1)
				}
			})
		})
	})
}

// shortcutRow 是一行「按键胶囊 + 说明」，最后一行不带分隔线。
func (a *App) shortcutRow(c *ui.Context, key, action string, last bool) {
	ui.Column(c).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(7, 2, 7, 2).Gap(16).Children(func() {
			ui.Box(c).Padding(3, 8, 3, 8).Radius(theme.RadiusXS).
				Background(theme.FillQuaternary).Children(func() {
				ui.Text(c, key).FontSize(theme.TextXS).FontWeight(600).
					TextColor(theme.TextSecondary).FontFeatures("tnum")
			})
			ui.Text(c, action).FontSize(theme.TextSM).TextColor(theme.TextPrimary).Grow(1)
		})
		if !last {
			ui.Box(c).Height(1).Background(theme.Separator)
		}
	})
}
