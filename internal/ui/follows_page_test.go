package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 「全部关注」是顶栏第一个 tab 对应的页面：切过去铺满正文区，
// 不是浮层，也不需要点空白处收起。
func TestFollowsTabOpensPage(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	app.switchTab("follows")
	tt.Frame()

	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("路由是 %s，期望 %s", got, routeFollows)
	}
	// 关注页独有的元素：页内搜索框。首页没有这个 label。
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Fatal("关注页没有渲染")
	}
}

// 从关注页切回平台后，页面内容应该换回列表。
func TestFollowsTabSwitchesBack(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	app.switchTab("follows")
	tt.Frame()
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Fatal("关注页没有渲染")
	}

	app.switchTab("douyu")
	tt.Frame()
	if got := app.Router.Path(); got != routeHome {
		t.Fatalf("路由是 %s，期望首页", got)
	}
	if _, ok := tt.Find("搜索已关注的主播"); ok {
		t.Error("切回平台后关注页还在")
	}
}

// 刷新中：按钮按 disabled 降透明度，进度环按 done/total 画弧。
// 环是自绘的，这条测试保证 Draw 的弧线路径在测试器里也跑得通。
func TestFollowsRefreshBusyRenders(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	app.followsBusy = true
	app.followRefreshTotal = 4
	app.followRefreshDone = 1

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if _, ok := tt.Find("正在刷新 1/4"); !ok {
		t.Fatal("刷新中关注页没有渲染进度按钮")
	}
	if got := app.followRingProgress(); got != 0.25 {
		t.Errorf("进度 %.2f，期望 0.25", got)
	}
}

// 页内搜索：按昵称包含匹配，清空后恢复；提示文案跟着切到「N 位匹配」。
func TestFollowsPageSearch(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)

	before := len(app.followsList())
	plainTip := app.followsChipTip("")
	if err := tt.Click("搜索已关注的主播"); err != nil {
		t.Fatal(err)
	}
	tt.Type("PDD")

	got := app.followsList()
	t.Logf("before=%d after=%d", before, len(got))
	if len(got) == 0 || len(got) >= before {
		t.Fatalf("搜索 PDD 后列表 %d 条（之前 %d）", len(got), before)
	}
	for _, s := range got {
		if !strings.Contains(s.Name, "PDD") {
			t.Errorf("列表里混入不匹配的主播 %q", s.Name)
		}
	}
	if tip := app.followsChipTip(""); !strings.Contains(tip, "位匹配") {
		t.Errorf("搜索时提示是 %q，期望「N 位匹配」", tip)
	}

	app.FollowsQuery = ""
	if len(app.followsList()) != before {
		t.Error("清空搜索后没有恢复全量列表")
	}
	if tip := app.followsChipTip(""); tip != plainTip && !strings.Contains(tip, "位主播") {
		t.Errorf("清空后提示是 %q，期望回到「N 位主播 · N 正在直播」", tip)
	}
}

// 计数不再常驻：常态只有胶囊，指针停住后才以提示浮出。
func TestFollowsChipCountShowsOnHover(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if tt.HasText("位主播") {
		t.Fatal("计数又常驻在胶囊条里了")
	}

	r, ok := tt.Find("全部")
	if !ok {
		t.Fatal("找不到「全部」胶囊")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	// 提示要等主题的 TooltipDelay，tester 的时钟不会自己走
	tt.Advance(time.Second)

	if !tt.HasText("位主播") {
		t.Fatal("悬停后没有出现计数提示")
	}
	if !tt.HasText(app.followsChipTip("")) {
		t.Errorf("提示文案不是 %q", app.followsChipTip(""))
	}
}

// 平台筛选只留该平台的主播，提示也只数这个平台。
func TestFollowsListFiltersByPlatform(t *testing.T) {
	app := NewMockApp()
	app.FollowsPlatform = "douyu"

	list := app.followsList()
	if len(list) == 0 {
		t.Fatal("按斗鱼筛选后列表为空")
	}
	for _, s := range list {
		if s.Platform != "douyu" {
			t.Fatalf("平台筛选混进 %s", s.Platform)
		}
	}
	if tip := app.followsChipTip("douyu"); !strings.Contains(tip, "位主播") {
		t.Errorf("斗鱼提示是 %q", tip)
	}
}

// 关注页没有平台上下文，顶栏不该出现搜索入口（它有自己的页内搜索）。
func TestSearchAvailableOnlyOnPlatformPages(t *testing.T) {
	app := NewMockApp()
	cases := []struct {
		name  string
		route string
		want  bool
	}{
		{"首页", routeHome, true},
		{"关注:关注主播", routeFollows, false},
	}
	for _, tc := range cases {
		app.Router = ui.NewRouter(tc.route)
		if got := app.searchAvailable(); got != tc.want {
			t.Errorf("%s：searchAvailable = %v，期望 %v", tc.name, got, tc.want)
		}
	}

	// 播放页有主播，就有平台上下文：搜索入口要留着。
	app.Router = ui.NewRouter(routePlayer)
	app.Player = Streamer{Platform: "bilibili", RoomID: "1", Name: "某主播"}
	if !app.searchAvailable() {
		t.Error("播放页应该有搜索入口")
	}
}

// 右键关注卡片：菜单里只有“取消关注”，选中后记录消失，且不会反过来重新关注。
func TestFollowsRowContextMenuUnfollows(t *testing.T) {
	app := NewMockApp()
	// 关注页只有在 LiveMode 下才读 Follows；NewApp 在正式入口里打开它，
	// Mock 不带，测试要自己补。
	app.LiveMode = true
	app.Follows = []FollowRecord{
		{Platform: "douyu", RoomID: "1", Name: "甲"},
		{Platform: "douyu", RoomID: "2", Name: "乙"},
	}
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	targetLabel, restLabel := "关注的主播 甲", "关注的主播 乙"
	targetName := "甲"
	if err := tt.RightClick(targetLabel); err != nil {
		t.Fatal(err)
	}
	labels := tt.Menu()
	found := false
	for _, label := range labels {
		if label == "取消关注" {
			found = true
		}
	}
	if !found {
		t.Fatalf("右键菜单里没有“取消关注”：%v", labels)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem("取消关注"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if app.ToastText != "已取消关注 "+targetName {
		t.Fatalf("取关没有反馈: %q", app.ToastText)
	}
	if _, ok := tt.Find(targetLabel); ok {
		t.Fatal("取关后卡片还在")
	}
	if _, ok := tt.Find(restLabel); !ok {
		t.Fatal("取关误删了别的卡片")
	}
}

// 记录已经不在时，取消关注不能把它加回去；重复触发是安全的。
func TestRemoveFollowIsNotAToggle(t *testing.T) {
	app := NewMockApp()
	app.Follows = nil
	streamer := Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	if app.removeFollow(streamer) {
		t.Fatal("没有记录却报告删除成功")
	}
	if len(app.Follows) != 0 {
		t.Fatalf("取消关注把记录加回来了: %+v", app.Follows)
	}

	app.Follows = []FollowRecord{{Platform: "douyu", RoomID: "1", Name: "甲"}}
	if !app.removeFollow(streamer) {
		t.Fatal("已关注的记录没有删除")
	}
	if len(app.Follows) != 0 {
		t.Fatalf("取关后仍剩 %+v", app.Follows)
	}
}

// 订阅分区并进关注页：顶栏只剩那颗星星，页内一级分区是
// 「关注主播 / 订阅分区」，二级各用自己那一行。
func TestFollowsPageMergesSubscribeSection(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "鸣潮", CategoryID: "1"},
	}
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	// 顶栏不再有独立的「订阅」tab
	if _, ok := tt.Find("订阅"); ok {
		t.Fatal("顶栏还留着订阅 tab")
	}
	for _, name := range []string{"关注主播", "订阅分区"} {
		if _, ok := tt.Find(name); !ok {
			t.Fatalf("关注页没有一级分区 %q", name)
		}
	}
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Fatal("默认一级分区应该是关注主播")
	}
	if _, ok := tt.Find("订阅分区:鸣潮"); ok {
		t.Fatal("关注主播下不该出现订阅 chip")
	}

	if err := tt.Click("订阅分区"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routeFollows {
		t.Fatalf("切订阅分区不该换路由，路由是 %q", app.Router.Path())
	}
	if _, ok := tt.Find("订阅分区:鸣潮"); !ok {
		t.Fatal("订阅分区没有列出已订阅的分区")
	}
	if _, ok := tt.Find("搜索已关注的主播"); ok {
		t.Error("订阅分区不该出现主播搜索")
	}
	if _, ok := tt.Find("关注筛选:全部"); ok {
		t.Error("订阅分区不该出现平台筛选胶囊")
	}

	if err := tt.Click("关注主播"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Fatal("切回关注主播后页内搜索没回来")
	}
}

// 一级分区的两枚 tab 可以拖动换位：落在哪一枚上就插到它的位置。
//
// 换的是格子顺序，不是当前看的分区——分区身份跟着 tab 走，
// 「关注主播」被拖到右边之后，屏幕上还是它那一屏（搜索框还在）。
func TestFollowsSectionTabsDragToReorder(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "鸣潮", CategoryID: "1"},
	}
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	from, ok := tt.Find("关注主播")
	if !ok {
		t.Fatal("没有「关注主播」tab")
	}
	to, ok := tt.Find("订阅分区")
	if !ok {
		t.Fatal("没有「订阅分区」tab")
	}
	sx, sy := from.X+from.W/2, from.Y+from.H/2
	tx, ty := to.X+to.W/2, to.Y+to.H/2

	tt.Move(sx, sy)
	tt.Press(sx, sy)
	// 先越过 4 DIP 的拖动阈值，再移到目标上松手
	tt.Move(sx+8, sy)
	tt.Move(tx, ty)
	tt.Release(tx, ty)
	tt.Frame()

	if got, want := app.FollowsSectionOrder, []string{sectionIDSubs, sectionIDStreamers}; !equalStrings(got, want) {
		t.Fatalf("拖动后顺序 %v，期望 %v", got, want)
	}
	// 屏幕上的位置也换了：订阅分区到了左边
	afterFrom, _ := tt.Find("关注主播")
	afterTo, _ := tt.Find("订阅分区")
	if afterTo.X >= afterFrom.X {
		t.Fatalf("拖动后订阅分区仍在右边：X %.1f / %.1f", afterTo.X, afterFrom.X)
	}
	// 选中项按分区身份走：还在关注主播那一屏
	if app.FollowsSection != followsSectionStreamers {
		t.Errorf("拖动换了选中的分区：%d", app.FollowsSection)
	}
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Error("拖动之后关注主播这一屏的内容跟着换了")
	}
}

// 一级分区换的是同一块内容区：代次 +1。
func TestFollowsSectionSwitchAdvancesGeneration(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	seq := app.followsSeq
	if err := tt.Click("订阅分区"); err != nil {
		t.Fatal(err)
	}
	if app.followsSeq != seq+1 {
		t.Errorf("代次没动：%d → %d", seq, app.followsSeq)
	}

	if err := tt.Click("关注主播"); err != nil {
		t.Fatal(err)
	}
}

// 还没有订阅时，订阅分区那一级只留一句入口说明，内容区是空态。
func TestFollowsSubsSectionEmptyState(t *testing.T) {
	app := NewMockApp()
	app.Subs = nil
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if err := tt.Click("订阅分区"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("还没有订阅分区") {
		t.Error("订阅分区为空时没有入口说明")
	}
}
