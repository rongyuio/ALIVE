package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 悬停底色的唯一写法。
//
// 同一枚控件上，底色和文字色必须是同一族动效：底色瞬间跳、文字在弹，读起来
// 是两种反馈，用户分不清哪一下代表"选中了"。以前每个控件各写一遍
// `if hovered { el.Background(x) }`，于是同一屏里有的弹、有的跳。
//
// 四个取用函数覆盖全部情形：
//
//	hoverSurface      常态透明，指针经过时铺一层底（图标按钮、行）
//	hoverSurfaceOn    同上，但只在满足条件时铺（选中项不叠 hover 底）
//	hoverSurfaceRest  常态有色，指针经过时换成另一色（有底色的胶囊、按钮）
//	hoverSurfaceRestOn 上一条 + 条件
//
// **底色是赋值不是叠加。** `Element.Background` 在 MyGo 里就是 `e.bg = c`，
// 最后一次生效。所以「先 Background(常态色)，再 Background(hoverSurface(...))」
// 会把常态色整块抹成透明——指针不在上面时按钮没有底。
// 这一条曾经在 9 处同时犯（重试键、退出直播间、键盘选中的搜索结果、订阅、
// 同步面板主按钮、岛上的 X…），其中几处让关键按钮在静止态完全不可读。
// 有常态底色的控件一律用后两个函数，把常态色当场传进来。
//
// 透明那一端用 ui.Transparent 而不是"不设置"：AnimateColor 需要两端都有值
// 才能插值，缺席的那一端按透明算。
func hoverSurface(el ui.Element, key string, hovered bool, on ui.Color) ui.Color {
	return hoverSurfaceRestOn(el, key, hovered, true, ui.Transparent, on)
}

func hoverSurfaceOn(el ui.Element, key string, hovered, enabled bool, on ui.Color) ui.Color {
	return hoverSurfaceRestOn(el, key, hovered, enabled, ui.Transparent, on)
}

// hoverSurfaceRest 是带常态色的版本：指针不在时 rest，经过时 on。
func hoverSurfaceRest(el ui.Element, key string, hovered bool, rest, on ui.Color) ui.Color {
	return hoverSurfaceRestOn(el, key, hovered, true, rest, on)
}

// hoverSurfaceRestOn 是上面几条的唯一实现。
//
// enabled 为假时始终取 rest：选中项不叠 hover 底（否则会盖住它自己的滑块／
// 选中色），这是「结果列表里键盘 ↑↓ 选中的那一条」用的形态。
func hoverSurfaceRestOn(el ui.Element, key string, hovered, enabled bool, rest, on ui.Color) ui.Color {
	c := rest
	if hovered && enabled {
		c = on
	}
	return el.AnimateColor(key, c, theme.HoverInk())
}

// hoverInk 是"指针经过时字色变一下"的统一写法。
//
// 和 hoverSurface 同一族动效：同一枚控件上底色与字色必须同步，否则一半在弹、
// 一半在跳。rest 是常态色，on 是悬停色。
func hoverInk(el ui.Element, key string, hovered bool, rest, on ui.Color) ui.Color {
	c := rest
	if hovered {
		c = on
	}
	return el.AnimateColor(key, c, theme.HoverInk())
}
