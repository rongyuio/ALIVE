package ui

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

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
