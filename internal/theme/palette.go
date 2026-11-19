package theme

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Palette 是一套完整配色：不是只换强调色，而是六套成对（同色系日夜各一套）的完整配色。
//
// 这些值不再是手写的十六进制。每套配色只声明六个输入（地、四档文字、强调色），
// 其余由 build 推导：表面沿一条明度阶梯抬升，文字沿另一条收敛，强调色按对比度
// 自动算出"当字用"和"当线用"两个版本。手写二十个十六进制的问题是它一定会走样
// ——六套里有四套的卡片色是从 signal 复制过来的，ember 暖黑的地配了冷蓝黑的卡。
type Palette struct {
	ID          string
	Name        string
	Description string
	Dark        bool
	// Swatch 是配色面板里的三色预览：背景 / 表面 / 强调色
	Swatch [3]string

	BgPrimary, BgSecondary, BgTertiary, BgElevated ui.Color

	// SurfaceChrome 是顶栏托盘：地和卡片的明度中点，由阶梯推导，不手写。
	SurfaceChrome ui.Color

	FillQuaternary, FillTertiary, FillSecondary   ui.Color
	HoverBg                                       ui.Color
	TextPrimary, TextSecondary, TextTertiary      ui.Color
	TextMeta                                      ui.Color
	Accent, AccentHover, AccentActive, AccentText ui.Color
	// AccentInk 是强调色当文字用（≥4.5:1），AccentLine 是当线条用（≥3:1）。
	// 浅色主题里青柠压在白底上只有 1.1:1，这两个版本是同一支色的两种用途。
	AccentInk    ui.Color
	AccentLine   ui.Color
	AccentSoft   ui.Color
	AccentSofter ui.Color
	AccentGlow   ui.Color
	Border       ui.Color
	BorderStrong ui.Color
	Separator    ui.Color
	// StatusOffline 是离线态的点：无彩色，明度跟着文字元数据走。
	StatusOffline ui.Color
	// TopLight 是平涂表面顶部那 1px 受光边；深色主题是白，浅色主题是极淡的墨色。
	TopLight ui.Color
}

// spec 是生成一套配色的全部输入，单位都是 Oklch 的 L 与 C、h。
//
// 只有地和强调色是"这一套和那一套不一样"的地方；文字四档和表面阶梯是
// 全系统共用的结构，只按深/浅换一套数值。
type spec struct {
	ID          string
	Name        string
	Description string
	Dark        bool

	GroundL, GroundC, GroundH float32
	Ink, Sub, Tert, Meta      float32
	InkC, InkH                float32

	AccentL, AccentC, AccentH float32
}

// 表面阶梯：相对地的明度抬升。深色靠提亮分层，浅色靠压暗分层。
const (
	darkSurface   float32 = 0.058
	darkTertiary  float32 = 0.105
	darkElevated  float32 = 0.088
	darkFillQ     float32 = 0.048
	darkFillT     float32 = 0.078
	darkFillS     float32 = 0.132
	darkBorder    float32 = 0.105
	darkBorderStr float32 = 0.155
	darkSeparator float32 = 0.075

	lightSurface   float32 = 0.062
	lightTertiary  float32 = -0.026
	lightElevated  float32 = 0.050
	lightFillQ     float32 = -0.016
	lightFillT     float32 = -0.034
	lightFillS     float32 = -0.064
	lightBorder    float32 = -0.078
	lightBorderStr float32 = -0.115
	lightSeparator float32 = -0.042
)

// specs 按「同色系日夜一行」排列，和配色面板的格子顺序一致。
var specs = []spec{
	{
		ID: "signal", Name: "青柠·夜", Description: "石墨黑 + 信号青柠，默认夜间配色",
		Dark:    true,
		GroundL: 0.150, GroundC: 0.004, GroundH: 255,
		Ink: 0.97, Sub: 0.82, Tert: 0.73, Meta: 0.665, InkC: 0.006, InkH: 95,
		AccentL: 0.955, AccentC: 0.208, AccentH: 123,
	},
	{
		ID: "daylight", Name: "青柠·日", Description: "4.x 原版浅色 + 信号青柠",
		Dark:    false,
		GroundL: 0.940, GroundC: 0.003, GroundH: 255,
		Ink: 0.22, Sub: 0.34, Tert: 0.415, Meta: 0.47, InkC: 0.006, InkH: 255,
		AccentL: 0.955, AccentC: 0.208, AccentH: 123,
	},
	{
		ID: "mono", Name: "黑白灰·夜", Description: "纯灰黑 + 白色强调，极简无彩色",
		Dark:    true,
		GroundL: 0.145, GroundC: 0, GroundH: 0,
		Ink: 0.97, Sub: 0.82, Tert: 0.73, Meta: 0.665, InkC: 0, InkH: 0,
		AccentL: 0.915, AccentC: 0, AccentH: 0,
	},
	{
		ID: "paper", Name: "黑白灰·日", Description: "纸白 + 墨黑强调，极简无彩色",
		Dark:    false,
		GroundL: 0.940, GroundC: 0, GroundH: 0,
		Ink: 0.22, Sub: 0.34, Tert: 0.415, Meta: 0.47, InkC: 0, InkH: 0,
		AccentL: 0.210, AccentC: 0.006, AccentH: 278,
	},
	{
		ID: "ember", Name: "琥珀·夜", Description: "暖炭黑 + 琥珀橙",
		Dark:    true,
		GroundL: 0.155, GroundC: 0.012, GroundH: 62,
		Ink: 0.97, Sub: 0.82, Tert: 0.73, Meta: 0.665, InkC: 0.010, InkH: 70,
		AccentL: 0.830, AccentC: 0.158, AccentH: 56,
	},
	{
		ID: "parchment", Name: "书页·日", Description: "米纸底 + 赭石橙，书本纸张质感",
		Dark:    false,
		GroundL: 0.932, GroundC: 0.022, GroundH: 78,
		Ink: 0.26, Sub: 0.37, Tert: 0.44, Meta: 0.49, InkC: 0.012, InkH: 60,
		AccentL: 0.550, AccentC: 0.130, AccentH: 50,
	},
}

// Palettes 是 specs 的展开结果。
var Palettes = buildAll(false)

// hcPalettes 是同一份 specs 的高对比版本，下标与 Palettes 一一对应。
var hcPalettes = buildAll(true)

func buildAll(hc bool) []Palette {
	out := make([]Palette, len(specs))
	for i, s := range specs {
		if hc {
			out[i] = s.buildHC()
			continue
		}
		out[i] = s.build()
	}
	return out
}

// build 把六个输入展开成二十个令牌。
// ladder 是一套主题模式下表面与文字的阶梯参数。
//
// 普通模式和高对比模式用同一份结构、不同的数：高对比把文字往正文收、
// 把描边升成强描边，层级从"明度拉开"改成"字重和字号拉开"。
type ladder struct {
	surface, tertiary, elevated, fillQ, fillT, fillS float32
	border, borderStrong, separator                  float32
	sub, tert, meta                                  float32
	accentInkMin, accentLineMin                      float64
}

func (s spec) ladder(hc bool) ladder {
	var l ladder
	if s.Dark {
		l = ladder{
			surface: darkSurface, tertiary: darkTertiary, elevated: darkElevated,
			fillQ: darkFillQ, fillT: darkFillT, fillS: darkFillS,
			border: darkBorder, borderStrong: darkBorderStr, separator: darkSeparator,
			sub: s.Sub, tert: s.Tert, meta: s.Meta,
			accentInkMin: 4.7, accentLineMin: 3.2,
		}
	} else {
		l = ladder{
			surface: lightSurface, tertiary: lightTertiary, elevated: lightElevated,
			fillQ: lightFillQ, fillT: lightFillT, fillS: lightFillS,
			border: lightBorder, borderStrong: lightBorderStr, separator: lightSeparator,
			sub: s.Sub, tert: s.Tert, meta: s.Meta,
			accentInkMin: 4.7, accentLineMin: 3.2,
		}
	}
	if !hc {
		return l
	}
	// 高对比：次级往正文靠一半，三级升到原来的次级，元数据只比它再退一点。
	l.sub = (s.Sub + s.Ink) / 2
	l.tert = s.Sub
	l.meta = s.Sub + (s.Meta-s.Sub)*0.5
	// 描边整体升一档，分隔线跟描边对齐。
	l.separator = l.border
	l.border = l.borderStrong
	l.borderStrong = l.borderStrong + 0.045
	l.accentInkMin = 7.0
	l.accentLineMin = 4.0
	return l
}

func (s spec) build() Palette { return s.buildWith(s.ladder(false)) }

// buildHC 是高对比模式下的同一套配色。
func (s spec) buildHC() Palette { return s.buildWith(s.ladder(true)) }

func (s spec) buildWith(l ladder) Palette {
	// 表面的色相跟着地走，彩度逐级收一点——高级的表面总是比地更素。
	surfaceL := s.GroundL + l.surface
	tertiaryL := s.GroundL + l.tertiary
	elevatedL := s.GroundL + l.elevated
	fillQ := s.GroundL + l.fillQ
	fillT := s.GroundL + l.fillT
	fillS := s.GroundL + l.fillS
	borderL := s.GroundL + l.border
	borderStrongL := s.GroundL + l.borderStrong
	separatorL := s.GroundL + l.separator

	// 托盘取地和卡片的 L* 中点。OKLCH 的 L 不是等距的，直接除以二会让
	// 深色主题的两段差出一倍；L* 才是等分的那把尺子。
	chromeL := midpointL(s.GroundL, clamp01(surfaceL), s.GroundC*0.9, s.GroundH)
	p := Palette{
		ID: s.ID, Name: s.Name, Description: s.Description, Dark: s.Dark,

		BgPrimary:      oklch(s.GroundL, s.GroundC, s.GroundH),
		BgSecondary:    oklch(surfaceL, s.GroundC*0.9, s.GroundH),
		BgTertiary:     oklch(tertiaryL, s.GroundC*0.8, s.GroundH),
		BgElevated:     oklch(elevatedL, s.GroundC*0.85, s.GroundH),
		FillQuaternary: oklch(fillQ, s.GroundC*0.9, s.GroundH),
		FillTertiary:   oklch(fillT, s.GroundC*0.85, s.GroundH),
		FillSecondary:  oklch(fillS, s.GroundC*0.8, s.GroundH),
		HoverBg:        oklch(fillT, s.GroundC*0.85, s.GroundH),

		TextPrimary:   oklch(s.Ink, s.InkC, s.InkH),
		TextSecondary: oklch(l.sub, s.InkC*0.8, s.InkH),
		TextTertiary:  oklch(l.tert, s.InkC*0.6, s.InkH),
		TextMeta:      oklch(l.meta, s.InkC*0.5, s.InkH),

		Border:       oklch(borderL, s.GroundC*0.55, s.GroundH),
		BorderStrong: oklch(borderStrongL, s.GroundC*0.5, s.GroundH),
		Separator:    oklch(separatorL, s.GroundC*0.5, s.GroundH),

		StatusOffline: oklch(l.meta, s.InkC*0.3, s.InkH),
		SurfaceChrome: oklch(chromeL, s.GroundC*0.9, s.GroundH),
	}

	p.Accent = oklch(s.AccentL, s.AccentC, s.AccentH)

	// 强调色上的字：亮底配深墨，暗底配近白。
	tint := s.AccentC * 0.15
	if tint > 0.02 {
		tint = 0.02
	}
	dark := oklch(0.16, tint, s.AccentH)
	light := oklch(0.985, tint*0.5, s.AccentH)
	// 哪个对比度高用哪个，不按 L 猜：parchment 的赭石正好卡在中间，
	// 猜错的代价是一段读不出来的字。
	if wcag(light, p.Accent) > wcag(dark, p.Accent) {
		p.AccentText = light
	} else {
		p.AccentText = dark
	}

	p.AccentHover = oklch(clamp01(s.AccentL+0.02), s.AccentC, s.AccentH)
	p.AccentActive = oklch(clamp01(s.AccentL-0.035), s.AccentC, s.AccentH)

	// 同一支色的两个"可用"版本：当字和当线。
	p.AccentInk = withContrast(s.AccentL, s.AccentC, s.AccentH, p.BgPrimary, l.accentInkMin, s.Dark)
	p.AccentLine = withContrast(s.AccentL, s.AccentC, s.AccentH, p.BgPrimary, l.accentLineMin, s.Dark)
	p.AccentSoft = withAlpha(p.Accent, 0.12)
	p.AccentSofter = withAlpha(p.Accent, 0.07)
	p.AccentGlow = withAlpha(p.Accent, 0.20)

	if s.Dark {
		p.TopLight = ui.RGBA(255, 255, 255, 0.04)
	} else {
		p.TopLight = ui.RGBA(24, 24, 27, 0.03)
	}

	// 预览带是「地 / 字 / 强调」：两块白挨在一起看不出差别，
	// 换成字色才知道这套配色的对比关系。
	p.Swatch = [3]string{hexOf(p.BgPrimary), hexOf(p.TextPrimary), hexOf(p.Accent)}
	return p
}

func oklch(l, c, h float32) ui.Color {
	return ui.Oklch(clamp01(l), c, h)
}

// labL 是 CIE L*：0 黑、100 白。表面的台阶用它量，不用 WCAG 对比度——
// 白卡和地在 WCAG 里只有 1.19:1，读数几乎为零，眼睛看得一清二楚。
func labL(c ui.Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	y := 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
	if y > 0.008856 {
		return 116*math.Cbrt(y) - 16
	}
	return 903.3 * y
}

// midpointL 返回一个 OKLCH 明度，使它的 L* 正好落在地和卡片的中间。
// 二分 24 次，init 时跑一遍，代价可以忽略。
func midpointL(ground, card, c, h float32) float32 {
	want := (labL(oklch(ground, c, h)) + labL(oklch(card, c, h))) / 2
	lo, hi := ground, card
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if labL(oklch(mid, c, h)) < want {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// withContrast 把强调色的明度往"离地更远"的方向推，直到在 ground 上够 min。
//
// 推的是 L，色相和彩度不动，所以青柠变深之后还是青柠（橄榄绿），
// 不会变成另一种颜色。浅色主题里 #d8ff4f 在卡片上只有 1.1:1，
// 它需要两个可用版本：当字的和当线用的。
func withContrast(l, c, h float32, ground ui.Color, min float64, dark bool) ui.Color {
	step := float32(0.01)
	if !dark {
		step = -step
	}
	for i := 0; i < 90; i++ {
		col := oklch(l, c, h)
		if wcag(col, ground) >= min {
			return col
		}
		l = clamp01(l + step)
		if l <= 0 || l >= 1 {
			return col
		}
	}
	return oklch(l, c, h)
}

func withAlpha(c ui.Color, a float32) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, a)
}

// hexOf 把颜色写回 #rrggbb，给配色面板的预览色用。
func hexOf(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// wcag 是 §1.4.3 的对比度。生产代码里只有 withContrast 用它；
// 测试有自己的实现，两处都不依赖对方。
func wcag(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c ui.Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// highContrast 由 Shell 每帧按系统偏好设置：Windows 的对比度主题、macOS 的
// Increase Contrast 都落在这里。自绘的界面不会自动跟随，得自己接。
var highContrast bool

// SetHighContrast 开关高对比配色。只在主线程调用。
func SetHighContrast(on bool) { highContrast = on }

// current 是当前生效的配色；零值时按 signal 处理。
var current = &Palettes[0]

// Current 返回当前配色。
func Current() *Palette { return current }

// Find 按 ID 找配色，找不到返回 nil。
func Find(id string) *Palette {
	for i := range Palettes {
		if Palettes[i].ID == id {
			return &Palettes[i]
		}
	}
	return nil
}

// FindHC 按 ID 找高对比版本的配色，找不到返回 nil。
func FindHC(id string) *Palette {
	for i := range hcPalettes {
		if hcPalettes[i].ID == id {
			return &hcPalettes[i]
		}
	}
	return nil
}

// Palettes 里同色系日夜成对、相邻排列（signal/daylight、mono/paper、
// ember/parchment），所以「换到另一边」就是把下标异或 1。
// Counterpart 返回 id 的日夜对应配色；找不到时返回原 id。
func Counterpart(id string) string {
	for i := range Palettes {
		if Palettes[i].ID != id {
			continue
		}
		return Palettes[i^1].ID
	}
	return id
}

// Side 返回某一侧（深色 / 浅色）的配色，保持同色系顺序。
func Side(dark bool) []*Palette {
	out := make([]*Palette, 0, len(Palettes)/2)
	for i := range Palettes {
		if Palettes[i].Dark == dark {
			out = append(out, &Palettes[i])
		}
	}
	return out
}

// Apply 切换配色。
//
// 包级颜色变量在每帧开头被重写一次，所以 view 里读到的永远是当前配色；
// 只在主线程调用（view 是主线程，和绘制同线程）。
func Apply(id string) {
	p := Find(id)
	if p == nil {
		p = &Palettes[0]
	}
	if highContrast {
		if h := FindHC(p.ID); h != nil {
			p = h
		}
	}
	current = p

	BgPrimary, BgSecondary, BgTertiary = p.BgPrimary, p.BgSecondary, p.BgTertiary
	BgElevated = p.BgElevated
	FillQuaternary, FillTertiary = p.FillQuaternary, p.FillTertiary
	SurfaceChrome = p.SurfaceChrome
	FillSecondary, HoverBg = p.FillSecondary, p.HoverBg
	TextPrimary, TextSecondary = p.TextPrimary, p.TextSecondary
	TextTertiary, TextMeta = p.TextTertiary, p.TextMeta
	Accent, AccentHover, AccentActive = p.Accent, p.AccentHover, p.AccentActive
	AccentText, AccentInk = p.AccentText, p.AccentInk
	AccentLine = p.AccentLine
	AccentSoft, AccentSofter, AccentGlow = p.AccentSoft, p.AccentSofter, p.AccentGlow
	Border, BorderStrong, Separator = p.Border, p.BorderStrong, p.Separator
	StatusOffline = p.StatusOffline
	TopLight = p.TopLight

	// 阴影按明暗分成两套；浅色主题用蓝黑而不是纯黑。
	if p.Dark {
		ShadowPanel = ui.RGBA(0, 0, 0, 0.80)
		ShadowModal = ui.RGBA(0, 0, 0, 0.85)
		ShadowContact = ui.RGBA(0, 0, 0, 0.45)
	} else {
		// 浅色主题用蓝黑而不是纯黑：纯黑在白底上会压出一圈脏灰。
		ShadowPanel = ui.RGBA(24, 24, 27, 0.18)
		ShadowModal = ui.RGBA(24, 24, 27, 0.22)
		ShadowContact = ui.RGBA(24, 24, 27, 0.10)
	}
}
