//go:build windows

package support

import (
	"os"
	"path/filepath"
	"testing"
)

// 当前进程的 minidump 必须能写出来，而且不是空文件。
//
// 没有真实崩溃也能验证 FFI 调用是否正确：MiniDumpWriteDump 对自己同样有效。
func TestMinidump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.dmp")
	if err := Minidump(path); err != nil {
		t.Fatalf("Minidump: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Fatal("minidump 是空文件")
	}
}
