package playback

import (
	"context"
	"os"
	"testing"
	"time"
)

// Size() 只是查尺寸，不能算「有人在看」。
//
// 渲染门控的判据是 Player.lastPull：Frame() 会刷它，Size() 不该。以前
// Engine.Size 借道 Frame()，于是后台收听 / 墙上隐藏的格子只要被问一次尺寸，
// 就被当成有人在看、恢复全尺寸渲染——「没人看就不渲」永远生效不了。
//
// idle 没有导出，这里用 State().Renders 观察：从 idle 恢复会强制渲一帧，
// Renders 随之增长；先确认这一路真的进了 idle，进不了就跳过。
func TestSizeDoesNotWakeIdleRenderer(t *testing.T) {
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

	// 起播：拿到几帧新画面，渲染线程才真正在跑。
	deadline := time.Now().Add(10 * time.Second)
	frames := 0
	for time.Now().Before(deadline) && frames < 5 {
		if _, changed, ok := e.Frame(time.Now()); ok && changed {
			frames++
		}
		time.Sleep(10 * time.Millisecond)
	}
	if frames < 5 {
		t.Fatalf("起播 10 秒只出了 %d 帧", frames)
	}

	// 没人取帧，等渲染门控进 idle（renderIdleAfter = 300ms）。
	time.Sleep(1500 * time.Millisecond)
	settle := e.Debug().Renders
	time.Sleep(200 * time.Millisecond)
	if got := e.Debug().Renders; got != settle {
		t.Skipf("渲染线程没有进入 idle（Renders %d→%d），这个断言需要一个安静的流", settle, got)
	}

	// 只问尺寸：不该把这一路叫醒。
	before := e.Debug().Renders
	for i := 0; i < 20; i++ {
		if w, h := e.Size(); w == 0 || h == 0 {
			t.Fatalf("有画面时 Size() 返回 %d×%d", w, h)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after := e.Debug().Renders; after != before {
		t.Fatalf("idle 之后只调 Size() 又渲了 %d 帧：Size 被当成了「有人在看」", after-before)
	}
}
