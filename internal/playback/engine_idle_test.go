package playback

import (
	"context"
	"os"
	"testing"
	"time"
)

// 没人看一段时间之后再有人看，必须立刻出新画面。
//
// 钉的是一个真实事故（2026-11-23）：渲染门控进入 idle 之后，Frame() 叫不醒
// 渲染线程——idle 标志在渲染线程里写反了，`Frame()` 读到「不在跳过」就既不
// 置 forceRender 也不发唤醒。解码这时已经挂起，mpv 不会再发更新回调，于是
// 整格画面冻住，由墙上 1.5 秒的停帧看门狗判死、重开整路，用户看到几秒到
// 二十秒的冻帧。
//
// 用本地合成源（mpv 的 lavfi demuxer），不联网、不依赖平台地址。
func TestFramesResumeAfterIdle(t *testing.T) {
	if os.Getenv("ALIVE_MPV_LIB") == "" {
		t.Skip("没有 ALIVE_MPV_LIB：跳过真实引擎用例")
	}
	e, err := Open(context.Background(), "av://lavfi:testsrc=size=160x90:rate=30", Options{
		Silent:        true,
		SkipAudioGate: true,
		NoBackBuffer:  true,
		Width:         160,
		Height:        90,
		FrameRate:     30,
	})
	if err != nil {
		t.Fatalf("打不开本地合成源：%v", err)
	}
	defer func() { _ = e.Close() }()
	e.SetPresentSize(160, 90)

	// pull 模拟显示这一路的视图：按帧率取帧。
	pull := func(d time.Duration) (frames int) {
		deadline := time.Now().Add(d)
		for time.Now().Before(deadline) {
			if _, changed, ok := e.Frame(time.Now()); ok && changed {
				frames++
			}
			time.Sleep(10 * time.Millisecond)
		}
		return frames
	}

	// 起播：最多等 10 秒，拿到 5 帧新画面。
	deadline := time.Now().Add(10 * time.Second)
	got := 0
	for time.Now().Before(deadline) && got < 5 {
		got += pull(100 * time.Millisecond)
	}
	if got < 5 {
		t.Fatalf("起播 10 秒只出了 %d 帧", got)
	}

	// 没人看：超过 renderIdleAfter（300ms）之后，门控会跳过渲染并挂起解码。
	time.Sleep(1500 * time.Millisecond)

	// 回来：1 秒内至少要有 3 帧新画面。强制重渲的那一帧不算证据——它画的
	// 还是旧内容；连续的新帧才说明解码和渲染都活着。
	start := time.Now()
	var frames int
	for time.Since(start) < time.Second && frames < 3 {
		if _, changed, ok := e.Frame(time.Now()); ok && changed {
			frames++
		}
		time.Sleep(10 * time.Millisecond)
	}
	if frames < 3 {
		t.Fatalf("重新取帧后 1 秒只出了 %d 帧：渲染线程没有被唤醒（idle 标志或解码挂起）", frames)
	}
}
