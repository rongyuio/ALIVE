package ui

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 非墙直播间点岛收起：画面缩进岛，不做整页交叉淡化。
//
// 期望值来自 2026-11-24 的真实截图：交叉淡化中间那几帧是一层灰雾——不透明
// 的视频和浅色卡片互相透过去，控制条的深色长条浮在卡片上。收起的语义是
// 「这一路进了岛」，所以画面要连续缩到岛的位置，列表从第一帧起就是清楚的。
func TestCollapseOutsideWallShrinksIntoIsland(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routeHome {
		t.Fatalf("点岛之后路由是 %q，期望回首页", got)
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("点岛收起用了 %v：整页交叉淡化会把视频和列表混成灰雾", app.Router.Transition)
	}

	first, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatal("收起时窗口里没有画面在缩")
	}
	// 起飞的那一帧必须还是舞台：差一截就是「先跳小再飞」。舞台会被右侧
	// 弹幕面板挤窄，所以起点是播放页量下来的那一块，不是整个正文区。
	stage := app.playerStageBounds
	if stage.W <= 0 {
		t.Fatal("播放页没有量到舞台矩形")
	}
	near := func(a, b float32) bool { return math.Abs(float64(a-b)) <= 1 }
	if !near(first.X, stage.X) || !near(first.Y, stage.Y) ||
		!near(first.W, stage.W) || !near(first.H, stage.H) {
		t.Fatalf("起飞位置不是舞台：flight=%v stage=%v", first, stage)
	}

	tt.Advance(120 * time.Millisecond)
	tt.Frame()
	mid, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatal("飞行途中画面不见了")
	}
	if mid.W >= first.W || mid.H >= first.H {
		t.Fatalf("画面没有缩：%v → %v", first, mid)
	}

	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("演完之后画面还挂在窗口上")
	}
	// 收起不是关闭：声音焦点和岛都要留着。
	if app.focusKey() != key || app.media.roomKey() != key {
		t.Fatalf("收起把这一路关了：focus=%q roomKey=%q", app.focusKey(), app.media.roomKey())
	}
	if _, ok := tt.Find("回到直播间"); !ok {
		t.Fatal("岛没有继续显示这一路")
	}
}

// 卡片 → 播放页：封面从卡片长成舞台，落地后淡出露出画面。
//
// 路由立刻切走（不做整页交叉淡化），目标页从第一帧起就是清楚的；飞的是
// 卡片上那张封面。回列表时反过来缩回同一张卡片。
func TestCardToPlayerFliesTheCover(t *testing.T) {
	// 用带唯一 RoomID 的房间：卡片飞行按 platform|roomID 记账，示例池
	// 的空 ID 会让所有卡片共用一条记录，断言就分不清是哪一张。
	app, tt := cardFlightApp(t, cardFlightRooms("卡", "英雄联盟", 4))
	room := app.Streamers[0]
	// 先量被点的那张卡：断言不能回头读 cardRectFor——那是同一个 map，
	// 记错卡、记到别的卡上都会照样绿。
	want, ok := tt.Find("直播间:" + room.Name)
	if !ok {
		t.Fatalf("找不到 %s 的卡片", room.Name)
	}
	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点卡片没有进播放页：%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("进播放页用了 %v：目标页第一帧就该是清楚的", app.Router.Transition)
	}
	f := app.surfaceFlight
	if f == nil || f.kind != flightEnter || f.from != pictureCard {
		t.Fatalf("没有从卡片起飞：%+v", f)
	}
	if !nearRect(f.fromRect, want) {
		t.Fatalf("起飞矩形不是被点击的那张卡：flight=%v clicked=%v", f.fromRect, want)
	}

	first, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatal("进播放页时没有封面在飞")
	}
	tt.Advance(120 * time.Millisecond)
	tt.Frame()
	mid, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatal("飞行途中封面不见了")
	}
	if mid.W <= first.W || mid.H <= first.H {
		t.Fatalf("封面没有长成舞台：%v → %v", first, mid)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("演完之后封面还挂在窗口上")
	}

	// 回列表：缩回同一张卡片。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if app.Router.Path() != routeHome {
		t.Fatalf("Esc 之后路由是 %q，期望首页", app.Router.Path())
	}
	f = app.surfaceFlight
	if f == nil || f.kind != flightLeave || f.to != pictureCard {
		t.Fatalf("没有缩回卡片：%+v", f)
	}
	if !nearRect(f.toRect, want) {
		t.Fatalf("落点不是被点击的那张卡：flight=%v clicked=%v", f.toRect, want)
	}
}

// nearRect 比较两个矩形，容差 1 DIP（布局取整）。
func nearRect(a, b ui.Rect) bool {
	near := func(x, y float32) bool { return math.Abs(float64(x-y)) <= 1 }
	return near(a.X, b.X) && near(a.Y, b.Y) && near(a.W, b.W) && near(a.H, b.H)
}

// 从播放页点顶栏 tab 离开：画面缩进岛，不是硬切。
//
// 这一下不是「收起」，但声音留着、岛还显示这一路——画面就该去岛上，
// 和 Esc 收起同一个落点。目标页上没有它的卡片，也没有墙格。
func TestTabLeaveFliesIntoIsland(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)

	app.pushPage(routeFollows)
	tt.Frame()
	if app.Router.Path() != routeFollows {
		t.Fatalf("点关注 tab 之后路由是 %q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("离开播放页用了 %v：整页过渡会把画面和列表混在一起", app.Router.Transition)
	}
	if app.surfaceFlight == nil || app.surfaceFlight.to != pictureIsland {
		t.Fatalf("点 tab 离开时画面没有缩进岛：%+v", app.surfaceFlight)
	}
	if app.focusKey() != key {
		t.Fatalf("点 tab 把这一路关了：focus=%q", app.focusKey())
	}
}

// 岛 → 播放页，这一间已经在出画面：**飞活画面**，不硬切。
//
// 2026-11-26 统一模型：飞不飞只由「两端有没有几何」决定；这一间有没有出图只
// 决定飞行块里画什么（活画面 / 封面）。2026-11-25 那条报障（封面盖住活画面、
// 落地再切回直播）的根因是"飞的是封面"，不是"飞"本身——飞活画面就没有
// 那次凭空造出来的切换。
func TestIslandToLivePlayerFliesTheLivePicture(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)
	// 呈现尺寸的推需要一条引擎轨：Tester 里的 GIF 播放器底下没有
	// *playback.Engine，不换这个钩子，present 永远停在 0,0。
	spy := &trackSpy{}
	oldEngine := engineOf
	engineOf = func(*ui.VideoPlayer) videoTrack { return spy }
	t.Cleanup(func() { engineOf = oldEngine })

	// 先收进岛，让焦点活着、岛显示这一路。
	app.collapsePlayer()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if app.focusKey() != key {
		t.Fatalf("收起之后这一路没了：%q", app.focusKey())
	}
	if !app.readyPicture(app.focus) {
		t.Fatal("夹具里这一间没有画面，测不到「飞出活画面」这条")
	}
	// 收起之后是后台收听：引擎按 160×90 渲染。
	if got := app.focus.present; got != (presentSize{W: 160, H: 90}) {
		t.Fatalf("后台收听的呈现尺寸是 %+v，期望 160×90", got)
	}

	app.returnToFocus()
	// 进播放页的同一拍就要推全尺寸。
	if got := app.focus.present; got != (presentSize{}) {
		t.Fatalf("进播放页那一拍没有推全尺寸：present=%+v", got)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点岛没有回播放页：%q", app.Router.Path())
	}
	f := app.surfaceFlight
	if f == nil || f.from != pictureIsland {
		t.Fatalf("点岛没有从岛起飞：%+v", f)
	}
	if f.pixels != pixelsLive {
		t.Fatalf("这一间已经在出画面，飞行块却画封面：pixels=%v", f.pixels)
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("从岛进播放页用了 %v：只有墙格↔播放页才该用 Match", app.Router.Transition)
	}
}

// 岛 → 播放页，这一间还没有画面（在取流）：封面从岛的位置长出来。
func TestIslandToPlayerFliesTheCoverWhileConnecting(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)

	app.collapsePlayer()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if app.focusKey() != key {
		t.Fatalf("收起之后这一路没了：%q", app.focusKey())
	}

	// 把画面拿走，只留「正在取流」：这就是封面飞行该出现的状态。
	s := app.focus
	s.player.Close()
	s.player = nil
	s.loading = true
	tt.Frame()

	app.returnToFocus()
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点岛没有回播放页：%q", app.Router.Path())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("从岛进播放页用了 %v：目标页第一帧就该是清楚的", app.Router.Transition)
	}
	f := app.surfaceFlight
	if f == nil || f.kind != flightEnter || f.from != pictureIsland {
		t.Fatalf("没有从岛起飞：%+v", f)
	}
	if f.fromRect.W <= 0 || f.fromRect.H <= 0 {
		t.Fatalf("岛的位置没有量出来：%v", f.fromRect)
	}
}

// 点岛的起点是岛，不是同一页上那张卡片。
//
// 用户在屏幕正中的岛上点了一下，画面却从列表里某张卡片长出来——读起来
// 就是「不知道从哪冒出来一块封面」。卡片的画面归属只对「点卡片」成立。
func TestIslandClickStartsFromTheIslandNotACard(t *testing.T) {
	app, tt := cardFlightApp(t, cardFlightRooms("卡", "英雄联盟", 4))
	room := app.Streamers[0]

	// 焦点是这一间，还在取流：岛展开、首页上也有它的卡片。
	s := focusForTest(app, room)
	s.loading = true
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()

	card, ok := tt.Find("直播间:" + room.Name)
	if !ok {
		t.Fatalf("找不到 %s 的卡片", room.Name)
	}
	island, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到岛")
	}
	if r := app.cardRectFor(watchKey(room), routeHome, app.FollowsSection); r.W <= 0 {
		t.Fatal("卡片矩形没有记账，这条测试就测不到「被卡片抢走」")
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatalf("点岛失败: %v", err)
	}
	tt.Frame()
	f := app.surfaceFlight
	if f == nil || f.from != pictureIsland {
		t.Fatalf("点岛没有从岛起飞：%+v", f)
	}
	if nearRect(f.fromRect, card) {
		t.Fatalf("画面从卡片起飞了：flight=%v card=%v", f.fromRect, card)
	}
	// 起点必须落在岛的中心：宽高由飞行块自己定，中心不能偏。
	if dx := math.Abs(float64(f.fromRect.X + f.fromRect.W/2 - (island.X + island.W/2))); dx > 1 {
		t.Fatalf("起点不在岛的中心：flight=%v island=%v", f.fromRect, island)
	}
}

// 卡片 → 播放页，这一间已经在出画面：同样飞活画面。
//
// 统一模型下"同一张卡片、同一个操作"永远得到同一个结果——第一次开有动画，
// 收起后再开也有动画，区别只在飞行块里画活画面还是封面。
func TestCardToLivePlayerFliesTheLivePicture(t *testing.T) {
	app, tt := cardFlightApp(t, cardFlightRooms("卡", "英雄联盟", 4))
	room := app.Streamers[0]
	focusWithPlayer(t, app, room)
	tt.Frame()

	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatalf("点卡片失败: %v", err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点卡片没有进播放页：%q", app.Router.Path())
	}
	f := app.surfaceFlight
	if f == nil || f.from != pictureCard {
		t.Fatalf("点卡片没有从卡片起飞：%+v", f)
	}
	if f.pixels != pixelsLive {
		t.Fatalf("这一间已经在出画面，飞行块却画封面：pixels=%v", f.pixels)
	}
}

// ---------------------------------------------------------------- 卡片矩形的有效期

// cardFlightRooms 造一批互不相同的房间。卡片飞行按 platform|roomID 记账，
// 空 ID 会让整个示例池共用一条记录（见 mock_test.go 的说明）。
func cardFlightRooms(prefix, category string, n int) []Streamer {
	out := make([]Streamer, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Streamer{
			Platform: "douyu",
			RoomID:   fmt.Sprintf("%s-%d", prefix, i),
			Name:     fmt.Sprintf("%s%03d", prefix, i),
			Title:    "标题",
			Category: category,
			Live:     true,
		})
	}
	return out
}

func cardFlightApp(t *testing.T, rooms []Streamer) (*App, *ui.Tester) {
	t.Helper()
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.Streamers = rooms
	app.Router = ui.NewRouter(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Millisecond)
	tt.Frame()
	app.resizedAt = time.Now().Add(-time.Second)
	return app, tt
}

// openCardAndCollapse 点一张真实卡片进播放页，再 Esc 收起（焦点留着，
// 岛继续显示这一路）。后面的滚动 / 换分区测试都从这一刻开始。
//
// 这一间故意停在「正在取流」：封面飞行只在这一种状态下发生，有画面的房间
// 进播放页是硬切（见 enterPlayer 的 readyPicture）。用活画面做夹具，点岛
// 那一下根本不会有飞行块，测的就不是落点了。
func openCardAndCollapse(t *testing.T, app *App, tt *ui.Tester, room Streamer) {
	t.Helper()
	s := focusForTest(app, room)
	s.loading = true
	if err := tt.Click("直播间:" + room.Name); err != nil {
		t.Fatalf("点卡片失败: %v", err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点卡片后路由 %q，期望播放页", app.Router.Path())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if app.Router.Path() != routeHome {
		t.Fatalf("Esc 之后路由 %q，期望首页", app.Router.Path())
	}
	if _, ok := tt.Find("回到直播间"); !ok {
		t.Fatal("收起后岛没有显示这一路")
	}
}

// 换二级分区之后，上一批卡片的位置不能拿来当落点：网格整页重建，
// 旧代次的记录必须失效。
func TestCardRectExpiresAcrossCategories(t *testing.T) {
	aRooms := cardFlightRooms("甲", "英雄联盟", 5)
	bRooms := cardFlightRooms("乙", "CS2", 5)
	app, tt := cardFlightApp(t, append(append([]Streamer{}, aRooms...), bRooms...))
	room := aRooms[0]
	openCardAndCollapse(t, app, tt, room)

	key := watchKey(room)
	if r := app.cardRectFor(key, routeHome, app.FollowsSection); r.W <= 0 {
		t.Fatalf("卡片在屏内时没有记账")
	}

	if err := tt.Click("分类:CS2"); err != nil {
		t.Fatalf("切分区失败: %v", err)
	}
	tt.Frame()
	if got := app.currentCategory(); got != "CS2" {
		t.Fatalf("没有切到 CS2：%q", got)
	}
	if r := app.cardRectFor(key, routeHome, app.FollowsSection); r.W > 0 {
		t.Fatalf("换分区后旧卡片记录仍然有效：%v", r)
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatalf("点岛失败: %v", err)
	}
	tt.Frame()
	f := app.surfaceFlight
	if f == nil || f.from != pictureIsland {
		t.Fatalf("换分区后应从岛起飞：%+v", f)
	}
}

// 收起的第一帧必须和舞台最后一帧是同一幅画。
//
// 飞行块从舞台矩形起飞，所以它的底图、模糊、压暗、画面位置都必须和舞台
// 那一套完全一致。以前飞行块铺的是一张**没有模糊**的底图：起飞那一帧
// 黑边区从「糊」跳到「实」，正是用户说的「闪一下」里最容易被漏掉的一种。
//
// 判据是像素：8×8 棋盘底图在模糊之后，格子边界附近和原图差得很远；
// 两边共用 backdropLayers 时两帧逐点相等。容差 0——Tester 的软件光栅器
// 没有随机源（见 ui-regressions.md）。
func TestLeaveFlightStartsAsTheStage(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	room := Streamer{
		Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题",
		Live: true, CoverURL: "https://example.invalid/cover.jpg",
	}
	app.Router = ui.NewRouter(routeHome)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	useBackdropBitmap(t, checkerBitmap(8, 8))
	focusWithPlayer(t, app, room)
	app.openRoom(room)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	stage := app.playerStageBounds
	if stage.W <= 0 || stage.H <= 0 {
		t.Fatal("播放页没有量到舞台矩形")
	}
	before := tt.Image()

	app.collapsePlayer()
	tt.Frame()
	if f := app.surfaceFlight; f == nil || f.kind != flightLeave {
		t.Fatalf("收起没有起飞行块：%+v", f)
	}
	after := tt.Image()

	// 只比黑边区。舞台中间那块是画面，上下还压着悬浮 chrome（顶栏 / 控制条），
	// 飞行块不画 chrome，整块比会把“控制条消失”当成失败。
	// 黑边区的宽度用生产同一个 letterbox 算，不从像素里猜。
	pw, ph := app.focus.player.Size()
	_, barR, _, barL := letterbox(stage.W, stage.H, float32(pw), float32(ph))
	if barL < 40 || barR < 40 {
		t.Fatalf("黑边太窄（左右 %v/%v），测不出底图差异", barL, barR)
	}
	// 上下各让 120：躲开顶栏和控制条。
	top := int(stage.Y + 120)
	bottom := int(stage.Y + stage.H - 120)
	leftX0, leftX1 := int(stage.X+24), int(stage.X+barL-8)
	rightX0, rightX1 := int(stage.X+stage.W-barR+8), int(stage.X+stage.W-24)
	diff, samples := 0, 0
	for y := top; y < bottom; y += 7 {
		for _, span := range [][2]int{{leftX0, leftX1}, {rightX0, rightX1}} {
			for x := span[0]; x < span[1]; x += 7 {
				b, a := before.RGBAAt(x, y), after.RGBAAt(x, y)
				samples++
				if b != a {
					diff++
					if diff == 1 {
						t.Logf("第一处不同 (%d,%d)：舞台 %v → 飞行 %v", x, y, b, a)
					}
				}
			}
		}
	}
	if samples == 0 {
		t.Fatal("没有采到样")
	}
	if diff > 0 {
		t.Fatalf("%d/%d 个采样点不同：收起的第一帧不是舞台那一帧", diff, samples)
	}
}

// checkerBitmap 造一张黑白棋盘底图：模糊前后每个采样点都会变，用来区分
// 「铺了同一套模糊底」和「铺了一张没模糊的底」。
func checkerBitmap(w, h int) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			c := uint8(0)
			if (x+y)%2 == 0 {
				c = 255
			}
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c, c, c, 255
		}
	}
	return ui.NewBitmap(img)
}
