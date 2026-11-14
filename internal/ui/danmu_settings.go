package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 弹幕设置面板的尺寸取自 baseline 的 DanmuSettingsControl.module.css：
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
)

// danmuSettingsButton 是控制台里的弹幕设置入口。
//
// 和清晰度 / 线路两个下拉一样是 hover 打开、离开 220ms 关
// （baseline 的 DanmuSettingsControl 触发条也是 onMouseEnter 开）。
func (a *App) danmuSettingsButton(c *ui.Context) {
	// 和其它控制条按钮同一套：30×30 无底色，只变色
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label("弹幕设置")
	if btn.Clicked() {
		a.DanmuSettingsHover.clicked(&a.DanmuSettingsOpen)
	}
	a.DanmuSettingsHover.hover(c, btn.Hovered(), &a.DanmuSettingsOpen)
	btn.Children(func() {
		col := playerIcon
		switch {
		case a.DanmuSettingsOpen:
			col = theme.Accent
		case btn.Hovered():
			col = playerIconOn
		}
		ui.Icon(c, iconSettings2).FontSize(16).TextColor(col)
	})

	ui.PopoverBase(c, btn, &a.DanmuSettingsOpen, func(panel ui.Element) {
		panel.Width(danmuPanelW).Padding(14, 16, 14, 16).Radius(16).
			Background(ui.RGBA(17, 20, 28, 0.88)).
			Border(1, ui.RGBA(255, 255, 255, 0.12)).
			Shadow(0, 18, 40, 0, ui.RGBA(0, 0, 0, 0.4)).
			Margin(0, 0, 10, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		// 指针移进面板时取消关门计时
		a.DanmuSettingsHover.hover(c, panel.Hovered(), &a.DanmuSettingsOpen)
		panel.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				a.danmuColorRow(c, "颜色", &a.DanmuColor)
				a.danmuColorRow(c, "描边", &a.DanmuStroke)
				a.danmuSwitchRow(c, "彩色弹幕", &a.DanmuColorful)
				a.danmuSliderRow(c, "字体", fmt.Sprintf("%.0fpx", a.DanmuFont),
					(a.DanmuFont-14)/16, func(f float32) {
						a.DanmuFont = 14 + float32(math.Round(float64(f*8)))*2
					})
				a.danmuSliderRow(c, "速度", formatDanmuDuration(a.DanmuDur),
					float32(a.DanmuDur.Milliseconds()-5000)/15000, func(f float32) {
						a.DanmuDur = millis(5000 + int(math.Round(float64(f*30)))*500)
					})
				a.danmuSliderRow(c, "显示区域", formatDanmuArea(a.DanmuArea),
					(a.DanmuArea-0.25)/0.75, func(f float32) {
						a.DanmuArea = 0.25 + float32(math.Round(float64(f*3)))*0.25
					})
				a.danmuSliderRow(c, "透明度", fmt.Sprintf("%.0f%%", a.DanmuOpacity*100),
					(a.DanmuOpacity-0.2)/0.8, func(f float32) {
						a.DanmuOpacity = 0.2 + float32(math.Round(float64(f*16)))*0.05
					})
			})
		})
	})
}

// danmuRow 是一行的公共外观：8×10 padding、圆角 10、白 5% 底、白 8% 描边。
func danmuRow(c *ui.Context) ui.Element {
	return ui.Row(c).Padding(danmuRowPadV, danmuRowPadH, danmuRowPadV, danmuRowPadH).
		Gap(12).Radius(danmuRowRad).AlignItems(ui.Center).
		Background(ui.RGBA(255, 255, 255, 0.05)).
		Border(1, ui.RGBA(255, 255, 255, 0.08))
}

func danmuLabel(c *ui.Context, text string) ui.Element {
	return ui.Text(c, text).FontSize(theme.TextXS).
		TextColor(ui.RGBA(255, 255, 255, danmuLabelAlp))
}

func (a *App) danmuColorRow(c *ui.Context, label string, color *ui.Color) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		well := ui.ColorWell(c, color).Width(38).Height(24).Radius(theme.RadiusXS)
		if well.Changed() {
			a.markDirty()
		}
	})
}

func (a *App) danmuSwitchRow(c *ui.Context, label string, on *bool) {
	row := danmuRow(c)
	row.Children(func() {
		danmuLabel(c, label).Grow(1)
		sw := ui.Box(c).Width(34).Height(18).Radius(theme.RadiusFull).
			Cursor(ui.CursorPointer)
		if *on {
			sw.Background(theme.Accent)
		} else {
			sw.Background(ui.RGBA(255, 255, 255, 0.22))
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
			x := thumb.AnimateTo("danmu-switch", target, theme.EaseInOut(220*time.Millisecond))
			thumb.Absolute().Top(2).Left(x)
			if *on {
				thumb.Background(ui.Hex("#1a1a1a"))
			} else {
				thumb.Background(ui.RGBA(255, 255, 255, 0.92))
			}
		})
	})
}

// danmuSliderRow 是一行滑杆：标题 + 右侧当前值 + 4px 轨道（accent 进度 + 白色拇指）。
func (a *App) danmuSliderRow(c *ui.Context, label, value string, frac float32, set func(float32)) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}

	row := ui.Column(c).Padding(danmuRowPadV, danmuRowPadH, danmuRowPadV, danmuRowPadH).
		Gap(danmuRowGap).Radius(danmuRowRad).
		Background(ui.RGBA(255, 255, 255, 0.05)).
		Border(1, ui.RGBA(255, 255, 255, 0.08))
	row.Children(func() {
		ui.Row(c).FillWidth().Children(func() {
			danmuLabel(c, label).Grow(1)
			ui.Text(c, value).FontSize(theme.TextXS).TextColor(theme.Accent)
		})

		track := ui.Box(c).Width(danmuSliderW).Height(4).Radius(theme.RadiusFull).
			Background(ui.RGBA(255, 255, 255, 0.15)).Cursor(ui.CursorPointer)
		if x, _, ok := track.PointerPosition(); ok && (track.Pressed() || track.Dragging()) {
			f := x / danmuSliderW
			if f < 0 {
				f = 0
			}
			if f > 1 {
				f = 1
			}
			set(f)
			a.markDirty()
		}
		track.Children(func() {
			ui.Box(c).Absolute().Top(0).Left(0).Height(4).
				Width(frac * danmuSliderW).Radius(theme.RadiusFull).Background(theme.Accent)
			ui.Box(c).Absolute().Top(-5).Left(frac*danmuSliderW-7).
				Size(14, 14).Radius(7).Background(ui.RGB(255, 255, 255))
		})
	})
}

// formatDanmuDuration / formatDanmuArea 逐字取自 baseline 的 danmuSettings.ts。
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
