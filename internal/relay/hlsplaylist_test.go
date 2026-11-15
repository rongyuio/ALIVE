package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func singleWrapper(uri string) string {
	return "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-STREAM-INF:BANDWIDTH=800000,CODECS=\"avc1.640028,mp4a.40.2\",RESOLUTION=1920x1080,FRAME-RATE=60.000\n" + uri + "\n"
}

func TestSingleWrapperRedirectHeadersAndMedia(t *testing.T) {
	segment := []byte{0x47, 0x11, 0x22, 0x33}
	var badHeaders atomic.Int32
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://live.example.invalid/" || r.Header.Get("Authorization") != "test-only" || r.Header.Get("User-Agent") != "relay-fixture" {
			badHeaders.Add(1)
		}
		switch r.URL.Path {
		case "/jump.m3u8":
			http.Redirect(w, r, "/cdn/final/live.m3u8", 302)
		case "/cdn/final/live.m3u8":
			body := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-START:TIME-OFFSET=-3.000,PRECISE=NO\n#EXT-X-MEDIA-SEQUENCE:12\n#EXT-X-TARGETDURATION:3\n"

			body += "#EXT-BILI-AUX:URI=\"https://secret.invalid/private?sig=hidden\"\n#EXTINF:3.000,\n../segment.ts?sig=private\n"
			io.WriteString(w, body)
		case "/cdn/segment.ts":
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write(segment)
		default:
			http.NotFound(w, r)
		}
	}))
	defer media.Close()
	wrapper := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://live.example.invalid/" {
			badHeaders.Add(1)
		}
		switch r.URL.Path {
		case "/entry.m3u8":
			http.Redirect(w, r, "/outer/master.m3u8", 302)
		case "/outer/master.m3u8":
			io.WriteString(w, singleWrapper("../bridge.m3u8"))
		case "/bridge.m3u8":
			http.Redirect(w, r, media.URL+"/jump.m3u8", 302)
		default:
			http.NotFound(w, r)
		}
	}))
	defer wrapper.Close()
	p := testProxy(t, wrapper.URL+"/entry.m3u8", []string{"Referer: https://live.example.invalid/", "Authorization: test-only", "User-Agent: relay-fixture"})
	_, body := fetch(t, p.url, nil)
	text := string(body)
	if strings.Contains(text, "EXT-X-STREAM-INF") || strings.Contains(text, "EXT-BILI-AUX") || strings.Contains(text, "hidden") || strings.Contains(text, "sig=") {
		t.Fatal("上游包装或签名内容被透传")
	}
	if !strings.Contains(text, "#EXT-X-START:TIME-OFFSET=-3.000,PRECISE=NO") {
		t.Fatal("START 丢失")
	}
	paths := playlistPaths(text)
	if len(paths) != 1 {
		t.Fatal("分片数错误")
	}
	_, got := fetch(t, resolveLocal(t, p, paths[0]), nil)
	if !bytes.Equal(got, segment) {
		t.Fatal("分片内容改变")
	}

	_, again := fetch(t, p.url, nil)
	if !bytes.Equal(body, again) {
		t.Fatal("最终媒体列表刷新改变 URI")
	}
	if badHeaders.Load() != 0 {
		t.Fatal("重定向或解引用丢失请求头")
	}
}

func TestWrapperRejectsUnsafeOrAmbiguousMasters(t *testing.T) {
	cases := []string{
		singleWrapper("a.m3u8") + "#EXT-X-STREAM-INF:BANDWIDTH=1600000\nb.m3u8\n",
		singleWrapper("a.m3u8") + "b.m3u8\n",
		strings.Replace(singleWrapper("a.m3u8"), "BANDWIDTH=800000", "BANDWIDTH=800000,AUDIO=\"audio\"", 1),
		strings.Replace(singleWrapper("a.m3u8"), "BANDWIDTH=800000", "BANDWIDTH=800000,SUBTITLES=\"subs\"", 1),
		strings.Replace(singleWrapper("a.m3u8"), "BANDWIDTH=800000", "BANDWIDTH=800000,CLOSED-CAPTIONS=\"cc\"", 1),
		singleWrapper("a.m3u8") + "#EXT-X-MEDIA:TYPE=AUDIO,URI=\"secret\"\n",
		singleWrapper("a.m3u8") + "#EXT-X-SESSION-KEY:METHOD=AES-128,URI=\"secret\"\n",
		singleWrapper("a.m3u8") + "#EXT-X-CONTENT-STEERING:SERVER-URI=\"secret\"\n",
		singleWrapper("a.m3u8") + "#EXT-X-DEFINE:NAME=\"x\",VALUE=\"secret\"\n",
		singleWrapper("a.m3u8") + "#EXT-X-I-FRAME-STREAM-INF:URI=\"secret\"\n",
		singleWrapper("a.m3u8") + "#EXT-BILI-AUX:secret\n",
		singleWrapper("file:///private.m3u8"),
		singleWrapper("{$name}.m3u8"),
		strings.Replace(singleWrapper("a.m3u8"), "avc1.640028", "hvc1.1.6.L150", 1),
		strings.Replace(singleWrapper("a.m3u8"), "BANDWIDTH=800000", "BANDWIDTH=800000,BANDWIDTH=42", 1),
		strings.Replace(singleWrapper("a.m3u8"), "BANDWIDTH=800000", "BANDWIDTH=800000,UNKNOWN=\"secret\"", 1),
		strings.TrimPrefix(singleWrapper("a.m3u8"), "#EXTM3U\n"),
	}
	for i, body := range cases {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			var requests atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); io.WriteString(w, body) }))
			defer up.Close()
			p, err := newHLSProxy(context.Background(), up.URL+"/entry.m3u8", nil)
			if p != nil {
				p.stop()
				t.Fatal("不支持的包装被接受")
			}
			if !errors.Is(err, errProxyUnsupported) {
				t.Fatalf("拒绝类型不符: %v", err)
			}
			if requests.Load() != 1 {
				t.Fatal("在验证整个 master 前访问了 variant")
			}
		})
	}
}

func TestWrapperDepthAndCycles(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		calls   int
		success bool
	}{
		{"两层包装成功", "ok", 3, true},
		{"三层包装拒绝", "deep", 3, false},
		{"自循环", "self", 1, false},
		{"双向循环", "cycle", 2, false},
		{"重定向别名循环", "redirect", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				switch tc.mode {
				case "self":
					io.WriteString(w, singleWrapper("/0.m3u8"))
				case "cycle":
					if r.URL.Path == "/0.m3u8" {
						io.WriteString(w, singleWrapper("/1.m3u8"))
					} else {
						io.WriteString(w, singleWrapper("/0.m3u8"))
					}
				case "redirect":
					if r.URL.Path == "/0.m3u8" {
						http.Redirect(w, r, "/alias.m3u8", 302)
					} else {
						io.WriteString(w, singleWrapper("/0.m3u8"))
					}
				default:
					n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), ".m3u8"))
					if tc.success && n == 2 {
						io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\nseg.ts\n")
					} else {
						io.WriteString(w, singleWrapper(fmt.Sprintf("/%d.m3u8", n+1)))
					}
				}
			}))
			defer up.Close()
			p, err := newHLSProxy(context.Background(), up.URL+"/0.m3u8", nil)
			if p != nil {
				defer p.stop()
			}
			if tc.success {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, errProxyUnsupported) {
				t.Fatalf("深度或循环未拒绝: %v", err)
			}
			if int(calls.Load()) != tc.calls {
				t.Fatalf("实际请求=%d，预期=%d", calls.Load(), tc.calls)
			}
		})
	}
}

func TestWrapperCancellationStopsNestedFetch(t *testing.T) {
	entered, ended := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/entry.m3u8" {
			io.WriteString(w, singleWrapper("/media.m3u8"))
			return
		}
		close(entered)
		<-r.Context().Done()
		close(ended)
	}))
	defer up.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New()
	defer s.Stop()
	result := make(chan error, 1)
	go func() {
		_, err := s.Start(ctx, up.URL+"/entry.m3u8", nil, "avc", Options{HLSProxy: true})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("没有访问下层")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("取消触发回退或丢失错误类型")
		}
	case <-time.After(time.Second):
		t.Fatal("取消未结束预检")
	}
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("下层请求未取消")
	}
	if s.Mode() != "" || s.URL() != "" {
		t.Fatal("取消后发布了资源")
	}
}

type deadlineTransport struct {
	t        *testing.T
	deadline time.Time
	calls    int
}

func (d *deadlineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	if !ok {
		d.t.Error("缺少统一预算")
	} else if d.calls == 0 {
		d.deadline = deadline
	} else if !deadline.Equal(d.deadline) {
		d.t.Error("层间重置了预算")
	}
	d.calls++
	if d.calls == 3 {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(singleWrapper(fmt.Sprintf("/%d.m3u8", d.calls)))), Request: req}, nil
}

func TestWrapperSharesOnePreflightBudget(t *testing.T) {
	transport := &deadlineTransport{t: t}
	p := &hlsProxy{client: &http.Client{Transport: transport}}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, _, err := p.fetchMediaPlaylist(ctx, "http://fixture.invalid/0.m3u8")
	if !errors.Is(err, context.DeadlineExceeded) || transport.calls != 3 {
		t.Fatalf("链路未使用共同截止时间: %v", err)
	}
}

func TestPlaylistStartValidation(t *testing.T) {
	for _, value := range []string{"TIME-OFFSET=-3.500", "TIME-OFFSET=0,PRECISE=YES", "PRECISE=NO,TIME-OFFSET=2.25"} {
		if !validPlaylistStart(value) {
			t.Fatalf("标准 START 被拒绝: %s", value)
		}
	}
	for _, value := range []string{"PRECISE=YES", "TIME-OFFSET=NaN", "TIME-OFFSET=+Inf", "TIME-OFFSET=1e9", "TIME-OFFSET=1,PRECISE=MAYBE", "TIME-OFFSET=1,TIME-OFFSET=2", `TIME-OFFSET=1,URI="secret"`} {
		if validPlaylistStart(value) {
			t.Fatal("非法 START 被放行")
		}
	}
}

func TestProxyRejectsUnsupportedMediaBeforeFetchingResources(t *testing.T) {
	fixtures := map[string]string{
		"map-byterange": "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-MAP:URI=\"init.mp4\",BYTERANGE=\"100@0\"\n#EXTINF:3,\nsegment.m4s\n",
		"late-map":      "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\nsegment.m4s\n#EXT-X-MAP:URI=\"init.mp4\"\n",
	}
	for _, ext := range []string{".m4s", ".mp4", ".m4v", ".cmfv", ".cmfa", ".m4a", ".aac", ".MP4", ".bin", ""} {
		fixtures["segment"+ext] = "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXTINF:3,\nsegment" + ext + "?signature=private\n"
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			for _, wrapper := range []bool{false, true} {
				t.Run(fmt.Sprintf("wrapper=%v", wrapper), func(t *testing.T) {
					var mediaRequests atomic.Int32
					up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/entry.m3u8":
							if wrapper {
								io.WriteString(w, singleWrapper("/media.m3u8"))
							} else {
								io.WriteString(w, fixture)
							}
						case "/media.m3u8":
							io.WriteString(w, fixture)
						default:
							mediaRequests.Add(1)
							w.Write([]byte{0, 1, 2})
						}
					}))
					defer up.Close()
					p, err := newHLSProxy(context.Background(), up.URL+"/entry.m3u8", nil)
					if p != nil {
						p.stop()
						t.Fatal("未实现的媒体列表仍然启用代理")
					}
					if !errors.Is(err, errProxyUnsupported) {
						t.Fatalf("拒绝类型错误: %v", err)
					}
					if mediaRequests.Load() != 0 {
						t.Fatal("预检拒绝前下载了初始化段或媒体分片")
					}
				})
			}
		})
	}
}

func TestMapByteRangePreflightFallsBackToRemux(t *testing.T) {
	fakeFFmpeg(t, false)
	for _, wrapper := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrapper=%v", wrapper), func(t *testing.T) {
			var initRequests, playlistRequests atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/entry.m3u8":
					playlistRequests.Add(1)
					if wrapper {
						io.WriteString(w, singleWrapper("/media.m3u8"))
						return
					}
					fallthrough
				case "/media.m3u8":
					io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:3\n#EXT-X-MAP:URI=\"init.mp4\",BYTERANGE=\"100@0\"\n#EXTINF:3,\nsegment.m4s\n")
				default:
					initRequests.Add(1)
					http.NotFound(w, r)
				}
			}))
			defer up.Close()
			s := New()
			defer s.Stop()
			local, err := s.Start(context.Background(), up.URL+"/entry.m3u8", nil, "avc", Options{HLSProxy: true})
			if err != nil {
				t.Fatal(err)
			}
			if s.Mode() != "remux" || local == "" {
				t.Fatal("MAP BYTERANGE 未直接回退 FFmpeg")
			}
			if playlistRequests.Load() != 1 || initRequests.Load() != 0 {
				t.Fatal("代理预检重复访问初始化段或入口")
			}
		})
	}
}

// 真实 B 站包装带 PROGRAM-ID；只兼容纯数字，不能因此接受其他扩展属性。
func TestSingleVariantProgramID(t *testing.T) {
	for _, tc := range []struct {
		value string
		valid bool
	}{{"0", true}, {"1", true}, {"42", true}, {"", false}, {"-1", false}, {"1.0", false}, {"18446744073709551616", false}, {`"1"`, false}, {"1,PROGRAM-ID=2", false}, {"1,UNKNOWN=2", false}} {
		t.Run(tc.value, func(t *testing.T) {
			body := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-STREAM-INF:PROGRAM-ID=" + tc.value + ",BANDWIDTH=800000\nmedia.m3u8\n"
			uri, wrapper, err := singleVariantURI([]byte(body))
			if tc.valid {
				if err != nil || !wrapper || uri != "media.m3u8" {
					t.Fatal("兼容的单档包装被拒绝")
				}
			} else if err == nil {
				t.Fatal("非法属性被接受")
			}
		})
	}
}
