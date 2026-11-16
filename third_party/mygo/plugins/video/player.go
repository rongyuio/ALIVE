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
	wake, renderWake chan struct{}
	stop, done       chan struct{}
	workers          sync.WaitGroup
	invalidate       func()
	frame            *Frame
	serial           uint64
	width, height    int
	requestID        uint64
	loadID           uint64
	pending          map[uint64]bool // true for a load request
	loading          bool
	startPending     bool
	entry            int64
	paused           bool
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

// SetVideo turns the video track on or off, its sound playing either way.
//
// Off is vid=no: mpv stops decoding pictures and its render context stops
// waking the window, while the audio output keeps going on its own thread
// and the clock keeps to the sound. On is vid=auto, which picks the track
// again and decodes from the next keyframe — a live stream shows nothing
// for however long that takes.
//
// alive added this on top of upstream for background listening: leaving
// the player page keeps the stream's sound and drops its pictures, and
// nothing else in this package reached the vid option.
func (p *Player) SetVideo(on bool) error {
	value := "auto"
	if !on {
		value = "no"
	}
	return p.send([]string{"set", "vid", value}, false)
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
		if ev.unavailable && ev.name != "time-pos" && ev.name != "duration" && ev.name != "seekable" {
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
		p.mu.Unlock()
		if closed {
			return
		}
		w, h := p.target()
		frame, fresh, err := p.native.render(w, h)
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
