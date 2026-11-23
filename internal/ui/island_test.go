package ui

import (
	"fmt"
	"strings"
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

	// 宽度也必须是展开态，不是收起那枚圆。
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	if b.W < islandPlayerMinW-1 {
		t.Fatalf("岛宽 %v，比最小展开宽 %v 还窄", b.W, islandPlayerMinW)
	}
}

// islandWidth 按给定的主播与窗口宽度渲染一帧播放页，返回灵动岛的宽度。
//
// 状态在建 Tester 之前就摆好：岛的宽度是 AnimateTo 从目标值起步的，
// 先摆状态再建视图，拿到的是终值本身，不必再推进时钟等弹簧停下。
func islandWidth(t *testing.T, s Streamer, winW int) float32 {
	t.Helper()
	app := NewMockApp()
	app.Player = s
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, winW, 900)
	tt.Frame()
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	return b.W
}

// 直播间标题有长有短：岛宽跟着内容走，短标题收窄，长标题撑开。
//
// 固定的 350 在这个场景里会同时给两种标题一样宽——短标题右边空一大截，
// 长标题又要靠省略号硬裁。宽度必须是量出来的，不是写死的。
func TestPlayerIslandWidthFollowsContent(t *testing.T) {
	base := Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	short := base
	short.Title = "短"
	long := base
	long.Title = "这是一个明显更长的直播间标题"

	shortW := islandWidth(t, short, 1440)
	longW := islandWidth(t, long, 1440)
	if longW <= shortW {
		t.Fatalf("长标题岛宽 %v、短标题 %v：宽度没有跟着内容走", longW, shortW)
	}
	if shortW >= islandPlayerMaxW-1 {
		t.Fatalf("短标题岛宽 %v 顶到了上限 %v：没有按内容收窄", shortW, islandPlayerMaxW)
	}
}

// 内容再短也不能缩成一颗胶囊，再长也不能把顶栏两端盖掉。
func TestPlayerIslandWidthClamped(t *testing.T) {
	tiny := Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	if w := islandWidth(t, tiny, 1440); w < islandPlayerMinW-0.5 {
		t.Fatalf("极短内容的岛宽 %v 小于下限 %v", w, islandPlayerMinW)
	}

	huge := Streamer{
		Platform: "douyu",
		RoomID:   "1",
		Name:     "甲",
		Title:    strings.Repeat("很长很长的直播间标题", 20),
	}
	if w := islandWidth(t, huge, 1440); w > islandPlayerMaxW+0.5 {
		t.Fatalf("超长内容的岛宽 %v 超过上限 %v", w, islandPlayerMaxW)
	}
	if w := islandWidth(t, huge, 600); w > float32(600)*0.5+0.5 {
		t.Fatalf("窄窗口里岛宽 %v 超过了窗口一半", w)
	}
}

// 后台收听态：离开播放页之后岛缩成一枚圆形头像胶囊，点它回到那个直播间。
//
// 这是「不点叉就离开」之后唯一的路——声音还在放，但界面上得有东西说明
// 它在放、并且点得回去。
// 后台收听：离开播放页之后岛还是那一条，不缩成一颗圆。
//
// 点它回到这一间——热区写着「回到直播间」，不管这一间在不在观测盒里。
// 以前在盒里会改道去墙，用户点岛想回来，结果落在一面没有这间的墙上。
func TestBackgroundIslandStaysAPill(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(1))
	room := app.WatchRooms[0]
	fakeSession(t, app, room)
	tt.Frame()
	openTile(t, tt, room)
	if app.Router.Path() != routePlayer {
		t.Fatalf("没进播放页：%q", app.Router.Path())
	}

	app.Router.Push(routeHome)
	app.detachMedia()
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	b, ok := tt.Find("回到直播间")
	if !ok {
		t.Fatalf("后台收听没有回到直播间的入口：texts=%q", tt.Texts())
	}
	if b.W < islandPlayerMinW-1 {
		t.Fatalf("后台收听的岛宽 %v，比下限 %v 还窄：缩成圆了", b.W, islandPlayerMinW)
	}
	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("点岛之后路由是 %s，期望回播放页 %s", got, routePlayer)
	}
	if watchKey(app.Player) != watchKey(room) {
		t.Fatalf("点岛打开的是 %q，期望 %q", watchKey(app.Player), watchKey(room))
	}
}

// absInt 取绝对值。
func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// 岛宽由文字宽度算出：取较宽的那行，加一圈固定开销与缓冲，再钳进范围。
//
// 关键的一行是「贴着旧上限」那条：文字宽 262 时，旧实现被 350 卡住，
// 画出来正好把标题末端裁掉——同一标题于是有了完整和省略号两种状态。
func TestPlayerIslandWidthFromText(t *testing.T) {
	cases := []struct {
		name          string
		titleW, metaW float32
		winW, want    float32
	}{
		// body 从岛沿往里 2px（让给环带），宽度公式因此比环带 1px 那版多 2。
		{"内容极短抬到下限", 10, 10, 1440, islandPlayerMinW},
		{"按较宽的那行算", 100, 60, 1440, 198},
		{"第二行更宽时按第二行", 50, 200, 1440, 298},
		{"贴着旧上限也不裁字", 260, 100, 1440, 358},
		{"超过上限", 400, 100, 1440, islandPlayerMaxW},
		{"窄窗口按一半", 400, 100, 600, 300},
		{"窗口再窄也保住下限", 10, 10, 200, islandPlayerMinW},
		{"墙上 active 多一颗取消按钮", 100, 60, 1440, 234},
	}
	for _, tc := range cases {
		buttonsW := islandPlayerFollowW
		if tc.name == "墙上 active 多一颗取消按钮" {
			buttonsW = islandButtonsW(true)
		}
		if got := islandPlayerWidth(tc.titleW, tc.metaW, tc.winW, buttonsW); got != tc.want {
			t.Errorf("%s：宽 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 上限是产品尺寸，360 再往上会压住顶栏两端。用常量钉住，改这里必须先截图确认。
//
// 360 而不是 350：文字量出来的宽度就是排版宽度，画的时候亚像素取整还会
// 上下浮动一两像素。上限正好卡在标题末端时，同一个标题会在「完整」和
// 「省略号」之间来回跳——抬高这 10px 就是给那一刻留的缓冲。
func TestPlayerIslandMaxWidth(t *testing.T) {
	if islandPlayerMaxW != 360 {
		t.Fatalf("展开上限 %v，期望 360", islandPlayerMaxW)
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

// 墙上 active 时，岛右侧的收藏键必须自己收到点击，不能被我整块热区吃掉。
//
// 热区是「回到直播间」，它铺满内容区；上一版把它画在所有按钮之后，
// MyGo 的命中链只取最上层元素，于是点收藏键等于点整块岛——直接跳进
// 播放页，关注状态一点没变。
func TestIslandWatchActiveFollowButtonClicks(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(2))
	room := app.WatchRooms[1]
	app.WatchActive = watchKey(room)
	app.Player = room
	app.Follows = []FollowRecord{{
		Platform: room.Platform, RoomID: room.RoomID,
		Name: room.Name, Title: room.Title,
	}}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	if _, ok := tt.Find("取消关注"); !ok {
		t.Fatalf("墙上 active 的岛没有关注键：texts=%q", tt.Texts())
	}
	if err := tt.Click("取消关注"); err != nil {
		t.Fatalf("关注键点不到：%v", err)
	}
	tt.Frame()
	if app.isFollowed(room) {
		t.Fatal("点了取消关注，记录还在")
	}
	if app.Router.Path() != routeFollows {
		t.Fatalf("点关注键跳到了 %q", app.Router.Path())
	}
}

// 墙上 active 时，收藏右边那颗按钮取消 active：声音收掉、岛回到时钟，
// 但不离开这一屏，也不把这一间从墙上移出去。
func TestIslandWatchActiveCancelButton(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(2))
	room := app.WatchRooms[0]
	app.WatchActive = watchKey(room)
	app.Player = room
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	if _, ok := tt.Find("取消观测发声"); !ok {
		t.Fatalf("墙上 active 的岛没有取消按钮：texts=%q", tt.Texts())
	}
	if err := tt.Click("取消观测发声"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.WatchActive != "" {
		t.Fatalf("取消后 active=%q", app.WatchActive)
	}
	if app.Router.Path() != routeFollows {
		t.Fatalf("取消 active 离开了观测墙：route=%q", app.Router.Path())
	}
	if len(app.WatchRooms) != 2 {
		t.Fatalf("取消 active 动了观测盒：%+v", app.WatchRooms)
	}
	tt.Advance(time.Second)
	tt.Frame()
	if r, ok := tt.Find("灵动岛"); !ok || r.W < 90 {
		t.Fatalf("取消 active 后岛没有回到时钟：%+v ok=%v", r, ok)
	}
}

// 点岛进的是 active 那一间，不是 a.Player 里碰巧剩下的别的房间。
func TestIslandWatchActiveClickOpensActiveRoom(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(2))
	room := app.WatchRooms[1]
	app.WatchActive = watchKey(room)
	app.Player = app.WatchRooms[0] // 目标被别处改掉了
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("点岛没有进播放页：route=%q", app.Router.Path())
	}
	if watchKey(app.Player) != watchKey(room) {
		t.Fatalf("进的是 %q，期望 active 那一间 %q", watchKey(app.Player), watchKey(room))
	}
}

// 在线绿不随日夜变：浅色主题里岛上那颗点也必须是深色主题那一支。
func TestLiveDotIgnoresThemeMode(t *testing.T) {
	dark := theme.Find("inkwhite-night").StatusOnline
	count := func(tt *ui.Tester) int {
		img := tt.Image()
		n := 0
		for y := 0; y < 60; y++ {
			for x := 600; x < 840; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if absInt(int(r>>8)-int(dark.R)) < 26 &&
					absInt(int(g>>8)-int(dark.G)) < 26 &&
					absInt(int(b>>8)-int(dark.B)) < 26 {
					n++
				}
			}
		}
		return n
	}
	for _, tc := range []struct{ palette, mode string }{
		{"inkwhite-night", themeModeDark},
		{"inkwhite-day", themeModeLight},
	} {
		app := NewMockApp()
		app.PaletteID = tc.palette
		app.ThemeMode = tc.mode
		app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
		app.Router.Push(routePlayer)
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.Frame()
		if n := count(tt); n == 0 {
			t.Errorf("%s: 岛上没有深色主题的在线绿", tc.palette)
		}
	}
}

// 播放页的退出 X 长在灵动岛上：和墙上 active 那颗同一个位置、同一颗按钮，
// 动作按表面走——播放页退出整路并退回上一页，墙上只收声。
//
// 非全屏时画面左上角不再有第二颗 X：退出只在这一处。
func TestIslandPlayerExitButton(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("双击格子没进播放页，route=%q", app.Router.Path())
	}

	island, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatalf("播放页没有灵动岛：texts=%q", tt.Texts())
	}
	x, ok := tt.Find("退出直播间")
	if !ok {
		t.Fatalf("播放页的岛上没有退出 X：texts=%q", tt.Texts())
	}
	if x.X < island.X || x.X+x.W > island.X+island.W ||
		x.Y < island.Y || x.Y+x.H > island.Y+island.H {
		t.Fatalf("退出 X 不在岛上：X=%+v，岛=%+v", x, island)
	}

	if err := tt.Click("退出直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routeFollows {
		t.Fatalf("点岛上的 X 没回上一页：route=%q", app.Router.Path())
	}
	if len(app.WatchRooms) != 1 {
		t.Fatalf("退出直播间动了观测盒：%+v", app.WatchRooms)
	}
}

// 墙上 active 时右侧两颗按钮的顺序：收藏在左，取消发声在最右。
func TestIslandFollowLeftOfCancel(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(2))
	room := app.WatchRooms[0]
	app.WatchActive = watchKey(room)
	app.Player = room
	app.Follows = []FollowRecord{{
		Platform: room.Platform, RoomID: room.RoomID, Name: room.Name,
	}}
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	star, ok := tt.Find("取消关注")
	if !ok {
		t.Fatalf("没有关注键：texts=%q", tt.Texts())
	}
	cancel, ok := tt.Find("取消观测发声")
	if !ok {
		t.Fatalf("没有取消发声键：texts=%q", tt.Texts())
	}
	if star.X > cancel.X {
		t.Fatalf("收藏在右、取消在左：收藏 X=%.1f，取消 X=%.1f", star.X, cancel.X)
	}
}
