package ui

import (
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 往右点就从右来，往左点就从左来。方向是内容进出的唯一依据，
// 算反了整屏内容会朝反方向擦出去。
func TestCategorySwitchSetsDirection(t *testing.T) {
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
	if app.navDir != navRight {
		t.Errorf("往右点，方向是 %v，期望 navRight", app.navDir)
	}

	seq := app.navSeq
	if err := tt.Click("分类:" + cats[start]); err != nil {
		t.Fatal(err)
	}
	if app.navDir != navLeft {
		t.Errorf("往左点，方向是 %v，期望 navLeft", app.navDir)
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

// 换了一屏，新的一屏是从侧面进来的——不是原地换掉。
//
// 这条只能量「第一帧的位置」和「停稳后的位置」之差：Enter 的位移量就是
// 方向的全部体现，它一旦是 0，切换就退化成了原地淡入。
func TestCategorySwitchSlidesContentIn(t *testing.T) {
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
	name := "直播间:" + feed[0].Name
	entering, ok := tt.Find(name)
	if !ok {
		t.Fatalf("换代后的第一张卡片 %q 没出现", name)
	}

	tt.Advance(900 * time.Millisecond)
	tt.Frame()
	settled, ok := tt.Find(name)
	if !ok {
		t.Fatalf("第一张卡片 %q 不见了", name)
	}

	// 往右点：内容从右边进来，第一帧应该在最终位置的右边 16 DIP。
	if d := entering.X - settled.X; d < 6 || d > 20 {
		t.Errorf("进场位移是 %.1f DIP，期望约 16：内容没有从侧面进来", d)
	}
}

// 三级分区（数据源给了才出现）走的是同一套方向逻辑：它在「全部」前面
// 多一格，所以索引差要 +1 换算。
func TestSubCategorySwitchSetsDirection(t *testing.T) {
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
	// 「全部」是第 0 格，「娱乐」是第 2 格。
	if app.navDir != navRight {
		t.Errorf("方向是 %v，期望 navRight", app.navDir)
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

// 换筛选就是换了一份列表：代次 +1、方向按格子差算。
func TestFollowsFilterSwitchSetsDirection(t *testing.T) {
	app := NewMockApp()
	app.switchTab("follows")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	ids := app.followsPlatforms()
	if len(ids) < 2 {
		t.Skip("示例数据里只有一家的关注")
	}
	seq := app.followsSeq

	if err := tt.Click("关注筛选:" + platformLabel(ids[1])); err != nil {
		t.Fatal(err)
	}
	if app.FollowsPlatform != ids[1] {
		t.Fatalf("FollowsPlatform=%q，期望 %q", app.FollowsPlatform, ids[1])
	}
	if app.navDir != navRight {
		t.Errorf("往右点，方向是 %v，期望 navRight", app.navDir)
	}
	if app.followsSeq != seq+1 {
		t.Errorf("代次没动：%d → %d", seq, app.followsSeq)
	}

	if err := tt.Click("关注筛选:全部"); err != nil {
		t.Fatal(err)
	}
	if app.navDir != navLeft {
		t.Errorf("往左点，方向是 %v，期望 navLeft", app.navDir)
	}
}

// 订阅条上每个 chip 的格子同样要对得上：chip 里塞了三样东西，
// 宽度算式漏一项就会整体错开。
func TestSubscribeChipBoxMatchesLayout(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "无畏契约", CategoryID: "2"},
	}
	app.switchTab("subscribe")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	box := app.subsBox
	origin := float32(math.NaN())
	for i, sub := range app.Subs {
		r, ok := tt.Find("订阅:" + sub.Platform + " " + sub.Name)
		if !ok {
			t.Fatalf("%q 的 chip 没渲染出来", sub.Name)
		}
		if d := math.Abs(float64(r.W - box.w[i])); d > 1 {
			t.Errorf("%q 宽度：量出 %.2f，实际 %.2f", sub.Name, box.w[i], r.W)
		}
		d := r.X - box.x[i]
		if math.IsNaN(float64(origin)) {
			origin = d
		}
		if math.Abs(float64(d-origin)) > 1 {
			t.Errorf("%q 位置差 %.2f，第一个是 %.2f：格子没和布局对齐", sub.Name, d, origin)
		}
	}
}

// 换订阅项也要换代次和方向。
func TestSubscribeSwitchSetsDirection(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "无畏契约", CategoryID: "2"},
	}
	app.switchTab("subscribe")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	seq := app.subFeedSeq
	if err := tt.Click("订阅:B站 无畏契约"); err != nil {
		t.Fatal(err)
	}
	if app.SubIndex != 1 {
		t.Fatalf("SubIndex=%d，期望 1", app.SubIndex)
	}
	if app.navDir != navRight {
		t.Errorf("方向是 %v，期望 navRight", app.navDir)
	}
	if app.subFeedSeq != seq+1 {
		t.Errorf("代次没动：%d → %d", seq, app.subFeedSeq)
	}
}

// 三个内容区各有自己的代次：在订阅页换了 chip，回到首页不该再播一次进场。
func TestSequencesAreIndependent(t *testing.T) {
	app := NewMockApp()
	if app.navSeq != app.followsSeq || app.navSeq != app.subFeedSeq {
		t.Skip("初值不同，这条断言没有意义")
	}
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "A", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "B", CategoryID: "2"},
	}
	home := app.navSeq
	app.switchTab("subscribe")
	app.subFeedSeq++
	if app.navSeq != home {
		t.Errorf("换订阅项的代次串到了首页：%d → %d", home, app.navSeq)
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
	app.switchTab("subscribe")
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()

	sliderCovers(t, tt, "订阅:B站 鸣潮")
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
