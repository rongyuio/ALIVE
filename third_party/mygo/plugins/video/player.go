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

// debugLog 让 mpv 的错误日志打到 stderr，排障时设 ALIVE_MPV_LOG=1。
var debugLog = os.Getenv("ALIVE_MPV_LOG") != ""

var ErrClosed = errors.New("video: player closed")
var ErrLoading = errors.New("video: a source is still loading")

// Options selects a libmpv library and bounds the software video surface.
//
// Zero MaxWidth/MaxHeight use 3840×2160: 直播最高就到 4K，再大没有实际来源，
// 反而会让每一帧的内存和上传量翻倍。
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
	CacheDuration, CacheTime time.Duration // 读前缓存里有多少秒、当前读头位置
	CacheBuffering           int64         // 0–100，缓冲完成度
	CacheIdle                bool          // demuxer 在等数据（读前缓存空）
	DecoderDrops             int64         // 解码器主动丢的帧
	Mistimed                 int64         // 因音画不同步被判为「迟了」的帧
	DisplayFPS, VFFPS        float64       // 显示器刷新率 / 实际视频帧率
	VideoSpeed, AudioSpeed   float64       // 音视频各自的校正倍率（1.0 = 不校正）
	AVSync                   float64       // 当前音画偏差（秒）
	HWDec                    string        // 实际启用的硬解；空或 "no" 表示软件解码
}

// Player owns a native playback engine, event worker and rendering worker.
// Its methods are safe from any goroutine. Close it when its window closes.
// Commands enqueue asynchronously; State reports the engine's confirmed result.
type Player struct {
	mu                 sync.Mutex
	commandMu          sync.Mutex
	state              State
	native             backend
	opts               Options
	wake, renderWake   chan struct{}
	stop, done         chan struct{}
	workers            sync.WaitGroup
	invalidate         func()
	frame              *Frame
	serial             uint64
	width, height      int
	presentW, presentH int  // 非零时按这个尺寸渲染（后台听收不解全尺寸画面）
	forceRender        bool // 尺寸变化后立刻用当前帧重渲染一次
	requestID          uint64
	loadID             uint64
	pending            map[uint64]bool // true for a load request
	loading            bool
	startPending       bool
	entry              int64
	paused             bool
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
	p.start()
	return p, nil
}
func makePlayer(opts Options) *Player {
	return &Player{opts: opts, state: State{Phase: Idle, Volume: 100, Rate: 1}, paused: true, wake: make(chan struct{}, 1), renderWake: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}), pending: map[uint64]bool{}, width: 0, height: 0}
}
func (p *Player) start()       { p.workers.Add(2); go p.events(); go p.frames(); signal(p.wake) }
func (p *Player) State() State { p.mu.Lock(); defer p.mu.Unlock(); return p.state }
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

// SetPresenting turns full-size presentation on or off without touching the
// video track or the audio output. Off keeps decoding at a small render size;
// the last frame is still updated, but at 160×90, and nobody is meant to show
// it. On restores the video's own size and re-renders the current frame at
// once, so the next pull is a full-size picture.
//
// alive added this on top of upstream for background listening. It replaces
// vid=no: deselecting the track made mpv flush its demuxer cache and wait for
// the stream's next keyframe on return (0.08–5.2s measured on a live HLS).
// Keeping the track selected and only shrinking the render target leaves the
// demuxer, the audio clock and the decoded reference chain untouched, so the
// picture resumes within one frame and the sound never stops.
func (p *Player) SetPresenting(on bool) {
	p.mu.Lock()
	if on {
		p.presentW, p.presentH = 0, 0
	} else {
		p.presentW, p.presentH = 160, 90
	}
	p.forceRender = true
	p.mu.Unlock()
	select {
	case p.renderWake <- struct{}{}:
	default:
	}
}

// SetPresentSize 把软件渲染目标固定为 w×h（按偶数取整）。
//
// ALIVE 本地补丁，给观测墙用：源流档位、解码分辨率、demuxer 都不动，只把
// 呈现尺寸收到格子大小。它和把播放器窗口拉小是同一条路径，不是降画质；
// 没有它，整墙每格都会按 1080p 做一次 YUV→RGB，9 路付不起。
//
// 传 0 回到视频原始尺寸，等价于 SetPresenting(true)。
func (p *Player) SetPresentSize(w, h int) {
	if w <= 0 || h <= 0 {
		p.SetPresenting(true)
		return
	}
	if w < 2 {
		w = 2
	}
	if h < 2 {
		h = 2
	}
	p.mu.Lock()
	p.presentW, p.presentH = w&^1, h&^1
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
		// mpv 的错误日志。默认不打——正常播放时它什么也不说，出问题时才是
		// 唯一能说明「卡在哪一层」的东西。ALIVE_MPV_LOG=1 打开。
		if debugLog && (ev.name == "error" || ev.name == "fatal") {
			println("mpv log:", ev.name, ev.text)
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
			} else {
				s.AudioPTS = 0
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
		p.mu.Unlock()
		if closed {
			return
		}
		w, h := p.target()
		frame, fresh, err := p.native.render(w, h, force)
		p.mu.Lock()
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
		p.mu.Lock()
		p.width, p.height = w, h
		p.mu.Unlock()
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

// Frame 返回最近一次渲染出的画面与它的序号，还没有画面时返回 nil。
//
// 序号每次成功渲染自增，调用方据此判断有没有换帧——比反复比较像素便宜得多。
// 返回的切片指向引擎内部轮换的缓冲，三次渲染之后才会被覆写，所以调用方可以
// 把它交给上屏线程，不必自己拷。
func (p *Player) Frame() (*Frame, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.frame, p.serial
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
