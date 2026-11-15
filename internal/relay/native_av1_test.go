package relay

import (
	"runtime"
	"sync"
	"testing"
)

// 两种编码并发调用，分别验证缓存稳定；不以某台机器的安装状态作为断言。
func TestNativeAV1AvailableConcurrent(t *testing.T) {
	const workers = 16
	var results [workers][2]bool
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i] = [2]bool{nativeAV1Available(), nativeHEVCAvailable()}
		}()
	}
	close(start)
	wg.Wait()
	for _, result := range results {
		if result != results[0] {
			t.Fatalf("并发缓存结果不一致：%v / %v", result, results[0])
		}
	}
	for i := 0; i < workers; i++ {
		if nativeAV1Available() != results[0][0] {
			t.Fatal("AV1 缓存结果发生变化")
		}
	}
	t.Logf("HEVC=%v AV1=%v", results[0][1], results[0][0])
	if runtime.GOOS != "windows" && (results[0][0] || results[0][1]) {
		t.Fatal("非 Windows 平台必须保守返回 false")
	}
}
