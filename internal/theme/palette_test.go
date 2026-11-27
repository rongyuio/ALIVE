package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestPalettesIDsAreUnique 配色面板按 ID 找、按 ID 落盘：ID 撞了就会
// 切到"另一套"其实还是这一套，或者读盘读回一个别人。
func TestPalettesIDsAreUnique(t *testing.T) {
	if len(Palettes) == 0 {
		t.Fatal("Palettes 是空的")
	}
	seen := make(map[string]int, len(Palettes))
	for i, p := range Palettes {
		if p.ID == "" {
			t.Errorf("第 %d 套配色没有 ID", i)
			continue
		}
		if prev, dup := seen[p.ID]; dup {
			t.Errorf("ID %q 重复：第 %d 套和第 %d 套", p.ID, prev, i)
		}
		seen[p.ID] = i
	}
}

// TestApplyEveryPalette 把每一套配色都真切一遍。
//
// Apply 会重写包级颜色令牌；哪套配色算出一个零值（透明）令牌，界面里那一块
// 就直接看不见了。这比只测"不 panic"更具体：关键令牌必须真的有颜色。
func TestApplyEveryPalette(t *testing.T) {
	// 切完把当前配色还原，别给同包其它测试留下改过的全局状态。
	defer Apply(Palettes[0].ID)
	SetHighContrast(false)
	defer SetHighContrast(false)

	for _, p := range Palettes {
		t.Run(p.ID, func(t *testing.T) {
			Apply(p.ID)

			if got := Current(); got.ID != p.ID {
				t.Fatalf("Apply(%q) 之后 Current().ID = %q", p.ID, got.ID)
			}
			if Find(p.ID) == nil {
				t.Errorf("Find(%q) 找不到，Palettes 和 Find 的表对不上", p.ID)
			}
			if FindHC(p.ID) == nil {
				t.Errorf("FindHC(%q) 找不到，高对比表缺了这一套", p.ID)
			}
			if p.Name == "" {
				t.Errorf("%q 没有显示名", p.ID)
			}

			// 关键令牌：地面、卡片、正文、次级文字、强调色、危险色。
			// 零值的 ui.Color 是完全透明，画出来等于没有。
			tokens := map[string]ui.Color{
				"BgPrimary":     BgPrimary,
				"BgSecondary":   BgSecondary,
				"BgElevated":    BgElevated,
				"TextPrimary":   TextPrimary,
				"TextSecondary": TextSecondary,
				"Accent":        Accent,
				"AccentInk":     AccentInk,
				"Danger":        Danger,
			}
			for name, c := range tokens {
				if c == (ui.Color{}) {
					t.Errorf("Apply(%q) 之后 %s 是零值（透明）", p.ID, name)
				}
			}
		})
	}
}

// TestApplyUnknownIDFallsBack 落盘里存了个不认识的 ID 时不能把界面刷没：
// 退到第一套配色。
func TestApplyUnknownIDFallsBack(t *testing.T) {
	defer Apply(Palettes[0].ID)
	Apply("no-such-palette")
	if got := Current(); got.ID != Palettes[0].ID {
		t.Errorf("未知 ID 之后 Current().ID = %q，应退回 %q", got.ID, Palettes[0].ID)
	}
	if BgPrimary == (ui.Color{}) {
		t.Error("未知 ID 退回首套配色后 BgPrimary 是零值")
	}
}
