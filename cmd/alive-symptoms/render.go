package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	// 别名：这个文件里 `churn` 已经是一个 time.Duration（「只拦 N 小时内」），
	// 包名直接撞车。
	churnpkg "alive/internal/churn"
)

// 这个文件只负责把已经算好的账排成一页。所有数字都来自 Report，这里不推一个新数。

// Report 是生成 STATE.md 需要的全部输入。
type Report struct {
	Repo   *Repo
	Table  *Table
	Allow  Allow
	Window Window
	// NowOverride 是 -now 的原文；非空意味着这一页是回看某个时点，不是 HEAD。
	NowOverride string
	Files       []*FileStat // 已按窗口改动量降序
	Syms        []*SymptomStat
}

// 一次别把整张表倒进文档：这个仓库 7 天内越 §3.7 闸门的文件有四十多个，
// 铺开会把真正要读的那几行埋掉。要查具体一个走 -file。
const (
	fileTableLimit = 12
	orphanLimit    = 6
)

func shortTime(t time.Time) string { return t.Format("01-02 15:04") }

func fullTime(t time.Time) string { return t.Format("2006-01-02 15:04") }

// gapText 用「天」当主单位：这个仓库的节奏是小时级，天数已经能读。
func gapText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d / (24 * time.Hour))
	if days >= 1 {
		return fmt.Sprintf("%d 天", days)
	}
	hours := int(d / time.Hour)
	if hours >= 1 {
		return fmt.Sprintf("%d 小时", hours)
	}
	return "刚发生"
}

// verdictText 是「窗口内第 N 次」的唯一措辞来源。
// 光给数字没有用：必须带停手动词和「先写清前 N-1 次」的要求，读的人才不会继续加补丁。
func verdictText(n int, last time.Time, stop int, churn time.Duration, w Window) string {
	if n < stop {
		return fmt.Sprintf("窗内 %d 次，未到闸门（%d）", n, stop)
	}
	if w.Anchor.Sub(last) < churn {
		return fmt.Sprintf("**窗内第 %d 次，该停手了**：先写清前 %d 次为什么没修对，再动代码", n, n-1)
	}
	return fmt.Sprintf("窗内第 %d 次，该停手了（已冷却，账还在）：先写清前 %d 次为什么没修对", n, n-1)
}

// nowText 回答「今天还在动吗」。这一列的存在就是因为 §3.7 的规则只有配上「还在动」
// 才可执行：冷却的存量谁都不该拦，但账得留着。
func nowText(last time.Time, w Window, churn time.Duration) string {
	if last.IsZero() {
		return "—"
	}
	if w.Anchor.Sub(last) < churn {
		return "**还在动**"
	}
	return "欠账（已冷却 " + gapText(w.Anchor.Sub(last)) + "）"
}

// fileVerdict 比 verdictText 多两层：§3.7 的数字、`make gate` 会不会真拦、以及
// 拦了的话 .gate-allow 有没有把它豁免掉。三件事分开说，判决才不是一句空话。
func fileVerdict(f *FileStat, stop int, churn time.Duration, w Window, allow Allow) string {
	n := f.Fixes
	gated := f.GateFixes >= stop
	gatedHot := !f.GateLastFix.IsZero() && w.Anchor.Sub(f.GateLastFix) < churn
	if gated && gatedHot {
		if e, ok := allow.Churn[f.Path]; ok {
			if e.Count == f.GateFixes {
				return fmt.Sprintf("**窗内第 %d 次，该停手了**：`.gate-allow` 记着「%s」，和现在一致——绿的是豁免，不是许可", n, e.Recorded)
			}
			return fmt.Sprintf("**窗内第 %d 次，该停手了**：`.gate-allow` 记的是「%s」，现在 churn 数到 %d 次——豁免已失配，gate 现在会红", n, e.Recorded, f.GateFixes)
		}
		return fmt.Sprintf("**窗内第 %d 次，该停手了**：`make gate` 现在就会拦", n)
	}
	switch {
	case churnpkg.IsDoc(f.Path):
		return fmt.Sprintf("窗内第 %d 次（文档不在 churn 的账上）", n)
	case f.GateFixes < stop:
		// churn 数得比原文口径少，只有两种原因：那个提交动了太多文件被当横切
		// 剔掉，或者它落在窗口的 en 端之外。不点名原因就会写出「动了 6 个文件
		// 被剔」这种自相矛盾的句子——6 远小于阈值 8。
		if f.Fixes > 0 && f.LastFixFiles > churnpkg.DefaultCrossMax {
			return fmt.Sprintf("窗内第 %d 次；churn 口径只数到 %d 次（最后一次动了 %d 个文件，被当横切提交剔掉）",
				n, f.GateFixes, f.LastFixFiles)
		}
		return fmt.Sprintf("窗内第 %d 次；churn 口径只数到 %d 次（部分提交落在窗口边界外）", n, f.GateFixes)
	}
	// 到这里：次数够、但 churn 眼里它已经冷却。
	if !f.LastFix.Equal(f.GateLastFix) {
		return fmt.Sprintf("窗内第 %d 次，欠账：churn 最后一次在 %s前，已冷却，不拦",
			n, gapText(w.Anchor.Sub(f.GateLastFix)))
	}
	return fmt.Sprintf("窗内第 %d 次，欠账：churn 口径最后一次在 %s前，已冷却，不拦", n, gapText(w.Anchor.Sub(f.GateLastFix)))
}

// gateBlocks 回答真正的门禁会不会红：gate 口径越闸门 + 24 小时内还在动 + 豁免没盖住它。
// 和 fileVerdict 共用同一套判据，免得这一页和 `make gate` 说出两句不一样的话。
func gateBlocks(f *FileStat, stop int, churn time.Duration, w Window, allow Allow) bool {
	if f.GateFixes < stop || f.GateLastFix.IsZero() || w.Anchor.Sub(f.GateLastFix) >= churn {
		return false
	}
	if e, ok := allow.Churn[f.Path]; ok && e.Count == f.GateFixes {
		return false // 豁免有效，门禁是绿的
	}
	return true
}

// cumulativeText 是「全部历史次数」的措辞。第四节看的是累计，所以和 verdictText 分开写：
// 同一个症状在两节里出现两个不同的「第 N 次」，必须各自标清是哪个口径。
func cumulativeText(all, stopAt int) string {
	if all < stopAt {
		return fmt.Sprintf("累计 %d 次，未到闸门（%d）", all, stopAt)
	}
	return fmt.Sprintf("**累计第 %d 次 —— 该停手了**：先写清前 %d 次为什么没修对，再动代码", all, all-1)
}

func filesCell(files []string, max int) string {
	if len(files) == 0 {
		return "（无文件）"
	}
	var parts []string
	for i, f := range files {
		if i >= max {
			parts = append(parts, fmt.Sprintf("…(+%d)", len(files)-max))
			break
		}
		parts = append(parts, "`"+f+"`")
	}
	return strings.Join(parts, "、")
}

func sortFiles(stats []*FileStat) []*FileStat {
	out := append([]*FileStat(nil), stats...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Changes != out[j].Changes {
			return out[i].Changes > out[j].Changes
		}
		if out[i].Fixes != out[j].Fixes {
			return out[i].Fixes > out[j].Fixes
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// Render 产出 doc/STATE.md 的全文。
func Render(r Report) string {
	var b strings.Builder
	w := r.Window
	tbl := r.Table

	fmt.Fprintf(&b, "# 现状简报（STATE）\n\n")
	fmt.Fprintf(&b, "> 这一页由 `cmd/alive-symptoms` 从 git 历史生成，**不要手改**。\n")
	fmt.Fprintf(&b, "> 要改判据改 `doc/symptoms.json`，然后重跑 `go run ./cmd/alive-symptoms`。\n")
	fmt.Fprintf(&b, "> 每次会话开头先读它：先知道现在哪儿在烧，再决定要不要动那个文件。\n")
	fmt.Fprintf(&b, "> 要问「这个文件第几次了」：`go run ./cmd/alive-symptoms -file <路径>`。\n")
	fmt.Fprintf(&b, "> `make gate` 跑的是同一条规则里能自动检查的那几条（churn / commits / claims / evaluator / facts / probes）；")
	fmt.Fprintf(&b, "这一页补的是它数不到的那一半——按症状，而不是按文件。\n\n")

	fmt.Fprintf(&b, "| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 锚点 | `%s` %s（HEAD，committer 时间） |\n", shortHash(r.Repo.Head()), fullTime(r.Repo.Anchor()))
	fmt.Fprintf(&b, "| 窗口 | %d 天：%s → %s |\n", w.Days, fullTime(w.Since), fullTime(w.Anchor))
	if r.NowOverride != "" {
		fmt.Fprintf(&b, "| 窗口右端 | 由 `-now` 指定成 %s，不是 HEAD——这一页是回看，不再随新提交变化 |\n",
			fullTime(w.Anchor))
	}
	fmt.Fprintf(&b, "| 闸门 | 同一症状或同一文件，%d 天内第 %d 次 `fix:` → 停手，先写清前几次为什么没修对 |\n", w.Days, tbl.StopAt)
	fmt.Fprintf(&b, "| 冷却期 | %d 天：最后一次之后这么久没再犯，才算这条线停住 |\n", tbl.ConvergeDays)
	fmt.Fprintf(&b, "| 症状表 | `doc/symptoms.json`（%d 条，人工维护） |\n", len(tbl.Symptoms))
	fmt.Fprintf(&b, "| 全部历史 | %d 次提交，%d 个 `fix:` |\n\n", len(r.Repo.Commits), r.Repo.FixCount())

	renderNow(&b, r)
	renderBurning(&b, r)
	renderRecent(&b, r)
	renderUnkept(&b, r)
	return b.String()
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// 一、现在
func renderNow(b *strings.Builder, r Report) {
	fmt.Fprintf(b, "## 一、现在：最近 %d 天最热的文件\n\n", r.Window.Days)
	fmt.Fprintf(b, "| # | 文件 | 改动 | 其中 `fix:` | 累计改动 | 最近一次 |\n")
	fmt.Fprintf(b, "|---|--:|--:|--:|--:|---|\n")
	limit := 10
	for i, f := range r.Files {
		if i == limit {
			break
		}
		fmt.Fprintf(b, "| %d | `%s` | %d | %d | %d | %s |\n",
			i+1, f.Path, f.Changes, f.Fixes, f.TotalChanges, shortTime(f.Last))
	}
	fmt.Fprintf(b, "\n热不等于该修。这张表回答的是「最近有人在哪里反复按手印」，")
	fmt.Fprintf(b, "不回答「该不该再按一次」——那个在第二节。\n\n")
}

// 二、正在燃烧
func renderBurning(b *strings.Builder, r Report) {
	w := r.Window
	stop := r.Table.StopAt
	churn := time.Duration(r.Table.ChurnHours) * time.Hour

	fmt.Fprintf(b, "## 二、正在燃烧：%d 天内第 %d 次及以上\n\n", w.Days, stop)
	fmt.Fprintf(b, "「现在」那一列是这一节的关键：**还在动**的才是今天正被第 N 次修、`make gate` 会拦；")
	fmt.Fprintf(b, "**欠账**的是闸门早过了但已经停了几天——规则不拦，账还在。\n\n")

	// 2.1 按症状
	fmt.Fprintf(b, "### 2.1 按症状（`AGENTS.md` §3.7 管不到的那一半）\n\n")
	var hot []*SymptomStat
	var calm []*SymptomStat
	for _, s := range r.Syms {
		if s.Burn(stop) {
			hot = append(hot, s)
		} else {
			calm = append(calm, s)
		}
	}
	if len(hot) == 0 {
		fmt.Fprintf(b, "窗口内没有症状越过闸门。\n\n")
	} else {
		fmt.Fprintf(b, "| 症状 | 窗内 | 累计 | 最后一次 | 现在 | 结论 |\n")
		fmt.Fprintf(b, "|---|--:|--:|---|---|---|\n")
		for _, s := range hot {
			fmt.Fprintf(b, "| %s | %d | %d | %s | %s | %s |\n",
				s.S.Name, len(s.In), len(s.All), shortTime(s.Last),
				nowText(s.Last, w, churn), verdictText(len(s.In), s.Last, stop, churn, w))
		}
		fmt.Fprintf(b, "\n")
	}
	if len(calm) > 0 {
		fmt.Fprintf(b, "未越闸门的：")
		var parts []string
		for _, s := range calm {
			parts = append(parts, fmt.Sprintf("%s（窗内 %d 次、累计 %d 次、最后一次 %s）",
				s.S.Name, len(s.In), len(s.All), shortTime(s.Last)))
		}
		fmt.Fprintf(b, "%s。\n\n", strings.Join(parts, "；"))
	}

	// 2.2 按文件
	fmt.Fprintf(b, "### 2.2 按文件（§3.7 的原算法，看的是文件不是问题）\n\n")
	var burning []*FileStat
	for _, f := range r.Files {
		// 文档不进这张表：churn 明确豁免 doc/ 与 .md（见 internal/churnpkg.IsDoc），
		// 列在这里只会让「该停手」变成一句对门禁无效的判决。第一节仍然数它们的热度。
		if f.Fixes >= stop && !churnpkg.IsDoc(f.Path) {
			burning = append(burning, f)
		}
	}
	blocked := func(f *FileStat) bool {
		return f.GateFixes >= stop && !f.GateLastFix.IsZero() && w.Anchor.Sub(f.GateLastFix) < churn
	}
	sort.Slice(burning, func(i, j int) bool {
		bi, bj := blocked(burning[i]), blocked(burning[j])
		if bi != bj {
			return bi
		}
		if burning[i].Fixes != burning[j].Fixes {
			return burning[i].Fixes > burning[j].Fixes
		}
		return burning[i].Path < burning[j].Path
	})
	if len(burning) == 0 {
		fmt.Fprintf(b, "窗口内没有源码文件在 %d 天内被 `fix:` 到 %d 次。\n\n", w.Days, stop)
	} else {
		fmt.Fprintf(b, "| 文件 | 窗内 `fix:` | churn 口径 | 最后 fix | 现在 | 结论 |\n")
		fmt.Fprintf(b, "|---|--:|--:|---|---|---|\n")
		for i, f := range burning {
			if i == fileTableLimit {
				break
			}
			fmt.Fprintf(b, "| `%s` | %d | %d | %s | %s | %s |\n",
				f.Path, f.Fixes, f.GateFixes, shortTime(f.LastFix), nowText(f.LastFix, w, churn),
				fileVerdict(f, stop, churn, w, r.Allow))
		}
		fmt.Fprintf(b, "\n")
		if len(burning) > fileTableLimit {
			fmt.Fprintf(b, "按「gate 现在会拦 → 窗内 fix 多」排序还有 %d 个文件也越闸门，这里不铺开：要查具体一个用 `-file <路径>`。\n\n",
				len(burning)-fileTableLimit)
		}
		fmt.Fprintf(b, "两列数字为什么不一样：「窗内 `fix:`」是 §3.7 原文口径（不剔测试、不剔横切提交）；")
		fmt.Fprintf(b, "「churn 口径」是 `make gate` 真正数的（跳过动 >%d 个文件的横切提交，跳过 `doc/` 与 `.md`，只拦 %d 小时内还在动的）。",
			churnpkg.DefaultCrossMax, r.Table.ChurnHours)
		fmt.Fprintf(b, "标「欠账」的门禁不会红——不是它没问题，是规则只拦今天。\n\n")
		if len(r.Allow.Churn) > 0 {
			paths := make([]string, 0, len(r.Allow.Churn))
			for p := range r.Allow.Churn {
				paths = append(paths, p)
			}
			sort.Strings(paths)
			fmt.Fprintf(b, "**`.gate-allow` 已经豁免的 %d 个文件**（豁免的是存量，不是继续打补丁的许可）：\n\n", len(paths))
			for _, p := range paths {
				e := r.Allow.Churn[p]
				fmt.Fprintf(b, "- `%s` — 记着「%s」：%s\n", p, e.Recorded, e.Reason)
			}
			fmt.Fprintf(b, "\n")
		}
	}

	// 2.3 两条规则的分歧
	fmt.Fprintf(b, "### 2.3 两条规则的分歧\n\n")
	claimed := map[string]bool{}
	for _, s := range r.Syms {
		for _, f := range s.Files {
			claimed[f.Path] = true
		}
	}
	var orphans []string
	orphanTotal := 0
	for _, f := range burning {
		if claimed[f.Path] || isNoiseFile(f.Path) {
			continue
		}
		orphanTotal++
		if len(orphans) < orphanLimit {
			orphans = append(orphans, "`"+f.Path+"`")
		}
	}
	for _, s := range hot {
		if s.Typing == 0 {
			fmt.Fprintf(b, "- **%s**：%d 次修散在 %d 个文件上（%s），没有一个文件自己够 %d 次 —— §3.7 从头到尾不会拦住它。\n",
				s.S.Name, len(s.In), len(s.Files), spreadText(s), stop)
			continue
		}
		fmt.Fprintf(b, "- **%s**：%d 次修散在 %d 个文件上（%s），其中 %d 个文件自己也够 %d 次 —— §3.7 点到的是那些文件，一次都没点到这条症状本身。\n",
			s.S.Name, len(s.In), len(s.Files), spreadText(s), s.Typing, stop)
	}
	if orphanTotal > 0 {
		extra := ""
		if orphanTotal > len(orphans) {
			extra = fmt.Sprintf("（另有 %d 个略）", orphanTotal-len(orphans))
		}
		fmt.Fprintf(b, "- 有 %d 个源码文件在烧但没有任何症状认领：%s%s —— 症状表可能缺一条，也可能只是重构痕迹，看过再决定。\n",
			orphanTotal, strings.Join(orphans, "、"), extra)
	}
	fmt.Fprintf(b, "\n")
}

func spreadText(s *SymptomStat) string {
	if len(s.Files) == 0 {
		return "没有落点"
	}
	top := s.Files[0]
	return fmt.Sprintf("最密的是 `%s` %d 次", top.Path, top.Hits)
}

// 三、上次说了什么
func renderRecent(b *strings.Builder, r Report) {
	fmt.Fprintf(b, "## 三、上次说了什么：最近 10 次提交\n\n")
	limit := 10
	for i, c := range r.Repo.Commits {
		if i == limit {
			break
		}
		fmt.Fprintf(b, "%d. `%s` %s —— %s\n   - 改：%s\n",
			i+1, shortHash(c.Hash), shortTime(c.Time), c.Subject, filesCell(c.Files, 6))
	}
	fmt.Fprintf(b, "\n提交标题是这一页唯一的叙述来源：只有标题说了「修的是什么症状」，症状表才数得到它。\n")
	fmt.Fprintf(b, "标题写成「调整一下」的修复，在这里等于没发生。\n\n")
}

// 四、你说过但没做到的
func renderUnkept(b *strings.Builder, r Report) {
	tbl := r.Table
	w := r.Window

	fmt.Fprintf(b, "## 四、你说过但没做到的\n\n")
	fmt.Fprintf(b, "判据（写死在 `cmd/alive-symptoms` 里，不是手填的）：症状表里**累计被报 ≥ %d 次**，"+
		"且**最后一次离锚点不足 %d 天**——冷却期没走完，就算还没收敛。\n\n", tbl.StopAt, tbl.ConvergeDays)

	var open, done []*SymptomStat
	for _, s := range r.Syms {
		if len(s.All) < tbl.StopAt {
			continue
		}
		if s.Converged(tbl.ConvergeDays, w) {
			done = append(done, s)
			continue
		}
		open = append(open, s)
	}
	if len(open) == 0 {
		fmt.Fprintf(b, "没有：累计 ≥%d 次且仍在冷却期外的症状，一条都没有。\n\n", tbl.StopAt)
	}
	for _, s := range open {
		fmt.Fprintf(b, "### %s —— 累计 %d 次\n\n", s.S.Name, len(s.All))
		if s.S.Means != "" {
			fmt.Fprintf(b, "- 报的是什么：%s\n", s.S.Means)
		}
		fmt.Fprintf(b, "- 窗内 %d 次，最后一次 %s（%s前）\n", len(s.In), fullTime(s.Last), gapText(s.Gap(w)))
		fmt.Fprintf(b, "- 结论：%s\n", cumulativeText(len(s.All), tbl.StopAt))
		shown := 0
		fmt.Fprintf(b, "- 已试过的修法（按标题抄，最近 5 次）：\n")
		for _, c := range s.All {
			if shown == 5 {
				fmt.Fprintf(b, "  - …更早还有 %d 次\n", len(s.All)-5)
				break
			}
			fmt.Fprintf(b, "  - `%s` %s %s\n", shortHash(c.Hash), shortTime(c.Time), c.Desc())
			shown++
		}
		if s.S.Note != "" {
			fmt.Fprintf(b, "- 判据的代价：%s\n", s.S.Note)
		}
		fmt.Fprintf(b, "\n")
	}
	if len(done) > 0 {
		fmt.Fprintf(b, "已停住（累计 ≥%d 次，之后 %d 天没再犯）：\n\n", tbl.StopAt, tbl.ConvergeDays)
		for _, s := range done {
			fmt.Fprintf(b, "- **%s**：累计 %d 次，最后一次 %s，空窗 %s。\n",
				s.S.Name, len(s.All), fullTime(s.Last), gapText(s.Gap(w)))
		}
		fmt.Fprintf(b, "\n")
	}
	fmt.Fprintf(b, "这一节不列「应该怎么修」。它只负责让「第几次了」不能靠感觉回答。\n")
}

// renderSymptomQuery 是 -symptom 的答案：这条判据实际抓到了哪几次提交。
// 维护症状表时唯一靠谱的检查方式——判据写得对不对，只能看它数出来的是不是心里那几次。
func renderSymptomQuery(s *SymptomStat, r Report) string {
	var b strings.Builder
	w := r.Window
	tbl := r.Table

	fmt.Fprintf(&b, "%s（%s）\n\n", s.S.Name, s.S.ID)
	fmt.Fprintf(&b, "  累计 %d 次，窗口（%d 天）内 %d 次\n", len(s.All), w.Days, len(s.In))
	fmt.Fprintf(&b, "  结论：%s\n", verdictText(len(s.In), s.Last, tbl.StopAt,
		time.Duration(tbl.ChurnHours)*time.Hour, w))
	fmt.Fprintf(&b, "\n  命中的提交（新 → 旧，标 * 为窗口内）：\n")
	for _, c := range s.All {
		mark := " "
		if w.Contains(c.Time) {
			mark = "*"
		}
		fmt.Fprintf(&b, "  %s `%s` %s %s\n", mark, shortHash(c.Hash), shortTime(c.Time), c.Subject)
	}
	if len(s.Files) > 0 {
		fmt.Fprintf(&b, "\n  修落在哪些文件（窗口内，已剔除测试与 third_party）：\n")
		for _, f := range s.Files {
			fmt.Fprintf(&b, "  - %s ×%d\n", f.Path, f.Hits)
		}
	}
	return b.String()
}

// renderFileQuery 是 -file 的答案：这个文件第几次了。
func renderFileQuery(f *FileStat, r Report, owner []*SymptomStat) string {
	var b strings.Builder
	tbl := r.Table
	w := r.Window

	fmt.Fprintf(&b, "%s\n\n", f.Path)
	fmt.Fprintf(&b, "  窗口（%d 天，%s → %s）内被 `fix:` %d 次，改动 %d 次\n",
		w.Days, fullTime(w.Since), fullTime(w.Anchor), f.Fixes, f.Changes)
	fmt.Fprintf(&b, "  累计改动 %d 次，其中 `fix:` %d 次\n", f.TotalChanges, f.TotalFixes)
	if !f.LastFix.IsZero() {
		fmt.Fprintf(&b, "  最后一次 `fix:` %s（%s前）\n", fullTime(f.LastFix), gapText(w.Anchor.Sub(f.LastFix)))
	} else {
		fmt.Fprintf(&b, "  窗口内没有 `fix:`\n")
	}
	fmt.Fprintf(&b, "  结论：%s\n", fileVerdict(f, tbl.StopAt, time.Duration(tbl.ChurnHours)*time.Hour, w, r.Allow))
	if e, ok := r.Allow.Churn[f.Path]; ok {
		fmt.Fprintf(&b, "  `.gate-allow` 的豁免理由：%s\n", e.Reason)
	}
	if len(owner) > 0 {
		fmt.Fprintf(&b, "\n  这条文件上的提交算在哪些症状名下：\n")
		for _, s := range owner {
			for _, sf := range s.Files {
				if sf.Path == f.Path {
					fmt.Fprintf(&b, "  - %s：窗内 %d 次，累计 %d 次\n", s.S.Name, sf.Hits, len(s.All))
				}
			}
		}
	} else {
		fmt.Fprintf(&b, "\n  没有症状认领这个文件：症状表可能缺一条。\n")
	}
	return b.String()
}
