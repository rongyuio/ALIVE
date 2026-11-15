package relay

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// 连同最终媒体列表最多三层，即最多解引用两个单 variant 包装。
const proxyPlaylistDepth = 3

// fetchMediaPlaylist 全链路复用同一个 context；重定向和嵌套列表均不重置预算。
// 只解开没有选择歧义的包装，不负责挑画质或拼装独立音轨。
func (p *hlsProxy) fetchMediaPlaylist(ctx context.Context, rawURL string) ([]byte, *url.URL, error) {
	seen := make(map[string]bool)
	client := *p.client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 || !validProxyURL(req.URL) || seen[req.URL.String()] {
			return errProxyUnsupported
		}
		seen[req.URL.String()] = true
		// 新的请求和每次重定向均补上平台所需头，不依赖 net/http 的跨域复制规则。
		applyUpstreamHeaders(req, p.headers)
		return nil
	}
	for depth := 0; depth < proxyPlaylistDepth; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		u, err := url.Parse(rawURL)
		if err != nil || !validProxyURL(u) || seen[u.String()] {
			return nil, nil, errProxyUnsupported
		}
		seen[u.String()] = true
		body, base, err := p.fetchPlaylist(ctx, &client, u.String())
		if err != nil {
			return nil, nil, err
		}
		next, wrapper, err := singleVariantURI(body)
		if err != nil {
			return nil, nil, err
		}
		if !wrapper {
			return body, base, nil
		}
		ref, err := url.Parse(next)
		if err != nil || strings.Contains(next, "{$") {
			return nil, nil, errProxyUnsupported
		}
		absolute := base.ResolveReference(ref)
		if !validProxyURL(absolute) {
			return nil, nil, errProxyUnsupported
		}
		rawURL = absolute.String()
	}
	return nil, nil, errProxyUnsupported
}

// singleVariantURI 只接受 EXTM3U、可选 VERSION、一个 STREAM-INF 和一个 URI。
// 任何其他扩展标签或属性都拒绝，不能误吞外部音轨、字幕、加密和 steering。
func singleVariantURI(body []byte) (string, bool, error) {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	wrapper := false
	for _, line := range lines {
		tag, _, _ := strings.Cut(strings.TrimSpace(line), ":")
		if tag == "#EXT-X-STREAM-INF" {
			wrapper = true
		}
	}
	if !wrapper {
		return "", false, nil
	}
	fail := func() (string, bool, error) { return "", true, errProxyUnsupported }
	if len(lines) == 0 || lines[0] != "#EXTM3U" {
		return fail()
	}
	version, stream, uri := false, false, ""
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			if !stream || uri != "" {
				return fail()
			}
			uri = line
			continue
		}
		tag, value, colon := strings.Cut(line, ":")
		switch tag {
		case "#EXT-X-VERSION":
			if !colon || version || !positiveInteger(value) {
				return fail()
			}
			version = true
		case "#EXT-X-STREAM-INF":
			if !colon || stream || !validSingleVariantAttributes(value) {
				return fail()
			}
			stream = true
		default:
			return fail()
		}
	}
	if !stream || uri == "" {
		return fail()
	}
	return uri, true, nil
}

// 属性值保留引号，由调用者按类型校验；重复、空值和不完整的引号直接拒绝。
func strictPlaylistAttributes(text string) (map[string]string, bool) {
	result := make(map[string]string)
	for text != "" {
		key, rest, ok := strings.Cut(text, "=")
		if !ok || key == "" || result[key] != "" {
			return nil, false
		}
		for _, c := range key {
			if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return nil, false
			}
		}
		value := ""
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, false
			}
			value, rest = rest[:end+2], rest[end+2:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				value, rest = rest, ""
			} else {
				value, rest = rest[:end], rest[end:]
			}
			if strings.ContainsAny(value, "\" \t\r\n") {
				return nil, false
			}
		}
		if value == "" {
			return nil, false
		}
		result[key] = value
		if rest == "" {
			break
		}
		if rest[0] != ',' || len(rest) == 1 {
			return nil, false
		}
		text = rest[1:]
	}
	return result, len(result) > 0
}

func positiveInteger(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			return false
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && number > 0
}

func playlistDecimal(value string, signed bool) bool {
	if signed && strings.HasPrefix(value, "-") {
		value = value[1:]
	}
	if value == "" {
		return false
	}
	dots, digits := 0, 0
	for _, c := range value {
		if c == '.' {
			dots++
		} else if c >= '0' && c <= '9' {
			digits++
		} else {
			return false
		}
	}
	if dots > 1 || digits == 0 {
		return false
	}
	number, err := strconv.ParseFloat(value, 64)
	return err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func validSingleVariantAttributes(text string) bool {
	attrs, ok := strictPlaylistAttributes(text)
	if !ok || !positiveInteger(attrs["BANDWIDTH"]) {
		return false
	}
	for key, value := range attrs {
		switch key {
		case "BANDWIDTH", "AVERAGE-BANDWIDTH":
			if !positiveInteger(value) {
				return false
			}
		case "PROGRAM-ID":
			// B 站的单档包装仍携带此数字字段，不参与档位或资源选择。
			if value != "0" && !positiveInteger(value) {
				return false
			}
		case "RESOLUTION":
			w, h, ok := strings.Cut(value, "x")
			if !ok || !positiveInteger(w) || !positiveInteger(h) {
				return false
			}
		case "FRAME-RATE":
			if !playlistDecimal(value, false) {
				return false
			}
		case "CODECS":
			if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
				return false
			}
			// 只认普通 AVC/AAC 组合；不因包装声明与上层 codec 不一致而误放行。
			for _, codec := range strings.Split(value[1:len(value)-1], ",") {
				if !strings.HasPrefix(codec, "avc1.") && !strings.HasPrefix(codec, "avc3.") && !strings.HasPrefix(codec, "mp4a.") {
					return false
				}
			}
		case "CLOSED-CAPTIONS":
			if value != "NONE" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validPlaylistStart(value string) bool {
	attrs, ok := strictPlaylistAttributes(value)
	if !ok || !playlistDecimal(attrs["TIME-OFFSET"], true) {
		return false
	}
	for key, value := range attrs {
		switch key {
		case "TIME-OFFSET":
		case "PRECISE":
			if value != "YES" && value != "NO" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// 跟随重定向时的拒绝错误不能经由 URL 脱敏丢失类别。
func playlistFetchError(err error) error {
	if errors.Is(err, errProxyUnsupported) {
		return errProxyUnsupported
	}
	return redactError(err)
}
