package diag

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSanitize 钉住"密钥不进日志"的唯一出口。
//
// 每一条都是真实出现过的形态：带查询串的接口地址（query 就是签名）、
// 头部凭据、表单里的 secret、rtmp 推流地址、URL 里的用户名密码、
// Windows 用户目录、多行错误、超长堆栈。
func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "URL 的 query 整体丢掉",
			in:   "GET https://api.example.com/room?id=1&token=abc123 failed",
			want: "GET https://api.example.com/room failed",
		},
		{
			name: "rtmp 推流地址的签名也去掉",
			in:   "rtmp://push.example.com/live/abc?txSecret=feedfacecafe",
			want: "rtmp://push.example.com/live/abc",
		},
		{
			name: "URL 里的用户名密码不落盘",
			in:   "https://ry:fake-cred@example.com/path?x=1",
			want: "https://example.com/path",
		},
		{
			name: "Bearer 头部打码",
			in:   "Authorization: Bearer abcdef123456",
			want: "Authorization: Bearer ***",
		},
		{
			name: "Basic 头部打码",
			in:   "Authorization: Basic fake-cred",
			want: "Authorization: Basic ***",
		},
		{
			name: "secret= 打码，后面的参数保留",
			in:   "secret=fake-cred",
			want: "secret=***",
		},
		{
			name: "多行压成一行",
			in:   "first\nsecond\r\nthird",
			want: "first second third",
		},
		{
			name: "控制字符换成空格",
			in:   "a\x00b\x07c",
			want: "a b c",
		},
		{
			name: "Windows 用户目录换成 ***",
			in:   `open C:\Users\ry\secret.txt: denied`,
			want: `open C:\Users\***\secret.txt: denied`,
		},
		{
			name: "空串原样返回",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Sanitize(c.in); got != c.want {
				t.Errorf("Sanitize(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

// TestSanitizeTruncatesLongText 超长文本截到 240 个字符（按 rune 算，不撕裂汉字），
// 末尾留一个省略号。
func TestSanitizeTruncatesLongText(t *testing.T) {
	got := Sanitize(strings.Repeat("长", 300))
	if n := utf8.RuneCountInString(got); n != 241 {
		t.Errorf("截断后 %d 个字符，想要 241（240 + 省略号）：%q", n, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("截断后没有省略号：%q", got)
	}
}

// TestSanitizeLeavesOrdinaryTextAlone 普通文本不该被改动：脱敏不能把正常日志吃掉。
func TestSanitizeLeavesOrdinaryTextAlone(t *testing.T) {
	const in = "bilibili room 12345 loaded in 320 ms"
	if got := Sanitize(in); got != in {
		t.Errorf("Sanitize(%q) = %q，普通文本被改了", in, got)
	}
}
