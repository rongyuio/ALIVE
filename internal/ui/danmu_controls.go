package ui

import (
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// 弹幕控件的电视图标：和控制条其它图标同一个 24px。
// 开 / 关 / 设置 / 屏蔽四种状态共用同一台挖了右下角的电视，徽标叠在同一张
// 24 网格上，所以四者视觉重量一致。
//
// 24 和平常一样：机身在 24 网格里画成 18×18（描边 2），墨迹正好 20×20，
// 和 lucide 那几颗的墨迹同高；徽标收在机身墨迹里面，不会把整颗撑高。
const danmuTVSize = playerIconSize

// danmuTVIcon 把电视机身和右下角徽标叠在一起。
//
// 两张 SVG 必须同尺寸、同原点：机身在右下角用 mask 挖了一个圆口，徽标
// 正好压在这个口子上，读起来才是「徽标盖在电视机上」而不是两个并排的图形。
// 徽标单独取色，开启态的勾才能是主题色。
func danmuTVIcon(c *ui.Context, badge *ui.SVG, body, badgeColor ui.Color) ui.Element {
	box := ui.Box(c).Size(danmuTVSize, danmuTVSize)
	box.Children(func() {
		components.Icon(c, iconDanmuTV).Size(danmuTVSize, danmuTVSize).TextColor(body)
		if badge != nil {
			components.Icon(c, badge).Size(danmuTVSize, danmuTVSize).
				TextColor(badgeColor).
				Absolute().Top(0).Left(0)
		}
	})
	return box
}

// danmuToggleButton 是控制台里的弹幕开关。
//
// 整颗图标不再随开关染色：电视保持常态色，状态只由右下角的勾 / 禁止符
// 表达，勾固定用主题色；hover 时电视和禁止符提亮到纯白。
func (a *App) danmuToggleButton(c *ui.Context) {
	label := "开启弹幕"
	if a.DanmuOn {
		label = "关闭弹幕"
	}
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label(label)
	if btn.Clicked() {
		a.DanmuOn = !a.DanmuOn
		a.markDirty()
	}
	body := playerIconColor(btn, false)
	badge, badgeColor := iconDanmuBadgeCheck, theme.Accent
	if !a.DanmuOn {
		badge, badgeColor = iconDanmuBadgeOff, body
	}
	btn.Children(func() { danmuTVIcon(c, badge, body, badgeColor) })
}
