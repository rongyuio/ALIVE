// Package main 实现「症状账本」：把 git 历史折成 doc/STATE.md 一页。
//
// 这个文件只做一件事：把 git log 读成内存里的 []Commit，一行假数据都不掺。
package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"alive/internal/churn"
	"time"
)

// Commit 是一次提交的只读视图。
//
// Time 取 committer 时间并保留作者时区：窗口算术按绝对时间做（与 git log --since 同一个口径），
// 展示用 Time.Format，因此表里的钟点就是提交里那个钟点，不会被本机时区改写。
type Commit struct {
	Hash    string
	Time    time.Time
	Subject string
	Type    string // "fix" / "feat" / ...；不符合 "type: 描述" 的留空
	Files   []string
}

// Desc 是「type: 」之后的正文。表格里已经有缩略 hash，再带一次 type 只是噪音。
func (c Commit) Desc() string {
	if i := strings.Index(c.Subject, ":"); i >= 0 {
		return strings.TrimSpace(c.Subject[i+1:])
	}
	return c.Subject
}

var typePrefix = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?!?:\s*`)

func parseSubjectType(subject string) string {
	if m := typePrefix.FindStringSubmatch(subject); m != nil {
		return m[1]
	}
	return ""
}

// Repo 是整条历史，新 → 旧。
type Repo struct {
	Root    string
	Commits []Commit
}

// Anchor 是 HEAD 的 committer 时间，也是所有窗口的右端点。
// 用它而不是 time.Now()，是为了让 STATE.md 只随提交变化而变：没有新提交，重跑生成的 diff 是空的。
func (r *Repo) Anchor() time.Time { return r.Commits[0].Time }

// Head 是 HEAD 的 hash。
func (r *Repo) Head() string { return r.Commits[0].Hash }

// FixCount 数类型正好是 fix 的提交。
func (r *Repo) FixCount() int {
	n := 0
	for _, c := range r.Commits {
		if c.Type == "fix" {
			n++
		}
	}
	return n
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.Bytes(), nil
}

// FindRoot 从 dir 往上找仓库根。读历史只依赖 git，不依赖当前目录在哪儿。
func FindRoot(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("找不到 git 仓库（或没装 git）：%w", err)
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", fmt.Errorf("%s 不在 git 仓库里", dir)
	}
	return root, nil
}

const (
	recordMark = "\x01"
	fieldMark  = "\x1f"
	// 记录分隔符放行首：\x01 开头的行 = 一次提交，其余非空行 = 它改的文件。
	// 用 \x01 而不是空行，是因为 --name-only 的文件块之间本来就有空行，空行当分隔符分不清记录边界。
	recordFmt = "%x01%H%x1f%cd%x1f%s"
)

// LoadRepo 读全量历史（一次性读进内存，本仓库 421 次提交，不值得为此分页或按窗口二次调用）。
func LoadRepo(root string) (*Repo, error) {
	out, err := gitOutput(root, "log", "HEAD", "--date=iso-strict", "--format="+recordFmt, "--name-only")
	if err != nil {
		return nil, err
	}
	commits, err := parseLog(out)
	if err != nil {
		return nil, err
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("%s 的 git 历史是空的", root)
	}
	return &Repo{Root: root, Commits: commits}, nil
}

func parseLog(out []byte) ([]Commit, error) {
	var commits []Commit
	cur := -1
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if strings.HasPrefix(line, recordMark) {
			fields := strings.Split(strings.TrimPrefix(line, recordMark), fieldMark)
			if len(fields) != 3 {
				return nil, fmt.Errorf("git log 输出无法解析（%d 个字段）：%q", len(fields), line)
			}
			at, err := time.Parse(time.RFC3339, fields[1])
			if err != nil {
				return nil, fmt.Errorf("提交 %s 的时间无法解析：%w", fields[0], err)
			}
			commits = append(commits, Commit{
				Hash:    fields[0],
				Time:    at,
				Subject: fields[2],
				Type:    parseSubjectType(fields[2]),
			})
			cur = len(commits) - 1
			continue
		}
		if cur < 0 {
			continue
		}
		if p := strings.TrimSpace(line); p != "" {
			commits[cur].Files = append(commits[cur].Files, p)
		}
	}
	return commits, nil
}

// Window 是「最近 N 天」，两端都含。
type Window struct {
	Since  time.Time
	Anchor time.Time
	Days   int
}

func NewWindow(anchor time.Time, days int) Window {
	return Window{Since: anchor.Add(-time.Duration(days) * 24 * time.Hour), Anchor: anchor, Days: days}
}

// Contains 只看右端点：git log 默认顺序是 committer 时间倒序，锚点之后不会有更早的历史。
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Since) && !t.After(w.Anchor)
}

// FileStat 是一个文件在历史和窗口里的账。
//
// Gate 系列是「`make gate` 的 churn 真会看到什么」的口径：
// 跳过一次动 >8 个文件的横切提交、跳过文档；见 cmd/alive-gate/checks.go 的 checkChurn。
// 两套数并列，是为了让这一页的结论和门禁的结论不会互相打脸。
type FileStat struct {
	Path         string
	Changes      int // 窗口内改动次数（所有 type）
	Fixes        int // 窗口内 fix: 次数
	GateFixes    int // 窗口内 fix: 次数，按 gate 的 churn 口径
	TotalChanges int
	TotalFixes   int
	Last         time.Time
	LastFix      time.Time
	// LastFixFiles 是最后一次 fix 那个提交一共动了多少文件。用来解释一件事：
	// 它可能因为动了太多文件（横切提交）被 churn 剔掉，于是「今天还在动」和
	// 「gate 眼里它冷却了」同时成立——不解释清楚，这一页就自相矛盾。
	LastFixFiles int
	GateLastFix  time.Time
}

// ApplyChurn 用 internal/churn 填 GateFixes / GateLastFix。
//
// 这两个字段以前由这里自己算（>8 文件剔除、doc 豁免），数字抄自
// cmd/alive-gate —— 两处真相。现在两边都调 internal/churn：一条规则一个实现，
// 「门禁会红」和「这一页说第几次」永远给同一个数。
//
// anchor 与 windowDays 必须和本页「窗内」那一列用同一套：`-now` / `-days`
// 是回看开关，如果 churn 口径恒用 HEAD + 7 天，同一行就会同时出现「窗内 24 /
// churn 27」，而且「最后 fix」可能晚于窗口右端。同一个窗口，一个实现。
func ApplyChurn(root string, anchor int64, windowDays int, stats []*FileStat) error {
	res, err := churn.Scan(root, churn.Options{Anchor: anchor, WindowDays: windowDays})
	if err != nil {
		return err
	}
	for _, st := range stats {
		f := res.Files[st.Path]
		if f == nil {
			continue
		}
		st.GateFixes = f.Count
		if f.Last > 0 {
			st.GateLastFix = time.Unix(f.Last, 0)
		}
	}
	return nil
}

// FileStats 按文件记账。commits 必须新 → 旧：Last / LastFix 取第一次出现的那个。
func FileStats(commits []Commit, w Window) []*FileStat {
	byPath := map[string]*FileStat{}
	var order []*FileStat
	for _, c := range commits {
		in := w.Contains(c.Time)
		isFix := c.Type == "fix"
		seen := map[string]bool{}
		for _, p := range c.Files {
			if seen[p] {
				continue
			}
			seen[p] = true
			st := byPath[p]
			if st == nil {
				st = &FileStat{Path: p}
				byPath[p] = st
				order = append(order, st)
			}
			st.TotalChanges++
			if st.Last.IsZero() {
				st.Last = c.Time
			}
			if isFix {
				st.TotalFixes++
				if st.LastFix.IsZero() {
					st.LastFix = c.Time
					st.LastFixFiles = len(c.Files)
				}
			}
			if in {
				st.Changes++
				if isFix {
					st.Fixes++
				}
			}
			// GateFixes / GateLastFix 由 ApplyChurn 统一填：口径只能有一处
			// （internal/churn），和 make gate 的 churn 检查共用同一个实现。
		}
	}
	return order
}
