package video

import (
	"os"
	"strings"
	"testing"
	"time"
)

// 没人取帧的时候跳过渲染——这是"暂离"最便宜的一半。
//
// 判据是纯函数，因为真正的跳过发生在 libmpv 的渲染线程里，没有真流跑不起来。
// 这里钉住三条边界：没渲过第一帧不跳（否则 Ready 永远是假）、刚取过帧不跳、
// 超过阈值才跳。
func TestRenderIdle(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name     string
		hasFrame bool
		lastPull time.Time
		want     bool
	}{
		{"还没渲过第一帧", false, now.Add(-time.Minute), false},
		{"从没人取过帧", true, time.Time{}, false},
		{"刚取过", true, now.Add(-10 * time.Millisecond), false},
		{"阈值内", true, now.Add(-renderIdleAfter + 10*time.Millisecond), false},
		{"超过阈值", true, now.Add(-renderIdleAfter - time.Millisecond), true},
	} {
		if got := renderIdle(tc.hasFrame, tc.lastPull, now); got != tc.want {
			t.Errorf("%s：renderIdle=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// idle 之后第一次取帧必须强制渲染并唤醒渲染线程。
//
// 2026-11-23 的事故：这里读到的 idle 是反的，于是"没人看 → 再有人看"既不
// 置 forceRender 也不发唤醒。mpv 在没人看时本来就不发更新回调，渲染线程
// 永远不醒，墙上的一格就一直冻着，直到上层 1.5 秒的停帧看门狗把整路重开。
func TestFrameWakesAfterIdle(t *testing.T) {
	p := &Player{idle: true, renderWake: make(chan struct{}, 1)}
	p.Frame()
	if !p.forceRender {
		t.Fatal("idle 之后的取帧没有置 forceRender：恢复时画的是旧内容")
	}
	select {
	case <-p.renderWake:
	default:
		t.Fatal("idle 之后的取帧没有唤醒渲染线程：没人再会画这一路了")
	}

	// 没进 idle 时不该每帧都强制重渲——渲染由 mpv 的更新回调驱动。
	q := &Player{renderWake: make(chan struct{}, 1)}
	q.Frame()
	if q.forceRender {
		t.Fatal("不在 idle 时取帧也置了 forceRender")
	}
	select {
	case <-q.renderWake:
		t.Fatal("不在 idle 时取帧也唤醒了渲染线程")
	default:
	}
}

// 静态守卫：渲染门控只跳过渲染，不许挂起解码。
//
// vd-lavc-skipframe 会把"回来"的延迟绑到关键帧间隔上：all 断参考帧链，
// 实测（2026-11-23，B 站 7734200）恢复要 8–22 秒；nonref 保住链也要约
// 2.2 秒。只跳渲染、解码继续，12 路恢复约 130ms，隐藏期间多花 0.30 核
// （12 路，零拷贝）。要省带宽走用户的「暂离时省流量」，那条路直接关会话。
func TestRenderGateDoesNotSuspendDecode(t *testing.T) {
	src, err := os.ReadFile("player.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "options/vd-lavc-skipframe") {
		t.Fatal("渲染门控在挂起解码：恢复会把延迟绑到关键帧间隔上（实测最长 22 秒）")
	}
}
