package theme

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// WCAG 2.2 的对比度：文字 4.5:1，非文字的控件形状 3:1。
//
// 这条测试存在的理由：六套配色是手写的十六进制，改一处不影响别处，
// 于是 TextMeta 在每一套里都至少挂一个表面。人眼看不出来，读屏和低视力
// 用户看得出来。颜色可以审美，读数不能。
const (
	textMin    = 4.5
	graphicMin = 3.0
)

// contrast 是 WCAG 2.x §1.4.3 的 (L1+0.05)/(L2+0.05)。
func contrast(a, b ui.Color) float64 {
	la, lb := relLum(a), relLum(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relLum(c ui.Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// 文字实际会落在哪些表面上。
//
// 不是所有文字都上所有表面：主/次文字会出现在按钮的填充上，三级和元数据
// 只出现在平静的表面（地、卡片、面板、浅填充）。这个差别是设计事实，
// 不是给失败找台阶——FillSecondary 是按钮悬停色，那上面只有主文字。
var (
	calmSurfaces = func(p *Palette) []struct {
		name string
		c    ui.Color
	} {
		return []struct {
			name string
			c    ui.Color
		}{
			{"BgPrimary", p.BgPrimary},
			{"BgSecondary", p.BgSecondary},
			{"BgTertiary", p.BgTertiary},
			{"BgElevated", p.BgElevated},
			{"SurfaceChrome", p.SurfaceChrome},
			{"FillQuaternary", p.FillQuaternary},
		}
	}
	allSurfaces = func(p *Palette) []struct {
		name string
		c    ui.Color
	} {
		all := calmSurfaces(p)
		return append(all,
			struct {
				name string
				c    ui.Color
			}{"FillTertiary", p.FillTertiary},
			struct {
				name string
				c    ui.Color
			}{"FillSecondary", p.FillSecondary},
			struct {
				name string
				c    ui.Color
			}{"HoverBg", p.HoverBg},
		)
	}
)

func TestPaletteContrast(t *testing.T) {
	for i := range Palettes {
		audit(t, &Palettes[i], textMin, textMin, textMin, graphicMin)
	}
}

// 高对比模式：正文要 7:1，其余不低于普通模式的线。
//
// 自绘界面不会自动跟随系统的高对比设置，得自己接。这条测试是那个开关的
// 另一半——只把开关接上、颜色不动，等于没接。
func TestHighContrastPaletteContrast(t *testing.T) {
	for i := range hcPalettes {
		audit(t, &hcPalettes[i], 7.0, 7.0, textMin, 4.0)
	}
}

func audit(t *testing.T, p *Palette, primaryMin, secondaryMin, calmMin, lineMin float64) {
	t.Helper()
	check := func(surfaceName string, surface ui.Color, inkName string, ink ui.Color, min float64) {
		t.Helper()
		if r := contrast(ink, surface); r < min {
			t.Errorf("%s: %s 在 %s 上 %0.2f:1，低于 %.1f", p.ID, inkName, surfaceName, r, min)
		}
	}
	for _, s := range allSurfaces(p) {
		check(s.name, s.c, "TextPrimary", p.TextPrimary, primaryMin)
		check(s.name, s.c, "TextSecondary", p.TextSecondary, secondaryMin)
	}
	for _, s := range calmSurfaces(p) {
		check(s.name, s.c, "TextTertiary", p.TextTertiary, calmMin)
		check(s.name, s.c, "TextMeta", p.TextMeta, calmMin)
	}
	// 强调色当字用（AccentInk）。强调填充上的字由 TestAccentTextPolarity 管：
	// 那里有逐主题覆盖和普通模式的 2.5/3.0 例外，不混进这张通用表。
	check("BgPrimary", p.BgPrimary, "AccentInk", p.AccentInk, textMin)
	check("BgSecondary", p.BgSecondary, "AccentInk", p.AccentInk, textMin)
	// 强调色当线条用（描边、指示器、滑块）是图形。
	check("BgPrimary", p.BgPrimary, "AccentLine", p.AccentLine, lineMin)
	check("BgSecondary", p.BgSecondary, "AccentLine", p.AccentLine, lineMin)
}

// 明度阶梯必须单调：地 < 卡片 < 三级填充（深色），浅色反过来。
// 六套配色以前是复制粘贴的，ember 的卡片比它的地更冷——这条能抓住那种事。
func TestPaletteLadderIsMonotonic(t *testing.T) {
	for i := range Palettes {
		p := &Palettes[i]
		monotonic(t, p)
	}
	for i := range hcPalettes {
		monotonic(t, &hcPalettes[i])
	}
}

func monotonic(t *testing.T, p *Palette) {
	t.Helper()
	ground, surface, deep := relLum(p.BgPrimary), relLum(p.BgSecondary), relLum(p.BgTertiary)
	// 卡片永远比地亮：深色主题靠提亮分层，浅色主题靠白卡片浮起来。
	if !(surface > ground) {
		t.Errorf("%s: 卡片没有比地亮 %.4f / %.4f", p.ID, ground, surface)
	}
	// 三级填充是"再往里凹一档"：深色里更亮，浅色里更暗。
	if p.Dark && !(deep > surface) {
		t.Errorf("%s: 深色的三级填充没有比卡片更亮 %.4f / %.4f", p.ID, surface, deep)
	}
	if !p.Dark && !(deep < ground) {
		t.Errorf("%s: 浅色的三级填充没有比地更暗 %.4f / %.4f", p.ID, ground, deep)
	}
	// 文字阶梯：正文 → 次级 → 三级 → 元数据，单调地远离正文。
	if p.Dark {
		if !(relLum(p.TextPrimary) > relLum(p.TextSecondary) && relLum(p.TextSecondary) > relLum(p.TextTertiary) && relLum(p.TextTertiary) > relLum(p.TextMeta)) {
			t.Errorf("%s: 文字阶梯不单调", p.ID)
		}
		return
	}
	if !(relLum(p.TextPrimary) < relLum(p.TextSecondary) && relLum(p.TextSecondary) < relLum(p.TextTertiary) && relLum(p.TextTertiary) < relLum(p.TextMeta)) {
		t.Errorf("%s: 文字阶梯不单调", p.ID)
	}
}

// 一套配色的地、卡片、三级填充必须是同一个色系。
//
// 这是复制粘贴留下的伤：ember 的地是暖黑，卡片却是 #14161b 那块冷蓝黑，
// 两个色相相差一百多度。明度阶梯查不出来，色相查得出来。
func TestSurfaceFamilySharesHue(t *testing.T) {
	for i := range Palettes {
		p := &Palettes[i]
		base, chroma, ok := oklabHue(p.BgPrimary)
		if !ok {
			continue
		}
		for _, s := range []struct {
			name string
			c    ui.Color
		}{{"BgSecondary", p.BgSecondary}, {"BgTertiary", p.BgTertiary}, {"FillQuaternary", p.FillQuaternary}} {
			h, c, ok := oklabHue(s.c)
			if !ok || c < 0.008 {
				continue // 无彩色：色相没有意义
			}
			if d := math.Abs(h - base); d > 20 && 360-d > 20 {
				t.Errorf("%s: %s 的色相 %.0f° 和地的 %.0f° 差太远（彩度 %.3f / %.3f）",
					p.ID, s.name, h, base, c, chroma)
			}
		}
	}
}

// oklabHue 返回颜色的 Oklab 色相（度）与彩度。
func oklabHue(c ui.Color) (float64, float64, bool) {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	r, g, b := lin(c.R), lin(c.G), lin(c.B)
	l := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	m := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	s := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	a := 1.9779984951*l - 2.4285922050*m + 0.4505937099*s
	bb := 0.0259040371*l + 0.7827717662*m - 0.8086757660*s
	chroma := math.Hypot(a, bb)
	h := math.Atan2(bb, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return h, chroma, true
}

// 窗口在视图跑起来之前先铺一层底色，那个值是 main.go 里的字面量。
// 它必须和默认配色的地一模一样，否则第一帧和窗口底色之间有接缝。
//
// 这里去读 main.go，不复制那个字符串——复制一份就会有一份先过期。
func TestDefaultGroundMatchesWindow(t *testing.T) {
	src, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`BackgroundColor:\s*"(#[0-9a-fA-F]{6})"`).FindSubmatch(src)
	if m == nil {
		t.Fatal("main.go 里找不到 BackgroundColor 字面量")
	}
	if got, want := hexOf(Palettes[0].BgPrimary), strings.ToLower(string(m[1])); got != want {
		t.Errorf("默认配色的地是 %s，main.go 的窗口底色是 %s：两者必须一样，否则首帧和窗口之间有接缝", got, want)
	}
}

// 强调色不能只当填充：浅色主题里 #d8ff4f 压在白卡片上只有 1.1:1，
// 它作为描边、指示器时是看不见的。AccentLine 是同一支色的"当线条用"版本。
func TestAccentLineIsVisible(t *testing.T) {
	for i := range Palettes {
		p := &Palettes[i]
		for _, s := range []struct {
			name string
			c    ui.Color
		}{{"BgPrimary", p.BgPrimary}, {"BgSecondary", p.BgSecondary}} {
			if r := contrast(p.AccentLine, s.c); r < graphicMin {
				t.Errorf("%s: AccentLine 在 %s 上 %0.2f:1，低于 %.1f", p.ID, s.name, r, graphicMin)
			}
		}
	}
}

func TestEveryPaletteHasUniqueID(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Palettes {
		if seen[p.ID] {
			t.Errorf("重复的配色 ID %q", p.ID)
		}
		seen[p.ID] = true
		if p.Swatch[0] == "" || p.Swatch[2] == "" {
			t.Errorf("%s: 预览色没填", p.ID)
		}
	}
	_ = fmt.Sprint
}
