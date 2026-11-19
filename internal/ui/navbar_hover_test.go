package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 顶栏两侧的悬停反馈必须是同一套：未选中的 tab 铺一块 FillTertiary，
// 和右边工具组、窗口按钮一模一样。
//
// 这里曾经只有字色从 TextTertiary 变 TextPrimary，整颗 64×34 的胶囊还是
// 空的：左边看起来是「文字亮了一下」，右边是「按下去了一块」。两边坐在
// 同一种 SurfaceChrome 托盘里，用的就该是同一个填充。
//
// 选中项不叠底：它的底由 Accent 滑块画，tab 压在滑块上面（ZIndex 1），
// 再铺一块会把滑块盖掉。
func TestPlatformTabHoverTakesASurface(t *testing.T) {
	app := NewMockApp()
	app.PaletteID = "signal"
	app.ThemeMode = themeModeDark
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	// 取胶囊左沿往里 8px、垂直居中那一格：胶囊两端是半径 17 的半圆，
	// 这个点在实心区里；文字列从 ~18px 才开始，够不着。
	probe := func(b ui.Rect) ui.Color {
		p := tt.Image().RGBAAt(int(b.X)+8, int(b.Y+b.H/2))
		return ui.RGB(p.R, p.G, p.B)
	}
	hover := func(b ui.Rect) {
		tt.Move(b.X+b.W/2, b.Y+b.H/2)
		tt.Frame()
	}
	unhover := func() {
		tt.Move(720, 500)
		tt.Frame()
	}
	same := func(a, b ui.Color) bool { return a.R == b.R && a.G == b.G && a.B == b.B }

	// 未选中的平台 tab：悬停前是托盘色，悬停后是悬停填充。
	other, ok := tt.Find("虎牙")
	if !ok {
		t.Fatal("找不到平台 tab「虎牙」")
	}
	rest := probe(other)
	if !same(rest, theme.SurfaceChrome) {
		t.Fatalf("tab 常态不是托盘色：%v vs %v", rest, theme.SurfaceChrome)
	}
	hover(other)
	if got := probe(other); !same(got, theme.FillTertiary) {
		t.Fatalf("悬停平台 tab 没有铺表面：%v，期望 FillTertiary %v", got, theme.FillTertiary)
	}
	unhover()

	// 右侧工具图标走的就是这个填充：两边必须同色，否则又成了两套语言。
	if icon, ok := tt.Find("数据同步"); ok {
		hover(icon)
		// 圆形按钮半径 17，左沿往里 6px 在实心区。
		p := tt.Image().RGBAAt(int(icon.X)+6, int(icon.Y+icon.H/2))
		if got := ui.RGB(p.R, p.G, p.B); !same(got, theme.FillTertiary) {
			t.Fatalf("工具图标悬停色 %v，和平台 tab 的 FillTertiary 不一致", got)
		}
		unhover()
	}

	// 选中的 tab：悬停不能再铺一层，Accent 滑块露出来。
	sel, ok := tt.Find(tabName(app.activeTab()))
	if !ok {
		t.Fatalf("找不到选中的 tab %q", tabName(app.activeTab()))
	}
	hover(sel)
	if got := probe(sel); !same(got, theme.Accent) {
		t.Fatalf("悬停选中的 tab 盖住了滑块：%v，期望 Accent %v", got, theme.Accent)
	}
}
