package ui

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// mediaPanel 给浮在画面上的面板上底。
//
// 弹幕设置、关键词面板、清晰度下拉都浮在视频上，是这个应用里第二类
// "下面真有内容"的地方。能玻璃就玻璃，系统要求减少透明时退回实色。
//
// 为什么不用整块 MediaPanel（0.88）当 tint：那个不透明度会把模糊全遮掉，
// 玻璃就只是贵一点的实色。这里用 0.72，字靠模糊后的背景仍然压得住。
func mediaPanel(c *ui.Context, el ui.Element) ui.Element {
	if c.Preferences().ReduceTransparency {
		return el.Background(theme.MediaPanel)
	}
	return el.Material(glass.Glass{Style: glass.Regular, Tint: theme.MediaPanelTint})
}
