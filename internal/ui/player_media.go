package ui

import (
	"context"
	"log/slog"
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
	relay   *relay.Server
	player  *ui.VideoPlayer
	danmaku live.DanmakuStream
	stream  live.Stream

	// roomKey 是 platform|roomID：换房间时对比它就知道要不要重连
	roomKey string
	// seq 防止旧请求的结果盖掉新房间
	seq int
	// loading 表示取流 / 转封装还在进行
	loading bool
	// err 是取流失败的原因，直接显示在状态覆盖层上
	err string

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

// reset 停掉所有媒体资源。可以在任意时刻调用。
func (m *playerMedia) reset() {
	if m.danmaku != nil {
		m.danmaku.Close()
		m.danmaku = nil
	}
	if m.player != nil {
		m.player.Close()
		m.player = nil
	}
	if m.relay != nil {
		m.relay.Stop()
		m.relay = nil
	}
	m.stream = live.Stream{}
	m.loading = false
	m.err = ""
	m.lastPos, m.lastPosAt = 0, time.Time{}
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
func (a *App) closeMedia() {
	a.media.reset()
	a.media.roomKey = ""
	a.media.seq++
}

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

// startStream 取流 → 转封装 → 播放器 →（按需）弹幕。
//
// keepDanmaku 为 true 时保留已有的弹幕连接：换清晰度、换线路都不该
// 把聊天打断——画面和弹幕是两条独立的路。
func (a *App) startStream(keepDanmaku bool) {
	s := a.current()
	if s.Platform == "" || s.RoomID == "" {
		return
	}
	prov, ok := live.ProviderFor(live.Platform(s.Platform))
	if !ok {
		a.media.loading = false
		a.media.err = "该平台还不支持取流"
		return
	}
	streamer, ok := prov.(live.Streamer)
	if !ok {
		a.media.loading = false
		a.media.err = "该平台还不支持取流"
		return
	}

	// 先把旧画面收掉：两路 ffmpeg 同时拉会抢带宽，播放器也会争硬件解码器
	a.stopPlayback()
	if !keepDanmaku {
		if a.media.danmaku != nil {
			a.media.danmaku.Close()
			a.media.danmaku = nil
		}
		// 换房间时旧房的弹幕一条都不能留：面板是「历史」，混进上一间房
		// 的聊天会让人以为串台了。
		a.DanmuLog = nil
		a.Danmu = nil
		a.panelSeen = 0
		a.panelPending = 0
	}

	a.media.seq++
	seq := a.media.seq
	a.media.loading = true
	a.media.err = ""

	// 界面上存的是线路标签（「主线」「备线1」），取流要的是节点 id。
	line, want := a.lineID(a.Line), a.Quality
	platform, roomID := s.Platform, s.RoomID
	// 窗口在主线程上取一次再交给后台 goroutine：弹幕连接要用它做回主线程
	// 的入口，而 a.Win 本身不是并发安全的读。
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()

		info, err := streamer.Stream(ctx, roomID, line)
		slog.Info("取流返回", "room", roomID, "live", info.Live,
			"variants", len(info.Variants), "lines", len(info.Lines), "error", err)
		if err != nil {
			slog.Warn("取流失败", "platform", platform, "room", roomID, "error", err)
			a.applyMedia(seq, func() {
				a.media.loading = false
				a.media.err = "取流失败：" + err.Error()
			})
			return
		}

		// 房间详情先上屏：标题、头像、是否在播都不依赖转封装
		a.applyMedia(seq, func() {
			a.media.stream = info
			// 档位 / 线路的标签跟着平台走：上一间房选的「蓝光」在这一间
			// 可能根本不存在，不校正就会显示一个菜单里没有的值。
			if len(info.Variants) > 0 && !hasVariantLabel(info.Variants, a.Quality) {
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
		})
		// 弹幕和画面是两条独立的路：连弹幕不该被「取不到播流」门控。
		// mock 模式就是靠这一点验界面的——它故意不给任何档位，
		// 于是这里不起 ffmpeg，但弹幕照样要能到。
		//
		// 必须异步：弹幕要先换 token 再建 WebSocket，网络抖一下就是几秒。
		// 同步调在这里会把下面的起流整段压在后面——用户等的是画面，不是聊天。
		if !keepDanmaku {
			go a.startDanmaku(seq, prov, roomID, win)
		}

		if !info.Live || len(info.Variants) == 0 {
			a.applyMedia(seq, func() { a.media.loading = false })
			return
		}

		// 按当前选中的档位起流；找不到就退到第一档
		a.startPlayback(seq, pickVariant(info.Variants, want))
	}()
}

// startPlayback 起一路播放：ffmpeg 转封装（阻塞，等第一个分片落盘），
// 然后回主线程打开播放器。
//
// 必须在调用方的 goroutine 里同步调用——relay.Start 会卡到转封装就绪。
func (a *App) startPlayback(seq int, upstream live.Variant) {
	srv := relay.New()
	// 这个 ctx 必须活到 relay.Stop()：relay 内部用 exec.CommandContext 起
	// ffmpeg，父 ctx 一取消就把它一起杀了——之前在这里 defer cancel
	// 导致转封装刚就绪 ffmpeg 就被干掉，画面播两秒就卡住。
	// 启动超时由 relay.Start 内部的 15 秒等待兜底。
	localURL, err := srv.Start(context.Background(), upstream.URL, upstream.Headers, upstream.Codec,
		relay.Options{Encoder: relay.ParseEncoderMode(a.RelayEncoder)})
	slog.Info("转封装返回", "label", upstream.Label, "url", localURL, "error", err)
	if err != nil {
		srv.Stop()
		// seq 是否过期交给 applyMedia 判断——这里在后台 goroutine 上，
		// 不能直接读 a.media.seq。
		a.applyMedia(seq, func() {
			a.media.loading = false
			a.media.err = "无法拉取直播流：" + err.Error()
		})
		return
	}

	var openErr error
	a.applyMedia(seq, func() {
		p, err := video.OpenURL(localURL)
		if err != nil {
			openErr = err
			return
		}
		a.media.relay = srv
		a.media.player = p
		slog.Info("播放器已创建", "ready", p.Ready())
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
			p.Pause()
		}
	})
	if openErr != nil {
		srv.Stop()
		a.applyMedia(seq, func() {
			a.media.loading = false
			a.media.err = "播放器打不开这个流：" + openErr.Error()
		})
	}
}

// stopPlayback 收掉 ffmpeg 和播放器，弹幕不动。
func (a *App) stopPlayback() {
	if a.media.player != nil {
		a.media.player.Close()
		a.media.player = nil
	}
	if a.media.relay != nil {
		a.media.relay.Stop()
		a.media.relay = nil
	}
}

// pickQuality 是控制条上选中某个清晰度之后的动作。
//
// 不重新取流：档位表里已经拿到每个档位的地址，直接换上游重开一路更快，
// 而且不会因为重新枚举档位把正在看的画面闪一下。
func (a *App) pickQuality(label string) {
	a.Quality = label
	a.markDirty()

	v, ok := a.media.variantByLabel(label)
	if !ok {
		// 还没拿到档位表（刚进页面就点了一下），退回完整流程
		if a.media.roomKey != "" {
			a.startStream(true)
		}
		return
	}

	a.media.seq++
	seq := a.media.seq
	a.media.loading = true
	a.media.err = ""
	a.stopPlayback()
	go a.startPlayback(seq, v)
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

// startDanmaku 连上弹幕流。取流失败时也要连——聊天和画面是两条独立的路。
//
// win 由调用方在主线程上取好再传进来：这个函数跑在后台 goroutine 上，
// 不能去读 App 的字段。
func (a *App) startDanmaku(seq int, prov live.Provider, roomID string, win *mygo.Window) {
	danmakuer, ok := prov.(live.Danmakuer)
	if !ok {
		return
	}
	if win == nil {
		return
	}
	stream, err := danmakuer.Danmaku(context.Background(), roomID, func(d live.Danmu) {
		// 回调在后台 goroutine 上，必须切回主线程再碰视图数据
		win.Update(func() { a.applyDanmu(seq, d) })
	})
	if err != nil {
		slog.Warn("弹幕连接失败", "room", roomID, "error", err)
		return
	}
	a.applyMedia(seq, func() {
		a.media.danmaku = stream
	})
}

// applyMedia 在主线程上执行一次媒体状态变更；seq 过期就丢弃。
func (a *App) applyMedia(seq int, fn func()) {
	win := a.Win
	if win == nil {
		return
	}
	win.Update(func() {
		if a.media.seq != seq {
			return
		}
		fn()
	})
}

// applyDanmu 把一条真实弹幕写进飞行集合和历史。
func (a *App) applyDanmu(seq int, d live.Danmu) {
	if a.media.seq != seq {
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
	a.startStream(a.media.danmaku != nil)
}

func almostEqual(a, b float64) bool {
	d := a - b
	return d < 0.005 && d > -0.005
}
