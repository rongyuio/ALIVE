package churn

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// 这几条钉的是口径本身：门禁和现状简报共用一个实现，口径错了是两边一起错。
// 所以每条都造一个具体场景，不是对照常量。

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, dir, "README.md", "x\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "chore: init")
	return dir
}

// commitFix 造一次 fix 提交，见们是 n 个文件。
func commitFix(t *testing.T, dir string, n int, subject string) {
	t.Helper()
	for i := 0; i < n; i++ {
		write(t, dir, filepath.Join("internal", "x", string(rune('a'+i))+".go"),
			"package x\n// "+subject+"\n")
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix: "+subject)
}

// TestCountsFixes：三次 fix 同一个文件，Count 就是 3。
func TestCountsFixes(t *testing.T) {
	dir := newRepo(t)
	for i := 0; i < 3; i++ {
		write(t, dir, "internal/ui/hot.go", "package ui\n// "+string(rune('a'+i))+"\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "fix: 第几次")
	}
	res, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Files["internal/ui/hot.go"]
	if st == nil {
		t.Fatal("没统计到 hot.go")
	}
	if st.Count != 3 || st.Raw != 3 {
		t.Errorf("Count=%d Raw=%d，期望 3/3", st.Count, st.Raw)
	}
	if !res.Hot(st, Options{}) {
		t.Error("刚提交的东西应该是 hot")
	}
}

// TestCrossCuttingNotCounted：一次改十几个文件的提交不计入 Count，但仍计入
// Raw —— 两个数都给出来，答案才不会被误读成「没发生过」。
func TestCrossCuttingNotCounted(t *testing.T) {
	dir := newRepo(t)
	commitFix(t, dir, 12, "横切改动")
	commitFix(t, dir, 12, "又一次横切")

	res, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Files["internal/x/a.go"]
	if st == nil {
		t.Fatal("没统计到 internal/x/a.go")
	}
	if st.Count != 0 {
		t.Errorf("Count=%d，横切提交不该计入", st.Count)
	}
	if st.Raw != 2 {
		t.Errorf("Raw=%d，原文口径应该如实记 2 次", st.Raw)
	}
}

// TestDocExcluded：文档的 fix 不算同一条路径的返工。
func TestDocExcluded(t *testing.T) {
	dir := newRepo(t)
	for i := 0; i < 3; i++ {
		write(t, dir, "doc/x.md", "# x\n"+string(rune('a'+i))+"\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "fix: 文档修正")
	}
	res, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := res.Files["doc/x.md"]; ok {
		t.Error("doc/ 下的 fix 不该进账")
	}
}

// TestAnchorMakesItReproducible：同一个 anchor，结果必须一样。
//
// 现状简报靠这条出「同一页」——用墙钟会让没有新提交的日子里 diff 自己变。
func TestAnchorMakesItReproducible(t *testing.T) {
	dir := newRepo(t)
	commitFix(t, dir, 1, "一次")

	res, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	anchor := res.Now
	a, err := Scan(dir, Options{Anchor: anchor})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Scan(dir, Options{Anchor: anchor})
	if err != nil {
		t.Fatal(err)
	}
	if a.Files["internal/x/a.go"].Count != b.Files["internal/x/a.go"].Count {
		t.Error("同一个 anchor 两次结果不同")
	}
	if a.Since != b.Since || a.Now != b.Now {
		t.Errorf("窗口不一致：%d..%d vs %d..%d", a.Since, a.Now, b.Since, b.Now)
	}
}

// TestHotUsesCommitterDate：判定「还在动」用 committer date。
//
// 7091893 作者在 03:25 写完、09:31 才提交（中间 rebase 过）。用 author date
// 会把它判成「29 小时前、已冷却」，而它其实是 23 小时前进的仓库。
func TestHotUsesCommitterDate(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "internal/ui/hot.go", "package ui\n")
	gitIn(t, dir, "add", "-A")
	cmd := exec.Command("git", "commit", "-qm", "fix: 作者很久以前写的")
	cmd.Dir = dir
	// 作者时间是 3 天前，提交时间是现在。用 author date 会判成冷却。
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v\n%s", err, out)
	}

	res, err := Scan(dir, Options{WindowDays: 3650})
	if err != nil {
		t.Fatal(err)
	}
	st := res.Files["internal/ui/hot.go"]
	if st == nil {
		t.Fatal("没统计到 hot.go")
	}
	if !res.Hot(st, Options{}) {
		t.Error("committer date 是现在，应该算 hot；用 author date 才会判成冷却")
	}
}
