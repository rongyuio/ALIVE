package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 收口后的行为契约：收起 / 关闭 / 返回路径。
//
// 这几条对应的是用户报过的真实故障：收起的直播间点岛上的 X 没反应
// （watchDeactivate 对 WatchActive 为空的焦点直接 return）、从列表打开的
// 墙内房间退出时按"回墙"处理。判据全部落在可观察状态上：焦点、会话表、
// 路由，而不是内部计数器。

func focusSpecRoom(id, name string) Streamer {
	return Streamer{Platform: "bilibili", RoomID: id, Name: name, Title: "标题" + name, Live: true}
}

func newHomeMock(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	return app, tt
}

// openSpecRoom 走生产入口打开一间，并挂一个假引擎模拟"正在出声"。
// gifPlayer 会自己出帧，所以岛上、播放页上都有画面。
func openSpecRoom(t *testing.T, app *App, tt *ui.Tester, room Streamer) *roomSession {
	t.Helper()
	app.openRoom(room)
	tt.Frame()
	s := app.focus
	if s == nil {
		t.Fatal("openRoom 没有建立焦点")
	}
	p := gifPlayer(t)
	p.SetVolume(float64(s.volume))
	s.player = p
	s.track = &trackSpy{}
	s.firstFrame = true
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	return s
}

func settle(tt *ui.Tester) {
	for range 8 {
		tt.Advance(16 * time.Millisecond)
		tt.Frame()
	}
}

// 收起的直播间（墙外）：岛的 X 必须真的关掉它。
//
// 旧实现非播放页一律走 watchDeactivate，它第一行是
// `if a.WatchActive == "" { return }`；从列表打开的直播间从来没写过
// WatchActive，所以点了什么都不发生，而同一时刻按 Esc 却能关。
func TestIslandXClosesCollapsedRoomOutsideBox(t *testing.T) {
	app, tt := newHomeMock(t)
	room := focusSpecRoom("42", "墙外")
	s := openSpecRoom(t, app, tt, room)
	key := watchKey(room)

	// Esc 收起：离开播放页，声音、引擎、岛都留着。
	app.handleEscape()
	settle(tt)
	if app.Router.Path() != routeHome {
		t.Fatalf("收起之后路由是 %q，期望首页", app.Router.Path())
	}
	if app.focusKey() != key {
		t.Fatalf("收起把焦点丢了：%q", app.focusKey())
	}
	if s.player == nil || s.player.Volume() <= 0 {
		t.Fatal("收起之后声音没了：Esc 只该换形态")
	}

	// 岛上那颗 X 现在叫「关闭直播间」。点它。
	if _, ok := tt.Find("关闭直播间"); !ok {
		t.Fatalf("收起的直播间岛上没有关闭键：texts=%q", tt.Texts())
	}
	if err := tt.Click("关闭直播间"); err != nil {
		t.Fatal(err)
	}
	settle(tt)

	if app.focusKey() != "" {
		t.Fatalf("点 X 之后焦点还在：%q", app.focusKey())
	}
	if app.watch.sessions[key] != nil {
		t.Fatal("墙外的房间关掉之后会话还留着：静音引擎会一直跑")
	}
	if got := app.islandFormOf(); got != islandIdle {
		t.Fatalf("关掉之后岛是 %v，期望时钟", got)
	}
}

// 已经离开墙（首页）：X 不再叫「取消观测发声」，这一下真的关会话。
//
// 离开墙就把墙上的预览停了，这时说「取消发声」会承诺一个不存在的预览；
// 文案和动作都必须是「关闭」。
func TestIslandXOffWallClosesBoxedRoom(t *testing.T) {
	app, tt := newHomeMock(t)
	room := focusSpecRoom("43", "墙内")
	app.WatchRooms = []Streamer{room}
	s := openSpecRoom(t, app, tt, room)
	key := watchKey(room)

	// Esc 收起：回到首页。这一下已经离开了墙。
	app.handleEscape()
	settle(tt)
	if app.Router.Path() != routeHome {
		t.Fatalf("收起之后路由是 %q，期望首页", app.Router.Path())
	}
	if !app.watch.parked {
		t.Fatal("离开墙之后没有停掉墙上的预览")
	}
	if _, ok := tt.Find("取消观测发声"); ok {
		t.Fatal("已经离开墙，X 还写着「取消观测发声」")
	}
	if _, ok := tt.Find("关闭直播间"); !ok {
		t.Fatalf("离开墙之后没有「关闭直播间」：texts=%q", tt.Texts())
	}

	if err := tt.Click("关闭直播间"); err != nil {
		t.Fatal(err)
	}
	settle(tt)
	if app.focusKey() != "" {
		t.Fatalf("点 X 之后焦点还在：%q", app.focusKey())
	}
	if app.watch.sessions[key] != nil {
		t.Fatal("离开墙之后点 X，会话还留着")
	}
	if s.player != nil {
		t.Fatal("离开墙之后点 X，引擎没有关")
	}
}

// Esc 两档：在播放页收起，不在播放页关闭。
func TestEscapeCollapsesThenCloses(t *testing.T) {
	app, tt := newHomeMock(t)
	room := focusSpecRoom("44", "两档")
	s := openSpecRoom(t, app, tt, room)
	key := watchKey(room)

	app.handleEscape() // 第一档：收起
	settle(tt)
	if app.Router.Path() != routeHome || app.focusKey() != key || s.player == nil {
		t.Fatalf("第一下 Esc 没收起：route=%q focus=%q", app.Router.Path(), app.focusKey())
	}

	app.handleEscape() // 第二档：关闭
	settle(tt)
	if app.focusKey() != "" {
		t.Fatalf("第二下 Esc 没关闭：焦点是 %q", app.focusKey())
	}
	if app.watch.sessions[key] != nil {
		t.Fatal("墙外的房间关闭后会话还在")
	}
}

// 在观测墙上按 Esc：取消 active，格子留成静音预览，不离开墙。
func TestEscapeOnWallCancelsActive(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	s := focusWithPlayer(t, app, rooms[0])
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	app.handleEscape()
	settle(tt)

	if app.focusKey() != "" {
		t.Fatalf("墙上按 Esc 之后焦点还在：%q", app.focusKey())
	}
	if app.Router.Path() != routeFollows || app.FollowsSection != followsSectionWatch {
		t.Fatalf("Esc 把用户带离了观测墙：route=%q section=%v", app.Router.Path(), app.FollowsSection)
	}
	if app.watch.sessions[watchKey(rooms[0])] != s || s.player == nil {
		t.Fatal("墙上取消 active 把这一格关掉了：应该留成静音预览")
	}
}

// 过渡的落点只看「目标页上还有没有这一间的画面」，不看从哪一页进来的。
//
// 墙上 / 不在墙上、关注页上次停在观测墙——这些都不是判据。判据是
// transitionLeavingPlayer 里那一次 pictureOn(key, 目标页, 分区)。
func TestReturnAnimationFollowsDestination(t *testing.T) {
	t.Run("回首页：墙内也不飞格子", func(t *testing.T) {
		app, tt := newHomeMock(t)
		room := focusSpecRoom("45", "墙内从列表")
		app.WatchRooms = []Streamer{room}
		app.FollowsSection = followsSectionWatch // 上次停在墙，不代表这一下回墙
		openSpecRoom(t, app, tt, room)

		app.collapsePlayer()
		if app.Router.Transition != ui.TransitionNone {
			t.Fatalf("回首页用了 %v：这一页上没有它的格子", app.Router.Transition)
		}
		if app.surfaceFlight == nil || app.surfaceFlight.to != pictureIsland {
			t.Fatalf("回首页的落点不是岛：%+v", app.surfaceFlight)
		}
		settle(tt)
		if app.Router.Path() != routeHome {
			t.Fatalf("退回了 %q，期望首页", app.Router.Path())
		}
	})

	t.Run("从观测墙打开、收回观测墙：飞回格子", func(t *testing.T) {
		rooms := watchTestRooms(1)
		app, tt := newWatchMock(t, rooms)
		focusWithPlayer(t, app, rooms[0])
		tt.Frame()
		app.openRoom(rooms[0])
		if app.Router.Transition != ui.TransitionMatch {
			t.Fatalf("从墙格进播放页是 %v，期望 Match：同一格就在屏幕上", app.Router.Transition)
		}
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()

		app.collapsePlayer()
		// 飞回墙格是两拍：第一拍让舞台带上 Match 身份，第二拍才换路由。
		tt.Frame()
		if app.Router.Transition != ui.TransitionMatch {
			t.Fatalf("收起回墙是 %v，期望 Match：格子就在目标页上", app.Router.Transition)
		}
		settle(tt)
		if app.Router.Path() != routeFollows || app.FollowsSection != followsSectionWatch {
			t.Fatalf("收起之后没有回墙：route=%q section=%v", app.Router.Path(), app.FollowsSection)
		}
	})
}

// 播放页里换台不叠历史：X 之后回的是打开播放页之前那一页。
//
// 以前在播放页用搜索换台会 Push 第二个 player 路由，X 退回去还是播放页，
// 取流路径立刻又把同一间开起来——看起来像 X 没关掉。
func TestRoomSwitchInsidePlayerDoesNotStackHistory(t *testing.T) {
	app, tt := newHomeMock(t)
	a := focusSpecRoom("46", "第一间")
	b := focusSpecRoom("47", "第二间")
	openSpecRoom(t, app, tt, a)

	app.openRoom(b) // 已经在播放页：Replace
	tt.Frame()
	if app.focusKey() != watchKey(b) {
		t.Fatalf("换台没有把焦点切到第二间：%q", app.focusKey())
	}

	app.closeFocus() // X：结束并退回
	settle(tt)
	if app.Router.Path() != routeHome {
		t.Fatalf("X 之后路由是 %q，期望首页：播放页历史被叠了一层", app.Router.Path())
	}
	if app.focusKey() != "" {
		t.Fatalf("X 之后焦点还在：%q", app.focusKey())
	}
}
