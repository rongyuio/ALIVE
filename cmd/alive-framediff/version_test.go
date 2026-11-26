package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manifest 声明了 version 就要校验：不认识的新格式必须报错，不能按 v1 静默误读。
func TestOpenSequenceRejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	man := `{"tool":"alive","version":99,"format":"bgra-raw","width":2,"height":2}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := OpenSequence(dir)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version=99 应该被拒，得到 %v", err)
	}
}
