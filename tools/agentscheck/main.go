// Command agentscheck 校验 AGENTS.md 里能被机器证明的那部分：
//
//  1. 正文里出现的每个 `make <target>` 都真的在 Makefile 里有；
//  2. 每个反引号里的路径都存在，每个 `ui.X`/`mygo.X` 之类都是钉住版本里真实的导出名；
//  3. §出处 那张表：右栏 file:line「原话」必须真的落在那几行，左栏那句话必须仍然出现在
//     正文里——表和正文不可能走散。
//
// doc/reference/mygo.md 与 doc/known-debt.md 只验路径与行号（账本会随代码漂，行号越界就报）。
//
// 用法（仓库根）：go run ./tools/agentscheck
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

func main() {
	agents := flag.String("agents", "AGENTS.md", "工程契约")
	docs := flag.String("docs", "doc/reference/mygo.md,doc/known-debt.md", "跟着一起验的文档")
	root := flag.String("root", ".", "仓库根")
	mygo := flag.String("mygo", "third_party/mygo", "钉住的 MyGo 源码（相对 root）")
	flag.Parse()

	c := &checker{root: *root, mygo: filepath.Join(*root, *mygo)}
	var problems []string

	raw, err := os.ReadFile(filepath.Join(*root, *agents))
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	text := string(raw)
	problems = append(problems, c.checkCommands(text)...)
	problems = append(problems, c.checkPathsAndSymbols(text)...)
	problems = append(problems, c.checkAppendix(text)...)

	for _, d := range strings.Split(*docs, ",") {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(*root, d))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		problems = append(problems, c.checkPathsAndSymbols(string(raw))...)
	}

	if len(problems) > 0 {
		fmt.Printf("%d 处不通过：\n", len(problems))
		for _, p := range problems {
			fmt.Println("  -", p)
		}
		os.Exit(1)
	}
	fmt.Printf("agents-check ok：%d 条规矩有出处、%d 个引用立得住、%d 个路径/符号存在、%d 个 make 目标真实\n",
		c.rules, c.cites, c.paths, c.makes)
}

type checker struct {
	root string
	mygo string

	rules int
	cites int
	paths int
	makes int
	syms  map[string]map[string]bool // 包名 → 导出的名字
}

var (
	// 出处列：`path:12-13「原话」`（缩写或完整路径都行）
	citeRe  = regexp.MustCompile(`([A-Za-z0-9_./\-]+\.(?:md|go)|Makefile|\.gitignore):(\d+)(?:-(\d+))?「([^」]*)」`)
	aliasRe = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*`([^`]+)`\\s*\\|\\s*$")
	tickRe  = regexp.MustCompile("`([^`]+)`")
	symRe   = regexp.MustCompile(`^([a-z][a-z0-9]*)\.([A-Z][A-Za-z0-9_]*)$`)
	lineRe  = regexp.MustCompile(`:(\d+)(?:[,-](\d+))?$`)
	makeRe  = regexp.MustCompile(`\bmake ([a-z][a-z0-9-]*)`)
)

// 本机跑起来才有、不入库的目录：存在才查，不存在不算错。
var runtimeDirs = []string{".tmp/", ".mygo/", "build/"}

// checkCommands：正文提到的 make 目标都得在 Makefile 里。
func (c *checker) checkCommands(text string) []string {
	raw, err := os.ReadFile(filepath.Join(c.root, "Makefile"))
	if err != nil {
		return []string{"读不到 Makefile：" + err.Error()}
	}
	mk := string(raw)
	var problems []string
	seen := map[string]bool{}
	for _, m := range makeRe.FindAllStringSubmatch(text, -1) {
		t := m[1]
		if seen[t] {
			continue
		}
		seen[t] = true
		c.makes++
		if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(t) + `:|\b` + regexp.QuoteMeta(t) + `\b`).MatchString(mk) {
			problems = append(problems, "make 目标不存在："+t)
		}
	}
	return problems
}

// checkPathsAndSymbols：反引号里的项目路径要存在（顺带查 `path:NN` 的行号没越界），
// `pkg.Exported` 要是真导出名。代码块里的不算——那里是示例。
func (c *checker) checkPathsAndSymbols(text string) []string {
	var problems []string
	aliases := parseAliases(strings.Split(text, "\n"))
	for _, m := range tickRe.FindAllStringSubmatch(stripFences(text), -1) {
		tok := strings.TrimSpace(m[1])
		if tok == "" || strings.ContainsAny(tok, " =<>…%\"'「」") || strings.HasPrefix(tok, "http") {
			continue
		}
		if p := tok; strings.Contains(p, "/") && !strings.ContainsAny(p, "*\\") {
			ref, line := p, 0
			if loc := lineRe.FindStringSubmatch(p); loc != nil {
				ref = lineRe.ReplaceAllString(p, "")
				line, _ = strconv.Atoi(loc[1])
			}
			if !projectPath(ref) {
				continue // Go import 路径、别的仓库路径，不归这里管
			}
			if isRuntime(ref) && !exists(filepath.Join(c.root, filepath.FromSlash(ref))) {
				continue
			}
			c.paths++
			target, ok := c.resolvePath(ref, aliases)
			if !ok {
				problems = append(problems, "路径不存在："+ref)
				continue
			}
			if line > 0 {
				if src, err := os.ReadFile(target); err == nil {
					if n := strings.Count(string(src), "\n") + 1; line > n {
						problems = append(problems, fmt.Sprintf("行号越界：%s 只有 %d 行", ref, n))
					}
				}
			}
			continue
		}
		sym := strings.TrimLeft(tok, "*")
		sm := symRe.FindStringSubmatch(sym)
		if sm == nil {
			continue
		}
		c.paths++
		ok, err := c.hasSymbol(sm[1], sm[2])
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if !ok {
			problems = append(problems, fmt.Sprintf("符号不存在：%s（包 %s 里没有导出 %s）", tok, sm[1], sm[2]))
		}
	}
	return problems
}

// checkAppendix：§出处 那张表。右栏的引用要立得住，左栏那句要还在正文里。
func (c *checker) checkAppendix(text string) []string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "## 出处" {
			start = i
			break
		}
	}
	if start < 0 {
		return []string{"找不到 `## 出处` 那一节"}
	}
	body := norm(strings.Join(lines[:start], " "))
	aliases := parseAliases(lines)
	var problems []string
	for _, l := range lines[start:] {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "|") || strings.HasPrefix(t, "|---") {
			continue
		}
		cells := strings.Split(t, "|")
		if len(cells) < 3 {
			continue
		}
		left, right := cells[1], cells[2]
		if strings.Contains(left, "正文里的规矩") {
			continue // 表头
		}
		if !strings.Contains(body, norm(left)) {
			problems = append(problems, fmt.Sprintf("出处表的左栏在正文里找不到：%q（表和正文走散了）", strings.TrimSpace(left)))
			continue
		}
		c.rules++
		found := false
		for _, m := range citeRe.FindAllStringSubmatch(right, -1) {
			ref, from, to, quote := m[1], m[2], m[3], m[4]
			target, ok := c.resolvePath(ref, aliases)
			if !ok {
				problems = append(problems, "出处表的文件不存在："+ref)
				continue
			}
			src, err := os.ReadFile(target)
			if err != nil {
				problems = append(problems, "读不了 "+target)
				continue
			}
			srcLines := strings.Split(string(src), "\n")
			a, _ := strconv.Atoi(from)
			b := a
			if to != "" {
				b, _ = strconv.Atoi(to)
			}
			if a < 1 || b < a || b > len(srcLines) {
				problems = append(problems, fmt.Sprintf("出处行号越界：%s:%s-%s（该文件 %d 行）", ref, from, to, len(srcLines)))
				continue
			}
			if !strings.Contains(norm(strings.Join(srcLines[a-1:b], " ")), norm(quote)) {
				problems = append(problems, fmt.Sprintf("出处对不上：%s:%s-%s 里没有 %q", ref, from, to, quote))
				continue
			}
			c.cites++
			found = true
		}
		if !found {
			problems = append(problems, "出处表的这一行没有可验的 `file:line「原话」`："+strings.TrimSpace(left))
		}
	}
	return problems
}

func parseAliases(lines []string) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		if g := aliasRe.FindStringSubmatch(strings.TrimSpace(l)); g != nil && strings.HasSuffix(g[1], ".md") {
			m[g[1]] = g[2]
		}
	}
	return m
}

// resolvePath 先按仓库根，再按 third_party/mygo（文中 `docs/ui/...` 那类写法是相对 MyGo 的）。
func (c *checker) resolvePath(p string, aliases map[string]string) (string, bool) {
	if full, ok := aliases[p]; ok {
		p = full
	}
	p = filepath.FromSlash(p)
	for _, base := range []string{c.root, c.mygo} {
		if cand := filepath.Join(base, p); exists(cand) {
			return cand, true
		}
	}
	return "", false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isRuntime(p string) bool {
	q := filepath.ToSlash(p)
	for _, d := range runtimeDirs {
		if strings.HasPrefix(q, d) {
			return true
		}
	}
	return false
}

// projectPath 只对仓库自己的路径负责。第一段不在名单里的是 import 路径或别人的路径。
func projectPath(p string) bool {
	switch strings.SplitN(filepath.ToSlash(p), "/", 2)[0] {
	case "internal", "third_party", "doc", "docs", "tools", "locales", "resources", "build",
		"cmd", ".agents", ".github", ".githooks", ".tmp", ".mygo":
		return true
	}
	return false
}

func (c *checker) hasSymbol(pkg, name string) (bool, error) {
	if c.syms == nil {
		c.syms = map[string]map[string]bool{}
	}
	if set, ok := c.syms[pkg]; ok {
		return set[name], nil
	}
	dirs := map[string]string{
		"mygo": c.mygo, "ui": filepath.Join(c.mygo, "ui"), "i18n": filepath.Join(c.mygo, "i18n"),
		"transfer": filepath.Join(c.mygo, "transfer"), "audio": filepath.Join(c.mygo, "audio"),
		"media": filepath.Join(c.mygo, "media"), "scene": filepath.Join(c.mygo, "scene"),
		"ffi": filepath.Join(c.mygo, "ffi"), "video": filepath.Join(c.mygo, "plugins", "video"),
		"glass": filepath.Join(c.mygo, "plugins", "glass"), "updater": filepath.Join(c.mygo, "plugins", "updater"),
	}
	dir, known := dirs[pkg]
	if !known {
		return true, nil // 不认识的包前缀不归这里管
	}
	set, err := exportedNames(dir)
	if err != nil {
		return false, err
	}
	c.syms[pkg] = set
	return set[name], nil
}

// exportedNames 用 AST 扫一个包顶层导出的类型/函数/变量/常量：快，且不要求包能构建。
func exportedNames(dir string) (map[string]bool, error) {
	set := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.FuncDecl:
				if v.Recv == nil && v.Name.IsExported() {
					set[v.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, s := range v.Specs {
					switch sp := s.(type) {
					case *ast.TypeSpec:
						if sp.Name.IsExported() {
							set[sp.Name.Name] = true
						}
					case *ast.ValueSpec:
						for _, n := range sp.Names {
							if n.IsExported() {
								set[n.Name] = true
							}
						}
					}
				}
			}
		}
	}
	return set, nil
}

// stripFences 去掉 ``` 围起来的代码块：那里是示例命令，不是路径声明。
func stripFences(s string) string {
	var b strings.Builder
	in := false
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "```") {
			in = !in
			continue
		}
		if !in {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// norm 去掉反引号、星号、各种引号和所有空白——跨行折行的同一句话（尤其中文）也能对上。
func norm(s string) string {
	r := strings.NewReplacer("`", "", "*", "", "\u2019", "'", "\u2018", "'", "\u201c", "\"", "\u201d", "\"")
	return strings.Map(func(c rune) rune {
		if unicode.IsSpace(c) {
			return -1
		}
		return c
	}, r.Replace(s))
}
