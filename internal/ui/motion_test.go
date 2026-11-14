package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 系统要求减少动效时，循环动画必须停在最终值而不是继续跑：
// 骨架停在 opacity 0.8，眼睛停在 scaleY 1，在线环停在「扩满且透明」。
func TestReducedMotionRenders(t *testing.T) {
	for _, reduce := range []bool{false, true} {
		app := NewMockApp()
		app.switchTab("follows")
		tt := ui.NewTesterWith(app.Shell, 1440, 900, ui.TesterOptions{
			Preferences: ui.Preferences{ReduceMotion: reduce},
		})
		if _, ok := tt.Find("搜索已关注的主播"); !ok {
			t.Fatalf("reduce=%v：关注页没有渲染", reduce)
		}
	}
}

// 首屏骨架：列表为空且处于加载态时出现，reduced motion 下也要能正常构建。
func TestSkeletonRenders(t *testing.T) {
	app := NewMockApp()
	app.Loading = true
	app.Platform = "none" // 没有这个平台的示例数据，feed 为空
	ui.NewTesterWith(app.Shell, 1440, 900, ui.TesterOptions{
		Preferences: ui.Preferences{ReduceMotion: true},
	})
}
