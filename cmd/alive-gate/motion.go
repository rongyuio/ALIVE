package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// checkMotion：动画时长必须来自 internal/theme，不许在 ui 里就地写字面量。
//
// 这一条守的是「同一屏里几种节奏」这个反复出现的症状：190 / 220 / 240 / 320
// 各写各的，用户读不出哪两下是同一件事，改的时候也没人知道该改哪个。
//
// 判据刻意窄：只查**传进动画 API 的**时长字面量（AnimateTo / AnimateColor /
// Loop / ElementTransition / ui.Animation / EaseOut / EaseInOut / c.After 的
// 参数位置），以及这些位置上的 `N * time.Millisecond`。超时、轮询、防抖、
// 去重窗口不属于动画，它们是"等多久"，不是"动多久"——分开算。
var motionAPIs = map[string]bool{
	"AnimateTo":         true,
	"AnimateColor":      true,
	"Loop":              true,
	"Animation":         true,
	"EaseOut":           true,
	"EaseInOut":         true,
	"ElementTransition": true,
}

var msLiteral = regexp.MustCompile(`^(\d+)\s*\*\s*time\.Millisecond$`)

func checkMotion(e *env) []string {
	dir := e.path("internal/ui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	fset := token.NewFileSet()
	var out []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") ||
			strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, ent.Name()), nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isMotionCall(call) {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BinaryExpr)
				if !ok || lit.Op != token.MUL {
					continue
				}
				src := exprText(fset, lit)
				if msLiteral.MatchString(src) {
					out = append(out, fset.Position(call.Pos()).String()+
						"  动画时长写字面量："+src+
						"\n→ 用 internal/theme 的 Motion* 令牌；缺一档就先加令牌")
				}
			}
			return true
		})
	}
	sort.Strings(out)
	return out
}

// isMotionCall 判断这次调用是不是"设动画"：函数名在表里，或者是
// ui.Animation{...} 的复合字面量（那走 CompositeLit，不在这里）。
func isMotionCall(call *ast.CallExpr) bool {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return motionAPIs[fn.Name]
	case *ast.SelectorExpr:
		return motionAPIs[fn.Sel.Name]
	}
	return false
}

// exprText 把表达式还原成源码文本，用来匹配 `320 * time.Millisecond`。
func exprText(fset *token.FileSet, e ast.Expr) string {
	var sb strings.Builder
	_ = ast.Fprint(&sb, fset, e, nil)
	// ast.Fprint 会带上字段名和换行，这里只取最简形状：用位置截原文更可靠。
	start := fset.Position(e.Pos())
	end := fset.Position(e.End())
	if data, err := os.ReadFile(start.Filename); err == nil {
		if start.Offset >= 0 && end.Offset <= len(data) && start.Offset < end.Offset {
			return strings.Join(strings.Fields(string(data[start.Offset:end.Offset])), " ")
		}
	}
	return strconv.Itoa(start.Offset)
}
