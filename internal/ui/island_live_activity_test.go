package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/watch"
)

// 从岛回到一个你自己按过暂停、静音过的房间：接着刚才那样。
//
// iPhone 上从灵动岛的 Live Activity 回到 App 不会推翻你在 App 里按下的
// 暂停。以前 setFocus 只有一个入口，无条件 playing=true、静音还抬到可听
// 音量——用户特意做的那两下被系统改回去了。
func TestIslandReturnRestoresPausedMuted(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题"}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Router = ui.NewRouter(routeHome)
	s := a.sessionFor(key, room)
	a.focus = s
	s.playing = false
	s.volume = 0

	// 收起之后从岛点回去。
	a.islandTap()

	if a.Router.Path() != routePlayer {
		t.Fatalf("点岛没有打开播放页，停在 %s", a.Router.Path())
	}
	if s.playing {
		t.Error("从岛回去把播放意图翻成了「在播」：用户按下的暂停被推翻了")
	}
	if s.volume != 0 {
		t.Errorf("从岛回去把音量从 0 抬到了 %.2f：用户按下的静音被推翻了", s.volume)
	}
}

// 墙格「在墙上听」是明确的「点开就是要听」，仍然按 intentListen 走。
func TestWallActivateStillListens(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Router = ui.NewRouter(routeFollows)
	a.FollowsSection = followsSectionWatch
	a.WatchRooms = []Streamer{room}
	s := a.sessionFor(key, room)
	s.playing = false
	s.volume = 0

	a.watchActivate(room)

	if !s.playing {
		t.Error("点「在墙上听」之后没有开始播")
	}
	if s.volume <= 0 {
		t.Error("点「在墙上听」之后仍然是静音：点了没声")
	}
}

// 一帧里只有一个 ui.Video 画这一路的画面。
//
// 一个 VideoPlayer 只能被一个 ui.Video 画出来；两个元素同时画，实测只有
// 一个出画、另一个是黑的——那正是「一个直播间同时出现放大后的终态和过渡态」
// 的来源。videoOwnerOf 是唯一的裁决点。
func TestSingleVideoOwner(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	key := watchKey(room)

	base := func() *App {
		a := newApp()
		a.Platform = room.Platform
		a.WatchRooms = []Streamer{room}
		s := a.sessionFor(key, room)
		a.focus = s
		return a
	}

	t.Run("播放页：舞台", func(t *testing.T) {
		a := base()
		a.Router = ui.NewRouter(routePlayer)
		if got := a.videoOwnerOf(key); got != videoStage {
			t.Fatalf("得到 %v，应当是舞台", got)
		}
	})

	t.Run("观测墙且不在播放页：格子", func(t *testing.T) {
		a := base()
		a.Router = ui.NewRouter(routeFollows)
		a.FollowsSection = followsSectionWatch
		if got := a.videoOwnerOf(key); got != videoTile {
			t.Fatalf("得到 %v，应当是格子", got)
		}
	})

	t.Run("后台收听：没有表面在画它", func(t *testing.T) {
		a := base()
		a.Router = ui.NewRouter(routeHome)
		if got := a.videoOwnerOf(key); got != videoNone {
			t.Fatalf("得到 %v，应当是没有人画", got)
		}
	})
}

// 过渡帧里格子不该报「缓冲中…」。
//
// 那一帧画面归舞台，格子拿到的 player 是 nil，而 sess.stalled 仍然是它
// 自己的历史值；照着报就会在正在缩小的格子上闪一次缓冲提示。
func TestWallTileDoesNotReportStallWhileStageOwnsVideo(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	sess := &roomSession{key: watchKey(room), room: room, stalled: true, loading: false}

	a := newApp()
	tt := ui.NewTester(func(c *ui.Context) {
		a.watchState(c, room, sess, nil, false)
	}, 320, 180)
	tt.Frame()
	if tt.HasText("缓冲中") {
		t.Error("画面归舞台时格子报了「缓冲中…」：缩小的格子上会闪一下假状态")
	}

	// 反过来：格子自己画的时候要报。
	tt2 := ui.NewTester(func(c *ui.Context) {
		a.watchState(c, room, sess, nil, true)
	}, 320, 180)
	tt2.Frame()
	if !tt2.HasText("缓冲中") {
		t.Error("格子自己在画、时间轴停住了，却没有报「缓冲中…」")
	}
}

// 右键卡片把直播间收进观测墙，走的是真菜单。
//
// 这条同时钉住 e2e 能不能驱动右键：automation 的 CLI 说"不要在系统菜单上
// 右键"，因为系统的弹出菜单不在这套自绘元素树里；MyGo 有
// `Context.SetMenuPresentation(ui.PopoverMenus)` 让窗口自己画菜单，条目于是
// 变成元素，读屏和 automation 都够得着（App.drawnMenus 就是那个开关）。
//
// Tester 里两种表现形式都能开菜单，所以这里是纯视图断言。
func TestCardContextMenuAddsToTheWall(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}

	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	a.Router = ui.NewRouter(routeHome)

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()

	if err := tt.RightClick("直播间:甲"); err != nil {
		t.Fatalf("右键卡片没开出菜单：%v", err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("悲鸣观测"); err != nil {
		t.Fatalf("菜单里没有「悲鸣观测」：%v", err)
	}
	tt.Frame()

	if len(a.WatchRooms) != 1 || watchKey(a.WatchRooms[0]) != watchKey(room) {
		t.Fatalf("收进墙的是 %+v，应当正好是这一间", a.WatchRooms)
	}
}

// 观测墙上这一格在出声时：格子亮着、岛说「停止观测」、双击放大走 Match。
//
// 这是「观测墙怎么跟岛交互」的三条判据，一起钉住。
func TestWallTileAndIslandAgree(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.WatchRooms = []Streamer{room}
	s := a.sessionFor(key, room)
	a.focus = s
	a.Router = ui.NewRouter(routeFollows)
	a.FollowsSection = followsSectionWatch

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()

	// 岛：这一路在出声，文案是「停止观测」。
	if got := a.islandCloseLabel(); got != "停止观测" {
		t.Errorf("墙上的 X 是 %q，应当是「停止观测」", got)
	}
	// 格子：同一间房是 active（读屏能听出哪一格在响）。
	if got := a.wallActiveKey(); got != key {
		t.Errorf("墙上标着 %q，应当是这一间", got)
	}
	if _, ok := tt.AccessByRole(ui.RoleListItem, watchTileLabel(room)); !ok {
		t.Errorf("墙上找不到这一格：读屏只看到 %d 个元素", len(tt.AccessAll()))
	}

	// 双击放大：墙格 → 播放页是硬切，和其它表面一样。
	a.watchOpenPlayer(room)
	if a.Router.Path() != routePlayer {
		t.Fatalf("双击没有进播放页，停在 %s", a.Router.Path())
	}
	if a.Router.Transition != ui.TransitionNone {
		t.Errorf("墙格 → 播放页排成了 %v：这条路一律硬切", a.Router.Transition)
	}
	if a.activityKey() != key {
		t.Error("进播放页之后这一路不再是活动了")
	}
}

// 后台收听不许缩渲染尺寸。
//
// 这一间是 Live Activity，岛随时可能被点开成整屏。缩到 160×90 省下的那点
// 渲染，代价是点开那一瞬间引擎在 mini 尺寸上补渲染一帧——那一帧被放大三倍
// 多，用户看到的是「放大 → 画面 → 模糊画面 → 显示画面」。
//
// 真窗口探针（读 player.Size()）抓到的序列：
//
//	block=288x38    bmp=1280x720   清楚
//	block=523x241   bmp=160x90     糊  ← 就是这一帧
//	block=711x404   bmp=1280x720   清楚
//
// 改后同一段：block 从 288 长到 1201，每一帧 bmp 都是 1280x720。
func TestBackgroundListeningKeepsRenderSize(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Router = ui.NewRouter(routeHome)
	s := a.sessionFor(key, room)
	a.focus = s

	// 前提：这是后台收听——有焦点，但没有任何全尺寸视图。
	if a.fullSize() {
		t.Fatal("前提不成立：这不是后台收听")
	}

	got := a.presentSizeForSession(s)
	if got.W != 0 || got.H != 0 {
		t.Errorf("后台收听把呈现尺寸压到了 %dx%d：点开那一瞬间会闪一下模糊", got.W, got.H)
	}

	// 墙格仍然要缩：一墙十几路，按原始尺寸渲染没必要。
	a.WatchRooms = []Streamer{room}
	a.FollowsSection = followsSectionWatch
	a.Router = ui.NewRouter(routeFollows)
	a.watch.rects[key] = watch.Rect{X: 0, Y: 0, W: 320, H: 180}
	if got := a.presentSizeForSession(s); got.W == 0 && got.H == 0 {
		t.Error("墙格没有缩尺寸：十几路按原始尺寸渲染")
	}
}

// 播放页里按 F 切全屏，不能把还没演完的表面过渡抽成硬跳。
//
// 全屏边沿和窗口 resize 一样会改变外壳的盒子，所以要拨观测点让 Match
// 别去做尺寸弹簧——但**不能顺手清掉 routeTransitionUntil**：那等于把
// 演到一半的进 / 出播放页 Match 直接切掉（「按 F 画面跳一下」）。
func TestFullscreenEdgeKeepsInFlightMatch(t *testing.T) {
	a := newApp()
	a.Platform = "bilibili"
	a.Router = ui.NewRouter(routeHome)
	a.Router.Push(routePlayer)
	a.focus = &roomSession{key: "bilibili|42", room: Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}}

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()

	// 过渡正在演。
	until := time.Now().Add(time.Hour)
	a.routeTransitionUntil = until

	// 全屏边沿。
	a.WinFull = true
	tt.Frame()

	if a.routeTransitionUntil.IsZero() {
		t.Fatal("全屏边沿把正在演的过渡清掉了：画面会从 Match 变硬跳")
	}
	if !a.routeTransitionUntil.Equal(until) {
		t.Errorf("过渡截止时刻被改写成 %v，应当保持 %v", a.routeTransitionUntil, until)
	}
}
