package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 目录里有一个 Stat 失败的项（文件在 Glob 与排序之间被 prune 删掉、或悬挂
// 链接）时，load 应当跳过它，而不是 nil 解引用 panic。
func TestLoadSkipsStatFailures(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alive-ok.log"), []byte("{\"msg\":\"x\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "alive-dangling.log")
	if err := os.Symlink(filepath.Join(dir, "missing.log"), dangling); err != nil {
		t.Skipf("这个环境不支持建符号链接，跳过: %v", err)
	}
	if _, err := load(dir); err != nil {
		t.Fatalf("Stat 失败的项应该被跳过，load 却返回错: %v", err)
	}
}
