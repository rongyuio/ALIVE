package playback

import (
	"bytes"
	"testing"

	"github.com/egoist/mygo/plugins/video"
)

// 软件渲染帧的 CPU 转换：通道换回 RGBA，alpha 一律 255。
//
// bgr0 的第四个字节是垃圾值（实测 0），CPU 光栅化按预乘 alpha 混合会画成
// 透明——截图里播放区纯黑。这条断言原本写在已删除的 live_test.go 里，这里补回。
func TestRGBAOpaque(t *testing.T) {
	f := &video.Frame{
		Luma:   []byte{1, 2, 3, 0, 4, 5, 6, 0},
		Stride: 8,
		W:      2,
		H:      1,
	}
	got := rgbaOpaque(nil, f)
	want := []byte{3, 2, 1, 255, 6, 5, 4, 255}
	if !bytes.Equal(got, want) {
		t.Fatalf("转换结果 %v，期望 %v", got, want)
	}
	// 复用调用方给的缓冲：截图路径每帧都会带着上一帧的缓冲来。
	buf := make([]byte, 0, 8)
	if got := rgbaOpaque(buf, f); len(got) != 8 || &got[0] != &buf[:8][0] {
		t.Fatal("容量够时应当复用调用方的缓冲")
	}
}
