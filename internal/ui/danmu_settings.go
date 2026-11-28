package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// 弹幕设置面板的尺寸：
// 面板 232 宽、padding 14 16、圆角 16、玻璃底 rgba(17,20,28,.88)；
// 每行 8×10 padding、圆角 10、底色白 5%、描边白 8%。
const (
	danmuPanelW   float32 = 232
	danmuSliderW  float32 = 180 // 面板内容宽 200 − 行内 padding 20
	danmuRowPadH  float32 = 10
	danmuRowPadV  float32 = 8
	danmuRowGap   float32 = 6
	danmuRowRad   float32 = 10
	danmuLabelAlp float32 = 0.68 // rgba(255,255,255,.68)

	// 颜色 / 描边的色块就是胶囊本身：38×24、圆角 8，颜色铺到边上。
	// 不用 ui.ColorWell 的现成外观——它内部固定 36×20 的内芯，外面还带
	// 4px padding 和 1px 边，塞进 38×24 时内芯比内容盒还大，色块会被挤歪。
	danmuWellW float32 = 38
	danmuWellH float32 = 24
)

// danmuSettingsButton 是控制台里的弹幕设置入口。
//
// 只有点击才开：面板里有滑杆和取色器，指针路过就弹出来会打断正在拖动的
// 操作，也不像个能久留的面板。关闭交给 PopoverBase——点面板外面、按 Esc、
// 再点一次按钮都收得回去。清晰度 / 线路那两个是纯选项列表，仍然是 hover。
func (a *App) danmuSettingsButton(c *ui.Context) {
	// 和其它控制条按钮同一套：30×30 无底色，只变色
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label("弹幕设置")
	if btn.Clicked() {
		a.DanmuSettingsOpen = !a.DanmuSettingsOpen
	}
	col := playerIconColor(btn, a.DanmuSettingsOpen)
	btn.Children(func() { danmuTVIcon(c, iconDanmuBadgeSettings, col, col) })

	components.Popover(c, btn, &a.DanmuSettingsOpen, func(panel ui.Element) {
		panel.Width(danmuPanelW).Padding(14, 16, 14, 16).Radius(16).
			Border(1, theme.MediaHair).
			Shadow(0, 18, 40, 0, theme.MediaShadow).
			Margin(0, 0, 10, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		panel = mediaPanel(c, panel)
		panel.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				a.danmuColorRow(c, "颜色", &a.DanmuColor)
				a.danmuColorRow(c, "描边", &a.DanmuStroke)
				a.danmuSwitchRow(c, "彩色弹幕", &a.DanmuColorful)
				a.danmuSliderRow(c, "字体", fmt.Sprintf("%.0fpx", a.DanmuFont),
					float64(a.DanmuFont), 14, 30, 2, func(v float64) {
						a.DanmuFont = float32(v)
					})
				a.danmuSliderRow(c, "速度", formatDanmuDuration(a.DanmuDur),
					float64(a.DanmuDur.Milliseconds()), 5000, 20000, 500, func(v float64) {
						a.DanmuDur = millis(int(v))
					})
				a.danmuSliderRow(c, "显示区域", formatDanmuArea(a.DanmuArea),
					float64(a.DanmuArea), 0.25, 1, 0.25, func(v float64) {
						a.DanmuArea = float32(v)
					})
				a.danmuSliderRow(c, "透明度", fmt.Sprintf("%.0f%%", a.DanmuOpacity*100),
					float64(a.DanmuOpacity), 0.2, 1, 0.05, func(v float64) {
						a.DanmuOpacity = float32(v)
					})
			})
		})
	})
}

// danmuRow 是一行的公共外观：8×10 padding、圆角 10、白 5% 底、白 8% 描边。
func danmuRow(c *ui.Context) ui.Element {
	return ui.Row(c).Padding(danmuRowPadV, danmuRowPadH, danmuRowPadV, danmuRowPadH).
		Gap(12).Radius(danmuRowRad).AlignItems(ui.Center).
		Background(theme.MediaRow).
		Border(1, theme.MediaRowLine)
}

func danmuLabel(c *ui.Context, text string) ui.Element {
	return components.Text(c, text).FontSize(theme.TextXS).
		TextColor(theme.White.Alpha(danmuLabelAlp))
}

func (a *App) danmuColorRow(c *ui.Context, label string, color *ui.Color) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		a.danmuColorWell(c, label, color)
	})
}

// danmuColorWell 是「颜色」「描边」右侧的色块：38×24 的胶囊铺满颜色，
// 点一下弹 Onevra 的取色面板。
//
// 尺寸和圆角仍然按这里的规格给：components.ColorPicker 的默认是
// t.Space(12)×t.Space(8) = 48×32、圆角 t.Radius/2 = 6（color.go:331），
// 文档说 Size/Border/Radius/Padding 都可以在返回的元素上定制，所以
// 38×24 与 RadiusXS 是盖得回去的。
//
// 盖不回去的有两处，都记在这里：
//   - 内芯那个 Box 的圆角写死 t.Radius/2（color.go:339），外壳 8 内芯 6；
//   - 棋盘格是「白 + RGB(205,205,205)」（color.go:311-318），原来是
//     「白 + 黑 12%」。
//
// 描边还是用 danmuWellRing 按色块亮度算一次传进去：组件只在构造时收一个
// Border，不给每帧重算的出口。
//
// Padding(0) 是必须的：ColorPicker 自带 Padding(t.Space(1)) = 4（color.go:331），
// 那是给 48×32 的默认尺寸留的气口。缩到 38×24 之后它还留着，四周各吃掉 4，
// 色块只剩 30×16，看着就是个没填满的方块。
func (a *App) danmuColorWell(c *ui.Context, label string, color *ui.Color) {
	well := components.ColorPicker(c, color)
	well.Size(danmuWellW, danmuWellH).Padding(0).Radius(theme.RadiusXS).
		Border(1, danmuWellRing(*color)).Label(label)
	if well.Changed() {
		a.markDirty()
	}
}

// danmuWellRing 返回色块那圈 1px 描边。
//
// 不能固定用白色：白色色块上那圈白边根本看不见，只有黑色色块显得有框。
// 按色块实际亮度挑——亮色压暗环、暗色提亮环；半透明色先和底下的棋盘格
// 合成再算，不然 20% 的白会被当成暗色。
func danmuWellRing(c ui.Color) ui.Color {
	lum := 0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)
	lum = lum*float32(c.A)/255 + 255*(1-float32(c.A)/255)
	if lum > 140 {
		return theme.Black.Alpha(0.30)
	}
	return theme.MediaTrackFill
}

// danmuSwitchRow 是一行开关。
//
// 开关交给 components.Switch。尺寸拿不到 34×18 —— 它锚在
// t.Space(10)/t.Space(5)，也就是 40×20（compact 是 30×16），而这两个值只在
// switch.go:58-69 内部算轨道与拇指行程，SwitchOptions 只有 HideLabel 和
// Compact 两个开关，没有尺寸出口。拇指也从 14 圆点变成 1.3 倍高的胶囊。
//
// 换来的是真的开关语义：ui.SwitchBase 带 Focusable 和 RoleSwitch，
// 键盘 Space/Enter 能切；原来这颗 Box 既进不了 Tab，读屏也只当它是分组。
func (a *App) danmuSwitchRow(c *ui.Context, label string, on *bool) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		// Switch 在构造期就把值写回 *on，所以比前后即可 —— 它返回的外层
		// Row 拿不到 Changed（那个标记挂在内层的 SwitchBase 上）。
		before := *on
		components.Switch(c, on, label, components.SwitchOptions{HideLabel: true})
		if *on != before {
			a.markDirty()
		}
	})
}

// danmuSliderRow 是一行滑杆：标题 + 右侧当前值 + 轨道。
//
// 行为仍然来自 ui.SliderBase（拖动、方向键、Home/End、Step 吸附、
// role=slider 与 aria-valuenow），components.Slider 的单值路径就是
// ui.SliderBase(c,value,lo,hi).Step(o.Step)（slider.go:92），没有另起一套。
//
// 外观换成 Onevra 的：宽 180 照旧，但轨道画在 r.Y+t.Space(2.5) = +10
// （slider.go:127），拇指是 20×16 的胶囊而不是 14 圆点，最小宽 80
// （slider.go:98）。原来那条「4px 轨道 + 白色圆拇指」的自绘没有了。
func (a *App) danmuSliderRow(c *ui.Context, label, value string, cur, lo, hi, step float64, set func(float64)) {
	row := ui.Column(c).Padding(danmuRowPadV, danmuRowPadH, danmuRowPadV, danmuRowPadH).
		Gap(danmuRowGap).Radius(danmuRowRad).
		Background(theme.MediaRow).
		Border(1, theme.MediaRowLine)
	row.Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			danmuLabel(c, label).Grow(1)
			components.Text(c, value).FontSize(theme.TextXS).TextColor(theme.Accent)
		})

		// 局部副本：控件通过指针写回的写法见 views.md，值变了才回写模型。
		v := cur
		track := components.Slider(c, &v, lo, hi, components.SliderOptions{Step: step})
		track.Width(danmuSliderW).Label(label)
		if track.Changed() {
			set(v)
			a.markDirty()
		}
	})
}

// formatDanmuDuration / formatDanmuArea 是滑杆右侧那串档位文案：
// 时长分 5 档、显示区域按四等分，滑杆本身存的是原始值（毫秒 / 比例）。
func formatDanmuDuration(d time.Duration) string {
	switch ms := d.Milliseconds(); {
	case ms <= 4500:
		return "极快"
	case ms <= 7500:
		return "很快"
	case ms <= 10000:
		return "标准"
	case ms <= 14000:
		return "稍慢"
	default:
		return "慢速"
	}
}

func formatDanmuArea(area float32) string {
	switch {
	case area <= 0.25:
		return "上 1/4"
	case area <= 0.5:
		return "上 1/2"
	case area <= 0.75:
		return "上 3/4"
	default:
		return "全屏"
	}
}

func millis(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }
