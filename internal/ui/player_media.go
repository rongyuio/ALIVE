package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog"

	"alive/internal/diag"
	"alive/internal/live"
	"alive/internal/playback"
)

// 播放页的媒体层：焦点会话的取流、引擎与弹幕连接。
//
// 它不存房间身份。房间只有一个真相：App.focus。这里的 session() / roomKey()
// 都是读焦点，不能写——写入口只有 setFocus。
type playerMedia struct {
	// app 只用来读焦点（a.focus）。媒体层不再自己存一份房间身份：
	// 焦点是唯一的真相，会话住在 a.watch.sessions。
	app *App

	danmaku live.DanmakuStream

	// seq 是这一轮起播的代号。每开一轮就 +1，之前派出去的一切结果都变成过期
	// 结果，由接纳回调负责把它们的资源关掉。
	seq int
	// cancel / ctx 是这一轮起播的寿命：换房间、换档位、换线路、离开播放页都会
	// 取消它，后台还在取流或起 ffmpeg 的 goroutine 据此提前收手。
	// 这个 context 没有启动 deadline，贯穿播放；换档或退出时旧 ffmpeg 应随它停止。
	cancel      context.CancelFunc
	ctx         context.Context
	win         *mygo.Window
	stopDanmaku func()
	roomCtx     context.Context
	roomCancel  context.CancelFunc
	// id / start 是这一轮的关联 ID 与起点；log 把 id、房间和 logger 绑在一起。
	// 日志里的毫秒数都相对 log.start。
	id           string
	start        time.Time
	log          logRef
	pauseStarted time.Time
	pausedFor    time.Duration
	// danmuSeq 单独管弹幕连接。换清晰度不该让同一房间的聊天断掉，也不能因为
	// 画面那一路的代号变了，就把刚连上的弹幕当成过期结果丢掉。
	danmuSeq int
	// danmuRetryAt 是弹幕连接失败后的退避点。ensureDanmaku 每帧都会问，
	// 没有它会在平台拒绝时每帧重连。
	danmuRetryAt time.Time
	// danmuLastFail 是最近一次弹幕连接失败的时刻。下一次连上时用它证明恢复；
	// 只由主线程写。
	danmuLastFail time.Time
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
	// bufferStallSince 是「一直在缓冲、时间轴又不动」的起点。
	//
	// FLV 的静默断流（连接还在、数据不来）只表现为 paused-for-cache：
	// mpv 不 ended、不报错，本地实测 150 秒仍是 playing。没有这条上限，
	// 播放页就会永远停在最后一帧。
	bufferStallSince time.Time
	// refreshing 表示已经派出去一次热切换，避免每帧重复派。
	refreshing bool
	// lastSwapAt / swapFailures 用来退避：连续失败时不要每帧都重开一路。
	lastSwapAt   time.Time
	swapFailures int

	// 连续性日志的观测点：缓冲起止、音频停走。只在状态翻转时写一条，
	// 正常播放不产生日志。
	buffering      bool
	bufferingSince time.Time
	diagAt         time.Time
	diagPos        time.Duration
	diagAudio      time.Duration
	diagFrames     uint64
	diagCalls      uint64
	// mpvLogSeq 是已转进应用日志的 mpv 日志序号，避免同一条重复写。
	// mpvLogFor 记这个游标属于哪个播放器：换引擎后序号从 1 重新开始，
	// 不跟着换就会静默丢掉新引擎的所有 mpv 日志。
	mpvLogSeq    uint64
	mpvLogFor    *ui.VideoPlayer
	audioStalled bool
	videoStalled bool
	// lastFail 是最近一次落到错误终态的时刻。下一次出首帧时用它证明「恢复了」，
	// 恢复不是靠 err 字段是否为空猜的（adopt 时 err 已经被清掉）。
	lastFail time.Time
	// detachedAt 是进入后台收听的时刻，日志用。
	detachedAt time.Time
	// tickPending 表示后台收听的定时心跳已经排上，避免每帧重复排。
	tickPending bool
}

// session 是焦点那一间；nil 表示当前没有房间。
//
// 它是方法而不是字段：焦点只有一个写者（App.setFocus），媒体层不能自己
// 存第二份。以前的 m.session / m.roomKey 字段就是「同一间房两个所有者」的来源。
func (m *playerMedia) session() *roomSession {
	return m.app.focus
}

// roomKey 是焦点那一间的 platform|roomID；没有焦点时为空。
func (m *playerMedia) roomKey() string { return m.app.focusKey() }

// 下面这些是这一间房的运行态，住在会话表的 roomSession 里。
// 播放页只是它的一个视图，所以这里转发——只有主线程能读写。
// 房间级状态的转发。nil session 时读零值、写丢弃——没有房间就没有这些状态。
func (m *playerMedia) firstFrame() bool {
	return m.session() != nil && m.session().firstFrame
}
func (m *playerMedia) setFirstFrame(v bool) {
	if m.session() != nil {
		m.session().firstFrame = v
	}
}
func (m *playerMedia) opened() time.Time {
	if m.session() == nil {
		return time.Time{}
	}
	return m.session().openedAt
}
func (m *playerMedia) setOpened(t time.Time) {
	if m.session() != nil {
		m.session().openedAt = t
	}
}
func (m *playerMedia) refresh() time.Time {
	if m.session() == nil {
		return time.Time{}
	}
	return m.session().refreshAt
}
func (m *playerMedia) setRefresh(t time.Time) {
	if m.session() != nil {
		m.session().refreshAt = t
	}
}
func (m *playerMedia) isLoading() bool {
	return m.session() != nil && m.session().loading
}
func (m *playerMedia) setLoading(v bool) {
	if m.session() != nil {
		m.session().loading = v
	}
}
func (m *playerMedia) errText() string {
	if m.session() == nil {
		return ""
	}
	return m.session().err
}
func (m *playerMedia) setErr(v string) {
	if m.session() != nil {
		m.session().err = v
	}
}

func (m *playerMedia) eng() *ui.VideoPlayer {
	if m.session() == nil {
		return nil
	}
	return m.session().player
}
func (m *playerMedia) streamNow() live.Stream {
	if m.session() == nil {
		return live.Stream{}
	}
	return m.session().stream
}
func (m *playerMedia) up() live.Variant {
	if m.session() == nil {
		return live.Variant{}
	}
	return m.session().up
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
	log   logRef
}

// expired 报告这一轮是否已经作废，后台回调用它判断「我还是当前这一轮吗」。
//
// ctx 为 nil 表示这一轮根本不存在：cancelSession 之后、下一轮 beginSession
// 之前。这个窗口里焦点会话的播放器还活着（在墙里静音预览，或者一次焦点交接
// 还没走完），mediaTick 照样会看到「播放器报告结束」并派热切换。所以不能只问
// ctx.Err()——对 nil 接口调方法就是 0xc0000005（生产崩溃的现场）。
func (s playbackSession) expired() bool {
	return s.ctx == nil || s.ctx.Err() != nil
}

// cancelSession 结束当前这一轮起播。
//
// ctx 取消后，后台取流与起 ffmpeg 的 goroutine 会自行收手；已经就绪但还没
// 交出去的那一路，会在接纳回调里被判过期后关掉自己。
//
// **同时清掉热切换的在途闸门。** 闸门一端立起来、另一端只在对得上号的
// 回包里放下（runHotSwap 的 fail 先比 seq 再清 refreshing）。这一轮已经
// 作废时，那个回包会走「过期」分支直接 return——闸门就永远立着，此后
// scheduleSwap 每次都在第一行退出：地址续期和断流重连一起失效，画面停住，
// 只有用户手点重试（走 beginSession → stopPlayback）才解得开。
func (m *playerMedia) cancelSession() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.ctx = nil
	m.seq++
	m.refreshing = false
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

// mediaTick 是媒体层的常驻心跳：首帧观测、地址续期、断流重连、错误终态。
//
// 每帧调用，判据是「有没有播放器」而不是「在不在播放页」。原来它挂在
// PlayerPage 上，离开播放页就没人跑了——后台收听时地址到期（斗鱼 300 秒）
// 就没人续，声音五分钟后自己断；窗口最小化时视图不重建，同样会断。
//
// 这一帧的驱动靠调用方（Shell）在必要时 c.After 续上；background 态下
// 视图里没有视频元素在请求帧，所以还额外挂一条定时器，见 kickMediaTick。
func (a *App) mediaTick() {
	a.watchTick(time.Now())
	if a.media.eng() == nil {
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
	if a.Win == nil {
		m.tickPending = false
		return
	}
	// 一条链就够：mediaTick 先 watchTick（墙）再 trackFirstFrame（焦点）。
	// 以前后台收听和墙各排一条、回调还一模一样，两个条件同时成立时 watchTick
	// 的节拍会翻倍；stopPlayback 清标记又不取消已排的定时器，还会多留一条。
	need := (m.app.inBackground() && m.eng() != nil) || len(a.watch.sessions) > 0
	if need && !m.tickPending {
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
}

// CloseMedia 在窗口关闭前结束焦点并释放所有房间会话。
//
// 运行时的关闭语义在 surface_motion.go 的 closeFocus：焦点结束，会话存亡
// 由墙决定。这里是进程退出前的清场，墙里的会话也一起关。
func (a *App) CloseMedia() {
	a.setFocus(nil)
	for _, s := range a.watch.sessions {
		a.closeSession(s, "media-close")
	}
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
	// SetPresentSize 把呈现尺寸固定为 w×h；0 表示按视频原始尺寸。
	//
	// 这是呈现的**唯一**旋钮：格子预览、后台收听、播放页全尺寸都走它。
	// 以前还有一个 SetPresenting(on) 表示「全尺寸 / 160×90」，两套并存，
	// 各自被不同路径调用，墙 → 播放页那条路就永远停在格子分辨率上。
	SetPresentSize(w, h int)
}

// presentSize 是「这一路该呈现多大」。
//
// 零值表示按视频原始尺寸。所有会改变呈现的时机（开流、接管、进出播放页、
// 后台收听、热切换）都只调 presentSizeFor 求一次，再交给 applyPresent。
// 判断只写在一处，所以不会出现「这条路径忘了放开」。
type presentSize struct{ W, H int }

// presentSizeForSession 求某一间该呈现多大。
//
// 同一间房在墙格和播放页之间切换时，这里就是唯一改变的东西：0,0 与
// 格子像素之间换一下。引擎、流、连接一概不动。
func (a *App) presentSizeForSession(s *roomSession) presentSize {
	if s == nil {
		return presentSize{}
	}
	// 焦点且此刻有全尺寸视图：按视频原始尺寸。fullSize 已经包含「离开播放页
	// 的过渡还没演完」那一段——提前收尺寸，飞出去的舞台会变糊。
	if s == a.focus && a.fullSize() {
		return presentSize{}
	}
	if a.onWatchWall() {
		if r, ok := a.watch.rects[s.key]; ok {
			w, h := watchSize(r)
			return presentSize{W: w, H: h}
		}
		return s.present
	}
	return presentSize{W: 160, H: 90}
}

// applyPresent 把呈现尺寸推给**当前该看的那一间**。值没变就不推：
// SetPresentSize 每次都会让引擎立刻重渲染一帧，每帧都调等于按帧率重画。
//
// 只有一个入口：墙格和播放页都从这里推，判据是「这一间此刻被画在哪」。
func (a *App) applyPresent() {
	a.applyPresentTo(a.focus)
}

// applyPresentForce 无条件推一次。只给「换了新引擎」用：新引擎上的尺寸
// 还是它自己的默认值，和 present 相等不代表已经推过。
func (a *App) applyPresentForce() {
	s := a.focus
	if s == nil {
		return
	}
	a.setPresent(s, a.presentSizeForSession(s))
}

// presentNewEngine 把呈现尺寸推给某一间刚接上的新引擎。
//
// 和 applyPresentForce 的区别只有一个：它不要求这一间是焦点。墙格重新连上
// 的新引擎同样要推——dropEngine 会把 present 留在会话上，`applyPresentTo`
// 的 `want == present` 会短路，于是那一格按视频原始尺寸（1080p）渲染。
func (a *App) presentNewEngine(s *roomSession) {
	if s == nil {
		return
	}
	a.setPresent(s, a.presentSizeForSession(s))
}

// presentBucket 把像素尺寸压成三档。日志只记档位，不记像素：拖窗口时
// rect 每帧都在变，按像素记会把日志刷爆，而且那也不是排障需要的信息。
func presentBucket(p presentSize) string {
	switch {
	case p.W == 0 && p.H == 0:
		return "full"
	case p.W <= 160 && p.H <= 90:
		return "mini"
	default:
		return "tile"
	}
}

// setPresent 是呈现尺寸的唯一写者，同时记一条档位变化日志。
func (a *App) setPresent(s *roomSession, want presentSize) {
	if s == nil {
		return
	}
	from := presentBucket(s.present)
	s.present = want
	if tr := s.videoTrack(); tr != nil {
		tr.SetPresentSize(want.W, want.H)
	}
	to := presentBucket(want)
	if from != to {
		s.event().Str(diag.Event, diag.EvPresentChange).
			Str(diag.From, from).Str(diag.To, to).
			Int("w", want.W).Int("h", want.H).
			Msg("呈现尺寸变化")
	}
}

// applyPresentTo 把某一间的呈现尺寸推到它的引擎。墙格上的每一格用它，
// 播放页的当前这一间也用它——同一个函数，没有第二套。
func (a *App) applyPresentTo(s *roomSession) {
	if s == nil {
		return
	}
	want := a.presentSizeForSession(s)
	if want == s.present {
		return
	}
	tr := s.videoTrack()
	if tr == nil {
		return
	}
	a.setPresent(s, want)
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

// ensureFocusMedia 保证焦点这一间在播放页上有画面。
//
// 只在播放页、只在焦点没有引擎、没在取流、也没落终态时启动。同一间反复
// 进入播放页不会重复取流；墙上已经活着的引擎原样接着用——焦点和墙格画的
// 是同一个 roomSession，没有任何东西需要搬。
//
// 终态（取流失败 / 未开播）不自动重试：播放页画错误层，用户点「重试」。
// 转场期间 Router 会重建上一页，这里每帧被问到也不会开第二路——判据是
// 会话自己的状态，不是"上一次构建是不是播放页"。
func (a *App) ensureFocusMedia() {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	s := a.focus
	if s == nil || s.room.Platform == "" || s.room.RoomID == "" {
		return
	}
	if s.player != nil {
		// 画面是从墙上带进来的：这一路没有经过 beginSession，媒体层手上没有
		// 它这一轮的 ctx。补上，否则地址不续、ended 不重拉，热切换无凭据。
		if a.media.ctx == nil {
			a.adoptLiveSession(s)
		}
		return
	}
	if s.loading || s.offline || s.err != "" {
		return
	}
	// 平台已经说过没开播：播放页画未开播层，等用户手动刷新或开播探测。
	if s.stream.RoomID != "" && !s.stream.Live {
		return
	}
	diag.L().Info().Str("key", s.key).Msg("进入播放页，开始取流")
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
	room := ""
	if s := a.focus; s != nil {
		room = s.key
	}
	m.log = newLogRef("player", room, m.id, m.start)
	m.setFirstFrame(false)
	m.setOpened(time.Time{})
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	if m.session() != nil {
		m.session().up = live.Variant{}
	}
	m.setLoading(true)
	m.setErr("")
	if m.session() != nil {
		m.session().offline = false
	}
	m.buffering = false
	m.bufferingSince = time.Time{}

	sess := playbackSession{seq: m.seq, ctx: ctx, id: m.id, start: m.start, win: m.win, log: m.log}
	m.log.event().Str(diag.Event, diag.EvSessionBegin).Str("cause", stage).Msg("开始起播")
	return sess
}

// session 打包当前这一轮的凭据，交给后台 goroutine 用。
//
// 后台不能回读 App 字段（那些只属于主线程），所以这里一次拷全。
func (a *App) session() playbackSession {
	m := &a.media
	return playbackSession{seq: m.seq, ctx: m.ctx, id: m.id, start: m.start, win: m.win, log: m.log}
}

// adoptLiveSession 给已经在出图的一路补一轮媒体会话。
//
// 从观测墙、关注页的墙上岛进播放页时，画面是墙格自己开的那枚播放器，没经过
// beginSession；而 setFocus 已经把上一轮的 ctx 清掉了。ensureFocusMedia 看到
// 「已经有播放器」就不会再起播——于是这一路在播放页上没有轮次：地址不会续、
// ended 不会重拉，热切换只能拿空凭据去要地址（2026-11-25 的生产崩溃）。
//
// 不动画面：player 原样接着用，只把「这一轮」的凭据和状态补齐。首帧、opened
// 都按「已经有画面」写，否则 trackFirstFrame 会拿首帧预算去判一路早就播着的流。
func (a *App) adoptLiveSession(s *roomSession) {
	m := &a.media
	if s == nil || s.player == nil {
		return
	}
	m.cancelSession()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.ctx = ctx
	m.win = a.Win
	// 上一轮的换地址进度跟着作废。从墙上接管这一路（watchActivate →
	// ensureFocusMedia）不经过 stopPlayback，残留的 swapFailures 会让新
	// 这一路第一次热切换失败就直接凑满阈值走「整轮重开」——用户看到的是
	// loading 覆盖层 + 断音，而不是无缝续期。
	m.swapFailures = 0
	m.lastSwapAt = time.Time{}
	m.id = fmt.Sprintf("接管#%d", m.seq)
	m.start = time.Now()
	m.log = newLogRef("player", s.key, m.id, m.start)
	// 首帧只按「这一路到底出没出图」写：墙格刚挂上引擎、还没出图时
	// Ready() 是 false，写死 true 会让 needFirstFrameTimeout 永久失效、
	// 死地址无限停在「正在拉取直播流…」上（播放页那条 spinner 没有重试按钮）。
	m.setFirstFrame(s.player.Ready())
	m.setOpened(time.Now())
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	m.setLoading(false)
	m.setErr("")
	m.buffering = false
	m.bufferingSince = time.Time{}
	// 这一路的档位是墙格取到的，续期时刻按它自己的寿命算。
	m.setRefresh(refreshAtFor(s.up))
	m.log.event().Str(diag.Event, diag.EvSessionBegin).Str("cause", "adopt").Msg("接管已在播的一路")
}

// startStream 取流 → 转封装 → 播放器 →（按需）弹幕。
//
// keepDanmaku 为 true 时保留已有的弹幕连接：换清晰度、换线路都不该
// 把聊天打断——画面和弹幕是两条独立的路。
func (a *App) startStream(keepDanmaku bool) {
	sessFocus := a.focus
	if sessFocus == nil {
		return
	}
	s := sessFocus.room
	if s.Platform == "" || s.RoomID == "" {
		return
	}
	prov, ok := providerFor(live.Platform(s.Platform))
	if !ok {
		a.media.setLoading(false)
		a.media.setErr("该平台还不支持取流")
		return
	}
	_, hasStream := prov.(live.Streamer)
	_, hasPlayback := prov.(live.PlaybackStreamer)
	if !hasStream && !hasPlayback {
		a.media.setLoading(false)
		a.media.setErr("该平台还不支持取流")
		return
	}

	// 新的一轮：停掉旧画面（两路 ffmpeg 同时拉会抢带宽，播放器也会争硬件
	// 解码器），取消上一轮的后台工作，再记下这一轮的关联 ID 与起点。
	// 焦点会话一定存在：openRoom / setFocus 已经建好它。
	sess := a.beginSession("取流")
	if !keepDanmaku {
		a.closeDanmaku()
		a.media.danmuRetryAt = time.Time{}
		// 换房间时旧房的弹幕一条都不能留：面板是「历史」，混进上一间房
		// 的聊天会让人以为串台了。
		a.resetDanmuHistory()
	}

	// 清晰度 / 线路属于这一间房：用房间自己的记录去取流。线路在界面上
	// 是标签（「主线」「备线1」），取流要节点 id；本房间的档位表还在手上
	// 时现映射，否则用记录里存下的 id。
	streamKey := sessFocus.key
	known := sessFocus.stream
	lineLabel := a.lineFor(streamKey)
	line, want := a.lineIDForLabel(streamKey, known, lineLabel), a.qualityFor(streamKey)
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
		go a.startDanmaku(roomCtx, dseq, prov, live.Platform(s.Platform), roomID, win)
	}

	go func() {
		fetchCtx, cancel := context.WithTimeout(sess.ctx, streamFetchBudget)
		defer cancel()

		info, selected, err := fetchStream(fetchCtx, prov, roomID, line, want)
		if err != nil {
			// 被新一轮取代时不必报错：用户已经不看这一路了。
			if sess.expired() {
				sess.log.debug().Str(diag.Event, diag.EvSessionCancel).Msg("取流被新一轮取代")
				return
			}
			sess.log.warn().Str(diag.Event, diag.EvSessionFetchFail).Dict(diag.ErrField, diag.Err(err)).Msg("取流失败")
			a.applyMedia(sess, func() {
				a.media.setLoading(false)
				a.media.setErr("取流失败，请重试或切换线路")
				a.media.lastFail = time.Now()
				a.focusFailed(a.focus)
			}, nil)
			return
		}
		sess.log.event().Str(diag.Event, diag.EvSessionFetch).
			Bool(diag.Live, info.Live).
			Int(diag.Variants, len(info.Variants)).
			Int(diag.Lines, len(info.Lines)).
			Str(diag.Quality, selected.Label).
			Str(diag.Line, line).
			Str(diag.URLHost, diag.Host(selected.URL)).
			Msg("取流完成")

		// 房间详情先上屏：标题、头像、是否在播都不依赖转封装
		a.applyMedia(sess, func() {
			if a.media.session() != nil {
				a.media.session().stream = info
			}
			a.learnRoomChoice(streamKey, info, selected)
			// 取流接口知道此刻在不在播：顺手把关注记录点亮。
			// 只升不降：能取到地址就是在播的强证据，而「下播」这种降级
			// 交给房间信息那条准（它每次真查 live_status）。
			if info.Live {
				a.writeFollowStatus(s.Platform, s.RoomID, func(f *FollowRecord) { f.Live = true })
			}
			// 平台可以给不了所点的档位（斗鱼对匿名请求会把「原画」降成
			// 「蓝光4M」）。默默换掉会让人以为切档没生效，所以明说一次。
			if picked != "" && selected.Label != "" && !sameQuality(picked, selected.Label) {
				diag.L().Info().Str("requested", picked).Str("applied", selected.Label).Msg("清晰度被平台降档")
				a.notifyPlayback("该直播间没有「" + picked + "」，已切到「" + selected.Label + "」")
			}
		}, nil)

		if !info.Live {
			a.applyMedia(sess, func() {
				a.media.setLoading(false)
				a.focusFailed(a.focus)
			}, nil)
			return
		}
		if len(info.Variants) == 0 {
			// 开播了但平台没给任何档位：菜单和地址都是空的。只落 loading=false
			// 会让覆盖层四个分支一个都不命中，画面停在封面上，连重试都点不到。
			a.applyMedia(sess, func() {
				a.media.setLoading(false)
				a.media.setErr("平台没有返回可播放的清晰度")
				a.focusFailed(a.focus)
			}, nil)
			return
		}

		// selected 是本次确认结果；旧接口的选档已由 fetchStream 完成。
		upstream := selected
		if upstream.URL == "" {
			// 按需取流的平台在地址解析失败时会走到这里：菜单完整但没有任何
			// 一项能播，报错比让用户对着加载层干等强。
			a.applyMedia(sess, func() {
				a.media.setLoading(false)
				a.media.setErr("没有拿到可播放的地址")
				a.focusFailed(a.focus)
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
		a.media.setLoading(false)
		a.media.setErr("窗口未就绪，无法播放")
	}, nil)
}

// startNativePlayback 用 libmpv 接手这一路。
//
// 返回 false 表示当前环境起不来（没有窗口）。成功后的接纳照旧走 applyMedia：
// 过期结果自己关掉，不让旧会话的画面盖上来。
func (a *App) startNativePlayback(sess playbackSession, upstream live.Variant) bool {
	win := sess.win
	if win == nil {
		return false
	}
	sess.log.event().Str(diag.Event, diag.EvSessionPath).
		Str("path", "native").Str("codec", upstream.Codec).
		Msg("播放路径")
	opts := playback.Options{
		Headers:   upstream.Headers,
		Width:     upstream.Width,
		Height:    upstream.Height,
		FrameRate: upstream.FrameRate,
		// 起播先静音，音量由 adoptPlayback 按 a.focusVolume() 推给播放器。不这么做，
		// mpv 会按默认 100% 音量先出声：主线程忙一下、adopt 晚到几十毫秒，
		// 用户就听到一声不该存在的满音量。
		StartSilent: true,
		Key:         sess.log.room,
		Session:     sess.log.session,
		Role:        "player",
		MPVLog:      mpvLogLevel(),
	}
	go func() {
		// 引擎不挂在这一轮的取流 ctx 上：观测墙和播放页会交接同一个引擎，
		// 取消取流不能顺手把还在墙上的画面关掉。生命周期归 *ui.VideoPlayer，
		// 它 Close 时引擎自己收；这一轮的 ctx 仍用来判断结果是否过期。
		e, err := openNative(context.Background(), upstream.URL, opts)
		if err != nil {
			sess.log.warn().Str(diag.Event, diag.EvSessionOpenFail).Dict(diag.ErrField, diag.Err(err)).Msg("原生引擎打开失败")
			if sess.expired() {
				return
			}
			runOnMain(win, func() {
				a.applyMedia(sess, func() {
					a.media.setLoading(false)
					a.media.setErr("播放组件打不开这个流，请重试或切换线路")
					a.media.lastFail = time.Now()
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

// adoptPlayback 把已经就绪的播放器接到焦点会话上。
//
// 只在主线程、且代号已被采纳时调用。焦点在取流途中被关掉时（setFocus 已经
// 推进代号），这个回调本来就不会被接纳；这里的 nil 判断是兜底，顺便把
// 已经开出来的引擎关掉，别把它漏在后台。
func (a *App) adoptPlayback(sess playbackSession, p *ui.VideoPlayer, upstream live.Variant) {
	s := a.focus
	if s == nil {
		p.Close()
		return
	}
	a.media.setOpened(time.Now())
	a.media.pauseStarted = time.Time{}
	a.media.pausedFor = 0
	s.attachPlayer(p)
	s.up = upstream
	a.media.setRefresh(refreshAtFor(upstream))
	a.media.setLoading(false)
	a.media.setErr("")
	// 新引擎接上，按当前位置求一次呈现尺寸。墙上格子、播放页全尺寸、
	// 后台 160×90，判断只在 presentSizeFor 一处。必须无条件推：新引擎
	// 上的尺寸还是它自己的默认值，和 present 相等不代表已经推过。
	a.applyPresentForce()
	// 播放器打开就按这一间自己的音量对齐，别让用户先听到满音量再跳。
	a.applySessionVolume(s)
	if !s.playing {
		a.media.pauseStarted = a.media.opened()
		p.Pause()
	}
	sess.log.event().Str(diag.Event, diag.EvSessionOpen).
		Str(diag.Quality, upstream.Label).
		Str(diag.URLHost, diag.Host(upstream.URL)).
		Int(diag.DeclaredW, upstream.Width).Int(diag.DeclaredH, upstream.Height).
		Msg("引擎已打开")
	// 小窗可能正开着而主窗口藏着：这一路是在主窗口的 Update 里接上的，
	// 不叫小窗重画一次，它会一直停在「没有播放器」的那一帧封面上。
	a.invalidateWindows()
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

// playerEnded 是播放页的「这一路自己停了」。
//
// 和墙的 watchEnded 是同一套判据（confirmEnded），差别只在平台说还在播之后：
// 墙上整路重建，这里热切换——换一份地址、等新路出图再切，画面不断。
//
// 下播就走 watchOffline：引擎关掉、播放页画未开播层、焦点保留（错误层和岛上
// 的退出键还要用）。不走这一步的后果是一整条死循环，见 trackFirstFrame。
func (a *App) playerEnded(s *roomSession) {
	a.confirmEnded(s, "dead-ended", func(_ *roomSession, why string) {
		a.scheduleSwap(why)
	})
}

// scheduleSwap 派一次「取新地址 → 开第二路 → 换上去」。
//
// 两种触发：地址快到期了，或者这一路已经停了（播放器自己结束 / 时间轴不动）。
// 两条要的是同一件事——再要一份能播的地址——所以走同一条路。
func (a *App) scheduleSwap(reason string) {
	m := &a.media
	// m.ctx 是「这一轮取流还在」的存在凭据，m.eng() 不是：cancelSession 只清
	// ctx，焦点会话的播放器照旧活着（墙里的静音预览、交接中间态）。少了这一条，
	// 一次「没有活会话」的热切换会拿着 nil ctx 去打平台接口——成功的那一半被
	// 判过期丢掉，失败的那一半在 fail 里崩。
	if m.refreshing || m.ctx == nil || m.eng() == nil {
		// 没有活会话的换地址请求：不该有，但真出现过（2026-11-25 的生产
		// 崩溃就是它一路走到失败回调里对 nil ctx 调方法）。debug 级常驻，
		// 下次再有人报「播放页冻住 / 崩了」，这条能回答守卫有没有工作。
		if m.ctx == nil && m.eng() != nil && !m.refreshing {
			m.log.debug().Str(diag.Event, diag.EvSessionSwapSkip).
				Str(diag.Cause, reason).Msg("这一路没有媒体轮次，跳过换地址")
		}
		return
	}
	if !m.lastSwapAt.IsZero() && time.Since(m.lastSwapAt) < swapRetryDelay {
		return
	}
	m.refreshing = true
	m.lastSwapAt = time.Now()
	sess := a.session()
	cur := a.current()
	line := a.lineIDFor(m.roomKey(), m.streamNow())
	req := swapRequest{platform: live.Platform(cur.Platform), roomID: cur.RoomID, line: line, quality: m.up().Label, background: m.app.inBackground()}
	sess.log.event().Str(diag.Event, diag.EvSessionSwapStart).Str(diag.Cause, reason).Msg("开始换地址")
	go a.runHotSwap(sess, req, reason)
}

// runHotSwap 在后台取一条新地址、开好第二路，等它出图再交给主线程换上去。
//
// 新的一路先出图再切换，所以屏幕上不会经过加载态；切换那一刻画面是连续的，
// 只有声音有一次几百毫秒的接缝——网页端也是这个代价。
func (a *App) runHotSwap(sess playbackSession, req swapRequest, reason string) {
	fail := func(err error) {
		sess.log.warn().Str(diag.Event, diag.EvSessionSwapFail).
			Str(diag.Cause, reason).
			Dict(diag.ErrField, diag.Err(err)).
			Msg("换地址失败")
		runOnMain(sess.win, func() {
			// 过期会话的失败不能记到新会话头上。
			//
			// 换房间 / closeMedia 会 cancelSession 推进代号，旧一路的
			// fetchStream 随后带着 context.Canceled 回来。照常累加
			// swapFailures 会污染新会话的退避：新会话自己的热切换再失败一次
			// 就凑满阈值，把刚起来的一路整轮重开（loading 覆盖层 + 断音）。
			if sess.expired() || a.media.seq != sess.seq {
				return
			}
			a.media.refreshing = false
			a.media.swapFailures++
			if a.media.swapFailures >= swapMaxFailures {
				// 新地址也开不起来：退回整轮重来，至少让用户看到进度。
				a.media.swapFailures = 0
				// 这一行必须留：日志里连续出现的「取流#N」如果来自这里，
				// 说明是整轮重开把缓冲丢掉了，不是播放器自己停的。
				diag.L().Info().Str("reason", reason).Msg("热切换失败，整轮重开")
				a.startStream(true)
				return
			}
			a.media.setRefresh(time.Now().Add(swapRetryDelay))
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
	// 同上：引擎寿命归播放器，不归取流 ctx。交接给墙上的格子时不能被取消。
	//
	// StartSilent 是必须的：下面等的是「新路出了画面 / 出了声音」，那本身就
	// 证明它已经在出声。不带它，新路从 loadfile 起就是 mpv 默认的 100% 音量，
	// 等 adoptSwap 第一次 SetVolume 时已经晚了——每次续期都会有两路叠音，
	// 用户静音时也漏，画面 8 秒不出就响满 8 秒。
	e, err := openNative(context.Background(), selected.URL, playback.Options{
		Headers: selected.Headers, Width: selected.Width, Height: selected.Height, FrameRate: selected.FrameRate,
		StartSilent: true,
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
		if sess.expired() {
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
	s := a.media.session()
	if s == nil {
		// 焦点在热切换途中被关掉了：这一枚还没挂上会话，自己收掉。
		_ = e.Close()
		return
	}
	p := ui.NewEngineVideoPlayer(e)
	// 先摘旧引擎再挂新的：会话的引擎只有一个持有者，关和换都走 dropEngine。
	s.dropEngine()
	s.attachPlayer(p)
	s.up = v
	a.media.setRefresh(refreshAtFor(v))
	a.media.refreshing = false
	a.media.swapFailures = 0
	// 新引擎按当前位置求一次呈现尺寸：墙上是格子，播放页是全尺寸，后台
	// 收听是小图。判断只在 presentSizeFor 一处，热切换不必各自补一遍。
	a.applyPresentForce()
	a.applySessionVolume(s)
	if !s.playing {
		p.Pause()
	}
	sess.log.event().Str(diag.Event, diag.EvSessionSwapDone).
		Str(diag.Cause, reason).Str(diag.Quality, v.Label).
		Str(diag.URLHost, diag.Host(v.URL)).
		Bool("background", a.inBackground()).
		Msg("换地址完成")
	// 旧播放器是在小窗正显示的时候被换掉的，同样得让小窗重画：新引擎的
	// wake 要等小窗下一次画 Video 才会指向它。
	a.invalidateWindows()
}

func (m *playerMedia) stopPlayback() {
	if s := m.session(); s != nil {
		s.dropEngine()
	}
	m.lastPos, m.lastPosAt = 0, time.Time{}
	m.stillPos, m.stillAt = 0, time.Time{}
	m.setRefresh(time.Time{})
	m.lastSwapAt = time.Time{}
	m.refreshing = false
	m.swapFailures = 0
	// 呈现状态是推导出来的（fullSize），这里不用清：引擎没了，next
	// applyPresent 自然落到小图或空。
	m.detachedAt = time.Time{}
	m.tickPending = false
	m.buffering = false
	m.bufferingSince = time.Time{}
	m.diagAt = time.Time{}
	m.diagPos, m.diagAudio = 0, 0
	m.diagFrames, m.diagCalls = 0, 0
	m.audioStalled = false
}

// failPlayback 把这一轮落到可重试的终态。
//
// 先收掉播放器——留着坏的会让错误层盖在一块还在解码的黑画面上。房间 ctx 和
// 弹幕不动：画面断了不代表聊天断了，也不该因为一次播放失败触发整房间重连。
func (a *App) failPlayback(class, msg string, err error) {
	m := &a.media
	m.log.warn().Str(diag.Event, diag.EvSessionFail).
		Str("class", class).
		Dict(diag.ErrField, diag.Err(err)).
		Msg("播放失败")
	m.lastFail = time.Now()
	a.stopPlayback()
	m.setLoading(false)
	m.setErr(msg)
	// 不在播放页上的焦点没有声音也没有画面：收掉它（在墙上留静音格，
	// 不在墙上连会话一起关）。播放页上保留，错误层和退出键还要用。
	a.focusFailed(a.focus)
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
	// mpv 自己的报错转进应用日志。这一条是「卡在哪一层」的唯一直接证据：
	// hls 在跳分片、解码器拿不到参考帧、HTTP 在重连，都只有 mpv 知道。
	//
	// 游标跟着播放器走：换引擎后 mpv 的 Seq 从 1 重新开始，不重置就会一直
	// 等「更大的序号」，新引擎的日志全部静默丢失。
	if m.mpvLogFor != p {
		m.mpvLogFor = p
		m.mpvLogSeq = 0
	}
	if n := len(d.Log); n > 0 && d.Log[n-1].Seq > m.mpvLogSeq {
		for _, l := range d.Log {
			if l.Seq <= m.mpvLogSeq {
				continue
			}
			if m.mpvLogSeq > 0 && l.Seq > m.mpvLogSeq+1 {
				m.log.warn().Str(diag.Event, diag.EvMpv).Uint64(diag.Dropped, l.Seq-m.mpvLogSeq-1).Msg("mpv 日志缺口")
			}
			level := diag.LevelWarn
			if l.Level == "error" || l.Level == "fatal" {
				level = diag.LevelError
			}
			// level 是变量，logRef 只有按级别固定的四个入口（diag.go 不归这里改），
			// 所以在调用点分派一次，每个分支都是具体的原生链式调用。
			ev := func() *zerolog.Event {
				switch level {
				case diag.LevelError:
					return m.log.err()
				case diag.LevelDebug:
					return m.log.debug()
				case diag.LevelInfo:
					return m.log.event()
				default:
					return m.log.warn()
				}
			}()
			ev.Str(diag.Event, diag.EvMpv).
				Str(diag.Module, l.Prefix).
				Str("mpv_level", l.Level).
				Str(diag.Text, l.Text).
				Str("mpv_at", l.At.Format(time.RFC3339Nano)).
				Msg("mpv 日志")
			m.mpvLogSeq = l.Seq
		}
	}
	if d.Buffering != m.buffering {
		// 起止两条共用的字段：state 在前，stalled_ms 只加在「结束」那一条。
		bufferFields := func(ev *zerolog.Event) *zerolog.Event {
			return ev.Int64(diag.PositionMS, d.Position.Milliseconds()).
				Int64(diag.AudioMS, d.AudioPTS.Milliseconds()).
				Bool(diag.AudioOK, d.AudioPTSValid).
				Int64(diag.CacheMS, d.CacheDuration.Milliseconds()).
				Int64(diag.CachePct, d.CacheBuffering).
				Int64("decoder_drops", d.DecoderDrops).
				Int64("mistimed", d.Mistimed).
				Float64(diag.AVSyncS, d.AVSync)
		}
		if d.Buffering {
			m.bufferingSince = now
			bufferFields(m.log.event().Str(diag.Event, diag.EvSessionBuffer).Str("state", "start")).
				Msg("缓冲开始")
		} else {
			bufferFields(m.log.event().Str(diag.Event, diag.EvSessionBuffer).Str("state", "end")).
				Int64(diag.StalledMS, now.Sub(m.bufferingSince).Milliseconds()).
				Msg("缓冲结束")
		}
		m.buffering = d.Buffering
	}
	// 音频停走：一秒采一次。缓冲期内不算——上游没数据，声音当然停。
	// AudioPTSValid 为假时 AudioPTS 只是缺省值 0，不能据此判「停走」；
	// 否则没有音轨的流会得到一条假的因果。
	if now.Sub(m.diagAt) < time.Second {
		return
	}
	frames, _, _ := e.Stats()
	calls := e.Calls()
	if !m.diagAt.IsZero() && d.AudioPTSValid && !d.Buffering && d.AudioPTS == m.diagAudio && d.Position > m.diagPos {
		if !m.audioStalled {
			m.audioStalled = true
			m.log.warn().Str(diag.Event, diag.EvSessionAudioStop).
				Int64(diag.PositionMS, d.Position.Milliseconds()).
				Int64(diag.AudioMS, d.AudioPTS.Milliseconds()).
				Bool(diag.AudioOK, d.AudioPTSValid).
				Float64(diag.AVSyncS, d.AVSync).
				Msg("音频停走")
		}
	} else if !d.AudioPTSValid || d.AudioPTS > m.diagAudio {
		m.audioStalled = false
	}
	// 视频停帧：画面时钟不动、音频还在走。这是墙上和播放页都报过的那一种。
	// 同一秒把「视图取了几次帧 / 解码器给了几次新画面」写出来：calls 不涨
	// 说明没人来取帧，calls 涨而 frames 不涨说明取了但没画面——两者是完全
	// 不同的故障，不能再用别的猜。
	if !m.diagAt.IsZero() && !d.Buffering && d.Position == m.diagPos && d.AudioPTS > m.diagAudio {
		if !m.videoStalled {
			m.videoStalled = true
			m.log.warn().Str(diag.Event, diag.EvSessionVideoStall).
				Int64(diag.PositionMS, d.Position.Milliseconds()).
				Int64(diag.AudioMS, d.AudioPTS.Milliseconds()).
				Uint64("render_calls", calls-m.diagCalls).
				Uint64("new_frames", frames-m.diagFrames).
				Int64(diag.CacheMS, d.CacheDuration.Milliseconds()).
				Int64(diag.CachePct, d.CacheBuffering).
				Float64("vfps", d.VFFPS).
				Float64(diag.AVSyncS, d.AVSync).
				Msg("视频停帧")
		}
	} else if d.Position > m.diagPos {
		m.videoStalled = false
	}
	m.diagAt, m.diagPos, m.diagAudio = now, d.Position, d.AudioPTS
	m.diagFrames, m.diagCalls = frames, calls
}

// trackFirstFrame 记录首帧，并在超时和播放器报错时落到可重试终态。
//
// 每帧调用。Ready 只说明解码器交出了第一帧，不等于它在屏幕上呈现的时刻；
// 日志按「首帧可用」标注，别当成精确的观感时间。
func (a *App) trackFirstFrame() {
	m := &a.media
	p := m.eng()
	if p == nil {
		return
	}
	now := time.Now()
	a.trackContinuity(p, now)
	if !m.firstFrame() {
		m.observeStartupPause(a.focusPlaying(), now)
	}
	// 播放器报错时 Ready 仍可能为真，这个判断必须排在记录首帧之前。
	if err := p.Err(); err != nil {
		// 暂停中不重启也不清理：用户主动停着，恢复播放那一帧会再进来处理。
		if !a.focusPlaying() {
			return
		}
		a.failPlayback("playback-failed", "播放失败，请重试或切换线路", err)
		return
	}
	// 后台收听：全尺寸呈现关着、没人看画面，Ready / Position 的含义都变了。
	//
	// 这里不能再走首帧那一套——后台没人看画面，首帧预算会一路走到超时，
	// 把正在出声的一路判成失败。判据换成「音频时钟还在不在走」：它停了才是
	// 真的断了，那时候才换地址。
	if m.app.inBackground() {
		a.trackBackground(p, now)
		return
	}
	if m.firstFrame() || p.Ready() {
		if !m.firstFrame() {
			m.setFirstFrame(true)
			w, h := p.Size()
			var hwdec string
			var gpu bool
			var gpuFrames, renders uint64
			var displayFPS, vfps float64
			if e, ok := p.Engine().(*playback.Engine); ok {
				debug := e.Debug()
				hwdec = debug.HWDec
				gpu, gpuFrames, renders = debug.GPU, debug.GPUFrames, debug.Renders
				displayFPS, vfps = debug.DisplayFPS, debug.VFFPS
			}
			m.log.event().Str(diag.Event, diag.EvSessionFirstFrame).
				Str(diag.HWDec, hwdec).
				Str("upstream_codec", m.up().Codec).
				Int(diag.DeclaredW, m.up().Width).Int(diag.DeclaredH, m.up().Height).
				Int64("declared_bitrate", m.up().Bitrate).
				Float64("declared_fps", m.up().FrameRate).
				Int(diag.DecodedW, w).Int(diag.DecodedH, h).
				Bool(diag.GPU, gpu).
				Uint64(diag.GPUFrames, gpuFrames).
				Uint64(diag.Renders, renders).
				Float64(diag.DisplayFPS, displayFPS).
				Float64(diag.VFPS, vfps).
				Bool(diag.Buffering, p.Buffering()).
				Msg("首帧可用")
			if !m.lastFail.IsZero() {
				m.log.event().Str(diag.Event, diag.EvSessionRecover).
					Int64(diag.RecoverMS, time.Since(m.lastFail).Milliseconds()).
					Msg("播放恢复")
				m.lastFail = time.Time{}
			}
		}
		// 两条触发，最后都落到「换一份新地址」：
		//   1. 地址快到期了（平台声明过寿命）——提前换，用户看不到停；
		//   2. 这一路已经停了：播放器自己结束，或者时间轴长时间不动。
		//      这是兜底，正常情况下第一条先触发。
		if a.focusPlaying() {
			if why := m.deadReason(true, p.Playing(), p.Buffering(), p.Position(), now); why != "" {
				if why == "ended" {
					// 播放器自己停了：可能只是地址过期，也可能是主播下播了。
					// 这两种在画面上分不出来，只能问平台——不问就换地址，下播
					// 的房间会一直重取：平台给不出流，三次失败整轮重开，再判死。
					a.playerEnded(m.session())
				} else {
					// 不在这里打日志：卡住的时候每帧都会命中，scheduleSwap 自己会
					// 记一条 swap-start，带原因。
					a.scheduleSwap("dead-" + why)
				}
			}
		}
		if !m.refresh().IsZero() && !now.Before(m.refresh()) {
			a.scheduleSwap("expire")
		}
		return
	}
	// Ready 已经在上面的分支里优先，所以这里只剩「确实还没出图」的情况。
	if m.needFirstFrameTimeout(a.focus, now, a.focusPlaying()) {
		a.failPlayback("first-frame-timeout", "直播流长时间没有画面，请重试或切换线路", nil)
	}
}

// streamStallTimeout 是「时间轴多久不动就当成这一路已经死了」。
//
// 缓冲也会让时间轴停住，所以这个值必须比缓冲胶囊的 1.5 秒宽：6 秒还没动一下，
// 就不是抖动，是断了。再长用户就盯着静止画面了。
const streamStallTimeout = 6 * time.Second

// streamBufferStallTimeout 是缓冲态下时间轴停住的容忍。
//
// 和墙上的 watchDeadReason 共用同一个数。20 秒给了「停 8 秒又恢复」这类
// 短抖动自己接上的机会——本地实测恢复用时 2.5 秒；再长就不是抖动了。
const streamBufferStallTimeout = 20 * time.Second

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
	if !m.refresh().IsZero() && !now.Before(m.refresh()) {
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
func (m *playerMedia) deadReason(ready, playing, buffering bool, pos time.Duration, now time.Time) string {
	if !ready {
		return ""
	}
	if !playing {
		return "ended"
	}
	if buffering {
		// 缓冲时时间轴本来就不动。基准拨到现在，否则一次 4 秒的
		// paused-for-cache 会把之前冻住的时间一起攒满、缓冲刚结束就判死，
		// 刚攒的缓存也跟着丢。
		//
		// 但缓冲不能无限等：FLV 静默断流时 mpv 一直报 buffering，既不
		// ended 也不报错（实测 150 秒）。没有这条上限，播放页永远停在
		// 最后一帧，只有地址到期能把它救回来。
		if pos != m.stillPos || m.bufferStallSince.IsZero() {
			m.stillPos, m.stillAt, m.bufferStallSince = pos, now, now
			return ""
		}
		m.stillPos, m.stillAt = pos, now
		if now.Sub(m.bufferStallSince) > streamBufferStallTimeout {
			return "buffered-too-long"
		}
		return ""
	}
	m.bufferStallSince = time.Time{}
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
func (m *playerMedia) needFirstFrameTimeout(s *roomSession, now time.Time, playing bool) bool {
	if s == nil || !playing || s.firstFrame || s.openedAt.IsZero() {
		return false
	}
	return m.startupElapsed(s, now) >= firstFrameBudget
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
func (m *playerMedia) startupElapsed(s *roomSession, now time.Time) time.Duration {
	if s == nil || s.openedAt.IsZero() {
		return 0
	}
	end := now
	if !m.pauseStarted.IsZero() {
		end = m.pauseStarted
	}
	return end.Sub(s.openedAt) - m.pausedFor
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
	// 先记「用户的意图」，再记结果。排查「我切了 1080P 怎么还是糊」时，
	// 这两条必须分开：requested 是用户点的，session.fetch/首帧才是实际生效的。
	a.media.log.event().Str(diag.Event, diag.EvSessionQualityPick).
		Str(diag.Requested, label).
		Msg("用户选择清晰度")
	// 写进这一间房自己的记录：换台 / 离开后回来，选的还是这一档；
	// 别的房间、别的平台一个字都不动。
	a.setRoomQuality(a.choiceKey(), label)
	if a.media.roomKey() == "" {
		return
	}

	a.media.pendingQualityPick = label
	v, ok := a.media.variantByLabel(label)
	if ok && v.URL != "" {
		a.media.pendingQualityPick = ""
		sess := a.beginSession("换档")
		a.media.session().up = v
		// 不要在这里再包一层 go：startPlayback 内部已经在自己开的 goroutine
		// 里跑 openNative。多包一层只会把「读 App 字段」挪到后台线程。
		a.startPlayback(sess, v)
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
	// 选项来自当前这一路的档位表，标签 → 节点 id 就在这份表里换。
	id := lineIDIn(a.media.streamNow().Lines, label)
	a.media.log.event().Str(diag.Event, diag.EvSessionLinePick).
		Str(diag.Requested, label).Str(diag.Applied, id).
		Msg("用户选择线路")
	a.setRoomLine(a.choiceKey(), label, id)
	if a.media.roomKey() != "" {
		a.startStream(true)
	}
}

// qualityOptions 是清晰度菜单的选项，全部来自平台的真实档位。
//
// 还没取到档位时返回 nil：控制条上先不出现这个下拉。以前这里返回
// {"原画"} 撑位，但平台根本没有叫「原画」的档位，用户会先看到一个
// 不存在的选项，等真档位回来再被替换掉。
func (a *App) qualityOptions() []string {
	variants := a.media.streamNow().Variants
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
	lines := a.media.streamNow().Lines
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
	for _, v := range m.streamNow().Variants {
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
		diag.L().Info().Str("requested", label).Str("fallback", variants[0].Label).Msg("清晰度标签未命中，改用默认档")
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

// resetDanmuHistory 清掉聊天记录与面板游标。换房间、离开播放页都走它：
// 弹幕只属于当前这一段，串台比空白更糟。
func (a *App) resetDanmuHistory() {
	a.DanmuLog = nil
	a.Danmu = nil
	a.panelSeen = 0
	a.panelPending = 0
	a.danmuLanes = nil
	a.danmuPos = 0
	a.danmuStill = 0
	a.danmuLast = time.Time{}
}

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

// ensureDanmaku 给播放页的当前房间连一条弹幕。
//
// 弹幕只属于播放页：观测墙不连，离开播放页就断；从墙上接管过来的同一路画面
// 进播放页时也要重新连一条新的。失败后退避 30 秒，避免每帧重连。
func (a *App) ensureDanmaku() {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	s := a.current()
	if s.Platform == "" || s.RoomID == "" {
		return
	}
	if a.media.roomCtx != nil {
		return
	}
	if !a.media.danmuRetryAt.IsZero() && time.Now().Before(a.media.danmuRetryAt) {
		return
	}
	prov, ok := providerFor(live.Platform(s.Platform))
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.media.roomCtx, a.media.roomCancel = ctx, cancel
	a.media.danmuSeq++
	seq := a.media.danmuSeq
	win := a.Win
	go a.startDanmaku(ctx, seq, prov, live.Platform(s.Platform), s.RoomID, win)
}

// startDanmaku 使用独立房间 context；切档不取消，退出与切房立即取消。
// win 和 ctx 均由主线程捕获，后台不读取 App 的窗口字段。
func (a *App) startDanmaku(ctx context.Context, seq int, prov live.Provider, platform live.Platform, roomID string, win *mygo.Window) {
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
	log := diag.Ref{
		Sub:     "danmaku",
		Room:    string(platform) + "|" + roomID,
		Session: fmt.Sprintf("danmaku#%d", seq),
	}.Logger()
	if err != nil {
		if stream != nil {
			stream.Close()
		}
		// 正常关房也会返回 canceled；那不是故障，不要按 WARN 记。
		level := diag.LevelWarn
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			level = diag.LevelDebug
		}
		// 级别是变量，*zerolog.Logger 自带 WithLevel；不再经过 diag 的包装。
		log.WithLevel(level).
			Str(diag.Event, diag.EvDanmakuFail).
			Dict(diag.ErrField, diag.Err(err)).
			Msg("弹幕连接失败")
		runOnMain(win, func() {
			if a.media.danmuSeq == seq {
				a.media.danmuLastFail = time.Now()
				a.media.danmuRetryAt = time.Now().Add(30 * time.Second)
				a.closeDanmaku()
			}
		})
		return
	}
	if stream != nil {
		a.attachDanmaku(ctx, win, seq, stream, log)
	}
}

// attachDanmaku 的清理不依赖主线程回调被执行；取消房间即可收掉未接纳连接。
func (a *App) attachDanmaku(ctx context.Context, win *mygo.Window, seq int, stream live.DanmakuStream, log *zerolog.Logger) {
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
		if log != nil {
			log.Info().Str(diag.Event, diag.EvDanmakuConnect).Msg("弹幕已连接")
			if !a.media.danmuLastFail.IsZero() {
				log.Info().Str(diag.Event, diag.EvDanmakuRecover).
					Int64(diag.RecoverMS, time.Since(a.media.danmuLastFail).Milliseconds()).
					Msg("弹幕已恢复")
				a.media.danmuLastFail = time.Time{}
			}
		}
	}) {
		stop()
	}
}

// applyMedia 只在异步回调里接纳结果；捕获的窗口和 ctx 不需要后台回读 App。
// 资源清理由各结果的 context 守卫兜底，不能依赖关闭窗口后仍有回调。
func (a *App) applyMedia(sess playbackSession, fn, stale func()) {
	if runOnMain(sess.win, func() {
		if sess.expired() || a.media.seq != sess.seq {
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
	a.danmu.seq++
	line.Seq = a.danmu.seq
	a.DanmuLog = append(a.DanmuLog, line)
	if len(a.DanmuLog) > danmuLogMax {
		a.DanmuLog = a.DanmuLog[len(a.DanmuLog)-danmuLogMax:]
	}

	// 画面：只有聊天弹幕上屏，且要开着弹幕、在播放
	if !a.DanmuOn || !a.focusPlaying() || kind != danmuChat {
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
	p := a.media.eng()
	if p == nil {
		return
	}
	// 已报错的播放器交给回退或重试处理，Play 可能清掉错误并掩盖故障。
	if p.Err() == nil && a.focusPlaying() != p.Playing() {
		if a.focusPlaying() {
			p.Play()
		} else {
			p.Pause()
		}
	}
	// 音量只有一个写者：applySessionVolume。它按「是不是焦点」求目标值，
	// 用播放器当前音量做判据，所以起播静音、墙格预览、从墙上接管过来的
	// 一路都能在这一帧对齐。
	a.applySessionVolume(a.focus)
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
	// 帧钟，不是墙钟：提示的「还剩多久」由 noticePill 每帧在构建里算，
	// 那一侧读的是 c.Now()。用墙钟写、帧钟读的话两个时基对不上——Tester
	// 里 tt.Advance 推不动它，提示永远不消失；生产里两者相等所以看不出来。
	a.media.noticeUntil = a.now().Add(noticeDuration)
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
	if a.media.roomKey() == "" {
		return
	}
	// 用户主动重试是一条边沿：不记它，日志会把人的动作当成系统自动重连。
	a.media.log.event().Str(diag.Event, diag.EvSessionRetry).
		Str("previous_error", a.media.errText()).
		Msg("用户重试")
	a.startStream(a.media.roomCtx != nil)
}

// togglePlayback 是控制条播放键的入口。
//
// 媒体落到错误态时播放键没有意义：syncPlayer 不会把 Play 推给一个坏掉的播放器
// （Play 会清掉错误、把故障藏起来），所以这里当成显式重试，并让新一轮从「在播」
// 开始。正常状态仍然只是翻一个 UI 意图。
func (a *App) togglePlayback() {
	m := &a.media
	if m.errText() != "" || (m.eng() != nil && m.eng().Err() != nil) {
		if a.focus != nil {
			a.focus.playing = true
		}
		a.markDirty()
		a.retryStream()
		return
	}
	a.setFocusPlaying(!a.focusPlaying())
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
