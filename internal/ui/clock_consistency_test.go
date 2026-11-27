package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
	"alive/internal/watch"
)

// 分区树只有两级：`internal/live.CategoryGroup` 里没有第三级字段，四个平台
// 也都不返回。曾经有一整条三级分区的 UI 路径（`subCategories` 映射、
// `SubCatIndex`、`tertiaryRow`、sub-slide 滑块），而那个映射**没有任何
// 写入点**——`len(currentSubs()) > 0` 恒假，控件从未出现。
//
// 这条测试钉住"不要再把它加回来而不接数据源"。
func TestCategoryBarHasNoThirdLevel(t *testing.T) {
	a := newApp()
	a.LiveMode = true
	a.Groups = []CategoryGroup{
		{Name: "网游", Categories: []string{"英雄联盟", "DOTA2"}, CategoryIDs: []string{"1", "2"}},
		{Name: "手游", Categories: []string{"王者荣耀"}, CategoryIDs: []string{"3"}},
	}

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()

	if !tt.HasText("网游") {
		t.Fatal("一级分区没画出来")
	}
	if !tt.HasText("英雄联盟") {
		t.Fatal("二级分区没画出来")
	}
	// 三级分区的 chip 用「子分类:」前缀（曾经存在过）。一个都不该有。
	for _, s := range tt.Texts() {
		if len(s) >= 4 && s[:4] == "子分类:" {
			t.Fatalf("又画出了三级分区 chip %q：数据源里没有第三级", s)
		}
	}
}

// 分区切换只按「一级 + 二级」判空操作。
//
// 以前这两处各带一条 `SubCatIndex < 0` 的附加条件，而那一位恒为 -1——
// 判据恒真，读起来像有第三种状态，实际没有。
func TestCategorySelectionEmptyOperation(t *testing.T) {
	a := newApp()
	a.Groups = []CategoryGroup{
		{Name: "网游", Categories: []string{"英雄联盟", "DOTA2"}, CategoryIDs: []string{"1", "2"}},
		{Name: "手游", Categories: []string{"王者荣耀"}, CategoryIDs: []string{"3"}},
	}
	a.GroupIndex, a.CategoryIndex = 0, 0

	before := a.navSeq
	a.selectGroup(0)
	if a.navSeq != before {
		t.Error("点已选中的一级分区换代了：内容层会被重建，点击会丢")
	}
	a.selectCategory(0)
	if a.navSeq != before {
		t.Error("点已选中的二级分区换代了")
	}
	a.selectGroup(1)
	if a.navSeq == before {
		t.Error("换一级分区没有换代")
	}
}

// 观测墙的格子比例是统一值，没有 per-room 入口。
//
// `watchAspect(key)` 曾经收一个 key 却恒返回 DefaultAspect——留着那个签名
// 会让人以为「墙上按房间算比例」。现在建 items 时直接读
// watch.DefaultAspect，每一格的比例必然相同。
func TestWatchLayoutUsesUniformAspect(t *testing.T) {
	rooms := []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "甲"},
		{Platform: "bilibili", RoomID: "2", Name: "乙"},
		{Platform: "douyu", RoomID: "3", Name: "丙"},
	}
	items := make([]watch.Item, len(rooms))
	for i, r := range rooms {
		items[i] = watch.Item{Key: watchKey(r), Aspect: watch.DefaultAspect}
	}
	rects := watch.Layout(items, 1280, 720, theme.GridGap)
	if len(rects) != len(rooms) {
		t.Fatalf("布局返回 %d 个矩形，应为 %d", len(rects), len(rooms))
	}
	for i, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("第 %d 格是空矩形 %+v", i, r)
		}
		if got := r.W / r.H; got != rects[0].W/rects[0].H {
			t.Errorf("第 %d 格比例 %.3f，和第 0 格 %.3f 不同：比例不是统一的",
				i, got, rects[0].W/rects[0].H)
		}
	}
}

// 窗口 resize 的观测点和判据必须同一个时钟。
//
// `matchable()` 以前按挂钟算 `time.Since(resizedAt)`，Tester 里
// `tt.Advance` 推不动它——island_card_bug_test.go 曾经被迫写
// `time.Now().Add(-time.Hour)` 手工把观测点拨回去，那本身就是这条债的证据。
func TestMatchableFollowsFrameClock(t *testing.T) {
	a := newApp()
	tt := ui.NewTester(a.Shell, 1280, 800)

	// 第一帧会把 resizedAt 拨到这一帧的时钟上，于是 400ms 内不可挂 Match。
	tt.Frame()
	if a.matchable() {
		t.Fatal("刚观测到窗口尺寸就说可以挂 Match：整墙会做尺寸动画")
	}

	// 用 Tester 自己的钟往前推，越过 400ms 的收敛窗口。
	tt.Advance(matchSettleAfter + 100*time.Millisecond)
	tt.Frame()
	if !a.matchable() {
		t.Fatalf("tt.Advance 推过 %v 之后仍然不可挂 Match：判据读的不是帧钟", matchSettleAfter)
	}
}

// 播放页的临时提示按帧钟计时，能被 tt.Advance 推到消失。
func TestNoticePillUsesFrameClock(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	a.Router = ui.NewRouter(routePlayer)
	a.focus = &roomSession{key: watchKey(room), room: room}

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	tt.Frame()

	a.notifyPlayback("该直播间没有「原画」，已切到「蓝光4M」")
	tt.Frame()
	if !tt.HasText("蓝光4M") {
		t.Fatal("提示没画出来")
	}

	// 推过停留时长：提示该自己消失。
	tt.Advance(noticeDuration + time.Second)
	tt.Frame()
	tt.Frame()
	if tt.HasText("蓝光4M") {
		t.Fatalf("tt.Advance 推过 %v 之后提示还在：判据读的不是帧钟", noticeDuration)
	}
}
