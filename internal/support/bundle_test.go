package support

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleCollectsEvidenceOnly(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"alive-r1.log":  "time=2026-11-25T01:00:00.000+08:00 level=INFO msg=启动 event=run.start run=r1\n",
		"recent-r1.log": "recent\n",
		"crash-r1.log":  "panic\n",
		"state.json":    `{"token":"SECRET"}`,
		"alive-r2.log":  "time=2026-11-25T02:00:00.000+08:00 level=INFO msg=启动 event=run.start run=r2\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "bundle.zip")
	path, err := Bundle(Options{
		Out: out, LogDir: dir, Meta: map[string]any{"run": "r1", "version": "0.1.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != out {
		t.Fatalf("path=%q", path)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"alive-r1.log", "recent-r1.log", "crash-r1.log", "meta.json", "manifest.txt"} {
		if !names[want] {
			t.Fatalf("bundle 缺 %s: %v", want, names)
		}
	}
	if names["state.json"] {
		t.Fatal("bundle 不该包含 state.json")
	}
	var meta map[string]any
	for _, f := range zr.File {
		if f.Name != "meta.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewDecoder(rc).Decode(&meta); err != nil {
			t.Fatal(err)
		}
		rc.Close()
	}
	if meta["run"] != "r1" || meta["version"] != "0.1.0" {
		t.Fatalf("meta=%v", meta)
	}
	if _, ok := meta["metrics"]; !ok {
		t.Fatalf("meta 没有 metrics: %v", meta)
	}
}

func TestBundleKeepsRecentRuns(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alive-r1.log", "alive-r2.log", "alive-r3.log", "alive-r4.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "b.zip")
	if _, err := Bundle(Options{Out: out, LogDir: dir, KeepRun: 2}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	runs := 0
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "alive-") {
			runs++
		}
	}
	if runs != 2 {
		t.Fatalf("保留 %d 次运行，期望 2", runs)
	}
}
