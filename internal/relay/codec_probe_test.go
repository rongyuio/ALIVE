package relay

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestUnknownCodecProbe(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "ffprobe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ALIVE_FFMPEG_DIR", dir)
	for _, tc := range []struct{ raw, want string }{{"h264", "avc"}, {"hevc", "hevc"}, {"av1", "av1"}, {"unexpected", ""}} {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("ALIVE_RELAY_TEST_CODEC", tc.raw)
			got := probeVideoCodec(context.Background(), "https://example.invalid/live", nil)
			if got != tc.want {
				t.Fatalf("codec=%s want=%s", got, tc.want)
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		t.Setenv("ALIVE_RELAY_TEST_CODEC", "wait")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		start := time.Now()
		if got := probeVideoCodec(ctx, "https://example.invalid/live", nil); got != "" {
			t.Fatal("取消探测仍返回编码")
		}
		if time.Since(start) > time.Second {
			t.Fatal("探测未响应取消")
		}
	})
}
