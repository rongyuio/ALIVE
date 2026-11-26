package ui

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// spinner 是一个 0.8s 匀速转一圈的圆环，四边不同色——顶边 accent-ink、
// 右边 accent 60%、其余两边 accent 18%，转起来就是一道拖着浅色尾巴的光弧。
//
// MyGo 自带的 Spinner 是十二根辐条那种（AppKit 风格），和这个不是一套，
// 所以这里自己画。Path 没有弧线原语，用每象限 6 段折线逼近整圆。
func spinner(c *ui.Context, size float32) ui.Element {
	return spinnerTint(c, size, theme.Accent)
}

// 一帧里所有 spinner 共用同一个转角。
//
// 封面还没到时一屏可能同时挂几十个占位，每个都跑一遍自己的动画时钟既浪费，
// 视觉上又是各转各的、相位全错开。这里让本帧第一个 spinner 挂一个 Loop 负责
// 请求下一帧，转角从视图时钟直接算——不再依赖「第一个元素」的身份。
var spinDriven bool

// spinPeriod 是一圈的时间，对应 CSS 的 `animation: spin .8s linear infinite`。
const spinPeriod = 800 * time.Millisecond

// resetSpinFrame 在每次构建视图的开头清掉驱动标记（Shell 里调）。
func resetSpinFrame() { spinDriven = false }

// spinnerTint 是指定底色的圆环，用于需要更安静的地方（比如封面占位）。
func spinnerTint(c *ui.Context, size float32, base ui.Color) ui.Element {
	el := ui.Box(c).Size(size, size).Shrink(0)
	// 0.8s linear：对应 CSS 的 `animation: spin .8s linear infinite`
	if !spinDriven {
		spinDriven = true
		// Loop 只负责「请求下一帧」；相位从视图时钟直接算。
		// 相位挂在第一个 spinner 元素的身份上时，那个元素一换（骨架→封面）
		// 所有共享相位的环会一起跳回 0°；用 c.Now() 就没有这个问题。
		el.Loop("spin", spinPeriod, ui.Linear)
	}
	phase := float32(c.Now().UnixNano()%int64(spinPeriod)) / float32(spinPeriod)
	stroke := max(2, float32(int(size/10+0.5))) // max(2, round(size/10))

	el.Draw(func(p *ui.Painter, r ui.Rect) {
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		rad := r.W/2 - stroke/2
		turn := phase * 360

		// 顺时针四个象限，配色对应 CSS 的四条 border-color（0° 在右侧，y 向下）
		segs := [4]struct {
			from, to float32
			col      ui.Color
		}{
			{0, 90, base.Alpha(0.6)},
			{90, 180, base.Alpha(0.18)},
			{180, 270, base.Alpha(0.18)},
			{270, 360, base},
		}
		const steps = 6
		for _, s := range segs {
			var path ui.Path
			for i := 0; i <= steps; i++ {
				a := float64((s.from + (s.to-s.from)*float32(i)/steps + turn) * math.Pi / 180)
				x := cx + rad*float32(math.Cos(a))
				y := cy + rad*float32(math.Sin(a))
				if i == 0 {
					path.MoveTo(x, y)
				} else {
					path.LineTo(x, y)
				}
			}
			p.StrokePath(&path, stroke, s.col)
		}
	})
	return el
}
