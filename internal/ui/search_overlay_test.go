package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 换搜索词时旧结果必须立刻清掉。
//
// syncSearch 在查询词变化后有一段防抖 + 请求的窗口；旧结果留在
// a.SearchResults 里时，列表画的是上一个词的房间，按 Enter 打开的也是
// 上一词的首条。清空之后窗口里 hits 为空，Enter 不会误开任何旧结果。
func TestSearchClearsPreviousQueryResults(t *testing.T) {
	app := NewMockApp()
	app.Platform = "bilibili"
	app.SearchQuery = "旧词"
	app.searchQuery = "旧词"
	app.searchPlatform = app.viewPlatform()
	app.SearchResults = []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "旧词命中的房间"},
	}
	app.SearchHit = 0

	app.SearchQuery = "新词"
	app.syncSearch()

	if len(app.SearchResults) != 0 {
		t.Fatalf("换了搜索词，旧结果还挂着 %d 条：加载期间会打开上一个词的房间",
			len(app.SearchResults))
	}
}

// 顶栏搜索框必须有无障碍名字。
//
// 它只有 Placeholder，没有 Label；MyGo 的无障碍名只来自 Label，读屏和
// mygo-automation 都只看到角色、没有名字，只能按坐标找。
func TestSearchFieldHasAccessibleName(t *testing.T) {
	app := NewMockApp()
	app.SearchOpen = true
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	for _, n := range tt.AccessAll() {
		if n.Name == "搜索直播间" {
			return
		}
	}
	t.Fatal("顶栏搜索框没有 .Label：无障碍树里找不到「搜索直播间」")
}
