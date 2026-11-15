package relay

import (
	"runtime"
	"sync"
	"testing"
)

// 并发首次调用必须只探测一次，且所有 goroutine 拿到同一个结果。
func TestNativeHEVCAvailableConcurrent(t *testing.T) {
	const workers = 16
	results := make([]bool, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = nativeHEVCAvailable()
		}(i)
	}
	wg.Wait()
	for i := 1; i < workers; i++ {
		if results[i] != results[0] {
			t.Fatalf("并发调用结果不一致：goroutine 0 = %v，goroutine %d = %v", results[0], i, results[i])
		}
	}
	t.Logf("本机 nativeHEVCAvailable() = %v (GOOS=%s)", results[0], runtime.GOOS)
}

// 结果缓存：首次之后每次调用都必须一致。
func TestNativeHEVCAvailableStable(t *testing.T) {
	first := nativeHEVCAvailable()
	for i := 0; i < 8; i++ {
		if got := nativeHEVCAvailable(); got != first {
			t.Fatalf("第 %d 次调用结果 %v，与首次 %v 不一致", i+2, got, first)
		}
	}
}

// 非 Windows 平台没有 Media Foundation，必须保守返回 false。
// 这是平台契约，不是对某台机器结果的硬编码。
func TestNativeHEVCAvailableNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("非 Windows 契约检查")
	}
	if nativeHEVCAvailable() {
		t.Fatal("非 Windows 平台不应报告 HEVC 解码能力")
	}
}
