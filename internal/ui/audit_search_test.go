package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// H2 回归：指针停在搜索结果上时，键盘 ↑↓ 不能被 hover 覆盖。
//
// 旧实现把 `if row.Hovered() { a.SearchHit = i }` 写在渲染路径里，指针不动
// 就每帧把键盘刚改好的选中项写回悬停项——用户看到的是「按 ↑↓ 没反应，
// 鼠标挪开又好了」。
func TestSearchHoverDoesNotStealKeyboard(t *testing.T) {
	app := NewMockApp()
	app.LiveMode = true
	app.SearchOpen = true
	app.SearchQuery = "x"
	app.SearchResults = []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "甲"},
		{Platform: "bilibili", RoomID: "2", Name: "乙"},
		{Platform: "bilibili", RoomID: "3", Name: "丙"},
	}
	app.SearchHit = 0
	// syncSearch 在查询词变化时会清空旧结果；把「上次搜索的词/平台」先对齐，
	// 这一帧才不会把刚 seed 的结果清掉。
	app.searchQuery = app.SearchQuery
	app.searchPlatform = app.viewPlatform()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	b, ok := tt.Find("搜索结果:乙")
	if !ok {
		t.Fatal("找不到第二条搜索结果")
	}
	tt.Move(b.X+b.W/2, b.Y+b.H/2)
	tt.Frame() // 这一帧指针悬停在第二条上
	tt.Frame()
	if app.SearchHit != 0 {
		t.Fatalf("悬停把选中项写走了：SearchHit=%d（期望仍是 0）", app.SearchHit)
	}
	tt.Key(0, ui.KeyDown)
	if app.SearchHit != 1 {
		t.Fatalf("指针停在列表上时 ↑↓ 失效：SearchHit=%d（期望 1）", app.SearchHit)
	}
}

// H3 回归：分区搜索 Esc 之后，下一帧不能又被「聚焦即展开」翻回来。
//
// Handle.CancelFocus 只清待处理的聚焦请求、放不掉已生效焦点，所以旧写法
// （每帧 `if field.FocusWithin() { CategorySearchOpen = true }`）会让 Esc
// 永远关不掉搜索框。
func TestCategorySearchEscapeStaysClosed(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("搜索分区"); err != nil {
		t.Fatal(err)
	}
	tt.Type("英雄")
	tt.Advance(300 * time.Millisecond)

	tt.Key(0, ui.KeyEscape)
	// 多走两帧：旧实现会在 Esc 之后的下一帧按 FocusWithin 重新展开。
	tt.Frame()
	tt.Frame()
	if app.CategorySearchOpen {
		t.Fatal("Esc 之后搜索框又被「聚焦即展开」翻回来了")
	}
	tt.Advance(300 * time.Millisecond)
	f, ok := tt.Find("分区搜索框")
	if !ok {
		t.Fatal("找不到分区搜索框")
	}
	if f.W > catSearchW+1 {
		t.Fatalf("Esc 之后搜索框没有收回：宽 %.0f", f.W)
	}
}
