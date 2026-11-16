package playback

import (
	"context"
	"os"
	"testing"
	"time"

	"alive/internal/live"
)

// 真实房间的端到端验证：打开、解码、取帧、量音频连续性。
//
// 判断标准只有两条：
//   - 首帧要出来
//   - 音频不能断供（这是用户听到的「卡顿」）
func TestLiveEngine(t *testing.T) {
	room := os.Getenv("ALIVE_LIVE_ROOM")
	if room == "" {
		t.Skip("设 ALIVE_LIVE_ROOM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	prov, ok := live.ProviderFor(live.Platform(os.Getenv("ALIVE_LIVE_PLATFORM")))
	if !ok {
		t.Fatal("没有这个平台")
	}
	st, err := prov.(live.Streamer).Stream(ctx, room, "")
	if err != nil || len(st.Variants) == 0 {
		t.Fatalf("取流失败: %v", err)
	}
	v := st.Variants[0]
	for _, c := range st.Variants {
		if c.Width > v.Width {
			v = c
		}
	}
	t.Logf("平台声明 codec=%s %dx%d", v.Codec, v.Width, v.Height)

	start := time.Now()
	e, err := Open(ctx, v.URL, Options{
		Headers: v.Headers, Width: v.Width, Height: v.Height, FrameRate: v.FrameRate,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()
	t.Logf("Open 返回 %v", time.Since(start))

	// 首帧
	var first time.Duration
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := e.Frame(time.Now()); ok {
			first = time.Since(start)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if first == 0 {
		frames, dropped, audio := e.Stats()
		st, serr := e.State()
		t.Fatalf("15 秒没有首帧（frames=%d dropped=%d audio=%v state=%v err=%v）",
			frames, dropped, audio, st, serr)
	}
	// CPU 转换这条路是截图验收和没有 GPU 时的渲染器走的，它有一条自己的坑：
	// mpv 的 bgr0 第四字节是不确定值（实测为 0），直接当 alpha 用就是整块透明，
	// 屏幕上是好的、截出来全是黑。这里守住两件事：有内容，且不透明。
	time.Sleep(2 * time.Second)
	if pic, _, ok := e.Frame(time.Now()); ok {
		rgba := pic.RGBA(nil)
		if len(rgba) != 4*pic.W*pic.H {
			t.Fatalf("CPU 转换长度 %d，期望 %d", len(rgba), 4*pic.W*pic.H)
		}
		nonzero, opaque := 0, 0
		for i := 0; i+3 < len(rgba); i += 4 {
			if rgba[i] != 0 || rgba[i+1] != 0 || rgba[i+2] != 0 {
				nonzero++
			}
			if rgba[i+3] == 255 {
				opaque++
			}
		}

		total := len(rgba) / 4
		t.Logf("CPU 转换：非黑像素 %d / %d，不透明 %d / %d", nonzero, total, opaque, total)
		if nonzero < total/2 {
			t.Errorf("CPU 转换几乎没有内容：%d / %d", nonzero, total)
		}
		if opaque != total {
			t.Errorf("CPU 转换的 alpha 不是全 255：%d / %d，软件光栅化会当成整块透明", opaque, total)
		}
	}
	frames, dropped, audioOn := e.Stats()
	w, h := e.Size()
	t.Logf("首帧 %v，%dx%d，frames=%d dropped=%d audio=%v",
		first, w, h, frames, dropped, audioOn)

	// 取帧要一直跑，否则测不出真实节奏。
	go func() {
		tk := time.NewTicker(16 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				e.Frame(time.Now())
			}
		}
	}()

	// 音频连续性：每 100ms 采一次 Position，前进不足 80ms 就算一次断供。
	if !audioOn {
		t.Fatal("没有音频输出")
	}
	// 避开起播：预缓冲 + 音频设备打开有 1~2 秒，那不是稳态问题。
	time.Sleep(3 * time.Second)
	prev := e.Position()
	begin := time.Now()
	stalls, worst := 0, time.Duration(0)
	for time.Since(begin) < 12*time.Second {
		time.Sleep(100 * time.Millisecond)
		cur := e.Position()
		d := cur - prev
		prev = cur
		if d < 80*time.Millisecond {
			stalls++
			if worst == 0 || d < worst {
				worst = d
			}
			t.Logf("  断供 t=%v 只前进 %v", time.Since(begin).Truncate(10*time.Millisecond), d.Truncate(time.Millisecond))
		}
	}
	frames2, dropped2, _ := e.Stats()
	dbg := e.Debug()
	t.Logf("12 秒内音频断供 %d 次（最差 %v）；上屏 %d 帧、丢 %d 帧；phase=%s pos=%v buffering=%v",
		stalls, worst, frames2-frames, dropped2-dropped, dbg.Phase, dbg.Position, dbg.Buffering)
	if stalls > 3 {
		t.Errorf("音频断供 %d 次，太多了", stalls)
	}
}
