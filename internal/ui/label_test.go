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

// 可点击但没有 Label 的控件，对读屏和 mygo-automation 都是隐形的：
// MyGo 的无障碍树只收有名字的节点，这类元素根本不会出现在 AccessAll() 里，
// 运行时断言抓不到——只能查源码。
//
// 规则见 AGENTS.md 的「无障碍标签」：
//   - 带 Cursor(ui.CursorPointer) / Cursor(ui.CursorText) 的控件必须 .Label(...)；
//   - 有 Clicked() 等点击回调的控件同样要。
//
// 写法有两种，都要认：
//
//	btn := ui.Box(c).Cursor(ui.CursorPointer).Label("关闭")   // 先赋值
//	if btn.Clicked() { ... }
//
//	if ui.Box(c).Cursor(ui.CursorPointer).Clicked() { ... }   // 一条链到底
func TestEveryClickableHasLabel(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		problems = append(problems, scanLabels(t, path)...)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("这些可点击控件没有 .Label()（%d 处）：\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// scanLabels 检查一个文件，返回「文件:行:列」形式的问题列表。
func scanLabels(t *testing.T, path string) []string {
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

	// 先收集「这个变量指向的控件可点击 / 有没有名字」。
	//
	// 名字可能晚一步才给：`follow := ui.Box(...)...; follow.Label(...)`。
	// 所以分两遍扫——先把所有 Label 调用记下来，再判断可点击的有没有名字。
	type state struct{ clickable, labeled bool }
	vars := map[string]state{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		s := vars[id.Name]
		if sel.Sel.Name == "Label" && len(call.Args) == 1 {
			s.labeled = true
		}
		vars[id.Name] = s
		return true
	})
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
			s := vars[id.Name]
			if chainHas(call, isCursorPointer) {
				s.clickable = true
			}
			if chainHas(call, isLabel) {
				s.labeled = true
			}
			vars[id.Name] = s
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
		if !ok {
			return true
		}
		// 只看最外层：内层调用会再被 Inspect 到，重复报同一个控件。
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			if innerSel, ok := inner.Fun.(*ast.SelectorExpr); ok && innerSel.Sel.Name != "" {
				return true
			}
		}

		clicked := sel.Sel.Name == "Clicked" || sel.Sel.Name == "DoubleClicked" || sel.Sel.Name == "RightClicked"
		// 变量形态：btn.Clicked()
		if id, ok := sel.X.(*ast.Ident); ok {
			if s := vars[id.Name]; s.clickable && clicked && !s.labeled {
				problems = append(problems, fset.Position(call.Pos()).String())
			}
			return true
		}
		// 一条链到底：ui.Box(c).Cursor(...).Clicked()
		root, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		if (clicked || chainHas(root, isCursorPointer)) && !chainHas(root, isLabel) {
			problems = append(problems, fset.Position(call.Pos()).String())
		}
		return true
	})
	return problems
}

// isCursorPointer 报告这一环是不是 Cursor(ui.CursorPointer|ui.CursorText)。
func isCursorPointer(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Cursor" || len(c.Args) != 1 {
		return false
	}
	switch selectorName(c.Args[0]) {
	case "CursorPointer", "CursorText":
		return true
	}
	return false
}

// isLabel 报告这一环是不是 Label("...")。
func isLabel(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Label" && len(c.Args) == 1
}

// chainHas 沿着链式调用的 X 一路往里走，看有没有哪一环满足 pred。
func chainHas(call *ast.CallExpr, pred func(*ast.CallExpr) bool) bool {
	for {
		if pred(call) {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		inner, ok := sel.X.(*ast.CallExpr)
		if !ok {
			return false
		}
		call = inner
	}
}

// selectorName 返回 ui.CursorPointer 这种表达式的最后一段。
func selectorName(e ast.Expr) string {
	if sel, ok := e.(*ast.SelectorExpr); ok {
		return sel.Sel.Name
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}
