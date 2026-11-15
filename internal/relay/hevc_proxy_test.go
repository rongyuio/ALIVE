package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 编码资格不能从上层借给不匹配的包装；HEVC / AV1 只开放带初始化段的分片。
func TestNativeCodecProxyPlaylistBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, codec, declared, media string
		want                         bool
	}{
		{"av1-fmp4", "av1", "av01.0.08M.08", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", true},
		{"av1-ts-with-map", "av1", "av01.0.08M.08", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.ts\n", false},
		{"av1-without-init", "av1", "av01.0.08M.08", "#EXTINF:2,\nseg.m4s\n", false},
		{"av1-mislabeled-hevc", "av1", "hvc1.1.6.L120.90", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", false},
		{"hevc-fmp4", "hevc", "hvc1.1.6.L120.90", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", true},
		{"hevc-ts-with-map", "hevc", "hvc1.1.6.L120.90", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.ts\n", false},
		{"hevc-ts", "hevc", "hvc1.1.6.L120.90", "#EXTINF:2,\nseg.ts\n", false},
		{"hevc-without-init", "hevc", "hev1.1.6.L120.90", "#EXTINF:2,\nseg.m4s\n", false},
		{"avc-mislabeled-hevc", "avc", "hvc1.1.6.L120.90", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", false},
		{"hevc-mislabeled-avc", "hevc", "avc1.640028", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", false},
		{"hevc-mislabeled-av1", "hevc", "av01.0.08M.08", "#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\nseg.m4s\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/entry.m3u8" {
					fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000,CODECS=\"%s,mp4a.40.2\"\nmedia.m3u8\n", tc.declared)
					return
				}
				fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n"+tc.media)
			}))
			defer up.Close()
			p, err := newHLSProxy(context.Background(), up.URL+"/entry.m3u8", nil, tc.codec)
			if p != nil {
				defer p.stop()
			}
			if tc.want {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, errProxyUnsupported) {
				t.Fatalf("unsupported error=%v", err)
			}
		})
	}
}
