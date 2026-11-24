package ui

import (
	"context"
	"image"

	"alive/internal/live"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/playback"
	"alive/internal/watch"
)

// 真实引擎、真实像素：同一间房在墙格和播放页之间来回，画面必须一直在。
//
// 这是这个包唯一的「真播放」用例：libmpv + lavfi 的 life 源（画面自己一直
// 在变，所以「在动」和「冻住」能区分）。没有 ALIVE_MPV_LIB 就跳过。
//
// 断言全部落在像素上，不碰任何内部标志——我先后用假引擎写过三条断言
// 路由器标志/源码字符串的测试，全绿，真机上仍是黑屏。
// realWall 是一面真实的墙：真引擎、真像素、一个格子。
//
// 真实播放用例的公共底座：libmpv + lavfi 的 life 源（画面自己一直在变，
// 所以「在动」和「冻住」能区分）。断言落在画面和声音上，不碰内部标志——
// 我先后用假引擎写过三条断言路由器标志/源码字符串的测试，全绿，真机上
// 仍是黑屏。
type realWall struct {
	t    *testing.T
	app  *App
	tt   *ui.Tester
	room Streamer
	key  string
}

func openRealWall(t *testing.T) *realWall {
	t.Helper()
	if os.Getenv("ALIVE_MPV_LIB") == "" {
		t.Skip("没有 ALIVE_MPV_LIB：跳过真实播放用例")
	}
	const url = "av://lavfi:life=size=320x180:rate=30:mold=6:ratio=0.12" +
		":death_color=#101828:life_color=#ff9020"
	e, err := playback.Open(context.Background(), url, playback.Options{
		Silent: true, SkipAudioGate: true, NoBackBuffer: true,
		Width: 320, Height: 180, FrameRate: 30, MaxWidth: 320, MaxHeight: 180,
	})
	if err != nil {
		t.Fatalf("打不开 lavfi：%v", err)
	}
	t.Cleanup(func() { _ = e.Close() })

	room := Streamer{Platform: "bilibili", RoomID: "1", Name: "真源", Live: true}
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.WatchRooms = []Streamer{room}
	app.Router = ui.NewRouter(routeFollows)
	app.FollowsSection = followsSectionWatch

	useProvider(t, &staticProvider{live: true})
	oldOpen := openNative
	openNative = func(context.Context, string, playback.Options) (*playback.Engine, error) {
		return e, nil
	}
	t.Cleanup(func() { openNative = oldOpen })
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = oldRun })
	oldFull := windowFullScreen
	windowFullScreen = func(*mygo.Window) bool { return false }
	t.Cleanup(func() { windowFullScreen = oldFull })
	// 真实窗口的凭据：watchOpenSession / startNativePlayback 只看它非 nil。
	// 上面的 runOnMain 和 windowFullScreen 都被替换了，不会真去问这扇窗。
	app.Win = &mygo.Window{}
	t.Cleanup(func() { app.Win = nil })

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	// a.Win 是 nil（零值 Window 的方法会卡在 onMain），所以 Shell 里的
	// watchShow 不建会话；这里带上真实矩形直接叫一次。
	app.watchShow(map[string]watch.Rect{watchKey(room): {X: 14, Y: 140, W: 600, H: 340}})
	tt.Frame()
	return &realWall{t: t, app: app, tt: tt, room: room, key: watchKey(room)}
}

// pump 推 n 帧。MyGo 的动画时钟走 tt.Advance，应用自己的截止时刻（过渡、
// 防抖）走墙钟，所以每帧还要真的睡一下。
func (w *realWall) pump(n int) {
	for range n {
		w.tt.Advance(16 * time.Millisecond)
		w.tt.Frame()
		time.Sleep(8 * time.Millisecond)
	}
}

// luma 取一块区域的平均亮度（0–255）。返回 -1 表示区域为空。
func (w *realWall) luma(r ui.Rect) float64 {
	return regionLuma(w.tt.Image(), r)
}

func TestRealRoomSurvivesWallPlayerRoundTrip(t *testing.T) {
	w := openRealWall(t)
	app, tt, room, luma := w.app, w.tt, w.room, w.luma
	pump := w.pump

	// 1. 墙上这一格必须真的在出画
	pump(30)
	tile, ok := tt.Find(watchTileLabel(room))
	if !ok {
		t.Fatalf("找不到格子：%v", tt.Texts())
	}
	if l := luma(tile); l < 5 {
		t.Fatalf("起点：墙格是黑的（亮度 %.1f）", l)
	}

	// 2. 进播放页：媒体层画的必须是墙上的那个引擎，画面不能空
	sess := app.watch.sessions[watchKey(room)]
	if sess == nil || sess.player == nil {
		t.Fatal("墙上没有会话")
	}
	engineBefore := sess.player
	app.watchActivate(room)
	pump(10)
	if app.media.eng() == nil {
		t.Fatal("进播放页后媒体层没有引擎：这就是「盒子里不是同一个直播间」")
	}
	if app.media.eng() != engineBefore {
		t.Fatal("进播放页换了播放器：等于重开了这一路")
	}
	if l := luma(ui.Rect{X: 40, Y: 100, W: 1200, H: 700}); l < 5 {
		t.Fatalf("播放页舞台是黑的（亮度 %.1f）", l)
	}

	// 3. 离开播放页进后台：还是同一个引擎，墙格仍然有画面
	app.backgroundEnter()
	pump(10)
	if app.watch.sessions[watchKey(room)] == nil {
		t.Fatal("退回墙上后会话不在册")
	}
	if l := luma(tile); l < 5 {
		t.Fatalf("回到墙后格子是黑的（亮度 %.1f）", l)
	}

	// 4. 退出播放页：过渡期间不能出现黑帧。
	//
	// 引擎在过渡期间留着，旧页面在淡出时仍然画着这一路；飞进来的墙格
	// 画的是同一个 session、同一个播放器，两边天然一致。
	// 三个退出入口（岛上的 X、Esc、左上角）都走这个函数，这里直接调它。
	app.closeFocus()
	for i := 0; i < 8; i++ {
		tt.Frame()
		// 采样点远离墙格落点：旧页面那一侧的舞台有没有画面，只有格子盖不到
		// 的地方才看得出来。
		l := luma(ui.Rect{X: 900, Y: 150, W: 300, H: 200})
		if l >= 0 && l < 5 {
			t.Fatalf("退出过渡第 %d 帧舞台是黑的（亮度 %.1f）：引擎放手太早", i, l)
		}
		tt.Advance(16 * time.Millisecond)
	}

	// 5. 全程只有一个引擎对象
	seen := map[*ui.VideoPlayer]bool{engineBefore: true}
	if p := app.media.eng(); p != nil {
		seen[p] = true
	}
	for _, s := range app.watch.sessions {
		if s.player != nil {
			seen[s.player] = true
		}
	}
	if len(seen) != 1 {
		t.Fatalf("这一间有 %d 个播放器在跑，应该只有 1 个", len(seen))
	}
}

// Esc 是两档：在播放页收进岛，再按一次才结束这一路。
//
// 第一档和 X 的差别不在画面，在声音：X 是明确退出，Esc 只是换个形态继续
// 听，回来时接着看。所以断言落在引擎上——声音还在不在，引擎说了算。
func TestRealEscapeCollapsesThenCloses(t *testing.T) {
	w := openRealWall(t)
	app, tt := w.app, w.tt
	w.pump(30)

	sess := app.watch.sessions[w.key]
	if sess == nil || sess.player == nil {
		t.Fatal("墙上没有会话")
	}
	eng := sess.player

	// 进播放页（和点格子走同一条路）
	app.watchOpenPlayer(w.room)
	w.pump(40) // 进场过渡演完
	if app.Router.Path() != routePlayer {
		t.Fatalf("没进播放页：路由是 %s", app.Router.Path())
	}
	if app.media.eng() != eng {
		t.Fatal("进播放页换了播放器")
	}

	// 第一档：收进岛。路由退回墙，引擎和声音都在。
	tt.Key(0, ui.KeyEscape)
	w.pump(40)
	if got := app.Router.Path(); got == routePlayer {
		t.Fatal("Esc 之后还停在播放页")
	}
	if app.media.eng() != eng {
		t.Fatal("Esc 把这一路关了：应该收进岛、声音留着")
	}
	if v := eng.Volume(); v <= 0 {
		t.Fatalf("收进岛之后音量是 %.2f：声音没留住", v)
	}
	tile, ok := tt.Find(watchTileLabel(w.room))
	if !ok {
		t.Fatalf("回到墙上找不到格子：%v", tt.Texts())
	}
	if l := w.luma(tile); l < 5 {
		t.Fatalf("收进岛之后墙格是黑的（亮度 %.1f）", l)
	}
	if _, ok := tt.Find("取消观测发声"); !ok {
		t.Fatal("收进岛之后岛上没有这一路声音的入口")
	}

	// 第二档：结束这一路。收声，画面留在墙上做静音预览。
	tt.Key(0, ui.KeyEscape)
	w.pump(20)
	if v := eng.Volume(); v > 0 {
		t.Fatalf("第二下 Esc 之后音量还是 %.2f：没有收声", v)
	}
	if _, ok := tt.Find("取消观测发声"); ok {
		t.Fatal("收声之后岛上还画着退出键")
	}
	if l := w.luma(tile); l < 5 {
		t.Fatalf("收声之后墙格黑了（亮度 %.1f）：应该留成静音预览", l)
	}
}

// staticProvider：永远在播、有档位的平台。取流走 openNative，被换成真引擎。
type staticProvider struct{ live bool }

func (p *staticProvider) Platform() live.Platform { return live.Bilibili }
func (p *staticProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (p *staticProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (p *staticProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }
func (p *staticProvider) RoomInfo(context.Context, string) (live.Room, error) {
	return live.Room{Platform: live.Bilibili, RoomID: "1", Live: p.live}, nil
}
func (p *staticProvider) StreamForPlayback(context.Context, string, string, string) (live.Stream, live.Variant, error) {
	v := live.Variant{Label: "原画", URL: "fake", Width: 320, Height: 180, FrameRate: 30}
	return live.Stream{Platform: live.Bilibili, RoomID: "1", Live: p.live, Variants: []live.Variant{v}}, v, nil
}

// regionLuma 取一块区域的平均亮度（0–255）。用来判「这块是不是黑的」。
// 返回 -1 表示区域为空。
func regionLuma(img *image.RGBA, r ui.Rect) float64 {
	b := img.Bounds()
	x0, y0 := max(int(r.X), b.Min.X), max(int(r.Y), b.Min.Y)
	x1, y1 := min(int(r.X+r.W), b.Max.X), min(int(r.Y+r.H), b.Max.Y)
	if x1 <= x0 || y1 <= y0 {
		return -1
	}
	var sum float64
	n := 0
	for y := y0; y < y1; y += 2 {
		for x := x0; x < x1; x += 2 {
			c := img.RGBAAt(x, y)
			sum += float64(c.R) + float64(c.G) + float64(c.B)
			n += 3
		}
	}
	if n == 0 {
		return -1
	}
	return sum / float64(n)
}
