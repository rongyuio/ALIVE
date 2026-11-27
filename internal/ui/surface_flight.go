package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 表面过渡：一块画面在两个表面之间飞。
//
// 只剩两种落点走这里（见 surface.go 的 picture）：
//
//	卡片 → 播放页   封面从卡片长成舞台，落地后淡出露出画面
//	播放页 → 卡片   画面从舞台缩回卡片
//
// 墙格和岛都**不在这里**：它们和播放页共用一块真画面，走 MyGo 的元素级
// Match + Fly（`ui.ElementTransition{Match, Fly}`，见 transitions.md 的
// Shared elements）——两端各挂一次身份，交叉淡化、可打断、reduced motion
// 都由框架负责，不在这里手搓第二份画面。
//
// 卡片为什么留着：卡片上是封面，舞台上是视频，两端的"内容"根本不同一种，
// 共享身份做不出「封面长成画面」，而 Fly 只能搬元素、搬不了内容。这一块
// 飞行块画的正是那件事本身（有活画面用活画面，否则封面）。
//
// 进播放页的飞行**恒安排**（只要这一间在来源页上有几何）。这一间此刻有没有
// 在出图只决定飞行块里画什么。2026-11-25 那次报障（「点岛放大直播间，过程中
// 显示封面然后变成直播画面」）的根因是飞的是封面，不是"飞"——按"有没有引擎"
// 分叉成"飞 / 硬切"会让同一个操作时有时无。岛那一路现在改走元素 Match 之后，
// 这条分叉在岛上也消失了。
//
// 为什么不用 Router 的交叉淡化：它把两页同时按透明度混在一起。播放页是一整块
// 不透明的画面，列表页是浅色内容，中间那几帧就是一层灰雾——视频从卡片后面
// 透出来，控制条的深色长条浮在卡片上（2026-11-24 实测）。所以路由立刻切走，
// 目标页从第一帧起就是清楚的，只有这一块画面在飞。

const (
	// surfaceFlightDur 和表面之间的过渡同一档。再长，人已经在操作目标页了。
	surfaceFlightDur = theme.MotionSurface
)

// flightKind 是飞行的方向：决定内容画什么、要不要给目标页压暗。
type flightKind int

const (
	// flightEnter 是进播放页：舞台已经在下面，飞的是这一间最好的像素，
	// 落地后淡出。飞不飞与"有没有画面"无关，见 surface_plan.go。
	flightEnter flightKind = iota
	// flightLeave 是离开播放页：路由已经切走，飞的是画面本身。
	flightLeave
)

// surfaceFlight 是一次表面过渡的进行态。
type surfaceFlight struct {
	kind flightKind
	key  string
	room Streamer

	// from / to 是两端的表面；矩形在第一帧求一次就记住——画面已经从那里
	// 出发了，中途改窗口也不重算起点。
	from, to         picture
	fromRect, toRect ui.Rect
	// pixels 是飞行块里画什么：有活画面就用活画面，没有就用封面。
	// 它由 surface_plan.go 的 planTransition 决定，不由"从哪进来"决定。
	pixels transitionPixels
	// page 是落点所属的那一页（pageKey）。飞行途中换了页，这块画面就属于
	// 上一页的落点，立刻作废；岛是全局的，page 为空表示不校验。
	page string

	started bool
	start   time.Time
}

// startEnterFlight 安排「进播放页」的那一块画面。src 必须是 pictureCard；
// 墙格和岛都走元素级的 Match + Fly，没有画面可接就不飞。
func (a *App) startEnterFlight(s *roomSession, src picture, pixels transitionPixels) {
	if s == nil || src != pictureCard {
		return
	}
	f := &surfaceFlight{
		kind: flightEnter, key: s.key, room: s.room,
		from: src, to: picturePlayer, pixels: pixels,
		page: pageKey(a.Platform, routePlayer, a.FollowsSection),
	}
	if src == pictureCard {
		f.fromRect = a.cardRectFor(s.key, a.Router.Path(), a.FollowsSection)
		if f.fromRect.W <= 0 || f.fromRect.H <= 0 {
			return
		}
	}
	a.surfaceFlight = f
}

// startLeaveFlight 安排「离开播放页」的那一块画面。to 必须是 pictureCard；
// 墙格和岛都走元素级的 Match + Fly。page 是落点所属的那一页。
func (a *App) startLeaveFlight(s *roomSession, to picture, toRect ui.Rect, page string, pixels transitionPixels) {
	if s == nil || to != pictureCard {
		return
	}
	a.surfaceFlight = &surfaceFlight{
		kind: flightLeave, key: s.key, room: s.room, pixels: pixels,
		from: picturePlayer, fromRect: a.playerStageBounds,
		to: to, toRect: toRect, page: page,
	}
}

// surfaceFlightPlayer 取这一间此刻的引擎。
//
// 不能把 *ui.VideoPlayer 存进 surfaceFlight：收起之后后台收听可能因为
// 「没有引擎又没在取流」被收掉，会话和引擎一起销毁；存下来的指针就成了
// 悬空的。每帧按 key 现查，会话没了就退到封面。
func (a *App) surfaceFlightPlayer(key string) *ui.VideoPlayer {
	if s := a.watch.sessions[key]; s != nil {
		return s.player
	}
	return nil
}

// stageRect 是播放页舞台在窗口里的矩形。
//
// 和 PlayerPage 的布局同一套：正文区减掉左右 padding，再减掉右侧弹幕面板。
// 舞台会被面板挤窄，按正文区算会在起飞那一帧先跳大一圈。
func (a *App) stageRect(c *ui.Context) ui.Rect {
	winW, winH := c.Size()
	pad := a.bodyPadX(c)
	var panel float32
	if a.DanmuPanelOn && !a.winFullscreen {
		panel = a.panelWidth()
	}
	return ui.Rect{
		X: pad, Y: theme.NavbarHeight,
		W: winW - 2*pad - panel, H: winH - theme.NavbarHeight,
	}
}

// surfaceFlightView 画那一块正在飞的画面。没有飞行时什么都不画。
func (a *App) surfaceFlightView(c *ui.Context) {
	f := a.surfaceFlight
	if f == nil {
		return
	}
	// 飞行途中换了页：这块画面属于上一页的落点，作废。岛是全局落点
	// （page 为空），换页不影响它。
	if f.page != "" && a.Router != nil &&
		pageKey(a.Platform, a.Router.Path(), a.FollowsSection) != f.page {
		a.surfaceFlight = nil
		return
	}
	// reduced motion：不飞。路由已经切走了，这就是硬切。
	if c.Preferences().ReduceMotion {
		a.surfaceFlight = nil
		return
	}
	if !f.started {
		f.started = true
		f.start = c.Now()
		if f.fromRect.W <= 0 || f.fromRect.H <= 0 {
			f.fromRect = a.stageRect(c)
		}
		if f.toRect.W <= 0 || f.toRect.H <= 0 {
			f.toRect = a.stageRect(c)
		}
		if f.fromRect.W <= 0 || f.toRect.W <= 0 {
			a.surfaceFlight = nil
			return
		}
	}
	progress := float32(c.Now().Sub(f.start)) / float32(surfaceFlightDur)
	if progress >= 1 {
		a.surfaceFlight = nil
		return
	}
	if progress < 0 {
		progress = 0
	}
	e := easeOutUnit(progress)

	// 进播放页时目标页第一帧就已经在下面了：压一层随飞行退去的暗罩，
	// 注意力收在封面上，落地时罩子和封面一起退，画面接上。飞的是活画面
	// 时不压罩：那层暗罩会让"画面在动"变成"画面被蒙住"。
	if f.kind == flightEnter && f.pixels != pixelsLive {
		stage := f.toRect
		ui.Box(c).Absolute().Left(stage.X).Top(stage.Y).Width(stage.W).Height(stage.H).
			Background(theme.MediaFlightScrim.Alpha(1 - progress)).IgnoreInput(true)
	}

	x := f.fromRect.X + (f.toRect.X-f.fromRect.X)*e
	y := f.fromRect.Y + (f.toRect.Y-f.fromRect.Y)*e
	w := f.fromRect.W + (f.toRect.W-f.fromRect.W)*e
	h := f.fromRect.H + (f.toRect.H-f.fromRect.H)*e

	// 底色必须是舞台那一种黑，不能用岛的墨色：飞行块从舞台矩形起飞，
	// 起飞第一帧要和舞台最后一帧逐像素相同（黑边区尤其明显）。底图本身
	// 是半透明的模糊层，底下的 1 级色差会透过来。
	//
	// 判据在 TestCardFlightGrowsToStageAndEnds：它拿起飞帧和舞台帧的同一
	// 个像素比。换底色之前先跑它。
	box := ui.Box(c).Absolute().Left(x).Top(y).Width(w).Height(h).
		Radius(theme.RadiusXL).Clip().Background(theme.Black).
		IgnoreInput(true).Label("表面过渡中的画面")
	// 落点藏在岛 / 卡片后面了，但边缘还会漏出一点：最后一段淡掉，
	// 收尾不是「啪」地消失。进入时还要露出下面已经就位的画面。
	if progress > 0.75 {
		box.Opacity((1 - progress) / 0.25)
	}
	box.Children(func() {
		// 飞行块画的是**这一间此刻最好的像素**：有活画面就画活画面，
		// 没有才退到封面。进和出用同一套画法——"有没有画面"只决定画什么，
		// 不决定飞不飞。
		player := a.surfaceFlightPlayer(f.key)
		ready := player != nil && player.Ready()
		if f.pixels == pixelsLive && player != nil {
			if bmp := a.backdropFor(c, f.room); bmp != nil {
				backdropLayers(c, bmp)
			}
			ui.Video(c, player).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
			if ready {
				return
			}
			// 引擎还没出首帧：先用封面顶住，并在上面压一层软罩。
			if bmp := media.Default.Bitmap(f.room.CoverURL, f.fromRect.W); bmp != nil {
				ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
			}
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Background(theme.MediaScrimSoft)
			return
		}
		if bmp := media.Default.Bitmap(f.room.CoverURL, f.fromRect.W); bmp != nil {
			ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
		} else {
			coverArt(c, w, h)
		}
	})
	// 岛自己的流光每帧都在请求帧，但那不是这条动画的前提：reduced motion 下
	// 流光停了，画面还得走完。
	// 飞行期间每帧都要出：走 MyGo 的动画帧，按显示器刷新率走，而不是
	// 自己钉一个 16ms 定时器（120/144Hz 上会明显更顿，也绕开帧合并）。
	c.AnimationFrame()
}
