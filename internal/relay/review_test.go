package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const mediaFixture = "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\nseg.ts?sig=private\n"

func TestRewriteStableBoundedWindows(t *testing.T) {
	base, _ := url.Parse("https://cdn.invalid/live/index.m3u8")
	p := &hlsProxy{token: "test-key", resources: make(map[string]*proxyResource)}
	first, err := p.rewrite([]byte(mediaFixture), base)
	if err != nil {
		t.Fatal(err)
	}
	again, err := p.rewrite([]byte(mediaFixture), base)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatal("刷新同一列表改变资源 URI")
	}
	firstPath := playlistPaths(string(first))[0]
	previous := ""
	for i := 0; i < proxyResourceKeep*2; i++ {
		list := fmt.Sprintf("#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\n%d.ts\n", i)
		rewritten, err := p.rewrite([]byte(list), base)
		if err != nil {
			t.Fatal(err)
		}
		for _, local := range []string{playlistPaths(string(rewritten))[0], previous} {
			if local == "" {
				continue
			}
			id, _, _ := parseResourcePath(strings.TrimPrefix(local, "/t/test-key/"))
			if p.lookup(id) == nil {
				t.Fatal("当前或上一窗口资源被淘汰")
			}
		}
		previous = playlistPaths(string(rewritten))[0]
		if len(p.resources) > proxyResourceKeep || len(p.order) > proxyResourceKeep {
			t.Fatal("映射未有界")
		}
	}
	final, err := p.rewrite([]byte(mediaFixture), base)
	if err != nil || playlistPaths(string(final))[0] != firstPath {
		t.Fatal("淘汰再出现导致 URI 改变")
	}
	before := len(p.resources)
	_, err = p.rewrite([]byte(strings.Replace(mediaFixture, "seg.ts?sig=private", "new.ts\n#EXT-X-DEFINE:NAME=bad", 1)), base)
	if !errors.Is(err, errProxyUnsupported) || len(p.resources) != before {
		t.Fatal("失败解析污染资源映射")
	}
}

func TestProxyStrictMediaBoundary(t *testing.T) {
	cases := []string{
		strings.TrimPrefix(mediaFixture, "#EXTM3U\n"),
		strings.Replace(mediaFixture, "#EXTINF:3,\n", "", 1),
		strings.Replace(mediaFixture, "#EXTINF:3,", "#EXTINF:NaN,", 1),
		strings.Replace(mediaFixture, "#EXTINF:3,", "#EXTINF:3,\n#EXT-X-BYTERANGE:4", 1),
		strings.Replace(mediaFixture, "#EXTINF:3,", "#EXTINF:3,\n#EXT-X-BYTERANGE:4@10", 1),
		mediaFixture + "#EXT-X-MAP:URI=\"init.mp4\",BYTERANGE=\"4\"\n",
		mediaFixture + "#EXT-X-MAP:URI=\"init.mp4\",BYTERANGE=\"4@10\"\n",
	}
	for _, tag := range []string{
		`#EXT-X-SESSION-KEY:METHOD=AES-128,URI="https://cdn.invalid/key?secret=1"`,
		`#EXT-X-CONTENT-STEERING:SERVER-URI="https://cdn.invalid/api?secret=1"`,
		`#EXT-X-DEFINE:NAME="x",VALUE="https://cdn.invalid/?secret=1"`,
		`#EXT-X-MEDIA:URI="https://cdn.invalid/audio?secret=1"`,
		`#EXT-X-UNKNOWN:URI="https://cdn.invalid/?secret=1"`,
		`#EXT-X-KEY:METHOD=NONE,URI="https://cdn.invalid/?secret=1"`,
	} {
		cases = append(cases, mediaFixture+tag+"\n")
	}
	for i, list := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, list) }))
			defer up.Close()
			p, err := newHLSProxy(context.Background(), up.URL+"/live.m3u8", nil, "avc")
			if p != nil {
				p.stop()
				t.Fatal("未实现形式被接受")
			}
			if !errors.Is(err, errProxyUnsupported) || strings.Contains(err.Error(), "secret=1") {
				t.Fatal("没有安全拒绝")
			}
		})
	}
}

func TestPreflightFailureClosesOwnedConnections(t *testing.T) {
	closed := make(chan struct{}, 1)
	up := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "not hls") }))
	up.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	up.Start()
	defer up.Close()
	_, err := newHLSProxy(context.Background(), up.URL+"/live.m3u8", nil, "avc")
	if !errors.Is(err, errProxyUnsupported) {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("预检失败未释放上游空闲连接")
	}
}

func TestSegmentLimitRejectsKnownAndAbortsUnknown(t *testing.T) {
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/live.m3u8" {
					io.WriteString(w, mediaFixture)
					return
				}
				if known {
					w.Header().Set("Content-Length", fmt.Sprint(proxySegmentLimit+1))
					w.WriteHeader(200)
					return
				}
				w.(http.Flusher).Flush()
				block := make([]byte, 64<<10)
				for n := 0; n < proxySegmentLimit/len(block)+1; n++ {
					if _, err := w.Write(block); err != nil {
						return
					}
				}
			}))
			defer up.Close()
			p := testProxy(t, up.URL+"/live.m3u8", nil)
			_, list := fetch(t, p.url, nil)
			resp, err := testHTTPClient.Get(resolveLocal(t, p, playlistPaths(string(list))[0]))
			if err != nil {
				if known {
					t.Fatal(err)
				}
				return
			}
			defer resp.Body.Close()
			if known {
				if resp.StatusCode != 502 {
					t.Fatalf("已知超限状态=%d", resp.StatusCode)
				}
				return
			}
			_, err = io.Copy(io.Discard, resp.Body)
			if err == nil {
				t.Fatal("未知长度超限返回了完整成功响应")
			}
		})
	}
}

func waitUntil(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if f() {
			return
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("等待清理超时")
		}
	}
}

func TestSessionContextAloneClosesProxyAndInflightRequest(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live.m3u8" {
			io.WriteString(w, mediaFixture)
			return
		}
		close(entered)
		<-r.Context().Done()
		close(ended)
	}))
	defer up.Close()
	s := New()
	defer s.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local, err := s.Start(ctx, up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	_, list := fetch(t, local, nil)
	base, _ := url.Parse(local)
	ref, _ := url.Parse(playlistPaths(string(list))[0])
	done := make(chan struct{})
	go func() { defer close(done); _ = fetchErr(base.ResolveReference(ref).String()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("没有分片请求")
	}
	cancel()
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("上游请求未取消")
	}
	<-done
	waitUntil(t, func() bool { return s.URL() == "" && s.Mode() == "" })
	if fetchErr(local) == nil {
		t.Fatal("context 取消后端口仍可用")
	}
}

func TestStopDuringPreflightDoesNotFallBack(t *testing.T) {
	entered := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done() }))
	defer up.Close()
	s := New()
	defer s.Stop()
	done := make(chan error, 1)
	go func() {
		_, err := s.Start(context.Background(), up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
		done <- err
	}()
	<-entered
	s.Stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("取消被转换为回退错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start 未取消")
	}
	if s.Mode() != "" || s.URL() != "" {
		t.Fatal("Stop 后仍接管了资源")
	}
}

func TestRedactPreservesCancellationIdentity(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		got := redactError(&url.Error{Op: "GET", URL: "https://cdn.invalid/?secret=1", Err: err})
		if !errors.Is(got, err) || strings.Contains(got.Error(), "secret") {
			t.Fatal("脱敏丢失取消类型或泄漏地址")
		}
	}
}

// 测试进程自身充当 FFmpeg：只写本地假分片，不访问任何直播接口。
func TestMain(m *testing.M) {
	if codec := os.Getenv("ALIVE_RELAY_TEST_CODEC"); codec != "" {
		if codec == "wait" {
			time.Sleep(time.Minute)
			os.Exit(0)
		}
		fmt.Println(codec)
		os.Exit(0)
	}
	if os.Getenv("ALIVE_RELAY_TEST_CHILD") == "1" {
		args := os.Args
		output := args[len(args)-1]
		if os.Getenv("ALIVE_RELAY_TEST_EXIT") == "empty" {
			os.Exit(0)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(output), "seg-00001.ts"), []byte{0x47}, 0600); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(output, []byte("#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nseg-00001.ts\n"), 0600); err != nil {
			os.Exit(4)
		}
		switch os.Getenv("ALIVE_RELAY_TEST_EXIT") {
		case "clean":
			os.Exit(0)
		case "failed":
			os.Exit(5)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeFFmpeg(t *testing.T, broken bool) {
	t.Helper()
	dir := t.TempDir()
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var data []byte
	if broken {
		data = []byte("invalid executable")
	} else {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		data, err = os.ReadFile(exe)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIVE_FFMPEG_DIR", dir)
	t.Setenv("ALIVE_RELAY_TEST_CHILD", "1")
}

func TestUnsupportedPreflightFallsBackAndRemuxCleans(t *testing.T) {
	fakeFFmpeg(t, false)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nlow.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=1600000\nhigh.m3u8\n")
	}))
	defer up.Close()
	s := New()
	defer s.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	local, err := s.Start(ctx, up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode() != "remux" || calls.Load() != 1 {
		t.Fatal("预检失败未回退旧路径")
	}
	s.mu.Lock()
	dir, done := s.dir, s.done
	s.mu.Unlock()
	cancel()
	waitUntil(t, func() bool { _, err := os.Stat(dir); return s.URL() == "" && os.IsNotExist(err) })
	select {
	case <-done:
	default:
		t.Fatal("未等待子进程结束")
	}
	if fetchErr(local) == nil {
		t.Fatal("回退服务未清理")
	}
}

func TestCommandStartFailureCleansResources(t *testing.T) {
	fakeFFmpeg(t, true)
	temp := t.TempDir()
	t.Setenv("TEMP", temp)
	t.Setenv("TMPDIR", temp)
	s := New()
	defer s.Stop()
	_, err := s.Start(context.Background(), "http://127.0.0.1:1/live.flv", nil, "avc", Options{})
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("启动失败错误不正确: %v", err)
	}
	s.mu.Lock()
	clean := s.cmd == nil && s.server == nil && s.dir == "" && s.done == nil
	s.mu.Unlock()
	if !clean {
		t.Fatal("cmd.Start 失败留下资源")
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatal("cmd.Start 失败留下临时目录")
	}
}

func TestConcurrentStartStopWithProcess(t *testing.T) {
	fakeFFmpeg(t, false)
	s := New()
	defer s.Stop()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, _ = s.Start(ctx, "http://127.0.0.1:1/live.flv", nil, "avc", Options{})
		}()
		go func() { defer wg.Done(); s.Stop() }()
	}
	wg.Wait()
	s.Stop()
	if s.URL() != "" || s.Mode() != "" {
		t.Fatal("并发停止后还有会话")
	}
}

func TestRefreshThroughHTTPKeepsSegmentURI(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, mediaFixture) }))
	defer up.Close()
	p := testProxy(t, up.URL+"/live.m3u8", nil)
	_, first := fetch(t, p.url, nil)
	for i := 0; i < 3; i++ {
		_, next := fetch(t, p.url, nil)
		if !bytes.Equal(first, next) {
			t.Fatal("HTTP 刷新更换了资源 URI")
		}
	}
	if calls.Load() != 4 {
		t.Fatal("测试未实际触发刷新")
	}
}

func TestPlaylistSizeLimitAndOversizedWindow(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("x", proxyPlaylistLimit+1))
	}))
	defer up.Close()
	p, err := newHLSProxy(context.Background(), up.URL+"/live.m3u8", nil, "avc")
	if p != nil || !errors.Is(err, errProxyFetch) {
		t.Fatal("超大播放列表未拒绝")
	}
	base, _ := url.Parse("https://cdn.invalid/live.m3u8")
	proxy := &hlsProxy{token: "test-key"}
	var list strings.Builder
	list.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:3\n")
	for i := 0; i <= proxyResourceKeep/2; i++ {
		fmt.Fprintf(&list, "#EXTINF:3,\n%d.ts\n", i)
	}
	if _, err := proxy.rewrite([]byte(list.String()), base); !errors.Is(err, errProxyUnsupported) {
		t.Fatal("过大窗口未回退")
	}
	if len(proxy.resources) != 0 {
		t.Fatal("拒绝列表后仍登记了资源")
	}
}

func TestOldCancellationCannotStopReplacementSession(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, mediaFixture) }))
	defer up.Close()
	s := New()
	defer s.Stop()
	oldCtx, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	old, err := s.Start(oldCtx, up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.Start(context.Background(), up.URL+"/live.m3u8", nil, "avc", Options{HLSProxy: true})
	if err != nil {
		t.Fatal(err)
	}
	cancelOld()
	// 显式执行旧代次的清理，验证调度再晚也不能误杀新会话。
	s.stopGeneration(1)
	if s.URL() != replacement || s.Mode() != "hls-proxy" {
		t.Fatal("旧会话取消误停新会话")
	}
	resp, _ := fetch(t, replacement, nil)
	if resp.StatusCode != 200 {
		t.Fatal("新会话不可用")
	}
	if fetchErr(old) == nil {
		t.Fatal("旧监听端口未回收")
	}
}

func TestProxyDisabledDoesNotPreflight(t *testing.T) {
	fakeFFmpeg(t, false)
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, mediaFixture) }))
	defer up.Close()
	s := New()
	defer s.Stop()
	_, err := s.Start(context.Background(), up.URL+"/live.m3u8", nil, "avc", Options{})
	if err != nil || s.Mode() != "remux" || calls.Load() != 0 {
		t.Fatal("默认路径触发了代理")
	}
}

func TestUpstreamSegmentTimeoutAbortsRequest(t *testing.T) {
	ended := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/live.m3u8" {
			io.WriteString(w, mediaFixture)
			return
		}
		<-r.Context().Done()
		close(ended)
	}))
	defer up.Close()
	p := testProxy(t, up.URL+"/live.m3u8", nil)
	// 缩短测试客户端的总时限，不改生产回源的固定时限。
	p.client.Timeout = 30 * time.Millisecond
	_, list := fetch(t, p.url, nil)
	resp, _ := fetch(t, resolveLocal(t, p, playlistPaths(string(list))[0]), nil)
	if resp.StatusCode != 502 {
		t.Fatal("上游超时返回成功")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("上游超时请求未释放")
	}
}
