package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/pprof"
	"sync"
	"time"
)

// ErrProfileCanceled 表示采样被中途停掉。文件里已经有取消之前采到的样本，
// 仍然可以给 `go tool pprof` 看——所以它是「停了」，不是「失败了」。
var ErrProfileCanceled = errors.New("性能剖析被中断")

var (
	profileMu sync.Mutex
	profiling bool
)

// CPUProfile 采集一段时间的 CPU profile，写到 path。
//
// 用 pprof 的官方格式：`go tool pprof -http=:0 path` 就能看。同一时刻只允许
// 一份；第二次调用直接报错，不悄悄覆盖。
//
// ctx 取消时提前收尾：StopCPUProfile 会把已采到的样本写完，返回实际采样
// 时长和 ErrProfileCanceled。30 秒是一个不短的等待，用户必须能停。
func CPUProfile(ctx context.Context, path string, d time.Duration) (time.Duration, error) {
	profileMu.Lock()
	if profiling {
		profileMu.Unlock()
		return 0, fmt.Errorf("已有一份性能剖析在进行")
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
		return 0, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return 0, err
	}

	start := time.Now()
	t := time.NewTimer(d)
	defer t.Stop()
	stopped := false
	select {
	case <-t.C:
	case <-ctx.Done():
		stopped = true
	}
	pprof.StopCPUProfile()
	took := time.Since(start)
	if err := f.Close(); err != nil {
		return took, err
	}
	if stopped {
		return took, ErrProfileCanceled
	}
	return took, nil
}
