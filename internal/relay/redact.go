package relay

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// relayURIPattern 匹配日志里的 URL。ffmpeg 的 stderr 会把打不开的上游地址
// 原样打出来，而直播 CDN 的地址带着签名和时效 token，不能进日志。
var relayURIPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://[^\s"'<>\\)]+`)

// scrubText 抹掉文本里的 URL 和本次会话的本地 token。
func scrubText(token, text string) string {
	if text == "" {
		return text
	}
	out := relayURIPattern.ReplaceAllString(text, "<url>")
	if token != "" {
		out = strings.ReplaceAll(out, token, "<token>")
	}
	return out
}

// redactError 返回一个脱敏后的错误，切断原始错误链。
//
// http 客户端的错误会带完整 URL（含签名 query），而 relay 的错误会一路传到
// 界面日志里。取消和超时保留可判断的类型，其余错误只保留脱敏文本。
func redactError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return errors.New(scrubText("", err.Error()))
}

// truncateUTF8 按字节上限截断，但不切在多字节字符中间。
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}
	return s[:limit]
}
