package ui

import (
	"os"
	"path/filepath"
	"testing"

	"alive/internal/diag"
)

// 坏掉的 state.json 必须留下 state.load_fail，而不是静默回默认值。
//
// 用户报「设置全没了」时，日志要能区分首次运行、文件损坏、权限失败；
// 这条测试保证「文件损坏」这一支真的会写事件。
func TestCorruptStateLogsLoadFailure(t *testing.T) {
	c := captureLogs(t)
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "alive", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"follows":`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := loadState(); ok {
		t.Fatal("坏 state 不该读成功")
	}
	got := c.find(diag.EvStateLoadFail)
	if got == nil {
		t.Fatal("没有 state.load_fail")
	}
	if got["stage"] != "parse" {
		t.Fatalf("stage=%q，期望 parse", got["stage"])
	}
	if got["path"] != "state.json" {
		t.Fatalf("path=%q", got["path"])
	}
}
