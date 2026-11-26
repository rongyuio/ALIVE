package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 症状表声明了 version 就要校验：不认识的新格式报错，不要按 v1 静默少读字段。
func TestLoadTableRejectsUnknownVersion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "symptoms.json")
	body := `{"version":99,"windowDays":7,"stopAt":3,"convergeDays":7,"churnHours":24,"symptoms":[]}`
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadTable(p)
	if err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("version=99 应该被拒，得到 %v", err)
	}
}
