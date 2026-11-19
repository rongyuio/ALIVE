package theme

import (
	"math"
	"testing"
)

// 顶栏托盘只能待在地和卡片之间，而且两边都要看得见。
//
// 它以前用的是 FillQuaternary：那是「卡片上的浅填充」，相对卡片定的。
// 放到地上就只剩 1.9 个 L*——浅色主题里近乎同色，于是只能靠 1px 描边
// 把形状画出来；深色主题里它又跑到地上面，同一个托盘换主题就从「凹槽」
// 变成「凸片」。这条测试把位置钉死：地 < 托盘 < 卡片，两边都够，且等分。
//
// 深色主题的地→卡片整条阶梯只有 5 个 L*，等分后每边就在 2.5 上下；
// 取 2.3 是给 8 位量化留的余量。
const chromeStepMin = 2.3

func TestChromeSitsBetweenGroundAndCard(t *testing.T) {
	check := func(id string, p *Palette) {
		t.Helper()
		g, c, card := labL(p.BgPrimary), labL(p.SurfaceChrome), labL(p.BgSecondary)
		if !(g < c && c < card) {
			t.Fatalf("%s: 托盘 L* %.1f 不在 地 %.1f 与 卡片 %.1f 之间", id, c, g, card)
		}
		low, high := c-g, card-c
		if low < chromeStepMin || high < chromeStepMin {
			t.Errorf("%s: 地→托盘 %.1f、托盘→卡片 %.1f，两边都要 ≥%.1f", id, low, high, chromeStepMin)
		}
		if math.Abs(low-high) > 0.5 {
			t.Errorf("%s: 托盘没有等分阶梯：下 %.1f、上 %.1f", id, low, high)
		}
		// 带 -v 跑就是一张现成的表，doc/reference/palette-ladder.md 抄这里
		t.Logf("%-12s 地 %-8s L*=%5.1f | 托盘 %-8s L*=%5.1f | 卡片 %-8s L*=%5.1f | Δ %4.1f / %4.1f",
			id, hexOf(p.BgPrimary), g, hexOf(p.SurfaceChrome), c, hexOf(p.BgSecondary), card, low, high)
	}
	for i := range Palettes {
		check(Palettes[i].ID, &Palettes[i])
	}
	for i := range hcPalettes {
		check(hcPalettes[i].ID+" (高对比)", &hcPalettes[i])
	}
}
