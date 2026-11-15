package relay

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalPlaylistRequiresReferencedSegments(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		ready      bool
	}{
		{"complete", "#EXTM3U\n#EXTINF:1,\nready.ts\n", true},
		{"missing", "#EXTM3U\n#EXTINF:1,\nmissing.ts\n", false},
		{"partial", "#EXTM3U\n#EXTINF:1,\nready.ts\n#EXTINF:1,\nmissing.ts\n", false},
		{"empty", "#EXTM3U\n#EXTINF:1,\nempty.ts\n", false},
		{"no-duration", "#EXTM3U\nready.ts\n", false},
		{"invalid-duration", "#EXTM3U\n#EXTINF:NaN,\nready.ts\n", false},
		{"incomplete", "#EXTM3U\n#EXTINF:1,\n", false},
		{"escape", "#EXTM3U\n#EXTINF:1,\n../ready.ts\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{"live.m3u8": tc.body, "ready.ts": "data", "empty.ts": ""} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if got := localPlaylistReady(dir); got != tc.ready {
				t.Fatalf("ready=%v want=%v", got, tc.ready)
			}
		})
	}
}

func TestCompletedRemuxStartup(t *testing.T) {
	for _, mode := range []string{"clean", "empty", "failed"} {
		t.Run(mode, func(t *testing.T) {
			fakeFFmpeg(t, false)
			t.Setenv("ALIVE_RELAY_TEST_EXIT", mode)
			s := New()
			defer s.Stop()
			local, err := s.Start(context.Background(), "https://cdn.invalid/live.flv", nil, "avc", Options{})
			if mode == "clean" {
				if err != nil {
					t.Fatal(err)
				}
				resp, _ := fetch(t, local, nil)
				if resp.StatusCode != 200 || s.Mode() != modeRemux {
					t.Fatal("正常结束的输出不可播")
				}
			} else if err == nil || s.URL() != "" {
				t.Fatal("失败输出被接管")
			}
		})
	}
}
