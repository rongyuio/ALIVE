//go:build windows

package relay

import (
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

// 绕过缓存重复真实探测，覆盖两种 subtype 以及没有注册解码器的类型。
// 无崩溃与结果稳定不等于已经证明系统 DLL 内部无泄漏。
func TestProbeNativeVideoDecoderRepeatable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subtype nativeVideoGUID
		cached  func() bool
	}{
		{"HEVC", nativeVideoFormatHEVC, nativeHEVCAvailable},
		{"AV1", nativeVideoFormatAV1, nativeAV1Available},
		{"unknown", nativeVideoGUID{0x958d4c49, 0x9d66, 0x4c07, [8]byte{0xaa, 0x35, 0x8a, 0x57, 0xa8, 0x7f, 0x41, 0x20}}, func() bool { return false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first := probeNativeVideoDecoder(tc.subtype)
			for i := 0; i < 3; i++ {
				if probeNativeVideoDecoder(tc.subtype) != first {
					t.Fatal("重复探测结果不一致")
				}
			}
			if first != tc.cached() {
				t.Fatal("真实探测与缓存或不支持类型的契约不一致")
			}
			t.Logf("%v", first)
		})
	}
}

// 用最小 COM 表验证激活、关闭、释放的顺序和失败路径，不依赖系统解码器。
func TestNativeVideoActivationCleanup(t *testing.T) {
	for _, tc := range []struct {
		name          string
		activationHR  uintptr
		shutdownHR    uintptr
		withTransform bool
		want          bool
		order         []string
	}{
		{"success", 0, 0, true, true, []string{"activate", "shutdown", "release"}},
		{"activation-failed", 0x80004005, 0, true, false, []string{"activate", "release"}},
		{"null-transform", 0, 0, false, false, []string{"activate", "shutdown"}},
		{"shutdown-failed", 0, 0x80004005, true, false, []string{"activate", "shutdown", "release"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			var transformTable [3]uintptr
			transform := &struct{ table *uintptr }{&transformTable[0]}
			transformTable[nativeComRelease] = syscall.NewCallback(func(_ unsafe.Pointer) uintptr {
				order = append(order, "release")
				return 0
			})
			var activateTable [35]uintptr
			activate := &struct{ table *uintptr }{&activateTable[0]}
			activateTable[nativeActivateMFT] = syscall.NewCallback(func(_ unsafe.Pointer, _ unsafe.Pointer, out unsafe.Pointer) uintptr {
				order = append(order, "activate")
				if tc.withTransform {
					*(*unsafe.Pointer)(out) = unsafe.Pointer(transform)
				}
				return tc.activationHR
			})
			activateTable[nativeShutdownMFT] = syscall.NewCallback(func(_ unsafe.Pointer) uintptr {
				order = append(order, "shutdown")
				return tc.shutdownHR
			})
			if got := nativeActivateTransform(unsafe.Pointer(activate)); got != tc.want {
				t.Fatalf("激活结果 %v，预期 %v", got, tc.want)
			}
			runtime.KeepAlive(transform)
			runtime.KeepAlive(activate)
			if !reflect.DeepEqual(order, tc.order) {
				t.Fatalf("资源释放顺序 %v，预期 %v", order, tc.order)
			}
		})
	}
}

// 调用方已经建立 STA 时，探测必须沿用该套间且不能减掉调用方的引用。
func TestNativeVideoProbePreservesSTA(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := nativeProcCoInitializeEx.Call(0, 2)
	if nativeHRESULTFailed(hr) {
		t.Skip("当前线程无法建立 STA")
	}
	defer nativeProcCoUninitialize.Call()
	probeNativeVideoDecoder(nativeVideoFormatHEVC)
	probeNativeVideoDecoder(nativeVideoFormatAV1)
	hr, _, _ = nativeProcCoInitializeEx.Call(0, nativeCoInitMultithreaded)
	if uint32(hr) != nativeRPCChangedMode {
		if !nativeHRESULTFailed(hr) {
			nativeProcCoUninitialize.Call()
		}
		t.Fatal("探测改变了调用方的 STA 套间")
	}
}
