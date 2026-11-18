package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ClipInset 是 CSS 的 clip-path: inset()，它把元素自己连同背景、描边、
// 子元素一起裁掉。同一个元素既画圆角描边、又用 ClipInset 裁自己时，
// 裁剪边的圆角正好落在描边中线上：直边处像素中心在裁剪内、描边完整，
// 弧线上覆盖度被裁剪和描边各算一次、乘起来只剩四分之一——四个角的线条
// 就没了。播放页外壳就是这么坏的（见 player_test.go 的
// TestPlayerShellCornerBorderIsContinuous）。
//
// 正确的写法是 Clip()（overflow: hidden）：它裁的是 padding box，圆角
// 自动减掉边框宽，描边在裁剪之后补画一遍。
//
// 这条规则只拦「自己画描边又自己裁」。纯裁剪用的 ClipInset（封面切上两角、
// 调色板色带）没有描边，不受影响。
func TestClipInsetDoesNotCutItsOwnBorder(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		problems = append(problems, scanClipInsetOnBordered(t, path)...)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("这些元素既画描边又用 ClipInset 裁自己，圆角弧线会被裁掉（%d 处），改用 Clip()：\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// scanClipInsetOnBordered 检查一个文件，返回「文件:行」形式的问题列表。
//
// 两种写法都认：
//
//	box := ui.Column(c).Radius(16).Border(1, c)   // 先赋值
//	box.ClipInset(0, 0, 0, 0, 16)
//
//	ui.Column(c).Radius(16).Border(1, c).ClipInset(0, 0, 0, 0, 16)  // 一条链
func scanClipInsetOnBordered(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}

	// 第一遍：哪些变量自己画了描边。
	bordered := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != len(as.Rhs) {
			return true
		}
		for i, lhs := range as.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			call, ok := as.Rhs[i].(*ast.CallExpr)
			if !ok {
				continue
			}
			if chainHas(call, isVisibleBorder) {
				bordered[id.Name] = true
			}
		}
		return true
	})

	var problems []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ClipInset" {
			return true
		}
		switch x := sel.X.(type) {
		case *ast.Ident:
			if bordered[x.Name] {
				problems = append(problems, fset.Position(call.Pos()).String())
			}
		case *ast.CallExpr:
			// 两种写法都要认：
			//   - 一条链到底，链上既有 Border 也有 Radius；
			//   - 链根是变量，变量在别的语句里画的描边（player.go 的 shell 就是
			//     这样：Border 在 `shell := ...` 那条链上，Radius 和 ClipInset
			//     在后面的 `shell.Radius(...).ClipInset(...)` 里）。
			ownBorder := chainHas(x, isVisibleBorder)
			if root, ok := chainRoot(x).(*ast.Ident); ok && bordered[root.Name] {
				ownBorder = true
			}
			if ownBorder && chainHas(x, isRadius) {
				problems = append(problems, fset.Position(call.Pos()).String())
			}
		}
		return true
	})
	return problems
}

// isVisibleBorder 报告这一环是不是会画出来的描边。
//
// Border(0, ...) 是「去掉描边」的写法（全屏分支就这么用），不算。
func isVisibleBorder(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Border" || len(c.Args) == 0 {
		return false
	}
	lit, ok := c.Args[0].(*ast.BasicLit)
	if ok && lit.Value == "0" {
		return false
	}
	return true
}

// isRadius 报告这一环是不是 Radius(...)。
func isRadius(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Radius" && len(c.Args) > 0
}

// chainRoot 沿着链式调用一路往里走，返回最里面那一环：ui.Box(c) 这样的
// 构造调用，或者一个变量名。
func chainRoot(call *ast.CallExpr) ast.Expr {
	for {
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return call
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return sel.X
		}
		call = inner
	}
}
