package ui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 表面之间的过渡与焦点的生命周期。
//
// 三处"没有动画"是三件事，不能互相牵连（见 doc/architecture/surfaces.md）：
//
//   - 顶栏 tab / 分区之间：不动。方向模型与平级路由不匹配（d488013）。
//   - 内容层（卡片网格）：永不位移、永不淡入（0010）。在用户正在读的内容上
//     施加位移，一定会被读成"画面在抖"。
//   - 表面之间（墙格 ↔ 播放页）：做连续过渡。这不是"在内容上动"，是
//     "同一间房换位置"。
//
// 所以 Router.Transition 不能是一个全局开关——它得按次决定。这个文件把它
// 收在一处：调用点只说"这次是从墙格进的/出的"，什么时候开、什么时候关、
// 什么时候恢复都不散到外面。

// surfaceMatch 是共享元素的身份：同一间房在墙格与播放页上是同一个值。
//
// 用自定义类型而不是字符串，避免和别的 Match 撞身份（MyGo 文档的建议）。
type surfaceMatch struct{ key string }

// routeTransitionDur 是表面之间过渡的时长。
//
// 280ms 和 theme 的 EaseOut 一档对齐。比顶栏滑块的 220ms 长一点：
// 那是"换个选项"，这是"同一间房换个位置"，位移量级大得多。
const routeTransitionDur = 280 * time.Millisecond

// setFocus 是焦点的唯一写者。
//
// 换焦点时旧的那一间按"还有没有表面在显示它"收尾：在观测盒里 → 静音留着
// 做预览；不在盒里 → 连引擎一起关。这条规则同时覆盖墙上换间、进播放页、
// 关闭收起的直播间——不再有 watchAdoptActive / watchReleaseActive /
// watchDropActive / watchStopActive / watchDeactivate 五个搬运函数。
func (a *App) setFocus(s *roomSession) {
	old := a.focus
	if old == s {
		return
	}
	// 上一轮的取流结果不再接纳：旧结果回来时媒体代号已经过期。
	a.media.cancelSession()
	a.closeDanmaku()
	a.media.danmuRetryAt = time.Time{}
	a.resetDanmuHistory()
	// 先换焦点，再收尾旧的那间：endSessionFocus 靠「它已经不是焦点」
	// 把引擎音量落到 0，不需要另写一遍静音。
	a.focus = s
	if old != nil {
		a.endSessionFocus(old)
	}
	if s != nil {
		s.playing = true
		if s.volume <= 0 {
			// 这一间上次被静音过。点开它就是要听，恢复到静音前的值；
			// 没有记忆时给一个能听见的默认音量。
			if a.volumeBeforeMute > 0 {
				s.volume = a.volumeBeforeMute
			} else {
				s.volume = 0.6
			}
		}
		a.DefaultVolume = s.volume
		a.applyFocusAudio()
	}
	a.markDirty()
	a.invalidateWindows()
}

// endSessionFocus 结束某一间的声音焦点。
//
// 会话存亡只由一件事决定：还在观测盒里吗。在 → 静音、留在墙上做预览；
// 不在 → 关掉。这就是"取消 active"和"关闭直播间"的全部区别。
func (a *App) endSessionFocus(s *roomSession) {
	if s == nil {
		return
	}
	if a.watchHas(s.key) {
		// 只收声，画面不动：静音视频会被 MyGo 按住，看门狗会误判断流。
		a.applySessionVolume(s)
		return
	}
	a.closeSession(s)
}

// applySessionVolume 是引擎音量的唯一写者。
//
// 焦点那一间 = 它自己的音量；其余一律 0（墙格预览、后台里被让位的一路）。
// 只在值不同时推：SetVolume 每次都会让引擎立刻重渲染一帧。
// 判据用播放器当前音量而不是 pushedVolume——起播静音、墙格预览、从墙上
// 接管过来的引擎都能在这一帧对齐。
func (a *App) applySessionVolume(s *roomSession) {
	if s == nil || s.player == nil {
		return
	}
	var want float64
	if s == a.focus {
		want = float64(s.volume)
	}
	if !almostEqual(want, s.player.Volume()) {
		s.player.SetVolume(want)
	}
	if s == a.focus {
		a.pushedVolume = want
	}
}

// applyFocusAudio 把焦点的音量与播放意图推给它的引擎。
func (a *App) applyFocusAudio() {
	s := a.focus
	if s == nil || s.player == nil {
		return
	}
	a.applySessionVolume(s)
	if s.playing {
		s.player.Play()
	} else {
		s.player.Pause()
	}
}

// openRoom 是「看这一间」的唯一入口：首页卡片、关注行、搜索结果、墙格、
// 灵动岛都走这里。
//
// 进播放页的过渡由**画面归属**决定，不由「从哪一页点进来」决定：这一间在
// 当前页上有没有画面（墙格 / 卡片）、岛是不是正显示它，各自对应一种接法。
// 归属要在 setFocus 之前求——焦点一换，岛显示的就变成新的一间了。
func (a *App) openRoom(room Streamer) {
	if room.Platform == "" {
		return
	}
	key := watchKey(room)
	src := a.pictureNow(key)
	s := a.sessionFor(key, room)
	if a.focus != s {
		a.setFocus(s)
	}
	a.playerExitUntil = time.Time{}
	if a.Router == nil {
		return
	}
	if a.Router.Path() == routePlayer {
		// 已经在播放页：换台不叠历史，否则退回去还是播放页，取流路径
		// 会把刚关掉的这一间又自动开起来。同一页里换内容，没有表面过渡。
		// 同一页换台也作废还没走完的退场第二拍：那一拍属于上一间。
		a.surfaceFlight = nil
		a.exitArm = false
		a.exitAction = nil
		a.exitMatchKey = ""
		a.Router.Transition = ui.TransitionNone
		a.Router.Replace(routePlayer)
		a.invalidateWindows()
		return
	}
	// 记下退回哪一页：退出时按目标页求画面归属（见 leavePlayerTo）。
	a.playerReturnRoute, a.playerReturnSection = a.Router.Path(), a.FollowsSection
	a.enterPlayer(s, src)
}

// enterPlayer 换到播放页，按画面归属选接法。
//
//   - 墙格：舞台和格子画的是同一个播放器、同一帧画面，走 Match 连续放大；
//   - 卡片 / 岛：路由立刻切走，封面从那个矩形长成舞台，落地后淡出；
//   - 没有画面可接：硬切。
func (a *App) enterPlayer(s *roomSession, src picture) {
	a.surfaceFlight = nil
	switch src {
	case pictureTile:
		a.Router.Transition = ui.TransitionMatch
		a.routeTransitionUntil = a.now().Add(routeTransitionDur + 120*time.Millisecond)
	case pictureCard, pictureIsland:
		a.startEnterFlight(s, src)
		a.Router.Transition = ui.TransitionNone
		a.routeTransitionUntil = time.Time{}
	default:
		a.Router.Transition = ui.TransitionNone
		a.routeTransitionUntil = time.Time{}
	}
	a.Router.Push(routePlayer)
}

// returnToFocus 是点岛的「回到直播间」：不管这一间在不在盒里，目标都是它。
func (a *App) returnToFocus() {
	if a.focus == nil || a.Router == nil {
		return
	}
	if a.Router.Path() == routePlayer {
		// 在播放页点岛 = 收起（用户确认保留的手感），见 island.go。
		a.collapsePlayer()
		return
	}
	a.openRoom(a.focus.room)
}

// leaveOp 决定焦点在离开播放页之后还活不活。
type leaveOp int

const (
	// leaveCollapse 是收起 / 离开播放页：焦点留着（后台收听），岛继续显示这一路。
	leaveCollapse leaveOp = iota
	// leaveClose 是结束这一路：焦点清空，岛回时钟。
	leaveClose
)

// closeFocus 结束焦点：岛的 X、全屏返回钮、不在播放页的 Esc 都走这里。
//
// 声音立刻停；会话存亡由 endSessionFocus 的盒规则决定。在播放页上还要
// 退回上一页——目标页上如果还有这一间的画面（墙格），画面连续缩回去。
func (a *App) closeFocus() {
	s := a.focus
	if s == nil {
		return
	}
	if a.Router == nil || a.Router.Path() != routePlayer {
		// 结束这一路：没有「进岛」可言，正在飞的那块画面直接收掉。
		a.surfaceFlight = nil
		a.setFocus(nil)
		return
	}
	// 结束焦点和换路由是同一拍：焦点要先活着让画面找好落点，
	// 清早了目标页上就没有这一间可接。
	a.leavePlayerTo(leaveClose, a.playerReturnRoute, a.playerReturnSection, func() {
		a.setFocus(nil)
		a.Router.Back()
	})
}

// collapsePlayer 是 Esc / 点岛的第一档：离开播放页，这一路留着。
//
// 和 closeFocus 的区别只在焦点：这里不清焦点，引擎、取流、签了名还没过期
// 的地址、房间记号全都留着，岛继续显示这一路，点它回来接着看。
func (a *App) collapsePlayer() {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	if a.focus == nil {
		a.surfaceFlight = nil
		a.Router.Transition = ui.TransitionNone
		a.Router.Back()
		return
	}
	a.leavePlayerTo(leaveCollapse, a.playerReturnRoute, a.playerReturnSection, func() {
		a.Router.Back()
	})
}

// leavePlayerTo 在换路由之前把这一间的画面安排好，然后执行 action 换路由。
//
// 目标表面按**目标页上的画面归属**求，不再看「从哪一页进来的」：
//
//	墙格   在盒里、目标页是观测墙 → Match（两边画同一个播放器）
//	卡片   目标页的网格里还看得见这一间的卡片 → 画面缩回卡片
//	岛     收起且窗口模式 → 画面缩进岛
//	没有   结束、全屏、或目标页上没有它的画面 → 硬切
//
// 全屏下岛不画，也不做飞行；墙格仍然可以 Match——格子回到窗口里就是它的落点。
func (a *App) leavePlayerTo(op leaveOp, destRoute string, destSection int, action func()) {
	s := a.focus
	if s == nil || a.Router == nil || a.Router.Path() != routePlayer {
		action()
		return
	}
	key := s.key
	focusAlive := op == leaveCollapse
	fullscreen := a.WinFull || a.winFullscreen
	target := a.pictureOn(key, destRoute, destSection, focusAlive)
	// 结束这一路时卡片不是落点：画面已经随会话一起结束，飞回卡片是
	// 在为一个不存在的画面演收尾（surfaces.md 的操作表：X 只有墙格 > 硬切）。
	// 收起（leaveCollapse）保留卡片落点。
	if op == leaveClose && target == pictureCard {
		target = pictureNone
	}
	if fullscreen && target != pictureTile {
		target = pictureNone
	}

	a.surfaceFlight = nil
	a.routeTransitionUntil = time.Time{}
	switch target {
	case pictureTile:
		// 舞台和格子画的是同一个播放器：Match 让它连续缩回去。舞台这一帧
		// 身上可能没有身份（刚改过窗口尺寸），先补一拍，两帧后再换路由。
		if a.exitArm {
			return
		}
		a.exitArm = true
		a.exitArmAt = a.buildSeq
		a.exitMatchKey = key
		a.exitAction = action
		a.invalidateWindows()
	case pictureCard:
		a.startLeaveFlight(s, pictureCard, a.cardRectFor(key, destRoute, destSection),
			pageKey(a.Platform, destRoute, destSection))
		a.playerExitUntil = a.now().Add(surfaceFlightDur + 120*time.Millisecond)
		a.Router.Transition = ui.TransitionNone
		action()
	case pictureIsland:
		a.startLeaveFlight(s, pictureIsland, ui.Rect{}, "")
		a.playerExitUntil = a.now().Add(surfaceFlightDur + 120*time.Millisecond)
		a.Router.Transition = ui.TransitionNone
		action()
	default:
		// 没有画面可接：引擎立刻回后台，别为一块不存在的画面留全尺寸窗口。
		a.playerExitUntil = time.Time{}
		a.Router.Transition = ui.TransitionNone
		action()
	}
}

// tickExitArm 是「飞回墙格」的第二拍：上一帧的舞台已经带上身份，这一帧
// 换路由，退回去那一页里的同一格从舞台的矩形收回去。
//
// 每帧在 Shell 开头、构建之前调用。
func (a *App) tickExitArm() {
	// 等两趟：第一趟只是把身份挂上（Transition 新建的那一帧不参与匹配），
	// 第二趟才把它记进「上一帧在哪」，第三趟换路由才有起点。
	if !a.exitArm || a.buildSeq < a.exitArmAt+2 {
		return
	}
	a.exitArm = false
	// 迟到的第二拍：arm 之后又发生了别的导航（切平台、换页、关焦点）。
	// 这一拍已经不属于播放页，丢弃——旧 finishLeave 就是靠这条守卫避免
	// 在别的页面上再 Back 一次。
	if a.Router == nil || a.Router.Path() != routePlayer {
		a.exitMatchKey = ""
		a.exitAction = nil
		return
	}
	// exitMatchKey 要留到退场演完：closeFocus 的 action 会清掉焦点，
	// 这一帧的舞台只能靠它拿到 Match 身份，否则整页会退化成透明度淡出。
	// 清空在 tickRouteTransition 的 playerExitUntil 到点处。
	a.Router.Transition = ui.TransitionMatch
	a.playerExitUntil = a.now().Add(routeTransitionDur + 120*time.Millisecond)
	a.routeTransitionUntil = a.playerExitUntil
	if act := a.exitAction; act != nil {
		a.exitAction = nil
		act()
	}
}

// stageMatchKey 是播放舞台这一刻该带的 Match 身份：焦点会话的 key；
// 焦点已经清掉、但还在等退场的那两拍里，用 exitMatchKey 顶上。
func (a *App) stageMatchKey() string {
	if key := a.focusKey(); key != "" {
		return key
	}
	return a.exitMatchKey
}

// pushPage 换到另一页（顶栏 tab、切平台）。如果正从播放页离开，先把这一间
// 的画面安排好；不在播放页时就是普通换页，不带页面过渡。
func (a *App) pushPage(target string) {
	if a.Router == nil {
		return
	}
	// 不在播放页：这是一次普通换页。Match 只属于「墙格 ↔ 播放页」那一次
	// 转场；上一个退场窗口还没过时（tickExitArm 留了 400ms），不能把
	// Match 带进顶栏 tab 的换页，否则整页内容做交叉淡化（0010 禁止）。
	// 还在飞的那块画面也属于上一页的落点，换页即作废。
	if a.Router.Path() != routePlayer {
		a.Router.Transition = ui.TransitionNone
		a.routeTransitionUntil = time.Time{}
		a.surfaceFlight = nil
		a.Router.Push(target)
		return
	}
	a.leavePlayerTo(leaveCollapse, target, a.FollowsSection, func() {
		a.Router.Push(target)
	})
}

// tickRouteTransition 在转场结束后把路由器恢复成「不播页面过渡」，并收掉
// 离开播放页时那段「仍按全尺寸画」的窗口。
//
// 每帧调用，幂等。恢复得晚一点（+120ms）而不是卡着 280ms：Router 自己的
// 淡出还在收尾，提前切回 None 会把旧页面直接抽走，看起来是闪一下。
func (a *App) tickRouteTransition(now time.Time) {
	if !a.routeTransitionUntil.IsZero() && !now.Before(a.routeTransitionUntil) {
		a.routeTransitionUntil = time.Time{}
		if a.Router != nil {
			a.Router.Transition = ui.TransitionNone
		}
	}
	if !a.playerExitUntil.IsZero() && !now.Before(a.playerExitUntil) {
		a.playerExitUntil = time.Time{}
		// 退场演完，舞台不再需要刚才那一间的 Match 身份。
		a.exitMatchKey = ""
	}
}

// tickSurface 是每帧一次的表面观察者：全尺寸视图出现或消失时，做进入 /
// 离开播放页的收尾（断弹幕、拨看门狗基准、重求呈现尺寸）。
//
// 它不存真相：真相是 fullSize()，这里只是沿它的边沿做一次性动作。
func (a *App) tickSurface() {
	full := a.fullSize()
	if full == a.wasFullSize {
		return
	}
	a.wasFullSize = full
	if full {
		a.foregroundEnter()
		return
	}
	a.backgroundEnter()
}

// foregroundEnter 从后台收听回到播放页：接上现有这一路，只恢复全尺寸呈现。
//
// 后台期间解码、demuxer、音频输出、地址续期全都活着，回来不需要取流、
// 不需要重新选轨，也没有 refresh seek。引擎把渲染目标恢复成视频原始尺寸，
// 并立刻用当前帧重渲染一次（实测 4ms，音频停走 21ms）。
func (a *App) foregroundEnter() {
	m := &a.media
	m.detachedAt = time.Time{}
	// 后台那段时间没人看画面，看门狗的基准还停在离开前；拨到现在，回来不会
	// 立刻把「画面没动」判成卡死。
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	a.applyPresent()
}

// backgroundEnter 进入后台收听：全尺寸呈现关掉，解码与声音留着。
//
// 和 closeFocus 的区别是「关到哪一层」：这里焦点还在，mpv 实例、地址续期、
// 断流重连全部保留。解码不停——回来时画面只差一帧，声音一秒不断。
func (a *App) backgroundEnter() {
	m := &a.media
	// 离开播放页时，一个没有引擎、也没在取流的焦点就是死的：没有声音，
	// 也没有任何表面在画它。收掉它，岛才不会挂在一条已经不存在的路上。
	if f := a.focus; f != nil && f.player == nil && !f.loading &&
		(f.offline || f.err != "" || (f.stream.RoomID != "" && !f.stream.Live)) {
		a.setFocus(nil)
		return
	}
	m.detachedAt = time.Now()
	// 弹幕只属于全尺寸视图：离开就断，记录也清空。再进来（无论从墙还是从
	// 列表）都是一条全新连接。
	a.closeDanmaku()
	m.danmuRetryAt = time.Time{}
	a.resetDanmuHistory()
	// 后台只剩小帧、没人看，缓冲胶囊和停帧看门狗的判据失去意义。
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	a.applyPresent()
}

// matchable 报告此刻能不能给元素挂 Match。
//
// 刚改过窗口尺寸的一小段时间里不能：带 Match 的元素在 resize 时会做尺寸
// 动画，整墙的格子会一帧帧弹过去、视频每帧重新适配（用户报的「最大化会闪」）。
func (a *App) matchable() bool {
	return time.Since(a.resizedAt) > matchSettleAfter
}

// matchSettleAfter 是窗口改尺寸之后多久才重新挂 Match。
//
// 比一次布局重排宽裕得多，又比用户点下一格快：实测 resize 的尺寸动画持续
// 十几帧（约 200ms）。
const matchSettleAfter = 400 * time.Millisecond

// noteWindowSize 记下窗口尺寸变化，供 matchable 判断。每帧调用。
func (a *App) noteWindowSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if a.lastWindowW != w || a.lastWindowH != h {
		a.lastWindowW, a.lastWindowH = w, h
		a.resizedAt = time.Now()
	}
}
