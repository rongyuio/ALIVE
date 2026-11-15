package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlaylistRecoveryRefreshAndRollingSegment(t *testing.T) {
	for _, status := range []int{502, 503, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/new.ts" {
					io.WriteString(w, "new-segment")
					return
				}
				switch calls.Add(1) {
				case 1:
					io.WriteString(w, mediaFixture)
				case 2:
					w.WriteHeader(status)
				default:
					io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-MEDIA-SEQUENCE:2\n#EXTINF:3,\nnew.ts\n")
				}
			}))
			defer up.Close()
			p := testProxy(t, up.URL+"/live.m3u8", nil)
			defer p.stop()
			_, old := fetch(t, p.url, nil)
			resp, list := fetch(t, p.url, nil)
			if resp.StatusCode != 200 || calls.Load() != 3 || !strings.Contains(string(list), "#EXT-X-MEDIA-SEQUENCE:2") {
				t.Fatalf("刷新未恢复: status=%d calls=%d", resp.StatusCode, calls.Load())
			}
			paths := playlistPaths(string(list))
			if len(paths) != 1 || paths[0] == playlistPaths(string(old))[0] {
				t.Fatal("恢复后未发布滚动窗口的新分片")
			}
			resp, segment := fetch(t, resolveLocal(t, p, paths[0]), nil)
			if resp.StatusCode != 200 || string(segment) != "new-segment" {
				t.Fatal("恢复后的新分片不可读")
			}
		})
	}
}

func TestPlaylistRecoveryAttemptLimitAndRejections(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		attempts int32
	}{
		{"502", 502, "", 2}, {"503", 503, "", 2}, {"504", 504, "", 2},
		{"400", 400, "", 1}, {"401", 401, "", 1}, {"403", 403, "", 1},
		{"404", 404, "", 1}, {"429", 429, "", 1}, {"500", 500, "", 1},
		{"unsupported", 200, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-PART:DURATION=0.3,URI=\"part.ts\"\n", 1},
		{"oversized", 200, strings.Repeat("x", proxyPlaylistLimit+1), 1},
	}
	for _, tc := range cases {
		for _, preflight := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/preflight=%v", tc.name, preflight), func(t *testing.T) {
				var calls atomic.Int32
				up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if calls.Add(1) == 1 && !preflight {
						io.WriteString(w, mediaFixture)
						return
					}
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
				}))
				defer up.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				p, err := newHLSProxy(ctx, up.URL+"/live.m3u8", nil, "avc")
				if p != nil {
					defer p.stop()
				}
				if preflight {
					if err == nil || p != nil || calls.Load() != tc.attempts {
						t.Fatalf("预检未按边界结束: calls=%d err=%v", calls.Load(), err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				fetch(t, p.url, nil)
				resp, _ := fetch(t, p.url, nil)
				if resp.StatusCode != 502 || calls.Load() != 1+tc.attempts {
					t.Fatalf("刷新重试次数错误: status=%d calls=%d", resp.StatusCode, calls.Load())
				}
			})
		}
	}
}

func TestPlaylistRecoveryTransientNetworkFailure(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprintf("reset=%v", reset), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					if reset {
						// 真正重置 TCP 连接，覆盖 Windows 与 Unix 不同的系统错误码。
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						if err := conn.(*net.TCPConn).SetLinger(0); err != nil {
							t.Error(err)
						}
						conn.Close()
						return
					}
					// 响应正文中途断开，触发真实 HTTP 读取错误而非格式拒绝。
					w.Header().Set("Content-Length", "1000")
					io.WriteString(w, "#EXTM3U\n")
					return
				}
				io.WriteString(w, mediaFixture)
			}))
			defer up.Close()
			p := testProxy(t, up.URL+"/live.m3u8", nil)
			defer p.stop()
			resp, body := fetch(t, p.url, nil)
			if resp.StatusCode != 200 || len(playlistPaths(string(body))) != 1 || calls.Load() != 2 {
				t.Fatalf("网络故障未恢复: status=%d calls=%d", resp.StatusCode, calls.Load())
			}
		})
	}
}

func TestPlaylistRecoveryRedirectSeenIsPerAttempt(t *testing.T) {
	var entries, wrappers, finals atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/entry.m3u8":
			entries.Add(1)
			http.Redirect(w, r, "/wrapper.m3u8", http.StatusFound)
		case "/wrapper.m3u8":
			wrappers.Add(1)
			io.WriteString(w, singleWrapper("/media.m3u8"))
		case "/media.m3u8":
			if finals.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			io.WriteString(w, mediaFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()
	p := testProxy(t, up.URL+"/entry.m3u8", nil)
	defer p.stop()
	if entries.Load() != 2 || wrappers.Load() != 2 || finals.Load() != 2 {
		t.Fatalf("重试未完整经过重定向和包装: %d/%d/%d", entries.Load(), wrappers.Load(), finals.Load())
	}
}

func TestPlaylistRecoveryHeadPreservesPending(t *testing.T) {
	var calls atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, mediaFixture)
	}))
	defer up.Close()
	p := testProxy(t, up.URL+"/live.m3u8", nil)
	defer p.stop()
	for i := 0; i < 2; i++ {
		req, err := http.NewRequest(http.MethodHead, p.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := testHTTPClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || len(body) != 0 || resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/vnd.apple.mpegurl" {
			t.Fatal("HEAD 响应错误")
		}
	}
	resp, body := fetch(t, p.url, nil)
	if resp.StatusCode != 200 || len(playlistPaths(string(body))) != 1 || calls.Load() != 1 {
		t.Fatalf("HEAD 消耗了首份列表: calls=%d", calls.Load())
	}
	fetch(t, p.url, nil)
	if calls.Load() != 2 {
		t.Fatal("后续 GET 未正常刷新")
	}
}

func TestPlaylistRecoveryCancellationStopsInflight(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-r.Context().Done()
	}))
	defer up.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &hlsProxy{client: up.Client()}
	done := make(chan error, 1)
	go func() {
		_, _, err := p.fetchMediaPlaylistWithRetry(ctx, up.URL+"/live.m3u8")
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("未开始回源")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
			t.Fatalf("取消后仍重试: calls=%d err=%v", calls.Load(), err)
		}
	case <-time.After(time.Second):
		t.Fatal("取消未及时结束")
	}
}

// 记录真实 HTTP 请求携带的截止时间，验证第二次回源没有重新获得预算。
type recoveryDeadlineTransport struct {
	base      http.RoundTripper
	deadlines []time.Time
}

func (r *recoveryDeadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, _ := req.Context().Deadline()
	r.deadlines = append(r.deadlines, deadline)
	return r.base.RoundTrip(req)
}

func TestPlaylistRecoverySharesBudgetIncludingBackoff(t *testing.T) {
	for _, budget := range []time.Duration{proxyPlaylistRetryDelay / 2, 4 * proxyPlaylistRetryDelay} {
		t.Run(budget.String(), func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(503)
					return
				}
				<-r.Context().Done()
			}))
			defer up.Close()
			transport := &recoveryDeadlineTransport{base: up.Client().Transport}
			p := &hlsProxy{client: &http.Client{Transport: transport}}
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()
			deadline, _ := ctx.Deadline()
			_, _, err := p.fetchMediaPlaylistWithRetry(ctx, up.URL+"/live.m3u8")
			want := int32(2)
			if budget < proxyPlaylistRetryDelay {
				want = 1
			}
			if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != want {
				t.Fatalf("预算到期后未结束: calls=%d want=%d err=%v", calls.Load(), want, err)
			}
			for _, got := range transport.deadlines {
				if !got.Equal(deadline) {
					t.Fatal("重试改变了截止时间")
				}
			}
		})
	}
}

// 旧版列表的缓存提示不影响分片字节，本地仍禁止缓存且不能误放行扩展属性。
func TestProxyLegacyAllowCache(t *testing.T) {
	for _, value := range []string{"YES", "NO", "", "yes", `YES,URI="secret"`} {
		t.Run(value, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/seg.ts" {
					io.WriteString(w, "unchanged-media")
					return
				}
				fmt.Fprintf(w, "#EXTM3U\n#EXT-X-ALLOW-CACHE:%s\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nseg.ts\n", value)
			}))
			defer up.Close()
			p, err := newHLSProxy(context.Background(), up.URL+"/live.m3u8", nil, "avc")
			if value != "YES" && value != "NO" {
				if p != nil {
					p.stop()
				}
				if !errors.Is(err, errProxyUnsupported) {
					t.Fatalf("非法缓存标签未拒绝: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.stop()
			resp, body := fetch(t, p.url, nil)
			if resp.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), "ALLOW-CACHE") {
				t.Fatal("本地缓存策略错误")
			}
			paths := playlistPaths(string(body))
			if len(paths) != 1 {
				t.Fatalf("分片数量 = %d", len(paths))
			}
			_, segment := fetch(t, resolveLocal(t, p, paths[0]), nil)
			if string(segment) != "unchanged-media" {
				t.Fatal("代理改写了媒体字节")
			}
		})
	}
}
