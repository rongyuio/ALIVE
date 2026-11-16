package playback

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	"alive/internal/live"
)

// TestVideoResume 量「后台收听」回到播放页时，画面恢复要多久。
//
// 后台收听把视频轨关掉（vid=no），只让音频继续。回来时重新选轨，mpv 得
// 从下一个关键帧开始解码——直播切片 2 秒、GOP 1~4 秒，这个数直接决定
// 回来时会不会先黑一段，也就决定方案成不成立。
//
// 判据是渲染序号（Frame 的 serial）重新推进，不是 time-pos 变化：音频
// 时钟一直在走，只有画面出来 serial 才会动。
func TestVideoResume(t *testing.T) {
	room := os.Getenv("ALIVE_LIVE_ROOM")
	if room == "" {
		t.Skip("设 ALIVE_LIVE_ROOM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	e, err := Open(ctx, v.URL, Options{
		Headers: v.Headers, Width: v.Width, Height: v.Height, FrameRate: v.FrameRate,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()

	waitFresh := func(after uint64, budget time.Duration) time.Duration {
		start := time.Now()
		deadline := start.Add(budget)
		for time.Now().Before(deadline) {
			if _, serial := e.p.Frame(); serial != after {
				return time.Since(start)
			}
			time.Sleep(2 * time.Millisecond)
		}
		return 0
	}

	// 首帧：先等到画面，否则后面量的是「引擎还没起来」。
	if waitFresh(0, 20*time.Second) == 0 {
		t.Fatalf("20 秒没有首帧")
	}

	const rounds = 3
	resume := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		// 关视频轨，等两轮渲染过去，确认 serial 真的停住。
		if err := e.p.SetVideo(false); err != nil {
			t.Fatalf("SetVideo(false): %v", err)
		}
		time.Sleep(1500 * time.Millisecond)
		_, stopped := e.p.Frame()
		time.Sleep(500 * time.Millisecond)
		if _, again := e.p.Frame(); again != stopped {
			t.Fatalf("第 %d 轮：vid=no 之后画面还在推进（%d → %d）", i+1, stopped, again)
		}

		// 音频必须还在走，否则后台收听本身就没有意义。
		posBefore := e.p.State().Position
		time.Sleep(500 * time.Millisecond)
		if pos := e.p.State().Position; pos <= posBefore {
			t.Fatalf("第 %d 轮：vid=no 之后音频时钟停了（%v → %v）", i+1, posBefore, pos)
		}

		if err := e.p.SetVideo(true); err != nil {
			t.Fatalf("SetVideo(true): %v", err)
		}
		d := waitFresh(stopped, 15*time.Second)
		if d == 0 {
			t.Fatalf("第 %d 轮：15 秒没有恢复画面", i+1)
		}
		t.Logf("第 %d 轮：恢复画面 %v", i+1, d)
		resume = append(resume, d)
	}

	sort.Slice(resume, func(i, j int) bool { return resume[i] < resume[j] })
	t.Logf("恢复延迟 中位数 %v（样本 %v）", resume[len(resume)/2], resume)
}

// TestBackgroundListenKeepsAudio 验后台收听真正承诺的那件事：
// 视频轨关掉之后音频时钟继续走，重新打开之后画面回来。
//
// TestVideoResume 量的是延迟，这一条守的是语义——两者分开，因为延迟随平台
// GOP 变，而「关掉画面不停声音」是这套方案成立的前提，不该跟着网络抖。
func TestBackgroundListenKeepsAudio(t *testing.T) {
	room := os.Getenv("ALIVE_LIVE_ROOM")
	if room == "" {
		t.Skip("设 ALIVE_LIVE_ROOM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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

	e, err := Open(ctx, v.URL, Options{
		Headers: v.Headers, Width: v.Width, Height: v.Height, FrameRate: v.FrameRate,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer e.Close()

	// 等首帧，否则下面量的是「引擎还没起来」。
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, ok := e.Frame(time.Now()); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, _, ok := e.Frame(time.Now()); !ok {
		t.Fatal("20 秒没有首帧")
	}

	// 关视频轨，静置两秒：音频必须还在走。
	e.SetVideoEnabled(false)
	time.Sleep(2 * time.Second)
	before := e.Position()
	time.Sleep(time.Second)
	after := e.Position()
	if after <= before {
		t.Fatalf("关掉视频轨之后音频时钟停了：%v → %v", before, after)
	}
	t.Logf("后台一秒推进 %v", after-before)

	// 重新打开：画面必须回来。
	e.SetVideoEnabled(true)
	deadline = time.Now().Add(15 * time.Second)
	back := false
	for time.Now().Before(deadline) {
		if _, changed, ok := e.Frame(time.Now()); ok && changed {
			back = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !back {
		t.Fatal("重新打开视频轨之后 15 秒没有画面")
	}
}
