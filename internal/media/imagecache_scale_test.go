package media

import (
	"testing"

	"github.com/egoist/mygo"
)

// 窗口在哪块屏上，解码密度就用那块屏的缩放。
//
// 双屏 DPI 不同是常态：只认主屏的话，窗口拖到另一块屏后封面和头像会
// 按错误密度解码——要么拉大糊掉，要么白解一张大图。
func TestScaleForBoundsFollowsWindow(t *testing.T) {
	displays := []mygo.Display{
		{Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}, ScaleFactor: 1.5},
		{Bounds: mygo.Rectangle{X: 1920, Y: 0, Width: 2560, Height: 1440}, ScaleFactor: 1},
	}

	if got := scaleForBounds(mygo.Rectangle{X: 100, Y: 100, Width: 800, Height: 600}, displays); got != 1.5 {
		t.Fatalf("窗口在主屏时 scale=%v，期望 1.5", got)
	}
	if got := scaleForBounds(mygo.Rectangle{X: 2000, Y: 100, Width: 800, Height: 600}, displays); got != 1 {
		t.Fatalf("窗口拖到副屏后 scale=%v，期望 1", got)
	}
	// 横跨两块屏：按重叠面积大的那块算，而不是左上角落在哪块。
	if got := scaleForBounds(mygo.Rectangle{X: 1800, Y: 100, Width: 800, Height: 600}, displays); got != 1 {
		t.Fatalf("横跨两块屏时 scale=%v，期望按面积大的副屏 1", got)
	}
}
