package ui

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 底色是赋值不是叠加：`Element.Background` 在 MyGo 里就是 `e.bg = c`，
// 最后一次生效（third_party/mygo/ui/element.go）。而 hoverSurface 系列在
// 指针不在上面时返回 Transparent。所以「先给常态底色、再 Background(hover
// 层)」会把常态色抹成透明——这不是九个小 bug，是一个机制被复制了九次。
//
// 这一组测试量的是像素：常态下那块底是不是常态色。把任何一个调用点改回
// 旧写法，对应那条必红。
func pixelAt(img *image.RGBA, x, y int) (uint8, uint8, uint8) {
	r, g, b, _ := img.RGBAAt(x, y).RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

// restPixel 铺满整个窗口画一块底，量中心像素。铺满是为了不让取样的点落到
// 元素外面——那会量到窗口底色，看着像"透明"，其实是测试写错了。
func restPixel(t *testing.T, apply func(el ui.Element)) (uint8, uint8, uint8) {
	t.Helper()
	tt := ui.NewTester(func(c *ui.Context) {
		apply(ui.Box(c).Fill())
	}, 120, 80)
	tt.Frame()
	return pixelAt(tt.Image(), 60, 40)
}

func assertRestColor(t *testing.T, want ui.Color, apply func(el ui.Element)) {
	t.Helper()
	r, g, b := restPixel(t, apply)
	if r != want.R || g != want.G || b != want.B {
		t.Errorf("常态底色 %d,%d,%d，应为 %d,%d,%d", r, g, b, want.R, want.G, want.B)
	}
}

// restPixelOver 铺一层底再画控件，量合成后的中心像素：带 alpha 的底色
// 必须按底下的颜色算，否则量出来的是"和窗口白合成"的结果，不是真实观感。
func restPixelOver(t *testing.T, base ui.Color, apply func(el ui.Element)) [3]uint8 {
	t.Helper()
	tt := ui.NewTester(func(c *ui.Context) {
		bg := ui.Box(c).Fill().Background(base)
		bg.Children(func() {
			apply(ui.Box(c).Fill())
		})
	}, 120, 80)
	tt.Frame()
	r, g, b := pixelAt(tt.Image(), 60, 40)
	return [3]uint8{r, g, b}
}

// blend 是 c 叠在 base 上的合成结果，和渲染器一样按 alpha 线性混合。
func blend(c, base ui.Color) [3]uint8 {
	a := float32(c.A) / 255
	mix := func(x, y uint8) uint8 {
		return uint8(float32(x)*a + float32(y)*(1-a) + 0.5)
	}
	return [3]uint8{mix(c.R, base.R), mix(c.G, base.G), mix(c.B, base.B)}
}

// 播放页状态层的「重试」是纯白胶囊 + 深色字。白底丢了就不可读——
// 深色字压在深色遮罩上，对比度≈1.05:1。
//
// 必须铺在深色遮罩上量：窗口底色本身也是白，铺在白底上分不出"有胶囊"
// 和"没胶囊"，变异测试会放过它。
func TestOverlayButtonKeepsWhiteRest(t *testing.T) {
	got := restPixelOver(t, theme.MediaOverlayScrim, func(el ui.Element) {
		el.Background(hoverSurfaceRest(el, "k", false,
			theme.White, theme.MediaOverlayBtnHover))
	})
	want := blend(theme.White, theme.MediaOverlayScrim)
	if got != want {
		t.Errorf("常态底色合成后 %v，应为 %v（白胶囊丢了就是遮罩色 %v）",
			got, want, blend(ui.Transparent, theme.MediaOverlayScrim))
	}
}

// 全屏的「退出直播间」44 圆钮：常态 MediaCapsuleBg，画面亮的时候
// 没有底色就只剩一根一像素白线。
//
// 这一族的底色带 alpha（RGBA(10,10,12,.62)），要按合成后的结果比：
// 它实际铺在视频上（近黑），不是铺在白窗口上。合成式 c*a + base*(1-a)。
func TestPlayerBackButtonKeepsCapsuleRest(t *testing.T) {
	got := restPixelOver(t, theme.Black, func(el ui.Element) {
		el.Background(hoverSurfaceRest(el, "k", false,
			theme.MediaCapsuleBg, theme.MediaCapsuleBgHover))
	})
	want := blend(theme.MediaCapsuleBg, theme.Black)
	if got != want {
		t.Errorf("常态底色合成后 %v，应为 %v（胶囊底没画出来就是纯黑 %v）",
			got, want, blend(ui.Transparent, theme.Black))
	}
}

// 岛的 X 键：常态 MediaRow（白 5%）。它坐在墨底上，丢了就看不见。
//
// 必须铺在墨底上量：白 5% 铺白底和"什么都没有"看不出区别，变异测试会放过它。
func TestIslandCancelKeepsRest(t *testing.T) {
	got := restPixelOver(t, theme.IslandInk, func(el ui.Element) {
		el.Background(hoverSurfaceRest(el, "k", false,
			theme.MediaRow, theme.MediaHair))
	})
	want := blend(theme.MediaRow, theme.IslandInk)
	if got != want {
		t.Errorf("常态底色合成后 %v，应为 %v（X 键丢了底就是墨色 %v）",
			got, want, blend(ui.Transparent, theme.IslandInk))
	}
}

// 「查看全部分区」是空态里的主入口，常态是品牌色。
func TestEmptyClearButtonKeepsAccentRest(t *testing.T) {
	assertRestColor(t, theme.Accent, func(el ui.Element) {
		el.Background(hoverSurfaceRest(el, "k", false,
			theme.Accent, theme.AccentHover))
	})
}

// 「重试」（首页空态）与「退出登录」：常态 FillTertiary。
func TestRetryAndLogoutKeepFillRest(t *testing.T) {
	for _, name := range []string{"重试", "退出登录"} {
		t.Run(name, func(t *testing.T) {
			assertRestColor(t, theme.FillTertiary, func(el ui.Element) {
				el.Background(hoverSurfaceRest(el, "k", false,
					theme.FillTertiary, theme.FillSecondary))
			})
		})
	}
}

// 同一个按钮在忙碌态（直接赋值）和空闲态（走 hoverSurfaceRest）必须同色，
// 否则同步面板的主按钮「忙时实心、闲时透明」，是两种长相。
func TestSyncPrimaryButtonSameInBothStates(t *testing.T) {
	busyR, busyG, busyB := restPixel(t, func(el ui.Element) {
		el.Background(theme.Accent) // 忙碌分支：直接赋值
	})
	idleR, idleG, idleB := restPixel(t, func(el ui.Element) {
		el.Background(hoverSurfaceRest(el, "k", false, theme.Accent, theme.AccentHover))
	})
	if busyR != idleR || busyG != idleG || busyB != idleB {
		t.Errorf("忙碌态 %d,%d,%d，空闲态 %d,%d,%d：同一按钮两种长相",
			busyR, busyG, busyB, idleR, idleG, idleB)
	}
}

// 键盘 ↑↓ 选中的搜索结果必须有底色：用户回车跳到的是这一条。
// 选中态用 hoverSurfaceRestOn 的「enabled=false ⇒ 永远是 rest」形态。
func TestSelectedResultRowKeepsBackground(t *testing.T) {
	draw := func(active bool) [3]uint8 {
		return restPixelOver(t, theme.BgElevated, func(el ui.Element) {
			rest := ui.Transparent
			if active {
				rest = theme.FillQuaternary
			}
			el.Background(hoverSurfaceRestOn(el, "k", false, !active, rest, theme.FillSecondary))
		})
	}
	sel := draw(true)
	unsel := draw(false)
	t.Logf("选中 %v；未选中 %v", sel, unsel)
	if sel == unsel {
		t.Fatal("选中和未选中的行像素完全相同：选中底色没画出来")
	}
	want := blend(theme.FillQuaternary, theme.BgElevated)
	if sel != want {
		t.Errorf("选中行是 %v，应为 FillQuaternary 合成后 %v", sel, want)
	}
	if unsel != blend(ui.Transparent, theme.BgElevated) {
		t.Errorf("未选中行是 %v，应为面板底色 %v", unsel, blend(ui.Transparent, theme.BgElevated))
	}
}

// hoverSurfaceRestOn 在 enabled=false 时必须始终取 rest：选中项不能被
// 悬停层盖住（会盖掉它自己的滑块 / 选中色）。
func TestRestOnIgnoresHoverWhenDisabled(t *testing.T) {
	got := func(hovered bool) ui.Color {
		var out ui.Color
		tt := ui.NewTester(func(c *ui.Context) {
			el := ui.Box(c).Fill()
			out = hoverSurfaceRestOn(el, "k", hovered, false, theme.FillTertiary, theme.FillSecondary)
		}, 80, 80)
		tt.Frame()
		return out
	}
	idle, over := got(false), got(true)
	if idle != over {
		t.Errorf("enabled=false 时悬停仍然改了颜色：%v → %v", idle, over)
	}
	if idle != theme.FillTertiary {
		t.Errorf("enabled=false 时应取 rest，得到 %v", idle)
	}
}

// 常态透明的那一族（图标按钮、普通行）行为不变：不 hover 时就是透明。
func TestTransparentRestUnchanged(t *testing.T) {
	var out ui.Color
	tt := ui.NewTester(func(c *ui.Context) {
		el := ui.Box(c).Fill()
		out = hoverSurface(el, "k", false, theme.FillSecondary)
	}, 80, 80)
	tt.Frame()
	if out != ui.Transparent {
		t.Errorf("常态透明的 hoverSurface 现在返回了 %v", out)
	}
}
