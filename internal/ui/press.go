package ui

import (
	"github.com/egoist/mygo/ui"
)

// 按下反馈。
//
// 全应用以前只有 hover 变色，按下时零反应：按下去和没按下去长得一样，只有
// 松开之后才有结果。Apple 那条「按下即响应」说的就是这件事——延迟一出现，
// 直接感就掉一档。
//
// 做法只有一层压暗，没有动画：按下是二值的，它要的是"立刻"。给它一段
// 过渡反而会让快速连点看起来像卡顿。松开时遮罩直接消失，和 CSS 的
// :active 同一条语义。
//
// 不用 Scale：MyGo 的缩放会让文字重新栅格化，40 高的按钮在 0.97 下按一下
// 会整块抖一次。亮度只动颜色，不碰排版。
const pressDimAlpha float32 = 0.16

// pressDim 在控件内部盖一层按下遮罩，调用点必须写在 Children 的最后。
//
// 遮罩 PassThrough：它只是装饰，压在按钮上不能把点击吃掉——否则按下去
// 的那一下反而落不到按钮上。
//
// radius 传控件自己的圆角，遮罩才不会在四角露出直边。
func pressDim(c *ui.Context, down bool, radius float32) {
	if !down {
		return
	}
	ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
		Radius(radius).Background(ui.RGBA(0, 0, 0, pressDimAlpha)).PassThrough()
}
