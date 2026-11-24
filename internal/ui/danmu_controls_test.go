package ui

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// isAccentPixel 判断一个像素是不是当前配色的强调色。
// 不写死某个色相：主题换了，判据跟着 theme.Accent 走。
func isAccentPixel(r, g, b uint32) bool {
	a := theme.Accent
	return absInt(int(r)-int(a.R)) < 40 &&
		absInt(int(g)-int(a.G)) < 40 &&
		absInt(int(b)-int(a.B)) < 40
}

// countAccent 数一块区域里有多少强调色像素，坐标是设备像素。
func countAccent(img *image.RGBA, x0, y0, x1, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if isAccentPixel(r>>8, g>>8, b>>8) {
				n++
			}
		}
	}
	return n
}

// 开启弹幕时只有右下角的勾是主题色，电视机身保持常态白。
//
// 以前整颗图标都会随开启态染成 accent；现在状态由勾表达，机身染上色
// 就说明又退回去了。关闭态的勾位是禁止符，整颗图标都不该有 accent。
func TestDanmuToggleOnlyCheckIsAccent(t *testing.T) {
	app := NewMockApp()
	// 钉住一套强调色明确的深色配色，别让本机 state.json 决定。
	app.PaletteID = "curtain-night"
	app.ThemeMode = themeModeDark
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	tt := ui.NewTester(app.Shell, 1440, 900)
	const scale = 3
	tt.SetScale(scale)

	// 按钮 30×30，图标 20×20 居中。
	b, ok := tt.Find("关闭弹幕")
	if !ok {
		t.Fatal("找不到开启态的弹幕开关")
	}
	iconLeft := int((b.X + 5) * scale)
	iconTop := int((b.Y + 5) * scale)
	iconSize := int(danmuTVSize * scale)

	img := tt.Image()
	// 机身区：图标左上 50%×50%。徽标放大到 10 格以后，勾的左尖已经伸到
	// 57% 的位置，采样框再往右就会把勾的一角算成「机身被染色」。
	body := countAccent(img,
		iconLeft+iconSize/10, iconTop+iconSize/6,
		iconLeft+iconSize/2, iconTop+iconSize/2)
	if body > 0 {
		t.Errorf("开启态的电视机身有 %d 个主题色像素，应该只有勾染色", body)
	}
	// 勾在图标右下角：位置对、颜色对，两条都靠这块像素兜住。
	badge := countAccent(img,
		iconLeft+iconSize/2, iconTop+iconSize/2,
		iconLeft+iconSize, iconTop+iconSize)
	if badge < 10 {
		t.Errorf("开启态右下角只有 %d 个主题色像素，勾没画出来或颜色不对", badge)
	}

	app.DanmuOn = false
	tt.Frame()
	tt.Frame()
	img = tt.Image()
	if n := countAccent(img, iconLeft, iconTop, iconLeft+iconSize, iconTop+iconSize); n > 0 {
		t.Errorf("关闭态整颗图标有 %d 个主题色像素，应该全是常态色", n)
	}
	// 防呆：确认采样区落在图标上，而不是一片纯黑。
	if r, g, bl, _ := img.At(iconLeft+iconSize/2, iconTop+iconSize/2).RGBA(); (r>>8)+(g>>8)+(bl>>8) == 0 {
		t.Fatal("图标中心是全黑：采样坐标偏了")
	}
}

// danmuIconInk 量一颗弹幕按钮里亮像素的包围盒和「机身左边缘」那条竖线的中心。
//
// 弹幕图标是「电视机身 + 右下角徽标」两层叠出来的：徽标挂在角上、天生把
// 包围盒往下拽，所以包围盒中心不是这颗图标的视觉中心，机身才是。旁边
// lucide 图标居中的是字形本身，机身也必须落在同一条线上。
func danmuIconInk(t *testing.T, tt *ui.Tester, img *image.RGBA, name string, scale float32) (w, h, bodyCY float32) {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("找不到「%s」", name)
	}
	x0, y0 := int(r.X*scale), int(r.Y*scale)
	x1, y1 := int((r.X+r.W)*scale), int((r.Y+r.H)*scale)

	lit := func(x, y int) bool {
		c := img.RGBAAt(x, y)
		return int(c.R)+int(c.G)+int(c.B) > 120
	}

	top, bot, left, right := -1, -1, -1, -1
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			if !lit(x, y) {
				continue
			}
			if top < 0 {
				top = y
			}
			bot = y
			if left < 0 || x < left {
				left = x
			}
			if x > right {
				right = x
			}
		}
	}
	if top < 0 {
		t.Fatalf("「%s」没有墨迹：采样坐标偏了", name)
	}

	// 最左边那列墨迹就是机身的左边缘：徽标永远在右下角，够不着这里。
	bt, bb := -1, -1
	for y := y0; y < y1; y++ {
		if lit(left, y) {
			if bt < 0 {
				bt = y
			}
			bb = y
		}
	}
	if bt < 0 {
		t.Fatalf("「%s」找不到机身边缘", name)
	}
	return float32(right+1-left) / scale, float32(bot+1-top) / scale,
		(float32(bt+bb+1)/2 - (r.Y+r.H/2)*scale) / scale
}

// 开机身边缘必须落在热区中心 ±1px：这是「一排图标在同一条水平线上」的实际含义。
//
// 以前按「机身 + 徽标」的合成包围盒居中，徽标把包围盒往下拽，机身就抬高了
// 2px——一排看过去弹幕那组明显比小窗、全屏高。这条量的是机身左边缘那条
// 竖线，不量合成包围盒。
//
// 同时三种状态（开 / 关 / 设置面板展开）的机身尺寸必须一致，否则切换开关
// 时整颗图标会跳。
func TestDanmuTVIconAligned(t *testing.T) {
	const scale = 3
	cases := []struct {
		name  string
		on    bool
		label string
	}{
		{"开启态", true, "关闭弹幕"},
		{"关闭态", false, "开启弹幕"},
	}

	widths := make([]float32, 0, 3)
	heights := make([]float32, 0, 3)
	labels := make([]string, 0, 3)
	for _, tc := range cases {
		app := NewMockApp()
		// 图标墨迹的判据按当前强调色扫；钉住配色，别让本机 state.json 决定。
		app.PaletteID = "curtain-night"
		app.ThemeMode = themeModeDark
		focusForTest(app, app.Streamers[0])
		app.Router.Push(routePlayer)
		app.DanmuOn = tc.on
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.SetScale(scale)
		w, h, bodyCY := danmuIconInk(t, tt, tt.Image(), tc.label, scale)
		widths, heights, labels = append(widths, w), append(heights, h), append(labels, tc.name)
		if bodyCY > 1 || bodyCY < -1 {
			t.Errorf("%s 的机身边缘中心偏移 %.2fpx，和旁边 lucide 图标不在一条线上", tc.name, bodyCY)
		}
	}

	// 设置入口是另一颗按钮，单独量一次。
	app := NewMockApp()
	// 钉住一套强调色明确的深色配色，别让本机 state.json 决定。
	app.PaletteID = "curtain-night"
	app.ThemeMode = themeModeDark
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.SetScale(scale)
	w, h, bodyCY := danmuIconInk(t, tt, tt.Image(), "弹幕设置", scale)
	widths, heights, labels = append(widths, w), append(heights, h), append(labels, "设置")
	if bodyCY > 1 || bodyCY < -1 {
		t.Errorf("设置 的机身边缘中心偏移 %.2fpx，和旁边 lucide 图标不在一条线上", bodyCY)
	}

	for i := 1; i < len(widths); i++ {
		if d := widths[i] - widths[0]; d > 1 || d < -1 {
			t.Errorf("%s 的墨迹宽 %.1f，%s 是 %.1f：差 %.1fpx，切换状态会跳",
				labels[i], widths[i], labels[0], widths[0], d)
		}
		if d := heights[i] - heights[0]; d > 1 || d < -1 {
			t.Errorf("%s 的墨迹高 %.1f，%s 是 %.1f：差 %.1fpx，切换状态会跳",
				labels[i], heights[i], labels[0], heights[0], d)
		}
	}
}
