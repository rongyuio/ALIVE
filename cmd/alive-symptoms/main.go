// alive-symptoms 从 git 历史生成 doc/STATE.md：一页「现在哪儿在烧、这件事第几次了」。
//
// 只读 git，不写历史，不依赖 .tmp/ 里的任何东西。
//
//	go run ./cmd/alive-symptoms                     # 生成 doc/STATE.md
//	go run ./cmd/alive-symptoms -out -              # 打到 stdout
//	go run ./cmd/alive-symptoms -file internal/ui/surface_motion.go   # 这个文件第几次了
//	go run ./cmd/alive-symptoms -check              # 只判定：越闸门就退出码 3
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "alive-symptoms: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	root := flag.String("root", "", "仓库根；默认用 git rev-parse 从当前目录往上找")
	symptomsPath := flag.String("symptoms", "doc/symptoms.json", "症状表路径（相对仓库根的路径）")
	out := flag.String("out", "doc/STATE.md", "输出路径；- 表示 stdout")
	nowText := flag.String("now", "", "窗口右端点（2006-01-02 或 RFC3339）；默认取 HEAD 的 committer 时间")
	days := flag.Int("days", 0, "窗口天数；默认取症状表里的 windowDays")
	fileQuery := flag.String("file", "", "只回答一个文件：它是第几次了")
	symQuery := flag.String("symptom", "", "只回答一条症状：它抓到的是哪几次提交（维护症状表时用）")
	check := flag.Bool("check", false, "只判定不写文件：有任何症状或文件越闸门就退出码 3")
	flag.Parse()
	if flag.NArg() > 0 {
		return fmt.Errorf("多余的参数：%s", strings.Join(flag.Args(), " "))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot := *root
	if repoRoot == "" {
		repoRoot, err = FindRoot(cwd)
		if err != nil {
			return err
		}
	}
	repo, err := LoadRepo(repoRoot)
	if err != nil {
		return err
	}

	tblPath := *symptomsPath
	if !filepath.IsAbs(tblPath) {
		tblPath = filepath.Join(repoRoot, tblPath)
	}
	tbl, err := LoadTable(tblPath)
	if err != nil {
		return err
	}
	syms, err := tbl.Compile()
	if err != nil {
		return err
	}

	windowDays := tbl.WindowDays
	if *days > 0 {
		windowDays = *days
	}
	anchor := repo.Anchor()
	if *nowText != "" {
		anchor, err = parseAnchor(*nowText, anchor.Location())
		if err != nil {
			return err
		}
	}
	w := NewWindow(anchor, windowDays)

	files := FileStats(repo.Commits, w)
	if err := ApplyChurn(repo.Root, repo.Anchor().Unix(), files); err != nil {
		fmt.Fprintf(os.Stderr, "alive-symptoms: 读 churn 口径失败: %v\n", err)
		os.Exit(1)
	}
	byPath := make(map[string]*FileStat, len(files))
	for _, f := range files {
		byPath[f.Path] = f
	}
	symsStat := SymptomStats(repo.Commits, syms, w, byPath, tbl.StopAt)
	report := Report{Repo: repo, Table: tbl, Allow: LoadAllow(repoRoot), Window: w,
		NowOverride: *nowText, Files: sortFiles(files), Syms: symsStat}

	if *fileQuery != "" {
		path := filepath.ToSlash(*fileQuery)
		f := byPath[path]
		if f == nil {
			return fmt.Errorf("历史里没有 %s", path)
		}
		fmt.Print(renderFileQuery(f, report, symsStat))
		return nil
	}

	if *symQuery != "" {
		for _, s := range symsStat {
			if s.S.ID != *symQuery && s.S.Name != *symQuery {
				continue
			}
			fmt.Print(renderSymptomQuery(s, report))
			return nil
		}
		return fmt.Errorf("症状表里没有 %q", *symQuery)
	}

	if *check {
		return gate(report)
	}

	doc := Render(report)
	if *out == "-" {
		fmt.Print(doc)
		return nil
	}
	outPath := *out
	if !filepath.IsAbs(outPath) {
		outPath = filepath.Join(repoRoot, outPath)
	}
	if err := os.WriteFile(outPath, []byte(doc), 0o644); err != nil {
		return err
	}
	hot := 0
	for _, f := range files {
		if f.Changes > 0 {
			hot++
		}
	}
	fmt.Fprintf(os.Stderr, "已生成 %s（锚点 %s，%d 天窗口，窗口内动过 %d 个文件 / %d 条症状）\n",
		*out, shortHash(repo.Head()), w.Days, hot, len(report.Syms))
	return nil
}

func parseAnchor(s string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("-now 无法解析：%q（要 2006-01-02 或 RFC3339）", s)
}

// gate 让这个工具能当门禁用：越闸门就非零退出，不靠人读文档。
// 判据和 `make gate` 的 churn 一致（gate 口径 + 24 小时内还在动 + .gate-allow 生效才算绿），
// 另外加上症状这一层——§3.7 数不到的那一半。
func gate(r Report) error {
	stop := r.Table.StopAt
	churn := time.Duration(r.Table.ChurnHours) * time.Hour
	type hit struct {
		what string
		n    int
	}
	var hits []hit
	for _, s := range r.Syms {
		if s.Burn(stop) && r.Window.Anchor.Sub(s.Last) < churn {
			hits = append(hits, hit{what: "症状：" + s.S.Name, n: len(s.In)})
		}
	}
	for _, f := range r.Files {
		if gateBlocks(f, stop, churn, r.Window, r.Allow) {
			hits = append(hits, hit{what: "文件：" + f.Path, n: f.GateFixes})
		}
	}
	for _, h := range hits {
		fmt.Printf("停手 %s —— 窗口内第 %d 次\n", h.what, h.n)
	}
	if len(hits) == 0 {
		return nil
	}
	os.Exit(3)
	return nil
}
