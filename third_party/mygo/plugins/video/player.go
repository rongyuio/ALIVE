// Package video provides optional native-UI video playback using libmpv's
// software render API through purego. No webview, cgo or runtime Bun is required.
package video

import (
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Phase describes confirmed playback state.
type Phase string

const (
	Idle    Phase = "idle"
	Loading Phase = "loading"
	Paused  Phase = "paused"
	Playing Phase = "playing"
	Ended   Phase = "ended"
	Failed  Phase = "failed"
	Closed  Phase = "closed"
)

// debugLog 让 mpv 的日志额外打到 stderr，开发时设 ALIVE_MPV_LOG=1。
var debugLog = os.Getenv("ALIVE_MPV_LOG") != ""

// mpvLogCap 是保留的 mpv 日志条数。一场直播卡顿能刷出几百行 POC 报错，
// 留最近 200 条够看清「从哪一行开始坏的」，也不至于把内存撑起来。
const mpvLogCap = 200

var ErrClosed = errors.New("video: player closed")
var ErrLoading = errors.New("video: a source is still loading")

// Options selects a libmpv library and bounds the software video surface.
//
// Zero MaxWidth/MaxHeight use 3840×2160: 直播最高就到 4K，再大没有实际来源，
// 反而会让每一帧的内存和上传量翻倍。
//
// 这两个上限只是安全阀，**不参与选尺寸**。要多大画面用 SetPresentSize，
// 它才是唯一的呈现旋钮。以前墙上靠上限收格子、播放页靠上限放开，两个旋钮
// 各管一半，墙 → 播放页这条路上画面就永远停在格子分辨率。
//
// Headers 是回源要带的 HTTP 请求头，形如 "Referer: https://live.bilibili.com/"。
// 四个平台的直播流几乎都校验 Referer/UA，缺了直接 403。
//
// Silent uses a null audio output, for tests.
type Options struct {
	Library             string
	MaxWidth, MaxHeight int
	Headers             []string
	Silent              bool

	// Width/Height 是调用方事先知道的视频尺寸（平台声明值）。mpv 还没解析出
	// 真实尺寸时用它顶着，免得第一帧先按 640×360 渲染再跳变。
	Width, Height int

	// ClientName 是播放器向系统注册的名字：Windows 音量合成器里的应用名、
	// PulseAudio/PipeWire 里的流名。留空就沿用 mpv 自己的默认值。
	//
	// 必须连着 audio-client-name 一起设 title：mpv 在音频起来之后会用
	// --title 展开出的标题再把会话名覆盖一遍，默认模板是「<媒体名> - mpv」，
	// 所以只设 audio-client-name 会被这行覆盖回 mpv。
	ClientName string

	// GPU 走零拷贝（D3D11 render API）。默认关，见 d3d11_windows.go 的说明：
	// 结构体未经验证，实测出不画。
	GPU bool

	// NoBackBuffer 关掉 demuxer 的回退缓存（demuxer-max-back-bytes=0）。
	//
	// ALIVE 本地补丁。直播不能回退，那份缓存永远不会被读到，却按 mpv 的
	// 默认上限占着内存；观测墙一屏最多 12 路，每路各留一份是纯浪费。
	// 对画面没有任何影响：前向缓存（抗抖动要用的那段）不动。
	NoBackBuffer bool

	// LogLevel 是 mpv 自己的日志订阅级别，默认 warn。ALIVE 本地补丁：
	// 排障时设 info / v 才能拿到 hls、http、demuxer 那一层的第一手信息。
	LogLevel string
}

// Capabilities reflects a successfully created native renderer. Codec/container
// support depends on the supplied libmpv/FFmpeg build. Seeking is per-source.
type Capabilities struct {
	Engine                      string
	Playback, SoftwareRendering bool
	HardwareDecoding, HDR, DRM  bool
}

// State is an immutable snapshot updated by libmpv events. Err includes both
// source failures and asynchronous control failures. Durations are zero while
// unknown; Seekable determines whether seeking is supported for this source.
type State struct {
	Source                     string
	Phase                      Phase
	Position, Duration         time.Duration
	Volume, Rate               float64
	Muted, Seekable, Buffering bool
	Err                        error

	// 以下字段是 ALIVE 的本地补丁（上游没有）。直播「短暂不连续」有五种来源，
	// Position 和 Buffering 只能看出其中一种：音频断流时 Position 照样走，
	// 呈现抖动时两者都正常。有了这些数才能把上游断供、音频断流、呈现抖动
	// 分开，而不是靠猜。
	AudioPTS                 time.Duration // 音频时钟；没有音轨或未起步时为 0
	AudioPTSValid            bool          // AudioPTS 是否真的可用；否则 0 只是缺省值
	CacheDuration, CacheTime time.Duration // 读前缓存里有多少秒、当前读头位置
	CacheBuffering           int64         // 0–100，缓冲完成度
	CacheIdle                bool          // demuxer 在等数据（读前缓存空）
	DecoderDrops             int64         // 解码器主动丢的帧
	Mistimed                 int64         // 因音画不同步被判为「迟了」的帧
	DisplayFPS, VFFPS        float64       // 显示器刷新率 / 实际视频帧率
	VideoSpeed, AudioSpeed   float64       // 音视频各自的校正倍率（1.0 = 不校正）
	AVSync                   float64       // 当前音画偏差（秒）
	HWDec                    string        // 实际启用的硬解；空或 "no" 表示软件解码
	// GPU 表示这一路走的是零拷贝路径：mpv 直接渲进共享纹理，帧不经内存。
	// ALIVE 本地补丁。
	GPU bool
	// GPUFrames 是这条路径实际渲出的帧数。GPU 为真但它是 0，说明纹理
	// 建不出来——那一路其实没在出画。
	GPUFrames uint64
	// Renders 是累计真正做过的渲染次数（跳过的不算）。
	// 用来验证渲染门控：没人看时它应当停止增长。
	Renders uint64

	// Log 是 mpv 自己报的错误 / 警告，最近若干条（旧的在前面）。
	//
	// 直播卡顿这类问题，mpv 的日志是唯一能说明「卡在哪一层」的东西——
	// 是 hls 在跳分片、还是解码器拿不到参考帧、还是 HTTP 重连。原来它只
	// 往 stderr 打，双击启动的进程没有 stderr，等于不留。上层每帧把它转进
	// 应用日志，出问题时才有东西可看。
	Log []LogLine
}

// LogLine 是 mpv 的一条日志。
type LogLine struct {
	Level  string // error / warn / info
	Prefix string // mpv 的模块前缀（hls / ffmpeg / ao / vo），定位「哪一层」用
	Text   string
	At     time.Time
	// Seq 单调递增。上层据此只转发没见过的那些，不必自己维护游标。
	Seq uint64
}

// Player owns a native playback engine, event worker and rendering worker.
// Its methods are safe from any goroutine. Close it when its window closes.
// Commands enqueue asynchronously; State reports the engine's confirmed result.
type Player struct {
	mu               sync.Mutex
	commandMu        sync.Mutex
	state            State
	native           backend
	opts             Options
	logRank          int // mpv 日志订阅级别；ALIVE 本地补丁
	wake, renderWake chan struct{}
	stop, done       chan struct{}
	workers          sync.WaitGroup
	invalidate       func()
	frame            *Frame
	serial           uint64
	width, height    int
	// rawW/rawH 是 mpv 报的原始显示尺寸，不经渲染上限改写。target 每帧从它
	// 重新算上限，上限才能在中途放开（墙格 → 播放页）。
	rawW, rawH         int
	presentW, presentH int  // 非零时按这个尺寸渲染（后台听收不解全尺寸画面）
	forceRender        bool // 尺寸变化后立刻用当前帧重渲染一次
	// lastPull 是上一次有人取帧的时刻，renderIdle 据此决定要不要跳过渲染。
	// pull 只发生在真正显示这一路的帧里，所以它就是"有没有人在看"。
	lastPull time.Time
	// idle 表示渲染线程已经进入"跳过渲染"状态（没人看）。
	//
	// 它只由渲染线程写、Frame() 读后清：Frame() 看见 idle 就强制渲一帧并
	// 唤醒渲染线程。写成反的（idle 时置假）会让恢复永远等不到唤醒，画面
	// 一直冻着，直到上层看门狗把整路重开。
	idle bool
	// gpu 表示这一路走零拷贝（D3D11 渲染 API）。ALIVE 本地补丁。
	gpu bool
	// gpuFrames 是零拷贝实际渲出的帧数；用它判断这条路是不是真在出画。
	gpuFrames func() uint64
	// mpvLog 是最近的 mpv 错误 / 警告，环形缓冲，State.Log 交出去给上层落盘。
	mpvLog    []LogLine
	mpvLogSeq uint64
	// moovWarned 表示已经记过那条已知无害的 duplicated MOOV 警告。
	moovWarned   bool
	requestID    uint64
	loadID       uint64
	pending      map[uint64]bool // true for a load request
	loading      bool
	startPending bool
	entry        int64
	paused       bool
}

func New(opts Options) (*Player, error) {
	if opts.MaxWidth == 0 {
		opts.MaxWidth = 3840
	}
	if opts.MaxHeight == 0 {
		opts.MaxHeight = 2160
	}
	if opts.MaxWidth < 1 || opts.MaxHeight < 1 || opts.MaxWidth > 4096 || opts.MaxHeight > 4096 || int64(opts.MaxWidth)*int64(opts.MaxHeight) > 10<<20 {
		return nil, errors.New("video: render bounds must fit 4096 per axis and 10 million pixels")
	}
	p := makePlayer(opts)
	n, err := newNative(opts, p.wake, p.renderWake)
	if err != nil {
		return nil, err
	}
	p.native = n
	if nn, ok := n.(*native); ok && nn.gpu != nil {
		p.gpu = true
		p.gpuFrames = nn.gpu.frameCount
	}
	p.start()
	return p, nil
}
func makePlayer(opts Options) *Player {
	return &Player{opts: opts, logRank: mpvLogRank(opts.LogLevel), state: State{Phase: Idle, Volume: 100, Rate: 1}, paused: true, wake: make(chan struct{}, 1), renderWake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), pending: map[uint64]bool{}, width: 0, height: 0}
}

// mpvLogRank 把 mpv 的级别名映射成可比较的序号。默认 warn（4）。
func mpvLogRank(level string) int {
	switch level {
	case "trace":
		return 0
	case "debug":
		return 1
	case "v":
		return 2
	case "info":
		return 3
	case "warn", "warning", "":
		return 4
	case "error":
		return 5
	case "fatal":
		return 6
	}
	return 4
}
func (p *Player) start() { p.workers.Add(2); go p.events(); go p.frames(); signal(p.wake) }
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.state
	s.GPU = p.gpu
	if p.gpuFrames != nil {
		s.GPUFrames = p.gpuFrames()
	}
	return s
}
func (p *Player) Capabilities() Capabilities {
	p.mu.Lock()
	defer p.mu.Unlock()
	return Capabilities{Engine: "libmpv", Playback: p.state.Phase != Closed, SoftwareRendering: p.state.Phase != Closed}
}

// SetInvalidate 注册「画面或状态变了」的回调，通常是让窗口重绘。传 nil 注销。
func (p *Player) SetInvalidate(fn func()) {
	p.mu.Lock()
	p.invalidate = fn
	p.mu.Unlock()
}
func (p *Player) notify() {
	p.mu.Lock()
	fn := p.invalidate
	p.mu.Unlock()
	if fn != nil {
		fn()
	}
}
func (p *Player) send(args []string, load bool) error {
	p.commandMu.Lock()
	defer p.commandMu.Unlock()
	p.mu.Lock()
	if p.state.Phase == Closed {
		p.mu.Unlock()
		return ErrClosed
	}
	if load && p.loading {
		p.mu.Unlock()
		return ErrLoading
	}
	p.requestID++
	id := p.requestID
	p.pending[id] = load
	prior := p.state
	if load {
		p.loadID = id
		p.loading = true
		p.startPending = true
		p.state = State{Source: args[1], Phase: Loading, Volume: prior.Volume, Rate: prior.Rate, Muted: prior.Muted}
	}
	p.mu.Unlock()
	var err error
	if load {
		err = p.native.command(0, []string{"set", "pause", "yes"})
	}
	if err == nil {
		err = p.native.command(id, args)
	}
	if err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		if load && p.state.Phase != Closed {
			p.loading = false
			p.state = prior
		}
		p.mu.Unlock()
	}
	p.notify()
	return err
}

// Load replaces the source. It starts paused; call Play to begin. Paths, file
// URLs and direct media URLs are accepted by libmpv. App scripts/config and
// external URL helper programs are disabled. Empty/NUL-containing sources fail.
func (p *Player) Load(source string) error {
	if strings.TrimSpace(source) == "" || strings.ContainsRune(source, 0) {
		return errors.New("video: empty or NUL-containing source")
	}
	return p.send([]string{"loadfile", source, "replace"}, true)
}
func (p *Player) Play() error {
	if p.State().Phase == Ended {
		if err := p.Seek(0); err != nil {
			return err
		}
	}
	return p.send([]string{"set", "pause", "no"}, false)
}
func (p *Player) Pause() error { return p.send([]string{"set", "pause", "yes"}, false) }
func (p *Player) Stop() error  { return p.send([]string{"stop"}, false) }
func (p *Player) Seek(position time.Duration) error {
	s := p.State()
	if s.Phase == Closed {
		return ErrClosed
	}
	if s.Phase == Loading {
		return ErrLoading
	}
	if !s.Seekable {
		return errors.ErrUnsupported
	}
	if position < 0 {
		return errors.New("video: negative seek position")
	}
	if s.Duration > 0 {
		position = min(position, s.Duration)
	}
	return p.send([]string{"seek", strconv.FormatFloat(position.Seconds(), 'f', 6, 64), "absolute+exact"}, false)
}

// SetOption 在运行时改一个 mpv 选项，走 options/<name> 属性。
//
// ALIVE 本地补丁，只给测量探针用：缓存与同步参数是不是问题，只能靠同一路流、
// 同一时段、只改一个变量来回答。生产代码不要拿它当开关——那会变成一堆没人
// 记得的隐式配置。
func (p *Player) SetOption(name, value string) error {
	if name == "" || strings.ContainsRune(name, 0) || strings.ContainsRune(value, 0) {
		return errors.New("video: 非法的选项名或值")
	}
	return p.send([]string{"set", "options/" + name, value}, false)
}

func (p *Player) SetVolume(volume float64) error {
	if !finite(volume) || volume < 0 || volume > 100 {
		return errors.New("video: volume must be between 0 and 100")
	}
	return p.send([]string{"set", "volume", strconv.FormatFloat(volume, 'f', 2, 64)}, false)
}
func (p *Player) SetMuted(muted bool) error {
	value := "no"
	if muted {
		value = "yes"
	}
	return p.send([]string{"set", "mute", value}, false)
}
func (p *Player) SetRate(rate float64) error {
	if !finite(rate) || rate < 0.25 || rate > 4 {
		return errors.New("video: rate must be between 0.25 and 4")
	}
	return p.send([]string{"set", "speed", strconv.FormatFloat(rate, 'f', 3, 64)}, false)
}

// renderIdleAfter 是"没人取帧"多久之后停止渲染。
//
// 渲染只服务于显示。没有 Video 元素在画这一路时，逐帧做 YUV→RGB 再回读
// 是纯浪费：观测墙隐藏时 12 格各按格子尺寸转一遍，一秒几百 MB 的拷贝，
// 却没有任何地方会看到它。
//
// 300ms 足够厚：一帧的构建间隔、一次布局重排、Tester 的慢帧都不会误判；
// 又足够薄：真正的隐藏在一秒内就省下来了。
//
// ALIVE 本地补丁。恢复靠 Frame()：取帧的人回来了就强制渲一帧，画面立刻
// 追上，不需要重新加载。
const renderIdleAfter = 300 * time.Millisecond

// renderIdle 报告这一刻该不该跳过渲染。
//
// 纯函数，好在测试里穷举：还没渲过第一帧时不跳（那会让 Ready 永远为假），
// 有人刚取过帧时不跳，其余一律跳。
func renderIdle(hasFrame bool, lastPull, now time.Time) bool {
	if !hasFrame || lastPull.IsZero() {
		return false
	}
	return now.Sub(lastPull) > renderIdleAfter
}

// SetPresentSize 把软件渲染目标固定为 w×h（按偶数取整），0 表示按视频原始
// 尺寸渲染。这是呈现的**唯一**旋钮：格子预览、后台收听、播放页全尺寸都走它。
//
// ALIVE 本地补丁，来自上游之外的三个需求：
//
//   - 观测墙：源流档位、解码分辨率、demuxer 都不动，只把呈现尺寸收到格子
//     大小。和把播放器窗口拉小是同一条路径，不是降画质；没有它，整墙每格
//     都会按 1080p 做一次 YUV→RGB，9 路付不起。
//   - 后台收听：收到 160×90 保留解码，回来一帧接上。它替代了 vid=no——
//     取消选择视频轨会让 mpv 冲掉 demuxer 缓存、等下一个关键帧
//     （实测 0.08–5.2 秒），而只缩渲染目标不碰 demuxer、音频时钟和参考帧链，
//     画面一帧就回来，声音一秒不断。
//   - 播放页：0，按视频原始尺寸。
//
// 以前这里还有第二个入口 SetPresenting(on)（全尺寸 / 160×90）。两套并存时，
// 各条路径只记得自己那一套，「墙 → 播放页」就永远停在格子分辨率——整屏
// 都是放大的糊图。合并成一个入口，判断只在上层 presentSizeFor 一处。
func (p *Player) SetPresentSize(w, h int) {
	if w <= 0 || h <= 0 {
		w, h = 0, 0
	} else {
		w = max(w, 2) &^ 1
		h = max(h, 2) &^ 1
	}
	p.mu.Lock()
	p.presentW, p.presentH = w, h
	p.forceRender = true
	p.mu.Unlock()
	select {
	case p.renderWake <- struct{}{}:
	default:
	}
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func seconds(n float64) time.Duration {
	if !finite(n) || n < 0 || n > float64(math.MaxInt64)/1e9 {
		return 0
	}
	return time.Duration(n * 1e9)
}
func (p *Player) apply(ev event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Phase == Closed {
		return
	}
	s := &p.state
	switch ev.kind {
	case 2:
		// mpv 自己的日志。默认只收 error / warn——正常播放一条都没有，出问题时
		// 才是唯一能说明「卡在哪一层」的东西。存进环形缓冲，由上层落盘；
		// ALIVE_MPV_LOG=1 时额外打到 stderr，开发时用。
		// 零值 Player（测试里直接构造）没有 logRank：按默认 warn 处理，
		// 显式设 trace 时 opts.LogLevel 非空，rank 0 仍然有效。
		rank := p.logRank
		if rank == 0 && p.opts.LogLevel == "" {
			rank = mpvLogRank("warn")
		}
		if mpvLogRank(ev.name) >= rank {
			if debugLog {
				println("mpv log:", ev.name, ev.text)
			}
			// 已知无害的上游噪音，只收第一条：B 站的 fMP4 每个分片都重带 init
			// 段，于是每几秒刷一条 duplicated MOOV，实测 60 秒 50 条全是它，
			// 会把真正有用的那几行淹掉。
			if strings.Contains(ev.text, "duplicated MOOV Atom") {
				if p.moovWarned {
					break
				}
				p.moovWarned = true
			}
			p.mpvLogSeq++
			p.mpvLog = append(p.mpvLog, LogLine{Level: ev.name, Prefix: ev.prefix, Text: ev.text, At: time.Now(), Seq: p.mpvLogSeq})
			if len(p.mpvLog) > mpvLogCap {
				p.mpvLog = p.mpvLog[len(p.mpvLog)-mpvLogCap:]
			}
			s.Log = p.mpvLog
		}
	case 1:
		s.Phase = Failed
		s.Err = errors.New("video: native engine shut down")
	case 5:
		load, ok := p.pending[ev.reply]
		delete(p.pending, ev.reply)

		if ok && ev.err != nil && (!load || ev.reply == p.loadID) {
			s.Err = ev.err
			if load {
				s.Phase = Failed
				p.loading = false
			}
		}
	case 6:
		p.startPending = false
		p.entry = ev.entry
		s.Phase = Loading
		p.frame = nil
	case 8:
		p.loading = false
		s.Err = nil
		if p.paused {
			s.Phase = Paused
		} else {
			s.Phase = Playing
		}
	case 7:
		if p.startPending || ev.entry != p.entry {
			return
		}
		p.loading = false
		s.Buffering = false
		s.Seekable = false
		if ev.err != nil {
			s.Phase = Failed
			s.Err = ev.err
		} else if ev.reason == 0 {
			s.Phase = Ended
		} else {
			s.Phase = Idle
			s.Position = 0
			s.Duration = 0
			p.frame = nil
		}
	case 24:
		s.Phase = Failed
		s.Seekable = false
		p.loading = false
		p.startPending = false
		clear(p.pending)
		s.Err = errors.New("video: native event queue overflow")
	case 22:
		if ev.unavailable && ev.name != "time-pos" && ev.name != "duration" && ev.name != "seekable" &&
			!isDiagProperty(ev.name) {
			return
		}
		switch ev.name {
		case "pause":
			p.paused = ev.flag
			if s.Phase == Playing || s.Phase == Paused {
				if ev.flag {
					s.Phase = Paused
				} else {
					s.Phase = Playing
				}
			}
		case "time-pos":
			s.Position = seconds(ev.number)
		case "duration":
			s.Duration = seconds(ev.number)
		case "volume":
			if finite(ev.number) {
				s.Volume = ev.number
			}
		case "speed":
			if finite(ev.number) {
				s.Rate = ev.number
			}
		case "mute":
			s.Muted = ev.flag
		case "seekable":
			s.Seekable = ev.flag
		case "paused-for-cache":
			s.Buffering = ev.flag
		case "audio-pts":
			if !ev.unavailable && finite(ev.number) {
				s.AudioPTS = seconds(ev.number)
				s.AudioPTSValid = true
			} else {
				s.AudioPTS = 0
				s.AudioPTSValid = false
			}
		case "demuxer-cache-duration":
			s.CacheDuration = seconds(ev.number)
		case "demuxer-cache-time":
			s.CacheTime = seconds(ev.number)
		case "cache-buffering-state":
			s.CacheBuffering = ev.ival
		case "demuxer-cache-idle":
			s.CacheIdle = ev.flag
		case "decoder-frame-drop-count":
			s.DecoderDrops = ev.ival
		case "mistimed-frame-count":
			s.Mistimed = ev.ival
		case "display-fps":
			s.DisplayFPS = ev.number
		case "estimated-vf-fps":
			s.VFFPS = ev.number
		case "video-speed-correction":
			s.VideoSpeed = ev.number
		case "audio-speed-correction":
			s.AudioSpeed = ev.number
		case "total-avsync-change":
			s.AVSync = ev.number
		case "hwdec-current":
			if ev.unavailable {
				s.HWDec = ""
			} else {
				s.HWDec = ev.text
			}
		case "dwidth":
			if ev.ival > 0 {
				p.width = int(ev.ival)
			}
		case "dheight":
			if ev.ival > 0 {
				p.height = int(ev.ival)
			}
		case "eof-reached":
			if ev.flag && (s.Phase == Playing || s.Phase == Paused) {
				s.Phase = Ended
			} else if !ev.flag && s.Phase == Ended {
				if p.paused {
					s.Phase = Paused
				} else {
					s.Phase = Playing
				}
			}
		}
	}
}

// isDiagProperty 报告 name 是不是 ALIVE 加的那批连续性诊断属性。
//
// 这些属性在「没有音轨」「没有显示端」时会变成不可用；事件仍然要收下来，
// 并把值清零。保留上一次的旧值更危险——日志会以为音频还在走。
func isDiagProperty(name string) bool {
	switch name {
	case "audio-pts", "hwdec-current", "demuxer-cache-duration", "demuxer-cache-time",
		"cache-buffering-state", "demuxer-cache-idle", "decoder-frame-drop-count",
		"mistimed-frame-count", "display-fps", "estimated-vf-fps",
		"video-speed-correction", "audio-speed-correction", "total-avsync-change":
		return true
	}
	return false
}

func (p *Player) events() {
	defer p.workers.Done()
	for {
		select {
		case <-p.stop:
			return
		case <-p.wake:
		}
		for {
			select {
			case <-p.stop:
				return
			default:
			}
			ev, ok := p.native.next()
			if !ok {
				break
			}
			p.apply(ev)
		}
		p.notify()
	}
}
func (p *Player) frames() {
	defer p.workers.Done()
	for {
		select {
		case <-p.stop:
			return
		case <-p.renderWake:
		}
		p.mu.Lock()
		closed := p.state.Phase == Closed
		force := p.forceRender
		p.forceRender = false
		idle := !force && renderIdle(p.frame != nil, p.lastPull, time.Now())
		p.idle = idle
		p.mu.Unlock()
		if closed {
			return
		}
		if idle {
			// 没人看：不渲、不回读。解码不动。
			//
			// 挂起解码的代价实测（2026-11-23，B 站 7734200，隐藏 12 秒）：
			//   - vd-lavc-skipframe=all：参考帧链断，恢复要等直播流的下一个
			//     关键帧，实测 8–22 秒。墙上就是"回来画面冻十几秒"。
			//   - nonref：参考帧照解，恢复仍要约 2.2 秒（解码器追赶缓存）。
			//   - 不挂起：恢复即刻出画，隐藏期间多花的是解码——零拷贝下
			//     3 路 0.13 核量级。
			//
			// 要省带宽/解码就用用户的「暂离时省流量」，那条路直接关会话，
			// 回来重新加载是用户预期之内的。渲染门控只管渲染。
			continue
		}
		w, h := p.target()
		frame, fresh, err := p.native.render(w, h, force)
		p.mu.Lock()
		p.state.Renders++
		if p.state.Phase != Closed {
			if err == nil {
				if fresh {
					p.frame = frame
					p.serial++
				}
			} else {
				p.state.Err = err
				p.state.Phase = Failed
			}
		}
		p.mu.Unlock()
		p.notify()
	}
}

// target 是这一帧该渲染多大。
//
// 按视频的原始显示尺寸渲染，不跟窗口走：缩放交给上屏那一层在 GPU 采样时做，
// 我们这边少一次重采样，画面也不会随窗口变小而糊掉。超过 Options 的上限才
// 等比缩，防止异常流把内存打爆。
func (p *Player) target() (int, int) {
	// 尺寸来自 dwidth/dheight 的属性事件（mpv 解析出来后主动推），它已经算进
	// 旋转和像素宽高比；事件还没到时用调用方给的值顶着。
	p.mu.Lock()
	w, h := p.width, p.height
	rw, rh := p.presentW, p.presentH
	p.mu.Unlock()
	if w <= 0 || h <= 0 {
		w, h = p.opts.Width, p.opts.Height
	}
	if w > 0 && h > 0 {
		if w > p.opts.MaxWidth || h > p.opts.MaxHeight {
			scale := math.Min(float64(p.opts.MaxWidth)/float64(w), float64(p.opts.MaxHeight)/float64(h))
			w = int(float64(w) * scale)
			h = int(float64(h) * scale)
		}
		if w < 2 {
			w = 2
		}
		if h < 2 {
			h = 2
		}
		w, h = w&^1, h&^1
		// 不写回 p.width/p.height：那是 mpv 报的原始尺寸，写回裁剪结果会让
		// 上限变成单向阀——收得紧、放不回。
		if rw > 0 && rh > 0 {
			return rw &^ 1, rh &^ 1
		}
		return w, h
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.width > 0 && p.height > 0 {
		return p.width, p.height
	}
	return 640, 360
}

// Size 返回最近一次渲染出的画面的尺寸，还没有画面时返回 0,0。
//
// 只读：不刷 lastPull，也不唤醒渲染线程。lastPull 是渲染门控的判据
// （renderIdle），只看尺寸的调用方不该把自己算成「有人在看」——以前
// playback.Engine.Size 借道 Frame()，问一次尺寸就让没人看的格子恢复全尺寸
// 渲染，「没人看就不渲」永远生效不了。
func (p *Player) Size() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frame == nil {
		return 0, 0
	}
	return p.frame.W, p.frame.H
}

// Frame 返回最近一次渲染出的画面与它的序号，还没有画面时返回 nil。
//
// 序号每次成功渲染自增，调用方据此判断有没有换帧——比反复比较像素便宜得多。
// 返回的切片指向引擎内部轮换的缓冲，三次渲染之后才会被覆写，所以调用方可以
// 把它交给上屏线程，不必自己拷。
func (p *Player) Frame() (*Frame, uint64) {
	p.mu.Lock()
	p.lastPull = time.Now()
	resume := p.idle
	p.idle = false
	if resume {
		p.forceRender = true
	}
	f, serial := p.frame, p.serial
	p.mu.Unlock()
	if resume {
		// 从"没人看"回到"有人看"：立刻渲一帧，不然显示的是几百毫秒前的旧画面。
		select {
		case p.renderWake <- struct{}{}:
		default:
		}
	}
	return f, serial
}

// Close is idempotent and returns without waiting on the UI thread. Cleanup
// waits for both workers, frees the render context, then destroys the engine.
func (p *Player) Close() {
	p.mu.Lock()
	if p.state.Phase == Closed {
		p.mu.Unlock()
		return
	}
	p.state.Phase = Closed
	p.frame = nil
	clear(p.pending)
	close(p.stop)
	notify := p.invalidate
	p.invalidate = nil
	p.mu.Unlock()
	if notify != nil {
		notify()
	}
	go func() { p.workers.Wait(); p.commandMu.Lock(); p.native.close(); p.commandMu.Unlock(); close(p.done) }()
}
func (p *Player) Done() <-chan struct{} { return p.done }
