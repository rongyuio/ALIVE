package ui

import (
	"fmt"
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

// 播放页的岛接的是主播信息：进直播间展开，展开态常驻——它同时承担
// 「正在看谁」和「声音从哪来」两件事，自己缩回去等于把状态藏起来。
func TestPlayerIslandStaysExpanded(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if !app.InfoOpen {
		t.Fatal("进直播间没有展开信息岛")
	}

	// 指针移开、闲置远超原来那 4 秒：仍然展开。
	tt.Move(700, 450)
	tt.Advance(10 * time.Second)
	if !app.InfoOpen {
		t.Fatal("闲置 10 秒之后信息岛收起了：展开态应当常驻")
	}

	// 宽度也是展开宽，不是收起那枚圆。
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	if b.W < islandPlayerMinW-1 {
		t.Fatalf("岛宽 %v，期望接近展开宽 %v", b.W, islandPlayerExpandedW)
	}
}

// 后台收听态：离开播放页之后岛缩成一枚圆形头像胶囊，点它回到那个直播间。
//
// 这是「不点叉就离开」之后唯一的路——声音还在放，但界面上得有东西说明
// 它在放、并且点得回去。
func TestListeningIslandReturnsToPlayer(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	// 切到订阅页：这是「离开但不退出」的一条路（顶栏 tab）
	if err := tt.Click("订阅"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() == routePlayer {
		t.Fatal("点了订阅页还停在播放页")
	}
	// 没有真播放器时后台收听不成立（media.player 为 nil），
	// 所以这里验的是「岛按后台收听态渲染」的那条分支本身。
	if !app.media.background {
		t.Skip("没有真实播放器，后台收听态起不来")
	}

	b, ok := tt.Find("回到直播间")
	if !ok {
		t.Fatal("后台收听态没有画出回到直播间的入口")
	}
	if d := b.W - b.H; d < -0.1 || d > 0.1 {
		t.Fatalf("后台收听胶囊 %vx%v，不是正圆", b.W, b.H)
	}
	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("点胶囊之后路由是 %s，期望 %s", got, routePlayer)
	}
}

// 收起宽度常量仍然锁在岛高：后台收听那颗圆胶囊用的就是它。
func TestPlayerIslandCollapsedIsCircle(t *testing.T) {
	if islandPlayerCollapsedW != theme.IslandH {
		t.Fatalf("收起宽 %v、高 %v：圆胶囊该是正圆", islandPlayerCollapsedW, theme.IslandH)
	}
}

// 展开宽度是产品尺寸，400 太长会压住顶栏两端。用常量钉住，改这里必须先截图确认。
func TestPlayerIslandExpandedWidth(t *testing.T) {
	if islandPlayerExpandedW != 350 {
		t.Fatalf("展开宽 %v，期望 350", islandPlayerExpandedW)
	}
}

// 首页岛是 24 小时制数字时钟，每秒走一次。
//
// 每位数字是独立元素（翻牌需要逐位裁剪），所以按字符验证：时分秒六位
// 都在文本树里，冒号两个。用 Tester 的时钟推进，不 sleep。
func TestHomeIslandClock(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	digits := func() string {
		h, m, s := tt.Now().Clock()
		return fmt.Sprintf("%02d%02d%02d", h, m, s)
	}
	texts := func() map[string]bool {
		set := map[string]bool{}
		for _, x := range tt.Texts() {
			set[x] = true
		}
		return set
	}
	if want := digits(); !texts()[want[:1]] {
		t.Fatalf("时钟缺小时十位 %q，文本树 = %v", want[:1], tt.Texts())
	}
	before := tt.Now().Format("15:04:05")
	tt.Advance(2 * time.Second)
	after := tt.Now().Format("15:04:05")
	if before == after {
		t.Fatalf("推进两秒后时间没变：%s", after)
	}
	// 秒的个位必须已经换成新值
	if want := after[7:]; !texts()[want] {
		t.Fatalf("秒个位应为 %q，文本树里没有", want)
	}
}

// 秒变化时只有秒的个位在翻，其余位保持静止——整排一起滚看起来像整块在抖。
// 用 Tester 的时钟逐帧采样，不 sleep、不截图。
//
// 采样必须按**位置**而不是按字符：时钟里出现重复数字（11:55:19 这种）时，
// 两个 `1` 是两个不同的元素，按字符聚合会把它们的位置混在一起，明明只有
// 一位在动也会被算成「多位在动」。这条以前是偶发失败，根因就在这儿。
func TestClockRollsOnlyChangedDigit(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	// 对齐到「整秒 + 950ms」，往后推必然跨过一次整秒
	now := tt.Now()
	tt.Advance(time.Second - now.Sub(now.Truncate(time.Second)) + 950*time.Millisecond)

	// 采样：按元素位置记 y。翻牌中的位 y 会离开静止值。
	type slot struct{ x, w float32 }
	settled := map[slot]float32{}
	moving := map[slot]bool{}
	for i := 0; i < 12; i++ {
		for _, a := range tt.AccessAll() {
			if len(a.Name) != 1 || a.Name[0] < '0' || a.Name[0] > '9' {
				continue
			}
			// 位置 = 左边界 + 宽度。x 相同、宽也相同的两个元素就是同一位；
			// 同一秒里重复的数字不会落在同一格上。
			k := slot{x: a.Bounds.X, w: a.Bounds.W}
			y := a.Bounds.Y
			if prev, ok := settled[k]; !ok {
				settled[k] = y
			} else if y != prev {
				moving[k] = true
			}
		}
		tt.Advance(25 * time.Millisecond)
	}
	if len(moving) == 0 {
		t.Fatal("跨了一整秒，没有任何一位数字在翻——翻牌动画没跑")
	}
	// 跨一秒只会让秒的个位换值；十位只在 9→0 时变，分位不动。
	// 所以参与翻牌的位置数应当很少（这里只要求「不是整排」）。
	if len(moving) > 4 {
		t.Fatalf("一次走秒有 %d 位数字在动（%v）：应该只有变化的那一位", len(moving), moving)
	}
}

// 切平台时，正在收听的直播间头像不能被当成「旧平台的纹理」清掉。
//
// 清掉的那一帧 islandAvatar 拿不到位图，退回文字占位；解码回来又换回头像，
// 屏幕上就是「闪一下」。keep 集合必须带上当前主播的图。
func TestSwitchTabKeepsNowPlayingAvatar(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{
		Platform:  "douyu",
		RoomID:    "9999",
		Name:      "主播",
		AvatarURL: "https://example.invalid/avatar.jpg",
		CoverURL:  "https://example.invalid/cover.jpg",
	}
	app.media.background = true

	keep := app.feedURLs("bilibili")
	if !keep["https://example.invalid/avatar.jpg"] {
		t.Fatal("切平台要保住正在收听主播的头像：否则岛会闪回文字占位")
	}
	if !keep["https://example.invalid/cover.jpg"] {
		t.Fatal("切平台要保住正在收听主播的封面")
	}
}

// 没在听任何东西时不必保留——keep 集合里不该凭空多出空串。
func TestFeedURLsWithoutPlayer(t *testing.T) {
	app := NewMockApp()
	keep := app.feedURLs("bilibili")
	if keep[""] {
		t.Fatal("keep 集合里出现了空 URL")
	}
}
