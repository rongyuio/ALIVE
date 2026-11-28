package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/diag"
)

// 导航与焦点的生命周期。
//
// 播放页和它周围的表面（墙格 / 卡片 / 岛）之间**不做过渡**，一律硬切。
// 以前这里有一套按「源点是哪一块」分派的过渡（元素 Match / 整页摆位 /
// 飞行块），每一种都要在两帧之间对齐几何，需要一个跨帧状态机；三套并存时
// 它们互相踩，「从墙上放大」「画面 → 模糊 → 画面」「岛和墙一起飞」都是
// 这么来的。2026-11-28 全部拆掉，换成切。
//
// 顶栏 tab 之间、内容层（卡片网格）也从来不动：位移落在用户正在读的内容上
// 会被读成"画面在抖"（0010）。

// focusIntent 是「为什么把这一间设成焦点」——它决定听不听、要不要出声。
//
// iPhone 上这两件事本来就分开：点 App 图标打开它是"要听"，从灵动岛的
// Live Activity 回到一个你自己按过暂停的 App 是"接着刚才那样"。以前
// setFocus 只有一个入口，于是从岛点回去会自己开播、把静音的房间抬到
// 可听音量——用户特意按下的暂停被系统推翻了。
type focusIntent int

const (
	// intentListen 是"点开它就是要听"：卡片、关注行、墙格「在墙上听」。
	intentListen focusIntent = iota
)

// setFocus 是焦点的唯一写者。
//
// 换焦点时旧的那一间按"还有没有表面在显示它"收尾：在观测墙里 → 静音留着
// 做预览；不在墙上 → 连引擎一起关。这条规则同时覆盖墙上换间、进播放页、
// 关闭收起的直播间——不再有 watchAdoptActive / watchReleaseActive /
// watchDropActive / watchStopActive / watchDeactivate 五个搬运函数。
func (a *App) setFocus(s *roomSession, intent focusIntent) {
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
	from, to := "", ""
	if old != nil {
		from = old.key
	}
	if s != nil {
		to = s.key
	}
	// 焦点是「谁在出声」。它变了却没有日志，所有「点一下就黑了/没声了」
	// 都只能从别的行的缝里猜。
	diag.Ref{Sub: "focus"}.Logger().Info().
		Str(diag.Event, diag.EvFocusChange).Str(diag.From, from).
		Str(diag.To, to).Msg("焦点切换")
	if old != nil {
		a.endSessionFocus(old)
	}
	if s != nil {
		// 播放意图只在"要听"这一向来的时候改。intentRestore 保留会话自己
		// 记着的 playing——那是用户上一次亲手设的值。
		if intent == intentListen {
			s.playing = true
			if s.volume <= 0 {
				// 这一间上次被静音过。点开它就是要听，恢复到静音前的值；
				// 没有记忆时回落到默认音量——以前这里硬写 0.6，和
				// DefaultVolume 是两份语义，导入过配置就会跳。
				if a.volumeBeforeMute > 0 {
					s.volume = a.volumeBeforeMute
				} else {
					s.volume = a.DefaultVolume
				}
			}
		}
		if s.volume > 0 {
			a.DefaultVolume = s.volume
		}
		a.applyFocusAudio()
	}
	a.markDirty()
	a.invalidateWindows()
}

// endSessionFocus 结束某一间的声音焦点。
//
// 会话存亡只由一件事决定：还在观测墙里吗。在 → 静音、留在墙上做预览；
// 不在 → 关掉。这就是"取消 active"和"关闭直播间"的全部区别。
func (a *App) endSessionFocus(s *roomSession) {
	if s == nil {
		return
	}
	if a.watchHas(s.key) && !a.watch.parked {
		// 只收声，画面不动：静音视频会被 MyGo 按住，看门狗会误判断流。
		a.applySessionVolume(s)
		return
	}
	// 已经离开墙（parked）：墙上也不留静音预览——墙上的预览是停掉的，
	// 留一个只会让它继续解码。
	a.closeSession(s, "focus-ended")
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

// noteRoute 记一次页面切换。只在路径真的变化时写；同一页换台由 focus.change
// 覆盖，不在这里重复。
func (a *App) noteRoute(from, to string) {
	if a.Router == nil || from == to || to == "" {
		return
	}
	diag.Ref{Sub: "nav"}.Logger().Info().
		Str(diag.Event, diag.EvRouteChange).Str(diag.From, from).
		Str(diag.To, to).Msg("页面切换")
}

// openRoom 是「看这一间」的唯一入口：首页卡片、关注行、搜索结果、墙格、
// 灵动岛都走这里。
//
// 播放页与其它表面之间**不做过渡**：一律硬切。以前这里是三种接法（Match /
// 整页摆位 / 飞行块），每一种都要在两帧之间对齐几何，代价是一堆状态机，而
// 它们互相踩——2026-11-28 全部拆掉，换成切。
func (a *App) openRoom(room Streamer) {
	if room.Platform == "" {
		return
	}
	key := watchKey(room)
	s := a.sessionFor(key, room)
	if a.focus != s {
		a.setFocus(s, intentListen)
	}
	if a.Router == nil {
		return
	}
	if a.Router.Path() == routePlayer {
		// 已经在播放页：换台不叠历史，否则退回去还是播放页，取流路径
		// 会把刚关掉的这一间又自动开起来。
		a.Router.Replace(routePlayer)
		a.invalidateWindows()
		return
	}
	// 记下退回哪一页。
	a.playerReturnRoute, a.playerReturnSection = a.Router.Path(), a.FollowsSection
	from := a.Router.Path()
	a.Router.Transition = ui.TransitionNone
	a.Router.Push(routePlayer)
	a.noteRoute(from, routePlayer)
	// 路由一换，全尺寸就已经成立；在这里立刻推一次，别等下一帧的 tickSurface。
	//
	// 后台听着的引擎还在 160×90 上渲染。晚推一帧，第一帧就是一张放大的小图。
	// 早推一个帧间隔，渲染线程才有时间交出全尺寸那一帧（实测 4ms，见
	// playback.md）。
	a.applyPresent()
}

// closeFocus 结束焦点：岛的 X、全屏返回钮、不在播放页的 Esc 都走这里。
//
// 声音立刻停；会话存亡由 endSessionFocus 的墙规则决定。在播放页上还要
// 退回上一页。
func (a *App) closeFocus() {
	s := a.focus
	if s == nil {
		return
	}
	if a.Router == nil || a.Router.Path() != routePlayer {
		a.setFocus(nil, intentListen)
		return
	}
	from := a.Router.Path()
	a.setFocus(nil, intentListen)
	a.Router.Transition = ui.TransitionNone
	a.Router.Back()
	a.noteRoute(from, a.Router.Path())
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
		a.Router.Transition = ui.TransitionNone
		from := a.Router.Path()
		a.Router.Back()
		a.noteRoute(from, a.Router.Path())
		return
	}
	a.leaveSurface(func() {
		from := a.Router.Path()
		a.Router.Back()
		a.noteRoute(from, a.Router.Path())
	})
}

// leaveSurface 在离开播放页时执行 action 换路由。
//
// 只做一件事：把路由器恢复成不播页面过渡，然后换页。播放页与其它表面之间
// 不做过渡，所以这里不再算落点、不再排动画。
func (a *App) leaveSurface(action func()) {
	a.Router.Transition = ui.TransitionNone
	action()
}

// resetSurfaceTransition 把「正在演的表面过渡」一次清干净。
//
// 换台、普通换页、全屏边沿都调它。播放页与其它表面之间不做过渡，所以现在
// 它只剩一件事：确保路由器不带着上一次的页面过渡。
func (a *App) resetSurfaceTransition() {
	if a.Router != nil {
		a.Router.Transition = ui.TransitionNone
	}
}

// pushPage 换到另一页（顶栏 tab、切平台）。
func (a *App) pushPage(target string) {
	if a.Router == nil {
		return
	}
	// 不在播放页：这是一次普通换页，不带页面过渡。
	if a.Router.Path() != routePlayer {
		a.resetSurfaceTransition()
		from := a.Router.Path()
		a.Router.Push(target)
		a.noteRoute(from, a.Router.Path())
		return
	}
	a.leaveSurface(func() {
		from := a.Router.Path()
		a.Router.Push(target)
		a.noteRoute(from, a.Router.Path())
	})
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
	//
	// bufferStallSince 也要清。它只在 deadReason 的缓冲分支里推进，判据是
	// pos != m.stillPos || bufferStallSince.IsZero()——回来时 pos 与刚清过的
	// stillPos 都是 0，第一条不成立；而 bufferStallSince 若还停在进后台之前，
	// now.Sub(bufferStallSince) > 20s 立刻命中，一回播放页就派一次热切换。
	// 这正是「进后台前正在缓冲」的回放路径。
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	m.bufferStallSince = time.Time{}
	a.applyPresent()
}

// backgroundEnter 进入后台收听：全尺寸呈现关掉，解码与声音留着。
//
// 和 closeFocus 的区别是「关到哪一层」：这里焦点还在，mpv 实例、地址续期、
// 断流重连全部保留。解码不停——回来时画面只差一帧，声音一秒不断。
func (a *App) backgroundEnter() {
	m := &a.media
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

// tickActivity 收掉「已经是一条死路」的焦点，并且只在它离开播放页之后收。
//
// 没有引擎、没在取流、已经落了终态（未开播 / 取流失败）的焦点，如果已经
// 不在播放页上，就没有任何表面在画它——留着只会让岛挂着一条不存在的路。
// 在播放页上不收：那一页自己画错误层，有「重试」。
//
// 以前这条判断写在 backgroundEnter 里（全尺寸边沿），判据还依赖
// stream.RoomID 这个可选字段的有无。现在它是每帧的观察，判据只看
// engine / loading / offline / err 四个状态本身。
func (a *App) tickActivity() {
	if a.focus == nil || a.Router == nil {
		return
	}
	if a.Router.Path() == routePlayer {
		return
	}
	if !a.focus.dead() {
		return
	}
	a.setFocus(nil, intentListen)
}

// tickLeavePlayer 是「刚离开播放页」那一帧的一次性收尾。
//
// 以前它写在 Shell 里紧挨着构建，离开播放页的收尾和这一帧建出来的界面
// 没有先后保证。挪到构建之前：收尾总是先发生，界面永远看到收好的状态。
func (a *App) tickLeavePlayer() {
	// 先把「上一帧在不在播放页」挪进 playerShownLastFrame，再更新 wasOnPlayer。
	//
	// 外壳挂不挂 Match 要用**上一帧**的值（见 shellMatchKey）：它决定外壳是不是
	// 一个可以被认领的「源」。读 wasOnPlayer 不行——它在这个函数末尾就变成
	// 本帧的值，外壳读到的永远是「本帧在播放页」，判据恒真。
	a.playerShownLastFrame = a.wasOnPlayer
	if a.Router == nil {
		a.wasOnPlayer = false
		return
	}
	onPlayer := a.Router.Path() == routePlayer
	if !onPlayer && a.wasOnPlayer {
		a.leavePlayer()
	}
	a.wasOnPlayer = onPlayer
}
