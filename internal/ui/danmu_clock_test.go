package ui

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 弹幕必须跟着画面走：播放时前进，暂停时停住，恢复时不补跳。
//
// 这里用注入的时钟推进，不 sleep。之前弹幕位置直接读 time.Now()，测试根本
// 推不动它——这本身就是它和播放时钟脱钩的证据。
func TestDanmuClockFollowsPicture(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	app.DanmuFilters = nil

	base := time.Now()
	cur := base
	app.clock = func() time.Time { return cur }
	app.danmuNow, app.danmuLast = base, base
	app.Danmu = []danmuLine{{Text: "时钟测试", Track: 0, Born: base, Dur: 10 * time.Second}}

	tt := ui.NewTester(app.Shell, 1440, 900)
	at := func() ui.Rect {
		t.Helper()
		r, ok := tt.Find("时钟测试")
		if !ok {
			t.Fatal("弹幕没有渲染")
		}
		return r
	}

	x0 := at().X
	cur = cur.Add(time.Second)
	app.tickDanmu()
	tt.Frame()
	x1 := at().X
	if x1 > x0-1 {
		t.Fatalf("播放 1 秒弹幕应该前进：x %.1f → %.1f", x0, x1)
	}

	// 用户暂停：画面不动，弹幕也不该动。
	app.setFocusPlaying(false)
	cur = cur.Add(2 * time.Second)
	app.tickDanmu()
	tt.Frame()
	x2 := at().X
	if math.Abs(float64(x2-x1)) > 0.5 {
		t.Fatalf("暂停 2 秒弹幕仍在走：x %.1f → %.1f", x1, x2)
	}

	// 恢复：从停下的位置继续，不能把暂停的 2 秒补成一次瞬移。
	app.setFocusPlaying(true)
	cur = cur.Add(500 * time.Millisecond)
	app.tickDanmu()
	tt.Frame()
	x3 := at().X
	if x2-x3 <= 0.5 {
		t.Fatalf("恢复后 0.5 秒弹幕没有继续前进：x %.1f → %.1f", x2, x3)
	}
	if x2-x3 > (x0-x1)*0.75 {
		t.Fatalf("恢复后发生跳变：0.5 秒移动 %.1f，按 1 秒 %.1f 推算应约一半", x2-x3, x0-x1)
	}
}

// 轨道分配必须用实际轨道数，不能拿 seq 取模一个写死的 14。
//
// 900×600 的窗口扣掉顶栏和弹幕区比例后只有 8 条轨道；旧代码给第 9 条弹幕
// Track=8，渲染时再 %8 撞回第 0 条，两条弹幕直接叠在一起。
func TestDanmuTracksFitActualLaneCount(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	app.DanmuFilters = nil
	app.setFocusPlaying(true)
	app.danmuTracks = 8
	app.danmuNow = time.Now()
	app.media.danmuSeq = 7

	for i := 0; i < 20; i++ {
		app.applyDanmu(7, live.Danmu{Content: "轨道测试"})
	}
	if len(app.Danmu) == 0 {
		t.Fatal("一条弹幕都没上屏")
	}
	for i, d := range app.Danmu {
		if d.Track < 0 || d.Track >= app.danmuTracks {
			t.Fatalf("第 %d 条弹幕 Track=%d，超出实际轨道数 %d", i, d.Track, app.danmuTracks)
		}
	}
}

// 时钟推进规则：画面前进时按墙钟走，采样间隙照走，真停顿才冻结，
// 位置跳变只更新基准。
func TestDanmuAdvanceRules(t *testing.T) {
	const dt = 16 * time.Millisecond
	const grace = danmuStallGrace
	for _, tc := range []struct {
		name      string
		step      time.Duration
		has       bool
		playing   bool
		pos       time.Duration
		last      time.Duration
		still     time.Duration
		wantAdd   time.Duration
		wantStill time.Duration
	}{
		{"画面前进 16ms，按墙钟走 16ms", dt, true, true, 1016 * time.Millisecond, 1000 * time.Millisecond, 0, dt, 0},
		{"采样间隙内位置没变，照样走", dt, true, true, 1000 * time.Millisecond, 1000 * time.Millisecond, 30 * time.Millisecond, dt, 46 * time.Millisecond},
		{"停顿超过宽限，冻结", dt, true, true, 1000 * time.Millisecond, 1000 * time.Millisecond, grace, 0, grace + dt},
		{"用户暂停，不等宽限当场停住", dt, true, false, 1000 * time.Millisecond, 1000 * time.Millisecond, 30 * time.Millisecond, 0, 0},
		{"换路/seek 前跳 10s，只走墙钟", dt, true, true, 11000 * time.Millisecond, 1000 * time.Millisecond, grace, dt, 0},
		{"换路后位置变小，只更新基准", dt, true, true, 200 * time.Millisecond, 9000 * time.Millisecond, grace, dt, 0},
		{"无播放器、暂停", dt, false, false, 0, 0, 0, 0, 0},
		{"无播放器、播放", dt, false, true, 0, 0, 0, dt, 0},
		{"时间没走", 0, true, true, 1000 * time.Millisecond, 1000 * time.Millisecond, 0, 0, 0},
	} {
		add, still := danmuAdvance(tc.step, tc.has, tc.playing, tc.pos, tc.last, tc.still)
		if add != tc.wantAdd || still != tc.wantStill {
			t.Errorf("%s: add=%v still=%v，期望 %v / %v", tc.name, add, still, tc.wantAdd, tc.wantStill)
		}
	}
}

// time-pos 的采样间隙不能变成步伐。
//
// mpv 按视频帧更新 time-pos：30fps 流上每两帧界面才看到一次位置变化。
// 拿位置增量当步长，60fps 的界面就会「停一帧、走一帧」——弹幕看起来是
// 30Hz 的位移，就是「顿」。这里喂进真实的采样节奏，要求每帧步长一致。
func TestDanmuClockStepsEvenlyAcrossSampleGaps(t *testing.T) {
	const frame = time.Second / 60
	const sample = 2 * frame // 30fps 视频的 time-pos 更新间隔
	pos, last, still := time.Duration(0), time.Duration(0), time.Duration(0)
	var adds []time.Duration
	for i := 0; i < 60; i++ {
		if i%2 == 0 {
			pos += sample
		}
		add, newStill := danmuAdvance(frame, true, true, pos, last, still)
		last, still = pos, newStill
		adds = append(adds, add)
	}
	for i, add := range adds {
		if add != frame {
			t.Fatalf("第 %d 帧走了 %v，期望每帧 %v；步长序列 %v", i, add, frame, adds)
		}
	}
}

// 位置必须每帧重算。只在 33ms 的 tick 里推进时钟，弹幕就是 30Hz——
// 画面 60fps，看起来像掉帧。这里不调用 tickDanmu，只推进帧。
func TestDanmuMovesEveryFrame(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	app.DanmuFilters = nil
	base := time.Now()
	cur := base
	app.clock = func() time.Time { return cur }
	app.danmuNow, app.danmuLast = base, base
	app.Danmu = []danmuLine{{Text: "逐帧测试", Track: 0, Born: base, Dur: 10 * time.Second}}

	tt := ui.NewTester(app.Shell, 1440, 900)
	x0, ok := tt.Find("逐帧测试")
	if !ok {
		t.Fatal("弹幕没有渲染")
	}
	cur = cur.Add(16 * time.Millisecond)
	tt.Frame() // 只推进一帧，不调用 tickDanmu
	x1, ok := tt.Find("逐帧测试")
	if !ok {
		t.Fatal("第二帧弹幕消失了")
	}
	if x1.X >= x0.X-0.05 {
		t.Fatalf("一帧内弹幕没有移动：x %.3f → %.3f（时钟只在 tick 里推进）", x0.X, x1.X)
	}
}

// 同轨接力：前一条已经完全进入，后一条不比它快，就可以放同一轨。
// 不是「必须换轨」——轨道够用时优先不同轨，但不该为了换轨浪费容量。
func TestDanmuSharesLaneWhenSafe(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	app.DanmuFilters = nil
	app.setFocusPlaying(true)
	app.danmuTracks = 1
	app.danmuStageW = 1200
	app.DanmuDur = 10 * time.Second
	base := time.Now()
	app.danmuNow, app.danmuLast = base, base
	app.media.danmuSeq = 1

	app.applyDanmu(1, live.Danmu{Content: "前一条弹幕"})
	if len(app.Danmu) != 1 {
		t.Fatalf("第一条没有上屏：%d", len(app.Danmu))
	}
	// 2 秒后：前一条早已完全进入，后一条宽度相同（同速），不会追上。
	app.danmuNow = base.Add(2 * time.Second)
	app.applyDanmu(1, live.Danmu{Content: "后一条弹幕"})
	if len(app.Danmu) != 2 {
		t.Fatalf("安全接力被拒绝：%d 条", len(app.Danmu))
	}
	if app.Danmu[1].Track != 0 {
		t.Fatalf("只有一条轨道，Track=%d", app.Danmu[1].Track)
	}
}

// 追尾拒绝 + 空轨优先：后一条更宽（更快），会追上第一条；另一条轨道虽然
// 也被占着，但那条能安全接力。应该换到第二条轨道，而不是硬塞回第一条。
func TestDanmuSkipsCatchUpLane(t *testing.T) {
	app := NewMockApp()
	app.danmuTracks = 2
	app.danmuStageW = 1200
	app.DanmuDur = 10 * time.Second
	base := time.Now()
	app.danmuLanes = []danmuLane{
		{at: base, w: 100},                       // 1 秒后仍可能被追上
		{at: base.Add(-8 * time.Second), w: 100}, // 已经飞了很久，能安全接力
	}
	got := app.takeDanmuTrack(base.Add(time.Second), 3000)
	if got != 1 {
		t.Fatalf("更宽的后一条放进了会追尾的轨道 %d，期望换到 1", got)
	}
}

// 从上往下找第一条放得下的轨道：上面那条能安全接力，就继续用它，
// 不要因为下面还有空轨就硬往下排——那会排成一条固定斜线。
func TestDanmuReusesFirstSafeLane(t *testing.T) {
	app := NewMockApp()
	app.danmuTracks = 2
	app.danmuStageW = 1200
	app.DanmuDur = 10 * time.Second
	base := time.Now()
	app.danmuLanes = []danmuLane{
		{at: base.Add(-5 * time.Second), w: 100}, // 已完全进入，能安全接力
		{},                                       // 空轨
	}
	if got := app.takeDanmuTrack(base.Add(time.Second), 100); got != 0 {
		t.Fatalf("上面那条放得下却排到了 %d", got)
	}
}
