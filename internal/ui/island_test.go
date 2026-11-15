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

// 观看时长要一直写到小时级：100 宽的岛留给数字只有 46px，13px 的 mm:ss
// 实宽 34px、12px 的 h:mm:ss 实宽 35.9px。格式一旦多出一段就不只是「挤
// 一点」，整行会被裁——所以两个档位的边界必须钉住。
func TestFormatWatch(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "00:00"},
		{9 * time.Second, "00:09"},
		{61 * time.Second, "01:01"},
		{59*time.Minute + 59*time.Second, "59:59"},
		{time.Hour, "1:00:00"},
		{time.Hour + time.Minute + time.Second, "1:01:01"},
		{12*time.Hour + 5*time.Minute + 21*time.Second, "12:05:21"},
		{24 * time.Hour, "24:00:00"},
	}
	for _, c := range cases {
		if got := formatWatch(c.d); got != c.want {
			t.Errorf("formatWatch(%v) = %q，想要 %q", c.d, got, c.want)
		}
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
