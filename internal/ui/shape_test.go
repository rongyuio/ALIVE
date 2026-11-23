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

// 会飞（Match + Fly）的元素必须自己带外形：圆角、Clip，不能只靠祖先。
//
// MyGo 把飞行元素画在祖先的裁剪之外（docs/ui/transitions.md：「paints out of
// the clips of the scroll views and containers around it」），而飞行时的圆角
// 取的是元素自己的 radius（ui/match.go 的 lookOf / mixLook，在源元素与目标
// 元素之间插值）。所以只由祖先 Clip() 提供的外形，在飞行的几百毫秒里会消失：
// 2026-11-23 报的「过渡动画时是直角」就是外壳的 Radius(16).Clip() 在飞行帧
// 失效，舞台自己只有黑底。
//
// 这条只能查源码：Tester 的软件光栅器会套用祖先裁剪，飞行帧在它那里看不出
// 差别——写运行时断言必然是恒真的。
func TestFlyingElementsCarryTheirOwnShape(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var problems []string
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		problems = append(problems, scanFlyingShape(t, path)...)
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		t.Fatalf("这些元素会飞（Match + Fly）但外形挂在祖先上（%d 处）：\n  %s",
			len(problems), strings.Join(problems, "\n  "))
	}
}

// scanFlyingShape 找 `.Transition(ui.ElementTransition{... Match: ..., Fly: true ...})`
// 的调用，回溯它作用在哪个元素（变量名），再看这个变量的构造链上有没有
// `Radius(` 或 `Clip(`。
//
// 认两种写法，和 label_test.go 一样：
//
//	stage := ui.Box(c).Grow(1).Background(...)   // 先赋值，链上有 Radius / Clip
//	stage.Transition(...)
//
//	ui.Box(c).Radius(16).Transition(...)          // 一条链到底
func scanFlyingShape(t *testing.T, path string) []string {
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
	var problems []string

	// shaped 记下「变量名 → 它在构造链或调用语句里带过 Radius / Clip」。
	//
	// 外形可以写在构造链上（ui.Box(c).Radius(16)...），也可以写在之后的
	// 独立语句里（stage.Radius(16).Clip()）——两种都算，因为对 MyGo 来说
	// 都落在元素自己身上。
	shaped := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		if !rootedInUI(as.Rhs[0]) {
			return true
		}
		if callsAny(as.Rhs[0], "Radius", "Clip") {
			shaped[id.Name] = true
		}
		return true
	})

	// 「变量.Radius(...)」「变量.Clip()」形式的独立语句。
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "Radius" && sel.Sel.Name != "Clip") {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			shaped[id.Name] = true
		}
		return true
	})

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Transition" {
			return true
		}
		if !isFlying(call.Args[0]) {
			return true
		}
		pos := fset.Position(call.Pos())
		switch recv := sel.X.(type) {
		case *ast.Ident:
			if shaped[recv.Name] {
				return true
			}
			problems = append(problems, pos.String()+": "+recv.Name+
				" 会飞，但它的构造链上没有 Radius / Clip")
		case *ast.CallExpr:
			if callsAny(recv, "Radius", "Clip") {
				return true
			}
			problems = append(problems, pos.String()+": 这条链会飞，但没有 Radius / Clip")
		}
		return true
	})
	return problems
}

// rootedInUI 报告 expr 是不是一条从 ui.Xxx(c) 起手的元素构造链。
// 链可能很长（ui.Box(c).Grow(1).Background(...).Label(...)），所以一路
// 沿 SelectorExpr 的 X 往下走到根。
func rootedInUI(expr ast.Expr) bool {
	for {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if pkg, ok := sel.X.(*ast.Ident); ok {
			return pkg.Name == "ui"
		}
		expr = sel.X
	}
}

// callsAny 报告这条（可能嵌套的）方法链上有没有调用过这些方法。
func callsAny(expr ast.Expr, names ...string) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		for _, name := range names {
			if sel.Sel.Name == name {
				found = true
			}
		}
		return true
	})
	return found
}

// isFlying 报告过渡字面量里是不是 Match 非空、Fly 为真。
func isFlying(expr ast.Expr) bool {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return false
	}
	hasMatch, fly := false, false
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Match":
			// 空值（nil / 零值字面量）不算。
			if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "nil" {
				continue
			}
			hasMatch = true
		case "Fly":
			if id, ok := kv.Value.(*ast.Ident); ok && id.Name == "true" {
				fly = true
			}
			if bl, ok := kv.Value.(*ast.BasicLit); ok && bl.Kind == token.INT && bl.Value == "1" {
				fly = true
			}
		}
	}
	return hasMatch && fly
}
