package theme

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 动画只有这几档。控件不许自己发明第六档时长——散着写就是"这里 190、那里
// 240、另一个 320"，同一屏里三种节奏，用户读不出哪一下是同一件事。
//
// 分档的依据是**变化的量**，不是控件种类：
//
//	微反馈   颜色/位移的小变化（hover 底色、图标、缩放）
//	chrome   边框、箭头、标签着色这类"部件自己在变"
//	开关     两态之间的滑块（开关、通知进出）
//	展开     尺寸变化（搜索框宽、面板展开）
//	表面     一个表面换成另一个表面（飞行块、路由过渡）
//
// 弹簧（Slide / SlideFar）不走这张表：它们按"目标还会再变"设计，见下面。
const (
	MotionMicro   = 160 * time.Millisecond
	MotionChrome  = 190 * time.Millisecond
	MotionToggle  = 220 * time.Millisecond
	MotionReveal  = 240 * time.Millisecond
	MotionSurface = 280 * time.Millisecond

	// MotionSlack 是过渡窗口的余量：截止时刻比动画本身晚这么一点，让收尾
	// 那一帧的布局稳定下来再切状态。它不是第五档动画，是"演完之后再等一会"。
	MotionSlack = 120 * time.Millisecond

	// SpinPeriod 是所有转圈（spinner、按钮忙碌、环）的周期。以前三处各写
	// 800/900/900，同一屏里三个圈转速不同。
	SpinPeriod = 800 * time.Millisecond
)

// Micro 是微反馈：hover 底色、图标色、缩放。
func Micro() ui.Animation { return EaseOut(MotionMicro) }

// Chrome 是部件自身的着色：边框、箭头、标签。
func Chrome() ui.Animation { return EaseOut(MotionChrome) }

// Toggle 是两态滑块。
func Toggle() ui.Animation { return EaseInOut(MotionToggle) }

// Reveal 是尺寸变化：展开 / 收起。
func Reveal() ui.Animation { return EaseOut(MotionReveal) }

// HoverInk 是"指针经过时颜色变一下"的统一动效。
//
// 用 Slide 弹簧而不是 EaseOut：指针快速划过一排控件时，弹簧会把上一次的速度
// 带进新目标，不会每个控件都从零起跑、看起来一顿一顿。这一条和滑块同一族。
func HoverInk() ui.Animation { return Slide() }
