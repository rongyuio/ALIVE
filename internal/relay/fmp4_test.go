package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const fmp4Fixture = "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:3\n#EXT-X-MAP:URI=\"../init.mp4?sig=init-secret\"\n#EXTINF:3,\n../seg.m4s?sig=fragment-secret\n"

func fixtureMapPath(t *testing.T, playlist string) string {
	t.Helper()
	for _, line := range strings.Split(playlist, "\n") {
		if value, ok := strings.CutPrefix(line, "#EXT-X-MAP:"); ok {
			attrs, valid := strictPlaylistAttributes(value)
			uri := attrs["URI"]
			if !valid || len(uri) < 3 {
				t.Fatal("重写后的 MAP 无效")
			}
			return uri[1 : len(uri)-1]
		}
	}
	t.Fatal("重写后的 MAP 缺失")
	return ""
}

func TestFMP4InitFragmentBytesRangesAndRefresh(t *testing.T) {
	for _, wrapper := range []bool{false, true} {
		for _, ext := range []string{".m4s", ".mp4"} {
			t.Run(fmt.Sprintf("wrapper=%v/ext=%s", wrapper, ext), func(t *testing.T) {
				// 二进制含零字节与高位字节，验证代理实际传输，不推断解码器兼容性。
				init := []byte{0, 0, 0, 24, 'f', 't', 'y', 'p', 'i', 's', 'o', '6', 0, 0, 0, 1, 0x80, 0xff}
				fragment := []byte{0, 0, 0, 12, 'm', 'o', 'o', 'f', 0x81, 0, 0xfe, 0x47, 0, 0, 0, 12, 'm', 'd', 'a', 't', 1, 2, 3, 0xff}
				var resourceRequests atomic.Int32
				media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Referer") != "https://live.example.invalid/" {
						t.Error("回源 Referer 丢失")
					}
					switch r.URL.Path {
					case "/media.m3u8":
						http.Redirect(w, r, "/cdn/list/live.m3u8", http.StatusFound)
					case "/cdn/list/live.m3u8":
						io.WriteString(w, strings.Replace(fmp4Fixture, "seg.m4s", "seg"+ext, 1))
					case "/cdn/init.mp4", "/cdn/seg" + ext:
						resourceRequests.Add(1)
						want, data := "init-secret", init
						if r.URL.Path != "/cdn/init.mp4" {
							want, data = "fragment-secret", fragment
						}
						if r.URL.Query().Get("sig") != want {
							t.Error("上游签名未保留")
						}
						// 模拟 B 站 CDN 的错误类型；本地仍须返回 video/mp4。
						w.Header().Set("Content-Type", "video/MP2T")
						http.ServeContent(w, r, "resource.mp4", time.Time{}, bytes.NewReader(data))
					default:
						http.NotFound(w, r)
					}
				}))
				defer media.Close()
				entry := media.URL + "/media.m3u8"
				if wrapper {
					upstreamMedia := entry
					master := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Referer") != "https://live.example.invalid/" {
							t.Error("wrapper Referer 丢失")
						}
						io.WriteString(w, singleWrapper(upstreamMedia))
					}))
					defer master.Close()
					entry = master.URL + "/entry.m3u8"
				}
				s := New()
				defer s.Stop()
				local, err := s.Start(context.Background(), entry, []string{"Referer: https://live.example.invalid/"}, "avc", Options{HLSProxy: true})
				if err != nil {
					t.Fatal(err)
				}
				if s.Mode() != "hls-proxy" {
					t.Fatal("完整 fMP4 错误回退")
				}
				if resourceRequests.Load() != 0 {
					t.Fatal("预检不应主动下载 init 或 fragment")
				}
				_, list := fetch(t, local, nil)
				text := string(list)
				if strings.Contains(text, "secret") || strings.Contains(text, "sig=") {
					t.Fatal("本地列表泄漏签名")
				}
				paths := []string{fixtureMapPath(t, text), playlistPaths(text)[0]}
				base, _ := url.Parse(local)
				for i, path := range paths {
					ref, _ := url.Parse(path)
					target := base.ResolveReference(ref).String()
					want := [][]byte{init, fragment}[i]
					resp, got := fetch(t, target, nil)
					if resp.StatusCode != 200 || !bytes.Equal(got, want) || resp.Header.Get("Content-Type") != "video/mp4" {
						t.Fatal("完整资源字节或类型错误")
					}
					for _, rr := range []struct {
						header     string
						begin, end int
					}{{"bytes=2-7", 2, 8}, {"bytes=8-", 8, len(want)}} {
						resp, got = fetch(t, target, http.Header{"Range": []string{rr.header}})
						expectedRange := fmt.Sprintf("bytes %d-%d/%d", rr.begin, rr.end-1, len(want))
						if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != expectedRange || !bytes.Equal(got, want[rr.begin:rr.end]) {
							t.Fatal("Range 返回的偏移、状态或字节不正确")
						}
					}
				}
				for i := 0; i < 3; i++ {
					_, again := fetch(t, local, nil)
					if !bytes.Equal(list, again) {
						t.Fatal("刷新更换了 init 或 fragment URI")
					}
				}
			})
		}
	}
}

func TestFMP4StableMappingProtectsInitAndPreviousFragment(t *testing.T) {
	base, _ := url.Parse("https://cdn.invalid/live/index.m3u8")
	p := &hlsProxy{token: randomToken()}
	first, err := p.rewrite([]byte(fmp4Fixture), base)
	if err != nil {
		t.Fatal(err)
	}
	initPath, previous := fixtureMapPath(t, string(first)), playlistPaths(string(first))[0]
	for i := 0; i < proxyResourceKeep*2; i++ {
		list := strings.Replace(fmp4Fixture, "seg.m4s", fmt.Sprintf("seg-%d.m4s", i), 1)
		out, err := p.rewrite([]byte(list), base)
		if err != nil {
			t.Fatal(err)
		}
		if fixtureMapPath(t, string(out)) != initPath {
			t.Fatal("初始化段 ID 随窗口滚动变化")
		}
		current := playlistPaths(string(out))[0]
		for _, path := range []string{initPath, current, previous} {
			id, _, ok := parseResourcePath(strings.TrimPrefix(path, "/t/"+p.token+"/"))
			if !ok || p.lookup(id) == nil {
				t.Fatal("当前 MAP 或当前/上一分片被淘汰")
			}
		}
		previous = current
		if len(p.resources) > proxyResourceKeep || len(p.order) > proxyResourceKeep {
			t.Fatal("fMP4 映射无界增长")
		}
	}
	again, err := p.rewrite([]byte(fmp4Fixture), base)
	if err != nil || !bytes.Equal(first, again) {
		t.Fatal("淘汰后同一 absolute URL 未恢复同一 ID")
	}
}

func TestMapStrictAttributesAndUnsupportedBoundaries(t *testing.T) {
	base, _ := url.Parse("https://cdn.invalid/live/index.m3u8")
	for _, value := range []string{
		`URI="../init.mp4",BYTERANGE="10"`, `URI="../init.mp4",BYTERANGE="10@0"`,
		`URI="../init.mp4",URI="other.mp4"`, `URI="../init.mp4",UNKNOWN="private"`,
		`URI=init.mp4`, `URI=""`, `URI="init.mp4`, `URI="../init.mp4",`,
		`URI="file:///init.mp4"`, `URI="https://user:pass@cdn.invalid/init.mp4"`,
		`URI="../init.bin"`, `URI="{$name}.mp4"`,
	} {
		t.Run(value, func(t *testing.T) {
			list := strings.Replace(fmp4Fixture, `URI="../init.mp4?sig=init-secret"`, value, 1)
			p := &hlsProxy{token: randomToken()}
			_, err := p.rewrite([]byte(list), base)
			if !errors.Is(err, errProxyUnsupported) || len(p.resources) != 0 {
				t.Fatal("不安全或未实现 MAP 被接受/部分提交")
			}
		})
	}
	for _, tag := range []string{
		`#EXT-X-KEY:METHOD=AES-128,URI="key.bin"`,
		`#EXT-X-PART:DURATION=0.3,URI="part.m4s"`,
		`#EXT-X-BYTERANGE:10@0`,
		`#EXT-X-MEDIA:TYPE=AUDIO,URI="audio.m3u8"`,
	} {
		p := &hlsProxy{token: randomToken()}
		_, err := p.rewrite([]byte(fmp4Fixture+tag+"\n"), base)
		if !errors.Is(err, errProxyUnsupported) {
			t.Fatal("fMP4 恢复时放宽了其他边界")
		}
	}
	if proxyContentType(".mp4") != "video/mp4" || proxyContentType(".m4s") != "video/mp4" {
		t.Fatal("fMP4 内容类型缺省值错误")
	}
}
