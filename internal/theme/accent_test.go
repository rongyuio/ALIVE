package theme

// 填充上的字色由填充明度决定，不由"谁对比度高"决定。
//
// 中明度的饱和填充配黑字，数值上可能过线，视觉上会发脏（蓝底黑字、
// 红底黑字）。这里把极性钉死：L*≥62 的浅填充配深墨，低于 62 的配纯白。
// 普通模式的白字下限是 3.0（观感优先，不追求 AA 4.5）；HC 取最高对比。

import "testing"

func TestAccentTextPolarity(t *testing.T) {
	for i := range Palettes {
		p := &Palettes[i]
		fillL, textL := labL(p.Accent), labL(p.AccentText)
		r := contrast(p.AccentText, p.Accent)

		switch p.textMode {
		case accentTextAlwaysLight:
			if textL < 85 {
				t.Errorf("%s: 显式白字却配了 L*=%.1f", p.ID, textL)
			}
			// 显式覆盖的下限 2.5。灯暖·日的白字是 2.71，低于 3.0，
			// 这是明确的产品选择，不普适放宽。
			if r < 2.5 {
				t.Errorf("%s: 显式白字 %.2f < 2.5", p.ID, r)
			}
		case accentTextAlwaysDark:
			if textL > 22 {
				t.Errorf("%s: 显式深字却配了 L*=%.1f", p.ID, textL)
			}
			if r < 3.0 {
				t.Errorf("%s: 显式深字 %.2f < 3.0", p.ID, r)
			}
		default:
			if fillL >= accentFillLightL {
				if textL > 22 {
					t.Errorf("%s: 浅填充 L*=%.1f 却配了亮字 L*=%.1f", p.ID, fillL, textL)
				}
				if r < 4.5 {
					t.Errorf("%s: 浅填充上的深墨 %.2f < 4.5", p.ID, r)
				}
				continue
			}
			if textL < 85 {
				t.Errorf("%s: 中/深填充 L*=%.1f 却配了暗字 L*=%.1f", p.ID, fillL, textL)
			}
			// 普通模式强调填充的例外：下限 3.0，不是正文的 4.5。
			if r < 3.0 {
				t.Errorf("%s: 中/深填充上的白字 %.2f < 3.0", p.ID, r)
			}
		}
	}
	// 高对比模式忽略逐主题覆盖，取对比度最高的那支；仍是 4.5。
	for i := range hcPalettes {
		p := &hcPalettes[i]
		if r := contrast(p.AccentText, p.Accent); r < 4.5 {
			t.Errorf("%s (HC): AccentText %.2f < 4.5", p.ID, r)
		}
	}
}
