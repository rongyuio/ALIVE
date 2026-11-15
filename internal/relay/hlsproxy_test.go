package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// testHTTPClient 不走系统代理：测试只访问 httptest 的 127.0.0.1，
// 但环境里的代理变量会改变错误信息，干扰断言。
var testHTTPClient = &http.Client{
	Transport: &http.Transport{Proxy: nil},
	Timeout:   5 * time.Second,
}

func testProxy(t *testing.T, upstream string, headers []string) *hlsProxy {
	t.Helper()
	p, err := newHLSProxy(context.Background(), upstream, headers)
	if err != nil {
		t.Fatalf("newHLSProxy: %v", err)
	}
	t.Cleanup(p.stop)
	return p
}

func fetch(t *testing.T, target string, header http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	for name, values := range header {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	resp, err := testHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("请求 %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应: %v", err)
	}
	return resp, body
}

func fetchErr(target string) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := testHTTPClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	return err
}

// playlistPaths 取出重写后播放列表里的本地资源路径。
func playlistPaths(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "/t/") {
			out = append(out, line)
		}
	}
	return out
}

func resolveLocal(t *testing.T, p *hlsProxy, path string) string {
	t.Helper()
	base, err := url.Parse(p.url)
	if err != nil {
		t.Fatalf("解析本地地址: %v", err)
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: path}).String()
}

func TestProxyServesPlaylistAndSegmentsVerbatim(t *testing.T) {
	segmentA := []byte{0x47, 0x40, 0x00, 0x10, 0x00, 0xff, 0x1a, 0x2b}
	segmentB := []byte{0x00, 0x01, 0x02, 0xfe, 0xff, 0x47}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:7\n#EXTINF:3.000,\nseg-a.ts\n#EXTINF:3.000,\n/abs/seg-b.ts?sign=deadbeef\n")
		case "/seg-a.ts":
			w.Write(segmentA)
		case "/abs/seg-b.ts":
			if got := r.URL.Query().Get("sign"); got != "deadbeef" {
				t.Errorf("上游签名参数丢失: %q", got)
			}
			w.Write(segmentB)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	resp, playlist := fetch(t, p.url, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/vnd.apple.mpegurl" {
		t.Fatalf("本地播放列表类型 = %q", got)
	}
	text := string(playlist)
	if !strings.Contains(text, "#EXT-X-MEDIA-SEQUENCE:7") {
		t.Fatalf("播放列表标签被改动:\n%s", text)
	}
	if strings.Contains(text, "sign=deadbeef") {
		t.Fatalf("签名参数泄漏到本地播放列表:\n%s", text)
	}

	paths := playlistPaths(text)
	if len(paths) != 2 {
		t.Fatalf("本地资源数 = %d，正文:\n%s", len(paths), text)
	}
	for i, want := range [][]byte{segmentA, segmentB} {
		_, got := fetch(t, resolveLocal(t, p, paths[i]), nil)
		if !bytes.Equal(got, want) {
			t.Fatalf("第 %d 个分片字节被修改: %v", i, got)
		}
	}
}

func TestProxyRewritesRelativePaths(t *testing.T) {
	segment := []byte{0x47, 0x11, 0x22}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live/v1/index.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nmedia/seg0.ts\n")
		case "/live/v1/media/seg0.ts":
			w.Write(segment)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live/v1/index.m3u8", nil)
	_, playlist := fetch(t, p.url, nil)
	paths := playlistPaths(string(playlist))
	if len(paths) != 1 {
		t.Fatalf("本地资源数 = %d", len(paths))
	}
	_, got := fetch(t, resolveLocal(t, p, paths[0]), nil)
	if !bytes.Equal(got, segment) {
		t.Fatalf("相对路径解析错误，拿到 %v", got)
	}
}

func TestProxyFollowsRedirectBase(t *testing.T) {
	segment := []byte{0x47, 0x33, 0x44}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			http.Redirect(w, r, "/cdn/v2/live.m3u8", http.StatusFound)
		case "/cdn/v2/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\n../seg/seg0.ts\n")
		case "/cdn/seg/seg0.ts":
			w.Write(segment)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	_, playlist := fetch(t, p.url, nil)
	paths := playlistPaths(string(playlist))
	if len(paths) != 1 {
		t.Fatalf("本地资源数 = %d", len(paths))
	}
	_, got := fetch(t, resolveLocal(t, p, paths[0]), nil)
	if !bytes.Equal(got, segment) {
		t.Fatalf("重定向后 base 解析错误，拿到 %v", got)
	}
}

func TestProxySendsUpstreamHeaders(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]http.Header{}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		switch r.URL.Path {
		case "/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
		case "/seg.ts":
			w.Write([]byte{0x47, 0x55})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	headers := []string{"Referer: https://live.example.com/", "User-Agent: alive-test"}
	p := testProxy(t, upstream.URL+"/live.m3u8", headers)
	_, playlist := fetch(t, p.url, nil)
	paths := playlistPaths(string(playlist))
	if len(paths) != 1 {
		t.Fatalf("本地资源数 = %d", len(paths))
	}
	fetch(t, resolveLocal(t, p, paths[0]), nil)

	mu.Lock()
	defer mu.Unlock()
	for _, want := range []string{"/live.m3u8", "/seg.ts"} {
		got, ok := seen[want]
		if !ok {
			t.Fatalf("上游没有收到 %s 请求", want)
		}
		if got.Get("Referer") != "https://live.example.com/" {
			t.Fatalf("%s 的 Referer = %q", want, got.Get("Referer"))
		}
		if got.Get("User-Agent") != "alive-test" {
			t.Fatalf("%s 的 User-Agent = %q", want, got.Get("User-Agent"))
		}
	}
}

func TestProxyRangePassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
		case "/seg.ts":
			if got := r.Header.Get("Range"); got != "bytes=2-5" {
				t.Errorf("上游 Range = %q", got)
			}
			w.Header().Set("Content-Range", "bytes 2-5/10")
			w.Header().Set("Content-Type", "video/mp2t")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte{9, 8, 7, 6})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", []string{"Range: bytes=100-200"})
	_, playlist := fetch(t, p.url, nil)
	paths := playlistPaths(string(playlist))
	if len(paths) != 1 {
		t.Fatalf("本地资源数 = %d", len(paths))
	}

	resp, body := fetch(t, resolveLocal(t, p, paths[0]), http.Header{"Range": []string{"bytes=2-5"}})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("状态码 = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Range"); got != "bytes 2-5/10" {
		t.Fatalf("Content-Range = %q", got)
	}
	if got := resp.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if !bytes.Equal(body, []byte{9, 8, 7, 6}) {
		t.Fatalf("分片字节 = %v", body)
	}
}

func TestProxyRejectsUnsupportedPlaylists(t *testing.T) {
	cases := []struct {
		name     string
		playlist string
	}{
		{
			name:     "master",
			playlist: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1600000\nhigh.m3u8\n",
		},
		{
			name:     "encrypted",
			playlist: "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n#EXTINF:3.000,\nseg.ts\n",
		},
		{
			name:     "low-latency",
			playlist: "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-PART:DURATION=0.5,URI=\"part.ts\"\n#EXTINF:3.000,\nseg.ts\n",
		},
		{
			name:     "no-segments",
			playlist: "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-ENDLIST\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.WriteString(w, tc.playlist)
			}))
			defer upstream.Close()

			p, err := newHLSProxy(context.Background(), upstream.URL+"/live.m3u8", nil)
			if p != nil {
				p.stop()
				t.Fatal("不支持的播放列表仍然起了代理")
			}
			if !errors.Is(err, errProxyUnsupported) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestProxyAllowsClearKeyTag(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:3.000,\nseg.ts\n")
		case "/seg.ts":
			w.Write([]byte{0x47})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	_, playlist := fetch(t, p.url, nil)
	if !strings.Contains(string(playlist), "#EXT-X-KEY:METHOD=NONE") {
		t.Fatalf("METHOD=NONE 被改动:\n%s", playlist)
	}
}

func TestProxyRejectsNonHTTP(t *testing.T) {
	if _, err := newHLSProxy(context.Background(), "file:///C:/tmp/live.m3u8", nil); !errors.Is(err, errProxyUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

func TestProxyRequiresToken(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	base, err := url.Parse(p.url)
	if err != nil {
		t.Fatalf("解析本地地址: %v", err)
	}
	root := base.Scheme + "://" + base.Host

	for _, path := range []string{"/", "/index.m3u8", "/t/wrong/index.m3u8", "/t/wrong/r/0.ts"} {
		resp, _ := fetch(t, root+path, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s 状态码 = %d，期望 403", path, resp.StatusCode)
		}
	}
}

func TestProxyUnknownResourceIsNotFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	base, err := url.Parse(p.url)
	if err != nil {
		t.Fatalf("解析本地地址: %v", err)
	}
	root := base.Scheme + "://" + base.Host

	resp, _ := fetch(t, root+"/t/"+p.token+"/r/999.ts", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("未知资源状态码 = %d", resp.StatusCode)
	}
}

func TestProxyStopReleasesListener(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
	}))
	defer upstream.Close()

	p := testProxy(t, upstream.URL+"/live.m3u8", nil)
	target := p.url
	p.stop()
	if err := fetchErr(target); err == nil {
		t.Fatal("停止后本地服务仍然可访问")
	}
	p.stop() // 重复停止不应 panic
}

func TestProxyErrorRedactsSignature(t *testing.T) {
	_, err := newHLSProxy(context.Background(), "http://127.0.0.1:1/live.m3u8?sign=secret123", nil)
	if err == nil {
		t.Fatal("预期回源失败")
	}
	if strings.Contains(err.Error(), "secret123") {
		t.Fatalf("错误泄漏了签名: %v", err)
	}
}

func TestProxyEligible(t *testing.T) {
	cases := []struct {
		name     string
		opts     Options
		upstream string
		codec    string
		want     bool
	}{
		{"关闭开关", Options{}, "https://cdn.example.com/live.m3u8", "avc", false},
		{"明确 avc", Options{HLSProxy: true}, "https://cdn.example.com/live.m3u8", "avc", true},
		{"明确 h264 大写", Options{HLSProxy: true}, "https://cdn.example.com/live.m3u8?x=1", "H264", true},
		{"空 codec", Options{HLSProxy: true}, "https://cdn.example.com/live.m3u8", "", false},
		{"hevc", Options{HLSProxy: true}, "https://cdn.example.com/live.m3u8", "hevc", false},
		{"非 HLS 上游", Options{HLSProxy: true}, "https://cdn.example.com/live.flv", "avc", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := proxyEligible(tc.opts, tc.upstream, tc.codec); got != tc.want {
				t.Fatalf("proxyEligible = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestStartProxySetsModeAndStopClears(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3.000,\nseg.ts\n")
	}))
	defer upstream.Close()

	s := New()
	local, err := s.Start(context.Background(), upstream.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if err != nil {
		t.Fatalf("startProxy: %v", err)
	}
	t.Cleanup(s.Stop)
	if s.Mode() != "hls-proxy" {
		t.Fatalf("Mode = %q", s.Mode())
	}
	if s.URL() != local {
		t.Fatalf("URL = %q，期望 %q", s.URL(), local)
	}

	s.Stop()
	if s.Mode() != "" {
		t.Fatalf("Stop 后 Mode = %q", s.Mode())
	}
	if s.URL() != "" {
		t.Fatalf("Stop 后 URL = %q", s.URL())
	}
}

func TestStartProxyUnsupportedLeavesServerClean(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1600000\nhigh.m3u8\n")
	}))
	defer upstream.Close()

	s := New()
	_, err := s.startProxy(context.Background(), upstream.URL+"/live.m3u8", nil)
	if !errors.Is(err, errProxyUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if s.URL() != "" || s.Mode() != "" {
		t.Fatalf("失败后状态未清理: url=%q mode=%q", s.URL(), s.Mode())
	}
	s.Stop()
}

func TestStartCanceledDoesNotFallBack(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := New()
	_, err := s.Start(ctx, "http://127.0.0.1:1/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if !errors.Is(err, errCanceled) {
		t.Fatalf("err = %v，期望取消", err)
	}
	if s.Mode() != "" {
		t.Fatalf("取消后 Mode = %q", s.Mode())
	}
}
