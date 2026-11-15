package ui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/ffmpeg"
	"alive/internal/live"
	"alive/internal/relay"
)

// ---------------------------------------------------------------- 假边界

// fakeRelay 是 relaySession 的假实现：只记次数与上游地址，不起任何进程。
type fakeRelay struct {
	mu             sync.Mutex
	starts         int
	stops          int
	upstream       string
	codecs         []string
	block          chan struct{}
	startErr       error
	proxySupported bool
	proxy          bool
	ctx            context.Context
}

func (f *fakeRelay) Start(ctx context.Context, upstream string, headers []string, codec string, opts relay.Options) (string, error) {
	f.mu.Lock()
	f.starts++
	f.upstream = upstream
	f.codecs = append(f.codecs, codec)
	f.ctx = ctx
	f.proxy = opts.HLSProxy && f.proxySupported
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if f.startErr != nil {
		return "", f.startErr
	}
	return "http://127.0.0.1:1/t/live.m3u8", nil
}

// Mode 对应 relay.Server.Mode()：只在 Start 成功之后才有值。
func (f *fakeRelay) Mode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.starts == 0 || f.startErr != nil {
		return ""
	}
	if f.proxy {
		return "hls-proxy"
	}
	return "remux"
}

func (f *fakeRelay) Stop() {
	f.mu.Lock()
	f.stops++
	f.mu.Unlock()
}

func (f *fakeRelay) counts() (starts, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func (f *fakeRelay) gotUpstream() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.upstream
}

// mediaStubs 把起播流程的外部边界换成假的，测试结束自动还原。
type mediaStubs struct {
	mu      sync.Mutex
	relay   *fakeRelay
	proxies []bool
	// queue 复刻 Window.Update 的异步语义：回调排进来，测试自己当「主线程」执行。
	// 这样流程里的写和测试里的读永远在同一个 goroutine 上。
	queue chan func()
}

func newMediaStubs(t *testing.T, startErr error) *mediaStubs {
	t.Helper()
	s := &mediaStubs{
		relay: &fakeRelay{startErr: startErr},
		queue: make(chan func(), 8),
	}
	oldNew, oldOpts := newRelaySession, relayOptions
	oldOpen, oldRun, oldProv := openVideo, runOnMain, providerFor

	newRelaySession = func() relaySession { return s.relay }
	relayOptions = func(enc relay.EncoderMode, proxy bool) relay.Options {
		s.mu.Lock()
		s.proxies = append(s.proxies, proxy)
		s.mu.Unlock()
		return oldOpts(enc, proxy)
	}
	openVideo = func(url string) (*ui.VideoPlayer, error) {
		// 真播放器在这个环境里建不出来；失败路径正是要验的那条。
		return nil, errors.New("mf 打不开")
	}
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		s.queue <- fn
		return true
	}

	t.Cleanup(func() {
		newRelaySession, relayOptions = oldNew, oldOpts
		openVideo, runOnMain, providerFor = oldOpen, oldRun, oldProv
	})
	return s
}

func (s *mediaStubs) proxyFlags() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.proxies...)
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
	t.Cleanup(a.CloseMedia)
	return a
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
		t.Fatalf("按需取流应只调 StreamForPlayback，得到 calls=%d streamed=%d", prov.calls, prov.streamed)
	}
	if prov.gotQuality != "蓝光4M" {
		t.Fatalf("传给平台的是 %q，期望菜单标签", prov.gotQuality)
	}
}

// 没有 PlaybackStreamer 时按旧接口取全量，并按标签挑档。
func TestFetchStreamFallsBackToStreamer(t *testing.T) {
	prov := &legacyStreamer{full: live.Stream{Live: true, Variants: []live.Variant{
		{ID: "a", Label: "原画"},
		{ID: "b", Label: "高清"},
	}}}
	_, selected, err := fetchStream(context.Background(), prov, "123", "", "高清")
	if err != nil {
		t.Fatal(err)
	}
	if selected.ID != "b" {
		t.Fatalf("回落路径选中 %q", selected.ID)
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
	// 一次取流结果上屏，一次打开播放器。
	stubs.pump(t)
	stubs.pump(t)

	if a.Quality != "蓝光4M" {
		t.Fatalf("菜单标签没校正成实际档位，得到 %q", a.Quality)
	}
	if got := stubs.relay.gotUpstream(); got != "https://example.invalid/bluray.flv" {
		t.Fatalf("起播用的是 %q", got)
	}
}

// 播放组件还在后台安装时，提示要指向组件而不是直播间；用户不需要自己去装它。
func TestRelayFailureExplainsComponentPreparation(t *testing.T) {
	stubs := newMediaStubs(t, fmt.Errorf("%w: 下载中", ffmpeg.ErrPreparing))
	useProvider(t, &stubStreamer{
		full:     live.Stream{Live: true, Variants: []live.Variant{{ID: "origin", Label: "原画", URL: "https://example.invalid/a.flv"}}},
		selected: live.Variant{ID: "origin", Label: "原画", URL: "https://example.invalid/a.flv"},
	})
	a := seededApp(t)
	a.startStream(false)
	stubs.pump(t)
	stubs.pump(t)
	if !strings.Contains(a.media.err, "正在准备播放组件") {
		t.Fatalf("组件准备中的提示 = %q", a.media.err)
	}
	if a.media.loading {
		t.Fatal("组件准备中仍停在加载态")
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
	if !strings.Contains(a.media.notice, "原画1080P60") || !strings.Contains(a.media.notice, "蓝光4M") {
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
	if got := stubs.relay.gotUpstream(); got != "https://example.invalid/hd.flv" {
		t.Fatalf("起播用的是 %q", got)
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
	if got := stubs.relay.gotUpstream(); got != "https://example.invalid/hd.flv" {
		t.Fatalf("起播用的是 %q", got)
	}
}

// ---------------------------------------------------------------- 生命周期

// 过期的起播结果不许挂到媒体层上，还要把自己那一路转封装关掉。
func TestStalePlaybackResultStopsRelay(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	sess := playbackSession{seq: a.media.seq - 1, ctx: context.Background(), id: "stale#1", start: time.Now()}

	a.startPlayback(sess, live.Variant{Label: "原画", URL: "https://example.invalid/x.flv"}, false)
	// 过期判定发生在主线程回调里，先把它跑掉。
	stubs.pump(t)

	// Stop 走 go：主线程不该为了杀 ffmpeg 卡住，所以这里等它落地。
	waitFor(t, func() bool { _, stops := stubs.relay.counts(); return stops > 0 })
	if a.media.relay != nil || a.media.player != nil {
		t.Fatal("过期结果被挂到媒体层上了")
	}
	if a.media.err != "" {
		t.Fatalf("过期结果不该改错误状态，得到 %q", a.media.err)
	}
}

// 这一轮被换掉时，还在等首片的转封装必须立刻停。
func TestCancelledSessionStopsStartingRelay(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	block := make(chan struct{})
	stubs.relay.block = block

	a := seededApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	sess := playbackSession{seq: a.media.seq, ctx: ctx, id: "cancel#1", start: time.Now()}

	done := make(chan struct{})
	go func() {
		a.startPlayback(sess, live.Variant{Label: "原画", URL: "https://example.invalid/x.flv"}, false)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("取消后起播没有返回")
	}
	close(block)
	if _, stops := stubs.relay.counts(); stops == 0 {
		t.Fatal("取消后没有关掉半成品转封装")
	}
	if a.media.loading {
		// 取消的这一轮本来就不该动主线程状态，这里是防御性断言
		t.Fatal("取消的起播把 loading 留成了 true")
	}
}

// 代理快路径失败后只回退一次：第二次还失败就报错，不再重试。
func TestProxyFallbackHappensOnce(t *testing.T) {
	for _, codec := range []string{"avc", "hevc"} {
		t.Run(codec, func(t *testing.T) {
			stubs := newMediaStubs(t, nil)
			stubs.relay.proxySupported = true
			a := seededApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			a.media.proxyAttempt = true
			a.media.ctx = ctx
			a.media.start = time.Now()
			a.media.loading = true
			sess := playbackSession{seq: a.media.seq, ctx: ctx, id: "proxy#1", start: time.Now()}
			upstream := live.Variant{Label: "原画", URL: "https://example.invalid/x.m3u8", Codec: codec}

			a.startPlayback(sess, upstream, true)
			// 第一次打开失败 → 回退一次（第二次打开也会失败）
			stubs.pump(t)
			stubs.pump(t)

			if got := stubs.proxyFlags(); len(got) != 2 || !got[0] || got[1] {
				t.Fatalf("代理意图应为 [true false]，得到 %v", got)
			}
			if starts, _ := stubs.relay.counts(); starts != 2 {
				t.Fatalf("只该起两路（原路径 + 一次回退），实际 %d", starts)
			}
			if !a.media.proxyFellBack {
				t.Fatal("回退标记没落下，后面还可能再回退")
			}

			stubs.relay.mu.Lock()
			defer stubs.relay.mu.Unlock()
			if len(stubs.relay.codecs) != 2 || stubs.relay.codecs[0] != codec || stubs.relay.codecs[1] != codec {
				t.Fatalf("回退必须保留源编码供转码判断，得到 %v", stubs.relay.codecs)
			}

		})
	}
}

// 首帧迟迟不来 / 播放器报错时该回退；非实验路径、已回退过、已出图都不该。
func TestNeedProxyFallback(t *testing.T) {
	start := time.Now()
	cases := []struct {
		name  string
		proxy bool
		fell  bool
		err   error
		at    time.Duration
		want  bool
	}{
		{"未开实验", false, false, nil, time.Minute, false},
		{"已回退过", true, true, nil, time.Minute, false},
		{"还没到点", true, false, nil, time.Second, false},
		{"到点", true, false, nil, proxyFirstFrameWait, true},
		{"播放器报错", true, false, errors.New("decode"), 0, true},
	}
	for _, tc := range cases {
		m := playerMedia{proxyAttempt: tc.proxy, proxyFellBack: tc.fell, openedAt: start, start: start.Add(-time.Minute)}
		if got := m.needProxyFallback(tc.err, start.Add(tc.at), true); got != tc.want {
			t.Errorf("%s：得到 %v，期望 %v", tc.name, got, tc.want)
		}
	}
	// 没有起播轮次时一律不回退
	if (&playerMedia{proxyAttempt: true}).needProxyFallback(nil, start.Add(time.Hour), true) {
		t.Error("没有起播起点时不该回退")
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
	if got := stubs.relay.gotUpstream(); got != prov.selected.URL {
		t.Fatal("没有使用 selected 的实际地址")
	}
}

// 开关开启不代表实际使用代理：relay 自己回退 remux 后，MF 打开失败不能重起一次。
func TestActualRemuxDoesNotFallback(t *testing.T) {
	t.Setenv(envHLSProxy, "1")
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	sess := a.beginSession("测试")
	a.startPlayback(sess, live.Variant{URL: "https://example.invalid/x.flv"}, sess.proxy)
	stubs.pump(t)
	if a.media.proxyAttempt || a.media.proxyFellBack {
		t.Fatal("实际 remux 被当成代理")
	}
	if starts, _ := stubs.relay.counts(); starts != 1 {
		t.Fatalf("意外启动 %d 次", starts)
	}
	if a.media.err == "" {
		t.Fatal("未报告真正的打开失败")
	}
}

// fetch 花了一分钟也不消耗打开后的八秒，用户暂停期间同样不消耗预算。
func TestProxyTimeoutUsesOpenTimeAndExcludesPause(t *testing.T) {
	opened := time.Now()
	m := playerMedia{proxyAttempt: true, start: opened.Add(-time.Minute), openedAt: opened}
	if m.needProxyFallback(nil, opened.Add(time.Second), true) {
		t.Fatal("把取流时间算进首帧预算")
	}
	m.observeStartupPause(false, opened.Add(time.Second))
	if m.needProxyFallback(errors.New("decode"), opened.Add(time.Hour), false) {
		t.Fatal("暂停时不应回退")
	}
	m.observeStartupPause(true, opened.Add(time.Minute))
	if m.needProxyFallback(nil, opened.Add(time.Minute+6*time.Second), true) {
		t.Fatal("暂停时间消耗了预算")
	}
	if !m.needProxyFallback(nil, opened.Add(time.Minute+7*time.Second), true) {
		t.Fatal("有效等待八秒未回退")
	}
	m.firstFrame = true
	if m.needProxyFallback(nil, opened.Add(time.Hour), true) {
		t.Fatal("首帧后不能再次启动超时回退")
	}
}

// 模拟窗口已不再执行 Update：关闭入口仍要取消 context 并收掉待交付 relay。
func TestCloseMediaReclaimsUndeliveredRelay(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	sess := a.beginSession("测试")
	a.startPlayback(sess, live.Variant{URL: "https://example.invalid/x.flv"}, false)
	if len(stubs.queue) != 1 {
		t.Fatal("没有排入打开回调")
	}
	a.CloseMedia()
	if sess.ctx.Err() == nil {
		t.Fatal("关闭入口没有立即取消会话")
	}
	waitFor(t, func() bool { _, stops := stubs.relay.counts(); return stops == 1 })
	// 即使延迟回调之后执行，也只能拒绝，且不会重复关闭资源。
	stubs.pump(t)
	if a.media.player != nil || a.media.relay != nil {
		t.Fatal("迟到回调重新挂载了资源")
	}
}

// 弹幕挂载回调未执行时，房间 context 同样负责兜底关闭。
func TestCanceledRoomReclaimsUndeliveredDanmaku(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.media.roomCtx, a.media.roomCancel = ctx, cancel
	stream := &fakeDanmaku{}
	a.attachDanmaku(ctx, nil, a.media.danmuSeq, stream)
	a.CloseMedia()
	if ctx.Err() == nil {
		t.Fatal("房间 context 没取消")
	}
	waitFor(t, func() bool { return stream.closes() == 1 })
	stubs.pump(t)
	if a.media.danmaku != nil {
		t.Fatal("迟到回调重新挂载了弹幕")
	}
}

// 窗口指针在启动时捕获；后台完成时不能重新读取 App.Win。
func TestPlaybackUsesCapturedWindow(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	captured, replacement := &mygo.Window{}, &mygo.Window{}
	a.Win = captured
	sess := a.beginSession("测试")
	a.Win = replacement
	var got *mygo.Window
	var queued func()
	runOnMain = func(win *mygo.Window, fn func()) bool { got, queued = win, fn; return true }
	a.startPlayback(sess, live.Variant{URL: "https://example.invalid/x.flv"}, false)
	if got != captured {
		t.Fatal("使用了后台完成时的 App.Win")
	}
	a.CloseMedia()
	queued()
	a.Win = nil
}

// 本地单帧解码器只用于验证接纳后的寿命，不打开 MF 或访问网络。
type sessionFrameDecoder struct{ read bool }

func (d *sessionFrameDecoder) Next() (ui.VideoFrame, error) {
	if d.read {
		return ui.VideoFrame{}, io.EOF
	}
	d.read = true
	return ui.VideoFrame{Image: image.NewRGBA(image.Rect(0, 0, 16, 9)), Duration: time.Second}, nil
}
func (d *sessionFrameDecoder) Rewind() error { d.read = false; return nil }

func TestAcceptedRelayUsesSessionLifetime(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	openVideo = func(string) (*ui.VideoPlayer, error) { return ui.NewVideoPlayer(&sessionFrameDecoder{}) }
	a := seededApp(t)
	sess := a.beginSession("测试")
	a.startPlayback(sess, live.Variant{URL: "https://example.invalid/x.flv"}, false)
	stubs.pump(t)
	stubs.relay.mu.Lock()
	relayCtx := stubs.relay.ctx
	stubs.relay.mu.Unlock()
	if relayCtx != sess.ctx {
		t.Fatal("relay 没使用起播会话 context")
	}
	if _, ok := relayCtx.Deadline(); ok {
		t.Fatal("播放寿命不该带启动 deadline")
	}
	if relayCtx.Err() != nil || a.media.player == nil {
		t.Fatal("起播返回就取消了播放寿命")
	}
	a.beginSession("换档")
	if relayCtx.Err() == nil {
		t.Fatal("换档未取消旧 relay 的 context")
	}
	waitFor(t, func() bool { _, stops := stubs.relay.counts(); return stops == 1 })
}

// 即使实际清理很慢，CloseMedia 也必须立即取消 context 并返回主线程。
func TestCloseMediaDoesNotWaitForSlowCleanup(t *testing.T) {
	a := seededApp(t)
	sess := a.beginSession("测试")
	release, entered := make(chan struct{}), make(chan struct{})
	defer close(release)
	a.media.stopRelay = cleanupOnCancel(sess.ctx, func() { close(entered); <-release })
	done := make(chan struct{})
	go func() { a.CloseMedia(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("关闭入口等待慢资源")
	}
	if sess.ctx.Err() == nil {
		t.Fatal("关闭返回时 context 未取消")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("未执行清理")
	}
}

type pendingDanmakuer struct {
	*stubStreamer
	started chan context.Context
	release chan struct{}
	stream  *fakeDanmaku
}

func (p *pendingDanmakuer) Danmaku(ctx context.Context, _ string, _ func(live.Danmu)) (live.DanmakuStream, error) {
	p.started <- ctx
	<-p.release
	return p.stream, nil
}

// 验证正在建立的连接也属于房间：切档不会取消它，退出后即使迟到也必须关闭。
func TestPendingDanmakuSurvivesQualityButNotExit(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	a := seededApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	a.media.roomCtx, a.media.roomCancel = ctx, cancel
	p := &pendingDanmakuer{stubStreamer: &stubStreamer{}, started: make(chan context.Context, 1),
		release: make(chan struct{}), stream: &fakeDanmaku{}}
	seq, win := a.media.danmuSeq, &mygo.Window{}
	done := make(chan struct{})
	go func() { a.startDanmaku(ctx, seq, p, "123", win); close(done) }()
	select {
	case got := <-p.started:
		if got != ctx {
			t.Fatal("弹幕没有用房间 context")
		}
	case <-time.After(time.Second):
		t.Fatal("弹幕未开始连接")
	}
	a.beginSession("换档")
	if ctx.Err() != nil || a.media.danmuSeq != seq {
		t.Fatal("切档取消了正在建立的弹幕")
	}
	a.CloseMedia()
	if ctx.Err() == nil {
		t.Fatal("退出未取消正在建立的弹幕")
	}
	close(p.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("弹幕建立未结束")
	}
	waitFor(t, func() bool { return p.stream.closes() == 1 })
	stubs.pump(t)
	if a.media.danmaku != nil {
		t.Fatal("退出后迟到连接仍被接纳")
	}
}

// 声明参数与真正解码尺寸分别记录，日志不能把声明规格冒充测量结果。
func TestFirstFrameLogsDeclaredAndMeasuredSpecs(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	sess := a.beginSession("测试")
	p, err := ui.NewVideoPlayer(&sessionFrameDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	srv := &fakeRelay{starts: 1}
	upstream := live.Variant{URL: "https://example.invalid/stream?token=secret", Headers: []string{"Authorization: secret"},
		Codec: "avc", Width: 1920, Height: 1080, Bitrate: 6000000, FrameRate: 60}
	a.adoptPlayback(sess, srv, p, upstream, func() {})
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)
	a.trackFirstFrame()
	a.trackFirstFrame()
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatal("首帧日志应只输出一次:", err)
	}
	for key, want := range map[string]any{
		"stage": "首帧可用", "mode": "remux", "upstream_codec": "avc",
		"declared_width": float64(1920), "declared_height": float64(1080),
		"declared_bitrate": float64(6000000), "declared_fps": float64(60),
		"decoded_width": float64(16), "decoded_height": float64(9),
	} {
		if entry[key] != want {
			t.Errorf("%s = %v，期望 %v", key, entry[key], want)
		}
	}
	for _, sensitive := range []string{"example.invalid", "token", "secret", "Authorization"} {
		if strings.Contains(buf.String(), sensitive) {
			t.Fatalf("日志泄漏 %s", sensitive)
		}
	}
}

// 首帧后的错误仍保留 Ready，复现不能先按 firstFrame 或 Ready 短路的情况。
type failingPlaybackDecoder struct{ read bool }

func (d *failingPlaybackDecoder) Next() (ui.VideoFrame, error) {
	if d.read {
		return ui.VideoFrame{}, errors.New("播放中解码失败")
	}
	d.read = true
	return ui.VideoFrame{Image: image.NewRGBA(image.Rect(0, 0, 16, 9)), Duration: time.Second}, nil
}
func (d *failingPlaybackDecoder) Rewind() error { d.read = false; return nil }

func TestProxyPlaybackErrorFallsBackAndRemembersRoom(t *testing.T) {
	t.Setenv(envHLSProxy, "1")
	stubs := newMediaStubs(t, nil)
	openVideo = func(string) (*ui.VideoPlayer, error) { return ui.NewVideoPlayer(&sessionFrameDecoder{}) }
	a := seededApp(t)
	a.Playing = true
	sess := a.beginSession("测试")
	p, err := ui.NewVideoPlayer(&failingPlaybackDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	upstream := live.Variant{Label: "原画", URL: "https://example.invalid/live.m3u8"}
	a.media.proxyAttempt = true
	a.adoptPlayback(sess, &fakeRelay{starts: 1, proxy: true}, p, upstream, func() {})
	a.trackFirstFrame()
	if !a.media.firstFrame {
		t.Fatal("没有记录首次出图")
	}
	p.Seek(2 * time.Second)
	if p.Err() == nil || !p.Ready() {
		t.Fatal("没有复现已出图后报错的播放器")
	}
	a.syncPlayer()
	if p.Err() == nil {
		t.Fatal("同步播放状态清掉了应由回退处理的错误")
	}
	a.Playing = false
	a.trackFirstFrame()
	if a.media.proxyFellBack {
		t.Fatal("暂停时意外重启播放")
	}
	a.Playing = true
	a.trackFirstFrame()
	if !a.media.proxyFellBack || !a.media.proxyDisabled || a.media.player != nil {
		t.Fatal("播放错误没有关闭旧播放器并记住房间回退")
	}
	stubs.pump(t)
	a.trackFirstFrame()
	if a.media.player == nil || !a.media.firstFrame || a.media.proxyAttempt {
		t.Fatal("回退后未接上普通转封装")
	}
	a.media.stream = live.Stream{Variants: []live.Variant{{Label: "高清", URL: "https://example.invalid/hd.m3u8"}}}
	a.PickQuality("高清")
	stubs.pump(t)
	if got := stubs.proxyFlags(); len(got) != 2 || got[0] || got[1] {
		t.Fatalf("回退和随后切档都应跳过代理，得到 %v", got)
	}
	if a.beginSession("换线").proxy || a.session().proxy {
		t.Fatal("同一房间的新轮次又启用了代理")
	}
	a.CloseMedia()
	if !a.beginSession("新房间").proxy {
		t.Fatal("离开房间后没有恢复代理尝试")
	}
}

func TestReadyProxyFrameWinsOverStartupTimeout(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	a.Playing = true
	sess := a.beginSession("测试")
	p, err := ui.NewVideoPlayer(&sessionFrameDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	a.media.proxyAttempt = true
	a.adoptPlayback(sess, &fakeRelay{starts: 1, proxy: true}, p,
		live.Variant{URL: "https://example.invalid/live.m3u8"}, func() {})
	a.media.openedAt = time.Now().Add(-2 * proxyFirstFrameWait)
	a.trackFirstFrame()
	if !a.media.firstFrame || a.media.proxyFellBack || a.media.proxyDisabled {
		t.Fatal("已经有首帧却因超时而回退")
	}
}

// 平台不决定代理资格；同房间失败后换档仍不能再次尝试代理。
func TestPlaybackProxyPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, platform, override string
		want                     bool
	}{
		{"bilibili-default", "bilibili", "", true},
		{"douyu-default", "douyu", "", true},
		{"huya-default", "huya", "", true},
		{"douyin-default", "douyin", "", true},
		{"unknown-default", "", "", true},
		{"explicit-off", "bilibili", "0", false},
		{"douyin-explicit-off", "douyin", "0", false},
		{"diagnostic-on", "douyu", "1", true},
		{"invalid-override", "bilibili", "invalid", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envHLSProxy, tc.override)
			a := &App{Platform: "douyu", Player: Streamer{Platform: tc.platform}}
			defer a.media.reset()
			if got := a.beginSession("测试").proxy; got != tc.want {
				t.Fatalf("begin proxy = %v, want %v", got, tc.want)
			}
			if got := a.session().proxy; got != tc.want {
				t.Fatalf("session proxy = %v, want %v", got, tc.want)
			}
			a.media.proxyDisabled = true
			if a.beginSession("换档").proxy || a.session().proxy {
				t.Fatal("当前房间回退后不应再次尝试代理")
			}
		})
	}
}

// ---------------------------------------------------------------- 首帧预算与终态

// 所有路径共用一个首帧上限：未到点、暂停中、已出图、没有起播起点都不算超时。
func TestNeedFirstFrameTimeout(t *testing.T) {
	opened := time.Now()
	m := playerMedia{openedAt: opened}
	cases := []struct {
		name  string
		at    time.Duration
		play  bool
		first bool
		want  bool
	}{
		{"未到点", time.Second, true, false, false},
		{"到点", firstFrameBudget, true, false, true},
		{"暂停中", time.Hour, false, false, false},
		{"已出图", time.Hour, true, true, false},
	}
	for _, tc := range cases {
		m.firstFrame = tc.first
		if got := m.needFirstFrameTimeout(opened.Add(tc.at), tc.play); got != tc.want {
			t.Errorf("%s：得到 %v，期望 %v", tc.name, got, tc.want)
		}
	}
	if (&playerMedia{}).needFirstFrameTimeout(opened.Add(time.Hour), true) {
		t.Error("没有起播起点时不该超时")
	}
}

// 暂停时间不进预算：暂停中点一帧、或恢复播放，都不该被这段时间提前判死。
func TestStartupElapsedExcludesPause(t *testing.T) {
	opened := time.Now()
	m := playerMedia{openedAt: opened}
	m.observeStartupPause(false, opened.Add(time.Second))
	if got := m.startupElapsed(opened.Add(time.Hour)); got != time.Second {
		t.Fatalf("暂停期间计入预算，得到 %v", got)
	}
	m.observeStartupPause(true, opened.Add(time.Minute))
	// 1s（暂停前）+ 1m（恢复后）：中间那 59 秒暂停不计入。
	if got := m.startupElapsed(opened.Add(2 * time.Minute)); got != time.Minute+time.Second {
		t.Fatalf("恢复播放后预算应为 1m1s，得到 %v", got)
	}
}

// Ready 优先于首帧预算：解码器已经交了帧，就不能再按超时把这一路收掉。
func TestReadyFrameBeatsFirstFrameBudget(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	sess := a.beginSession("测试")
	p, err := ui.NewVideoPlayer(&sessionFrameDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	upstream := live.Variant{Label: "原画", URL: "https://example.invalid/x.flv"}
	a.adoptPlayback(sess, &fakeRelay{starts: 1}, p, upstream, func() {})
	a.media.openedAt = time.Now().Add(-firstFrameBudget - time.Second)
	a.trackFirstFrame()
	if !a.media.firstFrame || a.media.err != "" || a.media.player == nil {
		t.Fatalf("已就绪的首帧被判超时：firstFrame=%v err=%q", a.media.firstFrame, a.media.err)
	}
}

// 非代理路径的明确错误也要落到终态：收掉播放器和转封装，只留可重试的覆盖层。
func TestRemuxPlaybackErrorStopsMedia(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	a.Playing = true
	sess := a.beginSession("测试")
	p, err := ui.NewVideoPlayer(&failingPlaybackDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	closed := 0
	upstream := live.Variant{Label: "原画", URL: "https://example.invalid/x.flv"}
	a.adoptPlayback(sess, &fakeRelay{starts: 1}, p, upstream, func() {
		mu.Lock()
		closed++
		mu.Unlock()
	})
	a.trackFirstFrame()
	if !a.media.firstFrame {
		t.Fatal("没有记录首次出图")
	}
	p.Seek(2 * time.Second)
	if p.Err() == nil {
		t.Fatal("没有复现普通路径的播放错误")
	}
	a.trackFirstFrame()
	if a.media.player != nil || a.media.relay != nil {
		t.Fatal("错误后没有收掉媒体资源")
	}
	if a.media.err == "" || a.media.loading {
		t.Fatalf("错误没有落到可重试终态：err=%q loading=%v", a.media.err, a.media.loading)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return closed == 1
	})
	if a.media.proxyAttempt {
		t.Fatal("普通路径被误标成代理")
	}
	if !a.media.firstFrame {
		t.Fatal("已出图标记被终态清理重置，重试日志会退化成起播")
	}
}

// 回退只做一次：回退后的普通转封装同样受首帧预算约束，但不会再起第三路。
func TestFirstFrameTimeoutAfterFallbackDoesNotLoop(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	a.media.proxyFellBack = true
	a.media.proxyAttempt = false
	a.media.firstFrame = false
	a.media.openedAt = time.Now().Add(-firstFrameBudget - time.Second)
	if !a.media.needFirstFrameTimeout(time.Now(), true) {
		t.Fatal("回退后的普通路径没有首帧预算")
	}
	if a.fallbackToRemux(live.Variant{Label: "原画", URL: "https://example.invalid/x.flv"}) {
		t.Fatal("已回退过还能再回退一次")
	}
}

// 开播了但平台没给档位：必须有明确错误，不能停在封面上一句话都没有。
func TestLiveWithoutVariantsReportsError(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	useProvider(t, &stubStreamer{full: live.Stream{Live: true}})
	a := seededApp(t)
	a.startStream(false)
	stubs.pump(t) // 取流结果上屏
	stubs.pump(t) // 无档位分支
	if a.media.loading {
		t.Fatal("loading 没放下")
	}
	if a.media.err != "平台没有返回可播放的清晰度" {
		t.Fatalf("错误是 %q", a.media.err)
	}
	if got := a.qualityOptions(); got != nil {
		t.Fatalf("没有档位时不该有下拉：%v", got)
	}
}

// 错误态点播放键等于重试：不能让图标翻了、画面不动。
func TestTogglePlaybackRetriesOnError(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{
		full:     live.Stream{Live: true, Variants: []live.Variant{{ID: "origin", Label: "原画"}}},
		selected: live.Variant{ID: "origin", Label: "原画", URL: "https://example.invalid/x.flv"},
	}
	useProvider(t, prov)
	a := seededApp(t)
	a.Playing = false
	a.media.err = "播放失败，请重试或切换线路"

	a.togglePlayback()
	if !a.Playing {
		t.Fatal("错误态点播放没有回到在播")
	}
	if !a.media.loading {
		t.Fatal("错误态点播放没有开新一轮")
	}
	waitFor(t, func() bool { starts, _ := stubs.relay.counts(); return starts == 1 })
}

// 正常状态仍然是翻 UI 意图，不会重开流。
func TestTogglePlaybackPauses(t *testing.T) {
	newMediaStubs(t, nil)
	a := seededApp(t)
	a.Playing = true
	a.togglePlayback()
	if a.Playing {
		t.Fatal("正常状态没有暂停")
	}
	if a.media.loading {
		t.Fatal("暂停不该重开流")
	}
}

// 暂停时的播放错误既不回退也不清理：用户主动停着，恢复播放那一帧才处理。
// 代理和普通路径共用这一套语义，避免暂停时突然重开一路流。
func TestPausedPlaybackErrorDoesNotTouchMedia(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		name := "remux"
		if proxy {
			name = "proxy"
		}
		t.Run(name, func(t *testing.T) {
			stubs := newMediaStubs(t, nil)
			stubs.relay.proxySupported = proxy
			a := seededApp(t)
			a.Playing = true
			sess := a.beginSession("测试")
			p, err := ui.NewVideoPlayer(&failingPlaybackDecoder{})
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			closed := 0
			a.media.proxyAttempt = proxy
			a.adoptPlayback(sess, &fakeRelay{starts: 1, proxy: proxy}, p,
				live.Variant{Label: "原画", URL: "https://example.invalid/live.m3u8"}, func() {
					mu.Lock()
					closed++
					mu.Unlock()
				})
			a.trackFirstFrame()
			p.Seek(2 * time.Second)
			if p.Err() == nil {
				t.Fatal("没有复现播放错误")
			}

			// 暂停中报错：资源原封不动，只等下一帧。
			a.Playing = false
			a.trackFirstFrame()
			if a.media.player != p || a.media.relay == nil {
				t.Fatal("暂停时的错误把媒体资源收掉了")
			}
			if a.media.err != "" || a.media.loading || a.media.proxyFellBack {
				t.Fatalf("暂停时动了终态：err=%q loading=%v fellBack=%v",
					a.media.err, a.media.loading, a.media.proxyFellBack)
			}
			if starts, _ := stubs.relay.counts(); starts != 0 {
				t.Fatalf("暂停时重启了转封装，starts=%d", starts)
			}
			mu.Lock()
			got := closed
			mu.Unlock()
			if got != 0 {
				t.Fatal("暂停时关闭了媒体资源")
			}

			// 恢复播放：代理路径回退一次，普通路径落到可重试终态。
			a.Playing = true
			a.trackFirstFrame()
			if a.media.player != nil {
				t.Fatal("恢复播放后没有收掉报错的播放器")
			}
			if proxy {
				if !a.media.proxyFellBack || !a.media.proxyDisabled {
					t.Fatal("代理路径恢复播放后没有回退")
				}
				// 回退是一次后台起播：等它把接纳结果投递回主线程，再让测试结束。
				// t.Cleanup 会还原 newRelaySession / relayOptions / runOnMain，
				// 后台 goroutine 还没走完就还原是 data race，不是可以忽略的抖动。
				stubs.pump(t)
			} else if a.media.err == "" {
				t.Fatal("普通路径恢复播放后没有落到可重试终态")
			}
		})
	}
}
