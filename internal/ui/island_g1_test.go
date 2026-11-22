package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// islandRectsOverlap 报告两个矩形是否相交（边界相接不算）。
func islandRectsOverlap(a, b ui.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

// 窄窗口里展开的岛必须留在顶栏两端的固定控件之间。
//
// 岛是顶层命中目标，被它盖住的那一截（tab 右端、搜索按钮左端）点击会落进
// 岛根，tab 和搜索都收不到。以前上限只有「窗口一半」，MinWidth=900 时长
// 标题的岛取 360，正好越过两侧的 tab 组与工具组。
func TestPlayerIslandStaysBetweenNavbarGroups(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{
		Platform: "douyu",
		RoomID:   "1",
		Name:     "甲",
		Title:    strings.Repeat("很长很长的直播间标题", 20),
	}
	app.Router.Push(routePlayer)
	// 900 是 main.go 的 MinWidth：最窄的真实窗口，岛最容易越界。
	tt := ui.NewTester(app.Shell, 900, 700)
	tt.Frame()

	island, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	if island.W <= islandPlayerMinW {
		t.Fatalf("岛宽 %v 没有展开，这条用例没有覆盖到拥挤场景", island.W)
	}

	names := []string{"搜索"}
	for _, id := range app.tabIDs() {
		names = append(names, tabName(id))
	}
	for _, name := range names {
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("顶栏里找不到 %q", name)
		}
		if islandRectsOverlap(island, r) {
			t.Fatalf("灵动岛 [%.0f, %.0f] 盖住了 %q [%.0f, %.0f]：那一截点击会落进岛根",
				island.X, island.X+island.W, name, r.X, r.X+r.W)
		}
	}
}

// 灵动岛根元素必须自己就是拖窗目标。
//
// 岛挂在 Shell 根下、在 Column 之后构建，是顶栏 Navbar（唯一拖窗入口）的
// 兄弟。MyGo 的命中链只取指针下最上层的元素和它的祖先，不会穿到兄弟：
// 岛根不带 DragWindow 时，指针落在岛的非交互区域（时钟、头像、文字、留白）
// 会停在岛根，顶栏的 DragWindow 收不到，frameless 窗口正中就是一块拖不动、
// 双击也不会最大化的死区。
//
// Tester 的 headless host 把 startDrag 做成空操作，双击计数也没有导出，
// 这一条没法用行为断言；按 label_test.go 的路子用静态检查兜住：Island 里
// 创建根元素的那条链上必须有 DragWindow。（子元素上的 DragWindow 不算。）
func TestIslandRootDragsWindow(t *testing.T) {
	src, err := os.ReadFile("island.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "island.go", src, 0)
	if err != nil {
		t.Fatalf("island.go: %v", err)
	}

	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Island" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			id, ok := assign.Lhs[0].(*ast.Ident)
			if !ok || id.Name != "island" {
				return true
			}
			ast.Inspect(assign.Rhs[0], func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "DragWindow" {
					found = true
				}
				return true
			})
			return true
		})
		return false
	})
	if !found {
		t.Fatal("Island 的根元素链上没有 DragWindow：顶栏正中的拖窗与双击最大化会落进死区")
	}
}
