//go:build windows

package relay

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 能力探测只依据系统枚举与激活结果，不依据房间、扩展包名或流地址。
// 解码器能够创建，不代表任意 profile、位深或封装一定能播放。
const (
	// SDK 10.0.26100.0 um\mfapi.h：保持原探测的 MFT_ENUM_FLAG_ALL 范围。
	nativeEnumFlagAll         = 0x3f
	nativeMFVersion           = 0x00020070
	nativeMFStartupFull       = 0
	nativeCoInitMultithreaded = 0
	nativeRPCChangedMode      = 0x80010106
	// SDK um\mfobjects.h：IUnknown 三项、IMFAttributes 三十项之后是激活与关闭。
	nativeComRelease  = 2
	nativeActivateMFT = 33
	nativeShutdownMFT = 34
)

type nativeVideoGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// SDK 的 MFT_REGISTER_TYPE_INFO 由两个连续 GUID 组成。
type nativeMFTRegisterTypeInfo struct {
	MajorType nativeVideoGUID
	Subtype   nativeVideoGUID
}

var (
	// 以下 GUID 均核对自本机 SDK 10.0.26100.0 um\mfapi.h、mftransform.h。
	nativeCategoryVideoDecoder = nativeVideoGUID{0xd6c02d4b, 0x6833, 0x45b4, [8]byte{0x97, 0x1a, 0x05, 0xa4, 0xb0, 0x4b, 0xab, 0x91}}
	nativeMediaTypeVideo       = nativeVideoGUID{0x73646976, 0, 0x0010, [8]byte{0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}}
	// MFVideoFormat_HEVC = FCC('HEVC')。
	nativeVideoFormatHEVC = nativeVideoGUID{0x43564548, 0, 0x0010, [8]byte{0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}}
	// MFVideoFormat_AV1 = FCC('AV01')，不是 FCC('AV1 ')。
	nativeVideoFormatAV1  = nativeVideoGUID{0x31305641, 0, 0x0010, [8]byte{0x80, 0, 0, 0xaa, 0, 0x38, 0x9b, 0x71}}
	nativeIIDIMFTransform = nativeVideoGUID{0xbf94c121, 0x5b05, 0x4e6f, [8]byte{0x80, 0, 0xba, 0x59, 0x89, 0x61, 0x41, 0x4d}}
	hevcOnce              sync.Once
	hevcCached            bool
	av1Once               sync.Once
	av1Cached             bool

	// 限定系统目录，避免从应用目录加载同名 DLL。
	nativeMFPlat             = windows.NewLazySystemDLL("mfplat.dll")
	nativeOle32              = windows.NewLazySystemDLL("ole32.dll")
	nativeProcMFTEnumEx      = nativeMFPlat.NewProc("MFTEnumEx")
	nativeProcMFStartup      = nativeMFPlat.NewProc("MFStartup")
	nativeProcMFShutdown     = nativeMFPlat.NewProc("MFShutdown")
	nativeProcCoInitializeEx = nativeOle32.NewProc("CoInitializeEx")
	nativeProcCoUninitialize = nativeOle32.NewProc("CoUninitialize")
	nativeProcCoTaskMemFree  = nativeOle32.NewProc("CoTaskMemFree")
)

// nativeHEVCAvailable 可由任意 goroutine 调用，成功或失败都缓存到进程退出。
func nativeHEVCAvailable() bool {
	hevcOnce.Do(func() { hevcCached = probeNativeHEVC() })
	return hevcCached
}

// nativeAV1Available 与 HEVC 独立缓存，不假定系统已经安装 AV1 解码器。
func nativeAV1Available() bool {
	av1Once.Do(func() { av1Cached = probeNativeVideoDecoder(nativeVideoFormatAV1) })
	return av1Cached
}

func probeNativeHEVC() bool {
	return probeNativeVideoDecoder(nativeVideoFormatHEVC)
}

// probeNativeVideoDecoder 的 subtype 是 SDK 定义的视频编码 GUID。
func probeNativeVideoDecoder(subtype nativeVideoGUID) bool {
	// 先确认所有入口，避免缺少媒体组件时 LazyProc.Call 直接 panic。
	for _, proc := range []*windows.LazyProc{
		nativeProcMFTEnumEx, nativeProcMFStartup, nativeProcMFShutdown,
		nativeProcCoInitializeEx, nativeProcCoUninitialize, nativeProcCoTaskMemFree,
	} {
		if proc.Find() != nil {
			return false
		}
	}
	// COM 套间属于 OS 线程；释放与反初始化也必须在同一线程完成。
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := nativeProcCoInitializeEx.Call(0, nativeCoInitMultithreaded)
	switch uint32(hr) {
	case 0, 1:
		// S_FALSE 也增加初始化计数，必须配对反初始化。
		defer nativeProcCoUninitialize.Call()
	case nativeRPCChangedMode:
		// 沿用调用方已建立的 STA，不扣除调用方的初始化计数。
	default:
		return false
	}
	if hr, _, _ := nativeProcMFStartup.Call(nativeMFVersion, nativeMFStartupFull); nativeHRESULTFailed(hr) {
		return false
	}
	defer nativeProcMFShutdown.Call()

	input := nativeMFTRegisterTypeInfo{MajorType: nativeMediaTypeVideo, Subtype: subtype}
	var list unsafe.Pointer
	var count uint32
	hr = nativeEnumVideoDecoders(&input, &list, &count)
	// 成功或失败都回收已返回的数组与接口；清理先于 MFShutdown 和 CoUninitialize。
	if list != nil {
		defer func() {
			for _, activate := range unsafe.Slice((*unsafe.Pointer)(list), int(count)) {
				nativeCOMRelease(activate)
			}
			nativeProcCoTaskMemFree.Call(uintptr(list))
		}()
	}
	if nativeHRESULTFailed(hr) || list == nil || count == 0 {
		return false
	}
	for _, activate := range unsafe.Slice((*unsafe.Pointer)(list), int(count)) {
		if activate != nil && nativeActivateTransform(activate) {
			return true
		}
	}
	return false
}

// MFTEnumEx 的 category 按值传递：amd64 间接传 16 字节结构，386 分成
// 四个栈参数，arm64 分成两个寄存器，不能在所有架构上一律传 GUID 指针。
func nativeEnumVideoDecoders(input *nativeMFTRegisterTypeInfo, list *unsafe.Pointer, count *uint32) uintptr {
	var hr uintptr
	switch runtime.GOARCH {
	case "amd64":
		hr, _, _ = nativeProcMFTEnumEx.Call(
			uintptr(unsafe.Pointer(&nativeCategoryVideoDecoder)), nativeEnumFlagAll,
			uintptr(unsafe.Pointer(input)), 0, uintptr(unsafe.Pointer(list)), uintptr(unsafe.Pointer(count)))
	case "386":
		words := *(*[4]uint32)(unsafe.Pointer(&nativeCategoryVideoDecoder))
		hr, _, _ = nativeProcMFTEnumEx.Call(
			uintptr(words[0]), uintptr(words[1]), uintptr(words[2]), uintptr(words[3]), nativeEnumFlagAll,
			uintptr(unsafe.Pointer(input)), 0, uintptr(unsafe.Pointer(list)), uintptr(unsafe.Pointer(count)))
	case "arm64":
		words := *(*[2]uint64)(unsafe.Pointer(&nativeCategoryVideoDecoder))
		hr, _, _ = nativeProcMFTEnumEx.Call(
			uintptr(words[0]), uintptr(words[1]), nativeEnumFlagAll,
			uintptr(unsafe.Pointer(input)), 0, uintptr(unsafe.Pointer(list)), uintptr(unsafe.Pointer(count)))
	default:
		return 0x80004001 // E_NOTIMPL：未知 ABI 不冒险调用。
	}
	return hr
}

// 只读取所需槽位，避免把较短的 IMFTransform 表当成 IMFActivate 表。
// 对象和表全程保留原始指针，只在系统调用参数处转 uintptr。
func nativeCOMMethod(obj unsafe.Pointer, index int) uintptr {
	table := *(**uintptr)(obj)
	return unsafe.Slice(table, index+1)[index]
}

func nativeCOMRelease(obj unsafe.Pointer) {
	if obj != nil {
		syscall.SyscallN(nativeCOMMethod(obj, nativeComRelease), uintptr(obj))
	}
}

// 激活对象与返回接口分别持有引用，先关闭对象再释放返回接口。
// 失败返回的非空接口同样释放；ShutdownObject 失败时保守判为不可用。
func nativeActivateTransform(activate unsafe.Pointer) bool {
	var transform unsafe.Pointer
	// SyscallN 不保证输出槽逃逸；COM 调用可能回调 Go 并扩栈，固定槽位避免写入旧栈。
	var pinned runtime.Pinner
	pinned.Pin(&transform)
	defer pinned.Unpin()
	hr, _, _ := syscall.SyscallN(
		nativeCOMMethod(activate, nativeActivateMFT), uintptr(activate),
		uintptr(unsafe.Pointer(&nativeIIDIMFTransform)), uintptr(unsafe.Pointer(&transform)))
	defer func() { nativeCOMRelease(transform) }()
	if nativeHRESULTFailed(hr) {
		return false
	}
	shutdownHR, _, _ := syscall.SyscallN(nativeCOMMethod(activate, nativeShutdownMFT), uintptr(activate))
	return transform != nil && !nativeHRESULTFailed(shutdownHR)
}

// HRESULT 的符号位位于低 32 位，不依赖 uintptr 宽度。
func nativeHRESULTFailed(hr uintptr) bool { return int32(uint32(hr)) < 0 }
