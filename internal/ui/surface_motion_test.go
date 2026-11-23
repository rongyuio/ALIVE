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
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
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

// 退出播放页是硬切，而且回到墙的第一帧就得是画面，不能先闪封面/黑底。
//
// 为什么退出不做 Match：一个 VideoPlayer 只能被一个 ui.Video 画出来（实测
// 两个元素只有一个出画），而退出时必须把画面交回墙上的格子——播放舞台这一侧
// 就没有引擎可画了。TransitionMatch 会在淡出期间把旧页面重建一次，那一帧
// 舞台的 player 是 nil，整块闪成封面或黑底（帧探针实测第 0–2 帧全黑，之后
// 才淡入）。硬切则新墙第一帧就带着同一路画面，没有空窗。
//
// 先红后绿过：把 exitPlayer 换回 TransitionMatch，第 0 帧就是黑。
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
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	if err := tt.Click("退出直播间"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		tt.Frame()
		if got := app.Router.Path(); got != routeFollows {
			t.Fatalf("第 %d 帧还没回到墙：route=%q", i, got)
		}
		if app.Router.Transition != ui.TransitionNone {
			t.Fatalf("退出走了页面过渡（%v）：旧页面会重建，舞台这一侧没有引擎", app.Router.Transition)
		}
		r, g, b, ok := tilePixel(tt, rooms[0])
		if !ok {
			t.Fatalf("第 %d 帧墙格不在场", i)
		}
		if !isVideoRed(r, g, b) {
			t.Fatalf("第 %d 帧墙格是 %d,%d,%d，不是视频画面：退出时闪成了封面/黑底", i, r, g, b)
		}
		tt.Advance(16 * time.Millisecond)
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
	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
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

// 从列表进播放页、再退出，不连续。
//
// 这是和上一条配套的边界：连续过渡的前提是"退回去的那一页还画着这一间"。
// 从首页卡片进来的，退回去是首页——首页没有这一间的画面可接。给它加交叉
// 淡化就是在内容层上动，0010 明确禁止。所以这条必须硬切。
//
// 判据是反向的：路由器在退出那一帧仍然是 TransitionNone。如果哪天有人
// 把连续判据放宽成"只要在播放页就连续"，这条会红。
func TestListToPlayerExitsHard(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "42", Name: "甲"}
	app.media.roomKey = "douyu|42"
	app.Router.Push(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.pushRoute(routePlayer)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：route=%q", app.Router.Path())
	}
	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	tt.Frame()

	app.exitPlayer()
	if app.Router.Path() != routeHome {
		t.Fatalf("退出后没有回首页：route=%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("从列表进的播放页退出时播了过渡（%v）：退回去的首页没有画面可接",
			app.Router.Transition)
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
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	app.media.roomKey = "douyu|1"
	app.Router.Push(routePlayer)
	app.pushRoute(routeHome)

	if app.Router.Transition != ui.TransitionNone {
		// 从播放页离开本来就是"表面之间"，这一下应该是连续的。
		t.Logf("离开播放页的过渡是 %v（期望 Match）", app.Router.Transition)
	}

	app.tickRouteTransition(time.Now().Add(routeTransitionDur + time.Second))
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("转场结束后路由器还停在 %v：之后切 tab 会滑一下", app.Router.Transition)
	}
}

// 顶栏 tab 之间不播过渡：方向模型与平级路由不匹配（d488013）。
func TestTabSwitchStaysInstant(t *testing.T) {
	app := NewMockApp()
	app.Router.Push(routeFollows)
	app.pushRoute(routeHome)
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
func steadySession(t *testing.T, app *App, room Streamer) *wallSession {
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
