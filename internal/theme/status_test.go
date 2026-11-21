package theme

// 状态色三支的兜底：ink 当字、line 当图形、fill 当点和底。
// 阈值和 0008 的舒适契约一致：普通 ink ≥4.5，高对比 ink ≥7；
// line 普通 ≥3、高对比 ≥4；fill 两端都 ≥3。
// 在线绿是固定值，单独验；成功 / 危险仍按配色推导。

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestStatusTriad(t *testing.T) {
	// 在线绿是固定值：十二套配色、日夜、高对比都同一支。它压在画面和
	// 墨底上，不压在平静表面上，所以只验对墨底的 3:1，不参与下面那组
	// 「当字用」的 4.5 检查。
	live := Palettes[0].StatusOnline
	for _, set := range [][]Palette{Palettes, hcPalettes} {
		for i := range set {
			p := &set[i]
			if p.StatusOnline != live || p.StatusOnlineInk != live || p.StatusOnlineLine != live {
				t.Errorf("%s: 在线绿跟着配色变了：%s / %s / %s，期望 %s", p.ID,
					hexOf(p.StatusOnline), hexOf(p.StatusOnlineInk),
					hexOf(p.StatusOnlineLine), hexOf(live))
			}
		}
	}
	if r := contrast(live, IslandInk); r < 3 {
		t.Errorf("在线绿对墨底 %.2f < 3", r)
	}

	for _, set := range []struct {
		name    string
		ps      []Palette
		inkMin  float64
		lineMin float64
	}{
		{"普通", Palettes, 4.5, 3.0},
		{"高对比", hcPalettes, 7.0, 4.0},
	} {
		for i := range set.ps {
			p := &set.ps[i]
			calm := probeLikeCalmSurfaces(p)
			for _, s := range []struct {
				name          string
				fill, ink, ln ui.Color
			}{
				{"成功", p.Success, p.SuccessInk, p.SuccessLine},
				{"危险", p.Danger, p.DangerInk, p.DangerLine},
			} {
				if m := statusMin(s.ink, calm); m < set.inkMin {
					t.Errorf("%s/%s: %s ink %.2f < %.1f", set.name, p.ID, s.name, m, set.inkMin)
				}
				if m := statusMin(s.ln, calm); m < set.lineMin {
					t.Errorf("%s/%s: %s line %.2f < %.1f", set.name, p.ID, s.name, m, set.lineMin)
				}
				if m := statusMin(s.fill, calm); m < 3.0 {
					t.Errorf("%s/%s: %s fill %.2f < 3", set.name, p.ID, s.name, m)
				}
				if d := comfortDE00(s.ink, p.AccentInk); d < 5 {
					t.Errorf("%s/%s: %s 与强调 Ink 的 ΔE00 %.2f < 5：语义会撞色",
						set.name, p.ID, s.name, d)
				}
				for k, kind := range []string{"protanopia", "deuteranopia", "tritanopia"} {
					for _, sfc := range calm {
						r := contrast(cvdSim(s.ink, k), cvdSim(sfc.c, k))
						if r < 4.5 {
							t.Errorf("%s/%s: %s 在 %s 下对 %s 只有 %.2f",
								set.name, p.ID, s.name, kind, sfc.name, r)
						}
					}
				}
			}
		}
	}
}

func statusMin(ink ui.Color, surfaces []namedSurface) float64 {
	m := math.Inf(1)
	for _, s := range surfaces {
		if r := contrast(ink, s.c); r < m {
			m = r
		}
	}
	return m
}
