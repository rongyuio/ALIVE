package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	mymedia "github.com/egoist/mygo/media"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 表面之间的连续过渡：墙格 → 播放页，同一间房不该硬切。
//
// 判据分两层，各自都有分辨力：
//
//  1. 双击之后路由器处于 TransitionMatch——硬切就是 TransitionNone；
//  2. 起飞那一帧，另一格的位置仍然是墙的内容（红封面），不是播放舞台的
//     黑底——说明墙没有被瞬间抽走，飞行元素是长在它上面的。
//
// 播放器先暂停，避免 GIF 自己换帧污染第二层判据。
//
// 先红后绿过：把 pushRoute 换回 Router.Push，第 1 层直接失败。
func TestWallToPlayerIsContinuous(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r).player.Pause()
	}
	tt.Frame()

	// 另一格：不参与这次跳转，但落在播放舞台的范围之内。
	other, ok := tt.Find(watchTileLabel(rooms[2]))
	if !ok {
		t.Fatal("找不到第三格")
	}
	probe := image.Pt(int(other.X+other.W/2), int(other.Y+other.H/2))

	label := watchTileLabel(rooms[0])
	doubleClick(t, tt, label)
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("没有进播放页：route=%q", got)
	}

	// 1. 走的是连续过渡，不是硬切。
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("进播放页时路由器是 %v，期望 TransitionMatch：这一下是硬切",
			app.Router.Transition)
	}

	// 2. 起飞帧墙还在下面。
	if c := tt.Image().RGBAAt(probe.X, probe.Y); isStageInk(c) {
		t.Fatalf("起飞帧在另一格的位置已经是舞台底色 %v：墙被瞬间抽走了", c)
	}
}

// 退出播放页是连续过渡（反向收缩），而且全程不出现封面/黑底。
//
// 这条曾经反过来写过：当时退出是硬切，理由是「视频交回墙上之后播放页那一侧
// 没有引擎可画」。那个理由建立在一个已经被拆掉的模型上——墙格和播放页各存
// 一份引擎、靠 adopt/release 搬。现在一间房一个 roomSession，两边画的是
// 同一个播放器，退出时只要**把放手推迟到过渡演完**，旧页面就全程有画面。
//
// 判据：退出后每一帧墙格中心都必须是视频画面，且路由处于 TransitionMatch。
func TestExitToWallKeepsThePicture(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	useProvider(t, plainProvider{})
	oldEngine := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return &trackSpy{} }
	t.Cleanup(func() { engineOf = oldEngine })
	for _, r := range rooms {
		steadySession(t, app, r)
	}
	tt.Frame()

	label := watchTileLabel(rooms[0])
	doubleClick(t, tt, label)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	if err := tt.Click("退出直播间"); err != nil {
		t.Fatal(err)
	}
	sawMatch := false
	for i := 0; i < 6; i++ {
		tt.Frame()
		if got := app.Router.Path(); got != routeFollows {
			t.Fatalf("第 %d 帧还没回到墙：route=%q", i, got)
		}
		if app.Router.Transition == ui.TransitionMatch {
			sawMatch = true
		}
		// 过渡中页面在交叉淡化，格子颜色是红与背景的混合值——所以这里只
		// 拦「黑帧」（引擎已放手、那一侧没画面可画），落定再要求是纯视频。
		tr, ok := tt.Find(watchTileLabel(rooms[0]))
		if !ok {
			t.Fatalf("第 %d 帧墙格不在场", i)
		}
		if l := regionLuma(tt.Image(), tr); l >= 0 && l < 5 {
			t.Fatalf("第 %d 帧墙格是黑的：退出时引擎放手太早", i)
		}
		tt.Advance(16 * time.Millisecond)
	}
	if !sawMatch {
		t.Fatal("退出没有走连续过渡：同一间房缩回格子应该是飞的，不是硬切")
	}
	// 过渡演完之后，格子必须落成视频画面（不是封面）。
	tt.Advance(routeTransitionDur + time.Second)
	tt.Frame()
	if r, g, b, ok := tilePixel(tt, rooms[0]); !ok || !isVideoRed(r, g, b) {
		t.Fatalf("落定格是 %d,%d,%d，不是视频画面", r, g, b)
	}
}

// 窗口改尺寸不参与动画：墙格必须在第一帧就是新尺寸，不能一帧帧弹过去。
//
// 文档（docs/ui/transitions.md「When nothing moves」）写明 resize 不animate。
// 之前每个格子身上常挂着 Match + Fly，MyGo 于是把尺寸变化也做了弹簧——
// 最大化/还原时整墙一起缩放十几帧，视频每帧重新适配，看起来就是闪。
func TestWindowResizeDoesNotAnimateTheWall(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	useProvider(t, plainProvider{})
	oldEngine := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return &trackSpy{} }
	t.Cleanup(func() { engineOf = oldEngine })
	for _, r := range rooms {
		steadySession(t, app, r)
	}
	tt.Frame()

	label := watchTileLabel(rooms[0])
	tt.SetSize(1920, 1080)
	tt.Frame()
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	first, ok := tt.Find(label)
	if !ok {
		t.Fatal("改尺寸之后找不到格子")
	}
	if r, g, b, ok := tilePixel(tt, rooms[0]); !ok || !isVideoRed(r, g, b) {
		t.Fatalf("改尺寸第一帧墙格是 %d,%d,%d，不是视频画面", r, g, b)
	}
	for i := 0; i < 8; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		box, _ := tt.Find(label)
		if box != first {
			t.Fatalf("第 %d 帧格子还在动：%v → %v（resize 不该有尺寸动画）", i, first, box)
		}
	}
	tt.SetSize(1440, 900)
	tt.Frame()
	back, _ := tt.Find(label)
	for i := 0; i < 8; i++ {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
		box, _ := tt.Find(label)
		if box != back {
			t.Fatalf("还原尺寸后第 %d 帧格子还在动：%v → %v", i, back, box)
		}
	}
}

// 播放页同样：改尺寸不能让舞台做尺寸动画，画面也不能空。
func TestWindowResizeDoesNotAnimateTheStage(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	useProvider(t, plainProvider{})
	oldEngine := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return &trackSpy{} }
	t.Cleanup(func() { engineOf = oldEngine })
	steadySession(t, app, rooms[0])
	tt.Frame()
	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	tt.SetSize(1920, 1080)
	tt.Frame()
	// 判据是画面红区的矩形：正常重排时它第一帧就是新尺寸；挂着 Match +
	// Fly 时舞台会从旧矩形一帧帧飞过去，红区每帧都在长——那就是"最大化
	// 时闪"。整屏扫，避开舞台之外的背景色。
	want := redBounds(tt.Image())
	tt.Advance(16 * time.Millisecond)
	tt.Frame()
	for i := 0; i < 8; i++ {
		got := redBounds(tt.Image())
		if got != want {
			t.Fatalf("第 %d 帧画面红区是 %v，期望 %v：舞台在飞，窗口改尺寸不该有尺寸动画", i, got, want)
		}
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
}

// listOpenedPlayer 是从分区卡片直接打开播放页的样子：这一间不在观测墙里
// （watchHas 为假），媒体层握着引擎。用户报的「Esc 没动画」「点岛收不起」
// 都发生在这一格。
func listOpenedPlayer(t *testing.T) (*App, *ui.Tester, Streamer) {
	t.Helper()
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	app.Router = ui.NewRouter(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	// 卡片点击：设房间、推路由。媒体层直接给了这一间的引擎，等价于
	// 「从分区列表点进来、已经出画」。
	focusWithPlayer(t, app, room)
	app.openRoom(room)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	return app, tt, room
}

// Esc 从哪来回哪去：从关注页（关注主播列表）打开的，回关注主播，
// 不能因为这一间恰好在观测墙里就跳到观测墙。关注行没有画面可接（它是
// 头像不是封面），所以画面缩进岛，声音留着。
func TestEscReturnsToFollowsRowItCameFrom(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	app := NewMockApp()
	app.Streamers = []Streamer{room}
	app.WatchRooms = []Streamer{room}
	app.Router = ui.NewRouter(routeFollows)
	app.FollowsSection = followsSectionStreamers
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Millisecond)
	tt.Frame()
	// 窗口已经稳定：首帧会把 resizedAt 拨到此刻，而 matchable() 按墙钟
	// 挡掉改尺寸后 400ms 内的 Match。测试里墙钟不走，这里显式让窗口
	// 处于稳定态——和真实使用时按 Esc 那一刻一样。
	app.resizedAt = time.Now().Add(-time.Second)

	label := "关注的主播 " + room.Name
	if err := tt.Click(label); err != nil {
		t.Fatalf("点不到关注行：%v", err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("没有进播放页：route=%q", got)
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("Esc 之后路由是 %q，期望回关注页 %q", got, routeFollows)
	}
	if app.FollowsSection != followsSectionStreamers {
		t.Fatalf("Esc 把关注页的分区改了：%d，期望留在关注主播", app.FollowsSection)
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("Esc 退出是 %v，期望 None：关注行没有画面可接，画面缩进岛", app.Router.Transition)
	}
	if app.surfaceFlight == nil || app.surfaceFlight.to != pictureIsland {
		t.Fatalf("Esc 之后画面没有缩进岛：%+v", app.surfaceFlight)
	}
	if _, ok := tt.Find(label); !ok {
		t.Fatal("回到关注页后找不到那一行")
	}
}

// stepFrames 跑几帧，让一次性收尾走完。
func stepFrames(tt *ui.Tester) {
	for range 6 {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
}

// isStageInk 报告这个像素是不是播放舞台的黑底。
//
// 舞台是 ui.RGB(0,0,0)；留一点余量，抗锯齿边缘不算。
func isStageInk(c color.RGBA) bool { return c.R < 12 && c.G < 12 && c.B < 12 }

// 转场结束后路由器必须恢复成"不播页面过渡"。
//
// 不恢复的话，之后每次点顶栏 tab 都会滑一下——那正是 d488013 关掉的东西。
func TestRouteTransitionRestores(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, Streamer{Platform: "douyu", RoomID: "1", Name: "甲"})
	app.Router.Push(routePlayer)
	app.leavePlayerTo(leaveCollapse, routeHome, followsSectionStreamers, func() { app.Router.Back() })

	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("转场结束后路由器还停在 %v：之后切 tab 会滑一下", app.Router.Transition)
	}
}

// 顶栏 tab 之间不播过渡：方向模型与平级路由不匹配（d488013）。
func TestTabSwitchStaysInstant(t *testing.T) {
	app := NewMockApp()
	app.Router.Push(routeFollows)
	app.pushPage(routeHome)
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("顶栏 tab 之间播了页面过渡：%v", app.Router.Transition)
	}
}

// steadyEngine 是「一直出同一帧画面」的引擎：静态红帧，不退、不暂停。
//
// 过渡和 resize 的判据要一个像素稳定的画面；GIF 播放器会自己换帧，用它
// 分不清「动画在动」还是「内容在动」。
type steadyEngine struct{ pix []byte }

func (e *steadyEngine) SetWake(func()) {}
func (e *steadyEngine) Frame(time.Time) (mymedia.Picture, bool, bool) {
	if e.pix == nil {
		e.pix = make([]byte, 4*160*90)
		for i := 0; i < len(e.pix); i += 4 {
			e.pix[i], e.pix[i+3] = 255, 255
		}
	}
	return mymedia.Picture{W: 160, H: 90, RGBA: func(dst []byte) []byte { return append(dst[:0], e.pix...) }}, true, true
}
func (e *steadyEngine) Interval() time.Duration       { return 16 * time.Millisecond }
func (e *steadyEngine) State() (mymedia.State, error) { return mymedia.Ready, nil }
func (e *steadyEngine) Size() (int, int)              { return 160, 90 }
func (e *steadyEngine) Duration() time.Duration       { return 0 }
func (e *steadyEngine) Position() time.Duration       { return time.Second }
func (e *steadyEngine) Play()                         {}
func (e *steadyEngine) Pause()                        {}
func (e *steadyEngine) Seek(time.Duration)            {}
func (e *steadyEngine) SetLoop(bool)                  {}
func (e *steadyEngine) SetRate(float64)               {}
func (e *steadyEngine) SetVolume(float64)             {}
func (e *steadyEngine) SetMuted(bool)                 {}
func (e *steadyEngine) Close() error                  { return nil }

// steadySession 把一格换成静态视频，并给它一份还没过期的地址。
//
// 地址和刷新时刻必须给：接管进媒体层之后，一条 Expires 为零的变体会被
// 看门狗当成「地址到期」，立刻重取流——测试会从 20ms 变成一次真实请求。
func steadySession(t *testing.T, app *App, room Streamer) *roomSession {
	t.Helper()
	s := fakeSession(t, app, room)
	p := ui.NewEngineVideoPlayer(&steadyEngine{})
	t.Cleanup(p.Close)
	s.player, s.track = p, engineOf(p)
	s.up = live.Variant{URL: "https://example.invalid/live.m3u8", Expires: time.Now().Add(time.Hour), Width: 160, Height: 90}
	s.stream = live.Stream{RoomID: room.RoomID, Platform: live.Bilibili, Live: true}
	s.refreshAt = time.Now().Add(time.Hour)
	s.firstFrame = true
	return s
}

// tilePixel 读墙格中心那一像素。
func tilePixel(tt *ui.Tester, room Streamer) (r, g, b uint8, ok bool) {
	box, ok := tt.Find(watchTileLabel(room))
	if !ok {
		return 0, 0, 0, false
	}
	c := tt.Image().RGBAAt(int(box.X+box.W/2), int(box.Y+box.H/2))
	return c.R, c.G, c.B, true
}

// redBounds 扫出画面那一帧红的外接矩形。舞台飞过去时它每帧都在变。
func redBounds(img *image.RGBA) image.Rectangle {
	return img.Bounds().Intersect(redRect(img))
}

func redRect(img *image.RGBA) image.Rectangle {
	b := img.Bounds()
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, -1, -1
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			r, g, bl, _ := img.At(x, y).RGBA()
			if isVideoRed(uint8(r>>8), uint8(g>>8), uint8(bl>>8)) {
				if x < x0 {
					x0 = x
				}
				if y < y0 {
					y0 = y
				}
				if x > x1 {
					x1 = x
				}
				if y > y1 {
					y1 = y
				}
			}
		}
	}
	if x1 < 0 {
		return image.Rect(0, 0, 0, 0)
	}
	return image.Rect(x0, y0, x1+1, y1+1)
}

// isVideoRed 认 steadyEngine 那一帧红：封面（浅灰）和视频井（近黑）都不满足。
func isVideoRed(r, g, b uint8) bool { return r > 200 && g < 40 && b < 40 }

// 两头都没有这一间的画面：进、出都硬切。
//
// 连续过渡的前提是两端各有一个画着这一间的表面。首页上如果没有它的卡片
// （搜索直达、或者卡片已经滚走），就没有起点；退出时目标页上也没有它的
// 画面，没有落点。给它加页面过渡就是在内容层上动，0010 明确禁止。
func TestNoPictureOnEitherSideCutsHard(t *testing.T) {
	app := NewMockApp()
	room := Streamer{Platform: "douyu", RoomID: "42", Name: "甲"}
	focusForTest(app, room)
	app.Router.Push(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.openRoom(room)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("首页上没有这一间的卡片，进播放页却是 %v：没有起点可飞", app.Router.Transition)
	}
	tt.Advance(time.Second)
	tt.Frame()

	app.closeFocus()
	if app.Router.Path() != routeHome {
		t.Fatalf("退出后没有回首页：route=%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("退出是 %v：首页上没有它的画面，也没有落点", app.Router.Transition)
	}
}

// 站在墙上点岛进播放页：来源是墙，不是"没有共享元素"。
//
// 单击墙格只激活、不开页；从岛上打开是这条路唯一的入口。岛在四个表面上
// 都长一样，所以来源必须在**打开播放页的那一刻**按当前表面记下来——写死
// false 的话，同一格就在屏幕上，进页却是硬切、退出是淡出。
func TestIslandOnWallOpensPlayerFromWall(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routeFollows {
		t.Fatalf("单击格子就离开了墙：route=%q", app.Router.Path())
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点岛没有进播放页：route=%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("进播放页是 %v，期望 TransitionMatch：同一格就在屏幕上却硬切", app.Router.Transition)
	}
}

// 没开播的房间：从墙打开有动画，收起回墙也必须飞回去。
//
// 离线房间没有引擎，但两边画的是同一张封面——格子就在墙上，一样有东西可接。
// 以前飞回格子的判据里带着「这一间有引擎」，离线的这一路于是退成页面交叉
// 淡化，看起来就是没有动画。
func TestOfflineRoomCollapseFliesBackToWall(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	app.watch.init()
	app.watch.sessions[key] = &roomSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播",
	}
	tt.Frame()

	openTile(t, tt, rooms[0])
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("双击离线格没有进播放页：route=%q", got)
	}
	// 窗口已经稳定：matchable() 按墙钟挡掉改尺寸后 400ms 内的 Match，
	// 测试里墙钟走得慢，这里显式让窗口处于稳定态。
	app.resizedAt = time.Now().Add(-time.Second)
	tt.Frame()
	// 进页的过渡窗口已经过去：用户不会在 400ms 内就收起，舞台那时不该
	// 靠进页残留的身份来飞。
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	// 点岛收起：和从墙打开的其它房间同一套收尾。
	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
	first, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("收起之后墙上找不到这一格")
	}
	tt.Advance(160 * time.Millisecond)
	tt.Frame()
	settled, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("飞行途中那一格不见了")
	}
	if first.W <= settled.W {
		t.Fatalf("离线房间收起没有飞回格子：%v → %v（退成了交叉淡化）", first, settled)
	}
	if app.Router.Path() != routeFollows {
		t.Fatalf("收起之后没有回到墙：route=%q", app.Router.Path())
	}
}

// 从观测墙点进去的，Esc 回墙，画面飞回那一格。
func TestEscFromWallOpenedPlayerFliesToWall(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：%q", app.Router.Path())
	}
	// 窗口已经稳定：matchable() 按墙钟挡掉改尺寸后 400ms 内的 Match，
	// 测试里墙钟走得慢，这里显式让窗口处于稳定态。
	app.resizedAt = time.Now().Add(-time.Second)

	tt.Key(0, ui.KeyEscape)
	stepFrames(tt)
	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("Esc 之后路由是 %q，期望回观测墙", got)
	}
	if app.FollowsSection != followsSectionWatch {
		t.Fatal("Esc 回墙后关注页没落在观测分区")
	}
	if app.Router.Transition != ui.TransitionMatch {
		t.Fatalf("Esc 回墙是 %v，期望 Match", app.Router.Transition)
	}
	// 飞行真的在跑：墙格的矩形从舞台尺寸收下来。
	first, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("回到墙上找不到那一格")
	}
	tt.Advance(160 * time.Millisecond)
	tt.Frame()
	settled, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("飞行途中那一格不见了")
	}
	if first.W <= settled.W {
		t.Fatalf("墙格没有从舞台缩回来：%v → %v", first, settled)
	}
}

// Esc 在墙外的播放页上：退回打开它之前那一页，声音留着，画面缩进岛。
//
// 判据落在「路由回首页 + 这一路没关 + 不是整页交叉淡化 + 有一块画面在缩」。
// 交叉淡化会把不透明的视频和浅色列表混成一层灰雾（2026-11-24）。
func TestEscOnPlayerOutsideBoxGoesBackKeepingSound(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)

	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := app.Router.Path(); got != routeHome {
		t.Fatalf("Esc 之后路由是 %q，期望回首页", got)
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("Esc 退出是 %v，期望 None（画面缩进岛），不是整页交叉淡化", app.Router.Transition)
	}
	if _, ok := tt.Find("表面过渡中的画面"); !ok {
		t.Fatal("Esc 收起时没有画面缩进岛")
	}
	if app.media.roomKey() != watchKey(room) || app.focusKey() != watchKey(room) {
		t.Fatalf("Esc 之后这一路没了：roomKey=%q focus=%q", app.media.roomKey(), app.focusKey())
	}
}

// 播放页上点岛 = 收起这一路。以前热区把「在播放页」算成目标仍是播放页，
// 点一下等于原地不动——用户报的「点灵动岛不能收起直播间」。
func TestIslandClickOnPlayerCollapses(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	stepFrames(tt)
	tt.Advance(time.Second)
	tt.Frame()
	if got := app.Router.Path(); got == routePlayer {
		t.Fatalf("点岛之后还停在播放页：热区把目标算成了当前页")
	}
	if app.media.roomKey() != key || app.focusKey() != key {
		t.Fatalf("点岛把这一路关了：roomKey=%q focus=%q", app.media.roomKey(), app.focusKey())
	}
}
