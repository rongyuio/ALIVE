package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
)

// 播放页的媒体层。
//
// 三样东西都挂在 App 上而不是视图里——视图每帧重建，而播放器、
// 弹幕连接都要跨帧活着。
//
//	player   ui/video 的播放器，走系统媒体框架解码
//	danmaku  一条 WebSocket 弹幕连接
type playerMedia struct {
	player  *ui.VideoPlayer
	danmaku live.DanmakuStream
	stream  live.Stream

	// roomKey 是 platform|roomID：换房间时对比它就知道要不要重连
	roomKey string
	// seq 是这一轮起播的代号。每开一轮就 +1，之前派出去的一切结果都变成过期
	// 结果，由接纳回调负责把它们的资源关掉。
	seq int
	// loading 表示取流还在进行
	loading bool
	// err 是取流失败的原因，直接显示在状态覆盖层上
	err string

	// cancel / ctx 是这一轮起播的寿命：换房间、换档位、换线路、离开播放页都会
	// 取消它，后台还在取流或起 ffmpeg 的 goroutine 据此提前收手。
	// 这个 context 没有启动 deadline，贯穿播放；换档或退出时旧 ffmpeg 应随它停止。
	cancel      context.CancelFunc
	ctx         context.Context
	win         *mygo.Window
	stopDanmaku func()
	roomCtx     context.Context
	roomCancel  context.CancelFunc
	// id / start 是这一轮的关联 ID 与起点，日志里的毫秒数都相对 start。
	id    string
	start time.Time
	// firstFrame 表示这一轮已经记过「首帧可用」。
	firstFrame   bool
	openedAt     time.Time
	pauseStarted time.Time
	pausedFor    time.Duration
	// upstream 是已经接到播放器上的那一档。
	upstream live.Variant

	// danmuSeq 单独管弹幕连接。换清晰度不该让同一房间的聊天断掉，也不能因为
	// 画面那一路的代号变了，就把刚连上的弹幕当成过期结果丢掉。
	danmuSeq int

	// 画面推进的观测点。不能直接信 VideoPlayer.Buffering()：Media
	// Foundation 在直播 HLS 上每一次 playlist 滚动都会发一次 WAITING，而把
	// 状态清回 Ready 靠的是 PLAYING / CANPLAY 事件——直播流上这两个不会再
	// 来第二次，状态就永久停在 Buffering，左下角胶囊跟着常驻。实测 2K
	// 直播：第 6 秒进入 Buffering 后 174 秒没退出，期间 Position 一直以
	// 1x 推进、画面正常。所以这里自己看时间轴有没有在动。
	// notice / noticeUntil 是播放页上的临时提示（例如平台降档）。
	notice      string
	noticeUntil time.Time
	// pendingQualityPick 记录用户这一次明确点选的档位，取流回来对比实际档位。
	pendingQualityPick string

	lastPos   time.Duration
	lastPosAt time.Time
	// stillPos / stillAt 是断流看门狗自己的观测点，和缓冲胶囊那套分开：
	// 胶囊 1.5 秒不动就提示，重连要等 10 秒，不能共用一个时间戳。
	stillPos time.Duration
	stillAt  time.Time
	// refreshAt 是当前这条地址到期前该换新地址的时刻；零值表示平台没声明寿命。
	refreshAt time.Time
	// refreshing 表示已经派出去一次热切换，避免每帧重复派。
	refreshing bool
	// lastSwapAt / swapFailures 用来退避：连续失败时不要每帧都重开一路。
	lastSwapAt   time.Time
	swapFailures int

	// background 表示正在后台收听：引擎活着、音频在走、解码不停，
	// 只是全尺寸呈现关掉了（渲染目标 160×90）。
	// 离开播放页但没点叉 / 没按 Esc 时进入，回到播放页时退出。
	//
	// 这一态下 roomKey 和 player 都留着——它要的就是「还是那一路、还在出声」，
	// 和 closeMedia 的「全关掉」是两件事。
	background bool
	// 连续性日志的观测点：缓冲起止、音频停走。只在状态翻转时写一条，
	// 正常播放不产生日志。
	buffering      bool
	bufferingSince time.Time
	diagAt         time.Time
	diagPos        time.Duration
	diagAudio      time.Duration
	audioStalled   bool
	// detachedAt 是进入后台收听的时刻，日志用。
	detachedAt time.Time
	// tickPending 表示后台收听的定时心跳已经排上，避免每帧重复排。
	tickPending bool
}

// stalledThreshold 是判定「画面停住」的阈值。
//
// 取 1.5 秒：正常直播的时间轴每帧都在动，掉到 1.5 秒不动就是真卡了；
// 再短会被解码抖动误伤。
const stalledThreshold = 1500 * time.Millisecond

// streamFetchBudget 是取流的预算。转封装有自己的一套等待，不占这个预算。
const streamFetchBudget = 40 * time.Second

// firstFramePollInterval 只在等待首帧时驱动画面检查，首帧后交还播放器调度。
const firstFramePollInterval = time.Second / 30

// playbackHealthPollInterval 是首帧之后驱动画面健康检查的节拍。
//
// 播放器停帧不会主动报错，只靠用户操作触发帧会漏掉它，所以首帧后按这个
// 间隔续一帧，让 trackFirstFrame 有机会看到故障。
const playbackHealthPollInterval = 250 * time.Millisecond

// firstFrameBudget 是首帧上限：超过它还没有画面就落到可重试的错误终态。
//
// libmpv 收到 loadfile 就返回，流能不能连上要等它自己解析。没有这条兜底，
// 上游给了个死地址时界面会永远停在「正在拉取直播流…」。
// 从播放器打开后计算，暂停时间不计入。
const firstFrameBudget = 20 * time.Second

// playbackSession 是一轮起播的凭据：媒体代号、寿命 ctx、日志关联 ID 与起点。
//
// 后台 goroutine 只拿它，不回读 App 字段——那些字段只属于主线程。
type playbackSession struct {
	seq   int
	win   *mygo.Window
	ctx   context.Context
	id    string
	start time.Time
}

// reset 在主线程取消所有会话，慢资源在后台释放。
func (m *playerMedia) reset() {
	m.cancelSession()
	m.closeDanmaku()
	m.stopPlayback()
	m.stream = live.Stream{}
	m.loading = false
	m.err = ""
	m.firstFrame = false
	m.notice = ""
	m.noticeUntil = time.Time{}
	m.pendingQualityPick = ""
	m.upstream = live.Variant{}
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	m.refreshAt, m.lastSwapAt = time.Time{}, time.Time{}
	m.refreshing = false
	m.swapFailures = 0
	m.background = false
	m.detachedAt = time.Time{}
	m.tickPending = false
}

// cancelSession 结束当前这一轮起播。
//
// ctx 取消后，后台取流与起 ffmpeg 的 goroutine 会自行收手；已经就绪但还没
// 交出去的那一路，会在接纳回调里被判过期后关掉自己。
func (m *playerMedia) cancelSession() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.ctx = nil
	m.seq++
}

// mode 是本轮的实际呈现方式。现在只有 libmpv 一条路，日志里固定记 native。
func (m *playerMedia) mode() string { return "native" }

// stalled 报告画面是不是真的停住了。
//
// 判据是「正在播 + 已经出图 + 时间轴 1.5 秒没动」。暂停、起播前、切档时
// 的跳变都不算——只看用户能感知的那件事：画面有没有在走。
func (m *playerMedia) stalled(p *ui.VideoPlayer) bool {
	if !p.Playing() || !p.Ready() {
		m.lastPosAt = time.Time{}
		return false
	}
	pos := p.Position()
	now := time.Now()
	if m.lastPosAt.IsZero() || pos != m.lastPos {
		m.lastPos, m.lastPosAt = pos, now
		return false
	}
	return now.Sub(m.lastPosAt) > stalledThreshold
}

// mediaTick 是媒体层的常驻心跳：首帧观测、地址续期、断流重连、错误终态。
//
// 每帧调用，判据是「有没有播放器」而不是「在不在播放页」。原来它挂在
// PlayerPage 上，离开播放页就没人跑了——后台收听时地址到期（斗鱼 300 秒）
// 就没人续，声音五分钟后自己断；窗口最小化时视图不重建，同样会断。
//
// 这一帧的驱动靠调用方（Shell）在必要时 c.After 续上；background 态下
// 视图里没有视频元素在请求帧，所以还额外挂一条定时器，见 kickMediaTick。
func (a *App) mediaTick() {
	if a.media.player == nil {
		return
	}
	a.trackFirstFrame()
}

// kickMediaTick 在后台收听时驱动 mediaTick。
//
// 有画面时 Video 元素每帧都在请求下一帧，看门狗跟着走；后台收听把全尺寸呈现关了，
// 没有元素再请求帧，于是只能自己排。间隔取和播放页一样的健康检查节拍。
//
// 定时器不持有 App 的可变状态，只往主线程排一次 Update——它本身就是线程安全的，
// 而且窗口销毁后 Update 会自己丢掉回调。
func (a *App) kickMediaTick() {
	m := &a.media
	if !m.background || m.player == nil || a.Win == nil {
		m.tickPending = false
		return
	}
	if m.tickPending {
		return
	}
	m.tickPending = true
	win := a.Win
	time.AfterFunc(playbackHealthPollInterval, func() {
		runOnMain(win, func() {
			a.media.tickPending = false
			a.mediaTick()
			a.kickMediaTick()
		})
	})
}

// closeMedia 是离开播放页时的清理入口。
//
// 先把代号推进（reset 里的 cancelSession 会做），再清 roomKey：正在路上的
// 取流结果回来时已经找不到对应的那一轮，只会被丢弃并关掉自己的资源。
func (a *App) closeMedia() {
	a.media.reset()
	a.media.roomKey = ""
}

// detachMedia 进入后台收听：全尺寸呈现关掉，解码与声音留着。
//
// 和 closeMedia 的区别是「关到哪一层」：这里只关呈现，mpv 实例、地址续期、
// 断流重连、房间记号全部保留。解码不停——回来时画面只差一帧，声音一秒不断；
// 代价是后台单核 7.0–9.8%（硬解，1080p60，见 decisions/0009）。
//
// 没有引擎时不做事：还在取流的路上就离开播放页，那一路会被 closeMedia 收掉，
// 不该在这里留一个「假装在听」的态。判据是引擎而不是播放器指针——没有引擎，
// 就没有声音可留。
func (a *App) detachMedia() {
	m := &a.media
	if m.background {
		return
	}
	e := m.engine()
	if e == nil {
		return
	}
	e.SetPresenting(false)
	m.background = true
	m.detachedAt = time.Now()
	// 后台只剩 160×90 的小帧、没人看，缓冲胶囊和停帧看门狗的判据（「画面有没有
	// 在动」）失去意义。基准拨到现在，回来时不会立刻误判成卡死。
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	// 已经飞出去的那批属于离开前的时刻；弹幕时钟在后台不走，留着回来会看到
	// 一屏定格的旧弹幕。清掉，回来只接新的。轨道占用与时钟基准一起重置。
	a.Danmu = nil
	a.danmuLanes = nil
	a.danmuPos = 0
	a.danmuStill = 0
	a.danmuLast = time.Time{}
	hwdec := ""
	if m.player != nil {
		if eng, ok := m.player.Engine().(*playback.Engine); ok {
			hwdec = eng.Debug().HWDec
		}
	}
	logStage(m.id, m.start, "detach", "quality", m.upstream.Label, "hwdec", hwdec)
}

// attachMedia 从后台收听回到播放页：接上现有这一路，只恢复全尺寸呈现。
//
// 后台期间解码、demuxer、音频输出、地址续期全都活着，回来不需要取流、不需要
// 重新选轨，也没有 refresh seek。引擎把渲染目标从 160×90 恢复成视频原始尺寸，
// 并立刻用当前帧重渲染一次：实测 4ms 出全尺寸画面，音频停走 21ms（就是
// audio-pts 的事件粒度），声音一秒没断。
//
// 之前的做法是后台 vid=no：省 CPU（1.6%），但回来要等 live 边缘的下一个关键帧
// （0.08–5.2 秒）。取舍与数据见 decisions/0009。
//
// 没有引擎时只能退回整路起播：detach 的前提是有引擎，这条分支理论上走不到，
// 但走不到不等于可以黑屏。
//
// 判据必须带上路由：转场期间 Router 会把上一页（播放页）再构建一次，而那时
// 已经在别的页面上了。只看 background 的话，PlayerPage 的 attach 和 Shell 的
// detach 会每帧互相翻一次——实测一秒能刷十几条 attach/detach 日志，播放器
// 跟着来回重建。
func (a *App) attachMedia() {
	m := &a.media
	if !m.background {
		return
	}
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	m.background = false
	m.detachedAt = time.Time{}
	e := m.engine()
	logStage(m.id, m.start, "attach", "quality", m.upstream.Label)
	if e == nil {
		a.startStream(true)
		return
	}
	// 后台那段时间没人看画面，看门狗的基准还停在离开前；拨到现在，回来不会
	// 立刻把「画面没动」判成卡死。
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	e.SetPresenting(true)
	// 弹幕不动：回来还是同一间房，聊天不该断。
}

// videoTrack 是后台收听要的那一小块引擎能力：开关全尺寸呈现。
//
// 关只把渲染目标收到 160×90，视频轨照常解码——不碰 vid、不碰 demuxer、
// 不碰 AO。回来时画面只差一帧，声音一秒不断。代价是后台单核 6.4–8.9%
// （硬解、1080p60；软件解码 22.9–33.4%），见 decisions/0009。
//
// 只声明用得到的一个方法，而不是整个 *playback.Engine：测试里没有 libmpv，
// 造不出真引擎，但这一段状态机该被验到。
type videoTrack interface {
	SetPresenting(on bool)
}

// engineOf 从播放器取出它的引擎。生产实现是类型断言；测试里换成假的。
//
// 播放器底下不是我们的引擎（比如将来换成别的实现）时返回 nil，
// 后台收听自然不成立——没有可关的呈现，也就不该假装在听。
var engineOf = func(p *ui.VideoPlayer) videoTrack {
	if p == nil {
		return nil
	}
	e, _ := p.Engine().(*playback.Engine)
	if e == nil {
		return nil
	}
	return e
}

// engine 取出当前播放器底下的引擎。
//
// ui.VideoPlayer 把引擎藏在内部，只在这一处需要摸到它——呈现的开关是
// 渲染尺寸，不是播放器接口的一部分。
func (m *playerMedia) engine() videoTrack { return engineOf(m.player) }

// CloseMedia 在窗口关闭前取消取流与房间会话，并异步释放连接及转封装资源。
// 必须从主线程调用；后台资源回收不依赖后续 Window.Update 回调。
func (a *App) CloseMedia() { a.closeMedia() }

// ensureMedia 保证当前正在播放的直播间已经接上真实流。
//
// 只在房间变化时启动；同一个房间反复进入播放页不会重复取流。
func (a *App) ensureMedia(c *ui.Context) {
	// 只在播放页取流。
	//
	// 转场期间 Router.View 还会重建上一页（淡出动画），此时 PlayerPage 会
	// 被再构建一次；没有这道判断，它会和 Shell 里「离开播放页就 closeMedia」
	// 每帧互相抵消——roomKey 被清掉、下一帧又开一路流，一秒能堆出十几个
	// ffmpeg，第一次踩到时直接把进程拖死了。
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	s := a.current()
	if s.Platform == "" || s.RoomID == "" {
		return
	}
	key := s.Platform + "|" + s.RoomID
	if key == a.media.roomKey {
		return
	}

	a.media.reset()
	a.media.roomKey = key
	slog.Info("进入播放页，开始取流", "key", key)
	a.startStream(false)
}

// beginSession 开一轮新的起播：先停掉旧画面、结束上一轮，再建自己的寿命 ctx。
//
// 必须在主线程调用：它读写的是媒体层字段。返回的 playbackSession 是后台
// goroutine 唯一该看的东西。
func (a *App) beginSession(stage string) playbackSession {
	m := &a.media
	m.cancelSession()
	a.stopPlayback()

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.ctx = ctx
	m.win = a.Win
	m.id = fmt.Sprintf("%s#%d", stage, m.seq)
	m.start = time.Now()
	m.firstFrame = false
	m.openedAt = time.Time{}
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	m.upstream = live.Variant{}
	m.loading = true
	m.err = ""

	sess := playbackSession{seq: m.seq, ctx: ctx, id: m.id, start: m.start, win: m.win}
	logStage(sess.id, sess.start, "begin")
	return sess
}

// session 打包当前这一轮的凭据，交给后台 goroutine 用。
//
// 后台不能回读 App 字段（那些只属于主线程），所以这里一次拷全。
func (a *App) session() playbackSession {
	m := &a.media
	return playbackSession{seq: m.seq, ctx: m.ctx, id: m.id, start: m.start, win: m.win}
}

// startStream 取流 → 转封装 → 播放器 →（按需）弹幕。
//
// keepDanmaku 为 true 时保留已有的弹幕连接：换清晰度、换线路都不该
// 把聊天打断——画面和弹幕是两条独立的路。
func (a *App) startStream(keepDanmaku bool) {
	s := a.current()
	if s.Platform == "" || s.RoomID == "" {
		return
	}
	prov, ok := providerFor(live.Platform(s.Platform))
	if !ok {
		a.media.loading = false
		a.media.err = "该平台还不支持取流"
		return
	}
	_, hasStream := prov.(live.Streamer)
	_, hasPlayback := prov.(live.PlaybackStreamer)
	if !hasStream && !hasPlayback {
		a.media.loading = false
		a.media.err = "该平台还不支持取流"
		return
	}

	// 新的一轮：停掉旧画面（两路 ffmpeg 同时拉会抢带宽，播放器也会争硬件
	// 解码器），取消上一轮的后台工作，再记下这一轮的关联 ID 与起点。
	sess := a.beginSession("取流")
	if !keepDanmaku {
		a.closeDanmaku()
		// 换房间时旧房的弹幕一条都不能留：面板是「历史」，混进上一间房
		// 的聊天会让人以为串台了。
		a.DanmuLog = nil
		a.Danmu = nil
		a.panelSeen = 0
		a.panelPending = 0
	}

	// 界面上存的是线路标签（「主线」「备线1」），取流要的是节点 id。
	line, want := a.lineID(a.Line), a.Quality
	// 只有用户主动切档才记录；进房间时的默认档不该弹“降档”提示。
	picked := a.media.pendingQualityPick
	a.media.pendingQualityPick = ""
	roomID := s.RoomID
	// 窗口在主线程上取一次再交给后台 goroutine：弹幕连接要用它做回主线程
	// 的入口，而 a.Win 本身不是并发安全的读。
	win := sess.win
	// 房间会话独立于起播会话：切档保留尚在建立的弹幕连接，取流失败也不阻断聊天。
	if a.media.roomCtx == nil {
		a.media.roomCtx, a.media.roomCancel = context.WithCancel(context.Background())
		roomCtx, dseq := a.media.roomCtx, a.media.danmuSeq
		go a.startDanmaku(roomCtx, dseq, prov, roomID, win)
	}

	go func() {
		fetchCtx, cancel := context.WithTimeout(sess.ctx, streamFetchBudget)
		defer cancel()

		info, selected, err := fetchStream(fetchCtx, prov, roomID, line, want)
		if err != nil {
			// 被新一轮取代时不必报错：用户已经不看这一路了。
			if sess.ctx.Err() != nil {
				logStage(sess.id, sess.start, "fetch-canceled")
				return
			}
			logStage(sess.id, sess.start, "fetch-failed", "error", errClass(err))
			a.applyMedia(sess, func() {
				a.media.loading = false
				a.media.err = "取流失败，请重试或切换线路"
			}, nil)
			return
		}
		logStage(sess.id, sess.start, "fetch", "live", info.Live,
			"variants", len(info.Variants), "lines", len(info.Lines),
			"selected", selected.Label)

		// 房间详情先上屏：标题、头像、是否在播都不依赖转封装
		a.applyMedia(sess, func() {
			a.media.stream = info
			switch {
			// 平台确认了实际档位就听它的：请求「原画」但服务端只给了
			// 「蓝光」时，菜单必须显示真正在播的那一档，不能让标签撒谎。
			case selected.URL != "" && (selected.Label == "" || selected.ID == ""):
				a.Quality = "默认"
			case selected.Label != "":
				a.Quality = selected.Label
			case len(info.Variants) > 0 && !hasVariantLabel(info.Variants, a.Quality):
				// 档位 / 线路的标签跟着平台走：上一间房选的「蓝光」在这一间
				// 可能根本不存在，不校正就会显示一个菜单里没有的值。
				a.Quality = defaultVariantLabel(info.Variants)
			}
			// 单线路的平台（抖音）根本没有线路概念，控件整块不出现；
			// 旧的线路名要清掉，否则控制条上会挂着一个不存在的值。
			switch {
			case len(info.Lines) == 0:
				a.Line = ""
			case !hasLineLabel(info.Lines, a.Line):
				a.Line = info.Lines[0].Label
			}
			// 平台可以给不了所点的档位（斗鱼对匿名请求会把「原画」降成
			// 「蓝光4M」）。默默换掉会让人以为切档没生效，所以明说一次。
			if picked != "" && selected.Label != "" && !sameQuality(picked, selected.Label) {
				slog.Info("清晰度被平台降档", "requested", picked, "applied", selected.Label)
				a.notifyPlayback("该直播间没有「" + picked + "」，已切到「" + selected.Label + "」")
			}
		}, nil)

		if !info.Live {
			a.applyMedia(sess, func() { a.media.loading = false }, nil)
			return
		}
		if len(info.Variants) == 0 {
			// 开播了但平台没给任何档位：菜单和地址都是空的。只落 loading=false
			// 会让覆盖层四个分支一个都不命中，画面停在封面上，连重试都点不到。
			a.applyMedia(sess, func() {
				a.media.loading = false
				a.media.err = "平台没有返回可播放的清晰度"
			}, nil)
			return
		}

		// selected 是本次确认结果；旧接口的选档已由 fetchStream 完成。
		upstream := selected
		if upstream.URL == "" {
			// 按需取流的平台在地址解析失败时会走到这里：菜单完整但没有任何
			// 一项能播，报错比让用户对着加载层干等强。
			a.applyMedia(sess, func() {
				a.media.loading = false
				a.media.err = "没有拿到可播放的地址"
			}, nil)
			return
		}
		a.startPlayback(sess, upstream)
	}()
}

// fetchStream 取这一轮要播的流。
//
// 平台实现了 live.PlaybackStreamer 时只解析当前选中的档位：菜单仍然是完整的，
// 未解析项没有地址，省掉「点进直播间先把所有档位 × 所有线路请求一遍」的等待。
// 否则退回旧接口，整份菜单的地址一次性拿全。
func fetchStream(ctx context.Context, prov live.Provider, roomID, line, quality string) (live.Stream, live.Variant, error) {
	if pb, ok := prov.(live.PlaybackStreamer); ok {
		return pb.StreamForPlayback(ctx, roomID, line, quality)
	}
	streamer, ok := prov.(live.Streamer)
	if !ok {
		return live.Stream{}, live.Variant{}, live.ErrUnsupported
	}
	info, err := streamer.Stream(ctx, roomID, line)
	if err != nil {
		return info, live.Variant{}, err
	}
	return info, pickVariant(info.Variants, quality), nil
}

// startPlayback 交给 libmpv 接手这一路。
//
// sess.ctx 无启动 deadline，直到切档或退出才取消。
func (a *App) startPlayback(sess playbackSession, upstream live.Variant) {
	if a.startNativePlayback(sess, upstream) {
		return
	}
	// 没有窗口就无处呈现。
	a.applyMedia(sess, func() {
		a.media.loading = false
		a.media.err = "窗口未就绪，无法播放"
	}, nil)
}

// startNativePlayback 用 libmpv 接手这一路。
//
// 返回 false 表示当前环境起不来（没有窗口）。成功后的接纳照旧走 applyMedia：
// 过期结果自己关掉，不让旧会话的画面盖上来。
func (a *App) startNativePlayback(sess playbackSession, upstream live.Variant) bool {
	win := a.media.win
	if win == nil {
		return false
	}
	logStage(sess.id, sess.start, "playback-path", "mode", "native", "codec", upstream.Codec)
	opts := playback.Options{
		Headers:   upstream.Headers,
		Width:     upstream.Width,
		Height:    upstream.Height,
		FrameRate: upstream.FrameRate,
	}
	go func() {
		e, err := openNative(sess.ctx, upstream.URL, opts)
		if err != nil {
			logStage(sess.id, sess.start, "native-failed", "error", errClass(err))
			if sess.ctx.Err() != nil {
				return
			}
			runOnMain(win, func() {
				a.applyMedia(sess, func() {
					a.media.loading = false
					a.media.err = "播放组件打不开这个流，请重试或切换线路"
				}, nil)
			})
			return
		}
		runOnMain(win, func() {
			a.applyMedia(sess, func() {
				// 引擎交给播放器：之后每帧由 MyGo 问 Frame(t)，我们不再自己推帧。
				// 关播放器就关引擎，所以这里不必再挂一个 stop。
				p := ui.NewEngineVideoPlayer(e)
				a.adoptPlayback(sess, p, upstream)
			}, func() { _ = e.Close() })
		})
	}()
	return true
}

// cleanupOnCancel 让未交付和已接纳的资源都随会话释放；慢清理始终在后台执行。
// 显式拒绝、打开失败和取消可以同时发生，但资源只关闭一次。
func cleanupOnCancel(ctx context.Context, closeResource func()) func() {
	var once sync.Once
	closeOnce := func() { once.Do(closeResource) }
	unwatch := context.AfterFunc(ctx, closeOnce)
	return func() {
		unwatch()
		go closeOnce()
	}
}

// adoptPlayback 把已经就绪的播放器接到媒体层上。
//
// 只在主线程、且代号已被采纳时调用。
func (a *App) adoptPlayback(sess playbackSession, p *ui.VideoPlayer, upstream live.Variant) {
	a.media.openedAt = time.Now()
	a.media.pauseStarted = time.Time{}
	a.media.pausedFor = 0
	a.media.player = p
	a.media.upstream = upstream
	a.media.refreshAt = refreshAtFor(upstream)
	a.media.loading = false
	a.media.err = ""
	// 播放器打开就按当前音量对齐，别让用户先听到满音量再跳
	volume := float64(a.Volume)
	// 0 就是静音：播放器的 SetVolume(0) 与 SetMuted(true) 在听感上等价，
	// 统一走 SetVolume 一条路径。
	p.SetVolume(volume)
	a.pushedVolume = volume
	if !a.Playing {
		a.media.pauseStarted = a.media.openedAt
		p.Pause()
	}
	a.adoptBackgroundPresentingOff()
	logStage(sess.id, sess.start, "open", "mode", "native", "quality", upstream.Label,
		"ready", p.Ready(), "declared_width", upstream.Width, "declared_height", upstream.Height)
	// 小窗可能正开着而主窗口藏着：这一路是在主窗口的 Update 里接上的，
	// 不叫小窗重画一次，它会一直停在「没有播放器」的那一帧封面上。
	a.invalidateWindows()
}

// adoptBackgroundPresentingOff 把人不在播放页时开出来的这一路按后台收听处理（关全尺寸呈现）。
//
// 会走到这里的典型是「后台收听时登录成功、要求重取流」：beginSession 里的
// stopPlayback 清掉了 background（那一刻确实没有引擎），新引擎于是带着
// 打开的 vid 建出来——画面没人看，解码照跑；岛也因为 background 是 false
// 而从「正在收听」掉回默认态。
//
// 判据用路由而不是 background：background 在这一刻还没被置上。
func (a *App) adoptBackgroundPresentingOff() {
	if a.Router != nil && a.Router.Path() == routePlayer {
		return
	}
	e := a.media.engine()
	if e == nil {
		return
	}
	e.SetPresenting(false)
	a.media.background = true
	a.media.detachedAt = time.Now()
}

// stopPlayback 收掉播放器，弹幕不动。
func (a *App) stopPlayback() { a.media.stopPlayback() }

// refreshLead 是提前多久换地址：留出取流 + 首帧的时间，别卡在过期那一刻。
// 斗鱼的直链签 300 秒（实测），60 秒足够重开一路。
const refreshLead = 60 * time.Second

// swapRetryDelay 是换地址失败后多久再试。
const swapRetryDelay = 15 * time.Second

// swapMaxFailures 是连续失败多少次之后放弃无缝切换、退回整轮重来。
const swapMaxFailures = 2

// swapRequest 是一次换地址要用的上下文：全部在主线程上取好，后台不再回读 App。
type swapRequest struct {
	platform live.Platform
	roomID   string
	line     string
	quality  string
	// background 说明派这次换地址时正在后台收听：没人看画面，判据换成音频时钟。
	background bool
}

// refreshAtFor 算出该在什么时候换地址：平台声明了寿命就提前一分钟。
func refreshAtFor(v live.Variant) time.Time {
	if v.Expires.IsZero() {
		return time.Time{}
	}
	return v.Expires.Add(-refreshLead)
}

// scheduleSwap 派一次「取新地址 → 开第二路 → 换上去」。
//
// 两种触发：地址快到期了，或者这一路已经停了（播放器自己结束 / 时间轴不动）。
// 两条要的是同一件事——再要一份能播的地址——所以走同一条路。
func (a *App) scheduleSwap(reason string) {
	m := &a.media
	if m.refreshing || m.player == nil {
		return
	}
	if !m.lastSwapAt.IsZero() && time.Since(m.lastSwapAt) < swapRetryDelay {
		return
	}
	m.refreshing = true
	m.lastSwapAt = time.Now()
	sess := a.session()
	cur := a.current()
	req := swapRequest{platform: live.Platform(cur.Platform), roomID: cur.RoomID, line: a.Line, quality: m.upstream.Label, background: m.background}
	logStage(sess.id, sess.start, "swap-start", "reason", reason)
	go a.runHotSwap(sess, req, reason)
}

// runHotSwap 在后台取一条新地址、开好第二路，等它出图再交给主线程换上去。
//
// 新的一路先出图再切换，所以屏幕上不会经过加载态；切换那一刻画面是连续的，
// 只有声音有一次几百毫秒的接缝——网页端也是这个代价。
func (a *App) runHotSwap(sess playbackSession, req swapRequest, reason string) {
	fail := func(err error) {
		logStage(sess.id, sess.start, "swap-failed", "reason", reason, "error", errClass(err))
		runOnMain(sess.win, func() {
			a.media.refreshing = false
			a.media.swapFailures++
			if a.media.swapFailures >= swapMaxFailures {
				// 新地址也开不起来：退回整轮重来，至少让用户看到进度。
				a.media.swapFailures = 0
				a.startStream(true)
				return
			}
			a.media.refreshAt = time.Now().Add(swapRetryDelay)
		})
	}
	prov, ok := providerFor(req.platform)
	if !ok {
		fail(errors.New("没有这个平台的取流入口"))
		return
	}
	if _, ok := prov.(live.Streamer); !ok {
		fail(errors.New("这个平台不能取流"))
		return
	}
	_, selected, err := fetchStream(sess.ctx, prov, req.roomID, req.line, req.quality)
	if err != nil {
		fail(err)
		return
	}
	if selected.URL == "" {
		fail(errors.New("没有拿到可播放的地址"))
		return
	}
	e, err := openNative(sess.ctx, selected.URL, playback.Options{
		Headers: selected.Headers, Width: selected.Width, Height: selected.Height, FrameRate: selected.FrameRate,
	})
	if err != nil {
		fail(err)
		return
	}
	// 等到第一帧（音频门也在里面）：没有画面就换上去等于黑屏。
	//
	// 后台收听时没人看画面，等音频时钟更贴近真正要保证的那件事：time-pos
	// 走起来就说明这一路真的在出声，换上去之后再把它切回小尺寸呈现。
	deadline := time.Now().Add(8 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if sess.ctx.Err() != nil {
			_ = e.Close()
			return
		}
		if req.background {
			if e.Position() > 0 {
				ready = true
				break
			}
		} else if _, _, ok := e.Frame(time.Now()); ok {
			ready = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		_ = e.Close()
		if req.background {
			fail(errors.New("新地址 8 秒内没有声音"))
		} else {
			fail(errors.New("新地址 8 秒内没有画面"))
		}
		return
	}
	runOnMain(sess.win, func() {
		a.applyMedia(sess, func() {
			a.adoptSwap(sess, e, selected, reason)
		}, func() { _ = e.Close() })
	})
}

// adoptSwap 把新的一路接到媒体层上，并关掉旧的。
func (a *App) adoptSwap(sess playbackSession, e *playback.Engine, v live.Variant, reason string) {
	old := a.media.player
	p := ui.NewEngineVideoPlayer(e)
	a.media.player = p
	a.media.upstream = v
	a.media.refreshAt = refreshAtFor(v)
	a.media.refreshing = false
	a.media.swapFailures = 0
	// 后台收听时换上去的是一路新引擎：新引擎的呈现闸门默认是开的，不补
	// 这一下，换完地址它会在后台一直唤醒窗口，后台收听就名存实亡了。
	if a.media.background {
		e.SetPresenting(false)
	}
	volume := float64(a.Volume)
	p.SetVolume(volume)
	a.pushedVolume = volume
	if !a.Playing {
		p.Pause()
	}
	if old != nil {
		old.Close()
	}
	logStage(sess.id, sess.start, "swap-done", "reason", reason, "quality", v.Label, "background", a.media.background)
	// 旧播放器是在小窗正显示的时候被换掉的，同样得让小窗重画：新引擎的
	// wake 要等小窗下一次画 Video 才会指向它。
	a.invalidateWindows()
}

func (m *playerMedia) stopPlayback() {
	if m.player != nil {
		m.player.Close()
		m.player = nil
	}
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	m.refreshAt, m.lastSwapAt = time.Time{}, time.Time{}
	m.refreshing = false
	m.swapFailures = 0
	// 引擎没了，后台收听就不成立：不清这一位，灵动岛会挂着一颗「正在收听」
	// 的圆胶囊，而底下根本没有声音——比不显示更糟。
	m.background = false
	m.detachedAt = time.Time{}
	m.tickPending = false
	m.buffering = false
	m.bufferingSince = time.Time{}
	m.diagAt = time.Time{}
	m.diagPos, m.diagAudio = 0, 0
	m.audioStalled = false
}

// failPlayback 把这一轮落到可重试的终态。
//
// 先收掉播放器——留着坏的会让错误层盖在一块还在解码的黑画面上。房间 ctx 和
// 弹幕不动：画面断了不代表聊天断了，也不该因为一次播放失败触发整房间重连。
func (a *App) failPlayback(class, msg string) {
	m := &a.media
	logStage(m.id, m.start, class, "mode", m.mode())
	a.stopPlayback()
	m.loading = false
	m.err = msg
	// 小窗里没有错误层，播放器一收它只能看到一张旧封面：重画一次，
	// 至少让「画面没了」和「播放器被收掉」发生在同一帧上。
	a.invalidateWindows()
}

// trackContinuity 记连续性事件：缓冲起止与音频停走。
//
// 每帧调用，只在状态翻转时写一条日志——正常播放一条都不写。属性来自
// playback.Engine.Debug()，都是 mpv 自己报的：cache 空了是上游断供，
// 音频时钟停了而时间轴还在走才是音频这一侧的问题。两者分开，才不会把
// 上游的锅记到我们头上。
func (a *App) trackContinuity(p *ui.VideoPlayer, now time.Time) {
	e, _ := p.Engine().(*playback.Engine)
	if e == nil {
		return
	}
	d := e.Debug()
	m := &a.media
	if d.Buffering != m.buffering {
		args := []any{"pos", d.Position.Milliseconds(), "audio", d.AudioPTS.Milliseconds(),
			"cache_ms", d.CacheDuration.Milliseconds(), "cache_pct", d.CacheBuffering,
			"decoder_drops", d.DecoderDrops, "mistimed", d.Mistimed, "avsync", d.AVSync}
		if d.Buffering {
			m.bufferingSince = now
			logStage(m.id, m.start, "缓冲开始", args...)
		} else {
			args = append(args, "stalled_ms", now.Sub(m.bufferingSince).Milliseconds())
			logStage(m.id, m.start, "缓冲结束", args...)
		}
		m.buffering = d.Buffering
	}
	// 音频停走：一秒采一次。缓冲期内不算——上游没数据，声音当然停。
	if now.Sub(m.diagAt) < time.Second {
		return
	}
	if !m.diagAt.IsZero() && !d.Buffering && d.AudioPTS == m.diagAudio && d.Position > m.diagPos {
		if !m.audioStalled {
			m.audioStalled = true
			logStage(m.id, m.start, "音频停走", "pos", d.Position.Milliseconds(),
				"audio", d.AudioPTS.Milliseconds(), "avsync", d.AVSync)
		}
	} else if d.AudioPTS > m.diagAudio {
		m.audioStalled = false
	}
	m.diagAt, m.diagPos, m.diagAudio = now, d.Position, d.AudioPTS
}

// trackFirstFrame 记录首帧，并在超时和播放器报错时落到可重试终态。
//
// 每帧调用。Ready 只说明解码器交出了第一帧，不等于它在屏幕上呈现的时刻；
// 日志按「首帧可用」标注，别当成精确的观感时间。
func (a *App) trackFirstFrame() {
	m := &a.media
	p := m.player
	if p == nil {
		return
	}
	now := time.Now()
	a.trackContinuity(p, now)
	if !m.firstFrame {
		m.observeStartupPause(a.Playing, now)
	}
	// 播放器报错时 Ready 仍可能为真，这个判断必须排在记录首帧之前。
	if err := p.Err(); err != nil {
		// 暂停中不重启也不清理：用户主动停着，恢复播放那一帧会再进来处理。
		if !a.Playing {
			return
		}
		a.failPlayback("playback-failed", "播放失败，请重试或切换线路")
		return
	}
	// 后台收听：全尺寸呈现关着、没人看画面，Ready / Position 的含义都变了。
	//
	// 这里不能再走首帧那一套——后台没人看画面，首帧预算会一路走到超时，
	// 把正在出声的一路判成失败。判据换成「音频时钟还在不在走」：它停了才是
	// 真的断了，那时候才换地址。
	if m.background {
		a.trackBackground(p, now)
		return
	}
	if m.firstFrame || p.Ready() {
		if !m.firstFrame {
			m.firstFrame = true
			w, h := p.Size()
			hwdec := ""
			if e, ok := p.Engine().(*playback.Engine); ok {
				hwdec = e.Debug().HWDec
			}
			logStage(m.id, m.start, "首帧可用", "mode", m.mode(), "hwdec", hwdec, "upstream_codec", m.upstream.Codec,
				"declared_width", m.upstream.Width, "declared_height", m.upstream.Height,
				"declared_bitrate", m.upstream.Bitrate, "declared_fps", m.upstream.FrameRate,
				"decoded_width", w, "decoded_height", h, "buffering", p.Buffering())
		}
		// 两条触发，最后都落到「换一份新地址」：
		//   1. 地址快到期了（平台声明过寿命）——提前换，用户看不到停；
		//   2. 这一路已经停了：播放器自己结束，或者时间轴长时间不动。
		//      这是兜底，正常情况下第一条先触发。
		if a.Playing {
			if why := m.deadReason(true, p.Playing(), p.Position(), now); why != "" {
				// 不在这里打日志：卡住的时候每帧都会命中，scheduleSwap 自己会
				// 记一条 swap-start，带原因。
				a.scheduleSwap("dead-" + why)
			}
		}
		if !m.refreshAt.IsZero() && !now.Before(m.refreshAt) {
			a.scheduleSwap("expire")
		}
		return
	}
	// Ready 已经在上面的分支里优先，所以这里只剩「确实还没出图」的情况。
	if m.needFirstFrameTimeout(now, a.Playing) {
		a.failPlayback("first-frame-timeout", "直播流长时间没有画面，请重试或切换线路")
	}
}

// streamStallTimeout 是「时间轴多久不动就当成这一路已经死了」。
//
// 缓冲也会让时间轴停住，所以这个值必须比缓冲胶囊的 1.5 秒宽：6 秒还没动一下，
// 就不是抖动，是断了。再长用户就盯着静止画面了。
const streamStallTimeout = 6 * time.Second

// trackBackground 是后台收听态的健康检查。
//
// 唯一的判据是音频时钟：画面关掉了，Ready 和 Frame 都不再说明任何事，
// 只有 time-pos 还在前进才表示这一路真的在出声。它停了有两种可能——地址
// 到期（斗鱼 300 秒）或者上游断流，两条都落到「换一份新地址」。
//
// 首帧预算在这里不适用：后台态本来就不该有画面，用画面判死会把一路好好
// 出声的流关掉。
func (a *App) trackBackground(p *ui.VideoPlayer, now time.Time) {
	m := &a.media
	if !p.Playing() {
		// 用户主动暂停：声音本来就该停，别把它当成断流。
		m.stillPos, m.stillAt = 0, time.Time{}
		return
	}
	pos := p.Position()
	if m.stillAt.IsZero() || pos != m.stillPos {
		m.stillPos, m.stillAt = pos, now
		return
	}
	// 后台态用同一档阈值：6 秒没有一毫秒推进，不是抖动。
	if now.Sub(m.stillAt) <= streamStallTimeout {
		return
	}
	m.stillPos, m.stillAt = 0, time.Time{}
	// 地址到期同样要续：后台没人看画面，但声音不能断。
	if !m.refreshAt.IsZero() && !now.Before(m.refreshAt) {
		a.scheduleSwap("expire")
		return
	}
	a.scheduleSwap("dead-background")
}

// deadReason 报告直播是不是已经不能继续播了，返回空串表示还活着。
//
// ready 表示出过图，playing 是播放器自己报的状态，pos 是时间轴。直播没有
// 「播完」这一说，所以播放器自己停下只有一种解释：这颗地址不能用了——CDN
// 给的签名地址有过期时间（斗鱼实测约 5 分钟），到期后连接被关，mpv 把它当成
// 文件结束。卡住（时间轴不动）是另一种：数据断了，mpv 还在原地等。
func (m *playerMedia) deadReason(ready, playing bool, pos time.Duration, now time.Time) string {
	if !ready {
		return ""
	}
	if !playing {
		return "ended"
	}
	if m.stillAt.IsZero() || pos != m.stillPos {
		m.stillPos, m.stillAt = pos, now
		return ""
	}
	if now.Sub(m.stillAt) > streamStallTimeout {
		return "stalled"
	}
	return ""
}

// needFirstFrameTimeout 报告这一轮是否已经超出首帧预算。
// only 约束「确实还没出图」的情况：Ready 由调用方先判，已出图和暂停都不算。
// libmpv 收到 loadfile 就返回，流能不能连上要等它自己解析；没有这条兜底，
// 上游给了个死地址时界面会永远停在「正在拉取直播流…」。
func (m *playerMedia) needFirstFrameTimeout(now time.Time, playing bool) bool {
	if !playing || m.firstFrame || m.openedAt.IsZero() {
		return false
	}
	return m.startupElapsed(now) >= firstFrameBudget
}

// observeStartupPause 不把用户暂停的时间算进首帧超时预算。
func (m *playerMedia) observeStartupPause(playing bool, now time.Time) {
	if !playing && m.pauseStarted.IsZero() {
		m.pauseStarted = now
	}
	if playing && !m.pauseStarted.IsZero() {
		m.pausedFor += now.Sub(m.pauseStarted)
		m.pauseStarted = time.Time{}
	}
}

// startupElapsed 是「播放器打开后实际在等首帧」的时长，暂停不计入。
//
// 暂停期间 pauseStarted 非零，以它为止点；恢复播放时 observeStartupPause 才把
// 这一段补进 pausedFor。这样即使某一帧在暂停中间跑（点击、动画驱动），预算也
// 不会被暂停时间吃掉。
func (m *playerMedia) startupElapsed(now time.Time) time.Duration {
	if m.openedAt.IsZero() {
		return 0
	}
	end := now
	if !m.pauseStarted.IsZero() {
		end = m.pauseStarted
	}
	return end.Sub(m.openedAt) - m.pausedFor
}

// PickQuality 是控制条上选中某个清晰度之后的动作。
//
// 导出是为了让播放页之外的入口（验收程序、将来的快捷键）走同一条换档链路，
// 而不是各自复制一份取流逻辑。
//
// 菜单里这一档已经带地址时直接换上游重开一路：不用重新取流，也不会因为
// 重新枚举档位把正在看的画面闪一下。地址还没解析（PlaybackStreamer 只解析
// 当前档）时走取流流程。两种情况都保留弹幕连接。
func (a *App) PickQuality(label string) {
	a.Quality = label
	a.markDirty()
	if a.media.roomKey == "" {
		return
	}

	a.media.pendingQualityPick = label
	v, ok := a.media.variantByLabel(label)
	if ok && v.URL != "" {
		a.media.pendingQualityPick = ""
		sess := a.beginSession("换档")
		a.media.upstream = v
		go a.startPlayback(sess, v)
		return
	}
	// 档位表还没到（刚进页面就点了一下），或者按需取流的平台还没解析这一档：
	// 退回完整流程。菜单保持完整，弹幕不动。
	a.startStream(true)
}

// pickLine 是选中某条线路之后的动作。
//
// 线路会参与上游地址的解析（斗鱼每个线路给的 CDN 不同），所以必须
// 重新取一次 stream；弹幕连接保留。
func (a *App) pickLine(label string) {
	a.Line = label
	a.markDirty()
	if a.media.roomKey != "" {
		a.startStream(true)
	}
}

// qualityOptions 是清晰度菜单的选项，全部来自平台的真实档位。
//
// 还没取到档位时返回 nil：控制条上先不出现这个下拉。以前这里返回
// {"原画"} 撑位，但平台根本没有叫「原画」的档位，用户会先看到一个
// 不存在的选项，等真档位回来再被替换掉。
func (a *App) qualityOptions() []string {
	variants := a.media.stream.Variants
	if len(variants) == 0 {
		return nil
	}
	out := make([]string, 0, len(variants))
	for _, v := range variants {
		out = append(out, v.Label)
	}
	return out
}

// lineOptions 是线路菜单的选项，来自平台的真实线路。
//
// 没有线路就返回空：抖音这类平台本来就没有多线路，控制条上不该多出
// 一个只能选「自动」的控件——网页端也没有这种东西。
func (a *App) lineOptions() []string {
	lines := a.media.stream.Lines
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Label)
	}
	return out
}

// variantByLabel 按界面上的档位名找真实档位。
func (m *playerMedia) variantByLabel(label string) (live.Variant, bool) {
	for _, v := range m.stream.Variants {
		if v.Label == label {
			return v, true
		}
	}
	return live.Variant{}, false
}

// pickVariant 按标签选档位；空或找不到时用第一档。
//
// 菜单是按 qn 降序排的，第一档就是当前会话能拿到的最高档——进房间
// 默认播它，想看低画质再自己往下选。
func pickVariant(variants []live.Variant, label string) live.Variant {
	if len(variants) == 0 {
		return live.Variant{}
	}
	if label != "" {
		for _, v := range variants {
			if v.Label == label {
				return v
			}
		}
		// 菜单标签和平台这次的档位名对不上（列表刷新过）：仍然给一个能播的，
		// 但要说清楚，并且让上层用“点选 vs 实际”的对比提示用户。
		slog.Info("清晰度标签未命中，改用默认档", "requested", label, "fallback", variants[0].Label)
	}
	return variants[0]
}

// defaultVariantLabel 取默认档位的显示名。
func defaultVariantLabel(variants []live.Variant) string {
	if len(variants) == 0 {
		return ""
	}
	return variants[0].Label
}

// lineID 把界面上的线路名翻回平台要的节点 id。
//
// B 站的线路在界面上是「主线 / 备线1」，实际切的是 CDN 节点域名；
// 取流接口只认后者，所以发请求前要换一次。
func (a *App) lineID(label string) string {
	for _, l := range a.media.stream.Lines {
		if l.Label == label {
			return l.ID
		}
	}
	return label
}

// hasVariantLabel 报告 label 是否在这一档表里。
func hasVariantLabel(variants []live.Variant, label string) bool {
	for _, v := range variants {
		if v.Label == label {
			return true
		}
	}
	return false
}

func hasLineLabel(lines []live.Line, label string) bool {
	for _, l := range lines {
		if l.Label == label {
			return true
		}
	}
	return false
}

// closeDanmaku 断掉当前弹幕连接，并让还在路上的回调失效。
func (a *App) closeDanmaku() { a.media.closeDanmaku() }

func (m *playerMedia) closeDanmaku() {
	m.danmuSeq++
	if m.roomCancel != nil {
		m.roomCancel()
		m.roomCancel = nil
	}
	m.roomCtx = nil
	if m.stopDanmaku != nil {
		m.stopDanmaku()
		m.stopDanmaku = nil
	} else if m.danmaku != nil {
		stream := m.danmaku
		go stream.Close()
	}
	m.danmaku = nil
}

// startDanmaku 使用独立房间 context；切档不取消，退出与切房立即取消。
// win 和 ctx 均由主线程捕获，后台不读取 App 的窗口字段。
func (a *App) startDanmaku(ctx context.Context, seq int, prov live.Provider, roomID string, win *mygo.Window) {
	danmakuer, ok := prov.(live.Danmakuer)
	if !ok || win == nil || ctx.Err() != nil {
		return
	}
	stream, err := danmakuer.Danmaku(ctx, roomID, func(d live.Danmu) {
		runOnMain(win, func() {
			if ctx.Err() == nil {
				a.applyDanmu(seq, d)
			}
		})
	})
	if err != nil {
		if stream != nil {
			stream.Close()
		}
		slog.Warn("弹幕连接失败", "room", roomID, "error", errClass(err))
		runOnMain(win, func() {
			if a.media.danmuSeq == seq {
				a.closeDanmaku()
			}
		})
		return
	}
	if stream != nil {
		a.attachDanmaku(ctx, win, seq, stream)
	}
}

// attachDanmaku 的清理不依赖主线程回调被执行；取消房间即可收掉未接纳连接。
func (a *App) attachDanmaku(ctx context.Context, win *mygo.Window, seq int, stream live.DanmakuStream) {
	stop := cleanupOnCancel(ctx, stream.Close)
	if !runOnMain(win, func() {
		if ctx.Err() != nil || a.media.danmuSeq != seq {
			stop()
			return
		}
		if a.media.stopDanmaku != nil {
			a.media.stopDanmaku()
		}
		a.media.danmaku, a.media.stopDanmaku = stream, stop
	}) {
		stop()
	}
}

// applyMedia 只在异步回调里接纳结果；捕获的窗口和 ctx 不需要后台回读 App。
// 资源清理由各结果的 context 守卫兜底，不能依赖关闭窗口后仍有回调。
func (a *App) applyMedia(sess playbackSession, fn, stale func()) {
	if runOnMain(sess.win, func() {
		if sess.ctx.Err() != nil || a.media.seq != sess.seq {
			if stale != nil {
				stale()
			}
			return
		}
		fn()
	}) {
		return
	}
	if stale != nil {
		stale()
	}
}

// applyDanmu 把一条真实弹幕写进飞行集合和历史。
func (a *App) applyDanmu(seq int, d live.Danmu) {
	if a.media.danmuSeq != seq {
		return
	}
	// 命中屏蔽词的整条丢掉：不上屏，也不写进右侧历史
	if a.danmuBlocked(d.Content) {
		return
	}
	kind := danmuChat
	if d.Kind == live.DanmuSuperChat {
		kind = danmuSuperChat
	}

	line := danmuLine{
		Text:       d.Content,
		Nick:       d.Nickname,
		Level:      d.Level,
		Medal:      d.Medal,
		Platform:   live.Platform(a.current().Platform),
		Kind:       kind,
		Emotes:     d.Emotes,
		Color:      d.Color,
		Price:      d.Price,
		Background: d.Background,
		Born:       a.danmuTime(),
		Dur:        a.DanmuDur,
	}

	// 历史：收到就留，和飞行无关
	a.DanmuLog = append(a.DanmuLog, line)
	if len(a.DanmuLog) > danmuLogMax {
		a.DanmuLog = a.DanmuLog[len(a.DanmuLog)-danmuLogMax:]
	}

	// 画面：只有聊天弹幕上屏，且要开着弹幕、在播放
	if !a.DanmuOn || !a.Playing || kind != danmuChat {
		return
	}
	if len(a.Danmu) >= danmuFlyMax {
		return
	}
	// 轨道在真正上屏时才分配：日志不需要，而且分配要看当前窗口的轨道数。
	line.Track = a.takeDanmuTrack(a.danmuTime(), danmuWidthEstimate(line, a.DanmuFont))
	a.Danmu = append(a.Danmu, line)
}

// syncPlayer 把 UI 上的播放状态和音量推给播放器。
//
// 每帧调用，值没变时直接返回——VideoPlayer 的方法都很便宜，
// 但播放 / 暂停来回切换会打断解码，所以只推变化。
func (a *App) syncPlayer() {
	p := a.media.player
	if p == nil {
		return
	}
	// 已报错的播放器交给回退或重试处理，Play 可能清掉错误并掩盖故障。
	if p.Err() == nil && a.Playing != p.Playing() {
		if a.Playing {
			p.Play()
		} else {
			p.Pause()
		}
	}
	// 音量统一走 SetVolume，0 就是静音。两种 surface 都不必各自处理静音态，
	// 也省掉「静音后恢复要强制重推一次」那套特殊路径。
	// VideoPlayer 没有 Volume() getter，所以自己记一份推过的值来比较。
	if want := float64(a.Volume); !almostEqual(want, a.pushedVolume) {
		p.SetVolume(want)
		a.pushedVolume = want
	}
}

// sameQuality 判断“点选的”和“实际下发的”是不是同一档。
//
// 平台会在标签上追加编码信息（抖音的「原画 · H.265」），直接比字符串会
// 把同一档当成降档；只有确实换成了别的档位才需要提示。
func sameQuality(requested, applied string) bool {
	if strings.EqualFold(requested, applied) {
		return true
	}
	// 允许“前缀 + 编码后缀”这一种差异，其余一律算不同档。
	return strings.HasPrefix(applied, requested+" ·") || strings.HasPrefix(requested, applied+" ·")
}

// noticeDuration 是播放页临时提示的停留时间：足够读完一句话，又不挡住画面。
const noticeDuration = 4 * time.Second

// notifyPlayback 在播放页上显示一条会自动消失的提示。
// 只在主线程调用；不在错误层里，不影响播放状态。
func (a *App) notifyPlayback(msg string) {
	a.media.notice = msg
	a.media.noticeUntil = time.Now().Add(noticeDuration)
	a.markDirty()
	a.invalidateWindows()
}

// noticeActive 报告是否需要画出提示，并按剩余时间安排下一次重绘，
// 保证提示能自己消失而不是靠用户下一次操作。
func (m *playerMedia) noticeActive(now time.Time) bool {
	if m.notice == "" {
		return false
	}
	if !now.Before(m.noticeUntil) {
		m.notice = ""
		return false
	}
	return true
}

// retryStream 是状态覆盖层上「重试 / 立即刷新」的动作。
//
// 弹幕还活着就保留它——画面断了不代表聊天断了。
func (a *App) retryStream() {
	if a.media.roomKey == "" {
		return
	}
	a.startStream(a.media.roomCtx != nil)
}

// togglePlayback 是控制条播放键的入口。
//
// 媒体落到错误态时播放键没有意义：syncPlayer 不会把 Play 推给一个坏掉的播放器
// （Play 会清掉错误、把故障藏起来），所以这里当成显式重试，并让新一轮从「在播」
// 开始。正常状态仍然只是翻一个 UI 意图。
func (a *App) togglePlayback() {
	m := &a.media
	if m.err != "" || (m.player != nil && m.player.Err() != nil) {
		a.Playing = true
		a.markDirty()
		a.retryStream()
		return
	}
	a.Playing = !a.Playing
	a.markDirty()
	a.invalidateWindows()
}
func almostEqual(a, b float64) bool {
	d := a - b
	return d < 0.005 && d > -0.005
}

// ---------------------------------------------------------------- 可替换的边界
//
// 下面这几个变量是起播流程唯一的外部接触面。抽出来只为让「过期回收」「打开
// 失败」「代理回退」「按需档位」能在测试里用假的转封装 / 播放器 / 平台验到；
// 生产实现就是它们右边的那个调用。

// openNative 是播放引擎的接入口；测试里替换。
var openNative = playback.Open

// providerFor 是平台数据入口的查找；测试里替换成桩，避免发真实请求。
var providerFor = live.ProviderFor

// runOnMain 把回调排到窗口主线程。Window.Update 是异步的，返回值只说「排上了」，
// 回调里的判断结果拿不回来——所以接纳和丢弃两侧都必须写进回调。
//
// 窗口已经不在（或还没建）时返回 false，调用方立刻自己清理。抽成变量是为了
// 在没有窗口的测试里换成可控回调队列。
var runOnMain = func(win *mygo.Window, fn func()) bool {
	if win == nil || win.IsDestroyed() {
		return false
	}
	win.Update(fn)
	return true
}

// logStage 记一条起播阶段日志：id 是这一轮的关联 ID，base 是这一轮开始的时刻，
// 毫秒数相对它算。参数必须成对。
func logStage(id string, base time.Time, stage string, args ...any) {
	if id == "" || base.IsZero() {
		return
	}
	args = append([]any{"id", id, "stage", stage, "ms", time.Since(base).Milliseconds()}, args...)
	slog.Info("起播阶段", args...)
}

// errClass 把错误压成一个不带 URL / 请求头 / token 的短标签，日志里只用它。
func errClass(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, live.ErrBlocked):
		return "blocked"
	case errors.Is(err, live.ErrUnsupported):
		return "unsupported"
	}
	var up *live.UpstreamError
	if errors.As(err, &up) {
		return "upstream"
	}
	return "error"
}
