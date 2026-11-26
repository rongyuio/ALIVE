package ui

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 换二级分区就是换了一份内容：代次必须 +1。
//
// 这里以前还断言方向（navDir），内容层按方向整块横滑时它决定从哪侧进。
// 横滑撤掉之后方向不再存在，代次仍然是换代换 key 的唯一依据。
func TestCategorySwitchAdvancesFeedGeneration(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	cats := app.currentCategories()
	if len(cats) < 2 {
		t.Skip("示例数据只有一个二级分区")
	}
	start := app.CategoryIndex

	if err := tt.Click("分类:" + cats[start+1]); err != nil {
		t.Fatal(err)
	}
	if app.CategoryIndex != start+1 {
		t.Fatalf("点了 %q 却没选中它：CategoryIndex=%d", cats[start+1], app.CategoryIndex)
	}
	seq := app.navSeq
	if err := tt.Click("分类:" + cats[start]); err != nil {
		t.Fatal(err)
	}
	if app.navSeq != seq+1 {
		t.Errorf("换了一屏但代次没动：%d → %d", seq, app.navSeq)
	}
}

// 方向由 tab 的序号差算出来，而序号来自 tabIDs；屏幕上的顺序来自
// platformTabs。两处一旦不同步（重排之后最可能），滑向就会指错边。
func TestTabOrderMatchesRenderedOrder(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	prev := float32(math.NaN())
	for _, id := range app.tabIDs() {
		r, ok := tt.Find(tabName(id))
		if !ok {
			t.Fatalf("tab %q 没渲染出来", id)
		}
		if !math.IsNaN(float64(prev)) && r.X <= prev {
			t.Errorf("tab %q 在第 %.1f，上一个在 %.1f：tabIDs 的顺序和屏幕上的不一致",
				id, r.X, prev)
		}
		prev = r.X
	}
}

// 换一屏内容，位置不能动。
//
// 内容层曾经按方向整块横滑 16 DIP：第一帧在最终位置的右边，再滑回来。
// 整块内容和分区栏错开一个身位，用户看到的就是「有偏差」。现在只换 key、
// 直接交换，第一帧就应该在最终位置上。
func TestCategorySwitchKeepsContentInPlace(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	cats := app.currentCategories()
	start := app.CategoryIndex
	if len(cats) <= start+1 {
		t.Skip("示例数据不够两个二级分区")
	}
	if err := tt.Click("分类:" + cats[start+1]); err != nil {
		t.Fatal(err)
	}

	feed := app.feed()
	if len(feed) == 0 {
		t.Skip("这个分区没有卡片")
	}
	names := cardNames(feed, 4)
	p := newProbe(t, tt, contentRect(t, tt, names...), 16*time.Millisecond, names...)
	p.Run(60) // 960ms：足够旧版 540ms 弹簧位移完整播完
	p.RequireNoShift(0.01)
	p.RequireAlwaysPresent()
}

// 三级分区（数据源给了才出现）同样只换代次。
func TestSubCategorySwitchAdvancesGeneration(t *testing.T) {
	app := NewMockApp()
	app.subCategories[app.currentCategory()] = []string{"排位", "娱乐"}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if _, ok := tt.Find("子分类:全部"); !ok {
		t.Fatal("三级分区行没出现")
	}
	if err := tt.Click("子分类:娱乐"); err != nil {
		t.Fatal(err)
	}
	if app.SubCatIndex != 1 {
		t.Fatalf("SubCatIndex=%d，期望 1", app.SubCatIndex)
	}
}

// 关注页的筛选条：量出来的格子和真实布局必须重合。
//
// 滑块停的位置是算的，不是布局给的。算错了它就会偏出半格——而偏出去的
// 时候代码看起来是对的，只有把两边的数字摆在一起才看得出来。
func TestFollowsChipBoxMatchesLayout(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	ids := append([]string{""}, app.followsPlatforms()...)
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = "全部"
		if id != "" {
			names[i] = platformLabel(id)
		}
	}
	box := app.followsBox

	origin := float32(math.NaN())
	for i, name := range names {
		r, ok := tt.Find("关注筛选:" + name)
		if !ok {
			t.Fatalf("筛选 %q 没渲染出来", name)
		}
		if d := math.Abs(float64(r.W - box.w[i])); d > 0.5 {
			t.Errorf("%q 宽度：量出 %.2f，实际 %.2f", name, box.w[i], r.W)
		}
		d := r.X - box.x[i]
		if math.IsNaN(float64(origin)) {
			origin = d
		}
		if math.Abs(float64(d-origin)) > 0.5 {
			t.Errorf("%q 位置差 %.2f，第一个是 %.2f：格子没和布局对齐", name, d, origin)
		}
	}
}

// 换筛选就是换了一份列表：代次 +1。
func TestFollowsFilterSwitchAdvancesGeneration(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	// 示例数据只关注了一家：补一家别的平台，这条才测得到「换筛选」。
	// 原来这里是 t.Skip——前提不满足就跳过，等于把「没测」写成「通过」。
	// restore() 在 NewMockApp 里已经跑过，它会从状态文件重建 Follows；
	// 所以补记录之后要再刷一次平台筛选项的可见集合。
	app.Follows = []FollowRecord{
		{Platform: "douyu", RoomID: "f-1", Name: "一家", Live: true},
		{Platform: "bilibili", RoomID: "f-2", Name: "另一家", Live: true},
	}
	app.clampFollowsPlatform()
	ids := app.followsPlatforms()
	t.Logf("关注平台 = %v（Follows=%d 条）", ids, len(app.Follows))
	if len(ids) < 2 {
		t.Fatalf("补了关注之后仍然只有 %d 家：夹具没生效", len(ids))
	}
	tt.Frame()
	seq := app.followsSeq

	if err := tt.Click("关注筛选:" + platformLabel(ids[1])); err != nil {
		t.Fatal(err)
	}
	if app.FollowsPlatform != ids[1] {
		t.Fatalf("FollowsPlatform=%q，期望 %q", app.FollowsPlatform, ids[1])
	}
	if app.followsSeq != seq+1 {
		t.Errorf("代次没动：%d → %d", seq, app.followsSeq)
	}

	if err := tt.Click("关注筛选:全部"); err != nil {
		t.Fatal(err)
	}
}

// 关注页一级分区的两枚 tab：滑块位置同样要从量出来的格子来。
func TestFollowsSectionBoxMatchesLayout(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	box := app.followsSectionBox
	for i, id := range app.followsSectionOrder() {
		name := followsSectionNames[followsSectionOf(id)]
		r, ok := tt.Find(name)
		if !ok {
			t.Fatalf("一级分区 %q 没渲染出来", name)
		}
		if d := math.Abs(float64(r.W - box.w[i])); d > 0.5 {
			t.Errorf("%q 宽度：量出 %.2f，实际 %.2f", name, box.w[i], r.W)
		}
	}
}

// clickSubChip 点一枚 chip 的分区名那一侧。
//
// 不能按标签的中心点：chip 右半边是平台胶囊（它自己是可点元素），
// 中心很可能落在胶囊上，那样点的是「切平台」而不是「选中分区」。
func clickSubChip(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	r, ok := tt.Find("订阅分区:" + name)
	if !ok {
		t.Fatalf("%q 没渲染出来", name)
	}
	tt.ClickAt(r.X+20, r.Y+r.H/2)
}

// 订阅分区那一行的 chip 格子同样要对得上：chip 里塞了三样东西，
// 宽度算式漏一项就会整体错开。
func TestSubscribeChipBoxMatchesLayout(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "无畏契约", CategoryID: "2"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	chips := app.subChips()
	box := app.subsBox
	origin := float32(math.NaN())
	for i, chip := range chips {
		r, ok := tt.Find("订阅分区:" + chip.Name)
		if !ok {
			t.Fatalf("%q 的 chip 没渲染出来", chip.Name)
		}
		if d := math.Abs(float64(r.W - box.w[i])); d > 1 {
			t.Errorf("%q 宽度：量出 %.2f，实际 %.2f", chip.Name, box.w[i], r.W)
		}
		d := r.X - box.x[i]
		if math.IsNaN(float64(origin)) {
			origin = d
		}
		if math.Abs(float64(d-origin)) > 1 {
			t.Errorf("%q 位置差 %.2f，第一个是 %.2f：格子没和布局对齐", chip.Name, d, origin)
		}
	}
}

// 首页二级 chip 的格子同样要和真实布局重合。
//
// chip 用自然宽度（内边距 + 文字 + 边框），量算必须复刻同一套加法；
// 漏一项，滑块就停在半格上，而代码读起来完全正常。
func TestCategoryChipBoxMatchesLayout(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	cats := app.currentCategories()
	if len(cats) == 0 {
		t.Fatal("没有二级分区")
	}
	origin := float32(math.NaN())
	for i, name := range cats {
		r, ok := tt.Find("分类:" + name)
		if !ok {
			t.Fatalf("%q 没渲染出来", name)
		}
		if d := math.Abs(float64(r.W - app.navBox.w[i])); d > 0.5 {
			t.Errorf("%q 宽度：量出 %.2f，实际 %.2f", name, app.navBox.w[i], r.W)
		}
		d := r.X - app.navBox.x[i]
		if math.IsNaN(float64(origin)) {
			origin = d
		}
		if math.Abs(float64(d-origin)) > 0.5 {
			t.Errorf("%q 位置差 %.2f，第一个是 %.2f：格子没和布局对齐", name, d, origin)
		}
	}
}

// sliderCovers 扫一行像素，断言选中态那枚滑块盖住了它自己的 chip。
//
// 光对布局数字抓不到这类错：滑块是绝对定位的，和 chip 之间差一个内边距时，
// 两边的数字各自都自洽——量算说宽度对、布局说位置对，只有像素把两者
// 摆在同一张图上才看得见。
func sliderCovers(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("%q 没渲染出来", label)
	}
	im := tt.Image()
	y := int(r.Y + r.H/2)
	left, right := -1, -1
	for x := 0; x < im.Bounds().Dx(); x++ {
		if isAccent(im.At(x, y)) {
			if left < 0 {
				left = x
			}
			right = x
		}
	}
	if left < 0 {
		t.Fatalf("%q 那一行没有滑块", label)
	}
	// 容差 2：圆角边缘有抗锯齿，最外侧那一两像素不是纯 accent。
	if float32(left) > r.X+2 || float32(right) < r.X+r.W-2 {
		t.Errorf("%q：滑块在 %d..%d，chip 在 %.0f..%.0f——滑块没盖住它",
			label, left, right, r.X, r.X+r.W)
	}
}

func isAccent(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	a := theme.Accent
	return near8(r>>8, a.R) && near8(g>>8, a.G) && near8(b>>8, a.B)
}

func near8(v uint32, want uint8) bool {
	d := int(v) - int(want)
	return d > -10 && d < 10
}

// 订阅条：滑块要盖住整个 chip，包括取消按钮。
func TestSubscribeSliderCoversChip(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "鸣潮", CategoryID: "1"},
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "鸣潮", CategoryID: "2"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	sliderCovers(t, tt, "订阅分区:鸣潮")
}

// 关注页筛选条：同一套要求。
func TestFollowsSliderCoversChip(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	sliderCovers(t, tt, "关注筛选:全部")
}

// 首页二级分区：同一套要求。
func TestCategorySliderCoversChip(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	sliderCovers(t, tt, "分类:"+app.currentCategory())
}
