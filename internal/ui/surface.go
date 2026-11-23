package ui

// 表面模型：四个"场景"其实是一个会话集合的三个视图加一个指示器。
//
// 产品里只有两个问题——声音在谁身上、画面出现在哪。这个文件把这两件事
// 收成纯函数，不碰播放器、不碰视图，好在 Tester 里穷举组合。
//
// 完整模型、七条不变量和操作路径见 doc/architecture/surfaces.md。

// surface 是一个会话此刻被画在哪种表面上。
//
// 它不是"模式"：同一个会话在任意时刻只有一处（或零处）全尺寸视图，
// 后台收听就是"没有任何全尺寸视图"的那一格，不是第四个并列状态。
type surface int

const (
	// surfaceNone 表示这一路没有可见视图：后台收听、或墙不可见。
	surfaceNone surface = iota
	// surfaceTile 表示被画在观测墙的格子里。
	surfaceTile
	// surfaceFull 表示被画成播放页的全尺寸画面。
	surfaceFull
)

func (s surface) String() string {
	switch s {
	case surfaceTile:
		return "tile"
	case surfaceFull:
		return "full"
	}
	return "none"
}

// focusKey 是当前出声的那一间；空串表示没有声音焦点。
//
// 它取代 a.WatchActive：墙上激活、播放页、后台收听都是同一个焦点，
// 区别只在焦点会话被画在哪。
//
// 判据是"谁真正握着这一路"：
//
//  1. WatchActive —— 墙上点过、正在出声的那一格；
//  2. media.roomKey —— 媒体层正在放的那一路。只认活着的会话：后台收听时
//     player 一定非 nil（background 也只会由「有引擎」的两条路置上），取流
//     途中 loading 为真；三者都没有就是这一轮已经落了终态（取流失败 /
//     未开播），把 roomKey 当焦点会让岛停在「后台收听」，点回去还是
//     同一张错误页；
//  3. 正在播放页上时，a.Player —— 那一屏画的就是它。这一条只在路由是
//     播放页时成立：示例数据没有房间号，roomKey 拼不出来；而离开播放页
//     之后 a.Player 还留着上一间，拿它兜底会让岛在声音已经停了之后继续
//     展开（从墙上放大再按 X 就是这样）。
func (a *App) focusKey() string {
	// WatchActive 也要认活着的会话：点一格离线/取流失败的格子，它确实
	// 成了焦点，但没有引擎、没有声音。无条件返回会让岛画出「后台收听」，
	// 点回去还是同一张未开播提示——和 media.roomKey 那条是同一个洞。
	if a.WatchActive != "" && a.watchFocusAlive(a.WatchActive) {
		return a.WatchActive
	}
	if a.media.roomKey != "" &&
		(a.media.eng() != nil || a.media.isLoading() || a.media.background) {
		return a.media.roomKey
	}
	if a.Router != nil && a.Router.Path() == routePlayer && a.Player.Name != "" {
		return watchKey(a.Player)
	}
	return ""
}

// watchFocusAlive 报告墙上这一格还有没有声音，或正在来的声音。
//
// 媒体层握着它时看引擎和 loading；退回墙上的看这一格自己的会话。
// 离线、取流失败、以及失败的终态都不算——没有声音就不该出现在岛上
// （doc/architecture/surfaces.md 不变量 5）。
func (a *App) watchFocusAlive(key string) bool {
	if a.media.roomKey == key {
		return a.media.eng() != nil || a.media.isLoading()
	}
	s := a.watch.sessions[key]
	if s == nil {
		// 交接间隙里可能还没有会话记录：这时别把岛闪回时钟。
		// 有记录但既没引擎也不在取流，才是真的没有声音。
		return true
	}
	return s.player != nil || s.loading
}

// surfaceFor 求某个会话此刻的可见表面。
//
// 三个判据，按优先级：播放页全尺寸 > 观测墙格子 > 无视图。媒体层在后台
// 收听时仍然持有引擎，所以"有播放器"不等于"有视图"。
func (a *App) surfaceFor(key string) surface {
	if key == "" {
		return surfaceNone
	}
	if a.Router != nil && a.Router.Path() == routePlayer {
		// 播放页画的是 a.Player 那一间。房间号为空时 key 是 "平台|"，
		// 和 focusKey 用的是同一个拼法，两边对得上。
		if a.Player.Name != "" && watchKey(a.Player) == key {
			return surfaceFull
		}
	}
	if a.onWatchWall() {
		if _, ok := a.watch.rects[key]; ok {
			return surfaceTile
		}
	}
	return surfaceNone
}

// islandForm 是灵动岛的两种形态：时钟，或者「这一路」。
//
// 以前分得更多（full / tile / listening / idle）。full 和 tile 在渲染上
// 完全一样，合并成展开态；listening 曾经缩成一颗头像圆，后来整条去掉——
// 后台听也是在听，凭什么要换个形状。现在只剩「有没有焦点」一条判据：
// 有焦点就是那一条主播信息，没有就是时钟。
type islandForm int

const (
	// islandIdle 是首页的时钟岛：没有声音焦点。
	islandIdle islandForm = iota
	// islandOpen 是有焦点的那一条：主播信息、关注键和结束键。
	islandOpen
)

func (f islandForm) String() string {
	if f == islandOpen {
		return "open"
	}
	return "idle"
}

func (a *App) islandFormOf() islandForm {
	if a.focusKey() == "" {
		return islandIdle
	}
	return islandOpen
}
