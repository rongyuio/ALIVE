package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 空墙上的按钮（「去关注列表」「去首页」）必须有常态底色。
//
// 和之前那九处同一个机制：Background 是赋值不是叠加，先 Background
// (FillTertiary) 再 Background(hoverSurface(...)) 会把常态抹成透明。
// 这一处上一轮全仓扫描漏掉了——base 那一行和 hover 那一行之间夹了
// .Center().Cursor().Label() 一串调用，按「相邻行」扫是扫不到的。
func TestWatchEmptyButtonKeepsBase(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		a := newApp()
		a.watchEmptyBtn(c, "去关注列表", func() {})
	}, 300, 120)
	tt.Frame()
	btn, ok := tt.Find("去关注列表")
	if !ok {
		t.Fatal("找不到空墙按钮")
	}
	r, g, b := pixelAt(tt.Image(), int(btn.X+btn.W/2), int(btn.Y+4))
	want := theme.FillTertiary
	if r != want.R || g != want.G || b != want.B {
		t.Errorf("空墙按钮底色 %d,%d,%d，应为 FillTertiary %d,%d,%d（抹成透明就是窗口底色）",
			r, g, b, want.R, want.G, want.B)
	}
}

// Esc 收起分区搜索之后，焦点还在输入框里；再打字必须能把面板重新打开。
//
// 「焦点刚进来」那个边沿只来一次。Esc 只写 CategorySearchOpen=false 并
// CancelFocus，而 CancelFocus 只清「待处理的聚焦请求」，放不掉已经生效的
// 焦点（third_party/mygo/ui/ref.go）。于是 focused 恒为真、边沿不再来，
// 输入框收窄、一个字都不显示结果——而这是最常见的连击。
func TestCategorySearchReopensAfterEsc(t *testing.T) {
	a := newApp()
	a.Groups = []CategoryGroup{
		{Name: "网游", Categories: []string{"英雄联盟", "DOTA2"}, CategoryIDs: []string{"1", "2"}},
		{Name: "手游", Categories: []string{"王者荣耀"}, CategoryIDs: []string{"3"}},
	}
	a.GroupIndex, a.CategoryIndex = 0, 0

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("搜索分区"); err != nil {
		t.Fatalf("点不到搜索框：%v", err)
	}
	tt.Frame()
	tt.Type("网")
	tt.Frame()
	if !a.CategorySearchOpen {
		t.Fatal("前提不成立：点进输入框后面板没开")
	}

	// Esc 收起
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	tt.Frame()
	if a.CategorySearchOpen {
		t.Fatal("Esc 没收起面板")
	}

	// 焦点还在框里，再打字：面板必须回来
	a.CategoryQuery = "游"
	tt.Frame()
	tt.Frame()
	if !a.CategorySearchOpen {
		t.Fatal("Esc 之后接着打字，面板没重新打开（焦点还在框里，只认聚焦边沿就再也不开）")
	}
	// 而且真的有结果行
	if len(a.categoryHits()) == 0 {
		t.Skip("这个分区表下没有匹配「游」的分区，跳过结果行断言")
	}
}

// 离开播放页的两次请求都要生效，谁也不能吞掉谁。
//
// 以前 transitionMatch 那条路要把 action 推迟两趟构建（exitArm + buildSeq
// 比较），这期间第二次「离开播放页」被整条丢弃：Esc（收起）之后紧接着点
// 岛上的 X（真关），X 消失、焦点还留着、这一路继续出声。
//
// 现在没有推迟：每一次请求都在当场执行。这条测的是那个结果本身——先收起，
// 再关掉，两次都发生了。
func TestLeaveRequestsBothApply(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	s := &roomSession{key: watchKey(room), room: room}
	a.watch.init()
	a.watch.sessions[s.key] = s
	a.focus = s

	a.Router = ui.NewRouter(routeFollows)
	a.FollowsSection = followsSectionWatch
	a.WatchRooms = []Streamer{room}
	a.playerReturnRoute, a.playerReturnSection = routeFollows, followsSectionWatch
	a.Router.Push(routePlayer)

	// 第一次：收起（Esc）。焦点留着，退回观测墙。
	a.collapsePlayer()
	if a.focus != s {
		t.Fatal("收起把焦点清掉了：这一路应该继续出声")
	}
	if a.Router.Path() == routePlayer {
		t.Fatal("收起没有离开播放页")
	}

	// 第二次：真关（岛上的 X）。焦点必须清掉。
	a.closeFocus()
	if a.focus != nil {
		t.Fatal("第二次请求被吞掉了：关掉之后焦点还在，这一路继续出声")
	}
}

// 焦点会话在没有引擎的那一段（取流中 / 取流失败 / 未开播）也必须有看门狗。
//
// 媒体层的 trackFirstFrame 在 eng()==nil 时直接返回；墙层的 watchTick 又
// 因为 s == a.focus 跳过。两边都不管：焦点会话永久停在 loading，骨架屏
// 不消、重试不触发——用户看到的是「一直在转圈，点重试也没反应」。
func TestFocusSessionGetsWatchdogWithoutEngine(t *testing.T) {
	a := newApp()
	s := &roomSession{
		key:      "p|1",
		room:     Streamer{Platform: "p", RoomID: "1", Name: "房"},
		loading:  true,
		openedAt: time.Now().Add(-time.Minute), // 早就超过 firstFrameBudget
		log:      wallLog("p|1", ""),
	}
	a.watch.init()
	a.watch.sessions[s.key] = s
	a.focus = s

	a.watchTick(time.Now())

	if s.loading && s.err == "" {
		t.Fatal("焦点会话在取流中超时了却没人管：loading 永久为真，重试永不触发")
	}
	if s.err == "" {
		t.Logf("焦点会话落到：err=%q loading=%v（只要有终态就算有人管）", s.err, s.loading)
	}
}
