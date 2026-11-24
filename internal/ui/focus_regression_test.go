package ui

// 播放与焦点组（G1）的三条回归：热切换起播音量、进播放页接管墙上的同房预览、
// 取流失败后离开播放页的焦点收尾。
//
// 三条都先在改动前的代码上跑红，再改代码。判据来自
// doc/architecture/surfaces.md 的不变量 2 / 3 / 5。

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
)

// optsRecorder 记下 openNative 收到的选项，然后按失败返回。
//
// 这里只需要「交给引擎的 Options 长什么样」，不需要真引擎：热切换起播漏音
// 的判据就在 Options.StartSilent 上。
type optsRecorder struct {
	mu   sync.Mutex
	opts []playback.Options
}

func (r *optsRecorder) open(_ context.Context, _ string, o playback.Options) (*playback.Engine, error) {
	r.mu.Lock()
	r.opts = append(r.opts, o)
	r.mu.Unlock()
	return nil, playback.ErrUnsupported
}

func (r *optsRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.opts)
}

func (r *optsRecorder) first() playback.Options {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.opts[0]
}

// 热切换的新一路必须静音起播。
//
// mpv 的默认音量是 100%：openNative 不带 StartSilent 时，新一路从 loadfile
// 那一刻就在出声，而 adoptSwap 要等「新路出图 / 出声音」之后才第一次
// SetVolume(a.focusVolume())。于是每次地址续期都有两路叠音，新路还是满音量；
// 用户静音时也会漏一声。
func TestHotSwapStartsSilent(t *testing.T) {
	rec := &optsRecorder{}
	oldOpen, oldRun, oldProv := openNative, runOnMain, providerFor
	openNative = rec.open
	// 回调就地执行：runHotSwap 本来就在主线程语义下调用，测试里只有这一个
	// goroutine 在写媒体层字段。
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		fn()
		return true
	}
	providerFor = func(live.Platform) (live.Provider, bool) {
		return &stubStreamer{
			full:     live.Stream{Live: true},
			selected: live.Variant{Label: "原画", URL: "http://example.invalid/live.flv"},
		}, true
	}
	t.Cleanup(func() { openNative, runOnMain, providerFor = oldOpen, oldRun, oldProv })

	a := NewMockApp()
	a.Win = &mygo.Window{}
	sess := playbackSession{seq: 1, win: a.Win, ctx: context.Background(), id: "swap#1", start: time.Now()}

	a.runHotSwap(sess, swapRequest{platform: live.Douyu, roomID: "1", quality: "原画"}, "expire")

	if rec.count() != 1 {
		t.Fatalf("新一路开了 %d 次，期望 1 次", rec.count())
	}
	if !rec.first().StartSilent {
		t.Fatal("热切换的新一路没有静音起播：它会按 mpv 默认 100% 音量先出声")
	}
}

// 首路同样要静音起播：adoptPlayback 一定会按 a.focusVolume() 推一次音量
// （player_media.go adoptPlayback），所以从 0 抬起来是安全的。
// 不这么做，主线程繁忙、adopt 晚到时用户会先听到一声满音量。
func TestFirstOpenStartsSilent(t *testing.T) {
	rec := &optsRecorder{}
	oldOpen, oldRun := openNative, runOnMain
	openNative = rec.open
	// 不执行回调：这条只验交给引擎的选项，媒体层的接纳由别的用例覆盖。
	runOnMain = func(*mygo.Window, func()) bool { return true }
	t.Cleanup(func() { openNative, runOnMain = oldOpen, oldRun })

	a := NewMockApp()
	a.Win = &mygo.Window{}
	a.media.win = a.Win
	sess := playbackSession{seq: a.media.seq, win: a.Win, ctx: context.Background(), id: "open#1", start: time.Now()}

	if !a.startNativePlayback(sess, live.Variant{Label: "原画", URL: "http://example.invalid/live.flv"}) {
		t.Fatal("有窗口时起播不该被拒绝")
	}
	waitFor(t, func() bool { return rec.count() == 1 })
	if !rec.first().StartSilent {
		t.Fatal("首路没有静音起播：adopt 之前会先漏一声满音量")
	}
}

// 用户把音量拉到 100% 时，新引擎也必须真的收到那一次 SetVolume。
//
// 引擎按 StartSilent 起播，mpv 的真实音量是 0；ui.NewEngineVideoPlayer 却
// 把播放器记录的音量初始化成 1。单一写者 applySessionVolume 用记录值判断
// 要不要推：目标 1 == 记录 1，第一次推送被跳过，引擎永远停在 0。从分区
// 卡片进播放页（引擎在焦点建立之后才接管）正好是这个组合——画面在走，
// 没有声音。
//
// 期望值来自代码之外：所有引擎创建点都传 StartSilent（player_media.go 的
// 首路 / 热切换、watch_media.go 的墙格），而 StartSilent 在
// playback/engine.go 里就是 SetVolume(0)。
func TestFreshEngineGetsVolumeAtFullScale(t *testing.T) {
	a := seededApp(t)
	room := Streamer{Platform: "douyu", RoomID: "9", Name: "满音量"}
	s := a.sessionFor(watchKey(room), room)
	s.volume = 1
	a.setFocus(s)

	// 生产里这一步是 ui.NewEngineVideoPlayer：记录音量初始化为 1。
	p := gifPlayer(t)
	p.SetVolume(1)
	s.attachPlayer(p)
	if got := p.Volume(); got != 0 {
		t.Fatalf("挂上新播放器后记录音量是 %v，期望 0（引擎按 StartSilent 起播）", got)
	}
	a.applySessionVolume(s)
	if got := p.Volume(); !almostEqual(got, 1) {
		t.Fatalf("目标音量 1 没有推给引擎，播放器记录还是 %v", got)
	}
}

// 进播放页时必须收掉墙上为同一间房开的静音预览。
//
// 墙上的每一格都有一个引擎。从首页卡片或关注列表进入其中某一间的播放页时，
// 如果 ensureMedia 放着不管，这一间就会有两个引擎；退出播放页时
// watchReleaseActive 又用新的一路覆盖同一个 key，旧引擎失去唯一引用，
// mpv 线程活到进程退出。
func TestEnteringPlayerClosesWallPreviewOfSameRoom(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	fakeSession(t, app, rooms[0])
	tt.Frame()
	if app.watch.sessions[key] == nil {
		t.Fatal("夹具没有建出墙上的会话")
	}

	useProvider(t, &stubStreamer{
		full:     live.Stream{Live: true},
		selected: live.Variant{Label: "原画", URL: "http://example.invalid/live.flv"},
	})
	focusForTest(app, rooms[0])
	app.Router.Push(routePlayer)
	tt.Frame()

	// 真正的风险是「同一间房有两个引擎在解同一路流」，不是「墙上还留着这条
	// 会话」。现在同一间房只有一个 roomSession，进播放页只是让媒体层指它，
	// 所以断言改成：这一间在手上一份引擎，且媒体层和墙指的是同一个对象。
	s := app.watch.sessions[key]
	if s == nil {
		t.Fatal("进播放页以后墙上这一间的会话没了：它应该还在册，只是换个尺寸画")
	}
	if s.player == nil {
		t.Fatal("这一间没有引擎：进播放页不该把墙上的引擎关掉重开")
	}
	if app.media.session() != nil && app.media.session() != s {
		t.Fatal("媒体层和墙各拿着一份会话：同一间房被拆成了两个")
	}
	if n := countRoomEngines(app, key); n > 1 {
		t.Fatalf("同一间房有 %d 个引擎在解同一路流", n)
	}
}

// countRoomEngines 数这一间到底有几个引擎对象在跑。
func countRoomEngines(a *App, key string) int {
	seen := map[*ui.VideoPlayer]bool{}
	if s := a.watch.sessions[key]; s != nil && s.player != nil {
		seen[s.player] = true
	}
	if a.media.roomKey() == key && a.media.eng() != nil {
		seen[a.media.eng()] = true
	}
	return len(seen)
}

// 取流失败 / 未开播后离开播放页，岛上不能永远停在「后台收听」。
//
// 这一轮落回 loading=false、player=nil，但 roomKey 还留着。焦点只认 roomKey
// 的话，它就变成了一个没有引擎的「声音焦点」：岛画成收听头像，点回去还是
// 同一张错误页（surfaces.md 不变量 2 / 5）。
func TestLeavingAfterStreamFailureClearsIslandFocus(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	useProvider(t, &stubStreamer{full: live.Stream{Live: false}})

	app := NewMockApp()
	app.Platform = "douyu"
	t.Cleanup(app.CloseMedia)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	focusForTest(app, Streamer{Platform: "douyu", RoomID: "777", Name: "未开播"})
	app.Router.Push(routePlayer)
	tt.Frame()

	// 取流回来：平台说没开播，loading 落回 false，引擎始终没有建出来。
	stubs.pump(t)
	stubs.pump(t)
	if app.media.roomKey() == "" || app.media.eng() != nil || app.media.isLoading() {
		t.Fatalf("夹具状态不对：roomKey=%q player=%v loading=%v",
			app.media.roomKey(), app.media.eng() != nil, app.media.isLoading())
	}

	// 顶栏 tab 离开播放页：Shell 走 detachMedia（和用户点 tab 同一条路）。
	app.switchTab(app.Platform)
	tt.Frame()

	if got := app.islandFormOf(); got != islandIdle {
		t.Fatalf("取流失败后离开播放页，岛是 %v，期望时钟：没有引擎就没有声音焦点", got)
	}
	if got := app.focusKey(); got != "" {
		t.Fatalf("焦点还是 %q：它落在一个没有引擎的房间上", got)
	}
}
