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

	// MaxWidth / MaxHeight 限制软件渲染目标的尺寸；零表示按视频原始尺寸。
	//
	// 观测墙用它：源流档位和解码分辨率都不动，只把呈现尺寸收到格子大小。
	// 这不是降画质——和把播放器窗口拉小是同一条路径。
	MaxWidth, MaxHeight int
	// Silent 使用空音频输出（ao=null）。测试和只取画面的探针用。
	Silent bool
	// StartSilent 在起播前把音量归零。观测墙的非激活格子要开真实 AO，但不能
	// 出声；先归零再 loadfile，避免起播瞬间漏出一声。
	//
	// 不能用 SetMuted：mpv 的 mute 和 volume 是两个独立开关，之后把音量从 0
	// 调回来并不会解除 mute。墙上激活时只走 SetVolume，于是切过去仍然没声音
	// ——这个 bug 已经发生过一次。静音一律用 volume 0。
	StartSilent bool
	// SkipAudioGate 不压画面等音频时钟。墙上的格子只要画面，等音频会白压两秒。
	SkipAudioGate bool
	// GPU 走零拷贝（mpv 的 D3D11 render API，直接渲进共享纹理）。
	//
	// 只有观测墙开：mpv 直接渲进共享 D3D11 纹理，MyGo 的渲染器用另一个
	// device 打开它，帧不经内存（12 路 8.7 核 → 0.4 核，见
	// doc/reference/measurements.md「视频零拷贝」）。播放页一路全尺寸，收益
	// 小，且截图 / Tester 的 CPU 回读路径留着更方便。
	GPU bool
	// NoBackBuffer 关掉 demuxer 的回退缓存。
	//
	// 直播不能回退，这份缓存永远不会被读到，却按 mpv 的默认上限占着内存。
	// 观测墙一屏最多 12 路，每路各留一份是纯浪费。对画面没有影响：前向
	// 缓存（抗抖动那段）不动。
	NoBackBuffer bool
}

// Engine 是一路直播的播放引擎，实现 media.Engine。
type Engine struct {
	p    *video.Player
	done chan struct{}

	mu     sync.Mutex
	serial uint64
	frames uint64
	// calls 是 Frame 被调用的次数。和 frames（真正产出了新画面的次数）一起
	// 用，才能分清「视图根本没在取帧」和「取了帧但解码器没给新画面」——
	// 直播画面停住时这两种完全不同，不能靠猜。
	calls  uint64
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

// destroyWaitBudget 是等待上一路 mpv 销毁完成的上限。
//
// 正常情况下 video.Player.Close 的清理是毫秒级；这个上限只防「销毁线程卡住
// 把整个应用一起拖住」，不是给慢启动留的余量。
const destroyWaitBudget = 5 * time.Second

var (
	destroyMu sync.Mutex
	destroyCh = map[uint64]<-chan struct{}{}
	destroyID uint64
)

// noteDestroy 登记一路正在销毁的引擎。Open 创建新实例前会等这些通道关闭，
// 保证上一个实例的 AO / mpv 真正释放完——两个实例的销毁和初始化交叠时，
// 新实例会打不开音频、video-sync=audio 的时钟直接停住。
func noteDestroy(done <-chan struct{}) {
	destroyMu.Lock()
	destroyID++
	id := destroyID
	destroyCh[id] = done
	destroyMu.Unlock()
	go func() {
		<-done
		destroyMu.Lock()
		delete(destroyCh, id)
		destroyMu.Unlock()
	}()
}

// waitDestroys 等所有正在销毁的引擎结束，最多 destroyWaitBudget。
func waitDestroys() {
	destroyMu.Lock()
	chans := make([]<-chan struct{}, 0, len(destroyCh))
	for _, c := range destroyCh {
		chans = append(chans, c)
	}
	destroyMu.Unlock()
	if len(chans) == 0 {
		return
	}
	deadline := time.NewTimer(destroyWaitBudget)
	defer deadline.Stop()
	for _, c := range chans {
		select {
		case <-c:
		case <-deadline.C:
			return
		}
	}
}

// Open 打开一路直播并开始播放。
//
// 返回时 mpv 已经收到 loadfile，首帧要等 Frame 第一次给出画面。
func Open(ctx context.Context, upstream string, opts Options) (*Engine, error) {
	if upstream == "" {
		return nil, fmt.Errorf("%w: 上游地址为空", ErrUnsupported)
	}
	// 上一个实例可能还在释放 AO / mpv；等它真正结束再建新的。
	waitDestroys()
	lib, err := libraryPath()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	p, err := video.New(video.Options{
		Library:      lib,
		Headers:      opts.Headers,
		Width:        opts.Width,
		Height:       opts.Height,
		MaxWidth:     opts.MaxWidth,
		MaxHeight:    opts.MaxHeight,
		Silent:       opts.Silent,
		ClientName:   audioClientName,
		NoBackBuffer: opts.NoBackBuffer,
		GPU:          opts.GPU,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	e := &Engine{p: p, done: make(chan struct{})}
	if opts.SkipAudioGate || opts.Silent {
		e.gate.started = true
	}
	if opts.StartSilent {
		if err := p.SetVolume(0); err != nil {
			p.Close()
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
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

	go func() {
		select {
		case <-ctx.Done():
			_ = e.Close()
		case <-p.Done():
		}
		// ctx 分支里 Close 只是发起销毁，真正的 workers.Wait + mpv_destroy
		// 在 video.Player 自己的 goroutine 里。Done 必须等它结束，否则
		// 「Done 表示这一路彻底停下」这句注释就是假的。
		<-p.Done()
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
	e.mu.Lock()
	e.calls++
	e.mu.Unlock()
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
	// 零拷贝：这一帧在 GPU 上，直接把共享纹理的句柄交给渲染器，它用同一个
	// device 打开，不经内存。RGBA 仍要留一条转换路径——CPU 渲染器（Tester、
	// headless、截图）没有 GPU，只能读回像素。
	//
	// 回读只在有人真要像素时发生（截图、CPU 光栅化），不在渲染热路径上；
	// 拿不到就返回空，让截图显示底色，而不是拿错帧。
	if f.Shared != 0 {
		surface := &scene.Surface{
			Format:   scene.SurfaceBGRA,
			Native:   f.Shared,
			NativeID: f.SharedID,
		}
		readback := f.ReadBack
		return media.Picture{
			Surface: surface,
			W:       w,
			H:       h,
			RGBA: func(dst []byte) []byte {
				if readback == nil {
					return dst[:0]
				}
				if pix := readback(dst); pix != nil {
					return pix
				}
				return dst[:0]
			},
		}, changed, true
	}
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

// SetMuted 静音或取消静音。mygo 的 media.Engine 接口要求这个方法，但应用层
// 不该用它：mpv 的 mute 和 volume 是两个独立开关，之后把音量从 0 调回来并
// 不会解除 mute——观测墙激活时只走 SetVolume，于是切过去仍然没声音。要静音
// 一律把音量置 0，见 Options.StartSilent。
func (e *Engine) SetMuted(m bool) { _ = e.p.SetMuted(m) }

// SetPresentSize 是呈现的**唯一**入口：格子像素、160×90、0（视频原始尺寸）
// 都走它。以前还有一个 SetPresenting(on) 表示「全尺寸 / 160×90」，两套并存，
// 各条路径只记得自己那一套，墙 → 播放页就永远停在格子分辨率。
//
// 观测墙的每一格用它：源流与解码分辨率不变，只缩小呈现目标。切换尺寸时
// 引擎会用当前帧立刻重渲染一次，格子上不会经过一帧黑。
func (e *Engine) SetPresentSize(w, h int) { e.p.SetPresentSize(w, h) }

// Debug 返回底层 libmpv 的原始状态，诊断用。
func (e *Engine) Debug() video.State { return e.p.State() }

// GPU 报告这一路是不是零拷贝路径。ALIVE 本地补丁。
func (e *Engine) GPU() bool { return e.p.State().GPU }

// Stats 返回累计上屏帧数。丢帧由 mpv 内部处理，这里不统计；audioOn 恒为真，
// 因为音轨的有无由 mpv 决定，我们不参与。
func (e *Engine) Stats() (frames, dropped uint64, audioOn bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frames, 0, true
}

// Calls 返回 Frame 被调用过多少次，诊断用。和 Stats 的 frames 相减就能
// 看出取帧节拍有没有在跑：calls 不涨 = 视图没在取帧；calls 涨而 frames
// 不涨 = 取了帧但解码器没有新画面。
func (e *Engine) Calls() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
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
	// 登记给下一次 Open：新实例要等这一路的 mpv / AO 真正释放完。
	noteDestroy(e.p.Done())
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
