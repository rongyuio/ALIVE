package ui

// 一条功能一次点击：点下去，看屏幕上有没有。
//
// 判据只有 tt.Click / tt.Find / tt.HasText / tt.Image。断言里不出现 app 的
// 内部字段——那种断言点不到真实路径，永远是绿的。
//
// 每条的"应该看到什么"来自 2026-11-26 的实测侦察（点一下，看点击前后屏幕
// 文本的差集），不是我的推测。

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// ---------------------------------------------------------------- 骨架

func home(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	return app, tt
}

// inPlayer 从首页点第一张卡片进播放页，并等飞行动画走完。
func inPlayer(t *testing.T, app *App, tt *ui.Tester) {
	t.Helper()
	if err := tt.Click(cardLabels(tt)[0]); err != nil {
		t.Fatalf("点第一张卡片失败：%v", err)
	}
	tt.Frame()
	tt.Advance(400e6)
	tt.Frame()
	mustFind(t, tt, "播放画面（双击全屏）")
}

func cardLabels(tt *ui.Tester) []string {
	var out []string
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "直播间:") {
			out = append(out, s)
		}
	}
	return out
}

func mustFind(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if _, ok := tt.Find(label); !ok {
		t.Fatalf("%q 不在屏幕上。可见文本：%v", label, tt.Texts())
	}
}

func mustNotFind(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if _, ok := tt.Find(label); ok {
		t.Fatalf("%q 还在屏幕上。可见文本：%v", label, tt.Texts())
	}
}

// volumeNow 读音量滑块的 aria-valuenow —— 控件的无障碍值本身，
// 不是屏幕上的那段 "60%" 文本。
//
// 这里曾经用 HasText("0%")：它是子串匹配，"60%" 里也含 "0%"，两边都命中，
// 于是取消静音之后音量明明是 60%，断言还是红的。真正的问题是那个音量条
// 当时只是一颗 Label 叫「音量」的普通 Box——无障碍树里 role 是分组、
// value 是空，读屏念不出音量，方向键也调不动。修的是控件（RoleSlider +
// Range + Step + 方向键），不是断言。
func volumeNow(t *testing.T, tt *ui.Tester) float64 {
	t.Helper()
	v, ok := tt.AccessByRole(ui.RoleSlider, "音量")
	if !ok {
		var names []string
		for _, a := range tt.AccessAll() {
			names = append(names, fmt.Sprintf("%v:%q", a.Role, a.Name))
		}
		t.Fatalf("无障碍树里没有音量滑块。现有节点：%v", names)
	}
	return v.Now
}

// tabSelected 报告某个 tab 是不是当前选中的那一格：滑块铺在它上面，
// 所以取它左沿往里 8px 的底色，和 Accent 比。
func tabSelected(t *testing.T, tt *ui.Tester, label string) bool {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		return false
	}
	p := tt.Image().RGBAAt(int(r.X)+8, int(r.Y+r.H/2))
	got := ui.RGB(p.R, p.G, p.B)
	want := theme.Accent
	return got.R == want.R && got.G == want.G && got.B == want.B
}

// mustHover 把指针移到某个元素中心。悬停才开的东西（下拉菜单）用它。
func mustHover(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q 不在屏幕上，移不过去。可见文本：%v", label, tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
}

func mustClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatalf("点不到 %q：%v", label, err)
	}
	tt.Frame()
}

// ---------------------------------------------------------------- 浏览

func TestFeatureHomeHasCards(t *testing.T) {
	_, tt := home(t)
	if n := len(cardLabels(tt)); n == 0 {
		t.Fatalf("首页没有卡片。可见文本：%v", tt.Texts())
	}
}

func TestFeatureGroupSwap(t *testing.T) {
	_, tt := home(t)
	before := cardLabels(tt)

	mustClick(t, tt, "分区:网游")

	after := cardLabels(tt)
	if len(after) == 0 {
		t.Fatal("切到「网游」之后列表空了")
	}
	if strings.Join(before, "|") == strings.Join(after, "|") {
		t.Fatalf("切一级分区后卡片没变：%v", after)
	}
}

func TestFeatureCategorySwap(t *testing.T) {
	_, tt := home(t)
	before := cardLabels(tt)

	mustClick(t, tt, "分类:CS2")

	after := cardLabels(tt)
	if len(after) == 0 {
		t.Fatal("切到 CS2 之后列表空了")
	}
	if strings.Join(before, "|") == strings.Join(after, "|") {
		t.Fatalf("切二级分类后卡片没变：%v", after)
	}
}

func TestFeatureSubCategorySwap(t *testing.T) {
	_, tt := home(t)
	before := cardLabels(tt)

	mustClick(t, tt, "子分类:LPL")

	after := cardLabels(tt)
	if strings.Join(before, "|") == strings.Join(after, "|") {
		t.Fatalf("切三级子分区后卡片没变：%v", after)
	}
}

func TestFeatureSubscribeToggle(t *testing.T) {
	_, tt := home(t)
	mustFind(t, tt, "订阅分区")

	mustClick(t, tt, "订阅分区")

	mustFind(t, tt, "已订阅")
}

// 切平台：顶栏 tab 换一家，卡片换一批。
//
// 平台切换要等目标平台的首屏就绪（上限 700ms），这段等待走的是真实时钟
// （tickPlatformSwitch 里的 time.Since），tt.Advance 推的虚拟时钟推不动它。
// 所以这里用 waitFor 等条件成立——等的是「卡片变了」，不是「睡够了」。
func TestFeaturePlatformSwitch(t *testing.T) {
	app, tt := home(t)
	before := cardLabels(tt)
	if len(before) == 0 {
		t.Fatal("首页没有卡片")
	}

	mustClick(t, tt, "虎牙")

	waitFor(t, func() bool {
		tt.Frame()
		after := cardLabels(tt)
		return len(after) > 0 && strings.Join(after, "|") != strings.Join(before, "|")
	})
	// 选中态也跟着走：滑块在虎牙那一格，不在斗鱼。
	// 滑块是弹簧（theme.Slide），推虚拟时钟让它走完；平台切换本身走的是
	// 真实时钟，所以上面等的是条件，这里等的是动画。
	tt.Advance(400e6)
	tt.Frame()
	if !tabSelected(t, tt, "虎牙") {
		t.Fatal("切到虎牙之后，tab 的选中态没跟着走")
	}
	if tabSelected(t, tt, "斗鱼") {
		t.Fatal("切到虎牙之后，斗鱼那格还亮着：滑块没走")
	}
	_ = app
}

func TestFeatureSearchOpens(t *testing.T) {
	_, tt := home(t)
	mustClick(t, tt, "搜索")
	mustFind(t, tt, "搜索直播间")
}

// 切订阅项：卡片真的换成那一项的内容。
//
// 这条以前断言 app.followsSeq +1——私有计数器，重构一动就红、行为坏了却
// 不一定红。换成卡片列表：切过去之后屏幕上那几张必须是新的。
func TestFeatureSubscribeItemSwitchChangesCards(t *testing.T) {
	app, tt := home(t)
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "无畏契约", CategoryID: "2"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt.Frame()

	before := cardLabels(tt)
	if len(before) == 0 {
		t.Fatalf("订阅页没有卡片。可见文本：%v", tt.Texts())
	}

	mustClick(t, tt, "订阅分区:无畏契约")

	after := cardLabels(tt)
	if len(after) == 0 {
		t.Fatalf("切到另一项之后没有卡片：%v", tt.Texts())
	}
	if strings.Join(after, "|") == strings.Join(before, "|") {
		t.Fatalf("切订阅项之后卡片没变：%v", after)
	}
}

// 切一级分区（关注主播 ↔ 订阅分区）：内容区真的换了。
func TestFeatureFollowsSectionSwitchChangesContent(t *testing.T) {
	app, tt := home(t)
	app.switchTab("follows")
	tt.Frame()
	mustFind(t, tt, "搜索已关注的主播")

	// 没有订阅时，切过去是空态——但内容确实换了。
	mustClick(t, tt, "订阅分区")

	mustNotFind(t, tt, "搜索已关注的主播")
	mustFind(t, tt, "还没有订阅任何分区")
}

// ---------------------------------------------------------------- 进出播放页

func TestFeatureOpenRoomShowsTheStage(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)
}

func TestFeatureEscReturnsToList(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	before := cardLabels(tt)
	if len(before) != 0 {
		t.Fatalf("播放页上不该有卡片：%v", before)
	}

	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	mustNotFind(t, tt, "播放画面（双击全屏）")
	if n := len(cardLabels(tt)); n == 0 {
		t.Fatalf("Esc 之后回到的页面上没有卡片：%v", tt.Texts())
	}
}

func TestFeatureCloseReturnsToList(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	mustClick(t, tt, "退出直播间")

	mustNotFind(t, tt, "播放画面（双击全屏）")
	if n := len(cardLabels(tt)); n == 0 {
		t.Fatalf("X 之后回到的页面上没有卡片：%v", tt.Texts())
	}
}

func TestFeatureDoubleClickGoesFullscreen(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	stage, ok := tt.Find("播放画面（双击全屏）")
	if !ok {
		t.Fatal("找不到画面")
	}
	cx, cy := stage.X+stage.W/2, stage.Y+stage.H/2
	tt.ClickAt(cx, cy)
	tt.Frame()
	tt.ClickAt(cx, cy)
	tt.Frame()

	// 全屏后顶栏让位给画面，控制条的按钮变成「退出窗口全屏」。
	mustFind(t, tt, "退出窗口全屏")
	mustNotFind(t, tt, "窗口全屏")
}

// 过渡：点卡片进播放页时有一段飞行块，走完就消失；X 关闭时没有。
//
// 飞行块有 Label「表面过渡中的画面」，所以「有没有动画」本身就有可见判据。
// 这比断言 app.surfaceFlight.kind == flightEnter 结实：那个字段叫什么都不
// 影响用户看到什么，而这块画面在不在，用户一眼就知道。
//
// X 那条是你 2026-11-26 明确定的策略：明确关掉一个直播间，不要收尾动画。
func TestFeatureEnteringARoomAnimates(t *testing.T) {
	_, tt := home(t)
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("还没点，屏幕上就有飞行块")
	}

	mustClick(t, tt, "直播间:旭旭宝宝")
	// 点完同一拍就该在飞。
	if _, ok := tt.Find("表面过渡中的画面"); !ok {
		t.Fatalf("进播放页没有飞行块。可见文本：%v", tt.Texts())
	}

	// 走完（280ms + 余量）就该消失，否则它会一直盖着画面。
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("飞行块走完之后还留在屏幕上")
	}
}

func TestFeatureClosingARoomDoesNotAnimate(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	mustClick(t, tt, "退出直播间")

	// X 是明确关闭：不留收尾动画。
	if _, ok := tt.Find("表面过渡中的画面"); ok {
		t.Fatal("按 X 关闭还安排了收尾动画")
	}
	mustNotFind(t, tt, "播放画面（双击全屏）")
}

// ---------------------------------------------------------------- 播放控制

func TestFeaturePause(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)
	mustFind(t, tt, "暂停")

	mustClick(t, tt, "暂停")

	mustFind(t, tt, "播放")
	mustNotFind(t, tt, "暂停")
}

func TestFeatureMuteKeepsTheVolume(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	before := volumeNow(t, tt)

	mustClick(t, tt, "静音")
	mustFind(t, tt, "取消静音")
	if got := volumeNow(t, tt); got != 0 {
		t.Fatalf("静音之后 aria-valuenow 是 %.0f，不是 0", got)
	}

	// 取消静音要把音量还回去，不是留在 0%。
	mustClick(t, tt, "取消静音")
	mustFind(t, tt, "静音")
	if got := volumeNow(t, tt); got != before {
		t.Fatalf("取消静音之后音量是 %.0f，静音前是 %.0f：静音把原音量丢了", got, before)
	}
}

// 音量滑块的无障碍通路：读屏念得出值、Tab 停得上、方向键真的调。
// 这三件缺一条，读屏用户就只知道"这儿有个叫音量的东西"，其余全靠猜。
func TestFeatureVolumeSliderIsAdjustableWithoutAPointer(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	first := volumeNow(t, tt)

	// Tab 要能停在滑块上。
	reached := false
	for i := 0; i < 30; i++ {
		tt.Key(0, ui.KeyTab)
		tt.Frame()
		if tt.Focused("音量") {
			reached = true
			break
		}
	}
	if !reached {
		t.Fatal("Tab 停不到音量滑块上：读屏和键盘都够不着它")
	}

	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if got := volumeNow(t, tt); got == first {
		t.Fatalf("在滑块上按 → 音量没变（还是 %.0f）", got)
	}

	tt.Key(0, ui.KeyLeft)
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if got := volumeNow(t, tt); got >= first {
		t.Fatalf("按 ← ← 之后音量是 %.0f，不比 %.0f 小", got, first)
	}
}

// ---------------------------------------------------------------- 弹幕

func TestFeatureDanmuToggle(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)
	mustFind(t, tt, "关闭弹幕")

	mustClick(t, tt, "关闭弹幕")

	mustFind(t, tt, "开启弹幕")
	mustNotFind(t, tt, "关闭弹幕")
}

func TestFeatureDanmuSettingsOpens(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	mustClick(t, tt, "弹幕设置")

	mustFind(t, tt, "彩色弹幕")
	mustFind(t, tt, "描边")
}

func TestFeatureDanmuFilterOpens(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)

	mustClick(t, tt, "弹幕屏蔽")

	mustFind(t, tt, "屏蔽词")
}

func TestFeatureDanmuPanelCollapse(t *testing.T) {
	_, tt := home(t)
	inPlayer(t, nil, tt)
	mustFind(t, tt, "弹幕列表")

	mustClick(t, tt, "收起弹幕")

	mustFind(t, tt, "展开弹幕")
	mustNotFind(t, tt, "弹幕列表")
}

// ---------------------------------------------------------------- 顶栏面板

func TestFeaturePalettePanelOpens(t *testing.T) {
	_, tt := home(t)

	mustClick(t, tt, "配色面板")

	mustFind(t, tt, "主题配色")
}

func TestFeatureSyncPanelOpens(t *testing.T) {
	_, tt := home(t)

	mustClick(t, tt, "数据同步")

	mustFind(t, tt, "保存到文件…")
}

// ---------------------------------------------------------------- 关注

// 卡片左上角的关注键：常态透明且不接输入（点击要穿透给卡片），
// 悬停之后才可点。这条同时钉住两件事——常态不挡点击，悬停后能用。
func TestFeatureFollowNeedsHover(t *testing.T) {
	app, tt := home(t)
	room := app.Streamers[0]
	label := "关注:" + room.Name
	mustFind(t, tt, label)

	card, ok := tt.Find("直播间:" + room.Name)
	if !ok {
		t.Fatal("找不到卡片")
	}
	// 常态：点关注键的位置，应当打开直播间（按钮让路）。
	st := app.isFollowed(room)
	tt.ClickAt(card.X+8+16, card.Y+8+16)
	tt.Frame()
	if app.isFollowed(room) != st {
		t.Fatal("常态下点左上角却切换了关注：按钮没有让路给卡片")
	}
	mustFind(t, tt, "播放画面（双击全屏）")

	// 回到列表，悬停卡片之后那颗星才接点击。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	tt.Advance(400e6)
	tt.Frame()
	mustClick(t, tt, label)
	mustFind(t, tt, "取消关注:"+room.Name)
}

// 卡片滚出虚拟列表之后点岛：画面从岛起飞，不是从屏幕外那块卡片。
//
// 原来断言 app.cardRectFor(key) 返回零矩形——私有 map，重构一动就红。
// 换成看得见的：飞行块的起点中心必须落在岛里。屏幕外的卡片不在树上，
// 它的矩形不该成为起点。
func TestFeatureIslandFlightStartsFromTheIslandAfterScrolling(t *testing.T) {
	rooms := cardFlightRooms("房", "英雄联盟", 120)
	app, tt := cardFlightApp(t, rooms)
	openCardAndCollapse(t, app, tt, rooms[0])

	tt.Move(720, 600)
	for i := 0; i < 8; i++ {
		tt.Wheel(720, 600, 0, 400)
		tt.Advance(200 * time.Millisecond)
	}
	tt.Frame()

	mustClick(t, tt, "回到直播间")
	flight, ok := tt.Find("表面过渡中的画面")
	if !ok {
		t.Fatalf("点岛没有飞行块。可见文本：%v", tt.Texts())
	}
	island, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到岛")
	}
	cx := flight.X + flight.W/2
	cy := flight.Y + flight.H/2
	if cx < island.X || cx > island.X+island.W || cy < island.Y || cy > island.Y+island.H {
		t.Fatalf("飞行块从 (%.0f,%.0f) 起飞，不在岛 %v 上：滚出去的卡片被当成了起点", cx, cy, island)
	}
}

// 首屏封面还没解码完，不该拦住切平台：点完 tab 内容就该换。
func TestFeaturePlatformSwitchDoesNotWaitForCovers(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "bilibili"
	a.GroupsByPlatform["douyu"] = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []string{"英雄联盟"}, CategoryIDs: []string{"86"},
	}}
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms: []Streamer{
			{Platform: "douyu", RoomID: "1", Name: "甲", CoverURL: "https://example.invalid/a.jpg"},
			{Platform: "douyu", RoomID: "2", Name: "乙", CoverURL: "https://example.invalid/b.jpg"},
		},
		cursor: listPageSize,
		at:     time.Now(),
	}
	tt := ui.NewTester(a.Shell, 1440, 900)
	tt.Frame()

	a.switchTab("douyu")

	waitFor(t, func() bool {
		tt.Frame()
		return len(cardLabels(tt)) > 0
	})
	got := strings.Join(cardLabels(tt), "|")
	if !strings.Contains(got, "甲") || !strings.Contains(got, "乙") {
		t.Fatalf("切平台后卡片是 %v，期望斗鱼那两间", cardLabels(tt))
	}
}
