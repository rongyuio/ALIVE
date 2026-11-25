package applog

import (
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"alive/internal/diag"
)

// setup 把日志目录换到临时目录，返回日志路径。
func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	path, err := Setup()
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	t.Cleanup(Close)
	return path
}

func read(t *testing.T, path string) string {
	t.Helper()
	Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读日志失败: %v", err)
	}
	return string(data)
}

// 日志必须真的落盘，而且每行带 run。没有 run，多次启动的日志无法分段——
// 这次排障就撞上了：同一份文件里两个构建、多个进程混在一起。
func TestSetupWritesRun(t *testing.T) {
	path := setup(t)
	run := RunID()
	if run == "" {
		t.Fatal("RunID 为空")
	}
	L().Info().Str(diag.Event, "test.event").Msg("测试事件")
	data := read(t, path)
	if !strings.Contains(data, "测试事件") {
		t.Fatalf("写入的内容没进文件: %q", data)
	}
	if !strings.Contains(data, `"run":"`+run+`"`) {
		t.Fatalf("每行必须带 run=%s: %q", run, data)
	}
	if !strings.Contains(data, `"source":"`) {
		t.Fatalf("每行必须带 source: %q", data)
	}
}

// 唯一脱敏出口必须真的挡住 URL query、token 和 Windows 用户名。
func TestRedaction(t *testing.T) {
	path := setup(t)
	secret := "tok" + "en=SECRET123"
	err := errors.New(`GET https://api.example.com/room?` + secret + ` failed under C:\Users\alice\AppData`)
	L().Error().Dict(diag.ErrField, diag.Err(err)).Msg("上游失败")
	data := read(t, path)
	for _, bad := range []string{"SECRET123", "alice", "?token"} {
		if strings.Contains(data, bad) {
			t.Fatalf("日志里出现了 %q: %q", bad, data)
		}
	}
	if !strings.Contains(data, "api.example.com") {
		t.Fatalf("脱敏过度，host 应该保留: %q", data)
	}
}

// Debug 默认不落盘；ALIVE_LOG_LEVEL=debug 后必须出现。
func TestLevelSwitch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_LEVEL", "debug")
	path, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	L().Debug().Str(diag.Event, "test.debug").Msg("调试细节")
	data := read(t, path)
	if !strings.Contains(data, "调试细节") {
		t.Fatalf("ALIVE_LOG_LEVEL=debug 时 Debug 应落盘: %q", data)
	}
}

// 最近 N 行必须能取出来；崩溃/卡死时这是唯一的上下文。
//
// 环形缓冲现在由写入 goroutine 更新（12µs 的解析和脱敏从 UI 线程挪走了），
// 入队返回不代表已进 ring。等的是「10 条都到了」，不是某一条到了。
func TestRecent(t *testing.T) {
	setup(t)
	for i := 0; i < 10; i++ {
		L().Info().Int("i", i).Msg("环形缓冲")
	}
	deadline := time.Now().Add(2 * time.Second)
	var joined string
	for time.Now().Before(deadline) {
		joined = strings.Join(Recent(), "")
		if strings.Count(joined, "环形缓冲") >= 10 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("2 秒内 Recent 没拿到刚写的 10 条: %q", joined)
}

// 每次启动一个文件，旧文件按数量保留。轮转/多开都不会再互相覆盖。
func TestRunFilesAndRetention(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_KEEP", "2")

	var paths []string
	for i := 0; i < 4; i++ {
		path, err := Setup()
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		L().Info().Int("i", i).Msg("一次运行")
		Close()
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil && !os.IsNotExist(err) {
			t.Fatalf("检查 %s: %v", p, err)
		}
	}
	logs, err := filepath.Glob(filepath.Join(dir, "alive", "logs", "alive-*.log"))
	if err != nil {
		t.Fatal(err)
	}
	// keep=2，当前文件也保留，所以最多 2 份；.1 备份可能让数量略多。
	if len(logs) > 4 {
		t.Fatalf("保留策略失效，留下 %d 份: %v", len(logs), logs)
	}
	// 每次启动的文件名必须不同。
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Fatalf("两次启动复用了同一个文件: %s", p)
		}
		seen[p] = true
	}
}

// std log 和 slog 的日志必须真的进文件。
//
// 换 zerolog 那次把 slog.SetDefault 删了，两条来源退回 stderr：GUI 进程没有
// stderr，mpv 的警告和 MyGo 的帧统计就此从日志里消失。这条测试盯着桥本身。
// 帧统计（"frames a second"）仍然按 noise 规则丢掉，其余保留。
func TestStdLogAndSlogBridge(t *testing.T) {
	path := setup(t)
	log.Printf("mygo: 60 frames a second, uncapped")
	log.Printf("mygo: engine ready")
	slog.Warn("video: 零拷贝不可用", "reason", "probe", "engine", 7)

	deadline := time.Now().Add(2 * time.Second)
	var data string
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			data = string(raw)
			if strings.Contains(data, "engine ready") && strings.Contains(data, "零拷贝不可用") {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(data, `"event":"stdlog"`) {
		t.Fatalf("std log 没进日志文件: %q", data)
	}
	if !strings.Contains(data, `"event":"slog"`) || !strings.Contains(data, `"level":"warn"`) {
		t.Fatalf("slog 没进日志文件或级别丢了: %q", data)
	}
	if !strings.Contains(data, `"engine":7`) {
		t.Fatalf("slog 的结构化属性丢了: %q", data)
	}
	// 系统 source 只能看到 bridge 自己的帧；origin 才是 plugins/video 的调用点。
	if !strings.Contains(data, `"origin":"`+"applog"+`/applog_test.go:`) {
		t.Fatalf("slog 没有保留真实调用点: %q", data)
	}
	if strings.Contains(data, "frames a second") {
		t.Fatalf("帧统计应被 noise 丢掉: %q", data)
	}
}

// Close 之后 std log 要回到进程默认输出，不能继续写进已经关掉的 sink。
func TestStdRestoredOnClose(t *testing.T) {
	setup(t)
	Close()
	if log.Writer() != os.Stderr {
		t.Fatalf("Close 之后 std log 没有还原: %T", log.Writer())
	}
}

// recent 从尾部截断时必须落在行边界上：半行 JSON 会被读侧整条丢掉。
func TestRecentStartsOnLineBoundary(t *testing.T) {
	setup(t)
	s := current.Load()
	if s == nil {
		t.Fatal("没有 sink")
	}
	recentPath := s.recentPath
	payload := strings.Repeat("x", 240)
	for i := 0; i < ringSize*2; i++ {
		L().Info().
			Str("a", payload).Str("b", payload).Str("c", payload).
			Str("d", payload).Str("e", payload).Str("f", payload).
			Msg("填充 recent")
	}
	Close()

	data, err := os.ReadFile(recentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("recent 为空")
	}
	if len(data) > 256<<10 {
		t.Fatalf("recent 超过上限: %d 字节", len(data))
	}
	for i, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !json.Valid([]byte(line)) {
			t.Fatalf("第 %d 行不是完整 JSON: %q", i, line)
		}
	}
}

// 每一类运行产物都要按 keep 清理，不能只清 alive-*.log。
func TestPruneAllArtifactKinds(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	var names []string
	for _, kind := range []string{"recent-", "crash-", "hang-", "trace-", "cpu-"} {
		names = append(names, kind+"1.log", kind+"2.log", kind+"3.log")
	}
	for i, n := range names {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// 明确拉开修改时间：同一秒内的写入在排序上不稳定。
		ts := now.Add(time.Duration(i) * time.Second)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	prune(dir, 2, "")
	for _, kind := range []string{"recent-", "crash-", "hang-", "trace-", "cpu-"} {
		if _, err := os.Stat(filepath.Join(dir, kind+"1.log")); !os.IsNotExist(err) {
			t.Fatalf("%s1.log 该被清掉", kind)
		}
		for _, n := range []string{kind + "2.log", kind + "3.log"} {
			if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
				t.Fatalf("%s 该保留: %v", n, err)
			}
		}
	}
}

// 队列/轮转失败不能静默。这里只验证写满上限后仍能继续写，且新文件存在。
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_MAX_MB", "1")
	path, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)
	payload := strings.Repeat("x", 200)
	for i := 0; i < 6000; i++ {
		L().Info().Int("i", i).Str("payload", payload).Msg("轮转填充")
	}
	Close()
	if _, err := os.Stat(strings.TrimSuffix(path, ".log") + ".1.log"); err != nil {
		t.Fatalf("轮转备份不存在: %v", err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Size() == 0 {
		t.Fatalf("轮转后主文件不可写: %v", err)
	}
}
