// Package hang 是主线程的独立观察者。
//
// 卡死和普通故障不同：日志会停在最后一条，看门狗也跑在被卡住的那条线程上，
// 没人能报案。这里用一个不依赖帧循环的 goroutine，定期把探针投到 UI 线程；
// 探针在约定时间内没跑，就说明 UI 线程无响应，立刻把全量 goroutine 栈、
// 最近事件、指标和一份 minidump 写到独立文件。
package hang

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"

	"alive/internal/applog"
	"alive/internal/diag"
	"alive/internal/metrics"
	"alive/internal/support"
)

const (
	probeEvery  = 5 * time.Second
	probeBudget = 10 * time.Second
	dumpEvery   = 5 * time.Minute
)

// Watcher 观察一个窗口的 UI 线程。
type Watcher struct {
	win       *mygo.Window
	dir       string
	run       string
	minimized *atomic.Bool
	stop      chan struct{}
	done      chan struct{}
	beat      atomic.Int64
	lastDump  atomic.Int64
}

// Start 启动观察。win 为 nil 时返回一个空 watcher，Stop 仍然安全。
func Start(win *mygo.Window, dir, run string, minimized *atomic.Bool) *Watcher {
	w := &Watcher{
		win:       win,
		dir:       dir,
		run:       run,
		minimized: minimized,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if win == nil || dir == "" {
		close(w.done)
		return w
	}
	go w.loop()
	return w
}

// Stop 停掉观察。可以重复调用。
func (w *Watcher) Stop() {
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	<-w.done
}

func (w *Watcher) loop() {
	defer close(w.done)
	t := time.NewTicker(probeEvery)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
		}
		if w.win.IsDestroyed() || (w.minimized != nil && w.minimized.Load()) {
			continue
		}
		before := w.beat.Load()
		w.win.Update(func() { w.beat.Add(1) })
		timer := time.NewTimer(probeBudget)
		select {
		case <-w.stop:
			timer.Stop()
			return
		case <-timer.C:
		}
		if w.beat.Load() != before {
			continue
		}
		if w.win.IsDestroyed() || (w.minimized != nil && w.minimized.Load()) {
			continue
		}
		// 两次 dump 之间至少隔 5 分钟：卡死往往持续很久，不要每 15 秒
		// 写一份几百 KB 的栈。
		now := time.Now().UnixNano()
		last := w.lastDump.Load()
		if last != 0 && now-last < int64(dumpEvery) {
			continue
		}
		if !w.lastDump.CompareAndSwap(last, now) {
			continue
		}
		w.dump("ui-thread-unresponsive")
	}
}

// dump 写一份卡死现场。失败只写 stderr，不影响进程。
func (w *Watcher) dump(reason string) {
	stamp := time.Now().Format("20060102-150405")
	base := filepath.Join(w.dir, fmt.Sprintf("hang-%s-%s", w.run, stamp))
	logPath := base + ".log"

	var b []byte
	b = append(b, fmt.Sprintf("time=%s reason=%s run=%s pid=%d goroutines=%d\n",
		time.Now().Format(time.RFC3339), reason, w.run, os.Getpid(), runtime.NumGoroutine())...)
	if m, err := json.Marshal(metrics.Snapshot()); err == nil {
		b = append(b, "metrics="...)
		b = append(b, m...)
		b = append(b, '\n')
	}
	b = append(b, "\n--- recent events ---\n"...)
	for _, line := range applog.Recent() {
		b = append(b, line...)
	}
	b = append(b, "\n--- goroutine stacks ---\n"...)
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	b = append(b, buf[:n]...)
	if err := os.WriteFile(logPath, b, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "alive: 写卡死现场失败: %v\n", err)
	}
	if err := support.Minidump(base + ".dmp"); err != nil {
		fmt.Fprintf(os.Stderr, "alive: 写 minidump 失败: %v\n", err)
	}
	// 独立文件是现场；主日志这一行是入口——没有它，timeline 和
	// alive-diag answer freeze 不知道这次卡死发生过。applog 的入队是非阻塞的，
	// 队列满就丢，不会把卡死检测 goroutine 拖在这里。
	//
	// 不写 run：handler 给每一行都注入，这里再写一次会落成两个 run=。
	diag.L().Warn().
		Str(diag.Event, diag.EvHang).
		Str("reason", reason).
		Str("file", logPath).
		Msg("卡死现场已落盘")
}
