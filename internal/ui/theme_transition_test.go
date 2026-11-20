package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 换配色时颜色在 190ms 内连续混合；中途再换目标从当前颜色继续，不回跳。
func TestThemeTransitionBlendsAndRetargets(t *testing.T) {
	app := NewMockApp()
	app.ThemeMode = themeModeDark
	app.PaletteID = "inkwhite-night"
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	from := theme.Find("inkwhite-night")
	to := theme.Find("curtain-night")
	third := theme.Find("tech-night")
	if theme.Accent != from.Accent {
		t.Fatalf("首帧不是墨白·夜：%v", theme.Accent)
	}

	app.PaletteID = to.ID
	tt.Frame()
	if theme.Accent != from.Accent {
		t.Fatalf("切换首帧应仍停在起点，实际 %v", theme.Accent)
	}

	tt.Advance(95 * time.Millisecond)
	mid := theme.Accent
	if mid == from.Accent || mid == to.Accent {
		t.Fatalf("95ms 时颜色没有在中间：%v → %v", from.Accent, mid)
	}

	// 中途换目标：不能跳回起点，也不能跳到新目标。
	app.PaletteID = third.ID
	tt.Frame()
	if d := colorDelta(theme.Accent, mid); d > 2 {
		t.Fatalf("换目标回跳了：%v → %v（Δ%d）", mid, theme.Accent, d)
	}
	tt.Advance(250 * time.Millisecond)
	if theme.Accent != third.Accent {
		t.Fatalf("过渡没有落到科技蓝·夜：%v", theme.Accent)
	}
}

func colorDelta(a, b ui.Color) int {
	d := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	n := d(a.R, b.R)
	if v := d(a.G, b.G); v > n {
		n = v
	}
	if v := d(a.B, b.B); v > n {
		n = v
	}
	return n
}
