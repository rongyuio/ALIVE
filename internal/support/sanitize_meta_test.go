package support

import (
	"strings"
	"testing"
)

// meta 里的绝对路径必须和日志正文走同一条脱敏出口。
//
// 诊断包是「用户主动发给支持人员」的产物：日志里是 C:\Users\***，meta.json
// 里却是 C:\Users\alice，等于把本机用户名原样送出去。
func TestSanitizeMetaScrubsUserPath(t *testing.T) {
	m := map[string]any{
		"log_dir": `C:\Users\alice\AppData\Local\alive\logs`,
		"nested":  map[string]any{"path": `D:/Users/bob/x`},
		"list":    []string{`C:\Users\carol\y`},
		"count":   int64(3),
	}
	got := sanitizeMeta(m).(map[string]any)
	for _, s := range []string{
		got["log_dir"].(string),
		got["nested"].(map[string]any)["path"].(string),
		got["list"].([]string)[0],
	} {
		if strings.Contains(s, "alice") || strings.Contains(s, "bob") || strings.Contains(s, "carol") {
			t.Fatalf("用户名没脱敏: %q", s)
		}
	}
	if got["count"].(int64) != 3 {
		t.Fatalf("非字符串值被改了: %v", got["count"])
	}
}
