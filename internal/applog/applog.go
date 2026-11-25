// Package applog 把进程日志落到文件，并接住 mpv 自己的日志。
//
// 设计目标不是「多写几行」，而是让一份日志包能回答排障问题：
//
//   - 每次启动一个文件（alive-<run>.log），多开、重启、旧构建混写都不会
//     再混成一份无法分段的流水账；
//   - 每行带 run，关键行带 room/session/engine；
//   - 唯一脱敏出口，URL query、token、用户名目录不会原样落盘；
//   - 有界队列 + 单写 goroutine：解析、脱敏、编码和文件 I/O 都在写线程上，
//     调用方只付一次拷贝入队；
//   - 最近 512 行常驻内存并定期写 recent-<run>.log，崩溃/卡死时仍有上下文；
//   - 未捕获的 panic 由 debug.SetCrashOutput 写到 crash-<run>.log。
//
// 落点仍是 %LOCALAPPDATA%\alive\logs。缓存和日志放同一层，丢了不丢配置。
package applog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

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

	// level 存 zerolog.Level 的底层值。改级别要重建 logger——zerolog 的
	// Level 是构造时的定值，没有运行中修改的入口。
	level      atomic.Int32
	components map[string]struct{}
	logMygo    bool

	// log 是当前级别的 logger。SetLevel 重建它并原子换掉，
	// 已经拿在手里的旧 logger 会继续用旧级别跑完这一轮，不会崩。
	log atomic.Pointer[zerolog.Logger]

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
	recentDone chan struct{}
	// recentMu 让 ticker 和 Close 不会同时写同一个 .tmp 再 rename。
	recentMu sync.Mutex

	crashPath string
}

var current atomic.Pointer[sink]

// Setup 打开本次运行的日志文件，并把 std log 与 slog 的默认输出接过去。
//
// 返回日志路径。失败时返回错误，调用方必须把错误送到用户能看见的地方——
// 双击启动的 GUI 没有 stderr，静默失败等于没有日志。
// init 把 zerolog 的全局字段名对齐项目契约，而且必须在任何 logger 被造出来
// 之前生效——包括 applog.Setup 之前那个写 stderr 的兜底 logger。
//
// 默认值里有两个是错的：消息键默认 "message"（diag.Msg 是 "msg"，alive-diag
// 和几十个读侧地方都按它取，不设就等于每条日志正文读空）；时间默认秒精度，
// 同一秒内的先后会丢。
func init() {
	zerolog.MessageFieldName = diag.Msg
	zerolog.LevelFieldName = diag.LevelKey
	zerolog.TimeFieldFormat = time.RFC3339Nano
	// 字段名沿用 slog 时代的 "source"：alive-diag 和文档都按它查。
	zerolog.CallerFieldName = "source"
	// source 只留最后两段路径：完整路径一行能占掉半屏，排障要的是「哪个
	// 文件哪一行」，目录层级没有信息量。
	zerolog.CallerMarshalFunc = func(_ uintptr, file string, line int) string {
		return shortSource(file, line)
	}
}

// shortSource 把完整路径压成「目录/文件:行」。slog 桥用它补真实调用点：
// 系统 source 只能看到 bridge 自己。
func shortSource(file string, line int) string {
	p := filepath.ToSlash(file)
	if parts := strings.Split(p, "/"); len(parts) > 2 {
		p = strings.Join(parts[len(parts)-2:], "/")
	}
	return fmt.Sprintf("%s:%d", p, line)
}

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
		recentDone: make(chan struct{}),
		recentPath: filepath.Join(dir, "recent-"+run+".log"),
	}
	if lvl, ok := diag.ParseLevel(os.Getenv("ALIVE_LOG_LEVEL")); ok {
		s.level.Store(int32(lvl))
	} else {
		s.level.Store(int32(diag.LevelInfo))
	}
	s.rot = newRotator(f, path, s.maxBytes)
	s.installCrash()

	l := s.logger()
	s.log.Store(l)
	diag.SetLogger(l)

	current.Store(s)
	go s.runWriter()
	go s.runRecent()
	prune(dir, s.keep, path)
	// MyGo 走标准库 log，plugins/video 走 slog。两者都不会自己找到这个文件，
	// 必须在这里接进来——换 zerolog 时漏掉这一步，mpv 的警告和帧统计就只剩
	// stderr，而双击启动的 GUI 进程没有 stderr。
	installStd(s)
	L().Info().
		Str(diag.Event, diag.EvLogStart).
		Str(diag.Path, path).
		Str(diag.LevelKey, Level().String()).
		Int64("max_bytes", s.maxBytes).
		Int("keep", s.keep).
		Str("components", strings.Join(componentList(s.components), ",")).
		Msg("日志就绪")

	return path, nil
}

// logger 按当前级别造一个 logger。
//
// run 走 With() 而不是每行追加：结构上就不可能出现「两个 run」——调用点
// 拿到的 logger 已经带着它，再写一次才会重复，而那是可见的第二个键。
func (s *sink) logger() *zerolog.Logger {
	zl := zerolog.New(&asyncWriter{s: s}).
		Level(zerolog.Level(s.level.Load())).
		With().
		Timestamp().
		Caller().
		Str(diag.Run, s.run).
		Logger()
	return &zl
}

// L 是进程级 logger。applog 未就绪时返回一个丢弃一切的 logger，
// 测试和独立使用都不会因为没 Setup 而崩。
func L() *zerolog.Logger {
	if s := current.Load(); s != nil {
		if l := s.log.Load(); l != nil {
			return l
		}
	}
	return diag.L()
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
//
// 改级别要重建 logger 并换掉全局那一个：zerolog 的级别是构造时定下的，
// 没有 slog.LevelVar 那种活的东西。
func Level() zerolog.Level {
	if s := current.Load(); s != nil {
		return zerolog.Level(s.level.Load())
	}
	return diag.LevelInfo
}

func SetLevel(l zerolog.Level) {
	s := current.Load()
	if s == nil {
		return
	}
	s.level.Store(int32(l))
	nl := s.logger()
	s.log.Store(nl)
	diag.SetLogger(nl)
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
	// 这一行要赶在队列关闭之前写：之后 closed 为真，写进来的会静默丢弃。
	// current 已经是 nil，L() 走 diag 的注入点，仍是同一个 sink。
	L().Info().
		Str(diag.Event, diag.EvLogStop).
		Int64(diag.Dropped, s.dropped.Load()).
		Msg("日志关闭")
	s.mu.Lock()
	s.closed.Store(true)
	close(s.queue)
	s.mu.Unlock()
	// 先等 writer 把队列刷完，再停 recent。反过来的话 ticker 可能正卡在
	// flushRecent 里，和最后的刷写抢同一个 .tmp。
	<-s.done
	close(s.stopRecent)
	<-s.recentDone
	_ = s.flushRecent()
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	s.rot.close()
	restoreStd()
	// 关掉之后还写进来的日志不该落到已经关掉的文件句柄上。
	diag.SetLogger(nil)
}

// ---------------------------------------------------------------- async writer

type asyncWriter struct{ s *sink }

func (w *asyncWriter) Write(p []byte) (int, error) {
	s := w.s
	// 过滤看的是序列化后的前缀，只在调用方做一次廉价扫描；被丢掉的行不进
	// 队列，也就不会让 writer 白解析一遍。
	if s.noise(p) {
		return len(p), nil
	}
	// 拷贝再入队：zerolog 的缓冲区在这条 Msg 返回后立刻复用，拿着原切片
	// 就是读写同一块内存。解码、脱敏、重编码全部挪到 writer——那是每行
	// 十几微秒的活，不该压在 UI 线程上。
	b := make([]byte, len(p))
	copy(b, p)

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

// noise 判断这条要不要丢。
//
// 两条规则以前住在 slog handler 里，现在换到落盘前：MyGo 的帧统计走 std log，
// 正常播放也每秒一条；抖音逐条解析失败这类 Debug 在指定了组件时只放行选中
// 的子系统，免得把真正要看的行淹掉。
//
// 看的是序列化后的 JSON 前缀，所以只在需要判断时才解析消息字段——绝大多数行
// 在第一个开关关着时直接放过。
func (s *sink) noise(p []byte) bool {
	lvl := zerolog.Level(s.level.Load())
	// Debug 级别默认全开；指定了组件就只放行这些子系统。
	if lvl <= zerolog.DebugLevel && len(s.components) > 0 {
		if !bytes.Contains(p, []byte(`"`+diag.Sub+`":"`)) {
			return true
		}
		ok := false
		for sub := range s.components {
			if bytes.Contains(p, []byte(`"`+diag.Sub+`":"`+sub+`"`)) {
				ok = true
				break
			}
		}
		if !ok {
			return true
		}
	}
	if s.logMygo || bytes.Contains(p, []byte(`"level":"warn"`)) ||
		bytes.Contains(p, []byte(`"level":"error"`)) {
		return false
	}
	// "mygo: 60 frames a second, uncapped" / "mygo: frame ..." 这类帧统计。
	if !bytes.Contains(p, []byte("mygo:")) {
		return false
	}
	return bytes.Contains(p, []byte("frames a second")) ||
		bytes.Contains(p, []byte(`mygo: frame `))
}

func (s *sink) runWriter() {
	defer close(s.done)
	for raw := range s.queue {
		s.consume(raw)
	}
	if n := s.dropped.Swap(0); n > 0 {
		line := fmt.Sprintf(`{"level":"warn","time":%q,"msg":"日志队列满，丢弃了部分行","event":%q,"dropped":%d}`+"\n",
			time.Now().Format(time.RFC3339Nano), diag.EvLogDropped, n)
		s.rot.write([]byte(line))
		// 这一行是直写的，没经过 asyncWriter.Write 的解码那一步，所以指标
		// 得自己补一次。不补的话 log_dropped_total 永远是 0——「丢了多少行」
		// 这个唯一能回答「日志可不可信」的数字就白记了。
		metrics.Observe(map[string]any{diag.Event: diag.EvLogDropped})
	}
}

// consume 把一行原始日志变成落盘字节：解析 → 指标 → 脱敏 → 重编码 → 环形缓冲。
//
// 所有这些都只在 writer goroutine 上发生。调用方（UI 线程）只付一次拷贝和
// 一次入队，这是把 18µs/行从主线程挪走的全部意义。
//
// 代价：环形缓冲比实际落盘慢一个队列身位。hang 探针要等 10 秒，writer 早追
// 平了；崩溃瞬间最后几行可能还没进 ring，但 recent 每 2 秒刷一次，那个窗口
// 本来就存在。
func (s *sink) consume(raw []byte) {
	obj := decodeLine(raw)
	if obj == nil {
		return
	}
	metrics.Observe(obj)
	redactValue(obj)
	b := encodeLine(obj)
	if b == nil {
		return
	}
	s.ring.add(string(b))
	s.rot.write(b)
}

func (s *sink) runRecent() {
	defer close(s.recentDone)
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
	s.recentMu.Lock()
	defer s.recentMu.Unlock()

	lines := s.ring.snapshot()
	if len(lines) == 0 {
		return nil
	}
	data := []byte(strings.Join(lines, ""))
	const cap = 256 << 10
	if len(data) > cap {
		// 从尾部切会落在某一行中间：活下来的第一行就是半条 JSON，读侧只能
		// 丢掉。往后找到第一个换行，保证每一行都是完整的。
		cut := data[len(data)-cap:]
		if i := bytes.IndexByte(cut, '\n'); i >= 0 {
			cut = cut[i+1:]
		}
		data = cut
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

// prune 按类别保留最近 keep 份运行产物。按修改时间排序，当前文件永远保留。
//
// 只清 alive-*.log 是不够的：recent、crash、hang（含 minidump）、trace、pprof
// 都是一次运行留下的证据，哪一类不清理都会随运行次数无限长下去。
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
	groups := map[string][]fileInfo{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		cat := ""
		for _, p := range []string{"alive-", "recent-", "crash-", "hang-", "trace-", "cpu-"} {
			if strings.HasPrefix(n, p) {
				cat = p
				break
			}
		}
		if cat == "" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		groups[cat] = append(groups[cat], fileInfo{filepath.Join(dir, n), info.ModTime()})
	}
	for _, files := range groups {
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
}

// ---------------------------------------------------------------- std log / slog

// installStd 把标准库 log 和 slog 接到这个 sink 上。
//
// 两条来源都真实存在：MyGo 全程用 log.Printf，plugins/video 用 slog.Warn/Debug。
// 在 slog.SetDefault 被删掉的那次迁移里，它们悄悄退回了 stderr——GUI 进程没有
// stderr，等于 mpv 的警告和帧统计从日志里消失。
func installStd(s *sink) {
	// 先 slog：Go 1.21 起 SetDefault 会把 std log 也接到 slog 上，顺序反了
	// 我们的 stdBridge 会被它覆盖。
	slog.SetDefault(slog.New(&slogBridge{s: s}))
	log.SetOutput(&stdBridge{s: s})
	log.SetFlags(0)
}

func restoreStd() {
	// 和 installStd 同一个理由：SetDefault 会顺手改写 std log 的输出，
	// 所以它必须先跑，我们的 os.Stderr 才是最后一手。
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags)
}

// stdBridge 把 std log 的一行转成一条事件。原文放在 text 里，noise() 还能
// 按 "mygo:" 认出帧统计并丢掉。
type stdBridge struct{ s *sink }

func (b *stdBridge) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	if line == "" {
		return len(p), nil
	}
	if l := b.s.log.Load(); l != nil {
		l.Info().Str(diag.Event, "stdlog").Str(diag.Text, line).Msg("std log")
	}
	return len(p), nil
}

// slogBridge 把 slog 的 record 转成 zerolog 事件，保留级别和结构化属性。
type slogBridge struct {
	s     *sink
	attrs []slog.Attr
}

func (h *slogBridge) Enabled(context.Context, slog.Level) bool { return true }

func (h *slogBridge) WithAttrs(as []slog.Attr) slog.Handler {
	n := *h
	n.attrs = append(append([]slog.Attr(nil), h.attrs...), as...)
	return &n
}

func (h *slogBridge) WithGroup(string) slog.Handler { return h }

func (h *slogBridge) Handle(_ context.Context, r slog.Record) error {
	l := h.s.log.Load()
	if l == nil {
		return nil
	}
	e := l.WithLevel(slogLevel(r.Level))
	if !e.Enabled() {
		return nil
	}
	e = e.Str(diag.Event, "slog")
	// 系统 source 记的是 bridge 这一帧，不是 plugins/video 的调用点。
	// 单开一个 origin：读侧不依赖它，但排障时能直接跳到出事的文件。
	if r.PC != 0 {
		frames := runtime.CallersFrames([]uintptr{r.PC})
		if fr, _ := frames.Next(); fr.File != "" {
			e = e.Str("origin", shortSource(fr.File, fr.Line))
		}
	}
	for _, a := range h.attrs {
		e = addAttr(e, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		e = addAttr(e, a)
		return true
	})
	e.Msg(r.Message)
	return nil
}

func slogLevel(l slog.Level) zerolog.Level {
	switch {
	case l >= slog.LevelError:
		return zerolog.ErrorLevel
	case l >= slog.LevelWarn:
		return zerolog.WarnLevel
	case l >= slog.LevelInfo:
		return zerolog.InfoLevel
	default:
		return zerolog.DebugLevel
	}
}

func addAttr(e *zerolog.Event, a slog.Attr) *zerolog.Event {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		return e.Str(a.Key, v.String())
	case slog.KindInt64:
		return e.Int64(a.Key, v.Int64())
	case slog.KindUint64:
		return e.Uint64(a.Key, v.Uint64())
	case slog.KindFloat64:
		return e.Float64(a.Key, v.Float64())
	case slog.KindBool:
		return e.Bool(a.Key, v.Bool())
	case slog.KindDuration:
		return e.Dur(a.Key, v.Duration())
	case slog.KindTime:
		return e.Time(a.Key, v.Time())
	default:
		if err, ok := v.Any().(error); ok {
			return e.Dict(diag.ErrField, diag.Err(err))
		}
		return e.Str(a.Key, fmt.Sprint(v.Any()))
	}
}

// 保持 io 的引用，避免未来删掉 Write 时误删导入（占位）。
var _ io.Writer = (*asyncWriter)(nil)
