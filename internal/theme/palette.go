package theme

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"
)

// Palette 是一族配色的一个曝光（日或夜）。六族身份、十二套配色：
// 墨白、幕红、屏青、灯暖、科技蓝、应援粉，每族日夜各一套。
//
// 每套只声明六个输入（地、四档文字、强调色），其余由 build 推导：
// 表面沿明度阶梯抬升，文字沿另一条收敛，强调色按对比度算出"当字用"
// 和"当线用"两个版本，状态色拆成 fill / ink / line 三支。
//
// 两个容易踩的点：
//   - AccentInk / AccentLine 对 FillSecondary 推导，不对地。FillSecondary 是
//     深色主题里最亮的平静表面、浅色主题里最暗的那个；在它上面过线，其余
//     平静表面都过。旧版只对地/卡片推，浅色主题在 FillSecondary 上掉到 3.86–3.98。
//   - 状态色不再写死。语义色相固定，明度按当前配色和模式推导，所以浅色主题
//     不会出现 1.3:1 的绿点。
type Palette struct {
	ID          string
	Name        string
	Description string
	Dark        bool
	textMode    accentTextMode
	// Swatch 是配色面板里的三色预览：地 / 文字 / 强调色。
	Swatch [3]string

	BgPrimary, BgSecondary, BgTertiary, BgElevated ui.Color

	// SurfaceChrome 是顶栏托盘：地和卡片的明度中点，由阶梯推导，不手写。
	SurfaceChrome ui.Color

	FillQuaternary, FillTertiary, FillSecondary ui.Color
	HoverBg                                     ui.Color
	TextPrimary, TextSecondary, TextTertiary    ui.Color
	TextMeta                                    ui.Color
	Accent, AccentHover, AccentActive           ui.Color
	AccentText                                  ui.Color
	// AccentInk 是强调色当文字用（≥4.5:1），AccentLine 是当线条用（≥3:1）。
	// 两者都对 FillSecondary 推导，不用 BgPrimary。
	AccentInk        ui.Color
	AccentLine       ui.Color
	AccentOnInk      ui.Color
	AccentSoft       ui.Color
	AccentSofter     ui.Color
	AccentGlow       ui.Color
	Border           ui.Color
	BorderStrong     ui.Color
	BorderFunctional ui.Color
	Separator        ui.Color
	// StatusOffline 是离线态的点：无彩色，明度跟着文字元数据走。
	StatusOffline ui.Color

	// 状态色三支：fill 用于点、底色和低透明度叠加；ink 用于文字和图标；
	// line 用于边框、指示器。ink ≥4.5，line/fill ≥3。
	StatusOnline     ui.Color
	StatusOnlineInk  ui.Color
	StatusOnlineLine ui.Color
	Success          ui.Color
	SuccessInk       ui.Color
	SuccessLine      ui.Color
	Danger           ui.Color
	DangerInk        ui.Color
	DangerLine       ui.Color

	// TopLight 是平涂表面顶部那 1px 受光边；深色主题是白，浅色主题是极淡的墨色。
	TopLight ui.Color

	// 阴影阶梯也属于调色板；主题过渡时和颜色一起混合。
	ShadowPanel, ShadowModal, ShadowContact ui.Color
}

// accentFillLightL 是强调填充"浅到可以配深墨"的 L* 门槛。
//
// 低于它的填充按中/深填充处理，配纯白。普通模式的白字下限是 3.0，不是
// 正文的 4.5——强调填充观感优先，低视力用户走高对比模式（HC 取最高对比，
// 正文 7:1）。62 不是对比度的数学交点（约 49），而是"填充看起来像浅色表面"的位置。
const accentFillLightL = 62.0

// accentTextMode 是强调填充的字色策略。
//
// Auto 按填充 L* 走；AlwaysLight/AlwaysDark 是逐主题的显式覆盖，写在 spec 里。
// HC 永远忽略覆盖，取对比度最高的那支。
type accentTextMode uint8

const (
	accentTextAuto accentTextMode = iota
	accentTextAlwaysLight
	accentTextAlwaysDark
)

// islandRingMin 是流光热段对岛面的最低对比度。
//
// WCAG §1.4.11 对非文字图形要 3:1，但那条是按「静止的边界」写的。这里是一道
// 2 DIP 宽、一直在动的弧：移动 + 细，两个因素都会压低可感知对比，所以往上
// 留一档，取 4.5——和正文同一档，不是随手加严。
const islandRingMin = 4.5

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
	TextMode                  accentTextMode
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

// specs 按「同族日夜一行」排列，和配色面板的格子顺序一致。
var specs = []spec{
	{
		ID: "inkwhite-night", Name: "墨白·夜", Description: "关灯后的屏幕：冷石墨底、冷白字，画面之外只剩字形",
		Dark:    true,
		GroundL: 0.203, GroundC: 0.006, GroundH: 250,
		Ink: 0.955, Sub: 0.850, Tert: 0.760, Meta: 0.700, InkC: 0.010, InkH: 240,
		AccentL: 0.995, AccentC: 0, AccentH: 0,
	},
	{
		ID: "inkwhite-day", Name: "墨白·日", Description: "冷月白底、墨字；不靠色相分层",
		Dark:    false,
		GroundL: 0.948, GroundC: 0.006, GroundH: 245,
		Ink: 0.290, Sub: 0.390, Tert: 0.455, Meta: 0.490, InkC: 0.012, InkH: 250,
		AccentL: 0, AccentC: 0, AccentH: 0,
	},
	{
		ID: "curtain-night", Name: "幕红·夜", Description: "开播前的幕布：深酒红底、幕布红强调",
		Dark:    true,
		GroundL: 0.200, GroundC: 0.019, GroundH: 15,
		Ink: 0.955, Sub: 0.845, Tert: 0.755, Meta: 0.695, InkC: 0.008, InkH: 40,
		AccentL: 0.650, AccentC: 0.160, AccentH: 22,
		TextMode: accentTextAlwaysDark,
	},
	{
		ID: "curtain-day", Name: "幕红·日", Description: "浅玫瑰底、深暖字、幕布红强调",
		Dark:    false,
		GroundL: 0.930, GroundC: 0.016, GroundH: 15,
		Ink: 0.290, Sub: 0.390, Tert: 0.455, Meta: 0.490, InkC: 0.010, InkH: 30,
		AccentL: 0.550, AccentC: 0.160, AccentH: 22,
	},
	{
		ID: "phosphor-night", Name: "屏青·夜", Description: "监视器的荧光余辉：深青底、冰青字",
		Dark:    true,
		GroundL: 0.160, GroundC: 0.018, GroundH: 195,
		Ink: 0.955, Sub: 0.845, Tert: 0.755, Meta: 0.695, InkC: 0.008, InkH: 190,
		AccentL: 0.880, AccentC: 0.100, AccentH: 190,
	},
	{
		ID: "phosphor-day", Name: "屏青·日", Description: "浅薄荷底、深青字、同色系青",
		Dark:    false,
		GroundL: 0.930, GroundC: 0.012, GroundH: 190,
		Ink: 0.290, Sub: 0.390, Tert: 0.455, Meta: 0.490, InkC: 0.010, InkH: 195,
		AccentL: 0.470, AccentC: 0.075, AccentH: 190,
	},
	{
		ID: "lamp-night", Name: "灯暖·夜", Description: "环形补光灯：暖灰底、灯丝光",
		Dark:    true,
		GroundL: 0.200, GroundC: 0.008, GroundH: 70,
		Ink: 0.955, Sub: 0.845, Tert: 0.755, Meta: 0.695, InkC: 0.010, InkH: 70,
		AccentL: 0.870, AccentC: 0.065, AccentH: 60,
	},
	{
		ID: "lamp-day", Name: "灯暖·日", Description: "奶油底、暖褐字、浅金强调",
		Dark:    false,
		GroundL: 0.945, GroundC: 0.006, GroundH: 85,
		Ink: 0.300, Sub: 0.395, Tert: 0.460, Meta: 0.495, InkC: 0.010, InkH: 70,
		AccentL: 0.700, AccentC: 0.090, AccentH: 75,
		TextMode: accentTextAlwaysLight,
	},
	{
		ID: "tech-night", Name: "科技蓝·夜", Description: "屏幕冷光：深海军蓝底、科技蓝强调",
		Dark:    true,
		GroundL: 0.205, GroundC: 0.020, GroundH: 260,
		Ink: 0.960, Sub: 0.850, Tert: 0.760, Meta: 0.700, InkC: 0.006, InkH: 250,
		AccentL: 0.760, AccentC: 0.120, AccentH: 250,
	},
	{
		ID: "tech-day", Name: "科技蓝·日", Description: "浅冰蓝底、深海军蓝字、科技蓝强调",
		Dark:    false,
		GroundL: 0.930, GroundC: 0.012, GroundH: 245,
		Ink: 0.290, Sub: 0.390, Tert: 0.455, Meta: 0.490, InkC: 0.012, InkH: 260,
		AccentL: 0.666, AccentC: 0.158, AccentH: 247,
	},
	{
		ID: "fanlight-night", Name: "应援粉·夜", Description: "观众席灯牌：冷灰底、珊瑚粉高亮",
		Dark:    true,
		GroundL: 0.160, GroundC: 0.006, GroundH: 300,
		Ink: 0.955, Sub: 0.845, Tert: 0.755, Meta: 0.695, InkC: 0.006, InkH: 20,
		AccentL: 0.735, AccentC: 0.165, AccentH: 8,
	},
	{
		ID: "fanlight-day", Name: "应援粉·日", Description: "浅粉底、深灰字、珊瑚粉强调",
		Dark:    false,
		GroundL: 0.940, GroundC: 0.008, GroundH: 355,
		Ink: 0.290, Sub: 0.390, Tert: 0.455, Meta: 0.490, InkC: 0.008, InkH: 350,
		AccentL: 0.680, AccentC: 0.187, AccentH: 6.3,
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

// ladder 是一套主题模式下表面与文字的阶梯参数。
//
// 普通模式和高对比模式用同一份结构、不同的数：高对比把文字往正文收、
// 把描边升成强描边，层级从"明度拉开"改成"字重和字号拉开"。
type ladder struct {
	surface, tertiary, elevated, fillQ, fillT, fillS float32
	border, borderStrong, separator                  float32
	sub, tert, meta                                  float32
	accentInkMin, accentLineMin                      float64
	hc                                               bool
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
	l.hc = true
	l.sub = (s.Sub + s.Ink) / 2
	l.tert = s.Sub
	// 元数据往 Sub 挪到 1/4，而不是 1/2：HC 的 11px 小字需要再亮/再深一档。
	// APCA 读数从 57–59 提到 60 以上；普通配色不动。
	l.meta = s.Sub + (s.Meta-s.Sub)*0.25
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
	chromeL := midpointL(s.GroundL, clamp01(surfaceL), s.GroundC, s.GroundC*0.9, s.GroundH)
	p := Palette{
		ID: s.ID, Name: s.Name, Description: s.Description, Dark: s.Dark, textMode: s.TextMode,

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

	// 功能性边界：中性灰，对卡片推导。按钮坐在卡片上，卡片是相邻面；
	// 公共 Border 保持"分隔"语义，不跟着提亮。
	base := float32(0.85)
	if s.Dark {
		base = 0.15
	}
	p.BorderFunctional = withContrast(base, 0, 0, p.BgSecondary, l.accentLineMin, s.Dark)

	p.Accent = oklch(s.AccentL, s.AccentC, s.AccentH)

	// 强调色上的字：按填充明度定极性，不按"谁对比度高"选。
	//
	// 旧规则只比 WCAG：中明度的饱和填充上黑字数值上赢一点，就被选中。
	// 结果就是蓝底黑字、红底黑字——读到的是"脏"，不是"可读"。
	// 现在：浅填充（L*≥62）配深墨；中/深填充配纯白。普通模式白字下限 3.0，
	// 高对比模式不跟随极性、取对比度最高的那支。
	tint := s.AccentC * 0.15
	if tint > 0.02 {
		tint = 0.02
	}
	dark := oklch(0.16, tint, s.AccentH)
	light := oklch(1, 0, 0)
	switch {
	case l.hc:
		// 高对比不跟随观感极性：取对比度最高的那支，保证可读。
		if wcag(light, p.Accent) > wcag(dark, p.Accent) {
			p.AccentText = light
		} else {
			p.AccentText = dark
		}
	case s.TextMode == accentTextAlwaysLight:
		p.AccentText = light
	case s.TextMode == accentTextAlwaysDark:
		p.AccentText = dark
	case labL(p.Accent) >= accentFillLightL:
		p.AccentText = dark
	default:
		// 中/深填充配纯白。普通模式下限 3.0——明确标出的例外：
		// 强调填充是品牌面，观感优先；低视力走高对比。
		p.AccentText = light
	}

	p.AccentHover = oklch(clamp01(s.AccentL+0.02), s.AccentC, s.AccentH)
	p.AccentActive = oklch(clamp01(s.AccentL-0.035), s.AccentC, s.AccentH)

	// 同一支色的两个"可用"版本：当字和当线。
	// 目标表面取 FillSecondary：深色主题里它是最亮的平静面，浅色主题里最暗。
	p.AccentInk = withContrast(s.AccentL, s.AccentC, s.AccentH, p.FillSecondary, l.accentInkMin, s.Dark)
	p.AccentLine = withContrast(s.AccentL, s.AccentC, s.AccentH, p.FillSecondary, l.accentLineMin, s.Dark)
	// 第三支：压在灵动岛的墨底上。
	//
	// 前两支都是对 FillSecondary 推导的：浅色主题里它们被压深过去当字/当线，
	// 压到 #060709 上等于消失——环不是「颜色不够亮」，是根本没有光。
	// 墨底是恒定的暗面，所以这一支永远往亮里走；深色主题里品牌色本来就
	// 够亮，withContrast 会原样返回它。
	//
	// 有一类配色的强调色本身就是「深墨」（paper 的近黑）。直接按对比度往上推，
	// 得到的是 #5f6064 这种中灰：压在黑底上既不亮也不暗，读起来是脏的。
	// 这类配色把明度翻过来——在亮底上当墨的那一支，在墨底上就当光。
	inkL := s.AccentL
	if inkL < 0.35 {
		inkL = 1 - inkL
	}
	p.AccentOnInk = withContrast(inkL, s.AccentC, s.AccentH, IslandInk, islandRingMin, true)
	p.AccentSoft = withAlpha(p.Accent, 0.12)
	p.AccentSofter = withAlpha(p.Accent, 0.07)
	p.AccentGlow = withAlpha(p.Accent, 0.20)

	// 状态色。色相是语义：成功 / 危险跟着日夜和配色推导明度。
	//
	// 在线绿是例外：它是固定值，日夜和各配色共用同一支。这颗绿只压在
	// 画面和墨底上（封面上的点、岛上的环、观测墙的点），底本来就是暗的；
	// 跟着浅色主题压深只会变成另一种深绿，和深色主题对不上。
	live := statusOnlineColor()
	p.StatusOnline, p.StatusOnlineInk, p.StatusOnlineLine = live, live, live
	p.Success, p.SuccessInk, p.SuccessLine = buildStatus(statusSuccess, s.Dark, l, p.FillSecondary)
	p.Danger, p.DangerInk, p.DangerLine = buildStatus(statusDanger, s.Dark, l, p.FillSecondary)

	if s.Dark {
		p.TopLight = ui.RGBA(255, 255, 255, 0.04)
		p.ShadowPanel = ui.RGBA(0, 0, 0, 0.80)
		p.ShadowModal = ui.RGBA(0, 0, 0, 0.85)
		p.ShadowContact = ui.RGBA(0, 0, 0, 0.45)
	} else {
		p.TopLight = ui.RGBA(24, 24, 27, 0.03)
		// 浅色主题用蓝黑而不是纯黑：纯黑在白底上会压出一圈脏灰。
		p.ShadowPanel = ui.RGBA(24, 24, 27, 0.18)
		p.ShadowModal = ui.RGBA(24, 24, 27, 0.22)
		p.ShadowContact = ui.RGBA(24, 24, 27, 0.10)
	}

	// 预览带是「地 / 字 / 强调」：两块白挨在一起看不出差别，
	// 换成字色才知道这套配色的对比关系。
	p.Swatch = [3]string{hexOf(p.BgPrimary), hexOf(p.TextPrimary), hexOf(p.Accent)}
	return p
}

// 状态色：色相与彩度是语义，明度按模式和配色推导。
//
// 危险色从 H25 移到 H35：幕红的品牌红在 H22，两者相差不到 5 度时
// 在浅色主题里会读成同一种红。H35 仍是错误色，和品牌红能拉开。
type statusSpec struct {
	hue                          float32
	darkL, darkC, lightL, lightC float32
}

var (
	// 在线绿只有深色那一支：浅色主题也用它，见 build 里的说明。
	statusOnline  = statusSpec{hue: 150, darkL: 0.72, darkC: 0.160}
	statusSuccess = statusSpec{hue: 145, darkL: 0.72, darkC: 0.150, lightL: 0.58, lightC: 0.140}
	statusDanger  = statusSpec{hue: 35, darkL: 0.62, darkC: 0.180, lightL: 0.52, lightC: 0.180}
)

// statusOnlineColor 返回固定的在线绿：fill / ink / line 同值。
//
// 三支原本的差别来自对不同表面的对比度推导；固定之后只压在墨底和画面上，
// 不需要再为「当字用」留一支更深的。
func statusOnlineColor() ui.Color {
	return oklch(statusOnline.darkL, statusOnline.darkC, statusOnline.hue)
}

// buildStatus 返回 fill / ink / line。fill 和 line 都以"在 FillSecondary 上
// 看得见"为底线；ink 按正文那一档推，保证当文字时够读。
func buildStatus(st statusSpec, dark bool, l ladder, worst ui.Color) (fill, ink, line ui.Color) {
	L, C := st.lightL, st.lightC
	if dark {
		L, C = st.darkL, st.darkC
	}
	fill = withContrast(L, C, st.hue, worst, 3.0, dark)
	ink = statusInk(L, C, st.hue, worst, l.accentInkMin, dark)
	line = withContrast(L, C, st.hue, worst, l.accentLineMin, dark)
	return
}

// statusInk 在正常对比达标之后，再查三型色觉模拟。
//
// 红/绿在 protanopia 下会掉 0.2–0.5 个对比度：正常视觉 4.5，模拟后
// 只剩 3.96–4.49。状态色不能只对正常视觉成立，所以不够就继续往同方向
// 推 L，直到两头都过。
func statusInk(l, c, h float32, worst ui.Color, min float64, dark bool) ui.Color {
	step := float32(0.01)
	if !dark {
		step = -step
	}
	for i := 0; i < 90; i++ {
		col := oklch(l, c, h)
		if wcag(col, worst) >= min {
			ok := true
			for k := 0; k < 3; k++ {
				if wcag(cvdSim(col, k), cvdSim(worst, k)) < 4.5 {
					ok = false
					break
				}
			}
			if ok {
				return col
			}
		}
		l = clamp01(l + step)
		if l <= 0 || l >= 1 {
			return col
		}
	}
	return oklch(l, c, h)
}

// cvdSim 是 Machado 2009 severity 1.0 的三型模拟，线性 sRGB 上乘矩阵。
func cvdSim(c ui.Color, kind int) ui.Color {
	mats := [3][3][3]float64{
		{
			{0.152286, 1.052583, -0.204868},
			{0.114503, 0.786281, 0.099216},
			{-0.003882, -0.048116, 1.051998},
		},
		{
			{0.367322, 0.860646, -0.227968},
			{0.280085, 0.672501, 0.047413},
			{-0.011820, 0.042940, 0.968881},
		},
		{
			{1.255528, -0.076749, -0.178779},
			{-0.078411, 0.930809, 0.147602},
			{0.004733, 0.691367, 0.303900},
		},
	}
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	enc := func(v float64) uint8 {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			return uint8(math.Round(v * 12.92 * 255))
		}
		return uint8(math.Round((1.055*math.Pow(v, 1/2.4) - 0.055) * 255))
	}
	m := mats[kind]
	r, g, b := lin(c.R), lin(c.G), lin(c.B)
	return ui.RGB(
		enc(m[0][0]*r+m[0][1]*g+m[0][2]*b),
		enc(m[1][0]*r+m[1][1]*g+m[1][2]*b),
		enc(m[2][0]*r+m[2][1]*g+m[2][2]*b))
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
//
// 地、卡片、托盘三者的彩度不一样（托盘取地的 0.9 倍），所以不能拿同一个
// 彩度去量三块的 L*。屏青和灯暖的地彩度稍高，旧算法会让托盘偏下 0.9 个 L*。
// 二分 24 次，init 时跑一遍，代价可以忽略。
func midpointL(ground, card, groundC, cardC, h float32) float32 {
	want := (labL(oklch(ground, groundC, h)) + labL(oklch(card, cardC, h))) / 2
	lo, hi := ground, card
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if labL(oklch(mid, cardC, h)) < want {
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

// current 是当前生效的配色；零值时按墨白·夜处理。
var current = &Palettes[0]

// Current 返回当前配色。
func Current() *Palette { return current }

// legacyIDs 把旧版落盘的配色 ID 迁到新族。
//
// 按色系就近迁移：绿 → 屏青，黑白 → 墨白，暖 → 灯暖。
// 迁移只在读取时发生，下一次落盘写的是新 ID。
var legacyIDs = map[string]string{
	"signal":    "phosphor-night",
	"daylight":  "phosphor-day",
	"mono":      "inkwhite-night",
	"paper":     "inkwhite-day",
	"ember":     "lamp-night",
	"parchment": "lamp-day",
}

func resolveID(id string) string {
	if v, ok := legacyIDs[id]; ok {
		return v
	}
	return id
}

// Find 按 ID 找配色，找不到返回 nil。旧 ID 会先迁移。
func Find(id string) *Palette {
	id = resolveID(id)
	for i := range Palettes {
		if Palettes[i].ID == id {
			return &Palettes[i]
		}
	}
	return nil
}

// FindHC 按 ID 找高对比版本的配色，找不到返回 nil。
func FindHC(id string) *Palette {
	id = resolveID(id)
	for i := range hcPalettes {
		if hcPalettes[i].ID == id {
			return &hcPalettes[i]
		}
	}
	return nil
}

// Palettes 里同族日夜成对、相邻排列（墨白、幕红、屏青、灯暖、科技蓝、应援粉），
// 所以「换到另一边」就是把下标异或 1。
// Counterpart 返回 id 的日夜对应配色；找不到时返回原 id。
func Counterpart(id string) string {
	id = resolveID(id)
	for i := range Palettes {
		if Palettes[i].ID != id {
			continue
		}
		return Palettes[i^1].ID
	}
	return id
}

// Side 返回某一侧（深色 / 浅色）的配色，保持同族顺序。
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
	Show(p)
}

// Show 把一套已展开的调色板写进包级令牌。
//
// Apply 用它；主题过渡的中间帧也用它——中间帧的 Palette 由 Blend 生成，
// 不在 Palettes 里。只在主线程调用。
func Show(p *Palette) {
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
	AccentOnInk = p.AccentOnInk
	AccentSoft, AccentSofter, AccentGlow = p.AccentSoft, p.AccentSofter, p.AccentGlow
	Border, BorderStrong, Separator = p.Border, p.BorderStrong, p.Separator
	BorderFunctional = p.BorderFunctional
	StatusOffline = p.StatusOffline
	StatusOnline, StatusOnlineInk, StatusOnlineLine = p.StatusOnline, p.StatusOnlineInk, p.StatusOnlineLine
	Success, SuccessInk, SuccessLine = p.Success, p.SuccessInk, p.SuccessLine
	Danger, DangerInk, DangerLine = p.Danger, p.DangerInk, p.DangerLine
	TopLight = p.TopLight
	ShadowPanel, ShadowModal, ShadowContact = p.ShadowPanel, p.ShadowModal, p.ShadowContact
}

// Blend 返回 from 到 to 之间 t 处的调色板，颜色都在 Oklab 里混。
//
// 主题切换用它。t<=0 返回 from，t>=1 返回 to；中间帧的元数据取 to 的。
// 阴影和强调色、状态色一起混，所以切主题时不会只有地面在动。
func Blend(from, to *Palette, t float32) *Palette {
	if t >= 1 {
		return to
	}
	// 颜色取两端之间；元数据（ID/Dark/名字）始终取目标。
	// t=0 时颜色还是 from，但面板按 Dark 换边的判断已经看到目标——
	// 否则日夜切换的头一帧，面板会继续列旧的一侧。
	mix := func(a, b ui.Color) ui.Color {
		if t <= 0 {
			return a
		}
		return a.MixOklab(b, t)
	}
	p := *to
	p.BgPrimary = mix(from.BgPrimary, to.BgPrimary)
	p.BgSecondary = mix(from.BgSecondary, to.BgSecondary)
	p.BgTertiary = mix(from.BgTertiary, to.BgTertiary)
	p.BgElevated = mix(from.BgElevated, to.BgElevated)
	p.SurfaceChrome = mix(from.SurfaceChrome, to.SurfaceChrome)
	p.FillQuaternary = mix(from.FillQuaternary, to.FillQuaternary)
	p.FillTertiary = mix(from.FillTertiary, to.FillTertiary)
	p.FillSecondary = mix(from.FillSecondary, to.FillSecondary)
	p.HoverBg = mix(from.HoverBg, to.HoverBg)
	p.TextPrimary = mix(from.TextPrimary, to.TextPrimary)
	p.TextSecondary = mix(from.TextSecondary, to.TextSecondary)
	p.TextTertiary = mix(from.TextTertiary, to.TextTertiary)
	p.TextMeta = mix(from.TextMeta, to.TextMeta)
	p.Accent = mix(from.Accent, to.Accent)
	p.AccentHover = mix(from.AccentHover, to.AccentHover)
	p.AccentActive = mix(from.AccentActive, to.AccentActive)
	p.AccentText = mix(from.AccentText, to.AccentText)
	p.AccentInk = mix(from.AccentInk, to.AccentInk)
	p.AccentLine = mix(from.AccentLine, to.AccentLine)
	p.AccentOnInk = mix(from.AccentOnInk, to.AccentOnInk)
	p.AccentSoft = mix(from.AccentSoft, to.AccentSoft)
	p.AccentSofter = mix(from.AccentSofter, to.AccentSofter)
	p.AccentGlow = mix(from.AccentGlow, to.AccentGlow)
	p.Border = mix(from.Border, to.Border)
	p.BorderStrong = mix(from.BorderStrong, to.BorderStrong)
	p.BorderFunctional = mix(from.BorderFunctional, to.BorderFunctional)
	p.Separator = mix(from.Separator, to.Separator)
	p.StatusOffline = mix(from.StatusOffline, to.StatusOffline)
	p.StatusOnline = mix(from.StatusOnline, to.StatusOnline)
	p.StatusOnlineInk = mix(from.StatusOnlineInk, to.StatusOnlineInk)
	p.StatusOnlineLine = mix(from.StatusOnlineLine, to.StatusOnlineLine)
	p.Success = mix(from.Success, to.Success)
	p.SuccessInk = mix(from.SuccessInk, to.SuccessInk)
	p.SuccessLine = mix(from.SuccessLine, to.SuccessLine)
	p.Danger = mix(from.Danger, to.Danger)
	p.DangerInk = mix(from.DangerInk, to.DangerInk)
	p.DangerLine = mix(from.DangerLine, to.DangerLine)
	p.TopLight = mix(from.TopLight, to.TopLight)
	p.ShadowPanel = mix(from.ShadowPanel, to.ShadowPanel)
	p.ShadowModal = mix(from.ShadowModal, to.ShadowModal)
	p.ShadowContact = mix(from.ShadowContact, to.ShadowContact)
	return &p
}
