package ui

import "testing"

// 拖动窗口时卡片宽度会在断点附近来回抖。分类胶囊的显示必须带回差：
// 进入 268 才显示、退出 252 才隐藏，中间保持上一次的状态。
func TestCategoryChipHysteresis(t *testing.T) {
	app := newApp()
	steps := []struct {
		w    float32
		want bool
	}{
		{300, true},  // 宽屏：显示
		{262, true},  // 落在回差里：保持
		{258, true},  // 仍在回差里：保持，不闪
		{251, false}, // 掉出下边界：隐藏
		{256, false}, // 没到上边界：保持隐藏
		{267, false}, // 差 1px：仍不显示
		{268, true},  // 跨过上边界：显示
		{300, true},
	}
	for _, step := range steps {
		if got := app.categoryChipVisible(step.w); got != step.want {
			t.Fatalf("宽度 %.0f：胶囊显示 = %v，期望 %v", step.w, got, step.want)
		}
	}
}

// 只抖动 1px 时状态不能翻转，这是拖动窗口最常见的输入。
func TestCategoryChipIgnoresOnePixelJitter(t *testing.T) {
	app := newApp()
	app.chipOn = true
	for i := 0; i < 200; i++ {
		w := float32(258 + i%3) // 257、258、259 来回抖
		if !app.categoryChipVisible(w) {
			t.Fatalf("1px 抖动把胶囊关掉了（宽度 %.0f）", w)
		}
	}
}
