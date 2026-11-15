package theme

import "github.com/egoist/mygo/ui"

// Palette 是一套完整配色。baseline 的 4.1 版把「只换强调色」改成了
// 六套成对（同色系日夜各一套）的完整配色，所以这里存的是全量色板，
// 不是几个变量覆盖。
type Palette struct {
	ID          string
	Name        string
	Description string
	Dark        bool
	// Swatch 是配色面板里的三色预览：背景 / 表面 / 强调色
	Swatch [3]string

	BgPrimary, BgSecondary, BgTertiary, BgElevated, SidebarBg ui.Color
	FillQuaternary, FillTertiary, FillSecondary, HoverBg      ui.Color
	TextPrimary, TextSecondary, TextTertiary, TextMeta        ui.Color
	Accent, AccentHover, AccentActive, AccentText, AccentInk  ui.Color
	AccentSoft, AccentSofter, AccentGlow                      ui.Color
	Border, BorderStrong, Separator                           ui.Color
	StatusLive, StatusOffline                                 ui.Color
	// TopLight 是平涂表面顶部那 1px 受光边；深色主题是白，浅色主题是极淡的墨色。
	TopLight ui.Color
}

// Palettes 按「同色系日夜一行」的顺序排列，和配色面板的格子顺序一致。
var Palettes = []Palette{
	{
		ID: "signal", Name: "青柠·夜", Description: "石墨黑 + 信号青柠，默认夜间配色",
		Dark: true, Swatch: [3]string{"#0a0b0d", "#14161b", "#d8ff4f"},
		BgPrimary: ui.Hex("#0a0b0d"), BgSecondary: ui.Hex("#14161b"),
		BgTertiary: ui.Hex("#1c1f25"), BgElevated: ui.Hex("#191c22"),
		SidebarBg:      ui.Hex("#0c0e11"),
		FillQuaternary: ui.Hex("#16181d"), FillTertiary: ui.Hex("#1e2128"),
		FillSecondary: ui.Hex("#272b33"), HoverBg: ui.Hex("#1e2128"),
		TextPrimary: ui.Hex("#f4f4ef"), TextSecondary: ui.Hex("#a7aab2"),
		TextTertiary: ui.Hex("#8b909a"), TextMeta: ui.Hex("#737884"),
		Accent: ui.Hex("#d8ff4f"), AccentHover: ui.Hex("#e6ff7c"),
		AccentActive: ui.Hex("#c2ed2f"), AccentText: ui.Hex("#0b0c08"),
		AccentInk:  ui.Hex("#d8ff4f"),
		AccentSoft: ui.RGBA(216, 255, 79, 0.12), AccentSofter: ui.RGBA(216, 255, 79, 0.07),
		AccentGlow: ui.RGBA(216, 255, 79, 0.20),
		Border:     ui.Hex("#24272e"), BorderStrong: ui.Hex("#343842"),
		Separator:  ui.Hex("#1f2229"),
		StatusLive: ui.Hex("#ff4757"), StatusOffline: ui.Hex("#5c6169"),
		TopLight: ui.RGBA(255, 255, 255, 0.04),
	},
	{
		ID: "daylight", Name: "青柠·日", Description: "4.x 原版浅色 + 信号青柠",
		Dark: false, Swatch: [3]string{"#f1f2f4", "#ffffff", "#d8ff4f"},
		BgPrimary: ui.Hex("#f1f2f4"), BgSecondary: ui.Hex("#ffffff"),
		BgTertiary: ui.Hex("#eaecf0"), BgElevated: ui.Hex("#ffffff"),
		SidebarBg:      ui.Hex("#eef0f3"),
		FillQuaternary: ui.Hex("#f0f1f3"), FillTertiary: ui.Hex("#e6e8ec"),
		FillSecondary: ui.Hex("#d9dce2"), HoverBg: ui.Hex("#e6e8ec"),
		TextPrimary: ui.Hex("#18181b"), TextSecondary: ui.Hex("#52525b"),
		TextTertiary: ui.Hex("#63636d"), TextMeta: ui.Hex("#71717a"),
		Accent: ui.Hex("#d8ff4f"), AccentHover: ui.Hex("#e4ff85"),
		AccentActive: ui.Hex("#c8ef3a"), AccentText: ui.Hex("#0b0c08"),
		AccentInk:  ui.Hex("#3f6212"),
		AccentSoft: ui.RGBA(120, 160, 10, 0.14), AccentSofter: ui.RGBA(120, 160, 10, 0.07),
		AccentGlow: ui.RGBA(120, 160, 10, 0.18),
		Border:     ui.Hex("#dde1e6"), BorderStrong: ui.Hex("#c9ced6"),
		Separator:  ui.Hex("#e7e9ed"),
		StatusLive: ui.Hex("#e8344a"), StatusOffline: ui.Hex("#a1a1aa"),
		TopLight: ui.RGBA(24, 24, 27, 0.03),
	},
	{
		ID: "mono", Name: "黑白灰·夜", Description: "纯灰黑 + 白色强调，极简无彩色",
		Dark: true, Swatch: [3]string{"#0a0a0a", "#14161b", "#e5e5e5"},
		BgPrimary: ui.Hex("#0a0a0a"), BgSecondary: ui.Hex("#14161b"),
		BgTertiary: ui.Hex("#1e1e1e"), BgElevated: ui.Hex("#181818"),
		SidebarBg:      ui.Hex("#0d0d0d"),
		FillQuaternary: ui.Hex("#161616"), FillTertiary: ui.Hex("#202020"),
		FillSecondary: ui.Hex("#2a2a2a"), HoverBg: ui.Hex("#202020"),
		TextPrimary: ui.Hex("#f5f5f5"), TextSecondary: ui.Hex("#a3a3a3"),
		TextTertiary: ui.Hex("#8a8a8a"), TextMeta: ui.Hex("#737373"),
		Accent: ui.Hex("#e5e5e5"), AccentHover: ui.Hex("#ffffff"),
		AccentActive: ui.Hex("#d4d4d4"), AccentText: ui.Hex("#0a0a0a"),
		AccentInk:  ui.Hex("#e5e5e5"),
		AccentSoft: ui.RGBA(229, 229, 229, 0.12), AccentSofter: ui.RGBA(229, 229, 229, 0.07),
		AccentGlow: ui.RGBA(229, 229, 229, 0.18),
		Border:     ui.Hex("#262626"), BorderStrong: ui.Hex("#3a3a3a"),
		Separator:  ui.Hex("#202020"),
		StatusLive: ui.Hex("#ff4757"), StatusOffline: ui.Hex("#5c6169"),
		TopLight: ui.RGBA(255, 255, 255, 0.04),
	},
	{
		ID: "paper", Name: "黑白灰·日", Description: "纸白 + 墨黑强调，极简无彩色",
		Dark: false, Swatch: [3]string{"#f4f4f5", "#ffffff", "#18181b"},
		BgPrimary: ui.Hex("#f4f4f5"), BgSecondary: ui.Hex("#ffffff"),
		BgTertiary: ui.Hex("#e9e9eb"), BgElevated: ui.Hex("#ffffff"),
		SidebarBg:      ui.Hex("#efeff1"),
		FillQuaternary: ui.Hex("#f7f7f8"), FillTertiary: ui.Hex("#e8e8ea"),
		FillSecondary: ui.Hex("#d9d9dc"), HoverBg: ui.Hex("#e8e8ea"),
		TextPrimary: ui.Hex("#18181b"), TextSecondary: ui.Hex("#52525b"),
		TextTertiary: ui.Hex("#5f5f68"), TextMeta: ui.Hex("#71717a"),
		Accent: ui.Hex("#18181b"), AccentHover: ui.Hex("#3f3f46"),
		AccentActive: ui.Hex("#09090b"), AccentText: ui.Hex("#ffffff"),
		AccentInk:  ui.Hex("#18181b"),
		AccentSoft: ui.RGBA(24, 24, 27, 0.10), AccentSofter: ui.RGBA(24, 24, 27, 0.05),
		AccentGlow: ui.RGBA(24, 24, 27, 0.15),
		Border:     ui.Hex("#dedee2"), BorderStrong: ui.Hex("#c9c9cf"),
		Separator:  ui.Hex("#e8e8ea"),
		StatusLive: ui.Hex("#e11d48"), StatusOffline: ui.Hex("#a1a1aa"),
		TopLight: ui.RGBA(24, 24, 27, 0.03),
	},
	{
		ID: "ember", Name: "琥珀·夜", Description: "暖炭黑 + 琥珀橙",
		Dark: true, Swatch: [3]string{"#100c08", "#14161b", "#ffab5c"},
		BgPrimary: ui.Hex("#100c08"), BgSecondary: ui.Hex("#14161b"),
		BgTertiary: ui.Hex("#2a2118"), BgElevated: ui.Hex("#191c22"),
		SidebarBg:      ui.Hex("#0d0a06"),
		FillQuaternary: ui.Hex("#171109"), FillTertiary: ui.Hex("#221a12"),
		FillSecondary: ui.Hex("#30241a"), HoverBg: ui.Hex("#221a12"),
		TextPrimary: ui.Hex("#fff4e6"), TextSecondary: ui.Hex("#d6bda2"),
		TextTertiary: ui.Hex("#b89c80"), TextMeta: ui.Hex("#967d64"),
		Accent: ui.Hex("#ffab5c"), AccentHover: ui.Hex("#ffc287"),
		AccentActive: ui.Hex("#f0923b"), AccentText: ui.Hex("#1d0f02"),
		AccentInk:  ui.Hex("#ffab5c"),
		AccentSoft: ui.RGBA(255, 171, 92, 0.13), AccentSofter: ui.RGBA(255, 171, 92, 0.07),
		AccentGlow: ui.RGBA(255, 171, 92, 0.22),
		Border:     ui.Hex("#33271b"), BorderStrong: ui.Hex("#4a3927"),
		Separator:  ui.Hex("#271f16"),
		StatusLive: ui.Hex("#ff5a5f"), StatusOffline: ui.Hex("#6b5b4a"),
		TopLight: ui.RGBA(255, 244, 230, 0.04),
	},
	{
		ID: "parchment", Name: "书页·日", Description: "米纸底 + 赭石橙，书本纸张质感",
		Dark: false, Swatch: [3]string{"#f3ead8", "#fffaf0", "#b45309"},
		BgPrimary: ui.Hex("#f3ead8"), BgSecondary: ui.Hex("#fffaf0"),
		BgTertiary: ui.Hex("#e9dcc4"), BgElevated: ui.Hex("#fffdf7"),
		SidebarBg:      ui.Hex("#efe4d0"),
		FillQuaternary: ui.Hex("#f7f0e1"), FillTertiary: ui.Hex("#eee3cf"),
		FillSecondary: ui.Hex("#dfd1b7"), HoverBg: ui.Hex("#eee3cf"),
		TextPrimary: ui.Hex("#2b2117"), TextSecondary: ui.Hex("#5c4b38"),
		TextTertiary: ui.Hex("#6d5a45"), TextMeta: ui.Hex("#806c55"),
		Accent: ui.Hex("#b45309"), AccentHover: ui.Hex("#d97706"),
		AccentActive: ui.Hex("#92400e"), AccentText: ui.Hex("#fffaf0"),
		AccentInk:  ui.Hex("#8a4b0f"),
		AccentSoft: ui.RGBA(180, 83, 9, 0.12), AccentSofter: ui.RGBA(180, 83, 9, 0.06),
		AccentGlow: ui.RGBA(180, 83, 9, 0.16),
		Border:     ui.Hex("#ded0b8"), BorderStrong: ui.Hex("#c9b89a"),
		Separator:  ui.Hex("#e9ddc9"),
		StatusLive: ui.Hex("#c2410c"), StatusOffline: ui.Hex("#a89880"),
		TopLight: ui.RGBA(74, 52, 24, 0.03),
	},
}

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
	current = p

	BgPrimary, BgSecondary, BgTertiary = p.BgPrimary, p.BgSecondary, p.BgTertiary
	BgElevated, SidebarBg = p.BgElevated, p.SidebarBg
	FillQuaternary, FillTertiary = p.FillQuaternary, p.FillTertiary
	FillSecondary, HoverBg = p.FillSecondary, p.HoverBg
	TextPrimary, TextSecondary = p.TextPrimary, p.TextSecondary
	TextTertiary, TextMeta = p.TextTertiary, p.TextMeta
	Accent, AccentHover, AccentActive = p.Accent, p.AccentHover, p.AccentActive
	AccentText, AccentInk = p.AccentText, p.AccentInk
	AccentSoft, AccentSofter, AccentGlow = p.AccentSoft, p.AccentSofter, p.AccentGlow
	Border, BorderStrong, Separator = p.Border, p.BorderStrong, p.Separator
	StatusLive, StatusOffline = p.StatusLive, p.StatusOffline
	TopLight = p.TopLight

	// baseline 的阴影按明暗分成两套；浅色主题用蓝黑而不是纯黑。
	if p.Dark {
		ShadowPopover = ui.RGBA(0, 0, 0, 0.80)
		ShadowLg = ui.RGBA(0, 0, 0, 0.85)
	} else {
		ShadowPopover = ui.RGBA(24, 24, 27, 0.26)
		ShadowLg = ui.RGBA(24, 24, 27, 0.28)
	}
}
