package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 光环的几何：baseline 的容器 1px 描边，::before inset:-1px + padding:2px，
// 环压在描边上并向外扩 1px。本体让出环的内侧 1px。
const (
	islandRingInset float32 = -1
	islandRingPad   float32 = 2
	islandBodyInset float32 = 1
)

// Island 是顶部居中的「灵动岛」。
//
// 它只报状态，不承载面板：一双会眨的眼睛（9×11 的青柠色椭圆、间隔 11）。
// hover 不换内容、也不显示数字——岛只做呼吸，不抢注意力。
//
// 全部关注面板的入口在顶栏的星标格子上（见 follows_panel.go），
// 点岛不再放大成面板。
func (a *App) Island(c *ui.Context) {
	winW, _ := c.Size()

	island := ui.Box(c).Key("island").Label("灵动岛").Absolute().Top(theme.IslandGap).
		Width(theme.IslandW).Height(theme.IslandH).Radius(theme.RadiusFull).
		Left((winW-theme.IslandW)/2).
		// baseline 的 .sidebar 有 1px 描边，光环正压在这条描边上
		Border(1, theme.Border)

	// ---------- 投影：baseline 深浅两套（.sidebar 与 [data-theme="light"] .sidebar）。
	// 浅色主题用低透明度蓝黑，套深色的纯黑阴影会在白底上压出一圈脏灰。
	//
	// 收起态是很散的一层：0 24px 56px -28px，几乎看不出投影边界。
	if theme.Current().Dark {
		island.Shadow(0, 24, 56, -28, ui.RGBA(0, 0, 0, 0.90)).
			Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.36)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.05))
	} else {
		island.Shadow(0, 22, 52, -28, ui.RGBA(15, 23, 42, 0.34)).
			Shadow(0, 4, 12, 0, ui.RGBA(15, 23, 42, 0.10)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.80))
	}

	island.Children(func() {
		a.islandRing(c, theme.RadiusFull)

		// ---------- 本体：环内侧缩进 1px，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(theme.RadiusFull - islandBodyInset).
			Background(theme.BgSecondary)
		body.ClipInset(0, 0, 0, 0, theme.RadiusFull-islandBodyInset)

		body.Children(func() {
			a.islandPill(c)
		})
	})
}

// islandPill 是收起态：只有一双眨着的眼睛，hover 不改变任何内容。
func (a *App) islandPill(c *ui.Context) {
	ui.Row(c).Fill().Center().Gap(11).Children(func() {
		// baseline 的 eye-blink：4.6s ease-in-out 循环，94% 时 scaleY 0.12。
		// transform-origin 默认居中；reduced motion 下框架直接停在最终值 1。
		eye := func() {
			e := ui.Box(c).Width(9).Height(11).Radius(theme.RadiusFull).
				Background(theme.AccentInk)
			// 用 Loop 而不是 Keyframes：这版 MyGo 的 RepeatForever 播完
			// 一次就停，眼睛只会眨一下。reduced motion 下不眨眼。
			if !c.Preferences().ReduceMotion {
				sy := blinkScale(e.Loop("blink", 4600*time.Millisecond, ui.Linear))
				e.Scale(1, sy)
			}
		}
		eye()
		eye()
	})
}

// islandRing 画那圈贴着边缘跑的 conic 流光。
//
// baseline（Sidebar.module.css 的 .sidebar::before）：
//   - conic 渐变，底子是 30% 的主题强调色，105° 处一道全亮的头
//   - 4.5s 线性绕行一圈
//   - 只留 padding 那一圈：content-box 取反的遮罩
//
// 所以环的颜色必须跟着配色走（--accent-ink），不能写死青柠；
// 旋转用 Loop 持续请求帧，动的是光，不是盒子。
func (a *App) islandRing(c *ui.Context, r float32) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	period := 4500 * time.Millisecond
	alpha := float32(1)

	dim := theme.AccentInk.Mix(ui.Transparent, 0.70) // 30% 的强调色
	hot := theme.AccentInk

	ring := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(r - islandRingInset).Padding(islandRingPad)
	angle := ring.Loop("island-ring", period, ui.Linear) * 360
	ring.ConicGradient(ui.ConicGradient{
		Angle: angle,
		Oklab: true,
		Stops: []ui.GradientStop{
			{Color: dim, At: 0},
			{Color: dim, At: 40.0 / 360},
			{Color: hot, At: 105.0 / 360},
			{Color: dim, At: 165.0 / 360},
			{Color: dim, At: 1},
		},
	}).Masks(
		ui.Mask{Image: solid, Box: ui.MaskContentBox, Composite: ui.MaskExclude},
		ui.Mask{Image: solid},
	).Opacity(alpha)
}

// livePulse 把 0–1 的周期相位换成脉冲环的 scale / opacity：
// 0% 时 0.75 / 0.9，70% 时扩到 1.85 并完全透明，之后保持到周期结束。
// 缓动与 baseline 的 --ease-out 同一档。
func livePulse(p float32) (scale, alpha float32) {
	if p >= 0.7 {
		return 1.85, 0
	}
	e := easeOutUnit(p / 0.7)
	return 0.75 + e*1.10, 0.9 * (1 - e)
}

// blinkScale 把 4.6s 的周期相位换成眼睛的 scaleY：
// 90% 之前睁着，90%–94% 闭到 0.12，94%–100% 睁开。
func blinkScale(p float32) float32 {
	switch {
	case p < 0.90:
		return 1
	case p < 0.94:
		return 1 - (p-0.90)/0.04*0.88
	default:
		return 0.12 + (p-0.94)/0.06*0.88
	}
}

// easeOutUnit 是 baseline --ease-out 在 0–1 区间上的求值。
func easeOutUnit(t float32) float32 {
	return ui.CubicBezier(0.22, 1, 0.36, 1)(t)
}
