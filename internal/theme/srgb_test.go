package theme

// 声明色必须落在 sRGB 内。
//
// ui.Oklch 先把颜色映射到 Display P3 再转 sRGB；Windows 拿到的是夹回来的
// 字节。声明一个屏幕显示不出来的颜色，令牌和屏幕就会悄悄说两套话。
// 这条测试把声明的 OKLCH 直接映射到 sRGB，和 ui.Oklch 的实际字节比 ΔE00。

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSpecColorsAreInSRGB(t *testing.T) {
	for _, s := range specs {
		for _, it := range []struct {
			name    string
			l, c, h float32
		}{
			{"地", s.GroundL, s.GroundC, s.GroundH},
			{"强调", s.AccentL, s.AccentC, s.AccentH},
			{"正文", s.Ink, s.InkC, s.InkH},
		} {
			got := ui.Oklch(it.l, it.c, it.h)
			want := srgbFromOKLCH(it.l, it.c, it.h)
			if d := comfortDE00(got, want); d >= 1 {
				t.Errorf("%s/%s: 声明值与 sRGB 字节差 ΔE00 %.2f（%.2f %.3f %.0f）",
					s.ID, it.name, d, it.l, it.c, it.h)
			}
		}
	}
}

func srgbFromOKLCH(l, c, h float32) ui.Color {
	inGamut := func(cc float32) bool {
		r, g, b := srgbLinear(l, cc, h)
		const e = 1e-5
		return r >= -e && r <= 1+e && g >= -e && g <= 1+e && b >= -e && b <= 1+e
	}
	lo, hi := float32(0), c
	if inGamut(c) {
		lo = c
	} else {
		for range 24 {
			mid := (lo + hi) / 2
			if inGamut(mid) {
				lo = mid
			} else {
				hi = mid
			}
		}
	}
	r, g, b := srgbLinear(l, lo, h)
	return ui.RGB(srgbEncode(r), srgbEncode(g), srgbEncode(b))
}

func srgbLinear(l, c, h float32) (float64, float64, float64) {
	hr := float64(h) * math.Pi / 180
	a, b := float64(c)*math.Cos(hr), float64(c)*math.Sin(hr)
	L := float64(l)
	l_ := L + 0.3963377774*a + 0.2158037573*b
	m_ := L - 0.1055613458*a - 0.0638541728*b
	s_ := L - 0.0894841775*a - 1.2914855480*b
	ll, mm, ss := l_*l_*l_, m_*m_*m_, s_*s_*s_
	return 4.0767416621*ll - 3.3077115913*mm + 0.2309699292*ss,
		-1.2684380046*ll + 2.6097574011*mm - 0.3413193965*ss,
		-0.0041960863*ll - 0.7034186147*mm + 1.7076147010*ss
}

func srgbEncode(v float64) uint8 {
	v = math.Max(0, math.Min(1, v))
	if v <= 0.0031308 {
		return uint8(math.Round(v * 12.92 * 255))
	}
	return uint8(math.Round((1.055*math.Pow(v, 1/2.4) - 0.055) * 255))
}
