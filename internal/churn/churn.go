// Package churn 是「同一个文件在窗口内被修了几次」这件事的唯一算法。
//
// 为什么要有这个包：它原来在两个地方各写了一遍——`cmd/alive-gate` 的 churn
// 检查（拦不拦）和 `cmd/alive-symptoms` 的现状简报（第几次了）。口径一模一样
// 的代码抄两份，改一边另一边就漂，而这两处恰恰必须给出同一个数字：一个是
// 「门禁现在会不会红」，一个是「这一页说它第几次」。
//
// 口径本身有三条，都不显然：
//
//  1. **跨很多文件的提交不算「反复修」**。江 `11eba38` 一次动 22 个文件（把所有
//     错误字段统一），它不构成「这个文件被反复修」的证据——那是横切改动，
//     由「一次提交只做一件事」那条管。
//  2. **文档不算**。`doc/` 下的 fix 是叙述修正，不是同一条路径的返工。
//  3. **只有最近动过的才算「还在燃烧」**。规则落地那一刻历史里已经有一堆
//     超阈值的文件，让它们全部变红等于让门禁立刻失去意义。
package churn

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 口径的默认值。导出是因为展示层也要念同一句话——「一次改超过 8 个文件不算」
// 这种解释如果各写一遍，改了阈值就有一边在说谎。
const (
	DefaultWindowDays = 7
	DefaultHotHours   = 24
	DefaultCrossMax   = 8
)

// IsDoc 报告这个路径算不算「叙述修正」——文档的 fix 不是同一条路径的返工。
func IsDoc(p string) bool {
	return strings.HasPrefix(p, "doc/") || strings.HasSuffix(p, ".md")
}

// Options 是口径。零值有默认。
type Options struct {
	WindowDays int // 统计窗口，默认 7 天
	HotHours   int // 最后一次距今多少小时内算「还在燃烧」，默认 24
	CrossMax   int // 一次改超过这么多文件的提交不计入单文件，默认 8
	// Anchor 是窗口右端。0 表示用当前时刻（门禁要「今天会不会红」）；
	// 传一个固定时刻则整份结果可复现（现状简报要「同一个 HEAD 出同一页」）。
	Anchor int64
}

func (o Options) withDefaults() Options {
	if o.WindowDays <= 0 {
		o.WindowDays = 7
	}
	if o.HotHours <= 0 {
		o.HotHours = 24
	}
	if o.CrossMax <= 0 {
		o.CrossMax = 8
	}
	return o
}

// Commit 是一次 fix 提交。
type Commit struct {
	Hash    string
	When    int64
	Subject string
	Files   []string
}

// FileStat 是一个文件在这个窗口里的返工情况。
type FileStat struct {
	// Fixes 是每次 fix 的「hash 标题」，按时间倒序。
	Fixes []string
	// Count 是按口径计入的次数（已剔除横切提交与文档）。
	Count int
	// Raw 是不剔任何东西的次数——原文口径（AGENTS.md §3.7 的字面）。
	// 两个数都给出来，是因为读者会问「为什么这里写 6、那儿写 8」。
	Raw    int
	Last   int64
	LastAt string
}

// Result 是一次扫描的全部产出。
type Result struct {
	Now   int64
	Since int64
	Files map[string]*FileStat
}

// Hot 报告这个文件是不是「最近还在动」——只有它才该让门禁变红。
func (r *Result) Hot(f *FileStat, opt Options) bool {
	o := opt.withDefaults()
	return r.Now-f.Last < int64(o.HotHours)*3600
}

// Scan 跑一次 git log 并统计。
func Scan(root string, opt Options) (*Result, error) {
	o := opt.withDefaults()
	now := o.Anchor
	if now == 0 {
		now = timeNow()
	}
	since := now - int64(o.WindowDays)*86400

	out, err := git(root, "log",
		"--since="+strconv.FormatInt(since, 10),
		"--grep=^fix:",
		"--name-only",
		// %ct 是 committer date，不是 author date。两者的差别是真实的：
		// 7091893 作者在 11-25 03:25 写完、09:31 才提交（中间 rebase 过），
		// author date 会让「24 小时内还在动」判成冷却。用户感知的是「代码
		// 什么时候进仓库」，那是 committer date。
		"--pretty=format:@@%h %ct %s")
	if err != nil {
		return nil, err
	}

	res := &Result{Now: now, Since: since, Files: map[string]*FileStat{}}
	for _, block := range strings.Split(out, "@@") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		c, ok := parseBlock(block)
		if !ok {
			continue
		}
		cross := len(c.Files) > o.CrossMax
		for _, f := range c.Files {
			if strings.HasPrefix(f, "doc/") || strings.HasSuffix(f, ".md") {
				continue // 叙述修正不是同一条路径的返工
			}
			st := res.Files[f]
			if st == nil {
				st = &FileStat{}
				res.Files[f] = st
			}
			st.Raw++
			if cross {
				continue
			}
			st.Count++
			st.Fixes = append(st.Fixes, c.Hash+" "+c.Subject)
			if c.When > st.Last {
				st.Last, st.LastAt = c.When, c.Subject
			}
		}
	}
	return res, nil
}

// Sorted 按 Count 降序、同数按路径升序返回——报告要能前后对比。
func (r *Result) Sorted() []string {
	keys := make([]string, 0, len(r.Files))
	for k := range r.Files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := r.Files[keys[i]], r.Files[keys[j]]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return keys[i] < keys[j]
	})
	return keys
}

func parseBlock(block string) (Commit, bool) {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 {
		return Commit{}, false
	}
	fields := strings.SplitN(strings.TrimSpace(lines[0]), " ", 3)
	if len(fields) < 2 {
		return Commit{}, false
	}
	c := Commit{Hash: fields[0]}
	// 解析不出时间就留 0（当它很旧）：一行日期格式异常不该让整条检查失效。
	if v, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
		c.When = v
	}
	if len(fields) == 3 {
		c.Subject = fields[2]
	}
	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			c.Files = append(c.Files, l)
		}
	}
	return c, true
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// timeNow 单独抽出来，测试可以钉住它。
var timeNow = func() int64 { return time.Now().Unix() }
