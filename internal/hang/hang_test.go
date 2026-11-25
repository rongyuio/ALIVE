package hang

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
