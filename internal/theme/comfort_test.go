package theme

// 舒适契约：所有主题、日夜两种模式一视同仁。
// 数值和理由在 doc/decisions/0008-palette-system.md。
//
// 这不是 WCAG 的替代品：WCAG 是地板，这里是从地板往上要求的舒适区。
// 旧六套在这条测试下会红——日间正文只有 L* 11–13、parchment 的地彩度 0.022、
// 浅色 AccentInk 在 FillSecondary 上只有 3.86–3.98。

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

const (
	comfortBody      = 7.0
	comfortSecondary = 5.5
	comfortTertiary  = 4.8
	comfortMeta      = 4.5
	maxSurfaceChroma = 0.02
	minAdjacentDE    = 1.5
)

func TestComfortContract(t *testing.T) {
	for _, set := range []struct {
		name string
		ps   []Palette
	}{{"普通", Palettes}, {"高对比", hcPalettes}} {
		for i := range set.ps {
			checkComfort(t, set.name, &set.ps[i])
		}
	}
}

func checkComfort(t *testing.T, set string, p *Palette) {
	t.Helper()
	all, calm := probeLikeAllSurfaces(p), probeLikeCalmSurfaces(p)

	gL, tL := labL(p.BgPrimary), labL(p.TextPrimary)
	if p.Dark {
		if gL < 2.5 || gL > 8 {
			t.Errorf("%s/%s: 夜间地 L*=%.1f，不在 2.5–8", set, p.ID, gL)
		}
		if tL < 92 || tL > 97 {
			t.Errorf("%s/%s: 夜间正文 L*=%.1f，不在 92–97", set, p.ID, tL)
		}
	} else {
		if gL < 90 || gL > 95 {
			t.Errorf("%s/%s: 日间地 L*=%.1f，不在 90–95", set, p.ID, gL)
		}
		if tL < 15 || tL > 28 {
			t.Errorf("%s/%s: 日间正文 L*=%.1f，不在 15–28", set, p.ID, tL)
		}
	}

	for _, s := range []struct {
		name string
		c    ui.Color
	}{{"BgPrimary", p.BgPrimary}, {"BgSecondary", p.BgSecondary}, {"SurfaceChrome", p.SurfaceChrome}} {
		if _, c, _ := oklabHue(s.c); c > maxSurfaceChroma {
			t.Errorf("%s/%s: %s 彩度 %.3f > %.2f", set, p.ID, s.name, c, maxSurfaceChroma)
		}
	}

	minOn := func(ink ui.Color, surfaces []namedSurface) float64 {
		m := math.Inf(1)
		for _, s := range surfaces {
			if r := contrast(ink, s.c); r < m {
				m = r
			}
		}
		return m
	}
	if m := minOn(p.TextPrimary, all); m < comfortBody {
		t.Errorf("%s/%s: 正文最低 %.2f < %.1f", set, p.ID, m, comfortBody)
	}
	if m := minOn(p.TextSecondary, all); m < comfortSecondary {
		t.Errorf("%s/%s: 次级最低 %.2f < %.1f", set, p.ID, m, comfortSecondary)
	}
	if m := minOn(p.TextTertiary, calm); m < comfortTertiary {
		t.Errorf("%s/%s: 三级最低 %.2f < %.1f", set, p.ID, m, comfortTertiary)
	}
	if m := minOn(p.TextMeta, calm); m < comfortMeta {
		t.Errorf("%s/%s: 元数据最低 %.2f < %.1f", set, p.ID, m, comfortMeta)
	}
	if m := minOn(p.AccentInk, all); m < 4.5 {
		t.Errorf("%s/%s: AccentInk 最低 %.2f < 4.5（FillSecondary 上最容易挂）", set, p.ID, m)
	}
	// 功能边界：可点控件靠它识别形状，对卡片 ≥3（HC 4）。
	borderMin := 3.0
	if set == "高对比" {
		borderMin = 4.0
	}
	if r := contrast(p.BorderFunctional, p.BgSecondary); r < borderMin {
		t.Errorf("%s/%s: BorderFunctional 对卡片 %.2f < %.1f", set, p.ID, r, borderMin)
	}

	for _, pair := range []struct {
		name string
		a, b ui.Color
	}{
		{"地↔托盘", p.BgPrimary, p.SurfaceChrome},
		{"托盘↔卡片", p.SurfaceChrome, p.BgSecondary},
	} {
		if d := comfortDE00(pair.a, pair.b); d < minAdjacentDE {
			t.Errorf("%s/%s: %s ΔE00 %.2f < %.1f", set, p.ID, pair.name, d, minAdjacentDE)
		}
	}
}

type namedSurface struct {
	name string
	c    ui.Color
}

func probeLikeAllSurfaces(p *Palette) []namedSurface {
	return []namedSurface{
		{"BgPrimary", p.BgPrimary},
		{"BgSecondary", p.BgSecondary},
		{"BgTertiary", p.BgTertiary},
		{"BgElevated", p.BgElevated},
		{"SurfaceChrome", p.SurfaceChrome},
		{"FillQuaternary", p.FillQuaternary},
		{"FillTertiary", p.FillTertiary},
		{"FillSecondary", p.FillSecondary},
		{"HoverBg", p.HoverBg},
	}
}

func probeLikeCalmSurfaces(p *Palette) []namedSurface {
	return []namedSurface{
		{"BgPrimary", p.BgPrimary},
		{"BgSecondary", p.BgSecondary},
		{"BgTertiary", p.BgTertiary},
		{"BgElevated", p.BgElevated},
		{"SurfaceChrome", p.SurfaceChrome},
		{"FillQuaternary", p.FillQuaternary},
	}
}

// comfortDE00 是 CIEDE2000，只给表面台阶用。
func comfortDE00(c1, c2 ui.Color) float64 {
	L1, a1, b1 := comfortLab(c1)
	L2, a2, b2 := comfortLab(c2)
	rad := math.Pi / 180
	C1, C2 := math.Hypot(a1, b1), math.Hypot(a2, b2)
	Cbar := (C1 + C2) / 2
	G := 0.5 * (1 - math.Sqrt(math.Pow(Cbar, 7)/(math.Pow(Cbar, 7)+math.Pow(25, 7))))
	a1p, a2p := (1+G)*a1, (1+G)*a2
	C1p, C2p := math.Hypot(a1p, b1), math.Hypot(a2p, b2)
	hp := func(b, ap float64) float64 {
		if b == 0 && ap == 0 {
			return 0
		}
		h := math.Atan2(b, ap) / rad
		if h < 0 {
			h += 360
		}
		return h
	}
	h1p, h2p := hp(b1, a1p), hp(b2, a2p)
	dLp, dCp := L2-L1, C2p-C1p
	var dhp float64
	if C1p*C2p != 0 {
		dhp = h2p - h1p
		if dhp > 180 {
			dhp -= 360
		} else if dhp < -180 {
			dhp += 360
		}
	}
	dHp := 2 * math.Sqrt(C1p*C2p) * math.Sin(dhp/2*rad)
	Lbarp := (L1 + L2) / 2
	Cbarp := (C1p + C2p) / 2
	var hbarp float64
	if C1p*C2p == 0 {
		hbarp = h1p + h2p
	} else if math.Abs(h1p-h2p) <= 180 {
		hbarp = (h1p + h2p) / 2
	} else if h1p+h2p < 360 {
		hbarp = (h1p + h2p + 360) / 2
	} else {
		hbarp = (h1p + h2p - 360) / 2
	}
	T := 1 - 0.17*math.Cos((hbarp-30)*rad) + 0.24*math.Cos(2*hbarp*rad) +
		0.32*math.Cos((3*hbarp+6)*rad) - 0.20*math.Cos((4*hbarp-63)*rad)
	theta := (hbarp - 275) / 25
	dTheta := 30 * math.Exp(-theta*theta)
	Rc := 2 * math.Sqrt(math.Pow(Cbarp, 7)/(math.Pow(Cbarp, 7)+math.Pow(25, 7)))
	dl := Lbarp - 50
	Sl := 1 + 0.015*(dl*dl)/math.Sqrt(20+dl*dl)
	Sc := 1 + 0.045*Cbarp
	Sh := 1 + 0.015*Cbarp*T
	Rt := -math.Sin(2*dTheta*rad) * Rc
	tl, tc, th := dLp/Sl, dCp/Sc, dHp/Sh
	return math.Sqrt(tl*tl + tc*tc + th*th + Rt*tc*th)
}

func comfortLab(c ui.Color) (L, a, b float64) {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, bl := lin(c.R), lin(c.G), lin(c.B)
	x := (0.4124564*r + 0.3575761*g + 0.1804375*bl) / 0.95047
	y := 0.2126729*r + 0.7151522*g + 0.0721750*bl
	z := (0.0193339*r + 0.1191920*g + 0.9503041*bl) / 1.08883
	f := func(t float64) float64 {
		if t > 0.008856 {
			return math.Cbrt(t)
		}
		return (903.3*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}
