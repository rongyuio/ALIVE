package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 造一个能滚的首页：n 张卡片，不触发任何图片下载。
func scrollableApp(n int) *App {
	app := NewMockApp()
	app.LiveMode = true
	app.ListLoading = false
	app.ListHasMore = true
	app.Rooms = make([]Streamer, n)
	for i := range app.Rooms {
		app.Rooms[i] = Streamer{
			Platform: "bilibili",
			RoomID:   fmt.Sprint(i),
			Name:     fmt.Sprintf("主播%03d", i),
			Title:    "标题",
		}
	}
	return app
}

// 滚轮真的能把列表往下推：可视范围前移，且始终只构建可视的那几行。
//
// 这条以前完全没测过——TestGridBuildsOnlyVisibleCards 只验了「一帧构建多少张」，
// 没有真的滚过。滚轮事件的坐标系和列表的虚拟化是两套东西，接错了不会报错，
// 只会「滚不动」。
func TestWheelScrollsGrid(t *testing.T) {
	app := scrollableApp(600)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame() // 第一帧测量行高
	tt.Frame()

	first, _ := app.HomeList.Visible()
	if first != 0 {
		t.Fatalf("初始可视范围从第 %d 行开始，期望 0", first)
	}
	// 指针落在网格中间再滚：滚动事件按指针位置找接收者。
	//
	// 滚轮是「滑行」的：一格不会立刻到位，要推进时钟让它走完。只 Frame
	// 不 Advance 的话列表纹丝不动——这正是这条测试第一次跑出来的样子。
	tt.Move(700, 600)
	for i := 0; i < 12; i++ {
		tt.Wheel(700, 600, 0, 400)
		tt.Advance(200 * time.Millisecond)
	}

	first2, _ := app.HomeList.Visible()
	if first2 <= first {
		t.Fatalf("滚了 12 次滚轮，可视范围仍是第 %d 行起：列表没动", first2)
	}

	// 虚拟化已经在 TestGridBuildsOnlyVisibleCards 里验过；这里只确认
	// 滚完之后列表还在正常工作（可视范围合理、没有退化成一次性铺满）。
	if _, last2 := app.HomeList.Visible(); last2-first2 > 20 {
		t.Fatalf("滚到第 %d 行后可视范围是 %d..%d：一次铺了太多行", first2, first2, last2)
	}
}

// 触底要拉下一页。
//
// 判据是 onMore 被调用：网格在「还剩两行」时就触发，不等到真正滚到底。
func TestGridAsksForMoreNearBottom(t *testing.T) {
	app := scrollableApp(60)
	called := 0
	tt := ui.NewTester(func(c *ui.Context) {
		app.grid(c, app.Rooms, &app.HomeList, func() { called++ })
	}, 1440, 900)
	tt.Frame()
	tt.Frame()
	if called != 0 {
		t.Fatalf("首屏就要求下一页了（%d 次）", called)
	}

	tt.Move(700, 600)
	for i := 0; i < 10 && called == 0; i++ {
		tt.Wheel(700, 600, 0, 400)
		tt.Advance(200 * time.Millisecond)
	}
	if called == 0 {
		t.Fatal("滚到接近底部也没要求下一页")
	}
}

// 窄窗口走 ≤1240 断点：正文左右 padding 从 14 收到 10，卡片列数跟着变。
//
// narrow() 那条分支以前只有真实窗口碰到过，一次都没测。
func TestNarrowLayout(t *testing.T) {
	app := scrollableApp(60)

	cols := func(w int) (int, float32) {
		tt := ui.NewTester(app.Shell, w, 900)
		tt.Frame()
		tt.Frame()
		return app.gridCols, app.gridCardW
	}

	wideCols, wideCard := cols(1440)
	narrowCols, narrowCard := cols(1000)

	if wideCols <= narrowCols {
		t.Fatalf("1440 宽 %d 列，1000 宽 %d 列：窄屏没有减少列数", wideCols, narrowCols)
	}
	// 列数少了，每列反而更宽——这是 auto-fill 的行为，不是 bug。
	if narrowCard <= wideCard {
		t.Fatalf("窄屏卡片宽 %v 不大于宽屏 %v：列数减少后每列应当更宽", narrowCard, wideCard)
	}
	// 窄屏 padding 收到 10：可用宽度 = 1000 - 20 - 滚动条。
	avail := float32(1000) - 2*10 - 10
	if want := (avail - 16*float32(narrowCols-1)) / float32(narrowCols); narrowCard != want {
		t.Fatalf("窄屏卡片宽 %v，按 padding 10 应为 %v", narrowCard, want)
	}
}
