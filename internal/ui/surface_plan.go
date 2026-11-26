package ui

// 过渡的唯一求值点。
//
// 表面之间的动画只由三件事决定：**离开哪个表面、去哪个表面、这两端有没有
// 可用的几何**（矩形 / 岛在不在画）。这一间此刻有没有在出画面，只决定飞行块
// 里画什么（活画面还是封面），不决定"飞不飞"——那是 2026-11-25 那次修补留下
// 的分叉：同样的操作因为引擎状态不同而时飞时切。
//
// 所有进入 / 离开播放页的路径（卡片、关注行、订阅列表、搜索、岛、墙格、
// Esc、X、顶栏 tab）都必须走这里，不许在调用点自己再判一次。
//
// 四条规则：
//
//	1. 两端是同一枚画面（墙格 ↔ 播放页）  → Match，画面连续移动
//	2. 两端都有几何，且不是同一枚画面      → Fly（像素：有活画面用活画面，否则封面）
//	3. 任一端没有几何                      → Cut
//	4. 两端是同一个表面                    → Cut（没有变化，不该有动画）
//
// "结束这一路"（X / 退出直播间）不进规则 2：它没有"要去的地方"，除了墙格
// 本来就在继续画同一枚画面（Match）。用户明确关掉一个直播间，不需要收尾动画。

type transitionKind int

const (
	transitionCut transitionKind = iota
	transitionFly
	transitionMatch
)

// transitionPixels 是飞行块里该画什么。
type transitionPixels int

const (
	pixelsCover transitionPixels = iota
	pixelsLive
)

type transitionPlan struct {
	kind   transitionKind
	pixels transitionPixels
	// reason 只在 Cut 时有值：为什么没有动画。写日志、写测试都靠它，
	// 免得"这里为什么硬切"变成又一处口口相传。
	reason string
}

func planTransition(from, to picture, fromOK, toOK, live bool) transitionPlan {
	switch {
	case !fromOK || !toOK:
		return transitionPlan{kind: transitionCut, reason: "no-geometry"}
	case from == to:
		return transitionPlan{kind: transitionCut, reason: "same-surface"}
	case from == pictureTile && to == picturePlayer, from == picturePlayer && to == pictureTile:
		return transitionPlan{kind: transitionMatch, pixels: pixelsLive}
	}
	p := pixelsCover
	if live {
		p = pixelsLive
	}
	return transitionPlan{kind: transitionFly, pixels: p}
}

// surfaceHasGeometry 报告某个表面此刻在屏幕上有没有一个可用的落点。
//
// key 是这一间的房间键；route / section 是**当前正在构建的那一页**（进出都
// 以"这一帧屏幕上真有它的画面"为准，不以"从哪页点进来"为准）。
func (a *App) surfaceHasGeometry(p picture, key, route string, section int) bool {
	switch p {
	case picturePlayer:
		return route == routePlayer
	case pictureTile:
		// 墙格是**目标页**上的落点：进播放页时目标页是当前页，离开时目标页
		// 是 destRoute。用 route/section 参数判，不看"此刻"在哪一页。
		return route == routeFollows && section == followsSectionWatch && a.watchHas(key)
	case pictureCard:
		r := a.cardRectFor(key, route, section)
		return r.W > 0 && r.H > 0
	case pictureIsland:
		if a.WinFull || a.winFullscreen {
			return false
		}
		return a.focus != nil && (a.focus.player != nil || a.focus.loading)
	}
	return false
}
