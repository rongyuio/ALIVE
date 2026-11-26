package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"alive/internal/churn"
)

func runGit(root string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// checkChurn：同一个文件 7 天内第 3 次 fix。
//
// 算法在 internal/churn —— 它和 cmd/alive-symptoms 的现状简报必须给出同一个
// 数字：一个是「门禁现在会不会红」，一个是「这一页说它第几次」。两边各写一遍
// 就会漂，而这两处恰恰是同一件事的两种说法。
//
// 只有「最近还在动」的才拦：规则落地那一刻历史里已经有一堆超阈值的文件，
// 让它们全部变红等于让门禁立刻失去意义。
func checkChurn(e *env) []string {
	opt := churn.Options{WindowDays: 7, HotHours: 24, CrossMax: 8}
	res, err := churn.Scan(e.root, opt)
	if err != nil || len(res.Files) == 0 {
		return nil // 浅克隆，或者还没有历史可查
	}

	var out []string
	for _, f := range res.Sorted() {
		st := res.Files[f]
		if st.Count < 3 || !res.Hot(st, opt) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s  %d 次 fix / 7 天，最后一次在 24 小时内（阈值 3）\n", f, st.Count)
		for _, h := range st.Fixes {
			fmt.Fprintf(&b, "  %s\n", h)
		}
		b.WriteString("→ 停手，先把这个文件的写者收成一个，再谈行为")
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

// checkCommits：一次提交只做一件事。
//
// 判据是文件数，不是行数：跨 30 个文件的 fix 几乎必然是几件事塞在一起
// （5b129a9 那次把表面过渡、起播音量、列表续取合成一个提交）。body 里
// 写明为什么拆不开就放过——这条要拦的是「没想过」，不是「真拆不开」。
//
// 只看**还没推送的**提交：历史已经定格，门禁管的是下一次。
func checkCommits(e *env) []string {
	const limit = 3

	// 有上游时看未推送的；没有上游（本地仓、这个仓库的 main 就是这样）就看
	// 最近 10 个提交。用 -10 而不是 `HEAD~10..HEAD`：后者在提交数不足十个的
	// 仓库里直接报错，门禁会在最小仓库上静默失效。
	args := []string{"log", "-10", "--pretty=format:@@%h|%s|%b", "--name-only"}
	if strings.TrimSpace(e.git("rev-parse", "--abbrev-ref", "@{u}")) != "" {
		args = []string{"log", "@{u}..HEAD", "-10", "--pretty=format:@@%h|%s|%b", "--name-only"}
	}
	raw := e.git(args...)
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	var out []string
	for _, block := range strings.Split(raw, "@@") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		lines := strings.Split(block, "\n")
		head := strings.SplitN(lines[0], "|", 3)
		if len(head) < 2 {
			continue
		}
		hash, subject := head[0], head[1]
		body := ""
		if len(head) == 3 {
			body = head[2]
		}
		if !strings.HasPrefix(subject, "fix:") {
			continue
		}
		var files []string
		for _, l := range lines[1:] {
			if l = strings.TrimSpace(l); l != "" {
				files = append(files, l)
			}
		}
		if len(files) <= limit {
			continue
		}
		if hasReason(body) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s —— %d 个文件（上限 %d，未写理由）\n", hash, subject, len(files), limit)
		for i, f := range files {
			if i == 6 {
				fmt.Fprintf(&b, "  …另有 %d 个\n", len(files)-i)
				break
			}
			fmt.Fprintf(&b, "  %s\n", f)
		}
		b.WriteString("→ 拆开；确实拆不开就在 body 里写「拆不开：<为什么>」")
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

func hasReason(body string) bool {
	for _, k := range []string{"拆不开", "同一件事", "同一个理由", "不可分割"} {
		if strings.Contains(body, k) {
			return true
		}
	}
	return false
}

// checkEvaluator：同一个概念只能有一个求值点。
//
// 这是四个 TestNoSecond* 守卫的同族扩展。那四条钉的是「写入口」，实测覆盖的
// 事实零返工；没钉住的「画面归属」被改了 5 次。区别在于：写入口可枚举
// （赋值语句），求值点也可枚举（函数名 + 分支数）。
//
// 判据：同前缀的函数族里，含 ≥2 个条件分支的不得超过一个——其余必须是
// 只做委托的包装。pictureFromIsland 曾经自己算了一遍，注释还写着「只差一条」。
func checkEvaluator(e *env) []string {
	families := map[string]string{
		"picture": "画面归属",
	}

	dir := e.path("internal/ui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type fn struct {
		file  string
		name  string
		score int
	}
	seen := map[string][]fn{}

	fset := token.NewFileSet()
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") ||
			strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, ent.Name()), nil, 0)
		if err != nil {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Recv == nil {
				continue
			}
			for prefix := range families {
				if !strings.HasPrefix(fd.Name.Name, prefix) {
					continue
				}
				score := 0
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					switch n.(type) {
					case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt:
						score++
					}
					return true
				})
				seen[prefix] = append(seen[prefix], fn{
					file: ent.Name(), name: fd.Name.Name, score: score,
				})
			}
		}
	}

	var out []string
	for prefix, list := range seen {
		var evaluators []fn
		for _, f := range list {
			if f.score >= 2 {
				evaluators = append(evaluators, f)
			}
		}
		if len(evaluators) <= 1 {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s 有 %d 个求值点（只能有一个）：\n", families[prefix], len(evaluators))
		for _, f := range evaluators {
			fmt.Fprintf(&b, "  %s %s（%d 个分支）\n", f.file, f.name, f.score)
		}
		b.WriteString("→ 留一个当权威，其余改成只做委托的包装")
		out = append(out, b.String())
	}
	sort.Strings(out)
	return out
}

// checkClaims：说「没有」必须举证。
//
// 这一条来自四次代价明确的误判：说「MyGo 画不了网页」（有 WebView）、
// 「没有 cookie 读取能力」（有 mygo.Cookies）、「页面只能看不能操作」
// （有 Page.Eval）、不知道 NewTester 存在（自建了 8 个 ALIVE_* 开关）。
//
// 判据刻意窄：只查「反引号标识符 + 否定词」这种写进文档的能力断言。
// 自然语言里的「无法避免」不算。举证形式：同一行或上下两行里有文件路径或
// 第二处反引号代码（比如 `third_party/mygo/ui/webview.go`）。
func checkClaims(e *env) []string {
	// 只收「断言某个东西不存在」的说法。「没有实现」「未实现」故意不在表里：
	// 那是自陈（我们没做），不是能力断言（它做不到），两者举证方式不同。
	denials := []string{"不支持", "做不到", "无法做到", "不具备"}
	codeRe := regexp.MustCompile("`[^`]+`")
	pathRe := regexp.MustCompile("[\"`]?[A-Za-z0-9_./-]+\\.(go|md|json|h)[\"`]?|third_party/[A-Za-z0-9_./-]+")

	var out []string
	_ = filepath.Walk(e.path("doc"), func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(e.root, p)
		rel = filepath.ToSlash(rel)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !containsAny(line, denials) {
				continue
			}
			if len(codeRe.FindAllString(line, -1)) < 1 {
				continue // 没提到具体东西，不是能力断言
			}
			// 举证窗口：本行 + 上下两行
			from, to := i-2, i+2
			if from < 0 {
				from = 0
			}
			if to >= len(lines) {
				to = len(lines) - 1
			}
			window := strings.Join(lines[from:to+1], "\n")
			if pathRe.MatchString(window) || len(codeRe.FindAllString(window, -1)) >= 2 {
				continue
			}
			out = append(out, fmt.Sprintf("%s:%d  %s\n→ 指出处（文件路径或第二处代码引用），或改写成「我没查」", rel, i+1, strings.TrimSpace(line)))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// checkProbes：探针不进仓库。
//
// AGENTS.md §4.9 写的是「放 .tmp/probes/，.gitignore 挡着」。.gitignore 只能
// 挡新增，挡不住「已经 git add -f 过」或「改名前就在仓库里」。
func checkProbes(e *env) []string {
	raw := e.git("ls-files")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, f := range strings.Split(raw, "\n") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		base := filepath.Base(f)
		if strings.HasSuffix(base, "_probe_test.go") || strings.HasPrefix(base, "zz_") {
			out = append(out, fmt.Sprintf("%s\n→ git rm --cached，探针放 .tmp/probes/", f))
		}
	}
	sort.Strings(out)
	return out
}

// checkFacts：文档里的数字必须能重算。
//
// 写进 AGENTS.md 的那几个统计（多少处 ui.Box、多少处 c.Key）在 36 小时内就
// 变成了旧值。数字写在散文里必然腐；能被一条命令重算的才许写。
func checkFacts(e *env) []string {
	type rule struct {
		file  string
		re    *regexp.Regexp
		count func() int
		what  string
	}
	rules := []rule{
		{
			file: "AGENTS.md",
			re:   regexp.MustCompile("(\\d+) 处 `ui\\.Box`"),
			count: func() int {
				return countInFiles(e.path("internal/ui"), "ui.Box(")
			},
			what: "internal/ui 里 ui.Box( 的出现次数",
		},
		{
			file: "AGENTS.md",
			re:   regexp.MustCompile("(\\d+) 处 `ui\\.Button`"),
			count: func() int {
				return countInFiles(e.path("internal/ui"), "ui.Button(")
			},
			what: "internal/ui 里 ui.Button( 的出现次数",
		},
		{
			file: "AGENTS.md",
			re:   regexp.MustCompile("现在 (\\d+) 处.*`c\\.Key\\(key\\)`"),
			count: func() int {
				return countInFiles(e.path("internal/ui"), "c.Key(")
			},
			what: "internal/ui 里 c.Key( 的出现次数",
		},
	}

	var out []string
	for _, r := range rules {
		data, err := os.ReadFile(e.path(r.file))
		if err != nil {
			continue
		}
		m := r.re.FindStringSubmatch(string(data))
		if m == nil {
			continue // 那句话已经不在了，没什么可核的
		}
		want, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		got := r.count()
		if got != want {
			out = append(out, fmt.Sprintf("%s 写的是 %d，实际 %d（%s）\n→ 改数字，或者把这句话删掉",
				r.file, want, got, r.what))
		}
	}
	return out
}

// countInFiles 数 dir 下非测试 .go 文件里 needle 出现多少次。
func countInFiles(dir, needle string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".go") ||
			strings.HasSuffix(ent.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			continue
		}
		n += strings.Count(string(data), needle)
	}
	return n
}
