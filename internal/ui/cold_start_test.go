package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// coldBootApp 是用户看到的第一帧：示例数据接上，但还没热身。
//
// 以前这里还要把启动层打开，好让测试复现生产的第一眼；启动层删掉之后，
// 冷启动就只是「数据还没到的普通一帧」——不再是一个需要单独构造的状态。
func coldBootApp() *App {
	a := NewMockApp()
	// 下面按 signal 夜色数像素，配色不能跟着本机 state.json 走。
	a.PaletteID = "signal"
	a.ThemeMode = themeModeDark
	return a
}

// 冷启动第一帧就该是应用本体：顶栏、平台胶囊、灵动岛、窗口按钮都在。
// 盖一层不透明的启动层，这一条就只剩底色。
func TestColdStartShowsTheShell(t *testing.T) {
	app := coldBootApp()
	tt := ui.NewTester(app.Shell, 900, 600)
	tt.Frame()

	if !theme.Current().Dark {
		t.Fatal("配色没钉在夜色上，下面的像素判断没有意义")
	}
	n := brightPixels(tt.Image(), 0, 0, 900, int(theme.NavbarHeight))
	t.Logf("顶栏亮像素 %d", n)
	if n < 800 {
		t.Errorf("顶栏只有 %d 个亮像素：冷启动第一帧被整屏盖住了", n)
	}
}

// 启动过程中输入不能被吞：这时候点平台 tab，就该换平台。
func TestColdStartAcceptsInput(t *testing.T) {
	app := coldBootApp()
	tt := ui.NewTester(app.Shell, 900, 600)
	tt.Frame()

	if err := tt.Click(tabName("douyu")); err != nil {
		t.Fatalf("冷启动时点不动顶栏：%v", err)
	}
	if app.Platform != "douyu" {
		t.Errorf("点了斗鱼却没换过去：Platform=%q", app.Platform)
	}
}

// 首屏空着的时候读屏要听到一句「正在载入」，而且只听到一次。
func TestColdStartAnnouncesTheWaitOnce(t *testing.T) {
	app := coldBootApp()
	app.Streamers = nil
	app.ListLoading = true
	tt := ui.NewTester(app.Shell, 900, 600)

	for i := 0; i < 4; i++ {
		tt.Frame()
	}
	got := tt.Announcements()
	if len(got) != 1 {
		t.Fatalf("播报了 %d 次：%q", len(got), got)
	}
	if !strings.Contains(got[0], "正在载入") {
		t.Errorf("播报内容是 %q，没说明在等什么", got[0])
	}
}

// 封面没到时那块占位必须是静止的。
//
// 一屏十几张卡一起转圈，每帧都在提醒「你还在等」，还各自跑一个 Loop
// 请求帧；占位本来就该安静，动的那个应该是封面到达。
func TestCoverPlaceholderDoesNotAnimate(t *testing.T) {
	src, err := os.ReadFile("card.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "card.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "coverArt" {
			return true
		}
		found = true
		var bad []string
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch x := call.Fun.(type) {
			case *ast.Ident:
				if x.Name == "spinnerTint" {
					bad = append(bad, "spinnerTint")
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "Loop" {
					bad = append(bad, "Loop")
				}
			}
			return true
		})
		if len(bad) > 0 {
			t.Errorf("封面占位还在动：coverArt 调用了 %s", strings.Join(bad, "、"))
		}
		return false
	})
	if !found {
		t.Fatal("card.go 里找不到 coverArt")
	}
}

// brightPixels 数一块区域里够亮的像素。
//
// 底色 #0a0b0d、胶囊轨道 #16181d 都在 0x40 以下；顶栏的字、accent 滑块、
// 时钟、窗口按钮都在上面。启动层的极淡光晕到不了这条线。
func brightPixels(img *image.RGBA, x0, y0, x1, y1 int) int {
	b := img.Bounds()
	if x1 > b.Dx() {
		x1 = b.Dx()
	}
	if y1 > b.Dy() {
		y1 = b.Dy()
	}
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 > 0x40 || g>>8 > 0x40 || bl>>8 > 0x40 {
				n++
			}
		}
	}
	return n
}
