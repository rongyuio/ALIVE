package ui

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 点「已经选中的」分区必须是空操作：不能换代、不能重铺内容层。
//
// 一级/二级 chip 以前无条件 navFlip：点同一个分区也会把内容层整棵子树换掉
// （feed key 变、列表重拉），页面上的卡片全部重建一次——用户看到的是
// 「同一个分区点一下内容重铺」，而且按下与松手落在不同元素上时那一下点击
// 直接丢失。三级 chip 本来就有这条保护，一级/二级漏了。
func TestReselectingSameCategoryKeepsFeedKey(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	key := app.navFeedKey()
	seq := app.navSeq

	// 默认选中的就是第一个二级分区（英雄联盟）。
	if err := tt.Click("分类:英雄联盟"); err != nil {
		t.Fatalf("点当前分区失败: %v", err)
	}
	tt.Frame()
	if app.navSeq != seq || app.navFeedKey() != key {
		t.Fatalf("点已选中的二级分区仍然换代：seq %d→%d，key %q→%q",
			seq, app.navSeq, key, app.navFeedKey())
	}

	// 换到另一个二级分区：应当换代一次。
	if err := tt.Click("分类:无畏契约"); err != nil {
		t.Fatalf("切分区失败: %v", err)
	}
	tt.Frame()
	seq2, key2 := app.navSeq, app.navFeedKey()
	if seq2 == seq {
		t.Fatal("切到另一个分区没有换代")
	}

	// 再点一次当前分区：空操作。
	if err := tt.Click("分类:无畏契约"); err != nil {
		t.Fatalf("再点当前分区失败: %v", err)
	}
	tt.Frame()
	if app.navSeq != seq2 || app.navFeedKey() != key2 {
		t.Fatalf("重复点当前分区又换代了：seq %d→%d", seq2, app.navSeq)
	}
}

// 一级分区同理：点当前这一组不换代。
func TestReselectingSameGroupKeepsFeedKey(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	key := app.navFeedKey()
	seq := app.navSeq
	if err := tt.Click("分区:热门"); err != nil {
		t.Fatalf("点当前一级分区失败: %v", err)
	}
	tt.Frame()
	if app.navSeq != seq || app.navFeedKey() != key {
		t.Fatalf("点已选中一级分区仍然换代：seq %d→%d", seq, app.navSeq)
	}
}

// roomsCountingProvider 只回答「有没有被问到列表」。
type roomsCountingProvider struct{ rooms int }

func (p *roomsCountingProvider) Platform() live.Platform { return live.Bilibili }
func (p *roomsCountingProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return []live.CategoryGroup{{Name: "g", Categories: []live.Category{
		{ID: "a", Name: "a"}, {ID: "b", Name: "b"},
	}}}, nil
}
func (p *roomsCountingProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	p.rooms++
	return live.Page{}, nil
}
func (p *roomsCountingProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

// 空态里的「查看全部分区」必须真的重新取列表。
//
// 以前它只把 SubCatIndex 设成 -1，不调 reload：按钮点了没有任何反应，而
// 它恰好是空态里给用户的唯一出路。控件在、命中也在，动作没接上——可达性缺陷。
func TestEmptyGridShowAllCategoriesReloads(t *testing.T) {
	app := NewMockApp()
	app.LiveMode = true
	app.Win = nil
	prov := &roomsCountingProvider{}
	old := providerFor
	providerFor = func(live.Platform) (live.Provider, bool) { return prov, true }
	t.Cleanup(func() { providerFor = old })

	app.Groups = []CategoryGroup{{Name: "g", Categories: []string{"a", "b"}, CategoryIDs: []string{"a", "b"}}}
	app.GroupIndex, app.CategoryIndex, app.SubCatIndex = 0, 0, 1
	app.Rooms = nil
	app.ListLoading = false
	// 让 ensureLiveData 认为各平台的分区树都已经拉过，否则 Home 会先画骨架。
	app.liveLoaded = map[string]bool{}
	for _, id := range app.PlatformOrder {
		app.liveLoaded[id] = true
	}

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if err := tt.Click("查看全部分区"); err != nil {
		t.Fatalf("空态里点不到「查看全部分区」: %v", err)
	}
	tt.Frame()

	if app.SubCatIndex != -1 {
		t.Fatalf("点了「查看全部分区」，SubCatIndex=%d（期望 -1）", app.SubCatIndex)
	}
	if prov.rooms == 0 {
		waitFor(t, func() bool { return prov.rooms > 0 })
	}
}
