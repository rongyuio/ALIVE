package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 悬停底色必须真的在动，而不是瞬间到位。
//
// 判据是三个像素：常态、悬停中途（40ms）、悬停停稳（400ms）。底色走
// theme.HoverInk() 弹簧时，中途那个值必须既不等于常态也不等于终态——这正是
// 「底色瞬间跳、文字在弹」那个缺陷的反向回归。
func TestHoverSurfaceAnimates(t *testing.T) {
	app := NewMockApp()
	app.PaletteID = "signal"
	app.ThemeMode = themeModeDark
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	chip, ok := tt.Find("分类:无畏契约")
	if !ok {
		t.Fatal("找不到二级分区 chip")
	}
	// 取胶囊左沿往里 6px、垂直居中：圆角半径 8，这个点在实心区里，
	// 文字从更右边才开始。
	probe := func() ui.Color {
		p := tt.Image().RGBAAt(int(chip.X)+6, int(chip.Y+chip.H/2))
		return ui.RGB(p.R, p.G, p.B)
	}
	same := func(a, b ui.Color) bool { return a.R == b.R && a.G == b.G && a.B == b.B }

	rest := probe()
	tt.Move(chip.X+chip.W/2, chip.Y+chip.H/2)
	tt.Frame()
	tt.Advance(40 * time.Millisecond)
	tt.Frame()
	mid := probe()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	settled := probe()

	if same(mid, rest) {
		t.Fatalf("悬停 40ms 时底色还是常态 %v：底色没有过渡（瞬间到位或根本没动）", mid)
	}
	if same(mid, settled) {
		t.Fatalf("悬停 40ms 时底色已经是终态 %v：底色是瞬间跳的，不是动效", mid)
	}
	if same(rest, settled) {
		t.Fatalf("悬停之后底色没变：%v → %v", rest, settled)
	}
}
