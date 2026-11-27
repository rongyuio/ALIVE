package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 弹幕设置面板的尺寸：
// 面板 232 宽、padding 14 16、圆角 16、玻璃底 rgba(17,20,28,.88)；
// 每行 8×10 padding、圆角 10、底色白 5%、描边白 8%。
const (
	danmuPanelW   float32 = 232
	danmuSliderW  float32 = 180 // 面板内容宽 200 − 行内 padding 20
	danmuSliderH  float32 = 14  // 滑杆热区高度＝拇指直径
	danmuThumbD   float32 = 14  // 拇指直径
	danmuRowPadH  float32 = 10
	danmuRowPadV  float32 = 8
	danmuRowGap   float32 = 6
	danmuRowRad   float32 = 10
	danmuLabelAlp float32 = 0.68 // rgba(255,255,255,.68)

	// 颜色 / 描边的色块就是胶囊本身：38×24、圆角 8，颜色铺到边上。
	// 不用 ui.ColorWell 的现成外观——它内部固定 36×20 的内芯，外面还带
	// 4px padding 和 1px 边，塞进 38×24 时内芯比内容盒还大，色块会被挤歪。
	danmuWellW       float32 = 38
	danmuWellH       float32 = 24
	danmuWellChecker float32 = 6
)

// danmuSettingsButton 是控制台里的弹幕设置入口。
//
// 只有点击才开：面板里有滑杆和取色器，指针路过就弹出来会打断正在拖动的
// 操作，也不像个能久留的面板。关闭交给 PopoverBase——点面板外面、按 Esc、
// 再点一次按钮都收得回去。清晰度 / 线路那两个是纯选项列表，仍然是 hover。
func (a *App) danmuSettingsButton(c *ui.Context) {
	// 面板关着时取色浮层也一起收掉：它挂在面板子树里，留着开关状态，
	// 下次打开面板会先莫名弹出一个取色器。
	if !a.DanmuSettingsOpen {
		a.DanmuColorOpen, a.DanmuStrokeOpen = false, false
	}
	// 和其它控制条按钮同一套：30×30 无底色，只变色
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label("弹幕设置")
	if btn.Clicked() {
		a.DanmuSettingsOpen = !a.DanmuSettingsOpen
	}
	col := playerIconColor(btn, a.DanmuSettingsOpen)
	btn.Children(func() { danmuTVIcon(c, iconDanmuBadgeSettings, col, col) })

	ui.PopoverBase(c, btn, &a.DanmuSettingsOpen, func(panel ui.Element) {
		panel.Width(danmuPanelW).Padding(14, 16, 14, 16).Radius(16).
			Border(1, theme.MediaHair).
			Shadow(0, 18, 40, 0, theme.MediaShadow).
			Margin(0, 0, 10, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		panel = mediaPanel(c, panel)
		panel.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				a.danmuColorRow(c, "颜色", &a.DanmuColor, &a.DanmuColorOpen)
				a.danmuColorRow(c, "描边", &a.DanmuStroke, &a.DanmuStrokeOpen)
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
	return ui.Text(c, text).FontSize(theme.TextXS).
		TextColor(theme.White.Alpha(danmuLabelAlp))
}

func (a *App) danmuColorRow(c *ui.Context, label string, color *ui.Color, open *bool) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		a.danmuColorWell(c, label, color, open)
	})
}

// danmuColorWell 是「颜色」「描边」右侧的色块：38×24 的胶囊铺满颜色，
// 点一下在下方开 ui.ColorPicker。
//
// 外观自己画，只有取色器还用 MyGo 的 ui.ColorPicker：ColorWell 的内芯
// 写死 36×20，再加 4px padding 和 1px 边，比这里 38×24 的容器还大，
// 内芯会被顶出容器，颜色也填不满。
func (a *App) danmuColorWell(c *ui.Context, label string, color *ui.Color, open *bool) {
	well := ui.Box(c).Size(danmuWellW, danmuWellH).Radius(theme.RadiusXS).
		Focusable().Cursor(ui.CursorPointer).
		Label(label).Role(ui.RoleColorWell)
	well.Draw(func(p *ui.Painter, r ui.Rect) {
		// 棋盘格垫底：描边色可能带 alpha，没有它看不出深浅。不透明的颜色
		// 会把格子全盖住，那就不画。
		if color.A < 255 {
			danmuWellCheckers(p, r)
		}
		p.Fill(r, *color, theme.RadiusXS)
		// Stroke 本来就画在矩形内侧，必须用同一个 r 和同一个半径：
		// 手动内缩再减半径会让两条弧线对不齐，圆角上多出一圈错位的
		// 描边（高 DPI 下看着是一排小点）。
		p.Stroke(r, danmuWellRing(*color), theme.RadiusXS, 1)
	})
	if well.Clicked() {
		*open = !*open
	}
	ui.PopoverBase(c, well, open, func(panel ui.Element) {
		panel.Padding(14).Radius(theme.RadiusXL).
			Background(theme.MediaPanel).
			Border(1, theme.MediaHair).
			Shadow(0, 18, 40, 0, theme.MediaShadow).
			Margin(8, 0, 0, 0)
		picker := ui.ColorPicker(c, color)
		if picker.Changed() {
			a.markDirty()
		}
	})
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

// danmuWellCheckers 是色块底下的灰白棋盘格。
func danmuWellCheckers(p *ui.Painter, r ui.Rect) {
	p.Fill(r, theme.White, theme.RadiusXS)
	p.Clip(r, theme.RadiusXS, func() {
		for y, row := r.Y, 0; y < r.Y+r.H; y, row = y+danmuWellChecker, row+1 {
			for x, col := r.X, 0; x < r.X+r.W; x, col = x+danmuWellChecker, col+1 {
				if (row+col)%2 == 0 {
					continue
				}
				p.Fill(ui.Rect{X: x, Y: y,
					W: min(danmuWellChecker, r.X+r.W-x),
					H: min(danmuWellChecker, r.Y+r.H-y)}, theme.Black.Alpha(0.12), 0)
			}
		}
	})
}

func (a *App) danmuSwitchRow(c *ui.Context, label string, on *bool) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		sw := ui.Box(c).Width(34).Height(18).Radius(theme.RadiusFull).
			Cursor(ui.CursorPointer).Label(label)
		if *on {
			sw.Background(theme.Accent)
		} else {
			sw.Background(theme.MediaTrackFill)
		}
		if sw.Clicked() {
			*on = !*on
			a.markDirty()
		}
		sw.Children(func() {
			thumb := ui.Box(c).Size(14, 14).Radius(7)
			target := float32(2)
			if *on {
				target = 18
			}
			x := thumb.AnimateTo("danmu-switch", target, theme.Toggle())
			thumb.Absolute().Top(2).Left(x)
			if *on {
				thumb.Background(theme.MediaSwitchThumbOn)
			} else {
				thumb.Background(theme.MediaText)
			}
		})
	})
}

// danmuSliderRow 是一行滑杆：标题 + 右侧当前值 + 4px 轨道（accent 进度 + 白色拇指）。
//
// 滑杆本身用 MyGo 的 ui.SliderBase——「没有外观的滑杆」（third_party/mygo/ui/base.go:182）。
// 拖动、方向键、Home/End、Step 吸附、以及无障碍树里的 role=slider 和
// aria-valuemin/valuemax/valuenow 全由它提供，外观仍然自己画。
//
// 这里原来是手搓的 4px Box + Label：屏幕上看着一模一样，读屏只会念「字体」，
// 念不出 20px，方向键按了也没反应——和音量条当年那个坑是同一个。
func (a *App) danmuSliderRow(c *ui.Context, label, value string, cur, lo, hi, step float64, set func(float64)) {
	row := ui.Column(c).Padding(danmuRowPadV, danmuRowPadH, danmuRowPadV, danmuRowPadH).
		Gap(danmuRowGap).Radius(danmuRowRad).
		Background(theme.MediaRow).
		Border(1, theme.MediaRowLine)
	row.Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			danmuLabel(c, label).Grow(1)
			ui.Text(c, value).FontSize(theme.TextXS).TextColor(theme.Accent)
		})

		// 局部副本：控件通过指针写回的写法见 views.md，值变了才回写模型。
		v := cur
		frac := float32(0)
		if hi > lo {
			frac = float32((v - lo) / (hi - lo))
		}
		track := ui.SliderBase(c, &v, lo, hi).Step(step).
			Width(danmuSliderW).Height(danmuSliderH).Label(label)
		track.Draw(func(p *ui.Painter, r ui.Rect) {
			mid := r.Y + r.H/2
			p.Fill(ui.Rect{X: r.X, Y: mid - 2, W: r.W, H: 4}, theme.MediaTrack, theme.RadiusFull)
			if frac > 0 {
				p.Fill(ui.Rect{X: r.X, Y: mid - 2, W: r.W * frac, H: 4}, theme.Accent, theme.RadiusFull)
			}
			p.Fill(ui.Rect{
				X: r.X + r.W*frac - danmuThumbD/2,
				Y: mid - danmuThumbD/2,
				W: danmuThumbD, H: danmuThumbD,
			}, theme.White, danmuThumbD/2)
		})
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
