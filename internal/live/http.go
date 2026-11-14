package live

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// 各平台共用的一套 HTTP 细节：桌面 UA、超时、响应体上限、错误分类。
//
// 平台实现只管拼 URL、加自己需要的头、解自己的格式；
// 超时、风控识别、错误包装这些重复逻辑集中在这里，
// 免得三个平台各写一份还各漏一点。

const (
	// 上层给的是 20s 的整体预算，单次请求必须更短，
	// 否则一个卡住的域名会吃掉整次刷新的时间。
	httpTimeout = 15 * time.Second

	// 列表与分区响应都是几十 KB 的量级；8MB 只用于兜住异常响应。
	httpMaxBody = 8 << 20

	desktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
)

// apiClient 是一个平台的 HTTP 出口，自带 UA 与 Referer 默认值。
type apiClient struct {
	platform Platform
	http     *http.Client
	ua       string
	referer  string
}

func newAPIClient(platform Platform, referer string) *apiClient {
	return &apiClient{
		platform: platform,
		// 不复用全局 DefaultClient：各平台的连接池互相隔离，
		// 一个平台被拖慢时不影响另一个。
		http:    &http.Client{Timeout: httpTimeout},
		ua:      desktopUA,
		referer: referer,
	}
}

// header 是单次请求要额外带的头，可为 nil。
type header map[string]string

// get 发一个 GET 并返回响应体。op 是给错误信息用的中文动作名。
func (c *apiClient) get(ctx context.Context, op, url string, h header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: 构造请求失败: %w", op, err)
	}
	c.apply(req, h)
	return c.do(req, op)
}

// getWithCookies 和 get 一样，但把响应的 Set-Cookie 一并交回。
//
// 只有扫码登录用它：斗鱼的登录态就是登录回调响应头里的 Set-Cookie，
// 普通的 do() 只留响应体，把它丢掉了就没法把会话接上。
func (c *apiClient) getWithCookies(ctx context.Context, op, url string, h header) ([]byte, []*http.Cookie, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: 构造请求失败: %w", op, err)
	}
	c.apply(req, h)
	resp, err := c.http.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, fmt.Errorf("%s: 请求失败: %w", op, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return nil, nil, fmt.Errorf("%s: 读取响应失败: %w", op, err)
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusPreconditionFailed, http.StatusTooManyRequests:
		return nil, nil, fmt.Errorf("%w: %s HTTP %d", ErrBlocked, op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, &UpstreamError{
			Platform: c.platform,
			Op:       op,
			Status:   resp.StatusCode,
			Message:  snippet(body),
		}
	}
	return body, resp.Cookies(), nil
}

// getJSON 发 GET 并把响应体解成 out。
func (c *apiClient) getJSON(ctx context.Context, op, url string, out any, h header) error {
	body, err := c.get(ctx, op, url, h)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		// 解不出来通常是被换成了验证页或风控空页
		return fmt.Errorf("%w: %s 响应不是 JSON: %v", ErrBlocked, op, err)
	}
	return nil
}

// postForm 发一个 application/x-www-form-urlencoded POST。
func (c *apiClient) postForm(ctx context.Context, op, url string, body io.Reader, h header) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("%s: 构造请求失败: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.apply(req, h)
	return c.do(req, op)
}

func (c *apiClient) apply(req *http.Request, h header) {
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	if c.referer != "" {
		req.Header.Set("Referer", c.referer)
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
}

func (c *apiClient) do(req *http.Request, op string) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		// 超时与主动取消不算风控，原样交给上层区分
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%s: 请求失败: %w", op, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return nil, fmt.Errorf("%s: 读取响应失败: %w", op, err)
	}

	// 403 / 412 / 429 是各平台风控最常用的三个状态码；
	// 挑出来标成 ErrBlocked，上层才能提示「请求过于频繁」而不是「加载失败」。
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusPreconditionFailed, http.StatusTooManyRequests:
		return nil, fmt.Errorf("%w: %s HTTP %d", ErrBlocked, op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &UpstreamError{
			Platform: c.platform,
			Op:       op,
			Status:   resp.StatusCode,
			Message:  snippet(body),
		}
	}
	return body, nil
}

// snippet 截一小段响应体放进错误信息：够定位问题，又不会把整页 HTML 塞进日志。
func snippet(b []byte) string {
	const max = 120
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
