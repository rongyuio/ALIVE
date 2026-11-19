package video

import (
	"errors"
	"fmt"
	"github.com/egoist/mygo/plugins/internal/contentlib"
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
}

type backend interface {
	command(uint64, []string) error
	next() (event, bool)
	// render 返回这一帧与它是否是新画出来的。mpv 没有新画面时不会写输出缓冲，
	// 这时候必须复用上一帧，否则拿到的是还没被写过的空内存。
	render(int, int) (*Frame, bool, error)
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
	}{{"mpv_client_api_version", &e.version}, {"mpv_create", &e.create}, {"mpv_set_option_string", &e.option}, {"mpv_initialize", &e.initialize}, {"mpv_destroy", &e.destroy}, {"mpv_command_async", &e.command}, {"mpv_wait_event", &e.wait}, {"mpv_observe_property", &e.observe}, {"mpv_set_wakeup_callback", &e.wake}, {"mpv_error_string", &e.errorString}, {"mpv_render_context_create", &e.renderCreate}, {"mpv_render_context_update", &e.renderUpdate}, {"mpv_render_context_render", &e.render}, {"mpv_render_context_set_update_callback", &e.renderWake}, {"mpv_render_context_free", &e.renderFree}} {
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
	// hwdec：SW 渲染下解出的帧还要下到内存再转 RGB，但解码本身硬解仍然快得多。
	// scale/dscale：SW 渲染是单线程 CPU 做缩放，默认 bicubic 在 1080p 上把帧率
	// 压到 40fps 上下；bilinear 便宜一个数量级，直播这个码率下看不出区别。
	// sws-fast：让 swscale 走快速路径（构建支持才有效）。
	for _, o := range [][2]string{{"hwdec", "no"}, {"scale", "bilinear"}, {"dscale", "bilinear"}, {"sws-fast", "yes"}} {
		_ = e.option(n.handle, o[0], o[1])
	} // 直播流几乎都要回源请求头（B站/虎牙/斗鱼的 Referer、抖音的 UA）。mpv 自己
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
	api := []byte("sw\x00")
	params := []renderParam{{kind: 1, data: unsafe.Pointer(&api[0])}, {}}
	if err = e.err(e.renderCreate(&n.renderer, n.handle, &params[0])); err != nil {
		return fail(fmt.Errorf("video: libmpv software render API unavailable: %w", err))
	}
	runtime.KeepAlive(api)
	for i, o := range []struct {
		name   string
		format int32
	}{{"pause", 3}, {"time-pos", 5}, {"duration", 5}, {"volume", 5}, {"mute", 3}, {"speed", 5}, {"seekable", 3}, {"eof-reached", 3}, {"paused-for-cache", 3}, {"dwidth", 4}, {"dheight", 4}} {
		if err = e.err(e.observe(n.handle, uint64(i+1), o.name, o.format)); err != nil {
			return fail(err)
		}
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
func (n *native) render(w, h int) (*Frame, bool, error) {
	// mpv_render_context_update 返回位标志：带 MPV_RENDER_UPDATE_FRAME（1）才
	// 表示有新画面。这一点必须看——没有新画面时 mpv 不会写输出缓冲，照拿就会
	// 把一块还没画过的空内存当成一帧交出去。预览截图、抓帧这类「在某一刻额外
	// 要一帧」的路径最容易撞上，屏幕上看起来就是突然全黑。
	if n.e.renderUpdate(n.renderer)&1 == 0 && n.last != nil {
		return n.last, false, nil
	}

	// SW 渲染只接受 RGB 格式（"bgr0" 之类），不接受 nv12：render.h 里写明了
	// 「internally marked as RGB, has exactly 1 plane」，NV12 两个平面，传过去
	// 直接 MPV_ERROR_NOT_SUPPORTED。
	w = (w + 1) &^ 1
	h = (h + 1) &^ 1
	// SW_STRIDE 的类型是 size_t*，单位是字节。bgr0 是 4 字节/像素。
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
