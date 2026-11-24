package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 表面过渡：一块画面在两个表面之间飞。
//
// 只有三种落点需要飞（见 surface.go 的 picture）：
//
//	卡片 → 播放页   封面从卡片长成舞台，落地后淡出露出画面
//	播放页 → 卡片   画面从舞台缩回卡片
//	播放页 → 岛     画面从舞台缩进岛（收起 / 离开播放页去别处）
//
// 墙格和播放页之间不在这里：两边画的是同一个播放器、同一帧画面，走 MyGo 的
// Match（见 watch.go / player.go）。其余组合（没有画面可接）硬切。
//
// 为什么不用 Router 的交叉淡化：它把两页同时按透明度混在一起。播放页是一整块
// 不透明的画面，列表页是浅色内容，中间那几帧就是一层灰雾——视频从卡片后面
// 透出来，控制条的深色长条浮在卡片上（2026-11-24 实测）。所以路由立刻切走，
// 目标页从第一帧起就是清楚的，只有这一块画面在飞。
//
// 飞行画在岛之前（Shell 里先 surfaceFlightView 再 Island）：落点在岛的位置，
// 岛盖住它的最后一段。

const (
	// surfaceFlightDur 和表面之间的过渡同一档。再长，人已经在操作目标页了。
	surfaceFlightDur = 280 * time.Millisecond
	// surfaceFlightEndW/H 是岛这个落点的大小：比岛（IslandW 100 × IslandH 38）
	// 小一圈，保证整块缩进岛后面，岛沿不会露出一个角。
	surfaceFlightEndW float32 = 80
	surfaceFlightEndH float32 = 30
)

// flightKind 是飞行的方向：决定内容画什么、要不要给目标页压暗。
type flightKind int

const (
	// flightEnter 是进播放页：舞台已经在下面，飞的是封面，落地后淡出。
	// 不能画视频——同一个 VideoPlayer 同时被舞台和这块画，只有一块出画。
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
	// page 是落点所属的那一页（pageKey）。飞行途中换了页，这块画面就属于
	// 上一页的落点，立刻作废；岛是全局的，page 为空表示不校验。
	page string

	started bool
	start   time.Time
}

// startEnterFlight 安排「进播放页」的那一块画面。src 必须是 pictureCard 或
// pictureIsland；墙格走 Match，没有画面可接就不飞。
func (a *App) startEnterFlight(s *roomSession, src picture) {
	if s == nil || (src != pictureCard && src != pictureIsland) {
		return
	}
	f := &surfaceFlight{
		kind: flightEnter, key: s.key, room: s.room,
		from: src, to: picturePlayer,
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

// startLeaveFlight 安排「离开播放页」的那一块画面。to 必须是 pictureCard 或
// pictureIsland；墙格走 Match，结束且没有画面可接就不飞。page 是落点所属
// 的那一页；岛传空（全局落点，不随换页作废）。
func (a *App) startLeaveFlight(s *roomSession, to picture, toRect ui.Rect, page string) {
	if s == nil || (to != pictureCard && to != pictureIsland) {
		return
	}
	a.surfaceFlight = &surfaceFlight{
		kind: flightLeave, key: s.key, room: s.room,
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

// islandFlightRect 是岛这个落点：居中、藏在岛后面。
func (a *App) islandFlightRect(c *ui.Context) ui.Rect {
	winW, _ := c.Size()
	return ui.Rect{
		X: (winW - surfaceFlightEndW) / 2,
		Y: islandTop + (theme.IslandH-surfaceFlightEndH)/2,
		W: surfaceFlightEndW, H: surfaceFlightEndH,
	}
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
			if f.from == pictureIsland {
				f.fromRect = a.islandFlightRect(c)
			} else {
				f.fromRect = a.stageRect(c)
			}
		}
		if f.toRect.W <= 0 || f.toRect.H <= 0 {
			switch f.to {
			case picturePlayer:
				f.toRect = a.stageRect(c)
			case pictureIsland:
				f.toRect = a.islandFlightRect(c)
			}
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
	// 注意力收在封面上，落地时罩子和封面一起退，画面接上。
	if f.kind == flightEnter {
		stage := f.toRect
		ui.Box(c).Absolute().Left(stage.X).Top(stage.Y).Width(stage.W).Height(stage.H).
			Background(theme.MediaFlightScrim.Alpha(1 - progress)).IgnoreInput(true)
	}

	x := f.fromRect.X + (f.toRect.X-f.fromRect.X)*e
	y := f.fromRect.Y + (f.toRect.Y-f.fromRect.Y)*e
	w := f.fromRect.W + (f.toRect.W-f.fromRect.W)*e
	h := f.fromRect.H + (f.toRect.H-f.fromRect.H)*e

	box := ui.Box(c).Absolute().Left(x).Top(y).Width(w).Height(h).
		Radius(theme.RadiusXL).Clip().Background(theme.IslandInk).
		IgnoreInput(true).Label("表面过渡中的画面")
	// 落点藏在岛 / 卡片后面了，但边缘还会漏出一点：最后一段淡掉，
	// 收尾不是「啪」地消失。进入时还要露出下面已经就位的画面。
	if progress > 0.75 {
		box.Opacity((1 - progress) / 0.25)
	}
	box.Children(func() {
		if f.kind == flightEnter {
			// 只画封面：舞台下面的播放器是同一个引擎，这里再画一次就只剩一块出画。
			if bmp := media.Default.Bitmap(f.room.CoverURL, f.fromRect.W); bmp != nil {
				ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
			} else {
				coverArt(c, w, h)
			}
			return
		}
		// 离开：和舞台同一套。底图铺满黑边，画面 contain，首帧没到用封面顶住。
		player := a.surfaceFlightPlayer(f.key)
		ready := player != nil && player.Ready()
		bgURL := f.room.BackdropURL
		if bgURL == "" {
			bgURL = f.room.CoverURL
		}
		if bmp := media.Default.Bitmap(bgURL, f.fromRect.W); bmp != nil {
			ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Background(theme.MediaBackdropScrim)
		}
		if player != nil {
			ui.Video(c, player).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
			if ready {
				return
			}
		}
		if bmp := media.Default.Bitmap(f.room.CoverURL, f.fromRect.W); bmp != nil {
			ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
		}
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(theme.MediaScrimSoft)
	})
	// 岛自己的流光每帧都在请求帧，但那不是这条动画的前提：reduced motion 下
	// 流光停了，画面还得走完。
	// 飞行期间每帧都要出：走 MyGo 的动画帧，按显示器刷新率走，而不是
	// 自己钉一个 16ms 定时器（120/144Hz 上会明显更顿，也绕开帧合并）。
	c.AnimationFrame()
}
