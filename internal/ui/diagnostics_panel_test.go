package ui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/metrics"
)

// 诊断页的期望值都从代码之外来：首帧的门槛来自
// doc/reference/measurements.md（正常 1666ms、被判为慢的旧路径 3648ms），
// 单位格式来自用户实际读数的方式（100ms 级不能被四舍五入成 0.1s）。

func diagFields(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// withFreshMetrics 清空指标注册表。指标是包级单例，不清会让上一条测试的
// 事件漏进这一条。
func withFreshMetrics(t *testing.T) {
	t.Helper()
	metrics.Reset()
	t.Cleanup(metrics.Reset)
}

func TestFmtLatency(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{0, "—"},       // 没有样本
		{250, "250ms"}, // 最短的桶边界
		{999, "999ms"}, // 1s 以下不走秒
		{1000, "1.0s"},
		{1666, "1.7s"}, // measurements.md 里 B 站 545068 的实测首帧
		{20000, "20.0s"},
	}
	for _, c := range cases {
		if got := fmtLatency(c.ms); got != c.want {
			t.Errorf("fmtLatency(%d)=%q want %q", c.ms, got, c.want)
		}
	}
}

func TestFmtCount(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1204, "1,204"},
		{32000, "32,000"},
		{-1500, "-1,500"},
	}
	for _, c := range cases {
		if got := fmtCount(c.n); got != c.want {
			t.Errorf("fmtCount(%d)=%q want %q", c.n, got, c.want)
		}
	}
}

func TestProfileStopLabel(t *testing.T) {
	start := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)
	cases := []struct {
		after time.Duration
		want  string
	}{
		{0, "停止剖析 · 30s"},
		{12 * time.Second, "停止剖析 · 18s"},
		{29500 * time.Millisecond, "停止剖析 · 1s"}, // 还没到点就别说 0s
		{30 * time.Second, "停止剖析 · 0s"},
		{40 * time.Second, "停止剖析 · 0s"}, // 超时也不显示负数
	}
	for _, c := range cases {
		if got := profileStopLabel(start, start.Add(c.after)); got != c.want {
			t.Errorf("after=%v got %q want %q", c.after, got, c.want)
		}
	}
}

// 冷启动时一行数字都没有：整块折叠成一句话，而不是一屏「—」和「0」。
func TestDiagnosticGroupsEmptyOnFreshRun(t *testing.T) {
	withFreshMetrics(t)
	_, empty := diagnosticGroups()
	if !empty {
		t.Fatal("没有任何事件时应该判为空态")
	}
}

func TestDiagnosticGroupsShowPlayback(t *testing.T) {
	withFreshMetrics(t)
	metrics.Observe(diagFields("event", "session.first_frame", "room", "bilibili|1",
		"ms", json.Number("1200")))
	metrics.Observe(diagFields("event", "wall.open", "room", "douyu|2"))

	groups, empty := diagnosticGroups()
	if empty {
		t.Fatal("有播放记录时不该折叠")
	}
	row, ok := findDiagRow(groups, "播放页", "首帧")
	if !ok {
		t.Fatalf("播放页没有首帧行：%+v", groups)
	}
	// 1200ms 落在 2000ms 桶，p50/p95 都是桶上界 2000ms。
	if !strings.Contains(row.value, "p95 ≤2.0s") {
		t.Errorf("首帧值=%q，期望含 p95 ≤2.0s", row.value)
	}
	if row.bad {
		t.Errorf("2.0s 低于 3s 门槛，不该标红：%q", row.value)
	}
}

// 首帧 p95 过门槛标红。门槛 3000ms 的依据是实测里「正常 1666ms / 被判为慢
// 3648ms」两点之间；把门槛改成 100 或 100000 这条测试都必须红。
func TestDiagnosticGroupsFlagSlowFirstFrame(t *testing.T) {
	withFreshMetrics(t)
	metrics.Observe(diagFields("event", "session.first_frame", "room", "bilibili|1",
		"ms", json.Number("4500")))

	groups, _ := diagnosticGroups()
	row, ok := findDiagRow(groups, "播放页", "首帧")
	if !ok {
		t.Fatal("播放页没有首帧行")
	}
	if !row.bad {
		t.Fatalf("p95 %q 超过 3s 门槛却没标红", row.value)
	}
}

// 日志丢弃说明证据本身少了，哪怕没播过也不能折叠——否则用户以为一切正常。
func TestDiagnosticGroupsKeepDroppedLogs(t *testing.T) {
	withFreshMetrics(t)
	metrics.Observe(diagFields("event", "log.dropped"))

	groups, empty := diagnosticGroups()
	if empty {
		t.Fatal("有日志丢弃时不该折叠成空态")
	}
	row, ok := findDiagRow(groups, "运行时", "日志丢弃")
	if !ok || !row.bad {
		t.Fatalf("日志丢弃行=%+v ok=%v，期望标红", row, ok)
	}
}

func findDiagRow(groups []diagGroup, title, label string) (diagStat, bool) {
	for _, g := range groups {
		if g.title != title {
			continue
		}
		for _, r := range g.rows {
			if r.label == label {
				return r, true
			}
		}
	}
	return diagStat{}, false
}

// 空态页面上只有一句话和两个动作；分组标题、复制摘要都不该出现。
func TestDiagnosticPageShowsEmptyState(t *testing.T) {
	withFreshMetrics(t)
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if !tt.HasText("还没有播放记录") {
		t.Fatalf("空态没有说明：%v", tt.Texts())
	}
	if tt.HasText("运行时") {
		t.Error("空态不该摆出分组标题")
	}
	if _, ok := tt.Find("复制摘要"); ok {
		t.Error("空态没有内容可复制，不该出复制摘要")
	}
	for _, label := range []string{"导出诊断包", "30 秒性能剖析", "刷新指标"} {
		if _, ok := tt.Find(label); !ok {
			t.Errorf("诊断页缺少 %q：%v", label, tt.Texts())
		}
	}
}

// 有数据时分组、行值都上屏。
func TestDiagnosticPageShowsGroups(t *testing.T) {
	withFreshMetrics(t)
	metrics.Observe(diagFields("event", "session.first_frame", "room", "bilibili|1",
		"ms", json.Number("1200")))
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	for _, want := range []string{"播放页", "观测墙", "运行时", "首帧", "p95 ≤2.0s", "复制摘要"} {
		if !tt.HasText(want) {
			t.Errorf("诊断页缺少 %q：%v", want, tt.Texts())
		}
	}
	if tt.HasText("还没有播放记录") {
		t.Error("有数据时不该再显示空态说明")
	}
}

// 剖析期间按钮写倒计时，时间只走 Tester 的时钟。
func TestProfileButtonCountsDown(t *testing.T) {
	withFreshMetrics(t)
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()
	app.SyncBusy = true
	app.SyncBusyAction = syncActProfile
	app.ProfStarted = time.Now()

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if !tt.HasText("停止剖析 · 30s") {
		t.Fatalf("刚开始剖析应该显示 30s 并可停：%v", tt.Texts())
	}

	tt.Advance(12 * time.Second)
	tt.Frame()
	if !tt.HasText("停止剖析 · 18s") {
		t.Fatalf("推进 12s 后应该显示 18s：%v", tt.Texts())
	}
}

// 结果区在「空 / 进行中 / 已写出」三种状态下占同一块高度。
//
// 真实窗口里发现的：剖析一开始清空上一条结果，面板整体缩回去、居中重排，
// 「导出诊断包」按钮向上跳了二十几像素——就在刚点完的那一刻。按钮位置
// 随状态移动，用户点到的是别的东西。
func TestDiagnosticResultSlotKeepsPanelStill(t *testing.T) {
	withFreshMetrics(t)
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	idle, ok := tt.Find("导出诊断包")
	if !ok {
		t.Fatal("诊断页没有导出按钮")
	}

	app.SyncBusy = true
	app.SyncBusyAction = syncActProfile
	app.ProfStarted = time.Now()
	tt.Frame()
	busy, ok := tt.Find("导出诊断包")
	if !ok {
		t.Fatal("剖析中导出按钮消失了")
	}

	app.SyncBusy = false
	app.SyncBusyAction = ""
	app.SyncNotice = "诊断包已导出：bundle.zip"
	app.SyncPath = filepath.Join(t.TempDir(), "bundle.zip")
	tt.Frame()
	done, ok := tt.Find("导出诊断包")
	if !ok {
		t.Fatal("出结果后导出按钮消失了")
	}

	if idle.Y != busy.Y || idle.Y != done.Y {
		t.Fatalf("按钮位置随结果区状态移动：空 %.1f / 进行中 %.1f / 已写出 %.1f",
			idle.Y, busy.Y, done.Y)
	}
}

// 剖析中那枚按钮不锁死：点它就是叫停，而不是再开一份。
func TestProfileStopButtonCancels(t *testing.T) {
	withFreshMetrics(t)
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()
	app.SyncBusy = true
	app.SyncBusyAction = syncActProfile
	app.ProfStarted = time.Now()

	canceled := 0
	app.ProfCancel = func() { canceled++ }

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if _, ok := tt.Find("停止剖析"); !ok {
		t.Fatalf("剖析中没有停止入口：%v", tt.Texts())
	}
	if _, ok := tt.Find("导出诊断包"); !ok {
		t.Fatal("剖析中导出按钮不该消失")
	}

	if err := tt.Click("停止剖析"); err != nil {
		t.Fatalf("点不到停止剖析：%v", err)
	}
	if canceled != 1 {
		t.Fatalf("点停止调用了 %d 次取消，期望 1 次", canceled)
	}
	// 收尾在采样 goroutine 里做；这里状态还没被改写，按钮仍在等它回来。
	if !app.SyncBusy {
		t.Fatal("采样 goroutine 还没回来，忙碌状态不该提前清掉")
	}
}
