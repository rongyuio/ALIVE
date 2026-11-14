package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 点击胶囊后必须真的长大：收起 100 宽，中途介于两者之间，最终接近 1000。
// （曾经因为展开时兄弟节点位置变化丢了 AnimateTo 状态，面板瞬间跳到终态。）
func TestIslandExpansionAnimates(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	closed, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到灵动岛")
	}

	app.IslandOpen = true
	app.IslandSeq++
	tt.Frame()

	tt.Advance(150 * time.Millisecond)
	mid, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("展开后找不到灵动岛")
	}

	// baseline 是 340ms 的曲线，400ms 时应当已经收敛
	tt.Advance(260 * time.Millisecond)
	full, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("展开完成找不到灵动岛")
	}

	t.Logf("close=%.0f mid=%.0f full=%.0f", closed.W, mid.W, full.W)
	if !(mid.W > closed.W && mid.W < full.W) {
		t.Errorf("150ms 时宽度 %.0f 不在收起 %.0f 与终态 %.0f 之间：展开没有动画",
			mid.W, closed.W, full.W)
	}
	if full.W < 990 {
		t.Errorf("340ms 后宽度只有 %.0f，期望接近 1000（baseline 的曲线是 340ms）", full.W)
	}
}

// 面板内搜索：按昵称包含匹配，副标题切到「N 位匹配」，清空后恢复。
func TestIslandSearch(t *testing.T) {
	app := NewMockApp()
	app.IslandOpen = true
	app.IslandSeq++
	tt := ui.NewTester(app.Shell, 1440, 900)

	before := len(app.islandList())
	if err := tt.Click("搜索已关注的主播"); err != nil {
		t.Fatal(err)
	}
	tt.Type("PDD")

	got := app.islandList()
	t.Logf("before=%d after=%d", before, len(got))
	if len(got) == 0 || len(got) >= before {
		t.Fatalf("搜索 PDD 后列表 %d 条（之前 %d）", len(got), before)
	}
	for _, s := range got {
		if !strings.Contains(s.Name, "PDD") {
			t.Errorf("列表里混入不匹配的主播 %q", s.Name)
		}
	}
	if !tt.HasText("位匹配") {
		t.Error("副标题没有切到「N 位匹配」")
	}

	app.IslandQuery = ""
	if len(app.islandList()) != before {
		t.Error("清空搜索后没有恢复全量列表")
	}
}
