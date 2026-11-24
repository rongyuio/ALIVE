package ui

import "github.com/egoist/mygo/ui"

// 卡片的矩形：进播放页时画面从这里起飞，回列表时缩回这里。
//
// 卡片是虚拟化的——滚出屏幕就不在树上，也就没有矩形。所以记录必须带上
// 「哪一页」，并且窗口尺寸变过就作废：拿一张旧位置的卡片当落点，画面会
// 飞进一片空白。
type cardRect struct {
	rect       ui.Rect
	page       string
	winW, winH int
	// gen 是记下这条矩形时该页网格的构建代次。cardRectFor 只认「目标页
	// 最近一次构建」留下的记录：卡片滚出虚拟列表、换分区、换订阅 chip
	// 都会让旧代次自然过期，不需要在每一条路径上手动清 map。
	gen uint64
}

// beginGridPage / endGridPage 给一页网格的每次构建编号。
//
// 点击发生在构建中途，卡片这一帧的记录还没写；所以 cardRectFor 比对的是
// **上一次构建完成**的代次，而不是正在进行的这一帧。
func (a *App) beginGridPage(page string) uint64 {
	if a.gridGen == nil {
		a.gridGen = map[string]uint64{}
		a.gridDone = map[string]uint64{}
	}
	a.gridGen[page]++
	return a.gridGen[page]
}

func (a *App) endGridPage(page string, gen uint64) {
	if a.gridDone == nil {
		a.gridDone = map[string]uint64{}
	}
	a.gridDone[page] = gen
}

// noteCardRect 记下卡片这一帧的位置。列表每帧都调，只覆盖同一间的记录。
//
// page 由 grid 在开始铺卡片之前取：点击卡片会立刻换路由，同一个 build 里
// 后面的卡片不能按新路由记账——那会把首页的卡片记成播放页的。
func (a *App) noteCardRect(s Streamer, el ui.Element, page string, gen uint64) {
	r := el.Bounds()
	if r.W <= 0 || r.H <= 0 {
		return
	}
	if a.cardRects == nil {
		a.cardRects = map[string]cardRect{}
	}
	a.cardRects[watchKey(s)] = cardRect{
		rect: r,
		page: page,
		gen:  gen,
		winW: a.lastWindowW, winH: a.lastWindowH,
	}
}

// cardRectFor 取 key 在目标页上的卡片矩形。不是那一页、没记过、窗口尺寸
// 变过、或者那张卡在目标页最近一次构建里已经不在树上，都返回零值。
func (a *App) cardRectFor(key, route string, section int) ui.Rect {
	page := pageKey(a.Platform, route, section)
	r, ok := a.cardRects[key]
	if !ok || r.page != page || r.gen != a.gridDone[page] {
		return ui.Rect{}
	}
	if a.lastWindowW > 0 && (r.winW != a.lastWindowW || r.winH != a.lastWindowH) {
		return ui.Rect{}
	}
	return r.rect
}
