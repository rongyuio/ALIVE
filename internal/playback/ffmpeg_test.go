package playback

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"alive/internal/live"
)

// TestFfmpegControl 用同一份签名地址跑 ffmpeg 命令行，作为自带解码的对照组。
//
// 这是判断「卡是上游的锅还是我们的锅」最快的一件事：同一个 URL、同一时刻，
// 命令行能把 8 秒的流几秒钟读完，说明供给没问题，卡在我们这边；命令行也卡，
// 那就是 CDN 或这个档位本身。
//
// 默认跳过，设 ALIVE_CTRL_ROOM / ALIVE_CTRL_PLATFORM 才跑。可选 ALIVE_CTRL_LABEL
// 指定档位（默认取分辨率最高的一档）。地址不会打印出来——它带签名。
func TestFfmpegControl(t *testing.T) {
	room := os.Getenv("ALIVE_CTRL_ROOM")
	plat := os.Getenv("ALIVE_CTRL_PLATFORM")
	if room == "" || plat == "" {
		t.Skip("设 ALIVE_CTRL_ROOM / ALIVE_CTRL_PLATFORM")
	}
	prov, ok := live.ProviderFor(live.Platform(plat))
	if !ok {
		t.Fatalf("没有平台 %q", plat)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	st, err := prov.(live.Streamer).Stream(ctx, room, "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	want := os.Getenv("ALIVE_CTRL_LABEL")
	var v live.Variant
	for _, c := range st.Variants {
		if want != "" && c.Label == want {
			v = c
			break
		}
		if want == "" && c.Height > v.Height {
			v = c
		}
	}
	if v.URL == "" {
		t.Fatalf("没有档位 %q", want)
	}

	var ua, ref string
	for _, h := range v.Headers {
		switch {
		case strings.HasPrefix(h, "User-Agent:"):
			ua = strings.TrimSpace(strings.TrimPrefix(h, "User-Agent:"))
		case strings.HasPrefix(h, "Referer:"):
			ref = strings.TrimSpace(strings.TrimPrefix(h, "Referer:"))
		}
	}

	ff := os.Getenv("ALIVE_FFMPEG")
	if ff == "" {
		ff = "ffmpeg"
	}
	args := []string{"-hide_banner", "-nostdin"}
	if ua != "" {
		args = append(args, "-user_agent", ua)
	}
	if ref != "" {
		args = append(args, "-referer", ref)
	}
	args = append(args, "-i", v.URL, "-t", "8", "-f", "null", "-")

	start := time.Now()
	out, err := exec.Command(ff, args...).CombinedOutput()
	elapsed := time.Since(start)
	last := ""
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "speed=") {
			last = strings.TrimSpace(l)
		}
	}
	t.Logf("%s %s：8 秒流用 %v（err=%v）", plat, v.Label, elapsed.Round(time.Millisecond), err)
	t.Logf("  %s", last)
	if elapsed > 8*time.Second {
		t.Logf("  命令超过实时长度，说明上游供给慢或这个档位有问题")
	}
}
