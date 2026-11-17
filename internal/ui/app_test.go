package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 视图测试不需要窗口：ui.NewTester 在内存里渲染，能点击、输入、按键。
// 用真实尺寸建视图，保证网格列数与窗口一致。
func TestHomeOpensPlayer(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	if !tt.HasText("旭旭宝宝") {
		t.Fatalf("首页没有渲染主播卡片，texts=%q", tt.Texts())
	}
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if got := app.Router.Path(); got != "/player" {
		t.Fatalf("点击卡片后路由为 %q，期望 /player", got)
	}
}

// 配色面板是一层浮层，展开后不应该遮掉首页内容，切配色要真的换掉令牌。
func TestPaletteSwitch(t *testing.T) {
	app := NewMockApp()
	app.PaletteOpen = true
	// 面板只列当前这一侧的配色（深色三套 / 浅色三套），所以要显式选日间，
	// 不能依赖 Tester 的默认外观——那个默认值会随 MyGo 版本变。
	app.ThemeMode = "light"
	tt := ui.NewTester(app.Shell, 1440, 900)

	if !tt.HasText("主题配色") {
		t.Fatalf("配色面板没有展开，texts=%q", tt.Texts())
	}
	if err := tt.Click("书页·日"); err != nil {
		t.Fatal(err)
	}
	if app.PaletteID != "parchment" {
		t.Fatalf("选中的配色为 %q，期望 parchment", app.PaletteID)
	}
	if app.PaletteOpen {
		t.Fatal("选中配色后面板应该收起")
	}
}
