package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
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

// 静态守卫：墙格上不许再出现"单击换声音"。
//
// 单击 = 进这一间，全应用统一。如果哪天有人在 watchTile 里把 Clicked 接回
// watchActivate，这条会红——那个改法看起来很像"恢复原来的行为"，实际上是把
// 最贵的一类不一致放回去。
func TestWallTileClickOpensPlayerStatically(t *testing.T) {
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, "watch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(src, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "watchTile" {
			return true
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "watchActivate" {
				return true
			}
			t.Errorf("watch.go:%d watchTile 里调了 watchActivate：单击该进播放页，"+
				"换声音只走底栏的「在墙上听」", fset.Position(call.Pos()).Line)
			return true
		})
		return false
	})
	if !found {
		t.Fatal("找不到 watchTile：静态守卫失效了")
	}
}

// 双击这个手势在墙格上已经不存在了。
//
// 全应用只有墙格用过双击，删掉它之后"点一下"处处只有一个意思。
func TestWallTileHasNoDoubleClick(t *testing.T) {
	src, err := parser.ParseFile(token.NewFileSet(), "watch.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(src, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "DoubleClicked" {
			t.Errorf("watch.go 里还有 DoubleClicked：墙格的双击手势已经删了")
		}
		return true
	})
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
