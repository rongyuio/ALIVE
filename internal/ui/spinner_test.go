package ui

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// spinner 以前靠一个包级 spinDriven 标记选「本帧第一个」去挂 Loop。那是构建期状态
// 住在包变量里（go tool mygo vet 看不见它），一旦有第二个根视图放 spinner，它的环
// 就不会请求帧、静默停住。现在每个 spinner 自己请求下一帧，这两条钉住后果：
// 环还在请求帧、相位还在走、同一帧里多个环同相位。
func TestSpinnerKeepsFramesComing(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Column(c).Fill().Center().Children(func() { components.Spinner(c, components.SpinnerRing).Size(24, 24) })
	}
	tt := ui.NewTester(view, 40, 40)
	tt.Frame()
	if !tt.FramePending() {
		t.Fatal("spinner 没有请求下一帧——环转不起来")
	}
	first := tt.Image()
	tt.Advance(200 * time.Millisecond)
	if sameRGBA(first, tt.Image()) {
		t.Fatal("过了 200ms 画面一个字节都没变——相位没走")
	}
}

// 同一帧里的两个环必须同相位：转角来自 c.Now()，不该各转各的。
func TestSpinnersSharePhase(t *testing.T) {
	view := func(c *ui.Context) {
		ui.Row(c).Fill().Center().Gap(16).Children(func() {
			components.Spinner(c, components.SpinnerRing).Size(24, 24)
			components.Spinner(c, components.SpinnerRing).Size(24, 24)
		})
	}
	const w, h = 96, 40
	tt := ui.NewTester(view, w, h)
	tt.Frame()
	img := tt.Image()
	// 两个 24×24 的环居中对齐：总宽 64，左边起点 x=16，右边 x=56；竖直居中 y=8。
	left := subRGBA(img, image.Rect(16, 8, 40, 32))
	right := subRGBA(img, image.Rect(56, 8, 80, 32))
	if left == nil || right == nil {
		t.Fatal("取不到环的像素")
	}
	if !sameRGBA(left, right) {
		t.Fatal("同一帧里的两个环相位不同：转角没有共用视图时钟")
	}
}

func sameRGBA(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	return bytes.Equal(a.Pix, b.Pix)
}

func subRGBA(img *image.RGBA, r image.Rectangle) *image.RGBA {
	if !r.In(img.Bounds()) {
		return nil
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		copy(out.Pix[y*out.Stride:y*out.Stride+r.Dx()*4], img.Pix[(r.Min.Y+y)*img.Stride+r.Min.X*4:])
	}
	return out
}
