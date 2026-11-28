package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// 弹幕时长为 0 时不许算出 NaN。
//
// Dur <= 0 时 0/0 是 NaN，而 `p < 0` 和 `p > 1` 对 NaN 都为假——NaN 会一路
// 塞进 Absolute().Left(x)，元素位置成了 NaN。
func TestDanmuProgressGuardsZeroDuration(t *testing.T) {
	now := time.Now()
	for _, dur := range []time.Duration{0, -time.Second} {
		p := danmuProgress(now, now.Add(-time.Second), dur)
		if p != p { // NaN 自己不等于自己
			t.Fatalf("dur=%v 算出了 NaN", dur)
		}
		if p != 1 {
			t.Errorf("dur=%v 时进度 = %v，应为 1（当作已走完）", dur, p)
		}
	}
	// 正常情形不变：走了 5 秒 / 共 10 秒 = 0.5。
	born := now.Add(-5 * time.Second)
	if p := danmuProgress(now, born, 10*time.Second); p < 0.45 || p > 0.55 {
		t.Errorf("正常进度算错了：%v", p)
	}
}

// 剖析倒计时的两端必须同一个时基，且向上取整。
//
// ProfStarted 是回调里用 time.Now() 存的；构建帧用 c.Now() 时，Tester 里两个
// 时钟差几十年，left 恒被夹到 0，永远显示「停止剖析 · 0s」。
func TestProfileStopLabelCountsDownInWallClock(t *testing.T) {
	start := time.Now()
	if got := profileStopLabel(start, start); got != "停止剖析 · 30s" {
		t.Errorf("刚开始应显示 30s，得到 %q", got)
	}
	if got := profileStopLabel(start, start.Add(profileWindow-time.Millisecond)); got != "停止剖析 · 1s" {
		t.Errorf("最后一刻应向上取整成 1s，得到 %q（0s 意味着已经停了）", got)
	}
	if got := profileStopLabel(start, start.Add(profileWindow)); got != "停止剖析 · 0s" {
		t.Errorf("到点应显示 0s，得到 %q", got)
	}

	src, err := os.ReadFile("sync_panel.go")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(string(src), "profileStopLabel(a.ProfStarted, time.Now())") {
		t.Error("调用点还在用 c.Now()：和 time.Now() 存的起点不是同一个时基")
	}
}

// 写盘只往新写：后拍的快照先落盘之后，先拍的那份必须被丢掉。
//
// 两次快照各起一个 goroutine 抢同一把锁。先拍的那份被调度得晚就会后写盘，
// state.json 回退到旧状态——用户看到的是「刚改的设置重启后没了」。
func TestWriteStateNeverGoesBackwards(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		t.Skip("拿不到配置目录")
	}
	a := newApp()

	newer := []byte(`{"v":"new"}`)
	older := []byte(`{"v":"old"}`)

	// 先写新的（seq 2），再写旧的（seq 1）：旧的必须被丢。
	a.writeState(2, newer)
	a.writeState(1, older)

	got, err := os.ReadFile(filepath.Join(cfg, "alive", "state.json"))
	if err != nil {
		t.Fatalf("读回 state.json 失败：%v", err)
	}
	if string(got) != string(newer) {
		t.Fatalf("盘上是 %s，应为 %s：旧快照把新快照覆盖了", got, newer)
	}
}

// markDirty 排上的定时器必须留句柄，快照时取消。
//
// 不取消的话 FlushState 之后它到点还会异步写一次——进程随时先退出，那次写盘
// 悬空，state.json 可能被写出一份「关窗之后」的半新状态。
func TestStateSnapshotCancelsPendingTimer(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	a := newApp()
	a.Win = new(mygo.Window)
	a.markDirty()
	if a.stateTimer == nil {
		t.Fatal("前提不成立：markDirty 没排定时器")
	}
	if _, _, ok := a.stateSnapshot(); !ok {
		t.Fatal("前提不成立：dirty 状态下拍不出快照")
	}
	if a.stateTimer != nil {
		t.Error("快照之后定时器还立着：到点会再异步写一次盘")
	}
}

// exitArm 等待第二拍时必须自己请求下一帧。
//
// arm 那一拍只调一次 invalidateWindows（= 一帧）。没有动画在跑时没人再请求
// 帧，第二拍永远不来——Esc 悬在那里直到用户动鼠标。
func TestExitArmKeepsRequestingFrames(t *testing.T) {
	body := funcBody(t, "surface_motion.go", "func (a *App) tickExitArm()")
	i := containsStrIndex(body, "a.buildSeq < a.exitArmAt+2")
	if i < 0 {
		t.Fatal("找不到第二拍的判据")
	}
	tail := body[i:]
	if j := containsStrIndex(tail, "\n\t}"); j > 0 {
		tail = tail[:j]
	}
	if !containsStr(tail, "a.invalidateWindows()") {
		t.Error("等第二拍时没有请求下一帧：没有动画在跑就会永远等下去")
	}
}

func containsStr(s, sub string) bool { return containsStrIndex(s, sub) >= 0 }

func containsStrIndex(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
