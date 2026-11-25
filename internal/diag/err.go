package diag

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"strings"

	"alive/internal/live"
)

// Err 把错误拆成可安全落盘的字段。
//
// 只留一个 class 等于没记：同一个 upstream 可能是 403 风控、412 签名失效、
// 429 限流或 5xx。class 用来分组，op/status/host/text 才是定位用的证据。
//
// 返回的是一个 slog.Group("err", ...)。调用点必须把它作为独立 attr 传入：
//
//	slog.Warn("...", diag.Err(err))
//
// 不要写成 "err", diag.Err(err)——slog 的 argsToAttr 会把 Attr 当普通值包进
// KindAny，group 结构和脱敏都会失效。
func Err(err error) slog.Attr {
	if err == nil {
		return slog.Attr{}
	}
	attrs := []any{
		slog.String(ErrClassField, ErrClass(err)),
		slog.String(ErrTypeField, fmt.Sprintf("%T", err)),
	}
	if up, ok := errors.AsType[*live.UpstreamError](err); ok {
		attrs = append(attrs,
			slog.String(ErrPlatformField, string(up.Platform)),
			slog.String(ErrOpField, up.Op),
			slog.Int(ErrStatusField, up.Status),
			slog.String(ErrTextField, Sanitize(up.Message)),
		)
	}
	if ue, ok := errors.AsType[*url.Error](err); ok {
		attrs = append(attrs,
			slog.String(ErrOpField, ue.Op),
			slog.String(ErrHostField, Host(ue.URL)),
		)
	}
	if ne, ok := errors.AsType[net.Error](err); ok {
		attrs = append(attrs, slog.Bool(ErrTimeoutField, ne.Timeout()))
	}
	if text := Sanitize(err.Error()); text != "" {
		attrs = append(attrs, slog.String(ErrTextField, text))
	}
	return slog.Group(ErrField, attrs...)
}

// classed 让错误自带分组标签，避免 diag 反向 import 业务包（会造成 import cycle）。
type classed interface {
	error
	DiagClass() string
}

// ErrClass 是错误的分组标签。它只用于聚合，不替代 Err 的细节。
func ErrClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, live.ErrBlocked):
		return "blocked"
	case errors.Is(err, live.ErrUnsupported):
		return "unsupported"
	}
	// 业务包可以给自己的哨兵实现 DiagClass()，diag 不必反向 import 它们。
	if c, ok := errors.AsType[classed](err); ok {
		return c.DiagClass()
	}
	if _, ok := errors.AsType[*live.UpstreamError](err); ok {
		return "upstream"
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
	urlRe    = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	secretRe = regexp.MustCompile(`(?i)\b(token|sign|sig|auth|secret|session|cookie|sessdata|qrcode_key|wssecret|wstime|txsecret|did|key)=([^&\s"']+)`)
	userRe   = regexp.MustCompile(`(?i)([A-Z]:\\Users\\)[^\\\s"']+`)
	ctrlRe   = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

// Sanitize 是日志的唯一脱敏出口：URL 去 query/fragment、敏感参数打码、
// Windows 用户名目录换成 ***、换行压平、超长截断。
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
