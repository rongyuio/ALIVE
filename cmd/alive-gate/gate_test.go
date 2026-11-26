package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 这些测试证明门禁真的会红。
//
// 一条永远绿的门禁比没有门禁更糟：它让人以为有保护。所以每条检查都要有一个
// 「造一个违规 → 它必须报」的用例。断言的是检查的返回值，不是退出码——
// 退出码那层由 main 负责，这里只测判据。

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

// newRepo 造一个最小仓库：一次提交，工作区干净。
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	write(t, dir, "README.md", "x\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "chore: init")
	return dir
}

// TestEvaluatorCatchesSecondEvaluator：同一概念出现第二个求值点时必须报。
// 这是四个 TestNoSecond* 之外的缺口——它守的是「求值点」，不是「写入口」。
func TestEvaluatorCatchesSecondEvaluator(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "internal/ui/surface.go", `
package ui

type picture int

func (a *App) pictureAlpha(key string) picture {
	if key == "" {
		return 0
	}
	if key == "x" {
		return 1
	}
	return 2
}

func (a *App) pictureBeta(key string) picture {
	if key == "" {
		return 0
	}
	if key == "y" {
		return 1
	}
	return 2
}
`)
	got := checkEvaluator(newEnv(dir))
	if len(got) == 0 {
		t.Fatal("两个求值点没被抓到")
	}
	if !strings.Contains(got[0], "pictureBeta") || !strings.Contains(got[0], "pictureAlpha") {
		t.Errorf("报的内容不对：%s", got[0])
	}
}

// TestEvaluatorAllowsWrapper：一个权威 + 若干只做委托的包装是合法的。
func TestEvaluatorAllowsWrapper(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "internal/ui/surface.go", `
package ui

type picture int

func (a *App) pictureAt(key string, card bool) picture {
	if key == "" {
		return 0
	}
	if card {
		return 1
	}
	return 2
}

func (a *App) pictureNow(key string) picture {
	return a.pictureAt(key, true)
}
`)
	if got := checkEvaluator(newEnv(dir)); len(got) != 0 {
		t.Fatalf("包装被误判成第二个求值点：%v", got)
	}
}

// TestFactsCatchesStaleNumber：文档里的数字和源码对不上时必须报。
func TestFactsCatchesStaleNumber(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "AGENTS.md", "界面是自绘的（`internal/ui` 里 3 处 `ui.Box`），其余随便。\n")
	write(t, dir, "internal/ui/a.go", `
package ui

func f() {
	ui.Box(c).Label("a")
	ui.Box(c).Label("b")
}
`)
	got := checkFacts(newEnv(dir))
	if len(got) == 0 {
		t.Fatal("文档写 3、实际 2，没被抓到")
	}
	if !strings.Contains(got[0], "3") || !strings.Contains(got[0], "2") {
		t.Errorf("报的内容不对：%s", got[0])
	}
}

// TestFactsIgnoresMissingNumber：话已经不在了就没什么可核的，不要报。
func TestFactsIgnoresMissingNumber(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "AGENTS.md", "界面是自绘的，没有数字。\n")
	if got := checkFacts(newEnv(dir)); len(got) != 0 {
		t.Fatalf("没有数字却报了：%v", got)
	}
}

// TestClaimsCatchesBareDenial：说「X 做不到」而没给一处出处，必须报。
func TestClaimsCatchesBareDenial(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "doc/x.md", "`WebView` 在 Windows 上做不到。\n")
	got := checkClaims(newEnv(dir))
	if len(got) == 0 {
		t.Fatal("无举证的能力断言没被抓到")
	}
}

// TestClaimsAcceptsEvidence：有出处就放过。
func TestClaimsAcceptsEvidence(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "doc/x.md", "`WebView` 在 Windows 上做不到，见 `third_party/mygo/ui/webview.go`。\n")
	if got := checkClaims(newEnv(dir)); len(got) != 0 {
		t.Fatalf("带出处却被报：%v", got)
	}
}

// TestProbesCatchesTrackedProbe：探针被 git 跟踪时必须报。
func TestProbesCatchesTrackedProbe(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "internal/ui/zz_probe_test.go", "package ui\n")
	write(t, dir, "internal/live/foo_probe_test.go", "package live\n")
	gitIn(t, dir, "add", "-f", "-A")
	got := checkProbes(newEnv(dir))
	if len(got) != 2 {
		t.Fatalf("两个探针，报了 %d 个：%v", len(got), got)
	}
}

// TestChurnCatchesHotFile：7 天内第 3 次 fix 且 24 小时内还在动 → 报。
func TestChurnCatchesHotFile(t *testing.T) {
	dir := newRepo(t)
	write(t, dir, "internal/ui/hot.go", "package ui\n")
	for i := 0; i < 3; i++ {
		write(t, dir, "internal/ui/hot.go", "package ui\n// "+string(rune('a'+i))+"\n")
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "fix: 第几次")
	}
	got := checkChurn(newEnv(dir))
	if len(got) == 0 {
		t.Fatal("3 次 fix 没被抓到")
	}
	if !strings.Contains(got[0], "hot.go") {
		t.Errorf("报的内容不对：%s", got[0])
	}
}

// TestChurnIgnoresCrossCutting：一次改十几个文件的提交不构成「这个文件被
// 反复修」的证据——那种提交由 commits 那条检查管。
func TestChurnIgnoresCrossCutting(t *testing.T) {
	dir := newRepo(t)
	var files []string
	for i := 0; i < 12; i++ {
		f := filepath.Join("internal", "x", string(rune('a'+i))+".go")
		write(t, dir, f, "package x\n")
		files = append(files, f)
	}
	for round := 0; round < 3; round++ {
		for _, f := range files {
			write(t, dir, f, "package x\n// "+string(rune('a'+round))+"\n")
		}
		gitIn(t, dir, "add", "-A")
		gitIn(t, dir, "commit", "-qm", "fix: 横切改动")
	}
	if got := checkChurn(newEnv(dir)); len(got) != 0 {
		t.Fatalf("横切提交被算成了反复修：%v", got)
	}
}

// TestCommitsCatchesFatFix：未推送的 fix 提交动了超过上限的文件 → 报。
func TestCommitsCatchesFatFix(t *testing.T) {
	dir := newRepo(t)
	for i := 0; i < 5; i++ {
		write(t, dir, filepath.Join("internal", "x", string(rune('a'+i))+".go"), "package x\n")
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix: 一口气改五件事")
	got := checkCommits(newEnv(dir))
	if len(got) == 0 {
		t.Fatal("5 个文件的 fix 没被抓到")
	}
}

// TestCommitsAcceptsReason：写了拆不开的理由就放过。
func TestCommitsAcceptsReason(t *testing.T) {
	dir := newRepo(t)
	for i := 0; i < 5; i++ {
		write(t, dir, filepath.Join("internal", "x", string(rune('a'+i))+".go"), "package x\n")
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "fix: 改五处", "-m", "拆不开：同一个写者收口，拆开就没有中间态")
	if got := checkCommits(newEnv(dir)); len(got) != 0 {
		t.Fatalf("写了理由却被报：%v", got)
	}
}
