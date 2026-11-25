package hang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"alive/internal/applog"
)

// 卡死现场必须包含原因、goroutine 全量栈和当时进程的规模；没有真实卡死也能
// 验证 dump 这条路本身是通的。
func TestDumpWritesStack(t *testing.T) {
	dir := t.TempDir()
	w := &Watcher{dir: dir, run: "r1"}
	w.dump("test-reason")
	matches, err := filepath.Glob(filepath.Join(dir, "hang-r1-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("找到 %d 份卡死现场", len(matches))
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"reason=test-reason", "goroutines=", "goroutine stacks", "testing.tRunner"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("卡死现场缺 %q:\n%s", want, data)
		}
	}
}

// 卡死现场写完之后，主日志必须留下一条 event=hang：alive-diag answer freeze
// 和 timeline 都靠它定位这次卡死，光有独立文件等于没报案。
//
// 这里起真的 applog，不用裸 TextHandler。run 是 handler 注入的，用裸 handler
// 断言「这行带不带 run」测的是 hang.go 的实现细节，不是主日志的样子——上一版
// 就是这么漏掉两个 run= 的。
func TestDumpEmitsHangEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	path, err := applog.Setup()
	if err != nil {
		t.Fatalf("applog.Setup: %v", err)
	}
	run := applog.RunID()

	w := &Watcher{dir: t.TempDir(), run: "r2"}
	w.dump("ui-thread-unresponsive")
	// Close 刷完队列，之后文件里才是这一行的全部内容。
	applog.Close()
	t.Cleanup(applog.Close)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读主日志: %v", err)
	}
	out := string(data)
	// 日志格式是 zerolog 的 JSON，键带引号：断言写 logfmt 的 event=hang
	// 在迁移后会全红，而它要守的其实是「这三个键在不在」。
	for _, want := range []string{`"event":"hang"`, `"reason":"ui-thread-unresponsive"`, "hang-r2-"} {
		if !strings.Contains(out, want) {
			t.Fatalf("卡死事件缺 %q:\n%s", want, out)
		}
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, `"event":"hang"`) {
			continue
		}
		// run 只该出现一次，而且来自 handler。两个 run 键说明调用点也在写。
		if n := strings.Count(line, `"run":`); n != 1 {
			t.Fatalf("run 出现 %d 次，只该由 handler 注入一次:\n%s", n, line)
		}
		if !strings.Contains(line, `"run":"`+run+`"`) {
			t.Fatalf("卡死这一行的 run 不是本次运行 %s:\n%s", run, line)
		}
		return
	}
	t.Fatalf("主日志里没有 event=hang:\n%s", out)
}
