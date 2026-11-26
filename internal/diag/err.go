package diag

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/rs/zerolog"
)

// Err 把错误拆成 zerolog 的嵌套对象，挂在 err 键上：
//
//	e.Dict(diag.ErrField, diag.Err(err))
//
// 只留一个 class 等于没记：同一个 upstream 可能是 403 风控、412 签名失效、
// 429 限流或 5xx。class 用来分组，op/status/host/text 才是定位用的证据。
func Err(err error) *zerolog.Event {
	d := zerolog.Dict()
	if err == nil {
		return d
	}
	d.Str(ErrClassField, ErrClass(err))
	d.Str(ErrTypeField, fmt.Sprintf("%T", err))
	// 业务错误自己交出证据，diag 不认识任何平台的类型。
	//
	// 它给的 text 是**权威**：那通常是平台原话（"forbidden"），比包了一层的
	// err.Error() 干净。下面只在它没给的时候才回落到整条消息。
	hasText := false
	if det, ok := errors.AsType[detailed](err); ok {
		platform, op, status, text := det.DiagError()
		if platform != "" {
			d = d.Str(ErrPlatformField, platform)
		}
		if op != "" {
			d = d.Str(ErrOpField, op)
		}
		if status != 0 {
			d = d.Int(ErrStatusField, status)
		}
		if text != "" {
			d = d.Str(ErrTextField, Sanitize(text))
			hasText = true
		}
	}
	if ue, ok := errors.AsType[*url.Error](err); ok {
		d = d.Str(ErrOpField, ue.Op).Str(ErrHostField, Host(ue.URL))
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		d = d.Bool(ErrTimeoutField, ne.Timeout())
	}
	if !hasText {
		if text := Sanitize(err.Error()); text != "" {
			d = d.Str(ErrTextField, text)
		}
	}
	return d
}

// classed 让错误自带分组标签。业务包给自己的哨兵实现它，diag 只认接口——
// 这样 diag 不必反向 import 业务包（那会造成 import cycle，也会让「日志语言」
// 反过来依赖某个平台的具体错误类型）。
type classed interface {
	error
	DiagClass() string
}

// detailed 让错误能交出结构化的证据：平台、操作、状态码、原文。
//
// 这些字段以前是靠 diag 认识 live.UpstreamError 才拿得到的。现在由错误自己
// 给出，diag 只负责脱敏和落盘。
type detailed interface {
	error
	DiagError() (platform, op string, status int, text string)
}

// ErrClass 是错误的分组标签。它只用于聚合，不替代 Err 的细节。
func ErrClass(err error) string {
	if err == nil {
		return ""
	}
	// 标准库的哨兵先认：它们最确定。
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	// 业务包自带标签的走自己的（blocked / unsupported / upstream …）。
	if c, ok := errors.AsType[classed](err); ok {
		return c.DiagClass()
	}
	// 文件系统错误必须排在 net.Error 前面：syscall.Errno 实现了 net.Error
	// （它有 Timeout()/Temporary()），而 *fs.PathError 会解包到 Errno。
	// 于是「Access is denied」「文件找不到」全被归成 network，排障时人会
	// 顺着网络查一整圈，而问题在磁盘或权限上。
	if _, ok := errors.AsType[*fs.PathError](err); ok {
		return "fs"
	}
	if _, ok := errors.AsType[*os.LinkError](err); ok {
		return "fs"
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return "network"
	}
	return "error"
}

// ErrText 是给日志之外的地方用的脱敏摘要（例如 UI 提示、bundle 清单）。
func ErrText(err error) string {
	if err == nil {
		return ""
	}
	return Sanitize(err.Error())
}

var (
	// 直播地址不只 http：弹幕走 wss，部分平台拉流是 rtmp。只认 http(s)
	// 会让带签名的 rtmp URL 整条原样落盘。
	urlRe    = regexp.MustCompile(`(?i)(?:https?|rtmps?|wss?)://[^\s"'<>]+`)
	secretRe = regexp.MustCompile(`(?i)\b([a-z0-9_\-]*(?:token|secret|signature|sign|sig|auth|session|cookie|sessdata|qrcode_key|wssecret|wstime|txsecret|did|key|csrf|password|passwd|pwd|hdnts|md5|expires|bili_jct|buvid3|buvid4)[a-z0-9_\-]*)=([^&\s"';,]+)`)
	// Windows 用户名目录两种写法：反斜杠来自 os 错误，正斜杠来自 Go 源码里
	// 拼出来的路径和日志里的 source。
	userRe = regexp.MustCompile(`(?i)([A-Z]:[\\/]Users[\\/])[^\\/\s"']+`)
	// 头部的凭据不走 `key=` 形式。Bearer/Basic 后面跟的是完整的凭据本体。
	// 下限 4 而不是 8：短 token 一样是凭据，宁可多打码。
	bearerRe = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{4,}`)
	ctrlRe   = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

// Sanitize 是日志的唯一脱敏出口：URL 去 query/fragment/userinfo、敏感参数
// 和头部凭据打码、Windows 用户名目录换成 ***、换行压平、超长截断。
//
// 它不追求「看起来一样」，只追求「不会把 cookie/token/签名带出去」。
// 所有写进日志的字符串属性都经过它——不是只在错误路径上。
func Sanitize(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = ctrlRe.ReplaceAllString(s, " ")
	s = userRe.ReplaceAllString(s, "${1}***")
	s = urlRe.ReplaceAllStringFunc(s, stripQuery)
	s = bearerRe.ReplaceAllString(s, "${1} ***")
	s = secretRe.ReplaceAllString(s, "${1}=***")
	s = strings.Join(strings.Fields(s), " ")
	const max = 240
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "…"
	}
	return s
}

func stripQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		// 解析不了就按字符截断，宁可少留也不原样带出去。
		if i := strings.IndexAny(raw, "?#"); i >= 0 {
			return raw[:i] + "?***"
		}
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	// userinfo 和 query 一样是凭据：推流/拉流地址里常带用户名密码，只清 query 会把密码原样带出去。
	u.User = nil
	return u.String()
}

// Host 只保留 scheme+host。日志里不放路径和 query：直播地址的 query 就是签名。
func Host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
