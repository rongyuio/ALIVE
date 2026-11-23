//go:build windows

// 视频零拷贝：mpv 的 D3D11 render API。
//
// 默认路径是 MPV_RENDER_API_TYPE_SW——mpv 把每一帧渲进一块 Go 的 []byte，
// 再上传成纹理。硬解时那一帧本来就在显存里，于是每帧要来回跨界两次：
//
//	GPU 解码 → 显存 --拷贝--> 内存 bgr0 --上传--> GPU 纹理
//
// 这条路径让 mpv 直接渲进一块**共享** D3D11 纹理，MyGo 的渲染器用自己
// 的 device 打开它，零拷贝。
//
// 为什么是"各自一个 device"而不是共用一个：mpv 的 render_d3d11.h 写着
// 「mpv renders with the immediate context of the device ... Other threads
// must not use the immediate context while an mpv_render_* function runs」。
// 共用 device 等于共用一个非线程安全的 immediate context，而我们和渲染器
// 分别在两个线程上画——2026-11-23 实测出来就是整格纯黑。
//
// 所以：插件自己建 device 解码 + 渲染 → 共享纹理（NT handle + keyed
// mutex）→ 渲染器用它的 device 打开。这正是 MyGo 自己的
// internal/gpu/d3d11/video_test.go 验证过的形态。
package video

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/egoist/mygo/internal/gpu/d3d11/device"
)

// D3D11 render param 的类型号，来自 render.h。
const (
	paramD3D11InitParams = 21
	paramD3D11FBO        = 22
)

// mpv_d3d11_init_params：render_d3d11.h 里只有一个 device 字段。
type d3d11InitParams struct {
	Device uintptr // ID3D11Device*
}

// mpv_d3d11_fbo：同样只有一个 tex 字段。
type d3d11FBO struct {
	Texture uintptr // ID3D11Texture2D*
}

// ptr 把 uintptr 还原成指针，只在自己的栈帧里用。直接 uintptr → unsafe.Pointer
// 会被 vet 判成可疑（unsafeptr）；上游 internal/gpu/d3d11 的 call 同款写法。
func ptr(a uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&a)) }

// comCall 按 COM 约定调一次方法：对象的第一个字是 vtable，方法是 vtable[n]，
// 第一个参数是对象自己。
//
// 调用方把 &结构体 转成 uintptr 传进来。没有 uintptrescapes，GC 看不见这些
// 转换后的指针，可能在调用途中把结构体收走——上游同名 helper 也标了这一条。
//
//go:uintptrescapes
func comCall(obj uintptr, method int, args ...uintptr) uintptr {
	if obj == 0 {
		return 0
	}
	vtbl := *(*uintptr)(ptr(obj))
	fn := *(*uintptr)(unsafe.Add(ptr(vtbl), method*int(unsafe.Sizeof(uintptr(0)))))
	all := append([]uintptr{obj}, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func failedHR(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func releaseCom(obj uintptr) { comCall(obj, 2) } // IUnknown::Release

const (
	devCreateTexture2D   = 5
	bindShaderResource   = 0x8
	bindRenderTarget     = 0x20
	miscSharedKeyedMutex = 0x100
	miscSharedNTHandle   = 0x800
	formatBGRA           = 87
	sharedReadWrite      = 0x80000000 | 1
	acquireSync          = 8
	releaseSync          = 9
	keyedAcquireWaitMS   = 20
)

var (
	iidIDXGIResource1 = [16]byte{
		0x79, 0x13, 0x96, 0x30, 0x09, 0x46, 0x41, 0x4a,
		0x99, 0x8e, 0x54, 0xfe, 0x56, 0x7e, 0xe0, 0xc1,
	}
	// 0x9d8e1289-0xd7b3-465f-8126-250e349af85d。和
	// internal/gpu/d3d11/video.go 的 iidIDXGIKeyedMutex 同一份——那一份是
	// 渲染器读共享纹理时用的，两边必须一致。
	iidIDXGIKeyedMutex = [16]byte{
		0x89, 0x12, 0x8e, 0x9d, 0xb3, 0xd7, 0x5f, 0x46,
		0x81, 0x26, 0x25, 0x0e, 0x34, 0x9a, 0xf8, 0x5d,
	}
)

// newDecoderDevice 建一个用于解码与渲染的 D3D11 device。
//
// 用 MyGo 的 device.NewForVideo：带视频支持位，硬解可用；建不出硬件
// device 时它会退回 WARP（软件光栅），那时零拷贝没有意义，调用方
// 应当退回 SW 渲染。
func newDecoderDevice() (dev, ctx uintptr, software bool, err error) {
	return device.NewForVideo()
}

// newSharedTexture 在 dev 上建一块 w×h 的共享纹理，返回纹理、keyed mutex
// 与 NT 共享句柄。
func newSharedTexture(dev uintptr, w, h int) (tex, mutex, handle uintptr, err error) {
	type desc struct {
		Width, Height              uint32
		MipLevels, ArraySize       uint32
		Format                     uint32
		SampleCount, SampleQuality uint32
		Usage                      uint32
		BindFlags                  uint32
		CPUAccess, MiscFlags       uint32
	}
	d := desc{
		Width: uint32(w), Height: uint32(h), MipLevels: 1, ArraySize: 1,
		Format: formatBGRA, SampleCount: 1,
		BindFlags: bindShaderResource | bindRenderTarget,
		MiscFlags: miscSharedKeyedMutex | miscSharedNTHandle,
	}
	if r := comCall(dev, devCreateTexture2D, uintptr(unsafe.Pointer(&d)), 0,
		uintptr(unsafe.Pointer(&tex))); failedHR(r) {
		return 0, 0, 0, fmt.Errorf("video: 建共享纹理失败 %#x", uint32(r))
	}
	if r := comCall(tex, 0, uintptr(unsafe.Pointer(&iidIDXGIKeyedMutex)),
		uintptr(unsafe.Pointer(&mutex))); failedHR(r) {
		releaseCom(tex)
		return 0, 0, 0, fmt.Errorf("video: 共享纹理没有 keyed mutex %#x", uint32(r))
	}
	var res uintptr
	if r := comCall(tex, 0, uintptr(unsafe.Pointer(&iidIDXGIResource1)),
		uintptr(unsafe.Pointer(&res))); failedHR(r) {
		releaseCom(mutex)
		releaseCom(tex)
		return 0, 0, 0, fmt.Errorf("video: 取不到 IDXGIResource1 %#x", uint32(r))
	}
	defer releaseCom(res)
	if r := comCall(res, 13, 0, sharedReadWrite, 0,
		uintptr(unsafe.Pointer(&handle))); failedHR(r) {
		releaseCom(mutex)
		releaseCom(tex)
		return 0, 0, 0, fmt.Errorf("video: 共享句柄失败 %#x", uint32(r))
	}
	return tex, mutex, handle, nil
}

func closeHandle(h uintptr) {
	if h != 0 {
		_ = syscall.CloseHandle(syscall.Handle(h))
	}
}
