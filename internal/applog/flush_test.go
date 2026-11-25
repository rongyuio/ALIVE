package applog

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"alive/internal/diag"
	"alive/internal/metrics"
)

// 事件写进去之后必须自己落盘，不能等到 Close 才刷。
//
// 真窗口里就是这么查过一次：recent 有内容、主日志看起来不动——最后发现是
// Get-ChildItem 对打开着的文件报的 Length 滞后，不是代码问题。这两条测试
// 留着是因为它们盯的那条路（异步队列 → 单写 goroutine → 文件）确实会坏。
func TestFlushesWithoutClose(t *testing.T) {
	path := setup(t)
	L().Info().Str("event", "test.flush").Msg("立即写入")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "立即写入") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("2 秒内没落盘，文件内容: %q", data)
}

// 连续写、跨越多次 recent 刷新之后，主日志必须还在长。
//
// 只测第一条会放过「writer 起来一下就死了」这种：第一条落盘了，后面全丢。
func TestFlushesAcrossRecentTicks(t *testing.T) {
	path := setup(t)
	const n = 12
	for i := 0; i < n; i++ {
		L().Info().Int("i", i).Msgf("第%d条", i)
		time.Sleep(120 * time.Millisecond)
	}
	// recent 的周期是 2 秒，跨过一次就够。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		ok := 0
		for i := 0; i < n; i++ {
			if strings.Contains(string(data), "第"+strconv.Itoa(i)+"条") {
				ok++
			}
		}
		if ok == n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := os.ReadFile(path)
	t.Fatalf("主日志没跟到底，落盘 %d 字节:\n%s", len(data), data)
}

// 队列真的满了的时候，丢弃必须被数出来，而且这个数要落盘、要进指标。
//
// 「丢了多少行」是唯一能回答「这份日志可不可信」的数字。它以前恒为 0：
// 补写行直接写文件、绕过 handler，指标收不到。现在补写那一条自己补一次指标。
//
// 造满队列的办法是把写入器的落盘口堵住——按住 rotator 的锁，writer 取到第一条
// 就停在那里，队列于是真的满。这比「并发写等它溢出」确定得多。
func TestDroppedIsCountedAndReported(t *testing.T) {
	path := setup(t)
	s := current.Load()
	if s == nil {
		t.Fatal("没有 sink")
	}
	metrics.Reset()

	// 堵住落盘口。
	s.rot.mu.Lock()
	const total = queueSize + 32
	for i := 0; i < total; i++ {
		L().Info().Int("i", i).Msg("填队列")
	}
	s.rot.mu.Unlock()

	if got := s.dropped.Load(); got == 0 {
		t.Fatalf("写了 %d 条，队列满了却一条都没记丢弃", total)
	}
	Close()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 比对落盘那一行自己的数字，不用 Close 之前读到的值：Close 里那条
	// 「日志关闭」也可能被丢，计数会再涨。
	line := droppedLine(t, string(data))
	if line == nil {
		t.Fatalf("没有 log.dropped 补写行，落盘 %d 字节", len(data))
	}
	reported := int64(line["dropped"].(float64))
	if reported < 1 {
		t.Fatalf("补写行说丢了 %v 条", line["dropped"])
	}

	// 一致性：落盘条数 + 补写行自报的丢弃数，应该等于实际写的条数。
	// 只断言「大于 0」只能证明计数器动过，证明不了丢的和说的是同一件事。
	//
	// 唯一的不确定项是 Close 自己那条 log.stop：队列关闭前写，可能落盘、
	// 也可能正好撞上满队列被丢，且不在这两个统计口径里。所以允许 ±0/1，
	// 不允许更多。
	written := int64(strings.Count(string(data), `"msg":"填队列"`))
	sum := written + reported
	if sum < total || sum > total+1 {
		t.Fatalf("对不上：落盘 %d + 丢弃 %d = %d，写入 %d", written, reported, sum, total)
	}

	counters := metrics.Snapshot()["counters"].(map[string]int64)
	if counters["log_dropped_total"] == 0 {
		t.Fatalf("log_dropped_total 还是 0，指标没看到这次丢弃：%v", counters)
	}
}

// droppedLine 找出补写的那一行并解成字段表。
func droppedLine(t *testing.T, data string) map[string]any {
	t.Helper()
	for _, raw := range strings.Split(data, "\n") {
		if !strings.Contains(raw, diag.EvLogDropped) {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			t.Fatalf("补写行不是合法 JSON: %v (%q)", err, raw)
		}
		return obj
	}
	return nil
}
