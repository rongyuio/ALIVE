// Package playback 用 libmpv 播放直播流，做成 MyGo 的媒体引擎。
//
// 这里曾经自己写解封装、解码、重采样、音频环、视频帧槽和内容时钟——用 cgo 直接
// 调 libav。那条路走到最后是：解码线程 99% 的时间在等空帧槽，音频一饿死内容时钟
// 就冻住，画面跟着停。同一个流用 ffmpeg 命令行解，8 秒的内容 1.3 秒跑完——差别
// 不在解码本身，在调度。
//
// mpv 把调度解决了二十年。我们现在只是借它：取流、把回源请求头交给它、把 NV12
// 帧拿出来上屏。音频直接由 mpv 输出，我们不碰；时钟、缓冲、音画同步、丢帧策略
// 全在里面。
package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo/media"
	"github.com/egoist/mygo/plugins/video"
	"github.com/egoist/mygo/scene"
)

// ErrUnsupported 表示这一路起不来：地址为空，或者 libmpv 不接受它。
var ErrUnsupported = errors.New("playback: 该流无法自带解码")

// Options 是一次播放的选项。
type Options struct {
	// Headers 是回源请求头，例如 "Referer: https://live.bilibili.com/"。
	Headers []string
	// 以下三项是平台声明值，只作参考；实际尺寸以 mpv 解析出来的为准。
	Width     int
	Height    int
	FrameRate float64
}

// Engine 是一路直播的播放引擎，实现 media.Engine。
type Engine struct {
	p    *video.Player
	done chan struct{}

	mu     sync.Mutex
	serial uint64
	frames uint64
	closed bool
	gate   audioGate
}

// audioGateBudget 是「等音频起步」的上限。
//
// 4K 起播时 mpv 会先解出画面、音频管线还落在后面（1x 供片下实测 0.9 秒），
// 于是出现一段「画面在动、没有声音」。这道门把画面压到音频时钟走起来为止。
// 上限是兜底：没有音轨的流、或者音频起不来时，不能永远不给画面。
const audioGateBudget = 2 * time.Second

// audioClientName 是播放器向系统注册的名字，也就是 Windows 音量合成器里
// 显示的应用名。mpv 默认用「mpv」，面板上就是一路陌生的喇叭。
const audioClientName = "ALIVE"

// audioGate 决定「已经解出来的画面现在能不能交出去」。
//
// 判据是音频时钟有没有走起来：mpv 里 time-pos 由音频驱动（video-sync=audio），
// 它还是 0 就说明声音一个采样都还没出来。1080p / 2K 上 time-pos 和首帧几乎
// 同时到，这道门一次都不触发；4K 上它会压住画面直到声音接上。
type audioGate struct {
	started bool
	// since 是第一次「有画面但音频还没起步」的时刻；零值表示还没进过门。
	since time.Time
}

// allow 报告这一帧能否交给上屏。pos 是音频时钟。
func (g *audioGate) allow(pos time.Duration, now time.Time) bool {
	if g.started {
		return true
	}
	if pos > 0 {
		g.started = true
		return true
	}
	if g.since.IsZero() {
		g.since = now
	}
	if now.Sub(g.since) > audioGateBudget {
		g.started = true
		return true
	}
	return false
}

// Open 打开一路直播并开始播放。
//
// 返回时 mpv 已经收到 loadfile，首帧要等 Frame 第一次给出画面。
func Open(ctx context.Context, upstream string, opts Options) (*Engine, error) {
	if upstream == "" {
		return nil, fmt.Errorf("%w: 上游地址为空", ErrUnsupported)
	}
	lib, err := libraryPath()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	p, err := video.New(video.Options{Library: lib, Headers: opts.Headers, Width: opts.Width, Height: opts.Height, ClientName: audioClientName})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if err := p.Load(upstream); err != nil {
		p.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	// 直接播：直播没有「准备好再开始」这一步，loadfile 之后越早开始读，
	// 首帧越早。暂停着等反而会多一轮状态切换。
	if err := p.Play(); err != nil {
		p.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}

	e := &Engine{p: p, done: make(chan struct{})}
	go func() {
		select {
		case <-ctx.Done():
			_ = e.Close()
		case <-p.Done():
		}
		close(e.done)
	}()
	return e, nil
}

// libraryPath 找 libmpv 的库文件。
//
// 默认放在可执行文件旁边，和 mygo 分发 resources 的方式一致。走的是运行时
// dlopen 而不是链接期依赖，所以缺了它进程照样起得来，只是这一路报错、界面
// 给可重试的终态。没有兜底路径——那正是我们想要的失败方式。
//
// ALIVE_MPV_LIB 可以指定绝对路径，排障和测试用。
func libraryPath() (string, error) {
	if v := os.Getenv("ALIVE_MPV_LIB"); v != "" {
		return v, nil
	}
	name := "libmpv-2.dll"
	switch runtime.GOOS {
	case "darwin":
		name = "libmpv.2.dylib"
	case "linux":
		name = "libmpv.so.2"
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("找不到 %s（设 ALIVE_MPV_LIB 可指定其他位置）", path)
	}
	return path, nil
}

// SetWake 注册「有新画面或状态变了」的回调，通常是让窗口重绘。
func (e *Engine) SetWake(wake func()) {
	e.p.SetInvalidate(wake)
}

// Frame 返回当前该显示的画面。
//
// changed 表示这是新的一帧（mpv 的渲染序号变了）。ok 为 false 表示还没有画面，
// 或者画面已经到了但音频还没起步（见 audioGate）：后者让加载态多压一会儿，
// 换来「画面出现时声音同时出现」。
// 返回的 NV12 平面指向 mpv 引擎内部轮换的缓冲，三帧之后才会被覆写。
func (e *Engine) Frame(t time.Time) (media.Picture, bool, bool) {
	f, serial := e.p.Frame()
	if f == nil {
		return media.Picture{}, false, false
	}
	if !e.allowFrame() {
		return media.Picture{}, false, false
	}
	e.mu.Lock()
	changed := serial != e.serial
	if changed {
		e.serial = serial
		e.frames++
	}
	e.mu.Unlock()

	w, h := f.W, f.H
	// mpv 的软件渲染只吐 RGB（render.h 里只认 "bgr0"/"rgb0"/"rgb24" 这类
	// 单平面 RGB），所以这里是 BGRA 单平面，不是 NV12。
	surface := &scene.Surface{
		Format: scene.SurfaceBGRA,
		Planes: [2]scene.Plane{
			{Pix: f.Luma, Stride: f.Stride},
		},
	}
	return media.Picture{
		Surface: surface,
		W:       w,
		H:       h,
		RGBA:    func(dst []byte) []byte { return rgbaOpaque(dst, f) },
	}, changed, true
}

// Interval 是呈现端的取帧节拍。
func (e *Engine) Interval() time.Duration { return 16 * time.Millisecond }

// allowFrame 问一次音频门：音频时钟还没走起来就先不给画面。
func (e *Engine) allowFrame() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.gate.allow(e.p.State().Position, time.Now())
}

// State 报告加载与播放状态。
func (e *Engine) State() (media.State, error) {
	s := e.p.State()
	switch s.Phase {
	case video.Closed, video.Ended:
		return media.Ended, nil
	case video.Failed:
		if s.Err != nil {
			return media.Failed, s.Err
		}
		return media.Failed, errors.New("playback: 播放失败")
	case video.Idle, video.Loading:
		return media.Loading, nil
	}
	if s.Buffering {
		return media.Buffering, nil
	}
	return media.Ready, nil
}

// Size 是当前画面的尺寸，还没有画面时返回 0,0。
func (e *Engine) Size() (int, int) {
	if f, _ := e.p.Frame(); f != nil {
		return f.W, f.H
	}
	return 0, 0
}

// Duration 对直播没有意义。
func (e *Engine) Duration() time.Duration { return 0 }

// Position 是已经播到的位置。
func (e *Engine) Position() time.Duration { return e.p.State().Position }

// Play 恢复播放。
func (e *Engine) Play() { _ = e.p.Play() }

// Pause 停住播放。
func (e *Engine) Pause() { _ = e.p.Pause() }

// Seek 在直播里没有意义，保留接口。
func (e *Engine) Seek(time.Duration) {}

// SetLoop 对直播没有意义。
func (e *Engine) SetLoop(bool) {}

// SetRate 设置倍速。
func (e *Engine) SetRate(r float64) {
	if r > 0 {
		_ = e.p.SetRate(r)
	}
}

// SetOption 在运行时改一个 mpv 选项，只给测量探针用。
//
// 连续性调查要回答「加缓存是不是把冻结换成延迟」，只能在同一条流上只改一个
// 变量复测。生产代码不调用它。
func (e *Engine) SetOption(name, value string) error { return e.p.SetOption(name, value) }

// SetVolume 设置音量，0 到 1。mpv 用的是 0 到 100。
func (e *Engine) SetVolume(v float64) {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	_ = e.p.SetVolume(v * 100)
}

// SetMuted 静音或取消静音。
func (e *Engine) SetMuted(m bool) { _ = e.p.SetMuted(m) }

// SetPresenting 开关全尺寸呈现。
//
// false 时视频轨照常解码，只把软件渲染目标收到 160×90；true 恢复视频原始
// 尺寸并立刻用当前帧重渲染一次。它不碰 vid、不碰 demuxer、不碰 AO：
// 重新选轨会让 mpv 清空读前缓存并等下一个关键帧（实测 0.08–5.2 秒），
// 而这里音频和参考帧链都没动，回来只差一帧。
func (e *Engine) SetPresenting(on bool) { e.p.SetPresenting(on) }

// Debug 返回底层 libmpv 的原始状态，诊断用。
func (e *Engine) Debug() video.State { return e.p.State() }

// Stats 返回累计上屏帧数。丢帧由 mpv 内部处理，这里不统计；audioOn 恒为真，
// 因为音轨的有无由 mpv 决定，我们不参与。
func (e *Engine) Stats() (frames, dropped uint64, audioOn bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frames, 0, true
}

// Close 释放这一路。可重复调用。
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()
	e.p.Close()
	return nil
}

// Done 在这一路彻底停下后关闭。
func (e *Engine) Done() <-chan struct{} { return e.done }

// rgbaOpaque 把 bgr0 的一帧转成 premultiplied RGBA，alpha 一律填 255。
//
// 为什么不直接用 Surface.RGBA：mpv 的软件渲染在 "bgr0" 里输出的第四个字节是
// 不确定值（文档原话是那个 0 字节「contains uninitialized garbage」），实测为 0。
// 真实窗口走 GPU 采样，着色器不看 alpha，所以看着正常；而软件光栅化（截图验收、
// 没有 GPU 时的 CPU 渲染器）按预乘 alpha 混合，alpha 为 0 等于整块全透明——
// 播放区就成了纯黑。这一趟只在 CPU 路径走，每帧几毫秒，可以接受。
func rgbaOpaque(dst []byte, f *video.Frame) []byte {
	need := 4 * f.W * f.H
	if cap(dst) < need {
		dst = make([]byte, need)
	}
	dst = dst[:need]
	for y := 0; y < f.H; y++ {
		src := f.Luma[y*f.Stride:][:4*f.W]
		out := dst[4*f.W*y:][:4*f.W]
		for x := 0; x < 4*f.W; x += 4 {
			out[x], out[x+1], out[x+2], out[x+3] = src[x+2], src[x+1], src[x], 255
		}
	}
	return dst
}
