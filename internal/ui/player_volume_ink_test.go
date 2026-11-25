package ui

// 音量组两处的墨色，各管各的。
//
// 滑条填充跟配色走，但取的是**墨面那一支** `AccentOnInk`：控制条是恒定暗面，
// 直接放 `Accent` 在浅色主题里就是墨白·日 / 纸·日的近黑强调色，压在墨玻璃上
// 等于没画（真窗口里量到过 (0,0,0)）。`AccentOnInk` 是 palette.go 对 IslandInk
// 推导的同族强调色：深色主题下它就是 Accent 本身，浅色主题下翻成亮的那一支。
// 灵动岛的环用的是同一支，理由一样。
//
// 百分比不跟配色：数字已经把位置说清楚了，再染一次只是把强调色用滥。
// 它退到 `MediaTextDim`——比同排图标的 `MediaTextBody` 低一档。
//
// 期望值出处：palette.go 里 `AccentOnInk = withContrast(inkL, …, IslandInk, …)`
// 那条推导，以及 theme.go「Media* 是固定墨面」的划分。不在被测实现里。

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// countNear 数一块区域里贴近某个色的像素。坐标是设备像素。
func countNear(img *image.RGBA, x0, y0, x1, y1 int, c ui.Color, tol int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if absInt(int(r>>8)-int(c.R)) <= tol &&
				absInt(int(g>>8)-int(c.G)) <= tol &&
				absInt(int(b>>8)-int(c.B)) <= tol {
				n++
			}
		}
	}
	return n
}

// litPixels 数一块区域里够亮的像素：数字必须真的画出来，采样区不能是
// 一片暗底。坐标是设备像素。
func litPixels(img *image.RGBA, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if int(r>>8)+int(g>>8)+int(b>>8) > 420 {
				n++
			}
		}
	}
	return n
}

// 两套配色各跑一遍：深色的幕红·夜（Accent 与 AccentOnInk 同色）、
// 浅色的墨白·日（Accent 是近黑，AccentOnInk 是白——用来证明滑块没退化）。
func TestVolumeSliderIsAccentOnInk(t *testing.T) {
	cases := []struct {
		name    string
		palette string
		mode    string
	}{
		{"幕红·夜", "curtain-night", themeModeDark},
		{"墨白·日", "inkwhite-day", themeModeLight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const scale = 3
			app := NewMockApp()
			app.PaletteID = tc.palette
			app.ThemeMode = tc.mode
			focusForTest(app, app.Streamers[0])
			app.Router.Push(routePlayer)
			app.setVolume(0.5)
			tt := ui.NewTester(app.Shell, 1440, 900)
			tt.SetScale(scale)
			// 控制条是玻璃：下面的画面会透上来，量墨色得先关掉透明。
			tt.SetPreferences(ui.Preferences{ReduceTransparency: true})
			img := tt.Image()

			hot, ok := tt.Find("音量")
			if !ok {
				t.Fatal("控制条里没有音量条")
			}
			mid := int((hot.Y + hot.H/2) * scale)

			// 填充段：50% 音量，热区从 +8 DIP 到 40% 宽这一段稳稳落在填充里。
			fx0, fx1 := int((hot.X+8)*scale), int((hot.X+hot.W*0.4)*scale)
			span := (fx1 - fx0) * 7
			if n := countNear(img, fx0, mid-3, fx1, mid+4, theme.AccentOnInk, 12); n < span*9/10 {
				t.Errorf("填充只有 %d/%d 个像素是 AccentOnInk %v（Accent 是 %v）：滑块应该整段是主题色",
					n, span, theme.AccentOnInk, theme.Accent)
			}

			// 百分比：热区右边 gap 8 之后那 36 DIP。
			px0, px1 := int((hot.X+hot.W+8)*scale), int((hot.X+hot.W+44)*scale)
			// 「不是主题色」这一条只认 AccentOnInk：墨白·日的 Accent 是近黑，
			// 而控制条本身也是近黑，拿它当判据分不出字和底。那一套配色里
			// 数字一旦染成 Accent 等于没画，由下面的亮像素判据兜住。
			if n := countNear(img, px0, mid-10, px1, mid+10, theme.AccentOnInk, 12); n > 0 {
				t.Errorf("百分比有 %d 个像素是主题色 %v：数字不该跟配色走", n, theme.AccentOnInk)
			}
			if n := litPixels(img, px0, mid-10, px1, mid+10); n < 20 {
				t.Errorf("百分比只量到 %d 个亮像素：「50%%」没画出来或采样偏了", n)
			}
		})
	}
}
