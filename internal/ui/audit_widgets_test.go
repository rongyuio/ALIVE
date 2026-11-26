package ui

import (
	"testing"
)

// M4 回归：翻牌旧字形必须取「上一秒同一槽位的真实字符」。
// 旧实现用 ch-1，十位 5→0 时把 5 画成 9。
func TestClockDigitCharTakesRealPreviousDigit(t *testing.T) {
	cases := []struct {
		v, i int
		want string
	}{
		{59, 0, "5"}, // 秒十位：整分钟前是 5，不是 9
		{59, 1, "9"},
		{0, 0, "0"},
		{0, 1, "0"},
		{23, 0, "2"},
		{7, 0, "0"},
		{7, 1, "7"},
	}
	for _, c := range cases {
		if got := clockDigitChar(c.v, c.i); got != c.want {
			t.Errorf("clockDigitChar(%d,%d)=%q want %q", c.v, c.i, got, c.want)
		}
	}
	if clockDigitChar(5, 5) != "" {
		t.Error("越界应该返回空串")
	}
}

// M15 回归：滚轮增量的方向必须与播放页一致（上滚为正增量）。
func TestWatchWheelStepDirection(t *testing.T) {
	if up := watchWheelStep(-100); up <= 0 {
		t.Fatalf("上滚（dy<0）应给出正增量，得到 %v", up)
	}
	if down := watchWheelStep(100); down >= 0 {
		t.Fatalf("下滚（dy>0）应给出负增量，得到 %v", down)
	}
	// 应用处是 volume + step：上滚要变大。
	if got := float32(0.5) + watchWheelStep(-100); got <= 0.5 {
		t.Fatalf("上滚后音量应增大，得到 %v", got)
	}
}
