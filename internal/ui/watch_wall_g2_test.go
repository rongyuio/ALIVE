package ui

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	mymedia "github.com/egoist/mygo/media"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/watch"
)

// G2 观测墙第二批：重试入口、首帧预算、下播分流、移出时的切片别名、满额提示。
//
// 每条都先在修复前的代码上跑红；夹具全部落在会话层，不发真实请求。

// stalledEngine 是「引擎开出来了、但永远不出图」的那一路。
//
// State 停在 Loading、Frame 永远没有画面，而播放器自己认为在播（暂停/结束
// 都为 false）。这正是首帧预算要兜的死地址：mpv 不报错、不结束，只是不给帧。
type stalledEngine struct{}

func (e *stalledEngine) SetWake(func()) {}
func (e *stalledEngine) Frame(time.Time) (mymedia.Picture, bool, bool) {
	return mymedia.Picture{}, false, false
}
func (e *stalledEngine) Interval() time.Duration       { return 0 }
func (e *stalledEngine) State() (mymedia.State, error) { return mymedia.Loading, nil }
func (e *stalledEngine) Size() (int, int)              { return 0, 0 }
func (e *stalledEngine) Duration() time.Duration       { return 0 }
func (e *stalledEngine) Position() time.Duration       { return 0 }
func (e *stalledEngine) Play()                         {}
func (e *stalledEngine) Pause()                        {}
func (e *stalledEngine) Seek(time.Duration)            {}
func (e *stalledEngine) SetLoop(bool)                  {}
func (e *stalledEngine) SetRate(float64)               {}
func (e *stalledEngine) SetVolume(float64)             {}
func (e *stalledEngine) SetMuted(bool)                 {}
func (e *stalledEngine) Close() error                  { return nil }

func stalledPlayer(t *testing.T) *ui.VideoPlayer {
	t.Helper()
	p := ui.NewEngineVideoPlayer(&stalledEngine{})
	t.Cleanup(p.Close)
	return p
}

// 缺陷 #10：active 格失败/未开播时，提示写着「点击重试」，点它却什么都不做。
//
// 激活同一格以前在 watchActivate 第一行直接 return；墙上的失败会话不属于
// 媒体层，「重新加载」走 retryStream 又因为 media.roomKey=="" 直接返回。
// 结果是这格没有任何有效重试入口，只能先激活别的格再点回来。
// 未开播 / 取流失败的格，重试入口是底栏那颗「重新加载」。
//
// 以前点格子本身就是重试（单击只切声音，对死格只重试不抢焦点）。单击改成
// "进这一间"之后，那条路走不通了：点下去会打开播放页。所以一步重试挪到
// 「重新加载」上——它本来就在，只是以前不是唯一入口。
func TestWatchReloadRetriesOfflineTile(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])

	s := &roomSession{
		key: key, room: rooms[0], offline: true,
		err: "主播暂时没有开播", failures: 3, retryAt: time.Now().Add(time.Minute),
	}
	app.watch.init()
	app.watch.sessions[key] = s
	tt.Frame()

	if !tt.HasText("主播暂时没有开播") {
		t.Fatalf("失败格没有给出提示：texts=%q", tt.Texts())
	}
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到格子")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("重新加载"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if s.offline {
		t.Fatal("点重新加载之后仍是未开播态：格子上没有有效重试入口")
	}
	if s.failures != 0 || !s.retryAt.IsZero() {
		t.Fatalf("重试没有清退避：failures=%d retryAt=%v", s.failures, s.retryAt)
	}
}

// 缺陷 #10（同一个洞的另一处）：hover 控制条上的「重新加载」对 active 走
// retryStream，而这一路还挂在墙的 pending 会话上，media.roomKey 为空，
// retryStream 立刻返回，按钮是空点。
func TestWatchReloadActiveWallSession(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])

	s := &roomSession{
		key: key, room: rooms[0], offline: true,
		err: "主播暂时没有开播", failures: 2,
	}
	app.watch.init()
	app.watch.sessions[key] = s
	focusForTest(app, rooms[0])
	app.setFocusPlaying(true)
	tt.Frame()

	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到格子")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("重新加载"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if s.offline {
		t.Fatal("active 格点「重新加载」后仍是未开播态：这一路不在媒体层，retryStream 是空点")
	}
	if s.failures != 0 {
		t.Fatalf("手动重试没有清退避：failures=%d", s.failures)
	}
}

// 缺陷 #11 / #39：引擎开出之后没有首帧预算。
//
// watchTick 的 20 秒超时只包 s.player==nil && s.loading；引擎一建出来
// s.loading 就置 false，而 Ready 为假时 watchDeadReason 只是刷新 stillSince。
// 于是一条「mpv 打开成功、不报错、不出帧」的死地址可以永远停在
// 「正在拉取直播流…」。播放页有 firstFrameBudget 兜这个，墙上漏了。
func TestWatchFirstFrameBudgetOnOpenEngine(t *testing.T) {
	rooms := watchTestRooms(2)
	app, _ := newWatchMock(t, rooms)
	// 首帧超时之后要问一次平台；这里给一个没有 RoomInfoer 的实现，
	// 断言落在"引擎被收掉"这一半，测试不发真实请求。
	useProvider(t, plainProvider{})
	now := time.Now()

	stalled := fakeSession(t, app, rooms[0])
	stalled.player = stalledPlayer(t)
	stalled.openedAt = now.Add(-firstFrameBudget - time.Second)
	stalled.stillSince = now

	// 同一拍里另一格刚开引擎、还在预算内：它不能被误杀。
	fresh := fakeSession(t, app, rooms[1])
	fresh.player = stalledPlayer(t)
	fresh.openedAt = now.Add(-firstFrameBudget / 2)

	app.watchTick(now)

	if stalled.player != nil {
		t.Fatal("引擎开着却超过首帧预算，watchTick 没有收掉它：这一格会永远停在「正在拉取直播流…」")
	}
	if stalled.err != "首帧超时" {
		t.Fatalf("超时原因记成了 %q，期望「首帧超时」", stalled.err)
	}
	if fresh.player == nil {
		t.Fatal("预算还没到的格子被提前砍掉了")
	}
}

// 缺陷 #39 的交接方向：从播放页退回墙上时，媒体层已经等过的首帧时长要带过去。
//
// 一首帧预算跟着会话走，不跟表面走。
//
// 以前退回墙上会新建一个会话，起播时刻从零开始，于是一条在播放页里已经
// 卡了 20 秒的死地址回到墙上又能再卡 20 秒——重复计时，实际永远等不到。
// 现在一间房只有一个 roomSession，openedAt 本来就在里面。
func TestWatchReleaseCarriesFirstFrameClock(t *testing.T) {
	rooms := watchTestRooms(1)
	app := NewMockApp()
	app.WatchRooms = rooms
	key := watchKey(rooms[0])
	now := time.Now()

	app.watch.init()
	app.watch.rects[key] = watch.Rect{W: 640, H: 360}
	s := &roomSession{key: key, room: rooms[0], player: stalledPlayer(t)}
	s.openedAt = now.Add(-firstFrameBudget - time.Second)
	focusSessionForTest(app, s)
	app.setFocus(nil)

	if got := app.watch.sessions[key]; got != s {
		t.Fatal("结束焦点之后会话不在墙上")
	}
	if !s.openedAt.Equal(now.Add(-firstFrameBudget - time.Second)) {
		t.Fatal("换表面把起播时刻重置了")
	}

	app.watchTick(now)
	s = app.watch.sessions[key]
	if s == nil || s.player != nil {
		t.Fatal("媒体层已经等过整个预算，退回墙上后没有接着超时：时钟被重置了")
	}
	if s.err != "首帧超时" {
		t.Fatalf("超时原因记成了 %q，期望「首帧超时」", s.err)
	}
}

// 接管方向：墙上还没出图的一路成为焦点时，不能直接记成「已出图」。
//
// 以前 watchAdoptActive 写死 m.firstFrame=true，媒体层的 needFirstFrameTimeout
// 从此不再看这一路，死地址接管进播放页永远转圈。现在接管只是 setFocus，
// 会话本身没有任何字段被改写。
func TestWatchAdoptKeepsFirstFramePending(t *testing.T) {
	rooms := watchTestRooms(1)
	app := NewMockApp()
	key := watchKey(rooms[0])
	opened := time.Now().Add(-firstFrameBudget / 2)

	app.watch.init()
	s := &roomSession{key: key, room: rooms[0], player: stalledPlayer(t)}
	s.openedAt = opened
	app.watch.sessions[key] = s
	focusSessionForTest(app, s)
	t.Cleanup(func() {
		if app.media.cancel != nil {
			app.media.cancel()
		}
	})

	if app.media.firstFrame() {
		t.Fatal("引擎还没出图，媒体层却记成已出图：首帧预算从此不再管这一路")
	}
	if !app.media.opened().Equal(opened) {
		t.Fatalf("接管没有带上墙上的起播时刻：openedAt=%v，期望 %v", app.media.opened(), opened)
	}
}

// 缺陷 #12 / #41：roomSession.prov 从未赋值，watchEnded 里的 RoomInfoer
// 断言恒为 false，于是「平台说 live=false 就落未开播」永远走不到，判死一律
// 白走一整轮取流。修复要求 nil 字段也能按平台现查一次。
func TestWatchEndedAsksPlatformWhenProvMissing(t *testing.T) {
	rooms := watchTestRooms(1)
	app := NewMockApp()
	key := watchKey(rooms[0])

	prov := &offlineRoomProvider{}
	useProvider(t, prov)

	oldRun := runOnMain
	var once sync.Once
	done := make(chan struct{})
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		// 主线程同步执行，并在接纳回调落地之后放行测试。
		fn()
		once.Do(func() { close(done) })
		return true
	}
	t.Cleanup(func() { runOnMain = oldRun })

	app.watch.init()
	s := &roomSession{key: key, room: rooms[0], player: gifPlayer(t)}
	app.watch.sessions[key] = s

	app.watchEnded(s, "播放器报告结束")
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("等不到 watchEnded 的平台确认结果")
	}

	if prov.lookups.Load() == 0 {
		t.Fatal("watchEnded 没有向平台确认：prov 为 nil 时被当成了「还在播」")
	}
	if !s.offline {
		t.Fatalf("平台说已经下播，格子却走了重连：err=%q failures=%d", s.err, s.failures)
	}
	if s.err != "主播暂时没有开播" {
		t.Fatalf("落到的提示是 %q，期望「主播暂时没有开播」", s.err)
	}
}

// offlineRoomProvider 只回答「这个房间已经下播」。
type offlineRoomProvider struct {
	plainProvider
	lookups atomic.Int32
}

func (p *offlineRoomProvider) RoomInfo(context.Context, string) (live.Room, error) {
	p.lookups.Add(1)
	return live.Room{Live: false}, nil
}

// 缺陷 #13：watchRemove 用 a.WatchRooms[:0] 原地压缩，改写了 watchWall 正在
// 遍历的同一份快照。点「移出观测」在构建这一格时就同步执行，余下的循环按
// 左移过的房间继续建格：同一间房一帧内画两格，被删的后一位被跳过。
func TestWatchRemoveKeepsWallSnapshot(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	tt.Frame()

	// watchWall 每帧开头拿到的就是这份切片头。期望值必须单独拷出来：
	// rooms 和快照共用底层数组，拿它当期望就是在和同一份被改写的数据比。
	want := make([]string, len(rooms))
	for i, room := range rooms {
		want[i] = watchKey(room)
	}
	snapshot := app.WatchRooms
	app.watchRemove(rooms[1])

	if len(snapshot) != 3 {
		t.Fatalf("视图快照的长度被改成了 %d，期望 3", len(snapshot))
	}
	for i, key := range want {
		if got := watchKey(snapshot[i]); got != key {
			t.Fatalf("快照第 %d 格被就地改写：%q，期望 %q（整帧会重复/跳格）", i, got, key)
		}
	}
	if len(app.WatchRooms) != 2 ||
		watchKey(app.WatchRooms[0]) != watchKey(rooms[0]) ||
		watchKey(app.WatchRooms[1]) != watchKey(rooms[2]) {
		t.Fatalf("移出后的墙不对：%+v", app.WatchRooms)
	}
}

// 缺陷 #40：观测墙满 12 间时用 notifyPlayback 提示，而它只画在播放页。
// 两个入口（首页卡片右键、关注页行菜单）都不在播放页，加入被静默拒绝。
func TestWatchAddOverCapToasts(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(watch.MaxRooms + 1)
	app.WatchRooms = rooms[:watch.MaxRooms]

	if app.watchAdd(rooms[watch.MaxRooms]) {
		t.Fatal("超过上限还加进去了")
	}
	want := fmt.Sprintf("观测墙最多 %d 间", watch.MaxRooms)
	// 提示要真的出现在常驻外壳上，而不只是写进某个字段。
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if !tt.HasText(want) {
		t.Fatalf("满额提示没有落到常驻外壳的 toast：texts=%q，期望 %q", tt.Texts(), want)
	}
}

// 加入观测墙的两个入口（首页卡片右键、关注页行菜单）都不在墙上，
// 成功与否必须在常驻外壳上给回音，否则用户不知道到底加没加进去。
func TestWatchAddNotifies(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(2)

	if !app.watchAdd(rooms[0]) {
		t.Fatal("没加入观测墙")
	}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if want := "已加入观测墙：" + rooms[0].Name; !tt.HasText(want) {
		t.Fatalf("加入后没有提示：texts=%q，期望 %q", tt.Texts(), want)
	}
}

// 重复加入不报错，但也不能点了毫无反应。
func TestWatchAddDuplicateNotifies(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(1)
	app.WatchRooms = []Streamer{rooms[0]}

	if app.watchAdd(rooms[0]) {
		t.Fatal("重复的房间又加了一次")
	}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if !tt.HasText("该直播间已在观测墙里") {
		t.Fatalf("重复加入没有提示：texts=%q", tt.Texts())
	}
}
