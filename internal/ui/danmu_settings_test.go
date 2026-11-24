package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 颜色 / 描边的色块就是那颗 38×24 的胶囊：颜色铺到四边，只留 1px 描边。
//
// 这条盯一个具体回归：色块曾经直接用 ui.ColorWell，它的内芯写死 36×20，
// 外面还有 4px padding + 1px 边；塞进 38×24 的容器时内芯比内容盒还大，
// 被顶得偏出容器，颜色也填不满。
func TestDanmuColorWellFillsCapsule(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuSettingsOpen = true
	app.DanmuColor = ui.RGB(255, 0, 0)
	app.DanmuStroke = ui.RGB(0, 0, 255)
	tt := ui.NewTester(app.Shell, 1440, 900)
	img := tt.Image()

	for _, tc := range []struct {
		name string
		want ui.Color
	}{
		{"颜色", app.DanmuColor},
		{"描边", app.DanmuStroke},
	} {
		info, ok := tt.AccessByRole(ui.RoleColorWell, tc.name)
		if !ok {
			t.Fatalf("找不到「%s」色块：没有 Label 的控件不进无障碍树", tc.name)
		}
		b := info.Bounds
		if b.W != danmuWellW || b.H != danmuWellH {
			t.Errorf("「%s」色块 %.1f×%.1f，期望 %.0f×%.0f", tc.name, b.W, b.H, danmuWellW, danmuWellH)
		}
		// 四边中点向里 2px 就该是纯色；留白只允许那 1px 描边。
		for _, pt := range [][2]float32{{2, b.H / 2}, {b.W - 2, b.H / 2}, {b.W / 2, 2}, {b.W / 2, b.H - 2}} {
			r, g, bl, _ := img.At(int(b.X+pt[0]), int(b.Y+pt[1])).RGBA()
			if near(int(r>>8), int(tc.want.R)) && near(int(g>>8), int(tc.want.G)) && near(int(bl>>8), int(tc.want.B)) {
				continue
			}
			t.Errorf("「%s」在 (%.0f,%.0f) 取到 %d,%d,%d，期望 %d,%d,%d：颜色没铺满胶囊",
				tc.name, b.X+pt[0], b.Y+pt[1], r>>8, g>>8, bl>>8, tc.want.R, tc.want.G, tc.want.B)
		}
	}
}

// 点色块开取色器。取色器是另一个浮层，指针挪出去之后设置面板不能先把
// 自己关掉——那会把取色器一起带走，等于点开就消失。
func TestDanmuColorWellKeepsSettingsOpen(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuSettingsOpen = true
	tt := ui.NewTester(app.Shell, 1440, 900)

	info, ok := tt.AccessByRole(ui.RoleColorWell, "颜色")
	if !ok {
		t.Fatal("找不到颜色色块")
	}
	b := info.Bounds
	tt.ClickAt(b.X+b.W/2, b.Y+b.H/2)
	if !app.DanmuColorOpen {
		t.Fatal("点色块没有打开取色器")
	}

	// 指针挪到画面上并等过 hover 关门那 220ms：两个面板都不该动。
	// 设置面板现在是点击触发，任何悬停状态都不参与开关。
	tt.Move(600, 300)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if !app.DanmuSettingsOpen || !app.DanmuColorOpen {
		t.Fatalf("指针离开后浮层被关了（设置面板 %v，取色器 %v）",
			app.DanmuSettingsOpen, app.DanmuColorOpen)
	}

	// Esc 只收掉最上面那层取色器；设置面板是用户点开的，留着。
	tt.Key(0, ui.KeyEscape)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if app.DanmuColorOpen {
		t.Fatal("Esc 没有收掉取色器")
	}
	if !app.DanmuSettingsOpen {
		t.Fatal("Esc 收取色器时把设置面板一起关掉了")
	}
}

// 弹幕设置面板只认点击：指针停在按钮上不该自己弹出来。
//
// 面板里有滑杆和取色器，hover 打开会在指针路过时打断操作；这条盯住
// 「有人又把它接回 hoverClose」这种回退。
func TestDanmuSettingsOpensOnClickOnly(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)

	r, ok := tt.Find("弹幕设置")
	if !ok {
		t.Fatal("找不到弹幕设置按钮")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Advance(600 * time.Millisecond)
	tt.Frame()
	if app.DanmuSettingsOpen {
		t.Fatal("指针悬停在弹幕设置上就打开了面板")
	}

	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if !app.DanmuSettingsOpen {
		t.Fatal("点击弹幕设置没有打开面板")
	}

	// 再点一次收起来
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if app.DanmuSettingsOpen {
		t.Fatal("再点一次弹幕设置没有收起面板")
	}
}

func near(a, b int) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 4
}

// 描边色按色块明暗翻面：白色色块配白边等于没画，黑色色块配黑边同理。
// 半透明的颜色先和棋盘格合成再判，20% 的白看起来仍然是白的。
func TestDanmuWellRingContrasts(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    ui.Color
		dark bool // true 表示该给暗环
	}{
		{"纯白", ui.RGB(255, 255, 255), true},
		{"浅灰", ui.RGB(200, 200, 200), true},
		{"中灰", ui.RGB(128, 128, 128), false},
		{"纯黑", ui.RGB(0, 0, 0), false},
		{"20% 白", ui.RGBA(255, 255, 255, 0.2), true},
		{"10% 蓝", ui.RGBA(0, 0, 255, 0.1), true},
	} {
		ring := danmuWellRing(tc.c)
		if dark := ring.R < 128; dark != tc.dark {
			t.Errorf("%s 的描边 %+v：期望暗环=%v", tc.name, ring, tc.dark)
		}
		if ring.A == 0 {
			t.Errorf("%s 的描边完全透明", tc.name)
		}
	}
}
