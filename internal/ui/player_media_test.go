package ui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
)

// ---------------------------------------------------------------- 假边界

// nativeStub 替代真实播放引擎：记住交给它的上游地址，然后按预设失败。
//
// 真引擎会起 libmpv、连网络，单测里不能碰；而「引擎起不来」正是这里要验的
// 那条路径。
type nativeStub struct {
	mu   sync.Mutex
	urls []string
	err  error
}

func (n *nativeStub) open(_ context.Context, url string, _ playback.Options) (*playback.Engine, error) {
	n.mu.Lock()
	n.urls = append(n.urls, url)
	n.mu.Unlock()
	if n.err != nil {
		return nil, n.err
	}
	return nil, playback.ErrUnsupported
}

func (n *nativeStub) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.urls)
}

// mediaStubs 把起播流程的外部边界换成假的，测试结束自动还原。
type mediaStubs struct {
	native *nativeStub
	// queue 复刻 Window.Update 的异步语义：回调排进来，测试自己当「主线程」执行。
	// 这样流程里的写和测试里的读永远在同一个 goroutine 上。
	queue chan func()
}

func newMediaStubs(t *testing.T, nativeErr error) *mediaStubs {
	t.Helper()
	s := &mediaStubs{
		native: &nativeStub{err: nativeErr},
		queue:  make(chan func(), 8),
	}
	oldRun, oldProv := runOnMain, providerFor
	oldNative := openNative

	openNative = s.native.open
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		s.queue <- fn
		return true
	}

	t.Cleanup(func() {
		runOnMain, providerFor = oldRun, oldProv
		openNative = oldNative
	})
	return s
}

// pump 执行主线程队列里的下一个回调；没有回调就等，超时直接 fail。
func (s *mediaStubs) pump(t *testing.T) {
	t.Helper()
	select {
	case fn := <-s.queue:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("等不到主线程回调")
	}
}

// stubStreamer 是同时实现 PlaybackStreamer 的假平台。
type stubStreamer struct {
	full       live.Stream
	selected   live.Variant
	err        error
	gotQuality string
	gotLine    string
	calls      int
	streamed   int
}

func (p *stubStreamer) Platform() live.Platform { return live.Douyu }

func (p *stubStreamer) Categories(context.Context) ([]live.CategoryGroup, error) { return nil, nil }

func (p *stubStreamer) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}

func (p *stubStreamer) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

func (p *stubStreamer) Stream(ctx context.Context, roomID, line string) (live.Stream, error) {
	p.streamed++
	return p.full, p.err
}

func (p *stubStreamer) StreamForPlayback(ctx context.Context, roomID, line, quality string) (live.Stream, live.Variant, error) {
	p.calls++
	p.gotLine, p.gotQuality = line, quality
	if p.err != nil {
		return live.Stream{}, live.Variant{}, p.err
	}
	p.full.RoomID = roomID
	return p.full, p.selected, nil
}

// legacyStreamer 只实现旧的 Streamer，用来验回落路径。
type legacyStreamer struct {
	full live.Stream
	err  error
}

func (p *legacyStreamer) Platform() live.Platform { return live.Douyu }

func (p *legacyStreamer) Categories(context.Context) ([]live.CategoryGroup, error) { return nil, nil }

func (p *legacyStreamer) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}

func (p *legacyStreamer) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

func (p *legacyStreamer) Stream(ctx context.Context, roomID, line string) (live.Stream, error) {
	return p.full, p.err
}

// fakeDanmaku 只数 Close 次数。
type fakeDanmaku struct {
	mu     sync.Mutex
	closed int
}

func (d *fakeDanmaku) Close() {
	d.mu.Lock()
	d.closed++
	d.mu.Unlock()
}

func (d *fakeDanmaku) closes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

func useProvider(t *testing.T, prov live.Provider) {
	t.Helper()
	old := providerFor
	providerFor = func(live.Platform) (live.Provider, bool) { return prov, true }
	t.Cleanup(func() { providerFor = old })
}

func seededApp(t *testing.T) *App {
	t.Helper()
	a := NewMockApp()
	a.Player = Streamer{Platform: "douyu", RoomID: "123", Name: "主播"}
	a.media.roomKey = "douyu|123"
	// 媒体层的状态机默认站在播放页上：attachMedia 有路由判据，
	// 留在 NewMockApp 给的首页就等于「人在别的页面」，那条路测不到。
	if a.Router == nil || a.Router.Path() != routePlayer {
		a.Router = ui.NewRouter(routePlayer)
	}
	// 起播要一个窗口才肯把工作交出去（beginSession 从 a.Win 取）。测试里
	// runOnMain 已被替换，这个零值窗口只用来过 nil 判断，不会被调用任何方法。
	a.Win = &mygo.Window{}
	t.Cleanup(a.CloseMedia)
	return a
}

// waitFor 轮询一个条件，超时直接 fail。只用于跨 goroutine 的最终状态检查。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等不到期望状态")
}

// ---------------------------------------------------------------- 取流

// 有 PlaybackStreamer 时只跑按需那一条：旧 Stream 不该被调用。
func TestFetchStreamPrefersPlaybackStreamer(t *testing.T) {
	prov := &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "蓝光4M"}}
	if _, selected, err := fetchStream(context.Background(), prov, "123", "main", "蓝光4M"); err != nil {
		t.Fatal(err)
	} else if selected.Label != "蓝光4M" {
		t.Fatalf("selected 是 %q", selected.Label)
	}
	if prov.calls != 1 || prov.streamed != 0 {
		t.Fatalf("按需=%d 旧接口=%d，应当只走按需", prov.calls, prov.streamed)
	}
}

// 平台只有旧接口时退回整份菜单。
func TestFetchStreamFallsBackToStreamer(t *testing.T) {
	prov := &legacyStreamer{full: live.Stream{Live: true, Variants: []live.Variant{
		{Label: "原画", URL: "https://example.invalid/origin.flv"},
	}}}
	if _, selected, err := fetchStream(context.Background(), prov, "123", "main", "原画"); err != nil {
		t.Fatal(err)
	} else if selected.Label != "原画" {
		t.Fatalf("selected 是 %q", selected.Label)
	}
}

// ---------------------------------------------------------------- 选档

// 起播用平台确认的档位，并把菜单上的标签校正成实际在播的那一档。
func TestStartStreamUsesSelectedVariant(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	menu := []live.Variant{
		{ID: "origin", Label: "原画"},
		{ID: "bluray", Label: "蓝光4M"},
		{ID: "hd", Label: "高清"},
	}
	prov := &stubStreamer{
		full:     live.Stream{Live: true, Variants: menu},
		selected: live.Variant{ID: "bluray", Label: "蓝光4M", URL: "https://example.invalid/bluray.flv"},
	}
	useProvider(t, prov)

	a := seededApp(t)
	a.Quality = "原画" // 请求原画，但服务端只给了蓝光
	a.media.stream = live.Stream{Variants: menu}

	a.startStream(false)
	// 一次取流结果上屏，一次打开播放引擎。
	stubs.pump(t)
	stubs.pump(t)

	if a.Quality != "蓝光4M" {
		t.Fatalf("菜单标签没校正成实际档位，得到 %q", a.Quality)
	}
	if got := stubs.native.count(); got != 1 {
		t.Fatalf("播放引擎被打开 %d 次", got)
	}
}

// 平台给不了所点的档位时必须说出来：斗鱼会把「原画」降成「蓝光4M」，
// 静默换掉会让用户以为切档没生效。
func TestQualityDowngradeShowsNotice(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{
		full: live.Stream{Live: true, Variants: []live.Variant{
			{ID: "0", Label: "原画1080P60"},
			{ID: "4", Label: "蓝光4M", URL: "https://example.invalid/bluray.flv"},
		}},
		selected: live.Variant{ID: "4", Label: "蓝光4M", URL: "https://example.invalid/bluray.flv"},
	}
	useProvider(t, prov)
	a := seededApp(t)
	a.media.stream = live.Stream{Variants: prov.full.Variants}
	a.media.roomKey = "douyu|1"

	a.PickQuality("原画1080P60")
	stubs.pump(t)
	stubs.pump(t)

	if a.Quality != "蓝光4M" {
		t.Fatalf("菜单没有校正到实际档位: %q", a.Quality)
	}
	if !contains(a.media.notice, "原画1080P60") || !contains(a.media.notice, "蓝光4M") {
		t.Fatalf("降档没有提示: %q", a.media.notice)
	}
	if prov.gotQuality != "原画1080P60" {
		t.Fatalf("请求的档位 = %q", prov.gotQuality)
	}
}

// 编码后缀不算降档：抖音把「原画」写成「原画 · H.265」是同一档。
func TestSameQualityTreatsCodecSuffixAsSame(t *testing.T) {
	for _, tc := range []struct {
		requested, applied string
		same               bool
	}{
		{"原画", "原画", true},
		{"原画", "原画 · H.265", true},
		{"原画 · H.265", "原画", true},
		{"原画1080P60", "蓝光4M", false},
		{"蓝光4M", "超清", false},
	} {
		if got := sameQuality(tc.requested, tc.applied); got != tc.same {
			t.Fatalf("sameQuality(%q,%q)=%v", tc.requested, tc.applied, got)
		}
	}
}

// 提示按时间自己消失，不依赖用户下一次操作。
func TestNoticeExpires(t *testing.T) {
	m := playerMedia{}
	now := time.Now()
	m.notice = "已切到「蓝光4M」"
	m.noticeUntil = now.Add(time.Second)
	if !m.noticeActive(now) || m.notice == "" {
		t.Fatal("提示未显示")
	}
	if m.noticeActive(now.Add(2*time.Second)) || m.notice != "" {
		t.Fatal("提示过期后未清理")
	}
}

// 菜单里有这一档但地址还没解析时，pickQuality 应该按需去要这一档，
// 而不是拿回来一个空地址去起播。
func TestPickQualityFetchesOnDemand(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{
		full: live.Stream{Live: true, Variants: []live.Variant{
			{ID: "origin", Label: "原画"},
			{ID: "hd", Label: "高清"},
		}},
		selected: live.Variant{ID: "hd", Label: "高清", URL: "https://example.invalid/hd.flv"},
	}
	useProvider(t, prov)

	a := seededApp(t)
	a.media.stream = live.Stream{Variants: []live.Variant{
		{ID: "origin", Label: "原画"},
		{ID: "hd", Label: "高清"},
	}}

	a.PickQuality("高清")
	stubs.pump(t)
	stubs.pump(t)

	if prov.gotQuality != "高清" {
		t.Fatalf("按需取流带的是 %q，期望刚选中的档位", prov.gotQuality)
	}
	if got := stubs.native.count(); got != 1 {
		t.Fatalf("播放引擎被打开 %d 次", got)
	}
}

// 菜单里已经带地址时直接换上游，不再回平台要一次。
func TestPickQualityPlaysDirectlyWhenURLPresent(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{full: live.Stream{Live: true}}
	useProvider(t, prov)

	a := seededApp(t)
	a.media.stream = live.Stream{Variants: []live.Variant{
		{ID: "origin", Label: "原画", URL: "https://example.invalid/origin.flv"},
		{ID: "hd", Label: "高清", URL: "https://example.invalid/hd.flv"},
	}}

	a.PickQuality("高清")
	stubs.pump(t)

	if prov.calls != 0 {
		t.Fatalf("地址已经在菜单里，不该再取一次流（calls=%d）", prov.calls)
	}
	if got := stubs.native.count(); got != 1 {
		t.Fatalf("播放引擎被打开 %d 次", got)
	}
}

// 未确认的地址可以播放，但不能把用户请求的原画当成服务端确认的档位。
func TestUnconfirmedSelectedUsesNeutralLabel(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	menu := []live.Variant{{ID: "source", Label: "原画"}, {ID: "hd", Label: "高清"}}
	prov := &stubStreamer{full: live.Stream{Live: true, Variants: menu},
		selected: live.Variant{URL: "https://example.invalid/unconfirmed.flv"}}
	useProvider(t, prov)
	a := seededApp(t)
	a.Quality = "原画"
	a.startStream(false)
	stubs.pump(t)
	stubs.pump(t)
	if a.Quality != "默认" {
		t.Fatalf("未确认档位显示为 %q", a.Quality)
	}
	if got := a.qualityOptions(); len(got) != 2 || got[0] != "原画" || got[1] != "高清" {
		t.Fatalf("目录被破坏: %v", got)
	}
}

// ---------------------------------------------------------------- 弹幕

// 换清晰度不该打断聊天：弹幕代号独立于画面代号。
func TestQualitySwitchKeepsDanmaku(t *testing.T) {
	a := seededApp(t)
	stream := &fakeDanmaku{}
	a.media.danmaku = stream
	a.media.danmuSeq = 7
	mediaSeq := a.media.seq

	a.beginSession("换档")
	if a.media.seq == mediaSeq {
		t.Fatal("换档没有推进画面代号")
	}
	if a.media.danmuSeq != 7 {
		t.Fatalf("换档把弹幕代号也推进了：%d", a.media.danmuSeq)
	}
	if a.media.danmaku != stream || stream.closes() != 0 {
		t.Fatal("换档把弹幕连接断了")
	}

	// 旧代号的弹幕仍然要能进历史——以前这里按画面代号判，换档后永久丢弹幕。
	a.applyDanmu(7, live.Danmu{Nickname: "观众", Content: "还在吗"})
	if len(a.DanmuLog) != 1 {
		t.Fatalf("换档后弹幕被丢了，历史 %d 条", len(a.DanmuLog))
	}
}

// 过期才连上的弹幕连接必须自己关掉，别留一条没人收的 WebSocket。
func TestAttachDanmakuDropsStaleStream(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	a.media.danmuSeq = 3

	stale := &fakeDanmaku{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.attachDanmaku(ctx, nil, 2, stale)
	stubs.pump(t)
	waitFor(t, func() bool { return stale.closes() == 1 })
	if a.media.danmaku != nil {
		t.Fatal("过期连接被挂上了")
	}

	fresh := &fakeDanmaku{}
	a.attachDanmaku(ctx, nil, 3, fresh)
	stubs.pump(t)
	if a.media.danmaku != fresh {
		t.Fatal("当前代号的连接没挂上")
	}
	if fresh.closes() != 0 {
		t.Fatal("当前代号的连接被关掉了")
	}
}

// 换房间：旧弹幕关掉，代号推进。
func TestCloseMediaDropsDanmaku(t *testing.T) {
	a := seededApp(t)
	stream := &fakeDanmaku{}
	a.media.danmaku = stream
	seq := a.media.danmuSeq

	a.closeMedia()
	waitFor(t, func() bool { return stream.closes() == 1 })
	if a.media.danmuSeq == seq {
		t.Fatal("离开播放页没推进弹幕代号")
	}
	if a.media.roomKey != "" {
		t.Fatalf("roomKey 没清掉：%q", a.media.roomKey)
	}
}

// ---------------------------------------------------------------- 首帧

// 首帧预算只约束「还没出图」的状态：出过图、暂停、没打开播放器都不算。
func TestNeedFirstFrameTimeout(t *testing.T) {
	opened := time.Now()
	for _, tc := range []struct {
		name    string
		m       playerMedia
		playing bool
		at      time.Duration
		want    bool
	}{
		{"预算内", playerMedia{openedAt: opened}, true, firstFrameBudget / 2, false},
		{"到点", playerMedia{openedAt: opened}, true, firstFrameBudget, true},
		{"已出图", playerMedia{openedAt: opened, firstFrame: true}, true, time.Hour, false},
		{"暂停中", playerMedia{openedAt: opened}, false, time.Hour, false},
		{"还没打开", playerMedia{}, true, time.Hour, false},
	} {
		if got := tc.m.needFirstFrameTimeout(opened.Add(tc.at), tc.playing); got != tc.want {
			t.Fatalf("%s: needFirstFrameTimeout=%v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 首帧预算里要扣掉用户暂停的时间，否则网络抖动一次就过早判失败。
func TestStartupElapsedExcludesPause(t *testing.T) {
	opened := time.Now()
	m := playerMedia{openedAt: opened}
	// 播了 1 秒，然后暂停 10 秒，再继续。
	m.observeStartupPause(true, opened.Add(time.Second))
	m.observeStartupPause(false, opened.Add(time.Second))
	m.observeStartupPause(false, opened.Add(11*time.Second))
	m.observeStartupPause(true, opened.Add(11*time.Second))
	elapsed := m.startupElapsed(opened.Add(12 * time.Second))
	if elapsed > 3*time.Second {
		t.Fatalf("暂停时间没被扣掉，startupElapsed=%v", elapsed)
	}
}

// ---------------------------------------------------------------- 播放控制

// 播放键在正常状态下只翻 UI 意图，不碰媒体层。
func TestTogglePlaybackPauses(t *testing.T) {
	a := seededApp(t)
	a.Playing = true
	a.togglePlayback()
	if a.Playing {
		t.Fatal("播放键没有翻成暂停")
	}
}

// 没有可播地址时要给错误终态，不能让用户对着加载层干等。
func TestLiveWithoutVariantsReportsError(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{
		full:     live.Stream{Live: true},
		selected: live.Variant{},
	}
	useProvider(t, prov)
	a := seededApp(t)
	a.startStream(false)
	// 一次是房间详情上屏，一次是「没有档位」的终态。
	stubs.pump(t)
	stubs.pump(t)
	if a.media.loading {
		t.Fatal("没有可播地址仍停在加载态")
	}
	if a.media.err == "" {
		t.Fatal("没有可播地址却没报错")
	}
}

// contains 是 strings.Contains 的短名，读起来顺一点。
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ---------------------------------------------------------------- 换地址

// 平台声明了寿命才提前换；没声明时什么也不做。
func TestRefreshAtFor(t *testing.T) {
	if got := refreshAtFor(live.Variant{}); !got.IsZero() {
		t.Fatalf("没声明寿命却排了换地址：%v", got)
	}
	exp := time.Now().Add(300 * time.Second)
	got := refreshAtFor(live.Variant{Expires: exp})
	if want := exp.Add(-refreshLead); !got.Equal(want) {
		t.Fatalf("换地址时刻 %v，期望 %v", got, want)
	}
}

// 断流判定：没出图、暂停、时间轴还在走都不算；自己停了、或者十秒不动才算。
func TestDeadReason(t *testing.T) {
	now := time.Now()
	var m playerMedia
	if got := m.deadReason(false, false, 0, now); got != "" {
		t.Fatalf("还没出图就判死：%q", got)
	}
	if got := m.deadReason(true, false, 10*time.Second, now); got != "ended" {
		t.Fatalf("播放器自己停了，判定=%q", got)
	}
	m = playerMedia{}
	if got := m.deadReason(true, true, 10*time.Second, now); got != "" {
		t.Fatalf("刚起步就判死：%q", got)
	}
	// 时间轴在走就一直健康。
	m.stillPos, m.stillAt = 10*time.Second, now
	if got := m.deadReason(true, true, 11*time.Second, now.Add(time.Minute)); got != "" {
		t.Fatalf("时间轴在走却判死：%q", got)
	}
	// 停在同一个位置上超过预算才算卡住。
	m.stillPos, m.stillAt = 11*time.Second, now
	if got := m.deadReason(true, true, 11*time.Second, now.Add(streamStallTimeout-time.Second)); got != "" {
		t.Fatalf("预算内就判死：%q", got)
	}
	if got := m.deadReason(true, true, 11*time.Second, now.Add(streamStallTimeout+time.Second)); got != "stalled" {
		t.Fatalf("停住超过预算，判定=%q", got)
	}
}

// ---------------------------------------------------------------- 后台收听

// trackSpy 记全尺寸呈现被开关了几次、最后是什么状态。
type trackSpy struct {
	mu     sync.Mutex
	states []bool
}

func (s *trackSpy) SetPresenting(on bool) {
	s.mu.Lock()
	s.states = append(s.states, on)
	s.mu.Unlock()
}

func (s *trackSpy) last() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.states) == 0 {
		return false, false
	}
	return s.states[len(s.states)-1], true
}

func (s *trackSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.states)
}

// useTrack 把引擎取用换成 spy，并给 app 挂一个非 nil 的播放器。
//
// ui.VideoPlayer 造不出不碰 libmpv 的实例，这里只用到「非 nil」这一个性质：
// detachMedia / attachMedia 的判据是它，呈现的开关走 engineOf。
func useTrack(t *testing.T, a *App, spy *trackSpy) {
	t.Helper()
	old := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return spy }
	t.Cleanup(func() { engineOf = old })
	a.media.player = nil
}

// 离开播放页进后台收听：全尺寸呈现关掉、房间记号留着、引擎不关。
func TestDetachKeepsSound(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.detachMedia()

	if !a.media.background {
		t.Fatal("没有进入后台收听")
	}
	if on, ok := spy.last(); !ok || on {
		t.Fatalf("全尺寸呈现最后状态是 %v（有没有设过 %v），应当关掉", on, ok)
	}
	if a.media.roomKey == "" {
		t.Fatal("后台收听把房间记号清了：地址续期和断流重连都会失去目标")
	}

	// 幂等：再调一次不该重复设。
	if n := spy.count(); n != 1 {
		t.Fatalf("第二次 detach 又关了一次呈现（共 %d 次）", n)
	}
}

// 回到播放页：接上后台那一路，不重新取流、不开新引擎。
//
// 后台保留解码，只把渲染目标收到 160×90：mpv 实例、地址续期、音频输出和
// 参考帧链都活着。回来把全尺寸呈现打开，引擎立刻用当前帧重渲染——实测 4ms
// 出全尺寸画面，声音一秒没断。
//
// 整路重新起播的代价实测是 1.09–1.56 秒的新路首声，也就是那一下断音。
func TestAttachResumesSameEngine(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "原画"}})

	a.detachMedia()
	before := a.media.seq
	epoch := a.media.openedAt
	a.attachMedia()

	if a.media.background {
		t.Fatal("回到播放页之后还挂着后台收听标记")
	}
	if a.media.seq != before {
		t.Fatal("回来重新起播了：后台那一路的声音本来就没断")
	}
	if a.media.loading {
		t.Fatal("回来进了 loading：画面上只剩加载层，最后一帧被盖掉了")
	}
	if on, ok := spy.last(); !ok || !on {
		t.Fatalf("回来没有打开全尺寸呈现：%v（设过 %v）", on, ok)
	}
	if n := spy.count(); n != 2 {
		t.Fatalf("呈现设了 %d 次（关+开应当正好 2 次）", n)
	}
	if !a.media.openedAt.Equal(epoch) {
		t.Fatal("回来把打开时刻重置了：首帧预算会从头再算一遍")
	}
}

// 引擎真的不在时（理论上不该发生：detach 需要引擎），回来只能整路起播，
// 不能把人留在一张黑屏上。
func TestAttachWithoutEngineStartsStream(t *testing.T) {
	a := seededApp(t)
	old := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return nil }
	t.Cleanup(func() { engineOf = old })
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}, selected: live.Variant{Label: "原画"}})
	a.media.background = true
	before := a.media.seq

	a.attachMedia()

	if a.media.background {
		t.Fatal("没有引擎时回来还挂着后台收听标记")
	}
	if a.media.seq == before {
		t.Fatal("没有引擎时回来没有重新起播")
	}
}

// 没有引擎时不该进后台收听：那会留下一个「假装在听」的态。
func TestDetachWithoutEngineDoesNothing(t *testing.T) {
	a := seededApp(t)
	old := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return nil }
	t.Cleanup(func() { engineOf = old })

	a.detachMedia()

	if a.media.background {
		t.Fatal("没有引擎也进了后台收听")
	}
}

// 真退出要把后台收听一起收掉：引擎没了岛还显示在收听，比不显示更糟。
func TestCloseMediaClearsBackground(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.detachMedia()
	a.closeMedia()

	if a.media.background {
		t.Fatal("关掉媒体之后还挂着后台收听标记")
	}
	if a.media.player != nil {
		t.Fatal("关掉媒体之后播放器还在")
	}
}

// 后台收听时首帧预算不适用：画面本来就不该有，用画面判死会把一路好好出声的
// 流关掉。这里验判据本身——background 态下 trackFirstFrame 不落错误终态。
func TestBackgroundIgnoresFirstFrameBudget(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)
	a.Playing = true
	a.detachMedia()

	// 把打开时刻拨到很久以前：真按首帧预算算，这一下必然超时。
	a.media.openedAt = time.Now().Add(-time.Minute)

	a.trackFirstFrame()

	if a.media.err != "" {
		t.Fatalf("后台收听被首帧预算判成了失败：%s", a.media.err)
	}
	if a.media.background == false {
		t.Fatal("后台收听被首帧预算关掉了")
	}
}

// 转场期间 Router 会重建上一页：那时 PlayerPage 仍会跑 attachMedia，
// 而人已经在别的页面上了。不加路由判据，attach 和 Shell 的 detach 会每帧
// 互相翻一次——实测一秒十几条日志，呈现跟着来回切。
func TestAttachIgnoredOffPlayerPage(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)
	a.detachMedia()

	// 人已经不在播放页了。
	a.Router = ui.NewRouter(routeHome)
	a.attachMedia()

	if !a.media.background {
		t.Fatal("不在播放页时 attachMedia 把后台收听标记清了")
	}
	if on, _ := spy.last(); on {
		t.Fatal("不在播放页时 attachMedia 把全尺寸呈现打开了")
	}
}
