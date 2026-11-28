package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 播放页与其它表面之间**不做过渡**，一律硬切。
//
// 2026-11-28 拆掉的三种接法（元素 Match / 整页摆位 / 飞行块）都要在两帧之间
// 对齐几何：谁持有身份、谁在飞、落点是哪一块，全靠状态机串起来，而它们互相踩
// ——「从墙上放大」「模糊画面闪一下」「岛和墙一起飞」都是这么来的。
//
// 现在这条路上只有一个动作：Router.Transition = TransitionNone，然后换路由。
// 这组判据守的就是「一点动画都没有」。

// 岛的呈现会按设计改宽度（播放页上回时钟），但它的元素身上永远不带
// ElementTransition —— 它每一帧都在构建，不是「正在出现的元素」。
func TestIslandNeverCarriesTransition(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	a.focus = &roomSession{key: key, room: room, loading: true}
	a.Router = ui.NewRouter(routeHome)

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Advance(600 * time.Millisecond)
	tt.Frame()

	assertIslandHasNoTransition(t, tt, "桌面上")

	a.openRoom(room)
	tt.Frame()
	assertIslandHasNoTransition(t, tt, "进播放页那一帧")
	if a.Router.Path() != routePlayer {
		t.Fatalf("点卡片没有进播放页，停在 %s", a.Router.Path())
	}

	a.collapsePlayer()
	tt.Frame()
	assertIslandHasNoTransition(t, tt, "收起那一帧")
}

// assertIslandHasNoTransition 断言岛这一刻没有挂共享身份。
func assertIslandHasNoTransition(t *testing.T, tt *ui.Tester, when string) {
	t.Helper()
	r, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatalf("%s找不到岛", when)
	}
	// 岛在每一帧都建得出来，且宽度不随路由跳变到「舞台那么大」。
	if r.W > 400 {
		t.Errorf("%s岛的宽是 %.0f：它被拉成舞台了", when, r.W)
	}
}

// 岛在播放页上是时钟（HIG：App 打开时它自己的 Live Activity 不该占着岛）。
//
// 退出钮与房间名跟着搬进播放页自己的顶栏（见 playerTopbar）。连带消失的是
// 「点岛收起」——iPhone 上根本没有这条手势，收起是 App 自己的返回。
func TestIslandIsClockOnPlayerPage(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	a.focus = &roomSession{key: key, room: room, loading: true}
	a.Router = ui.NewRouter(routeHome)

	// 桌面上：这一路在，岛是展开的。
	if got := a.islandFormOf(); got != islandOpen {
		t.Fatalf("桌面上岛是 %v，应当展开", got)
	}

	a.playerReturnRoute, a.playerReturnSection = routeHome, 0
	a.Router.Push(routePlayer)
	if got := a.islandFormOf(); got != islandIdle {
		t.Errorf("播放页上岛是 %v，应当回时钟", got)
	}

	// 屏幕上：岛是时钟，它里面不该有任何可点的热区。
	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()
	for _, name := range []string{"打开直播间", "关闭直播间", "停止观测"} {
		if _, ok := tt.AccessByRole(ui.RoleButton, name); ok {
			t.Errorf("播放页上岛上还有 %q 热区", name)
		}
	}
}

// 从每一块表面进播放页都是硬切：路由立刻换，页面过渡是 TransitionNone。
func TestEnterPlayerIsAHardCut(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	// 三种来源：首页卡片、观测墙墙格、岛。全都走 openRoom 这一个入口。
	cases := []struct {
		name string
		set  func(a *App)
	}{
		{"首页卡片", func(a *App) { a.Streamers = []Streamer{room} }},
		{"观测墙墙格", func(a *App) {
			a.WatchRooms = []Streamer{room}
			a.FollowsSection = followsSectionWatch
		}},
		{"岛", func(a *App) {}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newApp()
			a.Platform = room.Platform
			c.set(a)
			a.focus = &roomSession{key: key, room: room, loading: true}
			a.Router = ui.NewRouter(routeHome)

			tt := ui.NewTester(a.Shell, 1280, 800)
			tt.Frame()
			tt.Advance(600 * 1000000) // 600ms，让所有窗口都过期
			tt.Frame()

			a.openRoom(room)

			if a.Router.Path() != routePlayer {
				t.Fatalf("没有进播放页，停在 %s", a.Router.Path())
			}
			if a.Router.Transition != ui.TransitionNone {
				t.Errorf("进播放页排成了 %v：这条路不做页面过渡", a.Router.Transition)
			}
			// 第一帧就该是最终版面，不许有「摆位 / 飞行块」把它压在别处。
			tt.Frame()
			stage, ok := tt.Find("播放画面（双击全屏）")
			if !ok {
				t.Fatal("进播放页那一帧找不到舞台")
			}
			if stage.W < 400 {
				t.Errorf("舞台宽只有 %.0f：第一帧不是最终版面，还有动画在跑", stage.W)
			}
		})
	}
}

// 离开播放页同样是硬切，而且**当场**换路由。
func TestLeavePlayerIsAHardCut(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	a.focus = &roomSession{key: key, room: room, loading: true}
	a.Router = ui.NewRouter(routeHome)
	a.playerReturnRoute, a.playerReturnSection = routeHome, 0
	a.Router.Push(routePlayer)

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()

	a.collapsePlayer()

	if a.Router.Path() == routePlayer {
		t.Fatal("收起没有离开播放页：退场被推迟了")
	}
	if a.Router.Transition != ui.TransitionNone {
		t.Errorf("收起排成了 %v：这条路不做页面过渡", a.Router.Transition)
	}
	// 焦点留着（后台收听），岛继续显示这一路。
	if a.focus == nil {
		t.Error("收起把焦点清了：那是结束这一路")
	}
	if got := a.islandFormOf(); got != islandOpen {
		t.Errorf("收起之后岛是 %v，应当展开", got)
	}
}

// 结构性判据：三块表面的源码里都不许再挂 Match。
//
// 运行时断言只能看到「这一次没挂」，而 MyGo 的匹配器看的是上一帧谁带身份——
// 漏一处就是一个「两份画面一起动」。所以直接盯源码。
func TestNoSurfaceCarriesMatch(t *testing.T) {
	for _, f := range []string{"player.go", "watch.go", "island.go", "home.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读不到 %s：%v", f, err)
		}
		src := string(b)
		if strings.Contains(src, "Match:") {
			t.Errorf("%s 里还有 Match 身份：这条路一律硬切", f)
		}
	}
}
