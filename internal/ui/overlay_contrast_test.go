package ui

// 压在画面上的涂层：只算纯白和纯黑两端。
//
// 不采样直播画面。给定 tint 和 alpha，合成后的每个通道落在
// [a*c, a*c+(1-a)*255]；取两端，两端都过 4.5 才算过。模糊是局部平均，
// 不会越出这个区间；玻璃的饱和度提升由真窗口截图复核，这里不管。

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

func TestOverlaysSurviveWorstCaseBackdrop(t *testing.T) {
	white, black := ui.RGB(255, 255, 255), ui.RGB(0, 0, 0)
	overlays := []struct {
		name  string
		tint  ui.Color
		row   bool
		ink   []ui.Color
		names []string
	}{
		{"MediaPanel 0.88", theme.MediaPanel, false,
			[]ui.Color{theme.MediaText, theme.MediaTextBody, theme.MediaTextDim},
			[]string{"MediaText", "MediaTextBody", "MediaTextDim"}},
		{"MediaPanelTint", theme.MediaPanelTint, false,
			[]ui.Color{theme.MediaText, theme.MediaTextBody, theme.MediaTextDim},
			[]string{"MediaText", "MediaTextBody", "MediaTextDim"}},
		{"MediaPanelTint + row", theme.MediaPanelTint, true,
			[]ui.Color{theme.MediaText, theme.MediaTextBody, theme.MediaTextDim},
			[]string{"MediaText", "MediaTextBody", "MediaTextDim"}},
		{"OverlayInk 0.78", theme.OverlayInk, false,
			[]ui.Color{theme.White94, theme.White90},
			[]string{"White94", "White90"}},
		{"playerBar", playerBarBg, false,
			[]ui.Color{theme.MediaTextBody, theme.MediaText},
			[]string{"MediaTextBody", "MediaText"}},
		{"backBtn", ui.RGBA(10, 10, 12, 0.62), false,
			[]ui.Color{theme.MediaText},
			[]string{"MediaText"}},
		{"backBtnHover", ui.RGBA(10, 10, 12, 0.72), false,
			[]ui.Color{theme.MediaText},
			[]string{"MediaText"}},
	}
	for _, o := range overlays {
		for _, bg := range []struct {
			name string
			c    ui.Color
		}{{"白底", white}, {"黑底", black}} {
			panel := overlayComposite(o.tint, bg.c)
			if o.row {
				panel = overlayComposite(theme.MediaRow, panel)
			}
			for i, ink0 := range o.ink {
				ink := overlayComposite(ink0, panel)
				if r := overlayContrast(ink, panel); r < 4.5 {
					t.Errorf("%s / %s: %s %.2f:1 < 4.5", o.name, bg.name, o.names[i], r)
				}
			}
		}
	}
}

func overlayComposite(fg, bg ui.Color) ui.Color {
	if fg.A == 255 {
		return fg
	}
	a := float64(fg.A) / 255
	ch := func(f, b uint8) uint8 {
		return uint8(math.Round(a*float64(f) + (1-a)*float64(b)))
	}
	return ui.RGB(ch(fg.R, bg.R), ch(fg.G, bg.G), ch(fg.B, bg.B))
}

func overlayContrast(a, b ui.Color) float64 {
	la, lb := overlayLum(a), overlayLum(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func overlayLum(c ui.Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}
