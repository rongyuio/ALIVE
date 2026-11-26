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
// 两个取用函数覆盖全部情形：
//
//	hoverSurface   指针经过时铺一层底（图标按钮、行、胶囊）
//	hoverSurfaceOn 只在满足条件时铺（选中项不叠 hover 底，否则会盖住滑块）
//
// 用 ui.Transparent 而不是"不设置"：AnimateColor 需要两端都有值才能插值，
// 缺席的那一端按透明算。
func hoverSurface(el ui.Element, key string, hovered bool, on ui.Color) ui.Color {
	return hoverSurfaceOn(el, key, hovered, true, on)
}

func hoverSurfaceOn(el ui.Element, key string, hovered, enabled bool, on ui.Color) ui.Color {
	c := ui.Transparent
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
