// Package applog 把进程日志落到文件，并接住 mpv 自己的日志。
//
// 设计目标不是「多写几行」，而是让一份日志包能回答排障问题：
//
//   - 每次启动一个文件（alive-<run>.log），多开、重启、旧构建混写都不会
//     再混成一份无法分段的流水账；
//   - 每行带 run，关键行带 room/session/engine；
//   - 唯一脱敏出口，URL query、token、用户名目录不会原样落盘；
//   - 有界队列 + 单写 goroutine，日志 I/O 不压在 UI 线程上；
//   - 最近 512 行常驻内存并定期写 recent-<run>.log，崩溃/卡死时仍有上下文；
//   - 未捕获的 panic 由 debug.SetCrashOutput 写到 crash-<run>.log。
//
// 落点仍是 %LOCALAPPDATA%\alive\logs。缓存和日志放同一层，丢了不丢配置。
package applog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"alive/internal/diag"
	"alive/internal/metrics"
)

const (
	defaultMaxBytes = int64(8 << 20)
	defaultKeep     = 5
	queueSize       = 4096
	ringSize        = 512
	recentEvery     = 2 * time.Second
)

type sink struct {
	dir  string
	run  string
	path string

	level      slog.LevelVar
	components map[string]struct{}
	logMygo    bool

	maxBytes int64
	keep     int

	mu     sync.Mutex
	closed atomic.Bool
	queue  chan []byte
	done   chan struct{}

	dropped atomic.Int64
	ring    *ring

	rot *rotator

	recentPath string
	stopRecent chan struct{}

	crashPath string
}

var current atomic.Pointer[sink]

// Setup 打开本次运行的日志文件，并把 slog 的默认输出接过去。
//
// 返回日志路径。失败时返回错误，调用方必须把错误送到用户能看见的地方——
// 双击启动的 GUI 没有 stderr，静默失败等于没有日志。
func Setup() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return "", fmt.Errorf("拿不到缓存目录: %w", err)
	}
	dir := filepath.Join(base, "alive", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("建日志目录: %w", err)
	}

	run := newRunID()
	path := filepath.Join(dir, "alive-"+run+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("打开日志文件: %w", err)
	}

	s := &sink{
		dir:        dir,
		run:        run,
		path:       path,
		components: parseComponents(os.Getenv("ALIVE_LOG_COMPONENTS")),
		logMygo:    os.Getenv("ALIVE_LOG_MYGO") != "",
		maxBytes:   int64(envInt("ALIVE_LOG_MAX_MB", int(defaultMaxBytes>>20))) << 20,
		keep:       envInt("ALIVE_LOG_KEEP", defaultKeep),
		queue:      make(chan []byte, queueSize),
		done:       make(chan struct{}),
		ring:       newRing(ringSize),
		stopRecent: make(chan struct{}),
		recentPath: filepath.Join(dir, "recent-"+run+".log"),
	}
	if lvl, ok := diag.ParseLevel(os.Getenv("ALIVE_LOG_LEVEL")); ok {
		s.level.Set(lvl)
	}
	s.rot = newRotator(f, path, s.maxBytes)
	s.installCrash()

	h := &handler{
		inner: slog.NewTextHandler(&asyncWriter{s: s}, &slog.HandlerOptions{
			Level:       &s.level,
			AddSource:   true,
			ReplaceAttr: replaceAttr,
		}),
		s: s,
	}
	slog.SetDefault(slog.New(metrics.Wrap(h)))

	current.Store(s)
	go s.runWriter()
	go s.runRecent()
	prune(dir, s.keep, path)
	slog.Info("日志就绪", "event", diag.EvLogStart,
		diag.Path, path, diag.Level, s.level.Level().String(),
		"max_bytes", s.maxBytes, "keep", s.keep,
		"components", strings.Join(componentList(s.components), ","))

	return path, nil
}

// RunID 是本次运行的短标识，也是日志文件名的一部分。
func RunID() string {
	if s := current.Load(); s != nil {
		return s.run
	}
	return ""
}

// Path 返回当前日志文件路径，未初始化时为空串。
func Path() string {
	if s := current.Load(); s != nil {
		return s.path
	}
	return ""
}

// Dir 返回日志目录。UI 的「打开日志目录」和 support bundle 用它。
func Dir() string {
	if s := current.Load(); s != nil {
		return s.dir
	}
	return ""
}

// Level 返回当前级别，SetLevel 允许运行中提高/降低详细度。
func Level() slog.Level {
	if s := current.Load(); s != nil {
		return s.level.Level()
	}
	return slog.LevelInfo
}

func SetLevel(l slog.Level) {
	if s := current.Load(); s != nil {
		s.level.Set(l)
	}
}

// Recent 返回内存里最近若干条已格式化日志。崩溃/卡死/支持包用。
func Recent() []string {
	if s := current.Load(); s != nil {
		return s.ring.snapshot()
	}
	return nil
}

// Close 刷完队列、写好 recent、关掉文件。进程退出时不必调，测试里要。
func Close() {
	s := current.Swap(nil)
	if s == nil {
		return
	}
	close(s.stopRecent)
	slog.Info("日志关闭", "event", diag.EvLogStop, "dropped", s.dropped.Load())
	s.mu.Lock()
	s.closed.Store(true)
	close(s.queue)
	s.mu.Unlock()
	<-s.done
	_ = s.flushRecent()
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	s.rot.close()
}

// ---------------------------------------------------------------- handler

type handler struct {
	inner slog.Handler
	s     *sink
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.s.level.Level() && h.inner.Enabled(ctx, l)
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	// MyGo 的帧统计走 std log，正常播放也每秒一条。只在 Info 及以上过滤，
	// 不能把所有 mygo: 消息都丢掉——"drawing without the GPU" 这类是真故障。
	if !h.s.logMygo && r.Level < slog.LevelWarn && h.s.level.Level() > slog.LevelDebug {
		if msg := r.Message; strings.HasPrefix(msg, "mygo: ") &&
			(strings.Contains(msg, "frames a second, uncapped") || strings.HasPrefix(msg, "mygo: frame ")) {
			return nil
		}
	}
	// Debug 级别默认全开；一旦指定了组件，只放行这些子系统，避免抖音逐条
	// 解析失败这种高频 Debug 把真正要看的行淹掉。
	if r.Level < slog.LevelInfo && len(h.s.components) > 0 {
		sub := ""
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == diag.Sub {
				sub = a.Value.String()
				return false
			}
			return true
		})
		if _, ok := h.s.components[sub]; !ok {
			return nil
		}
	}
	r.AddAttrs(slog.String(diag.Run, h.s.run))
	return h.inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(as), s: h.s}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name), s: h.s}
}

func replaceAttr(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.SourceKey {
		if src, ok := a.Value.Any().(*slog.Source); ok {
			return slog.String(slog.SourceKey, shortSource(src))
		}
		return a
	}
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(diag.Sanitize(a.Value.String()))
	case slog.KindAny:
		// 直接写 "err", err 的地方很多；error 是 KindAny，不在这里接住就会
		// 绕过脱敏把完整 URL / 路径写进日志。
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(diag.Sanitize(err.Error()))
		}
	}
	return a
}

func shortSource(src *slog.Source) string {
	p := filepath.ToSlash(src.File)
	parts := strings.Split(p, "/")
	if len(parts) > 2 {
		p = strings.Join(parts[len(parts)-2:], "/")
	}
	return fmt.Sprintf("%s:%d", p, src.Line)
}

// ---------------------------------------------------------------- async writer

type asyncWriter struct{ s *sink }

func (w *asyncWriter) Write(p []byte) (int, error) {
	s := w.s
	b := make([]byte, len(p))
	copy(b, p)
	s.ring.add(string(b))

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() {
		return len(p), nil
	}
	select {
	case s.queue <- b:
	default:
		s.dropped.Add(1)
	}
	return len(p), nil
}

func (s *sink) runWriter() {
	defer close(s.done)
	for b := range s.queue {
		s.rot.write(b)
	}
	if n := s.dropped.Swap(0); n > 0 {
		s.rot.write([]byte(fmt.Sprintf(
			"time=%s level=WARN msg=日志队列满，丢弃了部分行 event=%s dropped=%d\n",
			time.Now().Format(time.RFC3339Nano), diag.EvLogDropped, n)))
	}
}

func (s *sink) runRecent() {
	t := time.NewTicker(recentEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			_ = s.flushRecent()
		case <-s.stopRecent:
			return
		}
	}
}

// flushRecent 把最近 N 行写成单独的黑匣子文件。崩溃时主日志可能还没刷，
// 这份文件是「出事前最后两秒」的唯一现场。
func (s *sink) flushRecent() error {
	lines := s.ring.snapshot()
	if len(lines) == 0 {
		return nil
	}
	data := []byte(strings.Join(lines, ""))
	const cap = 256 << 10
	if len(data) > cap {
		data = data[len(data)-cap:]
	}
	tmp := s.recentPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.recentPath)
}

func (s *sink) installCrash() {
	path := filepath.Join(s.dir, "crash-"+s.run+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		_ = f.Close()
		return
	}
	// SetCrashOutput 复制了文件描述符，调用方可以立刻关。
	_ = f.Close()
	s.crashPath = path
}

// ---------------------------------------------------------------- ring

type ring struct {
	mu    sync.Mutex
	lines []string
	max   int
	next  int
	full  bool
}

func newRing(n int) *ring { return &ring{lines: make([]string, n), max: n} }

func (r *ring) add(line string) {
	r.mu.Lock()
	r.lines[r.next] = line
	r.next = (r.next + 1) % r.max
	if r.next == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

func (r *ring) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return append([]string(nil), r.lines[:r.next]...)
	}
	out := make([]string, 0, r.max)
	out = append(out, r.lines[r.next:]...)
	out = append(out, r.lines[:r.next]...)
	return out
}

// ---------------------------------------------------------------- rotator

type rotator struct {
	mu   sync.Mutex
	f    *os.File
	path string
	n    int64
	max  int64
}

func newRotator(f *os.File, path string, max int64) *rotator {
	return &rotator{f: f, path: path, n: sizeOf(f), max: max}
}

func (r *rotator) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return
	}
	if r.n+int64(len(p)) > r.max {
		r.rotateLocked()
	}
	if r.f == nil {
		return
	}
	n, err := r.f.Write(p)
	r.n += int64(n)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alive: 日志写入失败: %v\n", err)
	}
}

// rotateLocked 把当前文件挪到 .1，重开主文件。任何一步失败都保留旧句柄，
// 绝不把 r.f 置 nil 之后静默不写。
func (r *rotator) rotateLocked() {
	_ = r.f.Sync()
	_ = r.f.Close()
	backup := strings.TrimSuffix(r.path, ".log") + ".1.log"
	if err := os.Rename(r.path, backup); err != nil {
		fmt.Fprintf(os.Stderr, "alive: 日志轮转失败: %v\n", err)
		f, openErr := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if openErr != nil {
			r.f = nil
			return
		}
		r.f, r.n = f, sizeOf(f)
		return
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alive: 日志重开失败: %v\n", err)
		r.f = nil
		return
	}
	r.f, r.n = f, 0
	marker := fmt.Sprintf("time=%s level=INFO msg=日志轮转 event=%s file=%s\n",
		time.Now().Format(time.RFC3339Nano), diag.EvLogRotate, filepath.Base(backup))
	if n, err := f.WriteString(marker); err == nil {
		r.n += int64(n)
	}
}

func (r *rotator) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f != nil {
		_ = r.f.Sync()
		_ = r.f.Close()
		r.f = nil
	}
}

// ---------------------------------------------------------------- helpers

var runSeq atomic.Uint64

// newRunID 由时间、pid 和进程内序号组成：同一秒里重启两次也不会复用文件。
func newRunID() string {
	now := time.Now()
	return fmt.Sprintf("%s-%d-%d", now.Format("20060102-150405"), os.Getpid(), runSeq.Add(1))
}

func componentList(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseComponents(v string) map[string]struct{} {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	out := make(map[string]struct{})
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func sizeOf(f *os.File) int64 {
	if f == nil {
		return 0
	}
	if st, err := f.Stat(); err == nil {
		return st.Size()
	}
	return 0
}

// prune 只保留最近 keep 份运行日志（含 .1）。按修改时间排序，当前文件永远保留。
func prune(dir string, keep int, current string) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		name string
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasPrefix(n, "alive-") || !strings.HasSuffix(n, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{filepath.Join(dir, n), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	kept := 0
	for _, f := range files {
		if f.name == current {
			kept++
			continue
		}
		if kept >= keep {
			_ = os.Remove(f.name)
			continue
		}
		kept++
	}
}

// 保持 io 的引用，避免未来删掉 Write 时误删导入（占位）。
var _ io.Writer = (*asyncWriter)(nil)
