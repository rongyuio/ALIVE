package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 悬停才显示是对的，悬停才创建是错的。
//
// 只在 hover 时构建的按钮在无障碍树里根本不存在：读屏用户永远找不到「关注」；
// 而且指针扫过左上角的那一瞬间元素树变了，按下与抬起可能落在两个不同节点上，
// 那一下点击就丢了。MyGo 的写法是常驻 + Opacity(0) + IgnoreInput(!可见)
// （third_party/mygo/docs/ui/input.md「A button that shows while its row is
// hovered」）。
func TestCardFollowButtonExistsAtRest(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	room := app.Streamers[0]
	want := "关注:" + room.Name

	// 1) 常态（没 hover）：无障碍树里必须找得到它。
	found := false
	for _, n := range tt.AccessAll() {
		if n.Name == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("常态下无障碍树里没有 %q：读屏用户找不到「关注」", want)
	}

	// 2) 常态下它不可见，指针要能穿过去点到卡片本身。
	//    点左上角那颗星的位置：应当打开直播间，而不是切换关注。
	card, ok := tt.Find("直播间:" + room.Name)
	if !ok {
		t.Fatal("找不到卡片")
	}
	before := app.isFollowed(room)
	tt.ClickAt(card.X+8+16, card.Y+8+16) // 关注键圆心：Left 8 + Size 32 的一半
	tt.Frame()
	if app.isFollowed(room) != before {
		t.Fatal("常态下点了左上角，却切换了关注：那颗按钮没有 IgnoreInput")
	}
	if app.Router.Path() != routePlayer {
		t.Fatalf("常态下点左上角没有打开直播间：路由 %q", app.Router.Path())
	}
}

// 悬停之后那颗星真的能点，而且点它只切换关注、不打开直播间。
func TestCardFollowButtonWorksWhenHovered(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	room := app.Streamers[0]
	card, ok := tt.Find("直播间:" + room.Name)
	if !ok {
		t.Fatal("找不到卡片")
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	tt.Advance(50 * time.Millisecond) // 等透明度的入场动效起步
	tt.Frame()

	before := app.isFollowed(room)
	if err := tt.Click("关注:" + room.Name); err != nil {
		t.Fatalf("悬停后点不到关注键: %v", err)
	}
	tt.Frame()
	if app.isFollowed(room) == before {
		t.Fatal("点了关注键，关注状态没变")
	}
	if app.Router.Path() == routePlayer {
		t.Fatal("点关注键把直播间也打开了")
	}
}

// 观测墙底栏：常态也必须**构建**（只是不可见 + 不接输入）。
//
// 墙格本身是一枚 Button（`观测:B站 …`），MyGo 的无障碍树不往下展开按钮的
// 子元素，所以这里不能用 AccessAll 断言——用 Find（按 Label 找元素，不看
// 无障碍树）证明它真的在树上，再用一次点击证明常态下它不接输入。
func TestWatchBarBuiltAtRestButInert(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	// 1) 常态：底栏控件已经在树上。
	if _, ok := tt.Find("在墙上听 " + rooms[0].Name); !ok {
		t.Fatalf("常态下底栏没有构建：%q 找不到（hover 才创建 = 读屏拿不到、点击会丢）",
			"在墙上听 "+rooms[0].Name)
	}

	// 2) 常态：点在它所在的位置上，不能触发它——要落到格子上（激活这一格）。
	bar, _ := tt.Find("在墙上听 " + rooms[0].Name)
	tt.ClickAt(bar.X+bar.W/2, bar.Y+bar.H/2)
	tt.Frame()
	if app.focusKey() != watchKey(rooms[0]) {
		t.Fatalf("常态下点底栏位置没有激活这一格：焦点 %q（底栏没有 IgnoreInput）", app.focusKey())
	}
}
