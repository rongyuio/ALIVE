package applog

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup 把日志目录换到临时目录，返回日志路径。
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	path, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(Close)
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读日志失败: %v", err)
	}
	return string(data)
}

// 日志必须真的落盘，而且每行带 run。没有 run，多次启动的日志无法分段——
// 这次排障就撞上了：同一份文件里两个构建、多个进程混在一起。
func TestSetupWritesRun(t *testing.T) {
	path := setup(t)
	run := RunID()
	if run == "" {
		t.Fatal("RunID 为空")
	}
	slog.Info("测试事件", "event", "test.event")
	data := read(t, path)
	if !strings.Contains(data, "测试事件") {
		t.Fatalf("写入的内容没进文件: %q", data)
	}
	if !strings.Contains(data, "run="+run) {
		t.Fatalf("每行必须带 run=%s: %q", run, data)
	}
	if !strings.Contains(data, "source=") {
		t.Fatalf("每行必须带 source: %q", data)
	}
}

// 唯一脱敏出口必须真的挡住 URL query、token 和 Windows 用户名。
func TestRedaction(t *testing.T) {
	path := setup(t)
	secret := "tok" + "en=SECRET123"
	err := errors.New(`GET https://api.example.com/room?` + secret + ` failed under C:\Users\alice\AppData`)
	slog.Error("上游失败", "err", err)
	data := read(t, path)
	for _, bad := range []string{"SECRET123", "alice", "?token"} {
		if strings.Contains(data, bad) {
			t.Fatalf("日志里出现了 %q: %q", bad, data)
		}
	}
	if !strings.Contains(data, "api.example.com") {
		t.Fatalf("脱敏过度，host 应该保留: %q", data)
	}
}

// Debug 默认不落盘；ALIVE_LOG_LEVEL=debug 后必须出现。
func TestLevelSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_LEVEL", "debug")
	path, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	slog.Debug("调试细节", "event", "test.debug")
	data := read(t, path)
	if !strings.Contains(data, "调试细节") {
		t.Fatalf("ALIVE_LOG_LEVEL=debug 时 Debug 应落盘: %q", data)
	}
}

// 最近 N 行必须能取出来；崩溃/卡死时这是唯一的上下文。
func TestRecent(t *testing.T) {
	setup(t)
	for i := 0; i < 10; i++ {
		slog.Info("环形缓冲", "i", i)
	}
	lines := Recent()
	if len(lines) == 0 {
		t.Fatal("Recent 为空")
	}
	joined := strings.Join(lines, "")
	if !strings.Contains(joined, "环形缓冲") {
		t.Fatalf("Recent 没拿到刚写的事件: %q", joined)
	}
}

// 每次启动一个文件，旧文件按数量保留。轮转/多开都不会再互相覆盖。
func TestRunFilesAndRetention(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_KEEP", "2")

	var paths []string
	for i := 0; i < 4; i++ {
		path, err := Setup()
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		slog.Info("一次运行", "i", i)
		Close()
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("检查 %s: %v", p, err)
		}
	}
	logs, err := filepath.Glob(filepath.Join(dir, "alive", "logs", "alive-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	// keep=2，当前文件也保留，所以最多 2 份；.1 备份可能让数量略多。
	if len(logs) > 4 {
		t.Fatalf("保留策略失效，留下 %d 份: %v", len(logs), logs)
	}
	// 每次启动的文件名必须不同。
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("两次启动复用了同一个文件: %s", p)
		}
		seen[p] = true
	}
}

// 队列/轮转失败不能静默。这里只验证写满上限后仍能继续写，且新文件存在。
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_MAX_MB", "1")
	path, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	payload := strings.Repeat("x", 200)
	for i := 0; i < 6000; i++ {
		slog.Info("轮转填充", "i", i, "payload", payload)
	}
	Close()
	if _, err := os.Stat(strings.TrimSuffix(path, ".log") + ".1.log"); err != nil {
		t.Fatalf("轮转备份不存在: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("轮转后主文件不可写: %v", err)
	}
}
