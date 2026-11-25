package diag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/rs/zerolog"
)

// errFields 把 Err(err) 真的序列化出来，再按 JSON 读回字段。
//
// 不用「取 Event 内部字段」那种写法：zerolog 的事件只能写不能读，而这恰好是
// 想要的——落盘形状才是读侧（alive-diag、指标）真正看到的东西。
func errFields(t *testing.T, err error) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	l := zerolog.New(&buf)
	l.Info().Dict(ErrField, Err(err)).Msg("x")
	var obj map[string]any
	if e := json.Unmarshal(buf.Bytes(), &obj); e != nil {
		t.Fatalf("序列化失败: %v (%q)", e, buf.String())
	}
	m, ok := obj[ErrField].(map[string]any)
	if !ok {
		t.Fatalf("没有 err 对象: %s", buf.String())
	}
	return m
}

func str(f map[string]any, k string) string {
	s, _ := f[k].(string)
	return s
}

func num(f map[string]any, k string) int {
	n, _ := f[k].(float64)
	return int(n)
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

// URL 里的 userinfo 也是凭据。rtmp 推/拉流地址带用户名密码是常态，
// 只清 query 会把密码原样落盘。
func TestSanitizeURLUserinfo(t *testing.T) {
	for _, in := range []string{
		"fetch https://alice:x@cdn.example.com/live.flv?token=abc failed",
		"pull rtmp://alice:y@push.example.com/live/stream failed",
	} {
		out := Sanitize(in)
		if strings.Contains(out, ":x@") || strings.Contains(out, ":y@") {
			t.Fatalf("userinfo 密码漏了: %q → %q", in, out)
		}
		if strings.Contains(out, "alice") {
			t.Fatalf("userinfo 用户名漏了: %q → %q", in, out)
		}
		if !strings.Contains(out, "example.com") {
			t.Fatalf("host 应该保留: %q → %q", in, out)
		}
	}
}

// 正斜杠的 Windows 路径也要脱敏：err.text 里两种斜杠都会出现。
func TestSanitizeSlashPath(t *testing.T) {
	out := Sanitize(`open C:/Users/alice/AppData/Roaming/alive/state.json failed`)
	if strings.Contains(out, "alice") {
		t.Fatalf("正斜杠路径没脱敏: %q", out)
	}
	if !strings.Contains(out, "C:/Users/***") {
		t.Fatalf("正斜杠路径应替换成 ***: %q", out)
	}
}

// 头部凭据和 cookie 串里的多个参数：只脱第一个等于没脱。
func TestSanitizeHeaderSecrets(t *testing.T) {
	for _, tc := range []struct {
		in   string
		bad  []string
		want string
	}{
		{"Authorization: Bearer abcdef1234567890", []string{"abcdef1234567890"}, "Bearer ***"},
		{"Cookie: SESSDATA=deadbeef; bili_jct=csrf123; buvid3=buv456", []string{"deadbeef", "csrf123", "buv456"}, "SESSDATA=***"},
	} {
		out := Sanitize(tc.in)
		for _, b := range tc.bad {
			if strings.Contains(out, b) {
				t.Fatalf("%q 没脱掉 %q: %q", tc.in, b, out)
			}
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%q 期望包含 %q，得到 %q", tc.in, tc.want, out)
		}
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

// upstreamErr 是假的平台错误：diag 只认接口，不认识任何平台的具体类型。
//
// 这正是把「diag 反向 import live」拆掉之后的样子——测试也不再需要真实业务包。
type upstreamErr struct {
	platform, op, text string
	status             int
}

func (e upstreamErr) Error() string     { return e.text }
func (e upstreamErr) DiagClass() string { return "upstream" }
func (e upstreamErr) DiagError() (platform, op string, status int, text string) {
	return e.platform, e.op, e.status, e.text
}

func TestErrUpstreamKeepsStatus(t *testing.T) {
	err := fmt.Errorf("包装: %w", upstreamErr{platform: "douyu", op: "stream", status: 403, text: "forbidden"})
	f := errFields(t, err)
	if got := str(f, "class"); got != "upstream" {
		t.Fatalf("class=%q", got)
	}
	if got := num(f, "status"); got != 403 {
		t.Fatalf("status=%d", got)
	}
	if got := str(f, "op"); got != "stream" {
		t.Fatalf("op=%q", got)
	}
	if got := str(f, "platform"); got != "douyu" {
		t.Fatalf("platform=%q", got)
	}
	if got := str(f, "text"); got != "forbidden" {
		t.Fatalf("text=%q", got)
	}
}

// 业务包不必 import diag，只要给哨兵实现 DiagClass() 就能被正确归类。
// playback.ErrUnsupported 用的就是这条路径（否则 diag 与 playback 会互相 import）。
type unsupportedErr struct{}

func (unsupportedErr) Error() string     { return "找不到 libmpv-2.dll" }
func (unsupportedErr) DiagClass() string { return "unsupported" }

func TestErrClassedSentinel(t *testing.T) {
	err := fmt.Errorf("%w: 找不到 libmpv-2.dll", unsupportedErr{})
	if got := str(errFields(t, err), "class"); got != "unsupported" {
		t.Fatalf("class=%q", got)
	}
}

func TestErrURLCarriesHostNotQuery(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://cdn.example.com/live.flv?token=SECRET", Err: context.DeadlineExceeded}
	f := errFields(t, err)
	if got := str(f, "class"); got != "timeout" {
		t.Fatalf("class=%q", got)
	}
	if host := str(f, "host"); host != "https://cdn.example.com" {
		t.Fatalf("host=%q", host)
	}
	if text := str(f, "text"); strings.Contains(text, "SECRET") {
		t.Fatalf("text 泄漏 token: %q", text)
	}
}

func TestErrCanceled(t *testing.T) {
	if got := str(errFields(t, context.Canceled), "class"); got != "canceled" {
		t.Fatalf("class=%q", got)
	}
}

// 文件系统错误不能归成 network。
//
// syscall.Errno 实现了 net.Error（它有 Timeout()/Temporary()），而
// *fs.PathError 会解包到 Errno——所以「Access is denied」曾经带着
// class=network 落盘，读的人会顺着网络查一整圈，问题其实在磁盘或权限上。
func TestErrClassFilesystem(t *testing.T) {
	cases := []error{
		&fs.PathError{Op: "open", Path: `C:\Users\x\AppData\Roaming\alive\state.json.tmp`, Err: syscall.ERROR_ACCESS_DENIED},
		&fs.PathError{Op: "open", Path: "state.json", Err: fs.ErrNotExist},
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: syscall.ERROR_ACCESS_DENIED},
	}
	for _, err := range cases {
		if got := ErrClass(err); got != "fs" {
			t.Fatalf("%v 的 class=%q，期望 fs", err, got)
		}
		if got := str(errFields(t, err), "class"); got != "fs" {
			t.Fatalf("落盘的 class=%q，期望 fs", got)
		}
	}
}

func TestErrNil(t *testing.T) {
	if f := errFields(t, nil); len(f) != 0 {
		t.Fatalf("nil 应返回空对象: %v", f)
	}
}

func TestEventCarriesEventKey(t *testing.T) {
	var buf bytes.Buffer
	l := zerolog.New(&buf)
	L()
	l.Info().Str(Event, EvSessionFetch).Bool("live", true).Msg("取流完成")
	if !strings.Contains(buf.String(), `"event":"session.fetch"`) {
		t.Fatalf("没写 event: %q", buf.String())
	}
}

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Level
		ok   bool
	}{
		{"debug", LevelDebug, true},
		{"INFO", LevelInfo, true},
		{"", LevelInfo, true},
		{"warn", LevelWarn, true},
		{"error", LevelError, true},
		{"nope", LevelInfo, false},
	} {
		got, ok := ParseLevel(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("ParseLevel(%q)=%v,%v want %v,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}
