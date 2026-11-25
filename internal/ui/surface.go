package ui

import (
	"fmt"
	"time"
)

// 表面模型：一个会话集合 + 一个焦点 + 三个派生量。
//
//	会话集合  a.watch.sessions   每个直播间至多一个 roomSession
//	焦点      a.focus            用户此刻「在」的那一间，至多一个或为空
//
// 引擎、流、音量、播放态都住在会话里，不管是墙上、播放页还是后台收听。
// 播放页画的是焦点，岛上显示的是焦点，出声的也是焦点；墙上的 active、
// 后台收听、呈现尺寸、岛形态全部从「焦点是谁 + 焦点此刻被画在哪」推导。
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

// picture 是一间房的**画面归属**：这一刻，它的画面住在哪个表面上。
//
// 它不是呈现尺寸（surface）：surface 回答"这一间被画成多大"，picture 回答
// "换表面时画面从哪来、到哪去"。过渡只由它决定，不由"从哪一页进来"决定——
// 后者是代理，换个台就过期了。
//
// 优先级就是过渡的优先级：播放页 > 墙格 > 列表卡片 > 岛 > 没有。
// 前三个画的是这一间自己的画面；岛不是画面，是声音的家，只有画面不在
// 屏幕上时才落到它。
type picture int

const (
	pictureNone picture = iota
	picturePlayer
	pictureTile
	pictureCard
	pictureIsland
)

func (p picture) String() string {
	switch p {
	case picturePlayer:
		return "player"
	case pictureTile:
		return "tile"
	case pictureCard:
		return "card"
	case pictureIsland:
		return "island"
	}
	return "none"
}

// pageKey 是一页的身份：路由 + 关注页分区。卡片的矩形只对记下它的那一页
// 有效，换页 / 换分区之后不能再拿来当落点。
func pageKey(platform, route string, section int) string {
	if route == routeFollows {
		return fmt.Sprintf("%s|follows|%d", platform, section)
	}
	return platform + "|" + route
}

// pictureOn 求 key 这一间在「route + 分区」这一页上的画面归属。
//
// keepsFocus 表示这一下之后焦点还活着（收起 / 后台收听），只有这时岛才是
// 一个落点：结束这一路时岛会变回时钟，画面飞进去就成了谎话。
//
// 卡片只在记下过它矩形的那一页上算数：列表是虚拟化的，滚出屏幕的卡片
// 不在树上，拿旧矩形当落点会飞到一片空白里。
func (a *App) pictureOn(key, route string, section int, keepsFocus bool) picture {
	if key == "" {
		return pictureNone
	}
	if route == routePlayer {
		if a.focusKey() == key {
			return picturePlayer
		}
		return pictureNone
	}
	if route == routeFollows && section == followsSectionWatch && a.watchHas(key) {
		return pictureTile
	}
	if r := a.cardRectFor(key, route, section); r.W > 0 && r.H > 0 {
		return pictureCard
	}
	// 岛是声音的家：只有这一间离开播放页之后还会出声（引擎在、或者正在来），
	// 才是一个落点。没有引擎又没有取流的焦点，离开就会被收掉，岛变回时钟——
	// 画面飞进去就成了谎话。
	if keepsFocus && a.focusKey() == key && a.focus != nil &&
		(a.focus.player != nil || a.focus.loading) {
		return pictureIsland
	}
	return pictureNone
}

// pictureNow 求 key 这一间在**当前**这一页上的画面归属。进播放页时用它
// 找画面的起点；焦点必须是换之前的那一个，所以调用方要在 setFocus 之前问。
func (a *App) pictureNow(key string) picture {
	if a.Router == nil {
		return pictureNone
	}
	return a.pictureOn(key, a.Router.Path(), a.FollowsSection, a.focusKey() == key)
}

// pictureFromIsland 求「点岛进播放页」的画面起点。
//
// 和 pictureNow 只差一条：不认卡片。用户点的是屏幕正中的岛，画面却从同一页
// 上某张卡片长出来，读起来就是「不知道从哪冒出来一块封面」。墙格仍然优先——
// 在观测墙上，这一间的画面确实在那一格里。
func (a *App) pictureFromIsland(key string) picture {
	if key == "" {
		return pictureNone
	}
	if a.onWatchWall() && a.watchHas(key) {
		return pictureTile
	}
	if a.focusKey() == key && a.focus != nil &&
		(a.focus.player != nil || a.focus.loading) {
		return pictureIsland
	}
	return pictureNone
}

// readyPicture 报告这一间此刻是不是已经在出画面。
//
// 有画面的房间不该再飞一张封面过去：飞行块会把活画面盖住，落地再从封面切回
// 直播，是一次假的闪。只有墙格走 Match（两边画同一个播放器）。
func (a *App) readyPicture(s *roomSession) bool {
	return s != nil && s.player != nil && s.player.Ready()
}

// now 是这一帧的视图时钟。生产里就是系统时钟，Tester 里是它自己的时钟。
// 过渡的截止时刻和 Router 的进度必须用同一个时钟（见 App.frameNow）。
func (a *App) now() time.Time {
	if a.frameNow.IsZero() {
		return time.Now()
	}
	return a.frameNow
}

// focusKey 是当前焦点那一间的 key；空串表示没有焦点。
//
// 它取代了以前三处平行的焦点来源（a.WatchActive / a.media.roomKey() /
// a.Player）。焦点只有一个写者：setFocus。
func (a *App) focusKey() string {
	if a.focus == nil {
		return ""
	}
	return a.focus.key
}

// wallActiveKey 是墙上那一格「正在出声」的标记者：焦点这一间在墙上时，
// 它的格子就是 active。
//
// 以前这是一个字段（a.WatchActive），要由墙上激活、watchShow、媒体层
// 三条路各自维护，漏一条就是「声音在响但没有一格有绿点」。现在它只是
// 「焦点在不在墙里」的推论。
func (a *App) wallActiveKey() string {
	if a.focus != nil && a.watchHas(a.focus.key) {
		return a.focus.key
	}
	return ""
}

// fullSize 报告焦点此刻是不是被画成全尺寸。
//
// 播放页上是；离开播放页的那段过渡里也是——引擎要留到演完才换尺寸，
// 否则飞出去的舞台会在半路变糊（见 surface_motion.go）。
func (a *App) fullSize() bool {
	if a.focus == nil {
		return false
	}
	if a.Router != nil && a.Router.Path() == routePlayer {
		return true
	}
	return !a.playerExitUntil.IsZero() && a.now().Before(a.playerExitUntil)
}

// inBackground 报告焦点在出声或留着，但眼下没有任何全尺寸视图。
// 它就是「后台收听」——不是一种模式，是 fullSize 的反面。
func (a *App) inBackground() bool {
	return a.focus != nil && !a.fullSize()
}

// surfaceFor 求某个会话此刻的可见表面。
//
// 三个判据，按优先级：播放页全尺寸 > 观测墙格子 > 无视图。
// 后台收听时焦点仍然有引擎，所以"有播放器"不等于"有视图"。
func (a *App) surfaceFor(key string) surface {
	if key == "" {
		return surfaceNone
	}
	if a.focus != nil && a.focus.key == key && a.fullSize() {
		return surfaceFull
	}
	if a.onWatchWall() {
		if _, ok := a.watch.rects[key]; ok {
			return surfaceTile
		}
	}
	return surfaceNone
}

// 焦点会话的音量与播放态。
//
// 它们住在 roomSession 里，App 上不再各存一份。以前「音量既是全局字段又是
// 每格的 map」「播放态既是 App 字段又是 player.Playing()」——两份真相互相追，
// 是切台后声音不对、暂停态串台的来源。

// focusVolume 是焦点这一间自己的音量；没有焦点时返回新房间的默认值。
func (a *App) focusVolume() float32 {
	if a.focus == nil {
		return a.DefaultVolume
	}
	return a.focus.volume
}

// focusPlaying 是焦点的播放意图；没有焦点时没有"在播"这回事。
func (a *App) focusPlaying() bool {
	return a.focus != nil && a.focus.playing
}

// islandForm 是灵动岛的两种形态：时钟，或者「这一路」。
//
// 以前分得更多（full / tile / listening / idle）。full 和 tile 在渲染上
// 完全一样，合并成展开态；listening 曾经缩成一颗头像圆，后来整条去掉——
// 后台听也是在听，凭什么要换个形状。现在只剩「有没有焦点」一条判据：
// 有焦点就是那一条主播信息，没有就是时钟。
type islandForm int

const (
	// islandIdle 是首页的时钟岛：没有焦点。
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

// focusAlive 报告焦点这一间还有没有"在播"的东西：引擎、正在来的引擎，
// 或者一个正在看的播放页。没有声音也没有画面的焦点不该占着岛——岛是
// 「正在直播/收起的直播间」，不是一块还没关掉的记录。
func (a *App) focusAlive() bool {
	if a.focus == nil {
		return false
	}
	if a.focus.player != nil || a.focus.loading {
		return true
	}
	return a.Router != nil && a.Router.Path() == routePlayer
}

func (a *App) islandFormOf() islandForm {
	if !a.focusAlive() {
		return islandIdle
	}
	return islandOpen
}

// setFocusPlaying 改焦点的播放意图，并立刻落到引擎上。
func (a *App) setFocusPlaying(on bool) {
	if a.focus == nil {
		return
	}
	a.focus.playing = on
	a.applyFocusAudio()
	a.markDirty()
	a.invalidateWindows()
}
