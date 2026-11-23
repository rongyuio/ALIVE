package ui

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/watch"
)

// 离线探测的三条硬规矩：
//
//  1. 探测无感：只问平台，不动格子上的 loading / err / offline。
//  2. 不在墙上完全不探测：看不见的格子开没开播没有意义。
//  3. 回到墙上立刻探一次；平时按退避，最长 5 分钟跟上开播。

type probeProvider struct {
	mu    sync.Mutex
	live  bool
	calls int
}

func (p *probeProvider) Platform() live.Platform { return live.Bilibili }
func (p *probeProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (p *probeProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (p *probeProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

func (p *probeProvider) RoomInfo(context.Context, string) (live.Room, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return live.Room{Platform: live.Bilibili, RoomID: "1", Live: p.live}, nil
}

func (p *probeProvider) roomInfoCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// 探测说开播之后会走正常起播；这里让它停在"没有可播放地址"，
// 测试既不联网，也不依赖引擎。
func (p *probeProvider) StreamForPlayback(context.Context, string, string, string) (live.Stream, live.Variant, error) {
	return live.Stream{Live: true}, live.Variant{}, nil
}

// runOnMainNow 让回主线程的调用当场执行，探测结果不用等视图重建。
func runOnMainNow(t *testing.T) {
	t.Helper()
	old := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = old })
}

// 离线探测必须无感：平台说还没开播时，格子上的东西一点都不许变。
func TestOfflineProbeKeepsTheTileUnchanged(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	prov := &probeProvider{live: false}
	useProvider(t, prov)
	runOnMainNow(t)
	app.Win = &mygo.Window{}

	s := &roomSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播",
		failures: 1,
	}
	app.watch.init()
	app.watch.sessions[key] = s

	now := time.Now().Add(time.Hour)
	s.retryAt = now.Add(-time.Second)
	app.watchTick(now)
	waitFor(t, func() bool { return !s.probing })

	if n := prov.roomInfoCalls(); n != 1 {
		t.Fatalf("到点之后该静默探测一次，实际 %d 次", n)
	}
	if !s.offline || s.err != "主播暂时没有开播" || s.loading {
		t.Fatalf("探测改了格子的状态：offline=%v err=%q loading=%v",
			s.offline, s.err, s.loading)
	}
	if d := s.retryAt.Sub(now); d <= 0 || d > 5*time.Minute {
		t.Fatalf("下一次探测在 %v 之后：要么立刻重问，要么超过 5 分钟上限", d)
	}
}

// 不在墙上就完全不探测。
func TestOfflineProbeDoesNothingOffWall(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	prov := &probeProvider{live: true}
	useProvider(t, prov)
	runOnMainNow(t)
	app.Win = &mygo.Window{}
	app.Router = ui.NewRouter(routeHome)

	s := &roomSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播",
	}
	app.watch.init()
	app.watch.sessions[key] = s
	now := time.Now()
	s.retryAt = now.Add(-time.Second)

	app.watchTick(now)

	if n := prov.roomInfoCalls(); n != 0 {
		t.Fatalf("人不在墙上，仍探测了 %d 次平台", n)
	}
	if s.player != nil || s.loading {
		t.Fatal("人不在墙上，仍然起播了这一格")
	}
}

// 墙回到屏上时，离线格不该等离开前攒下的退避：立刻探一次。
func TestOfflineProbeRunsImmediatelyBackOnWall(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	prov := &probeProvider{live: false}
	useProvider(t, prov)
	runOnMainNow(t)
	app.Win = &mygo.Window{}

	s := &roomSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播",
		failures: 4, retryAt: time.Now().Add(4 * time.Minute),
	}
	app.watch.init()
	app.watch.sessions[key] = s
	app.watch.hiddenSince = time.Now().Add(-time.Minute)

	app.watchShow(map[string]watch.Rect{key: {W: 640, H: 360}})
	if !s.retryAt.IsZero() {
		t.Fatal("回到墙上之后没有清掉离线退避：最早也要等 4 分钟才探")
	}

	app.watchTick(time.Now())
	waitFor(t, func() bool { return prov.roomInfoCalls() == 1 })
}

// 首帧超时之后要问平台：真下播就落未开播，不能永远按"在播"重开。
func TestFirstFrameTimeoutAsksPlatform(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	prov := &probeProvider{live: false}
	useProvider(t, prov)
	runOnMainNow(t)
	app.Win = &mygo.Window{}

	s := fakeSession(t, app, rooms[0])
	s.player = stalledPlayer(t)
	s.openedAt = time.Now().Add(-firstFrameBudget - time.Second)
	s.stillSince = time.Now()

	app.watchTick(time.Now())

	if s.player != nil {
		t.Fatal("过了首帧预算的引擎没有被收掉")
	}
	waitFor(t, func() bool { return s.offline })
	if n := prov.roomInfoCalls(); n != 1 {
		t.Fatalf("首帧超时之后该问一次平台，实际 %d 次", n)
	}
	if s.err != "主播暂时没有开播" {
		t.Fatalf("平台确认下播后落成了 %q", s.err)
	}
}

// failures 只在真的出了第一帧之后清零。
//
// 引擎开出就清零会让"平台说在播、实际不出帧"的格子 20 秒超时 → 2 秒重开，
// 永远循环。
func TestFirstFrameResetsFailures(t *testing.T) {
	app := NewMockApp()
	s := &roomSession{failures: 3, retryAt: time.Now().Add(time.Minute)}

	app.watchFirstFrame(s)
	if !s.firstFrame {
		t.Fatal("出了第一帧却没有记下来")
	}
	if s.failures != 0 || !s.retryAt.IsZero() {
		t.Fatalf("第一帧之后退避没有清零：failures=%d retryAt=%v", s.failures, s.retryAt)
	}

	// 再调一次不该改别的：幂等。
	s.failures = 0
	app.watchFirstFrame(s)
	if s.failures != 0 {
		t.Fatal("第一帧的清理不幂等")
	}
}
