package video

import (
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
