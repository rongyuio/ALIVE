package ui

import (
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"alive/internal/live"
)

// 缓冲中的格子必须报出来。
//
// 判据是纯函数，因为真正的画面停住要在真实解码器上才复现；这里验的是
// "有画面、在播、时间轴停了超过阈值"这三件事同时成立才算缓冲。
func TestWallStalledDetection(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		name    string
		ready   bool
		playing bool
		since   time.Time
		want    bool
	}{
		{"在播但时间轴停住", true, true, now.Add(-3 * time.Second), true},
		{"刚停一下，还在容忍内", true, true, now.Add(-300 * time.Millisecond), false},
		{"暂停的格子不算缓冲", true, false, now.Add(-time.Minute), false},
		{"还没出图不算缓冲（首帧预算管那个）", false, true, now.Add(-time.Minute), false},
		{"没有基准不算", true, true, time.Time{}, false},
	} {
		if got := wallStalled(tc.ready, tc.playing, tc.since, now); got != tc.want {
			t.Errorf("%s：wallStalled=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 缓冲态要在格子上看得见。
//
// 不报的话用户看到的是一张冻住的图，会以为那是实时画面——这比缓冲本身更糟。
func TestWatchStalledTileShowsBadge(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	s := fakeSession(t, app, rooms[0])
	s.stalled = true
	tt.Frame()

	if !tt.HasText("缓冲中…") {
		t.Fatalf("缓冲中的格子没有提示：texts=%q", tt.Texts())
	}

	s.stalled = false
	tt.Frame()
	if tt.HasText("缓冲中…") {
		t.Fatal("恢复播放之后提示还在")
	}
}

// 关掉引擎时留下签名地址，下次重连直接用。
//
// 直播重连里最贵的一段不是网络，是重新问平台要地址。地址有寿命（斗鱼实测
// 300 秒），在有效期内没必要再问一次。
func TestWatchCloseKeepsSignedURLForReconnect(t *testing.T) {
	app := NewMockApp()
	app.watch.init()
	key := "bilibili|1000"
	s := &roomSession{
		key: key,
		up: live.Variant{
			URL:     "https://example.invalid/live.flv",
			Expires: time.Now().Add(5 * time.Minute),
		},
	}
	app.watch.sessions[key] = s

	app.closeSession(s)

	r, ok := app.takeResume(key)
	if !ok {
		t.Fatal("关掉引擎之后没有留下签名地址")
	}
	if r.up.URL != s.up.URL {
		t.Fatalf("留下的地址是 %q，期望 %q", r.up.URL, s.up.URL)
	}

	// 一次性：用过就没了，不能反复拿一份过期凭据去撞。
	if _, again := app.takeResume(key); again {
		t.Fatal("同一份凭据被取走了两次")
	}
}

// 已经过期的地址不保留——留着也只会让下一次重连先失败一次。
func TestWatchCloseDropsExpiredURL(t *testing.T) {
	app := NewMockApp()
	app.watch.init()
	key := "bilibili|1000"
	s := &roomSession{
		key: key,
		up:  live.Variant{URL: "https://example.invalid/live.flv", Expires: time.Now().Add(-time.Minute)},
	}
	app.watch.sessions[key] = s

	app.closeSession(s)

	if _, ok := app.takeResume(key); ok {
		t.Fatal("过期的地址还被留下了")
	}
}

// 没声明寿命的平台给一个保守窗口：过期了也只是多一次完整取流。
func TestWatchCloseGuessesLifetimeWhenPlatformDoesNot(t *testing.T) {
	app := NewMockApp()
	app.watch.init()
	key := "douyin|1000"
	s := &roomSession{key: key, up: live.Variant{URL: "https://example.invalid/live.flv"}}
	app.watch.sessions[key] = s

	app.closeSession(s)

	r, ok := app.takeResume(key)
	if !ok {
		t.Fatal("没声明寿命时不该直接丢掉")
	}
	if d := time.Until(r.until); d <= 0 || d > watchResumeGuess+time.Second {
		t.Fatalf("保守窗口是 %v，不在 (0, %v] 内", d, watchResumeGuess)
	}
}

// 「暂离时省流量」要落盘：用户设过一次，重启后还是那个选择。
func TestWatchSaveDataPersists(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.WatchSaveData = true
	app.stateDirty = true
	app.saveState()

	path := statePath()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !NewMockApp().WatchSaveData {
		t.Fatal("重启之后「暂离时省流量」丢了")
	}

	// 默认是关：暂离默认要无缝，省流量是用户主动开的。
	other := NewMockApp()
	other.WatchSaveData = false
	other.stateDirty = true
	other.saveState()
	time.Sleep(100 * time.Millisecond)
	if NewMockApp().WatchSaveData {
		t.Fatal("关掉之后重启又变成了开")
	}
}

// 开关画在墙上，改它就是改行为。
func TestWatchSaveDataToggle(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	tt.Frame()

	label := "暂离时省流量：关"
	if _, ok := tt.Find(label); !ok {
		t.Fatalf("墙上没有「暂离时省流量」：texts=%q", tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !app.WatchSaveData {
		t.Fatal("点了开关但状态没变")
	}
	if _, ok := tt.Find("暂离时省流量：开"); !ok {
		t.Fatalf("开关没有变成「开」：texts=%q", tt.Texts())
	}
}

// 离线格要自己回来：蹲开播是墙的核心场景之一，但探测必须无感。
//
// 退避到期只静默问一次平台，不动格子上的任何状态；平台说开播了才起播。
func TestOfflineTileProbesSilentlyAndOpensWhenLive(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	prov := &probeProvider{live: true}
	useProvider(t, prov)
	runOnMainNow(t)
	app.Win = &mygo.Window{}

	s := &roomSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播",
		failures: 1, retryAt: time.Now().Add(time.Minute),
	}
	app.watch.init()
	app.watch.sessions[key] = s

	now := time.Now()
	app.watchTick(now)
	if n := prov.roomInfoCalls(); n != 0 {
		t.Fatalf("退避还没到就探测了平台：%d 次", n)
	}

	app.watchTick(now.Add(time.Hour))
	waitFor(t, func() bool { return !s.probing })
	if n := prov.roomInfoCalls(); n != 1 {
		t.Fatalf("退避到期只该静默探测一次，实际 %d 次", n)
	}
	if s.offline {
		t.Fatal("平台说开播了，格子还停在未开播")
	}
}

// 下播落终态时要排上重试，否则离线格永远停在那里。
func TestWatchOfflineSchedulesRetry(t *testing.T) {
	app := NewMockApp()
	app.watch.init()
	s := &roomSession{key: "bilibili|1", room: Streamer{Platform: "bilibili", RoomID: "1"}}
	app.watch.sessions[s.key] = s

	app.watchOffline(s)

	if s.retryAt.IsZero() {
		t.Fatal("下播之后没有排重试：蹲开播不成立")
	}
	if !s.offline {
		t.Fatal("下播应当落离线态")
	}
	if s.failures == 0 {
		t.Fatal("下播没有累加退避：会每 2 秒问一次平台")
	}
}

// 最小化也要算"墙看不见"。
//
// Shell 每帧按路由判断墙在不在屏上，但最小化时 Shell 根本不重建，那条路
// 走不到。没有 WatchHidden，开着「暂离时省流量」最小化窗口，墙上的会话
// 永远不会被回收。
func TestWatchHiddenCoversMinimize(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()

	app.WatchSaveData = true
	app.WatchHidden()
	app.watchReap(time.Now().Add(time.Hour))
	if n := len(app.watch.sessions); n != 0 {
		t.Fatalf("最小化之后还有 %d 路会话没回收", n)
	}

	// 恢复：标记清掉，下一次隐藏重新计时。
	app.WatchShown()
	app.watchReap(time.Now().Add(2 * time.Hour))
	if !app.watch.hiddenSince.IsZero() {
		t.Fatal("恢复之后 hiddenSince 还留着")
	}
}
