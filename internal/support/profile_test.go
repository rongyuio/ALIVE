package support

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCPUProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	if err := CPUProfile(path, 150*time.Millisecond); err != nil {
		t.Fatalf("CPUProfile: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() == 0 {
		t.Fatal("profile 是空文件")
	}
}
