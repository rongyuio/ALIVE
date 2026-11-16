package playback

import (
	"testing"
	"time"
)

// 音频门只在「画面到了、声音还没到」时压住画面；正常流一次都不触发。
func TestAudioGate(t *testing.T) {
	t0 := time.Now()
	for _, tc := range []struct {
		name string
		pos  time.Duration
		at   time.Duration
		want bool
	}{
		{"音频已起步", 20 * time.Millisecond, 0, true},
		{"音频没起步", 0, 0, false},
		{"音频没起步、同一时刻再问", 0, 0, false},
	} {
		g := audioGate{}
		if got := g.allow(tc.pos, t0.Add(tc.at)); got != tc.want {
			t.Fatalf("%s: allow=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 一旦放行就一直放行：音频起步之后再回到 0（换档、seek）不该重新压住画面。
func TestAudioGateStaysOpen(t *testing.T) {
	g := audioGate{}
	now := time.Now()
	if !g.allow(50*time.Millisecond, now) {
		t.Fatal("音频已起步却没放行")
	}
	if !g.allow(0, now.Add(time.Second)) {
		t.Fatal("放行之后又关上了")
	}
}

// 预算从「第一次拿到画面」开始算，不是从进程启动开始算。
//
// 4K 上首帧本身就要两秒才解出来；如果从 Open 起算，门在画面到达之前就过期，
// 等于没生效。
func TestAudioGateBudgetStartsAtFirstPicture(t *testing.T) {
	g := audioGate{}
	// 4K 上首帧本身要两秒才解出来，调用方在这之前根本不会问。
	pictureAt := time.Now().Add(2 * time.Second)
	if g.allow(0, pictureAt) {
		t.Fatal("第一帧就放行了")
	}
	if g.allow(0, pictureAt.Add(audioGateBudget-time.Millisecond)) {
		t.Fatal("预算内就放行了")
	}
	if !g.allow(0, pictureAt.Add(audioGateBudget+time.Millisecond)) {
		t.Fatal("预算用完仍压着画面")
	}
}
