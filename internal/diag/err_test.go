package diag

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"alive/internal/live"
)

func attr(t *testing.T, a slog.Attr, key string) slog.Attr {
	t.Helper()
	if a.Key != "err" || a.Value.Kind() != slog.KindGroup {
		t.Fatalf("不是 err group: %v", a)
	}
	for _, x := range a.Value.Group() {
		if x.Key == key {
			return x
		}
	}
	t.Fatalf("err group 里没有 %q: %v", key, a.Value.Group())
	return slog.Attr{}
}

func TestSanitizeURLAndSecrets(t *testing.T) {
	in := `GET https://api.example.com/room?token=` + "SECRET123" + `&sign=abc#frag failed under C:\Users\alice\AppData\Roaming\alive`
	out := Sanitize(in)
	for _, bad := range []string{"SECRET123", "sign=abc", "#frag", "alice"} {
		if strings.Contains(out, bad) {
			t.Fatalf("Sanitize 没去掉 %q: %q", bad, out)
		}
	}
	if !strings.Contains(out, "api.example.com") {
		t.Fatalf("host 应该保留: %q", out)
	}
	if !strings.Contains(out, `C:\Users\***`) {
		t.Fatalf("用户名目录应替换成 ***: %q", out)
	}
}

func TestSanitizeFlattensAndTruncates(t *testing.T) {
	out := Sanitize("第一行\n第二行\r\n" + strings.Repeat("长", 400))
	if strings.ContainsAny(out, "\r\n") {
		t.Fatalf("换行没压平: %q", out)
	}
	if n := len([]rune(out)); n > 241 {
		t.Fatalf("截断失效，长度 %d", n)
	}
}

func TestErrUpstreamKeepsStatus(t *testing.T) {
	err := fmt.Errorf("包装: %w", &live.UpstreamError{
		Platform: live.Douyu, Op: "stream", Status: 403, Message: "forbidden",
	})
	a := Err(err)
	if got := attr(t, a, "class").Value.String(); got != "upstream" {
		t.Fatalf("class=%q", got)
	}
	if got := attr(t, a, "status").Value.Int64(); got != 403 {
		t.Fatalf("status=%d", got)
	}
	if got := attr(t, a, "op").Value.String(); got != "stream" {
		t.Fatalf("op=%q", got)
	}
	if got := attr(t, a, "platform").Value.String(); got != string(live.Douyu) {
		t.Fatalf("platform=%q", got)
	}
}

// 业务包不必 import diag，只要给哨兵实现 DiagClass() 就能被正确归类。
// playback.ErrUnsupported 用的就是这条路径（否则 diag 与 playback 会互相 import）。
type unsupportedErr struct{}

func (unsupportedErr) Error() string     { return "找不到 libmpv-2.dll" }
func (unsupportedErr) DiagClass() string { return "unsupported" }

func TestErrClassedSentinel(t *testing.T) {
	err := fmt.Errorf("%w: 找不到 libmpv-2.dll", unsupportedErr{})
	a := Err(err)
	if got := attr(t, a, "class").Value.String(); got != "unsupported" {
		t.Fatalf("class=%q", got)
	}
}

func TestErrURLCarriesHostNotQuery(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://cdn.example.com/live.flv?token=SECRET", Err: context.DeadlineExceeded}
	a := Err(err)
	if got := attr(t, a, "class").Value.String(); got != "timeout" {
		t.Fatalf("class=%q", got)
	}
	host := attr(t, a, "host").Value.String()
	if host != "https://cdn.example.com" {
		t.Fatalf("host=%q", host)
	}
	text := attr(t, a, "text").Value.String()
	if strings.Contains(text, "SECRET") {
		t.Fatalf("text 泄漏 token: %q", text)
	}
}

func TestErrCanceled(t *testing.T) {
	a := Err(context.Canceled)
	if got := attr(t, a, "class").Value.String(); got != "canceled" {
		t.Fatalf("class=%q", got)
	}
}

func TestErrNil(t *testing.T) {
	if a := Err(nil); a.Key != "" {
		t.Fatalf("nil 应返回零 Attr: %v", a)
	}
}

func TestEmitAddsEvent(t *testing.T) {
	var buf strings.Builder
	h := slog.NewTextHandler(&buf, nil)
	l := slog.New(h)
	Emit(context.Background(), l, slog.LevelInfo, EvSessionFetch, "取流完成", "live", true)
	if !strings.Contains(buf.String(), "event=session.fetch") {
		t.Fatalf("Emit 没写 event: %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"debug", slog.LevelDebug, true},
		{"INFO", slog.LevelInfo, true},
		{"", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"nope", slog.LevelInfo, false},
	} {
		got, ok := ParseLevel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ParseLevel(%q)=%v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
