package media

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func testGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 255, 0, 255}}
	frame := func(idx uint8) *image.Paletted {
		img := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
		for i := range img.Pix {
			img.Pix[i] = idx
		}
		return img
	}
	g := &gif.GIF{
		Image: []*image.Paletted{frame(0), frame(1)},
		Delay: []int{5, 5}, // 50ms 一帧
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// 动图按帧解码，延迟按 GIF 的 1/100 秒换算。
func TestDecodeAnimated(t *testing.T) {
	c := testCache()
	c.decode = make(chan struct{}, maxDecodes)

	frames, delays, ok := c.decodeAnimated(64, testGIF(t))
	if !ok {
		t.Fatal("两帧 GIF 没被识别成动图")
	}
	if len(frames) != 2 || len(delays) != 2 {
		t.Fatalf("帧数 %d、延迟数 %d", len(frames), len(delays))
	}
	if delays[0] != 50*time.Millisecond {
		t.Fatalf("延迟 = %v，期望 50ms", delays[0])
	}

	// 静态图不能被当成动图。
	if _, _, ok := c.decodeAnimated(64, pngBytes(t)); ok {
		t.Fatal("PNG 被识别成了动图")
	}
}

// 时间到哪一帧：循环播放，返回离下一帧还有多久。
func TestAnimationFrameAt(t *testing.T) {
	f0, f1 := &ui.Bitmap{}, &ui.Bitmap{}
	a := &animation{
		frames: []*ui.Bitmap{f0, f1},
		delays: []time.Duration{100 * time.Millisecond, 100 * time.Millisecond},
		total:  200 * time.Millisecond,
	}
	base := time.Unix(1_700_000_000, 0)

	if bmp, wait := a.frameAt(base); bmp != f0 || wait != 100*time.Millisecond {
		t.Fatalf("0ms 时应显示第 0 帧，wait=%v", wait)
	}
	if bmp, _ := a.frameAt(base.Add(150 * time.Millisecond)); bmp != f1 {
		t.Fatal("150ms 时应显示第 1 帧")
	}
	// 一轮结束回到第 0 帧。
	if bmp, _ := a.frameAt(base.Add(250 * time.Millisecond)); bmp != f0 {
		t.Fatal("250ms 时应回到第 0 帧")
	}
}
