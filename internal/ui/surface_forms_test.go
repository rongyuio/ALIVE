package ui

import (
	"testing"
	"time"
)

// 岛的形态只有两种：时钟，或者「这一路」。
//
// 以前是四种（full / tile / listening / idle）。full 和 tile 在渲染上完全
// 一样——同一个宽度、同一棵内容树、同样的流光环，差别只有"点它回哪"，那是
// 点击处理该看表面决定的事；listening 曾经缩成一颗圆，后来整条去掉，后台
// 听也是同一条岛。多一种形态就多一处要同步维护的判据，而它没有任何可观察
// 的差别。
func TestIslandHasTwoForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		form islandForm
		want string
	}{
		{"时钟", islandIdle, "idle"},
		{"有焦点", islandOpen, "open"},
	} {
		if got := tc.form.String(); got != tc.want {
			t.Errorf("%s：String() = %q，期望 %q", tc.name, got, tc.want)
		}
	}
}

// 同一间房，在墙上是展开态，进了播放页还是展开态。
//
// 这条守的是"合并两种展开态"的语义：形态不随表面变，只随"有没有焦点、
// 焦点有没有可见视图"变。
func TestIslandFormIsStableAcrossWallAndPlayer(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	listenOnWall(t, tt, rooms[0])
	if got := app.islandFormOf(); got != islandOpen {
		t.Fatalf("墙上有焦点时岛是 %v，期望展开", got)
	}

	openTile(t, tt, rooms[0])
	if got := app.islandFormOf(); got != islandOpen {
		t.Fatalf("进播放页后岛是 %v，期望仍是展开", got)
	}
}

// 墙格的手势分两档：单击激活（换声音焦点，留在墙上），双击进播放页。
//
// 这条曾经反过来写过两次：先是单击激活、双击进播放页；后来改成单击进
// 播放页、双击整个删掉。现在按 ry 的要求回到两档——但"激活"必须真的出声、
// "打开"必须真的换路由，两者都不能只是看起来点到了。
func TestWallTileClickActivates(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	first := fakeSession(t, app, rooms[1])
	fakeSession(t, app, rooms[0])
	tt.Frame()

	if err := tt.Click(watchTileLabel(rooms[1])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := app.focusKey(); got != watchKey(rooms[1]) {
		t.Fatalf("单击之后焦点是 %q，期望 %q", got, watchKey(rooms[1]))
	}
	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("单击就离开了墙：route=%q", got)
	}
	if v := first.player.Volume(); v <= 0 {
		t.Fatalf("激活的那一格音量是 %v：单击没有真的出声", v)
	}
}

func TestWallTileDoubleClickOpensPlayer(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	doubleClick(t, tt, watchTileLabel(rooms[0]))
	tt.Frame()

	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("双击没有进播放页：route=%q", got)
	}
	if got := app.focusKey(); got != watchKey(rooms[0]) {
		t.Fatalf("双击之后焦点是 %q，期望 %q", got, watchKey(rooms[0]))
	}
}

// 展开态的岛上，X 的文案和动作按表面分：播放页退出整路，墙上只收声。
//
// 这条以前靠 islandFull / islandTile 两种形态区分，现在合并成一种之后，
// 区分挪到了点击处理里——更容易写错，所以要有测试盯着。
func TestIslandXFollowsSurface(t *testing.T) {
	t.Run("播放页：退出直播间", func(t *testing.T) {
		rooms := watchTestRooms(1)
		app, tt := newWatchMock(t, rooms)
		fakeSession(t, app, rooms[0])
		tt.Frame()
		openTile(t, tt, rooms[0])
		tt.Advance(time.Second)
		tt.Frame()

		if _, ok := tt.Find("退出直播间"); !ok {
			t.Fatalf("播放页的岛上没有「退出直播间」：texts=%q", tt.Texts())
		}
		if _, ok := tt.Find("取消观测发声"); ok {
			t.Fatal("播放页上出现了「取消观测发声」")
		}
	})

	t.Run("墙上：取消观测发声", func(t *testing.T) {
		rooms := watchTestRooms(1)
		app, tt := newWatchMock(t, rooms)
		fakeSession(t, app, rooms[0])
		tt.Frame()
		listenOnWall(t, tt, rooms[0])
		tt.Advance(time.Second)
		tt.Frame()

		if _, ok := tt.Find("取消观测发声"); !ok {
			t.Fatalf("墙上的岛上没有「取消观测发声」：texts=%q", tt.Texts())
		}
		if _, ok := tt.Find("退出直播间"); ok {
			t.Fatal("墙上出现了「退出直播间」：那会关掉整路，用户只是想不听")
		}
	})
}

// 展太态的岛整块是回这一路的热区——墙上和播放页都是。
func TestIslandOpenClickReturnsToFocus(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()
	listenOnWall(t, tt, rooms[0])
	tt.Advance(time.Second)
	tt.Frame()

	if _, ok := tt.Find("回到直播间"); !ok {
		t.Fatalf("墙上的展开岛没有回直播间的热区：texts=%q", tt.Texts())
	}
	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点岛回的是 %q，期望播放页", app.Router.Path())
	}
}
