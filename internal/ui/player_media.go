package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"

	"alive/internal/ffmpeg"
	"alive/internal/live"
	"alive/internal/playback"
	"alive/internal/relay"
)

// 播放页的媒体层。
//
// 三样东西都挂在 App 上而不是视图里——视图每帧重建，而转封装进程、
// 播放器、弹幕连接都要跨帧活着。
//
//	relay    ffmpeg 把上游流转封装成本地 HLS（Media Foundation 直接拉不了
//	         斗鱼的 FLV、也拉不了带防盗链的 m3u8）
//	player   ui/video 的播放器，走系统媒体框架解码
//	danmaku  一条 WebSocket 弹幕连接
type playerMedia struct {
	relay  relaySession
	player *ui.VideoPlayer
	// nativeTried 记录这一轮已经试过自带解码。系统解不了某个编码时先让自带
	// 解码接手，失败再退回 relay；这个标记保证只试一次，不会来回打转。
	nativeTried bool
	danmaku     live.DanmakuStream
	stream      live.Stream

	// roomKey 是 platform|roomID：换房间时对比它就知道要不要重连
	roomKey string
	// seq 是这一轮起播的代号。每开一轮就 +1，之前派出去的一切结果都变成过期
	// 结果，由接纳回调负责把它们的资源关掉。
	seq int
	// loading 表示取流 / 转封装还在进行
	loading bool
	// err 是取流失败的原因，直接显示在状态覆盖层上
	err string

	// cancel / ctx 是这一轮起播的寿命：换房间、换档位、换线路、离开播放页都会
	// 取消它，后台还在取流或起 ffmpeg 的 goroutine 据此提前收手。
	// 这个 context 没有启动 deadline，贯穿播放；换档或退出时旧 ffmpeg 应随它停止。
	cancel      context.CancelFunc
	ctx         context.Context
	win         *mygo.Window
	stopRelay   func()
	stopDanmaku func()
	roomCtx     context.Context
	roomCancel  context.CancelFunc
	// encoder 是这一轮用的转码器策略，起播前在主线程上取好给后台用。
	encoder relay.EncoderMode
	// id / start 是这一轮的关联 ID 与起点，日志里的毫秒数都相对 start。
	id    string
	start time.Time
	// firstFrame 表示这一轮已经记过「首帧可用」。
	firstFrame   bool
	openedAt     time.Time
	pauseStarted time.Time
	pausedFor    time.Duration
	// upstream 是已经接到播放器上的那一档，代理快路径回退要拿它重开。
	upstream live.Variant
	// proxyAttempt 表示这一轮走的是 HLS 代理快路径；proxyFellBack 表示
	// 已经为这一轮回退过一次——回退只做一次。
	proxyAttempt  bool
	proxyFellBack bool
	// proxyDisabled 在当前房间回退后保留，切档、换线和重试不再重复试错。
	proxyDisabled bool

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

// proxyHealthPollInterval 是首帧之后所有活跃播放器的健康检查间隔。
//
// Media Foundation 停帧后不会主动通知错误，只靠用户操作触发帧会漏掉它；
// 代理路径和普通转封装都按这个间隔看一眼。
const proxyHealthPollInterval = 250 * time.Millisecond

// componentPreparingMessage 是组件安装期间的提示；自动重试据此判断这一轮
// 是否仍停在同一原因上，避免把用户后来的其它错误误当成可重试。
const componentPreparingMessage = "正在准备播放组件，稍后点重试"

// componentRetryWindow 是等组件装完的上限；超过就不再自动重试，
// 用户仍然可以用界面上的重试按钮。
const componentRetryWindow = 10 * time.Minute

// proxyFirstFrameWait 是代理路径等首帧的上限：超过它还没出图就换路。
//
// 取 3 秒是因为代理的正常区间是 1.1~2.0 秒，而它的尾部可以到 9 秒——本机实测过
// 同一房间 1082 / 1245 / 1423 / 2047 / 9199ms。那个尾部不是网络，是 MF 的 HLS
// source 在等自己的起播水位：B 站这类房间的直播窗口只有 6 个 1 秒分片，它的等待
// 被放大成整窗口。网页用 hls.js + MSE，一片到就解就放，所以没有这个问题。
// 3 秒足够覆盖正常区间，超出的那部分交给自带解码（850~2644ms）来兜。
//
// 从播放器打开后计算，暂停时间不计入，避免网络抖动时过早重启。
const proxyFirstFrameWait = 3 * time.Second

// firstFrameBudget 是所有路径的首帧上限：代理回退、普通转封装、兼容转码都算。
//
// relay.Start 返回成功只代表本地 playlist 和首个分片可读，不代表播放器能建
// 出解码器交帧。没有这条兜底，remux / 转码路径会永远停在「正在拉取直播流…」。
// 比代理预算宽，因为它背后没有代理预检那层过滤，慢一点是正常的。
const firstFrameBudget = 20 * time.Second

// playbackSession 是一轮起播的凭据：媒体代号、寿命 ctx、日志关联 ID 与起点。
//
// 后台 goroutine 只拿它，不回读 App 字段——那些字段只属于主线程。
type playbackSession struct {
	seq     int
	win     *mygo.Window
	ctx     context.Context
	id      string
	start   time.Time
	encoder relay.EncoderMode
	// proxy 表示这一路允许尝试 HLS 代理快路径。它在主线程上定下来，后台 goroutine
	// 就不用回读媒体层字段。
	proxy bool
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
	m.proxyAttempt = false
	m.proxyFellBack = false
	m.proxyDisabled = false
	m.nativeTried = false
	m.notice = ""
	m.noticeUntil = time.Time{}
	m.pendingQualityPick = ""
	m.upstream = live.Variant{}
	m.lastPos, m.lastPosAt = 0, time.Time{}
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

// mode 是本轮的实际呈现方式：自带解码记 native，其余看 relay 的传输模式。
//
// native 路径没有 relay，直接拿 relayMode(nil) 会记成 starting，
// 日志里就看不出这条路到底走的是哪儿。
func (m *playerMedia) mode() string {
	if m.player != nil && m.relay == nil {
		return "native"
	}
	return relayMode(m.relay)
}

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

// closeMedia 是离开播放页时的清理入口。
//
// 先把代号推进（reset 里的 cancelSession 会做），再清 roomKey：正在路上的
// 取流结果回来时已经找不到对应的那一轮，只会被丢弃并关掉自己的资源。
func (a *App) closeMedia() {
	a.media.reset()
	a.media.roomKey = ""
}

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
	m.encoder = relay.ParseEncoderMode(a.RelayEncoder)
	m.firstFrame = false
	m.openedAt = time.Time{}
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	m.upstream = live.Variant{}
	m.proxyAttempt = false
	m.proxyFellBack = false
	m.loading = true
	m.err = ""

	sess := playbackSession{seq: m.seq, ctx: ctx, id: m.id, start: m.start, encoder: m.encoder, proxy: hlsProxyEnabled() && !m.proxyDisabled, win: m.win}
	logStage(sess.id, sess.start, "begin", "proxy_requested", sess.proxy)
	return sess
}

// session 打包当前这一轮的凭据，交给后台 goroutine 用。
//
// 后台不能回读 App 字段（那些只属于主线程），所以这里一次拷全。
func (a *App) session() playbackSession {
	m := &a.media
	return playbackSession{seq: m.seq, ctx: m.ctx, id: m.id, start: m.start, encoder: m.encoder, proxy: hlsProxyEnabled() && !m.proxyDisabled, win: m.win}
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
		a.startPlayback(sess, upstream, sess.proxy)
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

// startPlayback 在后台启动转封装，再把播放器创建排到主线程。
// sess.ctx 无启动 deadline，直到切档或退出才取消；取流预算不会杀死已起播的 ffmpeg。
func (a *App) startPlayback(sess playbackSession, upstream live.Variant, useProxy bool) {
	// 一律走自带解码。
	//
	// 目标不是「中位数最快」，是「点开就看」，也就是最差情况可控。代理路径把呈现交给
	// MF 的 HLS source，它自己决定何时起播：本机实测中位数 1.4 秒但会跳 9.2 秒，而它
	// 为什么跳我们改不了（`ui.VideoPlayer` 只有只读的 Buffering()）。
	// 自带解码最差 2.6 秒，现在把探测窗口的等待去掉后能压到 1 秒级。
	//
	// 失败在后台回退到 relay 的兼容路径，只试一次。
	useNative := true
	if v, ok := nativeOverride(); ok {
		useNative = v
	}
	if useNative && !a.media.nativeTried {
		a.media.nativeTried = true
		if a.startNativePlayback(sess, upstream, useProxy) {
			return
		}
	}
	srv := newRelaySession()
	localURL, err := srv.Start(sess.ctx, upstream.URL, upstream.Headers, upstream.Codec,
		relayOptions(sess.encoder, useProxy))
	mode := relayMode(srv)
	if err != nil || sess.ctx.Err() != nil {
		srv.Stop()
		if sess.ctx.Err() != nil {
			logStage(sess.id, sess.start, "relay-canceled", "mode", mode)
			return
		}
		logStage(sess.id, sess.start, "relay-failed", "mode", mode, "error", errClass(err))
		// 组件正在后台安装时说清原因，别让用户以为直播流坏了。
		preparing := errors.Is(err, ffmpeg.ErrPreparing)
		if preparing {
			a.retryWhenComponentsReady(sess)
		}
		a.applyMedia(sess, func() {
			a.media.loading = false
			if preparing {
				a.media.err = componentPreparingMessage
				return
			}
			a.media.err = "无法拉取直播流，请重试或切换线路"
		}, nil)
		return
	}
	// Start 完成后才注册 Stop，避免 Stop 早于 Start 接管资源；即使窗口不再执行
	// 回调，取消会话也会释放服务。接纳后这个守卫仍覆盖整个播放寿命。
	stop := cleanupOnCancel(sess.ctx, srv.Stop)
	logStage(sess.id, sess.start, "relay", "mode", mode, "quality", upstream.Label)
	a.applyMedia(sess, func() {
		a.media.proxyAttempt = mode == "hls-proxy"
		p, err := openVideo(localURL)
		if err != nil {
			if p != nil {
				p.Close()
			}
			stop()
			logStage(sess.id, sess.start, "open-failed", "mode", mode, "error", errClass(err))
			if a.fallbackToRemux(upstream) {
				return
			}
			a.media.loading = false
			a.media.err = "播放器打不开这个流，请重试或切换线路"
			return
		}
		a.adoptPlayback(sess, srv, p, upstream, stop)
	}, func() {
		stop()
		logStage(sess.id, sess.start, "playback-stale", "mode", mode)
	})
}

// startNativePlayback 用自带解码接手这一路。
//
// 返回 false 表示当前环境起不来（没有窗口），调用方继续走 relay。
// 成功后的接纳照旧走 applyMedia：过期结果自己关掉，不让旧会话的画面盖上来。
func (a *App) startNativePlayback(sess playbackSession, upstream live.Variant, useProxy bool) bool {
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
					// 自带解码不适配这一路，退回 relay 的兼容路径。
					a.startPlayback(sess, upstream, useProxy)
				}, nil)
			})
			return
		}
		runOnMain(win, func() {
			a.applyMedia(sess, func() {
				// 引擎交给播放器：之后每帧由 MyGo 问 Frame(t)，我们不再自己推帧。
				// 关播放器就关引擎，所以这里不必再挂一个 stop。
				p := ui.NewEngineVideoPlayer(e)
				a.adoptPlayback(sess, nil, p, upstream, nil)
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

// adoptPlayback 把已经就绪的转封装和播放器接到媒体层上。
//
// 只在主线程、且代号已被采纳时调用。
func (a *App) adoptPlayback(sess playbackSession, srv relaySession, p *ui.VideoPlayer, upstream live.Variant, stop func()) {
	a.media.stopRelay = stop
	a.media.openedAt = time.Now()
	a.media.pauseStarted = time.Time{}
	a.media.pausedFor = 0
	a.media.relay = srv
	a.media.player = p
	a.media.upstream = upstream
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
	mode := relayMode(srv)
	if srv == nil {
		mode = "native"
	}
	logStage(sess.id, sess.start, "open", "mode", mode, "quality", upstream.Label,
		"ready", p.Ready(), "declared_width", upstream.Width, "declared_height", upstream.Height)
}

// stopPlayback 收掉 ffmpeg 和播放器，弹幕不动。
func (a *App) stopPlayback() { a.media.stopPlayback() }

func (m *playerMedia) stopPlayback() {
	if m.player != nil {
		m.player.Close()
		m.player = nil
	}
	if m.stopRelay != nil {
		m.stopRelay()
		m.stopRelay = nil
	} else if m.relay != nil {
		srv := m.relay
		go srv.Stop()
	}
	m.relay = nil
	m.lastPos, m.lastPosAt = 0, time.Time{}
}

// failPlayback 把这一轮落到可重试的终态。
//
// 顺序是有意的：先读实际传输方式（stopPlayback 会清掉 relay 字段，之后再读
// 只剩 starting），再收掉播放器和转封装——留着坏播放器会让错误层盖在一块还
// 在解码的黑画面上，留着 ffmpeg 只会白占带宽。房间 ctx 和弹幕不动：画面断了
// 不代表聊天断了，也不该因为一次播放失败触发整房间重连。
func (a *App) failPlayback(class, msg string) {
	m := &a.media
	logStage(m.id, m.start, class, "mode", m.mode())
	a.stopPlayback()
	m.loading = false
	m.err = msg
}

// fallbackToRemux 在代理快路径失败时，对当前这一轮退回普通转封装一次。
//
// 只在主线程调用。打开失败、首帧超时或播放中明确报错都可触发；回退后
// 当前房间不再尝试代理，切档和换线也不重复付出等待成本。返回 true 表示
// 已经安排了回退，调用方不要再报错。
func (a *App) fallbackToRemux(upstream live.Variant) bool {
	m := &a.media
	if !m.proxyAttempt || m.proxyFellBack || m.ctx == nil || m.ctx.Err() != nil {
		return false
	}
	m.proxyFellBack = true
	m.proxyDisabled = true
	m.proxyAttempt = false
	m.firstFrame = false
	m.loading = true
	m.err = ""
	a.stopPlayback()
	sess := a.session()
	logStage(sess.id, sess.start, "proxy-fallback", "quality", upstream.Label)
	// 先换自带解码：代理晚出来的那几秒，多半是 MF 在等它自己的起播水位，
	// 换一条完全不经过 MF 的路，解到第一帧就上屏。native 也起不来时才退回转封装。
	if !m.nativeTried && m.win != nil {
		m.nativeTried = true
		if a.startNativePlayback(sess, upstream, false) {
			return true
		}
	}
	go a.startPlayback(sess, upstream, false)
	return true
}

// trackFirstFrame 记录首帧，并负责两条兜底：代理路径的八秒回退，以及所有
// 路径的首帧上限。播放器的明确错误也在这里落到可重试终态。
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
	if !m.firstFrame {
		m.observeStartupPause(a.Playing, now)
	}
	// 播放器报错时 Ready 仍可能为真，这个判断必须排在记录首帧之前。
	// 代理路径先回退一次；其余路径没有别的兜底，直接收掉坏资源。
	if err := p.Err(); err != nil {
		// 暂停中不重启也不清理：用户主动停着，恢复播放那一帧会再进来处理。
		// 和代理回退同一套语义，避免暂停时突然重开一路流。
		if !a.Playing {
			return
		}
		if m.upstream.URL != "" && a.fallbackToRemux(m.upstream) {
			return
		}
		a.failPlayback("playback-failed", "播放失败，请重试或切换线路")
		return
	}
	if m.firstFrame || p.Ready() {
		if !m.firstFrame {
			m.firstFrame = true
			w, h := p.Size()
			logStage(m.id, m.start, "首帧可用", "mode", m.mode(), "upstream_codec", m.upstream.Codec,
				"declared_width", m.upstream.Width, "declared_height", m.upstream.Height,
				"declared_bitrate", m.upstream.Bitrate, "declared_fps", m.upstream.FrameRate,
				"decoded_width", w, "decoded_height", h, "buffering", p.Buffering())
		}
		return
	}
	// 代理路径的预算更短：八秒还不出来就退回普通转封装，只退一次。
	if m.needProxyFallback(nil, now, a.Playing) {
		if m.upstream.URL != "" && a.fallbackToRemux(m.upstream) {
			return
		}
	}
	// 所有路径的首帧上限。Ready 已经在上面的分支里优先，所以这里只剩
	// 「确实还没出图」的情况。
	if m.needFirstFrameTimeout(now, a.Playing) {
		a.failPlayback("first-frame-timeout", "直播流长时间没有画面，请重试或切换线路")
	}
}

// needProxyFallback 判断代理路径是否因播放错误或首帧超时而回退。
//
// 播放器明确报错时不受首帧状态限制；八秒预算只约束尚未出图的阶段。
// 纯判断，不碰播放器状态，测试可以直接喂时间。
func (m *playerMedia) needProxyFallback(err error, now time.Time, playing bool) bool {
	if !playing || !m.proxyAttempt || m.proxyFellBack || m.openedAt.IsZero() {
		return false
	}
	if err != nil {
		return true
	}
	return !m.firstFrame && m.startupElapsed(now) >= proxyFirstFrameWait
}

// needFirstFrameTimeout 报告这一轮是否已经超出首帧预算。
//
// 只约束「确实还没出图」的情况：Ready 由调用方先判，已出图和暂停都不算。
// 所有路径共用这一个预算——relay 只保证首片落盘，不代表播放器能建解码器出图，
// 没有它，remux / 转码路径会永远停在「正在拉取直播流…」。
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
		go a.startPlayback(sess, v, sess.proxy)
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
		Kind:       kind,
		Emotes:     d.Emotes,
		Price:      d.Price,
		Background: d.Background,
		Born:       time.Now(),
		Dur:        a.DanmuDur,
	}
	// 真实平台不用 Track 固定也能接受：飞行层只按 Track 取模排轨道
	line.Track = a.danmuSeq % 14
	a.danmuSeq++

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

// retryWhenComponentsReady 在组件装好后自动重试一次当前房间。
//
// 只在“确实还缺组件”时挂等待，避免装好之后空转；重试前再核对房间和错误文案，
// 用户已经切房或手动重试过就不再插一手。
func (a *App) retryWhenComponentsReady(sess playbackSession) {
	win := a.media.win
	key := a.media.roomKey
	if win == nil || ffmpeg.Available() {
		return
	}
	go func() {
		select {
		case <-ffmpeg.ProvisionDone():
		case <-time.After(componentRetryWindow):
			return
		case <-sess.ctx.Done():
			return
		}
		if !ffmpeg.Available() {
			return
		}
		runOnMain(win, func() {
			if a.media.roomKey != key || a.media.err != componentPreparingMessage {
				return
			}
			a.retryStream()
		})
	}()
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

// relaySession 是一次转封装会话，生产上就是 *relay.Server。
type relaySession interface {
	Start(ctx context.Context, upstream string, headers []string, codec string, opts relay.Options) (string, error)
	Mode() string
	Stop()
}

// newRelaySession 创建一路转封装；测试里替换。
var newRelaySession = func() relaySession { return relay.New() }

// openVideo 是播放器接入口；测试里替换。
var openVideo = video.OpenURL

// openNative 是自带解码的接入口；测试里替换。
var openNative = playback.Open

// relayOptions 组装转封装选项。
//
// useProxy 允许 relay 按编码、系统解码能力和列表格式尝试代理；
// 不支持时仍由 relay 选择转封装或兼容转码。
var relayOptions = func(encoder relay.EncoderMode, useProxy bool) relay.Options {
	return relay.Options{Encoder: encoder, HLSProxy: useProxy}
}

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

// envHLSProxy 覆盖当前进程的代理策略，便于诊断与回退，不落盘、不进界面。
const envHLSProxy = "ALIVE_HLS_PROXY"

// hlsProxyEnabled 不按平台限制代理；编码、封装与列表格式由 relay 预检。
//
// 默认允许尝试，显式 0 可恢复转封装；未知非空值保守关闭。
func hlsProxyEnabled() bool {
	switch os.Getenv(envHLSProxy) {
	case "", "1":
		return true
	default:
		return false
	}
}

// relayMode 返回这一路的实际传输方式（hls-proxy / remux / transcode）。
//
// 还没起来时 Mode() 是空串，日志里记成 starting。
func relayMode(srv relaySession) string {
	if srv == nil {
		return "starting"
	}
	if m := srv.Mode(); m != "" {
		return m
	}
	return "starting"
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

// nativeOverride 是验收与排障用的强制开关：ALIVE_NATIVE=1 强制走自带解码，
// =0 强制不走，不设置时按系统解码能力判断。
//
// 本机装了 HEVC 解码器，自动模式下 native 永远不会被选中——这条路径需要
// 一个显式开关才验得到。
func nativeOverride() (value, set bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALIVE_NATIVE"))) {
	case "1", "true", "on":
		return true, true
	case "0", "false", "off":
		return false, true
	}
	return false, false
}

// isFLVStream 判断这条地址是不是 HTTP-FLV。
//
// 只看路径后缀：签名参数和 fragment 不影响容器判断。
func isFLVStream(raw string) bool {
	path := raw
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	return strings.HasSuffix(strings.ToLower(path), ".flv")
}
