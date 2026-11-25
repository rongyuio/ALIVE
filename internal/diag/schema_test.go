package diag

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// 文档里的事件表必须和代码里的 Ev* 常量完全一致。
//
// 这条测试防的是 7af7c49 那种事故：重构把日志一起删了，文档还在教人 grep
// 一个不会再出现的字段；或者反方向——代码加了一个事件，排障手册里没有，
// 下一个人根本不知道要查它。
func TestDocEventCatalogMatchesCode(t *testing.T) {
	code := codeEvents(t)
	doc := docEvents(t)

	for ev := range doc {
		if !code[ev] {
			t.Errorf("文档承诺了事件 %q，代码里没有对应的 Ev* 常量", ev)
		}
	}
	for ev := range code {
		if !doc[ev] {
			t.Errorf("代码里的事件 %q 没写进 doc/architecture/diagnostics.md 的事件表", ev)
		}
	}
}

func codeEvents(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "diag.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			// Ev + 大写开头；Event 字段常量不算事件。
			if len(name.Name) < 3 || !strings.HasPrefix(name.Name, "Ev") ||
				name.Name[2] < 'A' || name.Name[2] > 'Z' {
				continue
			}
			if i >= len(vs.Values) {
				continue
			}
			lit, ok := vs.Values[i].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			v, err := strconv.Unquote(lit.Value)
			if err != nil {
				continue
			}
			out[v] = true
		}
		return true
	})
	if len(out) == 0 {
		t.Fatal("没从 diag.go 解析出任何事件")
	}
	return out
}

func docEvents(t *testing.T) map[string]bool {
	t.Helper()
	text := docText(t)
	start := strings.Index(text, "### 2.2 事件目录")
	if start < 0 {
		t.Fatal("诊断文档里找不到「### 2.2 事件目录」")
	}
	rest := text[start:]
	end := strings.Index(rest, "**级别约定**")
	if end < 0 {
		t.Fatal("事件表后面找不到「**级别约定**」")
	}
	out := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(rest[:end], -1) {
		out[m[1]] = true
	}
	if len(out) == 0 {
		t.Fatal("事件表里没解析出任何事件")
	}
	return out
}

// 读侧的字段名必须来自 diag 常量，并且写进文档的字段表。
//
// 这条测试防的是「写入端改成 openMs，读侧还在查 open_ms，日志看起来有、
// 查询静默返回空」。字段名一旦是共享常量，重命名会在编译期炸；文档表则由
// 这条测试兜住。
func TestReaderFieldsUseKnownConstants(t *testing.T) {
	consts := fieldConstants(t)
	reader := readerFields(t)
	if len(reader) == 0 {
		t.Fatal("没从 alive-diag 解析出任何字段")
	}
	for name, value := range reader {
		if _, ok := consts[name]; !ok {
			t.Errorf("alive-diag 用了未知的 diag.%s", name)
		}
		if value == "" {
			t.Errorf("diag.%s 没有字符串值", name)
		}
	}
	doc := docFieldTokens(t)
	for name, value := range reader {
		if !doc[value] {
			t.Errorf("读侧字段 diag.%s=%q 没写进 doc/architecture/diagnostics.md 的字段表", name, value)
		}
	}
}

func TestErrKeyConstantsJoin(t *testing.T) {
	pairs := []struct {
		key, group, field string
	}{
		{ErrClassKey, ErrField, ErrClassField},
		{ErrTextKey, ErrField, ErrTextField},
		{ErrOpKey, ErrField, ErrOpField},
		{ErrStatusKey, ErrField, ErrStatusField},
		{ErrHostKey, ErrField, ErrHostField},
		{ErrPlatformKey, ErrField, ErrPlatformField},
		{ErrTypeKey, ErrField, ErrTypeField},
	}
	for _, p := range pairs {
		if want := p.group + "." + p.field; p.key != want {
			t.Errorf("组合键 %q != %q", p.key, want)
		}
	}
}

func fieldConstants(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "diag.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		gd, ok := n.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			return true
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				v, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				out[name.Name] = v
			}
		}
		return false
	})
	if len(out) == 0 {
		t.Fatal("没从 diag.go 解析出字段常量")
	}
	return out
}

// readerFields 返回 alive-diag 里 e.str(diag.X) 的 X，并拒绝字符串字面量。
func readerFields(t *testing.T) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../cmd/alive-diag/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	consts := fieldConstants(t)
	out := map[string]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "str" || len(call.Args) != 1 {
			return true
		}
		switch arg := call.Args[0].(type) {
		case *ast.BasicLit:
			if arg.Kind == token.STRING {
				t.Errorf("cmd/alive-diag/main.go:%d e.str 用了字符串字面量 %s，必须换成 diag 常量",
					fset.Position(call.Pos()).Line, arg.Value)
			}
		case *ast.SelectorExpr:
			if pkg, ok := arg.X.(*ast.Ident); ok && pkg.Name == "diag" {
				out[arg.Sel.Name] = consts[arg.Sel.Name]
			}
		}
		return true
	})
	return out
}

func docFieldTokens(t *testing.T) map[string]bool {
	t.Helper()
	text := docText(t)
	start := strings.Index(text, "### 2.1 字段")
	if start < 0 {
		t.Fatal("诊断文档里找不到「### 2.1 字段」")
	}
	rest := text[start:]
	end := strings.Index(rest, "### 2.2 事件目录")
	if end < 0 {
		t.Fatal("字段表后面找不到「### 2.2 事件目录」")
	}
	out := map[string]bool{}
	for _, m := range backtickRe.FindAllStringSubmatch(rest[:end], -1) {
		out[m[1]] = true
	}
	return out
}

// backtickRe 抓文档里 `xxx` 形式的事件/字段名；\x60 是反引号，避免在
// Go 原始字符串里再嵌套反引号。
var backtickRe = regexp.MustCompile(`\x60([a-z][a-z0-9_]*(?:\.[a-z0-9_]+)*)\x60`)

func docText(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../doc/architecture/diagnostics.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
