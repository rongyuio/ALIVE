package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 列表必须是虚拟化的：卡片数量涨十倍，一帧里构建的卡片数不该跟着涨。
//
// 这不是性能测试，是「有没有走 ui.List」的结构性回归——如果哪天有人把 grid
// 改回一次性铺满，这个测试会立刻炸。
func TestGridBuildsOnlyVisibleCards(t *testing.T) {
	build := func(n int) int {
		app := NewMockApp()
		app.LiveMode = true
		app.ListLoading = false
		app.Rooms = make([]Streamer, n)
		for i := range app.Rooms {
			app.Rooms[i] = Streamer{
				Platform: "bilibili",
				RoomID:   fmt.Sprint(i),
				Name:     fmt.Sprintf("主播%03d", i),
				Title:    "标题",
				// 不给图片地址：测试里不该触发任何下载
			}
		}
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.Frame() // 第一帧测量行高
		before := app.builtCards
		tt.Frame()
		return app.builtCards - before
	}

	small := build(40)
	big := build(600)
	t.Logf("40 条构建 %d 张卡片，600 条构建 %d 张", small, big)

	if small == 0 {
		t.Fatal("一张卡片都没构建，网格没有渲染")
	}
	// 一屏 5 列 3 行左右，加上下缓冲，40 张和 600 张的构建量应该在同一量级。
	if big > small*2 {
		t.Fatalf("600 条构建了 %d 张卡片，40 条只有 %d 张：虚拟化没生效", big, small)
	}
}

// 页脚是列表的最后一行，三态文案要跟着分页状态走（baseline 的 footer 三态）。
func TestGridFooterStates(t *testing.T) {
	cases := []struct {
		name    string
		hasMore bool
		loading bool
		want    string
	}{
		{"到底了", false, false, "已显示最近"},
		{"还有下一页", true, false, "继续下滑加载更多"},
		{"正在加载", true, true, "正在加载更多"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := NewMockApp()
			app.ListHasMore = tc.hasMore
			app.ListLoadingMore = tc.loading
			tt := ui.NewTester(app.Shell, 1440, 900)
			tt.Frame()
			if !tt.HasText(tc.want) {
				t.Fatalf("页脚没有出现 %q，texts=%q", tc.want, tt.Texts())
			}
		})
	}
}
