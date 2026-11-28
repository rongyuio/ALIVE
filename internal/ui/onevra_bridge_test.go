package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// Onevra 没有自己的调色板：每个组件都从 c.Theme() 读 Accent/Surface/
// Border/Radius/Spacing/FontSize。所以「能不能换」的第一道判据不是 API，
// 是这一条——它拿到的是不是 ALIVE 的那套颜色。
//
// 这里不读源码断言，只渲一帧，然后在像素里找 theme.Accent。Onevra 默认的
// 强调色是陶土红 #bd481b（theme/theme.go:12），只要组件自己带色，图里就
// 一定找不到 ALIVE 的强调色。
func TestOnevraButtonPaintsAliveAccent(t *testing.T) {
	theme.Apply("")
	accent := theme.Accent
	if accent.A == 0 {
		t.Fatal("theme.Accent 是零值：测试前置的 theme.Apply 没生效")
	}

	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme.Theme())
		ui.Column(c).Fill().Padding(20).Children(func() {
			components.Button(c, "保存", components.ButtonPrimary).
				Label("保存").Width(120).Height(40)
		})
	}, 160, 80)
	tt.Frame()

	img := tt.Image()
	bounds := img.Bounds()
	hit := false
	for y := bounds.Min.Y; y < bounds.Max.Y && !hit; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if sameColor(img.RGBAAt(x, y), accent) {
				hit = true
				break
			}
		}
	}
	if !hit {
		t.Fatalf("渲染结果里找不到 theme.Accent (%d,%d,%d)：Onevra 按钮没吃 ALIVE 的主题", accent.R, accent.G, accent.B)
	}
}

// 尺寸是另一条独立判据。Onevra 的尺寸锚在 t.Space(n) 与 t.Rem(n) 上，
// 而我们手柄调的 24 / 30 / 34 不是它的整数档。构造函数写死的值能不能被
// 链式方法盖住，只能量出来。
//
// 这条量的是一件事：IconButton 的 .Size() 有效（它是链式调用，不是闭包）。
// 同一份文档在 onevra.go 里记着哪些盖得住、哪些盖不住。
func TestOnevraIconButtonSizeIsOverridable(t *testing.T) {
	theme.Apply("")

	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme.Theme())
		ui.Row(c).Fill().Padding(20).Children(func() {
			components.IconButton(c, nil, "关闭", components.IconButtonPlain).Size(24, 24)
		})
	}, 120, 80)
	tt.Frame()

	rect, ok := tt.Find("关闭")
	if !ok {
		t.Fatal("找不到 label 为「关闭」的元素")
	}
	// Find 给的是可点区域，圆角/内距不参与；24 是热区尺寸，容 1 DIP 的量化误差。
	if rect.W < 23 || rect.W > 25 || rect.H < 23 || rect.H > 25 {
		t.Fatalf("IconButton Size(24,24) 量到 %.1f×%.1f，覆盖没生效", rect.W, rect.H)
	}
}

// sameColor 比的是最终像素与 ui.Color。允许 1 的量化误差：MyGo 在 CPU
// 光栅里做的是 8 位合成，圆角与抗锯齿边上会有中间值，而这里找的是纯色区。
func sameColor(c interface{ RGBA() (r, g, b, a uint32) }, want ui.Color) bool {
	r, g, b, a := c.RGBA()
	if a == 0 {
		return false
	}
	// RGBA() 返回 16 位预乘值；want 是 8 位非预乘。
	near := func(got uint32, want uint8) bool {
		d := int(got>>8) - int(want)
		return d >= -1 && d <= 1
	}
	return near(r, want.R) && near(g, want.G) && near(b, want.B)
}
