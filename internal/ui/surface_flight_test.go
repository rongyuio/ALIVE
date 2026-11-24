package ui

import (
	"fmt"
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

// 岛上的 X：目标页上没有这一间的卡片，硬切。
func TestCloseWithoutCardCutsHard(t *testing.T) {
	app, tt, _ := listOpenedPlayer(t)

	app.closeFocus()
	tt.Frame()
	if app.Router.Path() != routeHome {
		t.Fatalf("X 之后路由是 %q，期望首页", app.Router.Path())
	}
	if app.surfaceFlight != nil {
		t.Fatalf("结束这一路还在飞：%+v", app.surfaceFlight)
	}
	if app.focusKey() != "" {
		t.Fatalf("X 之后焦点还在：%q", app.focusKey())
	}
	if app.Router.Transition != ui.TransitionNone {
		t.Fatalf("X 用了 %v：没有画面可接，应该硬切", app.Router.Transition)
	}
}

// 岛 → 播放页：封面从岛的位置长出来。
//
// 这一间在目标页上没有卡片（回的是首页，但卡片没记到），岛是它唯一的家。
func TestIslandToPlayerFliesTheCover(t *testing.T) {
	app, tt, room := listOpenedPlayer(t)
	key := watchKey(room)

	// 先收进岛，让焦点活着、岛显示这一路。
	app.collapsePlayer()
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	if app.focusKey() != key {
		t.Fatalf("收起之后这一路没了：%q", app.focusKey())
	}
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("收起演完之后画面还挂着")
	}

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
func openCardAndCollapse(t *testing.T, app *App, tt *ui.Tester, room Streamer) {
	t.Helper()
	focusWithPlayer(t, app, room)
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

// 卡片滚出虚拟列表的构建范围后，旧矩形必须作废：点岛应从岛起飞，
// 而不是从窗口外那块早已不在树上的卡片起飞。
func TestCardRectExpiresWhenScrolledOut(t *testing.T) {
	rooms := cardFlightRooms("房", "英雄联盟", 120)
	app, tt := cardFlightApp(t, rooms)
	room := rooms[0]
	openCardAndCollapse(t, app, tt, room)

	key := watchKey(room)
	if r := app.cardRectFor(key, routeHome, app.FollowsSection); r.W <= 0 {
		t.Fatalf("卡片在屏内时没有记账")
	}

	tt.Move(720, 600)
	for i := 0; i < 8; i++ {
		tt.Wheel(720, 600, 0, 400)
		tt.Advance(200 * time.Millisecond)
	}
	tt.Frame()
	if first, _ := app.HomeList.Visible(); first == 0 {
		t.Fatal("列表没有滚动")
	}
	if r := app.cardRectFor(key, routeHome, app.FollowsSection); r.W > 0 {
		t.Fatalf("卡片已经滚出视口，旧矩形仍然有效：%v", r)
	}

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatalf("点岛失败: %v", err)
	}
	tt.Frame()
	f := app.surfaceFlight
	if f == nil || f.from != pictureIsland {
		t.Fatalf("滚出视口后应从岛起飞：%+v", f)
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
