package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// 渲染路径不许直接写日志。
//
// 背景：一次事件会重建多帧，渲染路径里的副作用会执行多次；同步文件 I/O
// 还会把耗时压在帧上。诊断事件只能从状态翻转处发，不能从 Shell/Card/Watch
// 的构建函数里发。这条规则用 AST 钉住，不靠注释。
func TestNoLoggingInRenderPath(t *testing.T) {
	files := []string{
		"player.go", "card.go", "home.go", "watch.go", "surface.go",
		"island.go", "danmu.go", "danmu_panel.go", "follows_page.go",
	}
	for _, file := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && (pkg.Name == "slog" || pkg.Name == "applog") {
				t.Errorf("%s:%d 渲染路径直接调了 %s.%s",
					file, fset.Position(call.Pos()).Line, pkg.Name, sel.Sel.Name)
			}
			if sel.Sel.Name == "event" || sel.Sel.Name == "Log" {
				t.Errorf("%s:%d 渲染路径直接调了 %s（诊断事件只能从状态翻转处发）",
					file, fset.Position(call.Pos()).Line, sel.Sel.Name)
			}
			return true
		})
	}
}
