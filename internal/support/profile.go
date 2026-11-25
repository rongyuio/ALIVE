package support

import (
	"fmt"
	"os"
	"runtime/pprof"
	"sync"
	"time"
)

var (
	profileMu sync.Mutex
	profiling bool
)

// CPUProfile 采集一段时间的 CPU profile，写到 path。
//
// 用 pprof 的官方格式：`go tool pprof -http=:0 path` 就能看。同一时刻只允许
// 一份；第二次调用直接报错，不悄悄覆盖。
func CPUProfile(path string, d time.Duration) error {
	profileMu.Lock()
	if profiling {
		profileMu.Unlock()
		return fmt.Errorf("已有一份性能剖析在进行")
	}
	profiling = true
	profileMu.Unlock()
	defer func() {
		profileMu.Lock()
		profiling = false
		profileMu.Unlock()
	}()
	if d <= 0 {
		d = 30 * time.Second
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return err
	}
	t := time.NewTimer(d)
	<-t.C
	pprof.StopCPUProfile()
	return f.Close()
}
