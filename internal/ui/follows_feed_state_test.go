package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// liveSubsApp 搭一个订阅分区首屏：LiveMode 开着，但把 ensureSubFeed 的
// 「这一项已经发起过」标记先填好，视图每帧都直接返回，不发真实请求。
//
// 平台用一个是没接入的名字：旧实现里的「重试」会走 retryLive，那里也会
// 因为平台未接入而直接返回，测试不会联网。
func liveSubsApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	app := NewMockApp()
	app.LiveMode = true
	app.Platform = "unwired"
	app.Subs = []Subscription{{
		PlatformID: "bilibili", Platform: "B站",
		Group: "热门", Name: "英雄联盟", CategoryID: "1",
	}}
	app.SubIndex = 0
	app.subKey = subFeedKey(app.Subs[0])
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	return app, tt
}

// 订阅分区的错误条必须显示 SubErr，重试也只能动订阅列表。
//
// 以前它和首页共用 errorBar：正文写的是 a.ErrText，按钮调的是 retryLive，
// 订阅请求失败时用户看到的是一条没字的红条，点「重试」重拉的却是首页。
func TestSubscribeErrorBarShowsAndRetriesSubFeed(t *testing.T) {
	app, tt := liveSubsApp(t)
	// 列表不为空：这样屏幕上只有错误条那一枚「重试」，不会和空态里的混。
	app.SubRooms = []Streamer{{Platform: "bilibili", RoomID: "100", Name: "订阅里的房间"}}
	app.SubErr = "订阅加载失败：网络错误"
	app.ErrText = "首页加载失败：网络错误"
	tt.Frame()

	if !tt.HasText("订阅加载失败：网络错误") {
		t.Fatalf("订阅分区的错误条没有显示 SubErr，文本树 = %v", tt.Texts())
	}
	if tt.HasText("首页加载失败：网络错误") {
		t.Fatal("订阅分区的错误条显示的是首页 ErrText")
	}

	if err := tt.Click("重试"); err != nil {
		t.Fatalf("点不到订阅错误条上的重试：%v", err)
	}
	tt.Frame()
	if app.SubErr != "" {
		t.Fatalf("点重试后 SubErr 还是 %q", app.SubErr)
	}
	if app.ErrText != "首页加载失败：网络错误" {
		t.Fatalf("订阅分区的重试清掉了首页错误：ErrText=%q", app.ErrText)
	}
}

// 订阅分区的加载/空态读自己的 SubLoading，不借首页的 ListLoading。
//
// 两个方向都要盯：订阅在途时不能画成空态，首页在途时也不能把骨架借给
// 一份确实为空的订阅列表。
func TestSubscribeEmptyStateUsesSubLoading(t *testing.T) {
	app, tt := liveSubsApp(t)

	app.SubRooms = nil
	app.SubLoading = true
	tt.Frame()
	if tt.HasText("这里还没有直播间") {
		t.Fatalf("订阅首屏还在加载，界面已经说「这里还没有直播间」：%v", tt.Texts())
	}

	app.SubLoading = false
	app.ListLoading = true
	tt.Frame()
	if !tt.HasText("这里还没有直播间") {
		t.Fatalf("订阅列表确实为空，却没有画空态：%v", tt.Texts())
	}
	if _, ok := tt.Find("查看全部分区"); ok {
		t.Fatal("订阅分区的空态摆出了只属于首页的「查看全部分区」")
	}
}

// 订阅网格的页脚也必须读订阅自己的翻页状态。
//
// gridFooter 原先直接读首页的 a.ListLoadingMore / a.ListHasMore：订阅
// 还有下一页时页脚写着「已显示最近 N 条」，首页的「还有更多」又会
// 反过来影响订阅。
func TestSubscribeGridFooterUsesSubPaging(t *testing.T) {
	app, tt := liveSubsApp(t)
	app.SubRooms = []Streamer{{Platform: "bilibili", RoomID: "100", Name: "订阅里的房间"}}
	app.SubHasMore = true
	app.SubLoadingMore = false
	app.ListHasMore = false
	app.ListLoadingMore = false
	tt.Frame()

	if !tt.HasText("继续下滑加载更多") {
		t.Fatalf("订阅还有下一页，页脚却没说：%v", tt.Texts())
	}
	if tt.HasText("已显示最近") {
		t.Fatal("订阅页脚读的是首页的 ListHasMore")
	}

	app.SubLoadingMore = true
	tt.Frame()
	if !tt.HasText("正在加载更多…") {
		t.Fatalf("订阅正在加载下一页，页脚没有 spinner：%v", tt.Texts())
	}
}
