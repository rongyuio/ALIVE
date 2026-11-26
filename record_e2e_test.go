//go:build e2e

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 录制保留策略：同一父目录下只留最近 keep 段。
//
// 这条守的是「录制不会无限涨」。一次排障录十几遍，每段 5 秒播放页就是 1.8 GB
// （record_e2e.go 的注释里有算法），不设上限一天几十 GB。而 .gitignore 挡住了
// git，于是「不进仓库」被当成「不用管」——2026-11-26 实测涨到 40 GB 没人发现。
func TestPruneRecordingsKeepsNewest(t *testing.T) {
	root := t.TempDir()
	// 造 5 段录制，mtime 依次变新；另有 1 个不是录制的目录。
	base := time.Now().Add(-5 * time.Hour)
	for i, name := range []string{"r1", "r2", "r3", "r4", "r5"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		at := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(root, "not-a-recording")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "notes.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// keep=3 是「含当前这段共 3 段」：当前是 r5，所以该留 r3 r4 r5。
	pruneRecordings(filepath.Join(root, "r5"), 3)

	left := func(name string) bool {
		_, err := os.Stat(filepath.Join(root, name))
		return err == nil
	}
	if left("r1") || left("r2") {
		t.Fatalf("最旧的两段没被清掉：r1=%v r2=%v", left("r1"), left("r2"))
	}
	if !left("r3") || !left("r4") || !left("r5") {
		t.Fatalf("该留的被删了：r3=%v r4=%v r5=%v", left("r3"), left("r4"), left("r5"))
	}
	if !left("not-a-recording") {
		t.Fatal("删到不是录制的目录了：prune 只能碰有 manifest.json / frames.idx 的目录")
	}
}

// keep <= 0 表示不限：这时一段都不该删。
func TestPruneRecordingsDisabled(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "frames.idx"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneRecordings(filepath.Join(root, "b"), 0)
	for _, name := range []string{"a", "b"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("keep=0 时删了 %s：它应当是「不限」", name)
		}
	}
}
