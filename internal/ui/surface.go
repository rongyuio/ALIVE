package ui

import (
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

// activityKey 是**唯一**的「这一间还有 Live Activity」判据：焦点在、而且
// 不是一条死路。
//
// dead 的定义只看状态本身，不看可选字段：没有引擎、没在取流、且已经落了
// 终态（未开播 / 取流失败）。刚点开、还没开始取流的一间不算死——那一瞬间
// 它既没有引擎也没有 loading，按「有没有引擎」判会让岛闪一下。
func (a *App) activityKey() string {
	if a.focus == nil {
		return ""
	}
	if a.focus.dead() {
		return ""
	}
	return a.focus.key
}

// videoOwner 是「这一间的画面此刻由谁画」的唯一裁决点。
//
// 一个 ui.VideoPlayer 在一帧里只能被一个 ui.Video 画出来。两个元素同时画
// 同一个播放器，实测只有一个出画，另一个是黑的——而那正是「一个直播间
// 同时出现放大后的终态和过渡态」这个报障的来源。
//
// 以前这条约束只写了一半：墙格在过渡窗口里把 player 置 nil 交给舞台，但
// 反过来舞台从不退让，飞行块也不参与裁决。于是「谁画」取决于哪个元素先建，
// 而不是取决于状态。
//
// 优先级就是画面的真实位置：
//
//	stage   播放页的舞台
//	tile    观测墙的格子（离开播放页的那段过渡里让给舞台）
//	none    没有表面在画它
type videoOwner int

const (
	videoNone videoOwner = iota
	videoStage
	videoTile
)

func (v videoOwner) String() string {
	switch v {

	case videoStage:
		return "stage"
	case videoTile:
		return "tile"
	}
	return "none"
}

// videoOwnerOf 求 key 这一间的画面此刻归谁画。
//
// 舞台那一档问的是 fullSize()，不是「路由是不是播放页」：离开播放页的那段
// 过渡里路由已经换走了，Router 仍然在建退场那一页（TransitionMatch 会同时
// 建两页），舞台还在画——这时候格子必须让位。
func (a *App) videoOwnerOf(key string) videoOwner {
	if key == "" {
		return videoNone
	}

	if a.focusKey() == key && a.fullSize() {
		return videoStage
	}
	if a.onWatchWall() && a.watchHas(key) {
		return videoTile
	}
	return videoNone
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

// wallActiveKey 是墙上那一格「正在出声」的标记者。
//
// 它就是 activityKey 在墙上的那一段：这一间是 Live Activity，而且这一间
// 就在墙上。和岛的形态同一个来源，所以「墙格亮着绿点、岛却是时钟」不会再
// 出现。
func (a *App) wallActiveKey() string {
	if key := a.activityKey(); key != "" && a.watchHas(key) {
		return key
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
	return a.Router != nil && a.Router.Path() == routePlayer
}

// inBackground 报告焦点在出声或留着，但眼下没有任何全尺寸视图。
// 它就是「后台收听」——不是一种模式，是 fullSize 的反面。
func (a *App) inBackground() bool {
	return a.focus != nil && !a.fullSize()
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
// 这两种形态照 iPhone 的 Live Activity：没有活动时岛是一条静态的胶囊
// （这里放时钟，因为桌面得有个东西占着中间那块），有活动时它就是这一路
// 的 compact 呈现。
//
// 判据只有 activityKey 一条。以前这里另有一套 focusAlive（引擎 / 取流 /
// 在播放页），和墙上的 wallActiveKey 各说各话，于是能出现「声音在响、
// 墙上有绿点、岛却是时钟」。
type islandForm int

const (
	// islandIdle 是桌面态的时钟岛：没有活动。
	islandIdle islandForm = iota
	// islandOpen 是这一路的 compact 呈现：主播信息、关注键和结束键。
	islandOpen
)

func (f islandForm) String() string {
	if f == islandOpen {
		return "open"
	}
	return "idle"
}

// islandFormOf 是岛形态的唯一求值点。
//
// 播放页上回时钟，这是照 HIG 来的：*"Don't add elements to your app that
// draw attention to the Dynamic Island. Your Live Activity appears in the
// Dynamic Island while your app isn't in use, and other items can appear in
// the Dynamic Island when your app is open."*
//
// 所以 App 打开的时候，它自己的 Live Activity 不该占着岛。退出钮与房间名
// 跟着搬进播放页自己的顶栏（见 playerTopbar），岛不再承担这两个职责。
//
// 连带消失的是「点岛收起」这条手势——iPhone 上根本没有它：收起是 App 自己
// 的返回（Esc / 左上返回钮）。
func (a *App) islandFormOf() islandForm {
	if a.Router != nil && a.Router.Path() == routePlayer {
		return islandIdle
	}
	if a.activityKey() == "" {
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
