package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
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

// 收起态要缩成正圆：宽高都等于岛高，头像铺满内侧，时间和名字都不画。
// 用尺寸常量锁住，免得以后有人把收起宽度又改回首页眼睛岛的 100。
func TestPlayerIslandCollapsedIsCircle(t *testing.T) {
	if islandPlayerCollapsedW != theme.IslandH {
		t.Fatalf("收起宽 %v、高 %v：播放页的岛该是正圆", islandPlayerCollapsedW, theme.IslandH)
	}
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Move(700, 450)
	tt.Advance(5 * time.Second)
	if app.InfoOpen {
		t.Fatal("岛没有收起")
	}
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	// 弹簧收尾会留亚像素残差，0.1px 以内肉眼不可见
	if d := b.W - b.H; d < -0.1 || d > 0.1 {
		t.Fatalf("收起态 %vx%v，不是正圆", b.W, b.H)
	}
}

// 展开宽度是产品尺寸，400 太长会压住顶栏两端。用常量钉住，改这里必须先截图确认。
func TestPlayerIslandExpandedWidth(t *testing.T) {
	if islandPlayerExpandedW != 350 {
		t.Fatalf("展开宽 %v，期望 350", islandPlayerExpandedW)
	}
}

// 首页岛是 24 小时制数字时钟，每秒走一次。
// 用 Tester 的时钟推进，不 sleep；格式钉死 HH:MM:SS，避免退回 12 小时制。
func TestHomeIslandClock(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	initial := tt.Now().Format("15:04:05")
	if len(initial) != 8 || initial[2] != ':' || initial[5] != ':' {
		t.Fatalf("Tester 起始时间格式异常：%q", initial)
	}
	if !tt.HasText(initial) {
		t.Fatalf("初始时钟 = %q，期望 %q", tt.Texts(), initial)
	}
	tt.Advance(2 * time.Second)
	want := tt.Now().Format("15:04:05")
	if !tt.HasText(want) {
		t.Fatalf("走了两秒，时钟文本 = %q，期望 %q", tt.Texts(), want)
	}
}
