// alive-gate 检查工程契约里能自动检查的那几条。
//
// 为什么要有它：AGENTS.md 写了 262 行规则，实测真正生效的只有带检查器的那几条
// （四个 TestNoSecond*、label_test、container_test），其余靠自觉，一条没拦住。
// 这个命令把「能检查但还没检查」的那些补上，并且**不依赖任何人的记性**——
// 什么时候跑由 make / CI 决定，不由当时在做什么决定。
//
//	alive-gate            跑全部检查
//	alive-gate churn      只跑某一条
//	alive-gate -list      列出所有检查
//
// 退出码非 0 表示有检查失败。豁免写在 .gate-allow，每行一条，必须带理由。
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// check 是一条检查。run 返回失败项（空表示通过）。
type check struct {
	name string
	why  string // 这条检查守的是 AGENTS.md 的哪一句
	run  func(*env) []string
}

var checks = []check{
	{
		name: "churn",
		why:  "§3.7 同一个文件 7 天内第三次 fix → 停手",
		run:  checkChurn,
	},
	{
		name: "commits",
		why:  "§8 一次提交只做一件事",
		run:  checkCommits,
	},
	{
		name: "claims",
		why:  "§1 不许凭印象说「没有」(说不支持/做不到必须举证)",
		run:  checkClaims,
	},
	{
		name: "evaluator",
		why:  "§3.7 一个事实一个写者(同一个判断只能有一个求值点)",
		run:  checkEvaluator,
	},
	{
		name: "facts",
		why:  "§9 文档里的数字必须能重算",
		run:  checkFacts,
	},
	{
		name: "probes",
		why:  "§4.9 探针不进仓库",
		run:  checkProbes,
	},
	{
		name: "motion",
		why:  "§3.10 动画时长只有一个来源（internal/theme 的 Motion*）",
		run:  checkMotion,
	},
}

func main() {
	list := flag.Bool("list", false, "列出所有检查")
	root := flag.String("root", ".", "仓库根目录")
	flag.Parse()

	if *list {
		for _, c := range checks {
			fmt.Printf("%-10s %s\n", c.name, c.why)
		}
		return
	}

	e := newEnv(*root)
	allow := e.loadAllow()
	if allow == nil {
		return // 读失败已经报过
	}

	selected := checks
	if flag.NArg() > 0 {
		byName := map[string]check{}
		for _, c := range checks {
			byName[c.name] = c
		}
		selected = nil
		for _, name := range flag.Args() {
			c, ok := byName[name]
			if !ok {
				fmt.Fprintf(os.Stderr, "alive-gate: 没有名为 %q 的检查\n", name)
				os.Exit(2)
			}
			selected = append(selected, c)
		}
	}

	failed := 0
	for _, c := range selected {
		problems := c.run(e)
		problems = allow.filter(c.name, problems)
		if len(problems) == 0 {
			fmt.Printf("ok    %-10s %s\n", c.name, c.why)
			continue
		}
		failed++
		fmt.Printf("FAIL  %-10s %s\n", c.name, c.why)
		for _, p := range problems {
			for _, line := range strings.Split(strings.TrimRight(p, "\n"), "\n") {
				fmt.Printf("        %s\n", line)
			}
		}
		fmt.Println()
	}

	if failed > 0 {
		fmt.Fprintf(os.Stderr, "alive-gate: %d 项未通过。豁免写 .gate-allow，每条必须带理由。\n", failed)
		os.Exit(1)
	}
}

// allowList 是 .gate-allow：已经知道、暂时不改的失败项。
//
// 存在的理由：规则落地那一刻，历史里已经有一堆违反。让它们全部变红等于让
// 门禁立刻失去意义——没人会去看一个永远红的灯。豁免必须带理由，而且只按
// 「检查名 + 目标」精确匹配，避免变成「关掉这条检查」。
type allowList struct {
	byCheck map[string][]string
}

func (a *allowList) filter(checkName string, problems []string) []string {
	patterns := a.byCheck[checkName]
	if len(patterns) == 0 {
		return problems
	}
	var out []string
	for _, p := range problems {
		// 只比第一行：那是「什么 + 现在多少」的摘要，后面几行是证据。
		// 空白压成一个，免得豁免文件对空格数量敏感。
		head := squeeze(firstLine(p))
		kept := true
		for _, pat := range patterns {
			if strings.Contains(head, squeeze(pat)) {
				kept = false
				break
			}
		}
		if kept {
			out = append(out, p)
		}
	}
	return out
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// squeeze 把连续空白压成一个空格并去掉首尾，豁免用的是「摘要」而不是
// 逐字节相等的原文——对齐、缩进一变就不该让豁免失效。
func squeeze(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func (e *env) loadAllow() *allowList {
	a := &allowList{byCheck: map[string][]string{}}
	data, err := os.ReadFile(e.path(".gate-allow"))
	if err != nil {
		if os.IsNotExist(err) {
			return a
		}
		fmt.Fprintf(os.Stderr, "alive-gate: 读 .gate-allow 失败: %v\n", err)
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 格式：<check> <target>  # 理由
		if i := strings.Index(line, "#"); i >= 0 {
			if strings.TrimSpace(line[i+1:]) == "" {
				fmt.Fprintf(os.Stderr, "alive-gate: 豁免缺理由: %s\n", line)
				continue
			}
			line = strings.TrimSpace(line[:i])
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			fmt.Fprintf(os.Stderr, "alive-gate: 豁免格式不对: %q\n", line)
			continue
		}
		a.byCheck[f[0]] = append(a.byCheck[f[0]], strings.Join(f[1:], " "))
	}
	return a
}

// env 是各检查共用的环境：git 输出缓存、根目录。
type env struct {
	root     string
	gitCache map[string]string
}

func newEnv(root string) *env {
	return &env{root: root, gitCache: map[string]string{}}
}

func (e *env) path(rel string) string {
	if rel == "" {
		return e.root
	}
	return strings.TrimRight(e.root, `/\`) + string(os.PathSeparator) + rel
}

// git 跑一条 git 命令，结果按参数缓存（一次 gate 会重复问同样的历史）。
func (e *env) git(args ...string) string {
	key := strings.Join(args, "\x00")
	if v, ok := e.gitCache[key]; ok {
		return v
	}
	out := runGit(e.root, args...)
	e.gitCache[key] = out
	return out
}
