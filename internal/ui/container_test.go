package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// ------------------------------------------------------------------ 静态检查

// 静态容器不画装饰描边。
//
// 规则一句话：表面靠色阶分层，边界靠间距和圆角，不靠 1px 线。
// 理由是可测的——daylight 里 Border 对地只有 1.27:1，那条线在低视力下不存在，
// 在 150% DPI 下还被拉成 1.5 个物理像素。它既不是功能边界，也不是体量。
//
// 这条测试抓两类组合：
//
//	Background(theme.BgSecondary) + Border(1, theme.Border)    // 卡片 / 面板
//	Background(theme.FillQuaternary) + Border(1, theme.Border)  // 井 / 输入框
//
// 例外用上一行的 `// container: allow 理由` 标注。浮层的 BgElevated + 描边
// 不在规则里：它们压在内容上，阴影单独撑不住浅色主题的边界。
//
// 功能性描边（订阅按钮、聚焦环）要走另一条 ≥3:1 的线，不归这里管。
func TestStaticSurfacesHaveNoStroke(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		problems = append(problems, scanContainerStrokes(t, path)...)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("这些静态表面又画了 1px 描边（%d 处）：\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// allowWindow 是一行 `// container: allow` 能保护的范围：它下面 4 行。
// 链式调用会跨行，标记写在链的起点上方。
const allowWindow = 4

func scanContainerStrokes(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(src), "\n")
	allow := map[int]bool{}
	for i, l := range lines {
		if strings.Contains(l, "container: allow") {
			for k := i + 1; k <= i+1+allowWindow; k++ {
				allow[k] = true
			}
		}
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	type state struct {
		surface bool
		stroke  bool
		pos     token.Pos
	}
	vars := map[string]state{}

	// 第一遍：变量形态。面板写完再补 `.Border(...)` 的也要算进去。
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			if len(n.Lhs) != len(n.Rhs) {
				return true
			}
			for i, lhs := range n.Lhs {
				id, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				call, ok := n.Rhs[i].(*ast.CallExpr)
				if !ok {
					continue
				}
				s := vars[id.Name]
				if chainHas(call, isStaticFill) {
					s.surface = true
					s.pos = call.Pos()
				}
				if chainHas(call, isThemeBorder) {
					s.stroke = true
				}
				vars[id.Name] = s
			}
		case *ast.CallExpr:
			sel, ok := n.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Border" {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if isThemeBorder(n) {
				s := vars[id.Name]
				s.stroke = true
				vars[id.Name] = s
			}
		}
		return true
	})

	var problems []string
	report := func(pos token.Pos) {
		line := fset.Position(pos).Line
		if allow[line] {
			return
		}
		problems = append(problems, fset.Position(pos).String())
	}
	for _, s := range vars {
		if s.surface && s.stroke {
			report(s.pos)
		}
	}

	// 第二遍：一条链到底的形态（没有赋值给变量）。
	ast.Inspect(f, func(n ast.Node) bool {
		stmt, ok := n.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		if chainHas(call, isStaticFill) && chainHas(call, isThemeBorder) {
			report(call.Pos())
		}
		return true
	})
	return problems
}

// isStaticFill 报告这一环是不是静态表面的填充。
func isStaticFill(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Background" || len(c.Args) != 1 {
		return false
	}
	switch selectorName(c.Args[0]) {
	case "BgSecondary", "FillQuaternary":
		return true
	}
	return false
}

// isThemeBorder 报告这一环是不是 Border(n, theme.Border)。
func isThemeBorder(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Border" || len(c.Args) != 2 {
		return false
	}
	s, ok := c.Args[1].(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := s.X.(*ast.Ident)
	return ok && id.Name == "theme" && s.Sel.Name == "Border"
}

// ------------------------------------------------------------------ 帧像素

// 顶栏三组托盘必须是同一块面。
//
// 这条盯的是「配方分叉」：左边用 fill-quaternary + 描边、右边用同一个填充
// 不描边、中间的岛又是白的——一张截图里四种材质。静态检查能挡住描边，
// 挡不住「给其中一组换一个填充」；像素签名能。
//
// 只扫顶栏两端，中间留给灵动岛和它那圈很宽的投影。
// 一条色段要被当成「面」，还得在上下 14 DIP 处还是同一个颜色——这样文字、
// 图标和头像（它们都不占满托盘高度）不会被误判成一种新表面。
func TestNavbarTraysUseOneSurface(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale float32
	}{{"100%", 1}, {"150%", 1.5}} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewMockApp()
			app.PaletteID = "daylight"
			app.ThemeMode = themeModeLight
			tt := ui.NewTester(app.Shell, 1440, 900)
			tt.SetScale(tc.scale)
			tt.Frame()

			img := tt.Image()
			w := img.Bounds().Dx()
			y := int(theme.NavbarHeight / 2 * tc.scale)
			if y >= img.Bounds().Dy() {
				t.Fatalf("顶栏中线 %d 落在画布外", y)
			}

			allowed := map[uint32]string{
				rgbKey(theme.BgPrimary):     "地面",
				rgbKey(theme.SurfaceChrome): "托盘",
				rgbKey(theme.Accent):        "滑块",
			}
			// 中间 40% 留给灵动岛
			spans := [][2]int{{0, w * 3 / 10}, {w * 7 / 10, w}}

			chromeRuns := 0
			for _, sp := range spans {
				x := sp[0]
				for x < sp[1] {
					key := pixelKey(img, x, y)
					end := x + 1
					for end < sp[1] && pixelKey(img, end, y) == key {
						end++
					}
					// 短段是字、图标和抗锯齿
					if end-x >= int(8*tc.scale) && spansHeight(img, (x+end)/2, y, int(14*tc.scale), key) {
						name, ok := allowed[key]
						if !ok {
							t.Errorf("顶栏 x=%d..%d 出现第 %d 种面：不在地面/托盘/滑块里（#%06x）",
								x, end, len(allowed)+1, key)
						}
						if name == "托盘" {
							chromeRuns++
						}
					}
					x = end
				}
			}
			if chromeRuns < 3 {
				t.Errorf("只找到 %d 段托盘：三组托盘没有共用同一块面（或被别的填充盖住了）", chromeRuns)
			}
		})
	}
}

// spansHeight 报告 (x,y) 的颜色在 y±dy 处是否还是同一个颜色。
// 托盘占满整条高度，文字和图标不占。
func spansHeight(img *image.RGBA, x, y, dy int, key uint32) bool {
	b := img.Bounds()
	for _, yy := range []int{y - dy, y + dy} {
		if yy < 0 || yy >= b.Dy() {
			return false
		}
		if pixelKey(img, x, yy) != key {
			return false
		}
	}
	return true
}

func pixelKey(img *image.RGBA, x, y int) uint32 {
	r, g, b, _ := img.At(x, y).RGBA()
	return rgbKey(ui.RGB(uint8(r>>8), uint8(g>>8), uint8(b>>8)))
}

func rgbKey(c ui.Color) uint32 {
	return uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}
