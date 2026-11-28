package ui

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// shortcuts 是播放页的快捷键说明。
var shortcuts = []struct{ key, action string }{
	{"Space / K", "播放 / 暂停"},
	{"F", "进入 / 退出全屏"},
	{"M", "静音 / 取消静音"},
	{"Esc", "退出全屏；再按收进岛"},
	{"?", "打开 / 关闭本说明"},
}

// shortcutsOverlay 是 ? 唤出的快捷键说明：居中弹层，点空白或 Esc 关掉。
//
// 层本身交给 components.Dialog：Modal 让窗口级快捷键等待（否则遮罩盖住整屏
// 时 Space / K / F / M 仍会穿到 Shell 的 shellKeys，对背后正在播的那一路
// 生效）、点面板不关、焦点陷阱、Esc 消费，它全自带（dialog.go:38-96）。
//
// 遮罩浓度是 Onevra 写死的 rgba(0,0,0,.18)（dialog.go:73），比原来那层
// MediaBackdropScrim 淡；这是换 Dialog 的代价。面板面用 orPanel 换回
// BgElevated —— Onevra 默认铺的是跟着配色的玻璃，而这里的材质会盖住
// Background（见 onevra.go 顶部）。
func (a *App) shortcutsOverlay(c *ui.Context) {
	components.Dialog(c, &a.ShortcutsOpen, components.DialogOptions{Label: "快捷键"},
		func(panel ui.Element) {
			panel = orPanel(panel, theme.BgElevated)
			panel.Width(420).Padding(20, 20, 18, 20).
				Radius(theme.RadiusXL).Border(1, theme.Border).
				Shadow(0, 24, 60, -24, theme.ShadowPanel).Shadow(0, 4, 12, 0, theme.ShadowContact)
			panel.Children(func() {
				components.Text(c, "快捷键").FontSize(theme.TextLG).FontWeight(600).
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
//
// 键帽和分隔线仍是自绘：Onevra 没有 kbd 组件，也没有 Divider
// （它有意把 Divider 留给你用 Box 拼）。
func (a *App) shortcutRow(c *ui.Context, key, action string, last bool) {
	ui.Column(c).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Padding(7, 2, 7, 2).Gap(16).Children(func() {
			ui.Box(c).Padding(3, 8, 3, 8).Radius(theme.RadiusXS).
				Background(theme.FillQuaternary).Children(func() {
				components.Text(c, key).FontSize(theme.TextXS).FontWeight(600).
					TextColor(theme.TextSecondary).FontFeatures("tnum")
			})
			components.Text(c, action).FontSize(theme.TextSM).TextColor(theme.TextPrimary).Grow(1)
		})
		if !last {
			ui.Box(c).Height(1).Background(theme.Separator)
		}
	})
}
