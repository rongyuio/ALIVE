package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 灵动岛在首页只报状态：点它不改路由、也不弹任何东西。
func TestIslandClickDoesNothing(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	before := app.Router.Path()
	if err := tt.Click("灵动岛"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := app.Router.Path(); got != before {
		t.Fatalf("点灵动岛把路由切到了 %s", got)
	}
}

// 播放页的岛接的是主播信息：进直播间自动展开，指针不在岛上、闲置 4 秒
// 之后收成小胶囊，点一下再展开；指针停在岛上时一直亮着，不计时。
func TestPlayerIslandAutoCollapses(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if !app.InfoOpen {
		t.Fatal("进直播间没有展开信息岛")
	}

	tt.Move(700, 450)
	tt.Advance(5 * time.Second)
	if app.InfoOpen {
		t.Fatal("闲置 4 秒之后信息岛没有收起")
	}

	// 收起态整块可点，点岛中心就能再展开。这里只能按坐标点：
	// 收起态压着一层透明热区，按 Label 找「主播信息」会落到它下面去。
	island, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	tt.Press(island.X+island.W/2, island.Y+island.H/2)
	tt.Release(island.X+island.W/2, island.Y+island.H/2)
	tt.Frame()
	if !app.InfoOpen {
		t.Fatal("点信息岛没有展开")
	}

	// 指针停在岛上：一直亮着
	tt.Move(720, 30)
	tt.Advance(6 * time.Second)
	if !app.InfoOpen {
		t.Fatal("指针停在岛上时不该收起")
	}

	// 移开之后重新数满 4 秒
	tt.Move(700, 450)
	tt.Advance(5 * time.Second)
	if app.InfoOpen {
		t.Fatal("指针移开 4 秒之后没有收起")
	}
}
