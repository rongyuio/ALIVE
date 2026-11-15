package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"

	"alive/internal/live"
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
	relay   relaySession
	player  *ui.VideoPlayer
	danmaku live.DanmakuStream
	stream  live.Stream

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
	// upstream 是已经接到播放器上的那一档，实验快路径回退要拿它重开。
	upstream live.Variant
	// proxyAttempt 表示这一轮走的是 HLS 代理实验快路径；proxyFellBack 表示
	// 已经为这一轮回退过一次——回退只做一次。
	proxyAttempt  bool
	proxyFellBack bool

	// danmuSeq 单独管弹幕连接。换清晰度不该让同一房间的聊天断掉，也不能因为
	// 画面那一路的代号变了，就把刚连上的弹幕当成过期结果丢掉。
	danmuSeq int

	// 画面推进的观测点。不能直接信 VideoPlayer.Buffering()：Media
	// Foundation 在直播 HLS 上每一次 playlist 滚动都会发一次 WAITING，而把
	// 状态清回 Ready 靠的是 PLAYING / CANPLAY 事件——直播流上这两个不会再
	// 来第二次，状态就永久停在 Buffering，左下角胶囊跟着常驻。实测 2K
	// 直播：第 6 秒进入 Buffering 后 174 秒没退出，期间 Position 一直以
	// 1x 推进、画面正常。所以这里自己看时间轴有没有在动。
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

// proxyFirstFrameWait 是实验快路径等首帧的上限：超过它还没出图就退回普通转封装。
//
// 8 秒是诊断实验的等待预算，从播放器打开后计算，暂停时间不计入。
const proxyFirstFrameWait = 8 * time.Second

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
	// proxy 表示这一路走 HLS 代理实验快路径。它在主线程上定下来，后台 goroutine
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

	sess := playbackSession{seq: m.seq, ctx: ctx, id: m.id, start: m.start, encoder: m.encoder, proxy: hlsProxyEnabled(), win: m.win}
	logStage(sess.id, sess.start, "begin", "proxy_requested", sess.proxy)
	return sess
}

// session 打包当前这一轮的凭据，交给后台 goroutine 用。
//
// 后台不能回读 App 字段（那些只属于主线程），所以这里一次拷全。
func (a *App) session() playbackSession {
	m := &a.media
	return playbackSession{seq: m.seq, ctx: m.ctx, id: m.id, start: m.start, encoder: m.encoder, proxy: hlsProxyEnabled(), win: m.win}
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
		}, nil)

		if !info.Live || len(info.Variants) == 0 {
			a.applyMedia(sess, func() { a.media.loading = false }, nil)
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
		a.applyMedia(sess, func() {
			a.media.loading = false
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
	if volume <= 0 {
		p.SetMuted(true)
	} else {
		p.SetVolume(volume)
	}
	a.pushedVolume = volume
	if !a.Playing {
		a.media.pauseStarted = a.media.openedAt
		p.Pause()
	}
	logStage(sess.id, sess.start, "open", "mode", relayMode(srv), "quality", upstream.Label,
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

// fallbackToRemux 在实验快路径失败时，对当前这一轮退回普通转封装一次。
//
// 只在主线程调用。两个失败场景都进来：打开失败（这时还没接管播放器）、
// 以及迟迟等不到首帧（这时旧的一路要先收掉）。返回 true 表示已经安排了
// 回退，调用方不要再报错。
func (a *App) fallbackToRemux(upstream live.Variant) bool {
	m := &a.media
	if !m.proxyAttempt || m.proxyFellBack || m.ctx == nil || m.ctx.Err() != nil {
		return false
	}
	m.proxyFellBack = true
	m.proxyAttempt = false
	m.firstFrame = false
	m.loading = true
	m.err = ""
	a.stopPlayback()
	sess := a.session()
	logStage(sess.id, sess.start, "proxy-fallback", "quality", upstream.Label)
	go a.startPlayback(sess, upstream, false)
	return true
}

// trackFirstFrame 记下首帧可用的耗时，并在实验快路径迟迟不出图时回退一次。
//
// 每帧调用。Ready 只说明解码器交出了第一帧，不等于它在屏幕上呈现的时刻；
// 日志按「首帧可用」标注，别当成精确的观感时间。
func (a *App) trackFirstFrame() {
	m := &a.media
	p := m.player
	if p == nil || m.firstFrame {
		return
	}
	if p.Ready() {
		m.firstFrame = true
		w, h := p.Size()
		logStage(m.id, m.start, "首帧可用", "mode", relayMode(m.relay), "upstream_codec", m.upstream.Codec,
			"declared_width", m.upstream.Width, "declared_height", m.upstream.Height,
			"declared_bitrate", m.upstream.Bitrate, "declared_fps", m.upstream.FrameRate,
			"decoded_width", w, "decoded_height", h, "buffering", p.Buffering())
		return
	}
	now := time.Now()
	m.observeStartupPause(a.Playing, now)
	if !m.needProxyFallback(p.Err(), now, a.Playing) {
		return
	}
	upstream := m.upstream
	if upstream.URL == "" {
		return
	}
	a.fallbackToRemux(upstream)
}

// needProxyFallback 判断这一轮要不要因为「迟迟没有首帧」而退回普通转封装。
//
// 两个触发条件：播放器已经报错，或者超过 proxyFirstFrameWait 还没 Ready。
// 纯判断，不碰播放器状态，测试可以直接喂时间。
func (m *playerMedia) needProxyFallback(err error, now time.Time, playing bool) bool {
	if !playing || m.firstFrame || !m.proxyAttempt || m.proxyFellBack || m.openedAt.IsZero() {
		return false
	}
	if err != nil {
		return true
	}
	return now.Sub(m.openedAt)-m.pausedFor >= proxyFirstFrameWait
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

// pickQuality 是控制条上选中某个清晰度之后的动作。
//
// 菜单里这一档已经带地址时直接换上游重开一路：不用重新取流，也不会因为
// 重新枚举档位把正在看的画面闪一下。地址还没解析（PlaybackStreamer 只解析
// 当前档）时走取流流程。两种情况都保留弹幕连接。
func (a *App) pickQuality(label string) {
	a.Quality = label
	a.markDirty()
	if a.media.roomKey == "" {
		return
	}

	v, ok := a.media.variantByLabel(label)
	if ok && v.URL != "" {
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
	if a.Playing != p.Playing() {
		if a.Playing {
			p.Play()
		} else {
			p.Pause()
		}
	}
	// 音量 0 用 SetMuted，避免每次拖动都触发一次解码器重配。
	// VideoPlayer 没有 Volume() getter，所以自己记一份推过的值来比较。
	if a.Volume <= 0 {
		if !p.Muted() {
			p.SetMuted(true)
		}
		return
	}
	if p.Muted() {
		p.SetMuted(false)
		a.pushedVolume = -1 // 强制重新推一次
	}
	if want := float64(a.Volume); !almostEqual(want, a.pushedVolume) {
		p.SetVolume(want)
		a.pushedVolume = want
	}
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

// relayOptions 组装转封装选项。
//
// useProxy 是 HLS 代理实验快路径的意图，默认关闭：relay 只在「codec 明确是
// H.264 且上游是 HLS」时才真的走代理，其它情况内部自己回退到转封装。
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

// envHLSProxy 是 HLS 代理快路径的实验开关。只在诊断时用，不落盘、不进界面。
const envHLSProxy = "ALIVE_HLS_PROXY"

// hlsProxyEnabled 读实验开关。
//
// 默认关闭，只用于诊断：不落盘、不进界面。
func hlsProxyEnabled() bool {
	return os.Getenv(envHLSProxy) == "1"
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
