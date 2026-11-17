package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 配色面板必须贴着配色按钮的右边缘展开，宽度 352（按钮 wrapper 的绝对定位子元素）。
func TestPalettePopoverAnchorsToButton(t *testing.T) {
	app := NewMockApp()
	app.PaletteOpen = true
	tt := ui.NewTester(app.Shell, 1440, 900)

	btn, ok := tt.Find("配色面板")
	if !ok {
		t.Fatal("找不到配色按钮")
	}
	panel, ok := tt.Find("配色浮层")
	if !ok {
		t.Fatal("找不到配色面板")
	}

	if panel.W != palettePanelW {
		t.Errorf("面板宽 %.1f，期望 %v", panel.W, palettePanelW)
	}
	if got, want := panel.X+panel.W, btn.X+btn.W; got != want {
		t.Errorf("面板右边缘 %.1f，按钮右边缘 %.1f：浮层没有右对齐按钮", got, want)
	}
}

// 夜 / 日 / 跟随系统只换同一色系的另一半，不把用户选的色系换掉。
func TestThemeModeKeepsPaletteFamily(t *testing.T) {
	app := NewMockApp()
	app.ThemeMode = themeModeLight
	app.PaletteID = "signal" // 青柠 · 夜
	app.syncThemeMode(true)
	if app.PaletteID != "daylight" {
		t.Fatalf("切到日间后配色 = %q，期望 daylight", app.PaletteID)
	}

	app.ThemeMode = themeModeDark
	app.PaletteID = "paper" // 黑白灰 · 日
	app.syncThemeMode(false)
	if app.PaletteID != "mono" {
		t.Fatalf("切到夜间后配色 = %q，期望 mono", app.PaletteID)
	}

	// 跟随系统：系统深色用深色侧，系统浅色用浅色侧。
	app.ThemeMode = themeModeSystem
	app.syncThemeMode(true)
	if cur := theme.Find(app.PaletteID); cur == nil || !cur.Dark {
		t.Fatalf("跟随深色系统时仍是 %q", app.PaletteID)
	}
	app.syncThemeMode(false)
	if cur := theme.Find(app.PaletteID); cur == nil || cur.Dark {
		t.Fatalf("跟随浅色系统时仍是 %q", app.PaletteID)
	}
}

// 面板标题右侧是夜 / 日 / 跟随系统三段切换，原来那行「N 套 · 当前 …」不再出现；
// 下面的配色格只列当前这一侧。
func TestPalettePanelShowsOneSide(t *testing.T) {
	app := NewMockApp()
	app.PaletteOpen = true
	app.ThemeMode = themeModeDark
	app.PaletteID = "signal"
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if tt.HasText("套 · 当前") {
		t.Fatal("旧的「N 套 · 当前 …」文案还在")
	}
	if !tt.HasText("青柠·夜") || tt.HasText("青柠·日") {
		t.Fatalf("夜间面板列出的配色不对：%v", tt.Texts())
	}
	// 只有一个圆形按钮，状态写在无障碍名里：夜 → 跟随系统 → 日。
	if _, ok := tt.Find("配色模式 夜"); !ok {
		t.Fatalf("缺少切换按钮，文本：%v", tt.Texts())
	}
	for _, gone := range []string{"配色模式 日", "配色模式 跟随系统"} {
		if _, ok := tt.Find(gone); ok {
			t.Fatalf("三个状态不该各占一个按钮（找到了 %q）", gone)
		}
	}

	// 按一次：跟随系统；再按一次：日，面板当场换到浅色侧。
	if err := tt.Click("配色模式 夜"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.ThemeMode != themeModeSystem {
		t.Fatalf("第一次点击后模式 = %q，期望跟随系统", app.ThemeMode)
	}
	if _, ok := tt.Find("配色模式 跟随系统"); !ok {
		t.Fatal("按钮图标/文案没跟上模式")
	}
	if err := tt.Click("配色模式 跟随系统"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.ThemeMode != themeModeLight {
		t.Fatalf("第二次点击后模式 = %q，期望日", app.ThemeMode)
	}
	if cur := theme.Find(app.PaletteID); cur == nil || cur.Dark {
		t.Fatalf("切到日间后配色 = %q", app.PaletteID)
	}
	if !tt.HasText("青柠·日") || tt.HasText("青柠·夜") {
		t.Fatalf("切到日间后面板仍是：%v", tt.Texts())
	}
}

// 循环顺序固定：日 → 夜 → 跟随系统 → 日，不能跳步也不能停在某个状态。
func TestThemeModeCycles(t *testing.T) {
	if got := themeModeNext(themeModeLight); got != themeModeDark {
		t.Fatalf("日 → %q", got)
	}
	if got := themeModeNext(themeModeDark); got != themeModeSystem {
		t.Fatalf("夜 → %q", got)
	}
	if got := themeModeNext(themeModeSystem); got != themeModeLight {
		t.Fatalf("跟随系统 → %q", got)
	}
	if got := themeModeNext(""); got != themeModeLight {
		t.Fatalf("空值 → %q", got)
	}
}
