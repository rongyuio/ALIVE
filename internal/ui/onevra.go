package ui

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"
)

// 这一层薄壳补两件事，都是实测出来的、光看组件签名看不出的。
//
// 一、换面只能走 Material。Onevra 的 Popover/Dialog/Toast 默认铺
// glass.Glass{Tint: t.Background.Alpha(.92)}，而 MyGo 里材质会盖住
// Background：Material(glass{Tint:红}) 与 Background(绿) 同时设，画出来是
// 红的，顺序无关；Material(nil) 直接 panic。所以调用方想换面（换成
// theme.MediaPanel 那一族墨，或者换成 BgElevated 那种抬起面），必须也传
// 一个 glass.Glass，写 Background 没有用。
//
// 二、尺寸不在这里统一。Onevra 的尺寸锚在 t.Space(n)/t.Rem(n) 上，
// 我们手柄调的 24 / 30 / 34 不是它的整数档；九个弹层的宽、内距、圆角本来
// 也各不相同（232 的弹幕设置、380 的搜索、352 的配色、130 的清晰度下拉），
// 统一了反而要把差异再拆成参数传回来。逐处自己设。

// orPanel 把 Onevra 浮层的面换成 callers 要的那一层。
//
// 传 theme.MediaPanel 得到「压在画面上的墨」（不跟配色）；
// 传 theme.BgElevated 得到「抬起的面」（跟配色）。
func orPanel(el ui.Element, tint ui.Color) ui.Element {
	return el.Material(glass.Glass{Style: glass.Regular, Tint: tint})
}

// listAtEnd 报告虚拟列表是否已经滚到底。
//
// components.ListState 不导出 MyGo 的 AtEnd/NearEnd/EndReached
// （list.go:25-68 只有 ScrollTo/ScrollIntoView/ScrollToEnd/Visible/
// Focus/Focused/FocusWithin），所以首页的无限滚动与弹幕面板的"贴底"
// 判定改成用 Visible 的可见区间自己算。
func listAtEnd(s *components.ListState, count int) bool {
	if count == 0 {
		return true
	}
	first, last := s.Visible()
	if last < first {
		// 还没有布局过：按贴底算，和 ui.ListState.AtEnd 的第一帧一致。
		return true
	}
	return last >= count-1
}
