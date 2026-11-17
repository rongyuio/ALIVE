package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/theme"
)

func withPalette(t *testing.T, id string) {
	t.Helper()
	old := theme.Current().ID
	theme.Apply(id)
	t.Cleanup(func() { theme.Apply(old) })
}

// 等级胶囊底色按平台各自的等级分档走；斗鱼跟随界面日夜换一套图标色。
func TestLevelChipColor(t *testing.T) {
	withPalette(t, "signal")

	cases := []struct {
		platform live.Platform
		level    int
		want     ui.Color
	}{
		{live.Bilibili, 5, ui.Hex("#5c7dbc")},
		{live.Bilibili, 30, ui.Hex("#4d76dc")},
		{live.Bilibili, 80, ui.Hex("#8f2189")},
		{live.Douyu, 3, ui.Hex("#806840")},
		{live.Douyu, 10, ui.Hex("#806840")},
		{live.Douyu, 30, ui.Hex("#3878b8")},
		{live.Douyu, 120, ui.Hex("#f88010")},
		{live.Douyin, 7, ui.Hex("#a0b8f8")},
		{live.Douyin, 22, ui.Hex("#6068f8")},
		{live.Douyin, 45, ui.Hex("#8028f8")},
		{live.Huya, 17, ui.Hex("#15789d")},
		{live.Huya, 45, ui.Hex("#8900ff")},
		{live.Douyu, 0, ui.Color{}},
	}
	for _, tc := range cases {
		if got := levelChipColor(tc.platform, tc.level); got != tc.want {
			t.Errorf("%s Lv.%d = %v，期望 %v", tc.platform, tc.level, got, tc.want)
		}
	}
}

// 浅色主题下斗鱼换成 web-light 图标色。
func TestLevelChipColorLightPalette(t *testing.T) {
	withPalette(t, "daylight")

	cases := []struct {
		level int
		want  ui.Color
	}{
		{3, ui.Hex("#dfc398")},
		{12, ui.Hex("#dfc398")},
		{30, ui.Hex("#70b0ea")},
		{120, ui.Hex("#f88010")},
	}
	for _, tc := range cases {
		if got := levelChipColor(live.Douyu, tc.level); got != tc.want {
			t.Errorf("浅色主题 斗鱼 Lv.%d = %v，期望 %v", tc.level, got, tc.want)
		}
	}
}

// 粉丝牌胶囊：B 站用协议下发的勋章色（自带 alpha 的原样用，6 位的补 60%）；
// 其余平台按牌子等级查表。
func TestMedalChipColor(t *testing.T) {
	withPalette(t, "signal")

	// 老字段兜底：6 位色，按网页端的 60% 透明度补 alpha。
	proto := danmuLine{
		Platform: live.Bilibili,
		Medal:    live.Medal{Name: "德云色", Level: 24, Color: "#2d0855"},
	}
	if got, _ := medalChipColors(proto); got != ui.RGBA(0x2d, 0x08, 0x55, badgeAlpha) {
		t.Fatalf("老字段勋章色 = %v，期望 60%% 的 #2d0855", got)
	}

	// v2 配色：颜色本身带 alpha，网页端就是拿它直接铺。
	v2 := danmuLine{
		Platform: live.Bilibili,
		Medal:    live.Medal{Name: "德云色", Level: 22, Color: "#3FB4F699", ColorEnd: "#3FB4F699"},
	}
	if got, end := medalChipColors(v2); got != ui.Hex("#3FB4F699") || end != (ui.Color{}) {
		t.Fatalf("v2 勋章色 = %v / %v，期望 #3FB4F699 且不渐变", got, end)
	}

	// 双色牌子：起止色不同时给渐变终点。
	dual := danmuLine{
		Platform: live.Bilibili,
		Medal:    live.Medal{Name: "德云色", Level: 40, Color: "#3FB4F699", ColorEnd: "#4C7DFF99"},
	}
	if got, end := medalChipColors(dual); got != ui.Hex("#3FB4F699") || end != ui.Hex("#4C7DFF99") {
		t.Fatalf("双色勋章 = %v / %v", got, end)
	}

	cases := []struct {
		platform live.Platform
		level    int
		want     ui.Color
	}{
		{live.Douyu, 3, ui.Hex("#48b068")},
		{live.Douyu, 12, ui.Hex("#3880d0")},
		{live.Douyu, 38, ui.Hex("#b030f8")},
		{live.Douyu, 55, ui.Hex("#c9a227")},
		{live.Douyin, 4, ui.Hex("#f8b860")},
		{live.Douyin, 15, ui.Hex("#f87828")},
		{live.Huya, 7, ui.Hex("#0068a8")},
		{live.Huya, 33, ui.Hex("#e018d0")},
		{live.Huya, 45, ui.Hex("#f85008")},
	}
	for _, tc := range cases {
		d := danmuLine{Platform: tc.platform, Medal: live.Medal{Name: "牌子", Level: tc.level}}
		if got, _ := medalChipColors(d); got != tc.want {
			t.Errorf("%s 牌子 Lv.%d = %v，期望 %v", tc.platform, tc.level, got, tc.want)
		}
	}

	if got, _ := medalChipColors(danmuLine{Platform: live.Bilibili}); got != (ui.Color{}) {
		t.Fatalf("没有牌子时返回了颜色 %v", got)
	}
}

// 牌子上的字色：有平台底色就纯白，不描边、不按亮度换深字。
//
// 这条盯的是「别再加回去」：浅底白字曾经加过一圈 1 DIP、黑 45% 的描边，
// 在 11px 中文上糊成黑框，四个平台网页端都没这么画。
func TestChipTextColor(t *testing.T) {
	white := ui.Hex("#ffffff")
	for _, bg := range []ui.Color{ui.Hex("#e0e8f8"), ui.Hex("#f89858"), ui.Hex("#2d0855")} {
		if got := chipTextColor(bg); got != white {
			t.Errorf("底色 %v 的字色 = %v，应为纯白", bg, got)
		}
	}
	if got := chipTextColor(ui.Color{}); got != theme.TextSecondary {
		t.Errorf("中性胶囊的字色 = %v，应为 TextSecondary", got)
	}
}

// 弹幕胶囊不许给文字加描边。
//
// 这是字色之外的另一半：字色对、描边还留着，浅橙底上的白字照样被黑框
// 圈住（就是当初那个 bug）。跑不了运行时断言——描边是渲染属性，元素树
// 里看不到，只能查源码。范围限定在弹幕的画法文件，预估未来的 TextStroke
// （正文里的字重强调等）不受影响。
func TestChipTextHasNoStroke(t *testing.T) {
	for _, path := range []string{"danmu_panel.go", "danmu_badge.go"} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, "TextStroke(") {
				t.Errorf("%s:%d 给弹幕胶囊文字加了描边：%s", path, i+1, strings.TrimSpace(line))
			}
		}
	}
}
