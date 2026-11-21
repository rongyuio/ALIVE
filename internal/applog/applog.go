// Package applog 把进程日志落到文件，并接住 mpv 自己的日志。
//
// 为什么要有它：原来所有日志都走 slog 的默认 handler，也就是 stderr。双击
// exe 启动的 Windows 进程没有 stderr，等于一行都不留；出了问题只能让用户
// 装个终端去跑，或者靠猜。排障的成本全落在这上面——一次「直播卡」的调查，
// 因为拿不到 mpv 自己说的话，来回猜了三轮。
//
// 落点按项目约定：%LOCALAPPDATA%\alive\logs\alive.log。缓存和日志放同一层，
// 丢了不丢配置。
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// 单个日志文件的上限。超过就轮转一次，只留 .1 这一份旧文件。
//
// 直播连续性日志只在状态翻转时写，正常播放一条都没有；这个上限是给 mpv
// 的日志兜底的——它能在一秒里刷出上百行。
const maxBytes = 8 << 20

var (
	mu      sync.Mutex
	file    *os.File
	writer  io.Writer = io.Discard
	logPath string
)

// Setup 打开日志文件并把 slog 的默认输出接过去。返回日志路径，失败时返回空串。
//
// 拿不到目录不是错误：进程照常跑，只是没有文件日志。
func Setup() string {
	dir, err := os.UserCacheDir()
	if err != nil || dir == "" {
		return ""
	}
	path := filepath.Join(dir, "alive", "logs", "alive.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ""
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return ""
	}
	mu.Lock()
	file = f
	logPath = path
	writer = &rotator{f: f, path: path, n: sizeOf(f)}
	mu.Unlock()

	slog.SetDefault(slog.New(slog.NewTextHandler(writer, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return path
}

// Path 返回当前日志文件路径，未初始化时为空串。
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return logPath
}

// Write 让别的日志源（mpv）写进同一个文件。
//
// 调用方是播放器的日志事件回调，可能在任意 goroutine 上；这里只做一次
// 带锁的写，不阻塞。
func Write(line string) {
	mu.Lock()
	w := writer
	mu.Unlock()
	if w == nil {
		return
	}
	_, _ = io.WriteString(w, line)
	if len(line) == 0 || line[len(line)-1] != '\n' {
		_, _ = io.WriteString(w, "\n")
	}
}

// Close 关掉文件。进程退出时不必调，测试里要。
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
		file = nil
	}
	writer = io.Discard
}

func sizeOf(f *os.File) int64 {
	if st, err := f.Stat(); err == nil {
		return st.Size()
	}
	return 0
}

// rotator 在写满上限时把当前文件挪到 .1 再重开。
//
// 不引第三方日志库：这里要的只是「文件不会无限长大」这一条。
type rotator struct {
	mu   sync.Mutex
	f    *os.File
	path string
	n    int64
}

func (r *rotator) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, os.ErrClosed
	}
	if r.n+int64(len(p)) > maxBytes {
		_ = r.f.Close()
		_ = os.Rename(r.path, r.path+".1")
		f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			r.f = nil
			return 0, err
		}
		r.f, r.n = f, 0
		_, _ = fmt.Fprintf(f, "--- 日志轮转，上一份在 %s.1 ---\n", r.path)
	}
	n, err := r.f.Write(p)
	r.n += int64(n)
	return n, err
}
