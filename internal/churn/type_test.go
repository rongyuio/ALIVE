package churn

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
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

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-qm", "chore: init")
	return dir
}

func touchAndCommit(t *testing.T, dir, rel, subject string, extra ...string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package x\n// "+subject+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	args := append([]string{"commit", "-qm", subject}, extra...)
	gitRun(t, dir, args...)
}

// fix(scope): 也是 fix。旧实现用 `--grep=^fix:` 既不认 scope，又会把 body 里
// 抄了一句 `fix:` 的 feat 提交算进来。
func TestTypeOfScopedAndBodyLine(t *testing.T) {
	if TypeOf("fix(ui): 修一个点") != "fix" {
		t.Fatal("fix(ui): 应该是 fix")
	}
	if TypeOf("feat: 新增") != "feat" {
		t.Fatal("feat 应该是 feat")
	}
	if TypeOf("Merge pull request #1 from x/fix/y") != "" {
		t.Fatal("merge 提交不该是 fix")
	}
}

func TestScanCountsScopedAndIgnoresFeatBodyFix(t *testing.T) {
	dir := initRepo(t)
	// 三次 fix(ui): 同一个文件
	for i := 0; i < 3; i++ {
		touchAndCommit(t, dir, "a.go", "fix(ui): 第"+string(rune('一'+i))+"次")
	}
	// 三次 feat:，body 里抄了 `fix: …`，不该被算成 fix
	for i := 0; i < 3; i++ {
		touchAndCommit(t, dir, "b.go", "feat: 新增"+string(rune('a'+i)), "-m", "fix: 顺手改了一句")
	}
	res, err := Scan(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if f := res.Files["a.go"]; f == nil || f.Count != 3 {
		t.Fatalf("fix(ui) 没被计入: %+v", f)
	}
	if f := res.Files["b.go"]; f != nil && f.Count != 0 {
		t.Fatalf("feat 的 body 被当成了 fix: %+v", f)
	}
}
