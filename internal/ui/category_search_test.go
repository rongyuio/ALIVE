package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 分区搜索框拿到焦点就展开，176 → 220，240ms ease-out。
func TestCategorySearchExpandsOnFocus(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	before, ok := tt.Find("分区搜索框")
	if !ok {
		t.Fatal("找不到分区搜索框")
	}
	if err := tt.Click("搜索分区"); err != nil {
		t.Fatal(err)
	}
	// 只聚焦不应该弹结果浮层（渲染条件是 query 非空）
	if tt.HasText("没有匹配的分区") {
		t.Error("空查询下出现了「没有匹配的分区」")
	}

	tt.Advance(120 * time.Millisecond)
	mid, ok := tt.Find("分区搜索框")
	if !ok {
		t.Fatal("聚焦后找不到分区搜索框")
	}
	tt.Advance(300 * time.Millisecond)
	after, _ := tt.Find("分区搜索框")

	t.Logf("before=%.0f mid=%.0f after=%.0f", before.W, mid.W, after.W)
	if !app.CategorySearchOpen {
		t.Error("聚焦后没有展开")
	}
	if after.W < catSearchWide-1 {
		t.Errorf("展开后宽度 %.0f，期望 220", after.W)
	}
	if mid.W < before.W || mid.W > after.W {
		t.Errorf("120ms 时宽度 %.0f 不在 %.0f 与 %.0f 之间：没有过渡",
			mid.W, before.W, after.W)
	}
}

// 空查询不弹可见面板；输入才出现；点外部后输入框收回。
func TestCategorySearchPanelNeedsQuery(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	if err := tt.Click("搜索分区"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText("没有匹配的分区") {
		t.Fatal("空查询下出现了结果面板")
	}

	tt.Type("英雄")
	if !tt.HasText("英雄联盟") {
		t.Fatal("输入关键词后没有结果")
	}

	// 点分区栏的另一个 tab（输入框外部）：浮层关闭、输入框收回
	if err := tt.Click("网游"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if app.CategorySearchOpen {
		t.Error("点外部后浮层仍开着")
	}
	f, ok := tt.Find("分区搜索框")
	if !ok {
		t.Fatal("找不到分区搜索框")
	}
	if f.W > catSearchW+1 {
		t.Errorf("点外部后输入框没有收回：宽 %.0f", f.W)
	}
}
