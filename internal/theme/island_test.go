package theme

import "testing"

// 环画在岛的墨底上，不能复用对 BgPrimary 推导的那两支强调色：浅色主题里
// 它们都被压深过去当字/当线，压到 #060709 上就什么都看不见了。
func TestAccentOnInkIsVisible(t *testing.T) {
	for _, set := range [][]Palette{Palettes, hcPalettes} {
		for i := range set {
			p := &set[i]
			if c := contrast(p.AccentOnInk, IslandInk); c < islandRingMin {
				t.Fatalf("%s: 环对岛面只有 %.2f:1，要求 >=%.1f:1", p.ID, c, islandRingMin)
			}
		}
	}
}
