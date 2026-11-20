package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// subsApp 造一份订阅页：同一分区挂在多家平台上。
func subsApp() (*App, *ui.Tester) {
	app := NewMockApp()
	// 钉住一套深色配色：NewMockApp 会 restore 本机 state.json，
	// 胶囊的像素判据不能跟着用户选的主题变。
	app.PaletteID = "tech-night"
	app.ThemeMode = themeModeDark
	app.Subs = []Subscription{
		{PlatformID: "huya", Platform: "虎牙", Group: "热门", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "douyu", Platform: "斗鱼", Group: "热门", Name: "英雄联盟", CategoryID: "1"},
		{PlatformID: "bilibili", Platform: "B站", Group: "热门", Name: "无畏契约", CategoryID: "2"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	return app, tt
}

// 同一个分区被几家订阅时只出一枚 chip：主体是分区，平台退成成员。
// 成员按顶栏平台顺序排，默认显示的也是顺序里的第一家。
func TestSubscribeChipsAggregateByCategory(t *testing.T) {
	app, tt := subsApp()

	chips := app.subChips()
	if len(chips) != 2 {
		t.Fatalf("聚合成 %d 枚 chip，期望 2（英雄联盟 / 无畏契约）", len(chips))
	}
	if chips[0].Name != "英雄联盟" || len(chips[0].Subs) != 2 {
		t.Fatalf("第一枚 chip 是 %q，成员 %d 个", chips[0].Name, len(chips[0].Subs))
	}
	// 默认顶栏顺序是 斗鱼 > 虎牙 > 抖音 > B站
	if got := chips[0].Subs[0].PlatformID; got != "douyu" {
		t.Errorf("成员第一个是 %q，期望顶栏顺序最靠前的 douyu", got)
	}
	if got := app.activeSub(chips[0]).PlatformID; got != "douyu" {
		t.Errorf("默认活动平台是 %q，期望 douyu", got)
	}
	if _, ok := tt.Find("订阅分区:英雄联盟"); !ok {
		t.Fatal("聚合后的 chip 没渲染出来")
	}
}

// 点平台胶囊切换平台：chip 本身不变，只是换了成员。
func TestPlatformTagSwitchesPlatform(t *testing.T) {
	app, tt := subsApp()

	idx := app.SubIndex
	if err := tt.Click("平台:虎牙 英雄联盟"); err != nil {
		t.Fatal(err)
	}
	if got := app.subActive[subChipKey("英雄联盟")]; got != "huya" {
		t.Fatalf("活动平台是 %q，期望 huya", got)
	}
	if app.SubIndex != idx {
		t.Errorf("切换平台把选中项从 %d 挪到了 %d", idx, app.SubIndex)
	}
	if _, ok := tt.Find("订阅分区:英雄联盟"); !ok {
		t.Fatal("切换平台之后 chip 消失了")
	}
}

// 点胶囊也会把这枚 chip 选中：用户点它就是想看那一家。
func TestPlatformTagSelectsChip(t *testing.T) {
	app, tt := subsApp()

	// 先停在第二枚 chip 上
	clickSubChip(t, tt, "无畏契约")
	if app.SubIndex != 1 {
		t.Fatalf("SubIndex=%d，期望 1", app.SubIndex)
	}

	if err := tt.Click("平台:斗鱼 英雄联盟"); err != nil {
		t.Fatal(err)
	}
	if app.SubIndex != 0 {
		t.Errorf("点胶囊后 SubIndex=%d，期望选中英雄联盟那一枚", app.SubIndex)
	}
	if got := app.subActive[subChipKey("英雄联盟")]; got != "douyu" {
		t.Errorf("活动平台是 %q，期望 douyu", got)
	}
}

// 取消当前正在看的平台：回落到顶栏顺序里的下一家。
func TestUnsubscribeFallsBackToNextPlatform(t *testing.T) {
	app, tt := subsApp()

	if err := tt.Click("取消订阅"); err != nil {
		t.Fatal(err)
	}
	chips := app.subChips()
	if len(chips) != 2 {
		t.Fatalf("取消一家之后 chip 数量变成 %d，期望仍是 2", len(chips))
	}
	if got := app.activeSub(chips[0]).PlatformID; got != "huya" {
		t.Errorf("活动平台是 %q，期望回落到 huya", got)
	}
}

// 取消按钮取消的是「当前 chip 的当前平台」：先切到虎牙再点它，
// 走的应该是虎牙，而不是默认的斗鱼。
func TestUnsubscribeTargetsActivePlatform(t *testing.T) {
	app, tt := subsApp()

	if err := tt.Click("平台:虎牙 英雄联盟"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("取消订阅"); err != nil {
		t.Fatal(err)
	}
	for _, s := range app.Subs {
		if s.PlatformID == "huya" && s.Name == "英雄联盟" {
			t.Fatal("取消的是斗鱼，虎牙那条还在")
		}
	}
	if len(app.Subs) != 2 {
		t.Fatalf("剩 %d 条订阅，期望 2", len(app.Subs))
	}
}

// 一枚 chip 的平台全被取消后，chip 自己消失，选中项收拢。
func TestSubscribeRemoveLastPlatformDropsChip(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "热门", Name: "英雄联盟", CategoryID: "1"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	r, ok := tt.Find("订阅分区:英雄联盟")
	if !ok {
		t.Fatal("找不到 chip")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()

	// 只有一家时不弹菜单，取消订阅走的是空态提示里的路径；
	// 这里直接调数据层，验的是「最后一个成员消失后 chip 也没了」。
	chips := app.subChips()
	app.removeSubPlatform(chips[0], "douyu")
	tt.Frame()

	if n := len(app.subChips()); n != 0 {
		t.Fatalf("取消最后一个平台后还有 %d 枚 chip", n)
	}
	if _, ok := tt.Find("订阅分区:英雄联盟"); ok {
		t.Fatal("chip 消失了但界面上还画着")
	}
	if app.SubIndex != 0 {
		t.Errorf("SubIndex=%d，期望收拢到 0", app.SubIndex)
	}
}

// 只有一家平台时，取消订阅按钮照样在：那是唯一的取消入口。
func TestSinglePlatformChipStillOffersRemove(t *testing.T) {
	app := NewMockApp()
	app.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "热门", Name: "英雄联盟", CategoryID: "1"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	r, ok := tt.Find("订阅分区:英雄联盟")
	if !ok {
		t.Fatal("找不到 chip")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()

	if _, ok := tt.Find("取消订阅"); !ok {
		t.Fatal("单平台 chip 没有取消订阅入口")
	}
	// 平台胶囊仍然可点（虽然只有一家），点了就是选中它
	if _, ok := tt.Find("平台:斗鱼 英雄联盟"); !ok {
		t.Fatal("单平台 chip 没有平台胶囊")
	}
}

// 会员标签不折叠：四个平台都订了也全画出来。
func TestSubscribeChipShowsAllPlatforms(t *testing.T) {
	app := NewMockApp()
	// NewMockApp 会 restore 落盘状态，先清空再摆数据
	app.Subs = nil
	for _, p := range []struct{ id, name string }{
		{"douyu", "斗鱼"}, {"huya", "虎牙"}, {"douyin", "抖音"}, {"bilibili", "B站"},
	} {
		app.Subs = append(app.Subs, Subscription{
			PlatformID: p.id, Platform: p.name,
			Group: "热门", Name: "英雄联盟", CategoryID: "1",
		})
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	chips := app.subChips()
	if len(chips) != 1 || len(chips[0].Subs) != 4 {
		t.Fatalf("chip 数 %d、成员数 %d，期望 1 / 4", len(chips), len(chips[0].Subs))
	}
	if _, ok := tt.Find("订阅分区:英雄联盟"); !ok {
		t.Fatal("chip 没渲染出来")
	}
}

// 各平台自己的分组名不一样（B 站「手游」/ 斗鱼「手游休闲」），
// 取消订阅不能拿分组名去比对，否则会静默失败。
func TestRemoveSubPlatformIgnoresGroupName(t *testing.T) {
	app := NewMockApp()
	app.Subs = nil
	app.Subs = []Subscription{
		{PlatformID: "bilibili", Platform: "B站", Group: "手游", Name: "鸣潮", CategoryID: "3_874"},
		{PlatformID: "douyu", Platform: "斗鱼", Group: "手游休闲", Name: "鸣潮", CategoryID: "3696"},
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	chips := app.subChips()
	if len(chips) != 1 || len(chips[0].Subs) != 2 {
		t.Fatalf("chip 数 %d、成员数 %d，期望 1 / 2", len(chips), len(chips[0].Subs))
	}

	app.removeSubPlatform(chips[0], "douyu")
	tt.Frame()

	if len(app.Subs) != 1 {
		t.Fatalf("取消后剩 %d 条订阅，期望 1", len(app.Subs))
	}
	if app.Subs[0].PlatformID != "bilibili" {
		t.Errorf("留下的是 %q，期望 bilibili", app.Subs[0].PlatformID)
	}
	if n := len(app.subChips()); n != 1 {
		t.Errorf("chip 数变成 %d，期望仍是 1（B 站那家还在）", n)
	}
}

// 悬停在胶囊上放大：视觉变大，同时命中区域跟着变大（Scale 走世界矩阵，
// 见 transform.go 的 contains）。chip 的布局宽度不变——否则 hover 一下
// 滑块就会跟着抖。
func TestPlatformTagGrowsOnHover(t *testing.T) {
	_, tt := subsApp()

	chip, ok := tt.Find("订阅分区:英雄联盟")
	if !ok {
		t.Fatal("找不到 chip")
	}
	tag, ok := tt.Find("平台:斗鱼 英雄联盟")
	if !ok {
		t.Fatal("找不到胶囊")
	}
	// 斗鱼是当前平台；选中胶囊是反相底（AccentText 当底），在 tech-night 里
	// 这块底是深色，压在浅蓝强调上。数整块而不是一行：放大是二维的。
	x0, y0 := int(tag.X)-12, int(tag.Y)-8
	x1, y1 := int(tag.X+tag.W)+12, int(tag.Y+tag.H)+8
	before := darkPixels(tt, x0, y0, x1, y1)

	tt.Move(tag.X+tag.W/2, tag.Y+tag.H/2)
	tt.Advance(600 * time.Millisecond)

	after := darkPixels(tt, x0, y0, x1, y1)
	if after <= before {
		t.Fatalf("悬停后胶囊没有放大：按 %d 个像素 → %d 个", before, after)
	}

	// 布局不动：chip 的矩形在 hover 前后必须完全一致
	if got, ok := tt.Find("订阅分区:英雄联盟"); !ok || got != chip {
		t.Fatalf("悬停让 chip 的布局变了：%+v → %+v", chip, got)
	}
}

// darkPixels 数一块区域里接近墨色的像素：选中胶囊的底是 AccentText。
// tech-night 的卡片底 #20252e 的蓝通道 46，不会被 <40 的阈值算进来。
func darkPixels(tt *ui.Tester, x0, y0, x1, y1 int) int {
	im := tt.Image()
	x0, y0 = max(0, x0), max(0, y0)
	x1, y1 = min(im.Bounds().Dx(), x1), min(im.Bounds().Dy(), y1)
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := im.At(x, y).RGBA()
			if r>>8 < 40 && g>>8 < 40 && b>>8 < 40 {
				n++
			}
		}
	}
	return n
}

// 放大要跟手：180ms 就该到位。之前用的是位移用的那枚弹簧（Slide），
// 在小跨度上尾段会拖到 400ms 以上，鼠标扫过去像慢慢胖起来。
func TestPlatformTagGrowIsQuick(t *testing.T) {
	_, tt := subsApp()
	tag, ok := tt.Find("平台:斗鱼 英雄联盟")
	if !ok {
		t.Fatal("找不到胶囊")
	}

	if d := subTagGrow().Duration; d > 220*time.Millisecond {
		t.Errorf("放大动画 %v，太慢（期望 ≤220ms）", d)
	}

	// 推进 200ms 之后应该基本到位：再推 400ms 不应再有变化。
	tt.Move(tag.X+tag.W/2, tag.Y+tag.H/2)
	tt.Advance(200 * time.Millisecond)
	early, ok := tt.Find("平台:斗鱼 英雄联盟")
	if !ok {
		t.Fatal("放大后胶囊不见了")
	}
	tt.Advance(400 * time.Millisecond)
	late, ok := tt.Find("平台:斗鱼 英雄联盟")
	if !ok {
		t.Fatal("胶囊不见了")
	}
	if d := early.W - late.W; d > 0.5 || d < -0.5 {
		t.Errorf("200ms 时宽 %.2f、600ms 时宽 %.2f：动画还没走完", early.W, late.W)
	}
}
