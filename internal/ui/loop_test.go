package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// Loop 必须回绕：这版 MyGo 的 Keyframes + RepeatForever 播完一次就停，
// 绿点脉冲、眨眼、骨架脉冲都改用了 Loop，这条守住「会回来」。
func TestLoopWraps(t *testing.T) {
	view := func(c *ui.Context) {
		b := ui.Box(c.Key("probe")).Label("probe").
			Absolute().Top(0).Size(10, 10).Background(ui.RGB(255, 0, 0))
		b.Left(b.Loop("l", time.Second, ui.Linear) * 100)
	}
	tt := ui.NewTester(view, 200, 200)

	tt.Advance(500 * time.Millisecond)
	mid, ok := tt.Find("probe")
	if !ok {
		t.Fatal("找不到探针")
	}
	// 1600ms = 第二圈的 600ms
	tt.Advance(1100 * time.Millisecond)
	wrapped, _ := tt.Find("probe")

	t.Logf("500ms=%.0f 1600ms=%.0f", mid.X, wrapped.X)
	if mid.X < 40 || mid.X > 60 {
		t.Errorf("500ms 时 x=%.0f，期望约 50", mid.X)
	}
	if wrapped.X < 50 || wrapped.X > 70 {
		t.Errorf("1600ms 时 x=%.0f，期望回到第二圈的约 60（不回绕会停在 100）", wrapped.X)
	}
}

// 脉冲环的相位映射：越往后越大、越淡，70% 之后停在扩满透明。
func TestLivePulse(t *testing.T) {
	s0, a0 := livePulse(0)
	s1, a1 := livePulse(0.35)
	s2, a2 := livePulse(0.7)

	if !(a0 > a1 && a1 > a2) {
		t.Errorf("透明度没有递减：%.2f %.2f %.2f", a0, a1, a2)
	}
	if !(s0 < s1 && s1 < s2) {
		t.Errorf("scale 没有递增：%.2f %.2f %.2f", s0, s1, s2)
	}
	if s2 != 1.85 || a2 != 0 {
		t.Errorf("70%% 后应停在 1.85/0，实际 %.2f/%.2f", s2, a2)
	}
}
