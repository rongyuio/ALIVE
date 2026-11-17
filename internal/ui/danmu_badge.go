package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/theme"
)

// 平台徽章配色：等级胶囊和粉丝牌胶囊的底色。
//
// 取色都来自各平台网页端自己的徽章素材（2026-11-18 采样），并按平台
// 自己的日/夜规则跟随界面主题：
//
//   - B 站：用户等级取 wealth_level_medal 图（36×16 PNG，1–80 级逐张取
//     主色）；粉丝牌用协议下发的 v2 勋章色（#RRGGBBAA，0x99 = 60%），
//     start/end 不同时按网页端画 45° 渐变。B 站这套素材只有一份，日/夜都相同。
//   - 斗鱼：用户等级图标有 web-light / web-dark 两套（newm3_lv{N}.png），
//     深色主题用 web-dark 的主色，浅色主题用 web-light；粉丝牌按官方配置
//     fans_medal_web_v5.json 的 commBg/bgPics 映射取 com_bg 图主色，每 5 级
//     一档（51 级起是黑金设计，取金色描边）。
//   - 抖音：用户等级取 new_user_grade_level_v1_{N}.png，粉丝团取
//     fansclub_new_advanced_badge_{N}_xmp.png 的主色；只有一套深色素材。
//   - 虎牙：消费等级取 ConsumeLevelBadge 底板主色（30 级起是紫色）；
//     粉丝牌取 3_{level}.png 的主色；网页端只有深色一套。
//
// 这些都是平台品牌色，不属于设计令牌，集中放这里；平台改配色只动这几张表。
type badgeTier struct {
	upTo int
	hex  string
}

func pickTier(level int, tiers []badgeTier) string {
	for _, t := range tiers {
		if level <= t.upTo {
			return t.hex
		}
	}
	return ""
}

var (
	biliLevelTiers = []badgeTier{
		{9, "#5c7dbc"}, {29, "#537dcf"}, {39, "#4d76dc"}, {49, "#435fce"},
		{59, "#5844cb"}, {69, "#6c41c1"}, {79, "#8b39a1"}, {80, "#8f2189"},
	}

	// web-light：浅色页面上的等级图标。
	douyuLevelTiersLight = []badgeTier{
		{14, "#dfc398"}, {29, "#99d895"}, {39, "#70b0ea"}, {49, "#8498f1"},
		{59, "#9781f1"}, {69, "#c080f8"}, {79, "#e878f8"}, {89, "#f82898"},
		{99, "#f84040"}, {109, "#f85018"}, {120, "#f88010"},
	}
	// web-dark：深色页面上的等级图标，整体更深一档。
	douyuLevelTiersDark = []badgeTier{
		{14, "#806840"}, {29, "#407040"}, {39, "#3878b8"}, {49, "#4860d0"},
		{59, "#7068e8"}, {69, "#9850e0"}, {79, "#b038c8"}, {89, "#e050b0"},
		{99, "#d83030"}, {109, "#e04008"}, {120, "#f88010"},
	}
	douyuMedalTiers = []badgeTier{
		{5, "#48b068"}, {10, "#2098a0"}, {15, "#3880d0"}, {20, "#6860d0"},
		{25, "#f89858"}, {30, "#f86888"}, {35, "#f81890"}, {40, "#b030f8"},
		{45, "#f03800"}, {50, "#f86800"}, {59, "#c9a227"}, {66, "#efc865"},
		{100, "#efc865"},
	}

	douyinLevelTiers = []badgeTier{
		{9, "#a0b8f8"}, {10, "#e0e8f8"}, {15, "#c8d8f8"}, {20, "#7890f8"},
		{25, "#6068f8"}, {40, "#6058f8"}, {50, "#8028f8"}, {60, "#8000c8"},
		{100, "#481088"},
	}
	douyinMedalTiers = []badgeTier{
		{9, "#f8b860"}, {10, "#f8a028"}, {100, "#f87828"},
	}

	huyaLevelTiers = []badgeTier{
		{9, "#6a9aa9"}, {19, "#15789d"}, {29, "#4448ea"}, {39, "#6e24fe"},
		{120, "#8900ff"},
	}
	huyaMedalTiers = []badgeTier{
		{5, "#087850"}, {13, "#0068a8"}, {17, "#4840e8"}, {20, "#b03050"},
		{23, "#9000d0"}, {26, "#7828f8"}, {29, "#e06000"}, {32, "#e84000"},
		{35, "#e018d0"}, {41, "#8028f8"}, {50, "#f85008"}, {100, "#680010"},
	}
)

// badgeAlpha 是 B 站勋章底色的透明度：网页端用 #RRGGBB99 画牌子，
// 深色聊天区看着偏暗、浅色页面看着偏淡，两边都是这个值。
const badgeAlpha = 0.6

// colorFromHex 把 #RRGGBB 转成 ui.Color；空串返回零值（表示没有配色）。
func colorFromHex(hex string) ui.Color {
	if hex == "" {
		return ui.Color{}
	}
	return ui.Hex(hex)
}

// levelChipColor 返回用户等级胶囊的底色；零值表示没有对应规则，用中性胶囊。
func levelChipColor(platform live.Platform, level int) ui.Color {
	if level <= 0 {
		return ui.Color{}
	}
	dark := theme.Current().Dark
	switch platform {
	case live.Bilibili:
		return colorFromHex(pickTier(level, biliLevelTiers))
	case live.Douyu:
		if dark {
			return colorFromHex(pickTier(level, douyuLevelTiersDark))
		}
		return colorFromHex(pickTier(level, douyuLevelTiersLight))
	case live.Douyin:
		return colorFromHex(pickTier(level, douyinLevelTiers))
	case live.Huya:
		return colorFromHex(pickTier(level, huyaLevelTiers))
	}
	return ui.Color{}
}

// medalChipColors 返回粉丝牌胶囊的底色；零值表示用中性胶囊。
//
// B 站的勋章色由协议直接下发（v2 配色自带 alpha，如 #3FB4F699 就是网页端
// 那块 60% 的牌子），start/end 不同时按平台的 45° 渐变画；其余平台按牌子
// 等级查表。
func medalChipColors(d danmuLine) (bg, bgEnd ui.Color) {
	if d.Medal.Name == "" {
		return ui.Color{}, ui.Color{}
	}
	if d.Medal.Color != "" {
		bg = medalHexColor(d.Medal.Color)
		if d.Medal.ColorEnd != "" && d.Medal.ColorEnd != d.Medal.Color {
			bgEnd = medalHexColor(d.Medal.ColorEnd)
		}
		return bg, bgEnd
	}
	switch d.Platform {
	case live.Douyu:
		return colorFromHex(pickTier(d.Medal.Level, douyuMedalTiers)), ui.Color{}
	case live.Douyin:
		return colorFromHex(pickTier(d.Medal.Level, douyinMedalTiers)), ui.Color{}
	case live.Huya:
		return colorFromHex(pickTier(d.Medal.Level, huyaMedalTiers)), ui.Color{}
	}
	return ui.Color{}, ui.Color{}
}

// medalHexColor 解析平台下发的牌子底色。
//
// #RRGGBB 没有 alpha，按网页端的 60% 补上；#RRGGBBAA 原样用（B 站 v2 的
// 配色自带 0x99）。其它长度当没有配色——ui.Hex 遇到坏值会 panic。
func medalHexColor(hex string) ui.Color {
	switch len(hex) {
	case 7:
		c := ui.Hex(hex)
		return ui.RGBA(c.R, c.G, c.B, badgeAlpha)
	case 9:
		return ui.Hex(hex)
	}
	return ui.Color{}
}
