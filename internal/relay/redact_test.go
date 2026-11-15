package relay

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestScrubTextRemovesURLs(t *testing.T) {
	in := `Failed to open https://cdn.example.com/live/seg.ts?sign=abc&t=1: Connection reset by peer`
	out := scrubText("", in)
	if strings.Contains(out, "sign=abc") || strings.Contains(out, "cdn.example.com") {
		t.Fatalf("URL 没有被抹掉: %q", out)
	}
	if !strings.Contains(out, "<url>") {
		t.Fatalf("没有替换标记: %q", out)
	}
	if !strings.Contains(out, "Connection reset by peer") {
		t.Fatalf("非 URL 内容被误删: %q", out)
	}
}

func TestScrubTextRemovesToken(t *testing.T) {
	out := scrubText("tok123", "GET /t/tok123/r/1.ts failed")
	if strings.Contains(out, "tok123") {
		t.Fatalf("本地 token 泄漏: %q", out)
	}
	if !strings.Contains(out, "<token>") {
		t.Fatalf("没有替换标记: %q", out)
	}
}

func TestScrubTextKeepsPlainText(t *testing.T) {
	in := "frame= 120 fps= 30 size= 512kB time=00:00:04.00"
	if out := scrubText("", in); out != in {
		t.Fatalf("普通文本被改动: %q", out)
	}
}

func TestTruncateUTF8MatchesRuneBoundary(t *testing.T) {
	s := strings.Repeat("字", 300)
	out := truncateUTF8(s, 400)
	if len(out) > 400 {
		t.Fatalf("长度 = %d", len(out))
	}
	if !utf8.ValidString(out) {
		t.Fatalf("截断点落在多字节字符中间")
	}
	if want := strings.Repeat("字", 133); out != want {
		t.Fatalf("截断结果长度 = %d，期望 %d", len(out), len(want))
	}
	if got := truncateUTF8("short", 400); got != "short" {
		t.Fatalf("短文本被改动: %q", got)
	}
}
