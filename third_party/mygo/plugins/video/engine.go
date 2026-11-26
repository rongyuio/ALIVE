package video

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo/plugins/internal/contentlib"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"
)

// Frame 是一帧 NV12 画面。Luma 是 Y 平面，Chroma 是交错的 UV 平面。
//
// 为什么是 NV12 而不是 RGBA：1080p 一帧 NV12 是 3MB，RGBA 是 8.3MB；60fps 下
// 带宽差 320MB/s。MyGo 的 Bitmap 本来就走 NV12 上屏（GPU 做色彩转换），中间
// 转一道 RGBA 纯属白烧 CPU。mpv 的软件渲染直接支持输出 NV12。
//
// Luma/Chroma 指向引擎内部复用的缓冲，下一次 render 就会被覆写。调用方要么
// 在这一轮用完，要么自己拷走。
type Frame struct {
	Luma, Chroma []byte
	Stride       int
	W, H         int
	// Shared 非零时这一帧在 GPU 上：mpv 直接渲进了这块共享纹理，
	// 渲染器用同一个 device 打开它就能零拷贝采样。Luma/Stride 此时为 nil。
	//
	// ALIVE 本地补丁，见 d3d11_windows.go。
	Shared uintptr
	// SharedID 标识这块纹理，跨帧不变时渲染器可以复用打开的资源。
	SharedID uint64
	// ReadBack 把这一帧读成 premultiplied RGBA，给没有 GPU 的消费者
	// （截图、CPU 光栅器）用；Shared 为 0 的帧为 nil。
	//
	// ALIVE 本地补丁。没有它，截图路径拿不到零拷贝帧的像素：真实窗口
	// 走 GPU 采样看着正常，截图里这一块却是空的。
	ReadBack func(dst []byte) []byte
}

type backend interface {
	command(uint64, []string) error
	next() (event, bool)
	// render 返回这一帧与它是否是新画出来的。mpv 没有新画面时不会写输出缓冲，
	// 这时候必须复用上一帧，否则拿到的是还没被写过的空内存。
	render(int, int, bool) (*Frame, bool, error)
	close()
}
type logMessage struct {
	prefix, level, text unsafe.Pointer
	logLevel            int32
	_                   int32
}
type event struct {
	unavailable bool
	kind        int32
	reply       uint64
	err         error
	name        string
	prefix      string
	text        string
	number      float64
	ival        int64
	flag        bool
	entry       int64
	reason      int32
}
type nativeEvent struct {
	kind, error int32
	reply       uint64
	data        unsafe.Pointer
}
type property struct {
	name   unsafe.Pointer
	format int32
	_      int32
	data   unsafe.Pointer
}
type endFile struct {
	reason, error int32
	entry         int64
}
type renderParam struct {
	kind int32
	_    int32
	data unsafe.Pointer
}
type engine struct {
	library      uintptr
	version      func() uint32
	create       func() uintptr
	option       func(uintptr, string, string) int32
	initialize   func(uintptr) int32
	destroy      func(uintptr)
	command      func(uintptr, uint64, unsafe.Pointer) int32
	wait         func(uintptr, float64) *nativeEvent
	observe      func(uintptr, uint64, string, int32) int32
	requestLog   func(uintptr, string) int32
	wake         func(uintptr, uintptr, uintptr)
	errorString  func(int32) unsafe.Pointer
	renderCreate func(*uintptr, uintptr, *renderParam) int32
	renderUpdate func(uintptr) uint64
	render       func(uintptr, *renderParam) int32
	renderWake   func(uintptr, uintptr, uintptr)
	renderFree   func(uintptr)
}

var libraryMu sync.Mutex
var libraries = map[string]*engine{}
var librariesByHandle = map[uintptr]*engine{}
var callbackOnce sync.Once
var callback uintptr
var callbackID atomic.Uint64
var routes sync.Map

// One callback per signature for the whole process. It routes by integer user
// data, and does nothing except wake Go workers; no native API or UI runs here.
func route(ch chan struct{}) uintptr {
	callbackOnce.Do(func() {
		callback = contentlib.Callback(func(id uintptr) uintptr {
			if c, ok := routes.Load(id); ok {
				select {
				case c.(chan struct{}) <- struct{}{}:
				default:
				}
			}
			// 必须返回 uintptr：purego 在 Windows 上把 NewCallback 转给
			// syscall.NewCallback，它只接受「一个 uintptr 返回值」的签名，
			// 无返回值的函数在注册时直接 panic。mpv 不读这个值。
			return 0
		})
	})
	id := uintptr(callbackID.Add(1))
	routes.Store(id, ch)
	return id
}
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
func loadEngine(path string) (*engine, error) {
	libraryMu.Lock()
	defer libraryMu.Unlock()
	if e := libraries[path]; e != nil {
		return e, nil
	}
	names := []string{"libmpv.so.2", "libmpv.so.1"}
	switch runtime.GOOS {
	case "darwin":
		names = []string{"libmpv.2.dylib", "/opt/homebrew/lib/libmpv.2.dylib", "/usr/local/lib/libmpv.2.dylib"}
	case "windows":
		names = []string{"libmpv-2.dll", "mpv-2.dll"}
	}
	h, err := contentlib.Open(path, names...)
	if err != nil {
		return nil, fmt.Errorf("video: %w", err)
	}
	if e := librariesByHandle[h]; e != nil {
		contentlib.Release(h)
		libraries[path] = e
		return e, nil
	}
	e := &engine{library: h}
	for _, s := range []struct {
		name string
		fn   any
	}{{"mpv_client_api_version", &e.version}, {"mpv_create", &e.create}, {"mpv_set_option_string", &e.option}, {"mpv_initialize", &e.initialize}, {"mpv_destroy", &e.destroy}, {"mpv_command_async", &e.command}, {"mpv_wait_event", &e.wait}, {"mpv_observe_property", &e.observe}, {"mpv_request_log_messages", &e.requestLog}, {"mpv_set_wakeup_callback", &e.wake}, {"mpv_error_string", &e.errorString}, {"mpv_render_context_create", &e.renderCreate}, {"mpv_render_context_update", &e.renderUpdate}, {"mpv_render_context_render", &e.render}, {"mpv_render_context_set_update_callback", &e.renderWake}, {"mpv_render_context_free", &e.renderFree}} {
		if err = contentlib.Bind(h, s.name, s.fn); err != nil {
			contentlib.Release(h)
			return nil, err
		}
	}
	// API v2 adds stable playlist entry IDs used to reject replaced-file events.
	if e.version()>>16 != 2 {
		contentlib.Release(h)
		return nil, errors.New("video: libmpv client API v2 required")
	}
	libraries[path] = e
	librariesByHandle[h] = e
	return e, nil
}
func (e *engine) err(code int32) error {
	if code >= 0 {
		return nil
	}
	return fmt.Errorf("video: %s (%d)", contentlib.CString(e.errorString(code)), code)
}

type native struct {
	e                *engine
	handle, renderer uintptr
	wakeID, renderID uintptr
	// bufs 是 NV12 输出缓冲池，轮换复用。
	//
	// 为什么要三个而不是一个：渲染在 mpv 的 worker 线程上，上屏在 UI 线程上，
	// 两者之间还隔着 GPU 的异步采样。只用一块内存，等于在 GPU 读它的同时
	// 让渲染线程往里写——屏幕上就是两帧叠在一起。三块轮换，每次覆写的都是
	// 两帧之前那块，那一刻的 GPU 命令早就执行完了。
	bufs [3][]byte
	slot int
	// last 是上一次真正画出来的帧。mpv 没有新画面时复用它。
	last *Frame
	// gpu 非空时走 D3D11 零拷贝路径（ALIVE 本地补丁，见 d3d11_windows.go）。
	gpu *gpuTarget
}

func newNative(opts Options, wake, renderWake chan struct{}) (backend, error) {
	e, err := loadEngine(opts.Library)
	if err != nil {
		return nil, err
	}
	n := &native{e: e, handle: e.create()}
	if n.handle == 0 {
		return nil, errors.New("video: mpv_create failed")
	}
	fail := func(err error) (backend, error) { n.close(); return nil, err }
	// 关键选项：设不上就起不来，直接报错。
	for _, o := range [][2]string{{"config", "no"}, {"load-scripts", "no"}, {"ytdl", "no"}, {"input-default-bindings", "no"}, {"input-terminal", "no"}, {"terminal", "no"}, {"vo", "libmpv"}, {"keep-open", "yes"}, {"idle", "yes"}, {"pause", "yes"}, {"osd-level", "0"}} {
		if err = e.err(e.option(n.handle, o[0], o[1])); err != nil {
			return fail(err)
		}
	}
	// 调优选项：设不上就算了。mpv 的选项组随 VO 和构建版本变化，写死一个名字
	// 在别的版本上会让整个播放起不来——这个代价太大。
	//
	// hwdec：SW 渲染下解出的帧要下到内存再转 RGB。后台收听现在保留解码、
	// 只把渲染目标收小，硬解把后台单核从 22.9–33.4% 降到 7.0–9.8%（1080p60）；
	// 前台 22.6–23.3%，与软件解码的 23.2–49.5% 持平或更好，丢帧/失时都是 0
	// （2026-11-21，B 站 545068）。auto-safe 在驱动不支持时退回软件，不会起不来。
	// scale/dscale：SW 渲染是单线程 CPU 做缩放，默认 bicubic 在 1080p 上把帧率
	// 压到 40fps 上下；bilinear 便宜一个数量级，直播这个码率下看不出区别。
	// sws-fast：让 swscale 走快速路径（构建支持才有效）。
	for _, o := range [][2]string{{"hwdec", "auto-safe"}, {"scale", "bilinear"}, {"dscale", "bilinear"}, {"sws-fast", "yes"}} {
		_ = e.option(n.handle, o[0], o[1])
	}
	// BLOCK_FOR_TARGET_TIME 关掉之后的补偿（见下面 render 里的 block=0）。
	//
	// render.h 对 MPV_RENDER_PARAM_BLOCK_FOR_TARGET_TIME=0 写得很明确：关掉
	// 渲染里的阻塞等待之后，要么在渲染线程里按 mpv_render_frame_info 的
	// target time 自己等，要么「把 video-timing-offset 设成 0」。两条等价，
	// 选后者：另一条要在渲染线程每帧读 target_time、自己算还差多久、再接一遍
	// mpv 的唤醒——多一份时钟逻辑，坏起来是音画缓慢漂移，比一个选项难查得多。
	//
	// 和调优项一样，设不上不致命：退回 mpv 默认的 50ms 提前量，A/V 同步略偏，
	// 但这一路照常播。所以留一行日志而不是 fail。
	//
	// 实测（2026-11-26，B 站 545068 720P，d3d11va-copy，audio 时钟有效）：
	// 同一条流上 offset=0 与 offset=0.050 各采 20s，total-avsync-change /
	// mistimed-frame-count / decoder-drop-count 全部为 0，两种设置看不出差别。
	// 原因是我们的渲染是「有帧就渲、按需呈现」，不落在 vsync-locked 的
	// display-resample 那一支——render.h 警告的「A/V 略偏」在这条管线上没有
	// 可观测载体。保留设为 0 只是把那个警告的前提彻底消掉，不指望它改同步。
	if err := e.err(e.option(n.handle, "video-timing-offset", "0")); err != nil {
		slog.Warn("video: video-timing-offset 设不上，A/V 同步可能略偏", "err", err)
	}
	// 缓冲抽干后的恢复门槛。mpv 默认 cache-pause-wait=1.0：缓存一空就要重新
	// 攒满 1 秒才继续播。直播分片本来就只有 1 秒，等 1 秒等于每次抖动都至少
	// 卡一秒，所以收到 0.2。
	//
	// 这里**不**设 demuxer-readahead-secs / http_persistent / live_start_index。
	// 三个都实测过，在 libmpv 这条路上都不兑现：
	//   - readahead-secs：选项读回正确（5.000000），但 hls 的缓冲深度不吃它；
	//   - http_persistent / http_multiple：mpv 自己的日志说
	//     「Disabling http_persistent due to custom io_open」——libmpv 的
	//     嵌入式用法强制自定义 io_open，连接复用和并行下载被 mpv 主动关掉，
	//     去掉请求头也一样，没有开关可绕；
	//   - live_start_index：单次样本结论相反，不足以下结论。
	// 每片新建连接（实测 380ms vs 复用 40ms）才是直播卡顿的主因，而它在这条
	// 路上无解。详见 doc/decisions/0009-live-continuity.md「第五轮」。
	_ = e.option(n.handle, "cache-pause-wait", "0.2")
	// 回退缓存：直播不能回退，这份缓存永远不会被读到。ALIVE 本地补丁，
	// 只在调用方明确要求时关掉（观测墙一屏最多 12 路，每路各留一份纯浪费）。
	// 前向缓存不动，抗抖动靠的是它。
	if opts.NoBackBuffer {
		_ = e.option(n.handle, "demuxer-max-back-bytes", "0")
	}
	// 直播流几乎都要回源请求头（B站/虎牙/斗鱼的 Referer、抖音的 UA）。mpv 自己
	// 不会编这些，必须由调用方给。http-header-fields 是逗号分隔的列表，每个
	// 元素原样作为一行 header 发出去。
	if len(opts.Headers) > 0 {
		if err = e.err(e.option(n.handle, "http-header-fields", strings.Join(opts.Headers, ","))); err != nil {
			return fail(err)
		}
	}
	// 音量合成器里的会话名。不设的话 Windows 音量面板上会冒出一个小喇叭叫
	// mpv，用户看到一个没见过的进程在出声。设不上就算了：最坏只是名字没变，
	// 播放和音量控制不受影响。
	if opts.ClientName != "" {
		_ = e.option(n.handle, "audio-client-name", opts.ClientName)
		_ = e.option(n.handle, "title", opts.ClientName)
	}
	if opts.Silent {
		if err = e.err(e.option(n.handle, "ao", "null")); err != nil {
			return fail(err)
		}
	}
	if err = e.err(e.initialize(n.handle)); err != nil {
		return fail(err)
	}
	// 订阅 mpv 自己的日志。不订阅就一条都收不到——mpv 默认不向客户端推。
	// 默认 warn：info 级别的 hls / 解复用消息正常播放时也在刷，会把日志淹掉。
	// 排障时由 playback.Options.MPVLog 提高到 info / v。
	logLevel := opts.LogLevel
	if logLevel == "" {
		logLevel = "warn"
	}
	if err := e.err(e.requestLog(n.handle, logLevel)); err != nil {
		slog.Warn("video: mpv 日志订阅失败", "level", logLevel, "err", err)
	}
	// 优先 D3D11 零拷贝：mpv 直接渲进共享纹理，帧不经内存。
	// 没有渲染器 device（headless / Tester / GPU 不可用）时退回 SW。
	if t := newGPUTarget(opts.GPU); t != nil {
		if err := t.createRenderer(e, n.handle); err == nil {
			n.gpu = t
			slog.Debug("video: 零拷贝路径", "device", fmt.Sprintf("%#x", t.device))
		} else {
			// 零拷贝建不出来时不能静默退回：那一路会出现「GPU=true 但
			// GPUFrames=0」的黑格，日志必须留下原因。
			slog.Info("video: 零拷贝不可用，退回软件渲染",
				"event", "engine.gpu_fallback", "cause", "create_renderer", "err", err)
			t.release()
		}
	}
	if n.gpu != nil {
		n.renderer = n.gpu.renderer
	} else {
		// mpv 要求 API 类型串以 NUL 结尾。
		api := append([]byte("sw"), 0)
		params := []renderParam{{kind: 1, data: unsafe.Pointer(&api[0])}, {}}
		if err = e.err(e.renderCreate(&n.renderer, n.handle, &params[0])); err != nil {
			return fail(fmt.Errorf("video: libmpv software render API unavailable: %w", err))
		}
		runtime.KeepAlive(api)
	}
	for i, o := range []struct {
		name   string
		format int32
	}{{"pause", 3}, {"time-pos", 5}, {"duration", 5}, {"volume", 5}, {"mute", 3}, {"speed", 5}, {"seekable", 3}, {"eof-reached", 3}, {"paused-for-cache", 3}, {"dwidth", 4}, {"dheight", 4}} {
		if err = e.err(e.observe(n.handle, uint64(i+1), o.name, o.format)); err != nil {
			return fail(err)
		}
	}
	// ALIVE 本地补丁：连续性诊断。上游只关心「有没有画面」，这十来个属性是
	// 为了把「上游断供 / 音频断流 / 呈现抖动」分开——只靠 time-pos 和
	// paused-for-cache，音频停了、帧被丢了都看不出来。
	//
	// 和上面的调优选项一样：设不上就跳过，不能因为别的 mpv 构建少一个属性
	// 就让整路播不起来。
	for i, o := range []struct {
		name   string
		format int32
	}{
		{"audio-pts", 5},                // 音频时钟，判断「有没有声音」的唯一可靠来源
		{"hwdec-current", 1},            // 实际启用的硬解（auto-safe 可能退回软件）
		{"demuxer-cache-duration", 5},   // 读前缓存秒数
		{"demuxer-cache-time", 5},       // 缓存读头位置
		{"cache-buffering-state", 4},    // 0–100
		{"demuxer-cache-idle", 3},       // 读前缓存空、在等数据
		{"decoder-frame-drop-count", 4}, // 解码器丢帧
		{"mistimed-frame-count", 4},     // 迟到的帧
		{"display-fps", 5},              // 呈现端刷新率
		{"estimated-vf-fps", 5},         // 实际视频帧率
		{"video-speed-correction", 5},   // 视频时钟校正倍率
		{"audio-speed-correction", 5},   // 音频时钟校正倍率
		{"total-avsync-change", 5},      // 音画偏差
	} {
		_ = e.observe(n.handle, uint64(100+i), o.name, o.format)
	}
	n.wakeID = route(wake)
	n.renderID = route(renderWake)
	e.wake(n.handle, callback, n.wakeID)
	e.renderWake(n.renderer, callback, n.renderID)
	return n, nil
}
func (n *native) command(id uint64, args []string) error {
	storage := make([][]byte, len(args))
	pointers := make([]unsafe.Pointer, len(args)+1)
	for i, s := range args {
		storage[i] = append([]byte(s), 0)
		pointers[i] = unsafe.Pointer(&storage[i][0])
	}
	err := n.e.err(n.e.command(n.handle, id, unsafe.Pointer(&pointers[0])))
	runtime.KeepAlive(storage)
	runtime.KeepAlive(pointers)
	return err
}
func (n *native) next() (event, bool) {
	raw := n.e.wait(n.handle, 0)
	if raw == nil || raw.kind == 0 {
		return event{}, false
	}
	ev := event{kind: raw.kind, reply: raw.reply, err: n.e.err(raw.error)}
	switch raw.kind {
	case 2:
		if raw.data != nil {
			m := *(*logMessage)(raw.data)
			ev.name = contentlib.CString(m.level)
			ev.prefix = contentlib.CString(m.prefix)
			ev.text = contentlib.CString(m.text)
		}
	case 6:
		if raw.data != nil {
			ev.entry = *(*int64)(raw.data)
		}
	case 7:
		if raw.data != nil {
			d := *(*endFile)(raw.data)
			ev.entry = d.entry
			ev.reason = d.reason
			ev.err = n.e.err(d.error)
		}
	case 22:
		if raw.data != nil {
			p := *(*property)(raw.data)
			ev.name = contentlib.CString(p.name)
			if p.data != nil {
				switch p.format {
				case 1:
					if str := *(*unsafe.Pointer)(p.data); str != nil {
						ev.text = contentlib.CString(str)
					}
				case 3:
					ev.flag = *(*int32)(p.data) != 0
				case 4:
					ev.ival = *(*int64)(p.data)
				case 5:
					ev.number = *(*float64)(p.data)
				}
			} else {
				ev.unavailable = true
			}
		}
	}
	return ev, true
}
func (n *native) render(w, h int, force bool) (*Frame, bool, error) {
	// mpv_render_context_update 返回位标志：带 MPV_RENDER_UPDATE_FRAME（1）才
	// 表示有新画面。这一点必须看——没有新画面时 mpv 不会写输出缓冲，照拿就会
	// 把一块还没画过的空内存当成一帧交出去。预览截图、抓帧这类「在某一刻额外
	// 要一帧」的路径最容易撞上，屏幕上看起来就是突然全黑。
	update := n.e.renderUpdate(n.renderer)
	if update&1 == 0 && (!force || n.last == nil) {
		if n.last != nil {
			return n.last, false, nil
		}
		return nil, false, nil
	}

	// 零拷贝：mpv 直接渲进共享纹理，交出去的是一块显存，不是内存。
	if n.gpu != nil {
		handle, id, err := n.gpu.render(n.e, w, h)
		if err != nil {
			return nil, false, err
		}
		if handle == 0 {
			// 纹理正被渲染器读着，这一拍让过去，复用上一帧。
			if n.last != nil {
				return n.last, false, nil
			}
			return nil, false, nil
		}
		g := n.gpu
		n.last = &Frame{W: w, H: h, Shared: handle, SharedID: id,
			ReadBack: func(dst []byte) []byte { return g.readPixels(id, w, h, dst) }}
		return n.last, true, nil
	}

	// SW 渲染只接受 RGB 格式（"bgr0" 之类），不接受 nv12：render.h 里写明了
	// 「internally marked as RGB, has exactly 1 plane」，NV12 两个平面，传过去
	// 直接 MPV_ERROR_NOT_SUPPORTED。
	w = (w + 1) &^ 1
	h = (h + 1) &^ 1
	// SW_STRIDE 的类型是 size_t*，单位是字节。bgr0 是 4 字节/像素。
	//
	// render.h 要求「stride 和指针都按 64 对齐」（快 SIMD 路径），否则最坏
	// 整帧拷一遍。这里的取整已经把它满足了：stride 是 64 的倍数，而 w、h 在
	// 上面都取成了偶数，所以 need = stride*h 恒为 128 的倍数；make 出来的
	// 切片首地址由 Go 分配器给出，至少 64 对齐（实测审计结论：不要为此加
	// 手动对齐，对齐不是问题所在）。
	stride := (w*4 + 63) &^ 63
	need := stride * h
	n.slot = (n.slot + 1) % len(n.bufs)
	if cap(n.bufs[n.slot]) < need {
		n.bufs[n.slot] = make([]byte, need)
	}
	buf := n.bufs[n.slot][:need]

	size := [2]int32{int32(w), int32(h)}
	st := uintptr(stride)
	format := []byte("bgr0\x00")
	// BLOCK_FOR_TARGET_TIME 关掉：渲染由 render context 的更新回调驱动，再在这里
	// 等一次目标时间只会把画面推后一帧。
	block := int32(0)
	params := []renderParam{
		{kind: 17, data: unsafe.Pointer(&size[0])},
		{kind: 18, data: unsafe.Pointer(&format[0])},
		{kind: 19, data: unsafe.Pointer(&st)},
		{kind: 20, data: unsafe.Pointer(&buf[0])},
		{kind: 12, data: unsafe.Pointer(&block)},
		{},
	}
	err := n.e.err(n.e.render(n.renderer, &params[0]))
	runtime.KeepAlive(format)
	if err != nil {
		return nil, false, err
	}
	// bgr0 是单平面：B G R 0 四字节一组。
	n.last = &Frame{
		Luma:   buf,
		Stride: stride,
		W:      w,
		H:      h,
	}
	return n.last, true, nil
}

func (n *native) close() {
	if n.gpu != nil {
		n.gpu.release()
		n.gpu = nil
	}
	if n.handle != 0 {
		n.e.wake(n.handle, 0, 0)
	}
	routes.Delete(n.wakeID)
	routes.Delete(n.renderID)
	if n.renderer != 0 {
		n.e.renderWake(n.renderer, 0, 0)
		n.e.renderFree(n.renderer)
		n.renderer = 0
	}
	if n.handle != 0 {
		n.e.destroy(n.handle)
		n.handle = 0
	}
}
