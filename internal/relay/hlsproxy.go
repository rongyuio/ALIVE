package relay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// proxyPlaylistLimit 是代理播放列表的大小上限。直播媒体列表通常不到
	// 10KB，留 4MB 只为挡住异常响应，不影响正常播放。
	proxyPlaylistLimit = 4 << 20
	// proxySegmentLimit 是单个分片的大小上限。2K 直播分片通常 1~3MB。
	proxySegmentLimit = 64 << 20
	// proxyResourceKeep 是保留的资源映射条数。播放器可能还在拉上一条播放
	// 列表里的分片，映射不能随列表刷新立刻丢弃。
	proxyResourceKeep = 400

	proxyPlaylistTimeout = 10 * time.Second
	proxySegmentTimeout  = 20 * time.Second
)

var (
	// errProxyUnsupported 表示上游播放列表不在快路径支持范围内：多 variant master、
	// BYTERANGE / 未知容器、加密分片、低延迟 HLS 等。调用方必须回退 FFmpeg，不能假装支持。
	errProxyUnsupported = errors.New("上游播放列表不在 HLS 代理支持范围")
	// errProxyFetch 表示代理回源失败（网络、HTTP 状态、体积超限）。
	errProxyFetch = errors.New("HLS 代理回源失败")
)

// proxyResource 仅保存完整上游资源；Range 由播放器请求决定。
type proxyResource struct {
	url string
	ext string
}

// hlsProxy 是 HLS 透明代理：只重写播放列表里的资源地址，分片字节原样转发。
// 不做转封装、不重新切片，播放器拿到的分片与上游逐字节一致。
type hlsProxy struct {
	token   string
	headers []string
	client  *http.Client

	playlistMu sync.Mutex
	stopOnce   sync.Once
	mu         sync.Mutex
	upstream   *url.URL
	resources  map[string]*proxyResource
	order      []string
	current    map[string]bool
	// pending 是预检阶段已经拉好并重写的首份播放列表。播放器第一次打开
	// 时直接用它，省掉一次回源。
	pending []byte

	server   *http.Server
	listener net.Listener
	cancel   context.CancelFunc
	url      string
}

// newHLSProxy 预检上游播放列表并起一个本地只读代理。
//
// 预检放在监听端口之前：不支持的 master、加密、低延迟这些不支持的形式会在这里失败，
// 此时没有任何本地资源需要清理，调用方可以直接回退 FFmpeg。
func newHLSProxy(ctx context.Context, upstream string, headers []string) (*hlsProxy, error) {
	base, err := url.Parse(trimSpace(upstream))
	if err != nil {
		return nil, fmt.Errorf("%w: 上游地址无法解析", errProxyUnsupported)
	}
	if !validProxyURL(base) {
		return nil, fmt.Errorf("%w: 上游不是 http(s)", errProxyUnsupported)
	}

	p := &hlsProxy{
		token:     randomToken(),
		headers:   append([]string(nil), headers...),
		resources: make(map[string]*proxyResource),
		client: &http.Client{Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			ForceAttemptHTTP2:   true,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		}},
	}

	// 只关闭本代理拥有的 Transport；任何预检失败均需释放已建立的连接。
	accepted := false
	defer func() {
		if !accepted {
			p.stop()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	fetchCtx, cancelFetch := context.WithTimeout(ctx, proxyPlaylistTimeout)
	body, finalURL, err := p.fetchMediaPlaylist(fetchCtx, base.String())
	cancelFetch()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	rewritten, err := p.rewrite(body, finalURL)
	if err != nil {
		return nil, err
	}
	p.upstream = finalURL
	p.pending = rewritten

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		// 预检已经建立了到上游的连接，这里失败退出就一并放掉，不留空闲连接。
		p.client.CloseIdleConnections()
		return nil, fmt.Errorf("%w: 监听本地端口失败", errProxyFetch)
	}
	runCtx, cancelRun := context.WithCancel(ctx)
	p.cancel = cancelRun
	p.listener = listener
	// token 放在路径里而不是 query：IMFMediaEngine 按 URL 后缀判断媒体类型，
	// 带 ?t= 的 m3u8 会被判成未知格式。资源本身只有不透明标识，客户端无法
	// 借这个地址去访问任意上游 URL。
	p.url = fmt.Sprintf("http://%s/t/%s/index.m3u8", listener.Addr().String(), p.token)
	p.server = &http.Server{
		Handler:           http.HandlerFunc(p.serve),
		ReadHeaderTimeout: 5 * time.Second,
		// 取消时让正在下载的分片回源请求一起结束，不留挂着的 goroutine。
		BaseContext: func(net.Listener) context.Context { return runCtx },
	}
	go func() {
		if err := p.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("HLS 代理本地服务退出", "error", redactError(err))
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accepted = true
	go func() { <-runCtx.Done(); p.stop() }()
	return p, nil
}

// stop 关闭本地服务并中断所有回源请求。可重复调用。
func (p *hlsProxy) stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
		if p.server != nil {
			_ = p.server.Close()
		}
		if p.listener != nil {
			_ = p.listener.Close()
		}
		if p.client != nil {
			p.client.CloseIdleConnections()
		}
	})
}

func (p *hlsProxy) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	prefix := "/t/" + p.token + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	if rest == "index.m3u8" {
		p.servePlaylist(w, r)
		return
	}
	id, _, ok := parseResourcePath(rest)
	if !ok {
		http.NotFound(w, r)
		return
	}
	p.serveResource(w, r, id)
}

func (p *hlsProxy) servePlaylist(w http.ResponseWriter, r *http.Request) {
	p.playlistMu.Lock()
	defer p.playlistMu.Unlock()
	body := p.takePending()
	if body == nil {
		ctx, cancel := context.WithTimeout(r.Context(), proxyPlaylistTimeout)
		raw, finalURL, err := p.fetchMediaPlaylist(ctx, p.upstreamURL())
		cancel()
		if err != nil {
			slog.Warn("HLS 代理刷新播放列表失败", "error", redactError(err))
			http.Error(w, "upstream playlist", http.StatusBadGateway)
			return
		}
		body, err = p.rewrite(raw, finalURL)
		if err != nil {
			slog.Warn("HLS 代理无法重写播放列表", "error", err)
			http.Error(w, "unsupported playlist", http.StatusBadGateway)
			return
		}
		p.mu.Lock()
		p.upstream = finalURL
		p.mu.Unlock()
	}
	h := w.Header()
	h.Set("Content-Type", "application/vnd.apple.mpegurl")
	h.Set("Cache-Control", "no-store")
	h.Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(body)
}

func (p *hlsProxy) serveResource(w http.ResponseWriter, r *http.Request, id string) {
	res := p.lookup(id)
	if res == nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), proxySegmentTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, r.Method, res.url, nil)
	if err != nil {
		http.Error(w, "bad resource", http.StatusBadGateway)
		return
	}
	applyUpstreamHeaders(req, p.headers)
	// 不接受调用方提供的 URL，只转发播放器对已登记完整资源的 Range。
	req.Header.Del("Range")
	if value := r.Header.Get("Range"); value != "" {
		req.Header.Set("Range", value)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		slog.Warn("HLS 代理回源分片失败", "error", redactError(err))
		http.Error(w, "upstream segment", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		// 不把上游错误正文或重定向地址带给本地客户端。
		http.Error(w, "upstream status", http.StatusBadGateway)
		return
	}
	if resp.ContentLength > proxySegmentLimit {
		http.Error(w, "upstream too large", http.StatusBadGateway)
		return
	}

	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Accept-Ranges", "bytes")
	// 部分 CDN 把 fMP4 初始化段错误标成 video/MP2T，MF 会在取媒体分片前
	// 就拒绝。资源已通过容器后缀白名单，按容器输出一致的媒体类型。
	h.Set("Content-Type", proxyContentType(res.ext))
	for _, key := range []string{"Content-Range", "Content-Length", "Content-Encoding"} {
		if v := resp.Header.Get(key); v != "" {
			h.Set(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if r.Method == http.MethodHead {
		return
	}
	// 未知长度先流式传到上限，再多读一个字节；超限或读写失败时中止 HTTP
	// 响应，客户端不能把截断的内容当成完整分片。
	n, err := io.CopyN(w, resp.Body, proxySegmentLimit)
	if err != nil && err != io.EOF {
		panic(http.ErrAbortHandler)
	}
	if n == proxySegmentLimit {
		var extra [1]byte
		count, readErr := io.ReadFull(resp.Body, extra[:])
		if count != 0 || readErr != io.EOF {
			panic(http.ErrAbortHandler)
		}
	}
}

func (p *hlsProxy) upstreamURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.upstream == nil {
		return ""
	}
	return p.upstream.String()
}

func (p *hlsProxy) lookup(id string) *proxyResource {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resources[id]
}

func (p *hlsProxy) takePending() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	body := p.pending
	p.pending = nil
	return body
}

// fetchPlaylist 拉一份播放列表，返回内容和跟随重定向后的最终地址。
func (p *hlsProxy) fetchPlaylist(ctx context.Context, client *http.Client, rawURL string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: 请求构造失败", errProxyFetch)
	}
	applyUpstreamHeaders(req, p.headers)
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errProxyFetch, playlistFetchError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%w: 上游状态 %d", errProxyFetch, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, proxyPlaylistLimit+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errProxyFetch, playlistFetchError(err))
	}
	if len(body) > proxyPlaylistLimit {
		return nil, nil, fmt.Errorf("%w: 播放列表超过 %d 字节", errProxyFetch, proxyPlaylistLimit)
	}
	base := resp.Request.URL
	if base == nil {
		base = req.URL
	}
	return body, base, nil
}

// rewrite 仅接受明确 TS 或带完整 MAP 的 fMP4 媒体列表。解析失败不提交映射，防止半份
// 列表污染当前播放窗口；当前和上一窗口的资源均保留。
func (p *hlsProxy) rewrite(playlist []byte, base *url.URL) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	unsupported := func() ([]byte, error) { return nil, errProxyUnsupported }
	lines := strings.Split(strings.ReplaceAll(string(playlist), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "#EXTM3U" {
		return unsupported()
	}
	var out bytes.Buffer
	out.WriteString("#EXTM3U\n")
	staged := make(map[string]*proxyResource)
	var stagedOrder []string
	resource := func(raw string) (string, error) {
		if strings.Contains(raw, "{$") {
			return "", errProxyUnsupported
		}
		ref, err := url.Parse(raw)
		if err != nil {
			return "", errProxyUnsupported
		}
		abs := base.ResolveReference(ref)
		if !validProxyURL(abs) {
			return "", errProxyUnsupported
		}
		// 由会话密钥生成稳定的不透明 ID，即使映射曾被淘汰，再次出现仍用同一 URI。
		hash := hmac.New(sha256.New, []byte(p.token))
		hash.Write([]byte(abs.String()))
		id := hex.EncodeToString(hash.Sum(nil))
		ext := strings.ToLower(path.Ext(abs.Path))
		// 只接受明确的 TS / fMP4 资源，未知后缀不猜测容器。
		if ext != ".ts" && ext != ".m4s" && ext != ".mp4" {
			return "", errProxyUnsupported
		}
		if _, ok := staged[id]; !ok {
			stagedOrder = append(stagedOrder, id)
		}
		staged[id] = &proxyResource{url: abs.String(), ext: ext}
		return "/t/" + p.token + "/r/" + id + ext, nil
	}
	pending, target, segments := false, false, 0
	startSeen := false
	mapSeen := false
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			if !pending {
				return unsupported()
			}
			local, err := resource(line)
			if err != nil {
				return unsupported()
			}
			out.WriteString(local + "\n")
			// fMP4 媒体分片需要此前声明的初始化段；不能把缺 init 的列表
			// 交给播放器后才等待失败。
			if path.Ext(local) != ".ts" && !mapSeen {
				return unsupported()
			}
			pending = false
			segments++
			continue
		}
		tag, value, colon := strings.Cut(line, ":")
		switch tag {
		case "#EXTINF":
			duration, _, comma := strings.Cut(value, ",")
			number, err := strconv.ParseFloat(duration, 64)
			if !colon || !comma || pending || err != nil || number <= 0 || math.IsInf(number, 0) || math.IsNaN(number) {
				return unsupported()
			}
			pending = true
			// 标题不影响媒体播放，去掉任意文本避免夹带签名地址。
			line = "#EXTINF:" + duration + ","
		case "#EXT-X-VERSION", "#EXT-X-TARGETDURATION", "#EXT-X-MEDIA-SEQUENCE", "#EXT-X-DISCONTINUITY-SEQUENCE":
			number, err := strconv.ParseUint(value, 10, 64)
			if !colon || err != nil {
				return unsupported()
			}
			if tag == "#EXT-X-TARGETDURATION" {
				if number == 0 {
					return unsupported()
				}
				target = true
			}
		case "#EXT-X-ENDLIST", "#EXT-X-DISCONTINUITY", "#EXT-X-INDEPENDENT-SEGMENTS", "#EXT-X-GAP":
			if colon {
				return unsupported()
			}
		case "#EXT-X-START":
			if !colon || startSeen || !validPlaylistStart(value) {
				return unsupported()
			}
			startSeen = true
		case "#EXT-BILI-AUX":
			// 仅剔除已观测的平台辅助标签，不将其内容或签名转发给播放器。
			continue
		case "#EXT-X-PLAYLIST-TYPE":
			if value != "EVENT" && value != "VOD" {
				return unsupported()
			}
		case "#EXT-X-PROGRAM-DATE-TIME":
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return unsupported()
			}
		case "#EXT-X-KEY":
			if value != "METHOD=NONE" {
				return unsupported()
			}
		case "#EXT-X-MAP":
			// 只接受单个带引号的 URI 属性。BYTERANGE、重复 URI 和其他
			// 属性仍回退；资源始终代表完整文件，Range 由播放器决定。
			attrs, ok := strictPlaylistAttributes(value)
			uri := attrs["URI"]
			if !colon || !ok || len(attrs) != 1 || len(uri) < 3 || uri[0] != '"' || uri[len(uri)-1] != '"' {
				return unsupported()
			}
			local, err := resource(uri[1 : len(uri)-1])
			if err != nil {
				return unsupported()
			}
			line = `#EXT-X-MAP:URI="` + local + `"`
			mapSeen = true
		default:
			// 所有未实现的扩展标签（含 master、LL-HLS、变量、URI 属性）拒绝。
			if strings.HasPrefix(tag, "#EXT") {
				return unsupported()
			}
			// 普通注释无需传给播放器，防止其中带有签名 URL。
			continue
		}
		out.WriteString(line + "\n")
	}
	if pending || !target || segments == 0 || len(staged) > proxyResourceKeep/2 {
		return unsupported()
	}
	if p.resources == nil {
		p.resources = make(map[string]*proxyResource)
	}
	protected := make(map[string]bool)
	for id := range p.current {
		protected[id] = true
	}
	for _, id := range stagedOrder {
		protected[id] = true
		if _, ok := p.resources[id]; !ok {
			p.order = append(p.order, id)
		}
		p.resources[id] = staged[id]
	}
	order := p.order[:0]
	for _, id := range p.order {
		if len(p.resources) > proxyResourceKeep && !protected[id] {
			delete(p.resources, id)
		} else {
			order = append(order, id)
		}
	}
	p.order = order
	p.current = make(map[string]bool, len(staged))
	for id := range staged {
		p.current[id] = true
	}
	return out.Bytes(), nil
}

func validProxyURL(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == ""
}

// parseResourcePath 解析 "r/<id><ext>"。id 只认十六进制字符，客户端没法借路径
// 指定任意上游资源。
func parseResourcePath(rest string) (string, string, bool) {
	if !strings.HasPrefix(rest, "r/") {
		return "", "", false
	}
	name := strings.TrimPrefix(rest, "r/")
	ext := strings.ToLower(path.Ext(name))
	if ext == "" {
		return "", "", false
	}
	id := name[:len(name)-len(ext)]
	if id == "" {
		return "", "", false
	}
	for i := 0; i < len(id); i++ {
		if !(id[i] >= '0' && id[i] <= '9' || id[i] >= 'a' && id[i] <= 'f') {
			return "", "", false
		}
	}
	return id, ext, true
}

func applyUpstreamHeaders(req *http.Request, headers []string) {
	for _, h := range headers {
		name, value, ok := strings.Cut(trimSpace(h), ":")
		if !ok {
			continue
		}
		name = trimSpace(name)
		if name == "" {
			continue
		}
		req.Header.Set(name, trimSpace(value))
	}
}

func proxyContentType(ext string) string {
	switch ext {
	case ".ts":
		return "video/mp2t"
	case ".m4s", ".mp4":
		return "video/mp4"
	}
	return "application/octet-stream"
}
