package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup 必须真的建出文件并写进去。这条测试的价值在于：日志落盘如果只是
// 「看起来接上了」而实际写不到，排障时才发现就晚了——它正是之前那个
// 问题的形状（slog 一直输出到没人看的 stderr）。
func TestSetupWritesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	// Windows 上 UserCacheDir 读 LOCALAPPDATA，其它平台读 XDG_CACHE_HOME。
	t.Setenv("XDG_CACHE_HOME", dir)

	path := Setup()
	if path == "" {
		t.Fatal("Setup 没给出日志路径")
	}
	t.Cleanup(Close)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("日志文件不存在：%v", err)
	}

	Write("hello 落盘")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读日志失败：%v", err)
	}
	if !strings.Contains(string(data), "hello 落盘") {
		t.Fatalf("写入的内容没进文件：%q", string(data))
	}
}

// 没有换行结尾的一行必须补上，否则两条 mpv 日志会粘成一行。
func TestWriteAppendsNewline(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)

	path := Setup()
	if path == "" {
		t.Skip("这个环境拿不到缓存目录")
	}
	t.Cleanup(Close)
	Write("a")
	Write("b")

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got < 2 {
		t.Fatalf("两行应该有两个换行，实际 %d：%q", got, string(data))
	}
	if strings.Contains(string(data), "ab") {
		t.Fatalf("两行粘在一起了：%q", string(data))
	}
}
