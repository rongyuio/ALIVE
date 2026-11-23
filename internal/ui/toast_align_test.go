package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// toast 里的字必须垂直居中。
//
// 用户报的是「通知的字不垂直居中」。量出来的原因是容器有 MinHeight(38)
// 但只做了交叉轴对齐，MinHeight 撑出来的余量全落在下面，文字就贴着上边。
// 修法是把容器本身 Center()（主轴 + 交叉轴都居中），不是给文字加位移。
//
// 判据取墨迹：胶囊上下边界之间，文字墨迹到上沿与到下沿的距离必须相等。
// 允许 1px 的差——字形本身不是上下对称的，抗锯齿也会吃掉半像素。
func TestToastTextIsVerticallyCentered(t *testing.T) {
	app := NewMockApp()
	app.ToastText = "已刷新 54 位主播"
	app.ToastTone = toastSuccess
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	// 入场动画 220ms，推过它再量，否则量到的是透明度中间态。
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	img := tt.Image()
	// 胶囊底色：取右端一列，找最亮的连续段。
	const midX = 1400
	top, bot := 0, 0
	for y := 800; y < 900; y++ {
		c := img.RGBAAt(midX, y)
		if c.R > 250 && c.G > 245 && c.B > 245 {
			if top == 0 {
				top = y
			}
			bot = y
		}
	}
	if top == 0 || bot <= top {
		t.Fatalf("没找到胶囊（top=%d bot=%d）", top, bot)
	}

	// 文字墨迹：x 从 1315 起，避开左侧的勾。
	inkTop, inkBot := 0, 0
	for y := top; y <= bot; y++ {
		for x := 1315; x < 1420; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if int(r>>8)+int(g>>8)+int(b>>8) < 400 {
				if inkTop == 0 {
					inkTop = y
				}
				inkBot = y
				break
			}
		}
	}
	if inkTop == 0 {
		t.Fatal("没找到文字墨迹")
	}

	above, below := inkTop-top, bot-inkBot
	if d := above - below; d < -1 || d > 1 {
		t.Fatalf("文字不居中：胶囊 %d..%d，墨迹 %d..%d，上留 %d、下留 %d（差 %d）",
			top, bot, inkTop, inkBot, above, below, d)
	}
}

// 图标与文字的中心也要对齐：两者行盒高度不同，只对齐交叉轴会错开。
func TestToastIconAlignedWithText(t *testing.T) {
	app := NewMockApp()
	app.ToastText = "已刷新 54 位主播"
	app.ToastTone = toastSuccess
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	img := tt.Image()
	iconTop, iconBot := 0, 0
	for y := 800; y < 900; y++ {
		for x := 1285; x < 1312; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if int(r>>8)+int(g>>8)+int(b>>8) < 500 {
				if iconTop == 0 {
					iconTop = y
				}
				iconBot = y
				break
			}
		}
	}
	inkTop, inkBot := 0, 0
	for y := 800; y < 900; y++ {
		for x := 1315; x < 1420; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if int(r>>8)+int(g>>8)+int(b>>8) < 400 {
				if inkTop == 0 {
					inkTop = y
				}
				inkBot = y
				break
			}
		}
	}
	if iconTop == 0 || inkTop == 0 {
		t.Fatalf("找不到图标或文字（icon=%d..%d text=%d..%d）", iconTop, iconBot, inkTop, inkBot)
	}
	iconMid := float64(iconTop+iconBot) / 2
	textMid := float64(inkTop+inkBot) / 2
	if d := iconMid - textMid; d < -2 || d > 2 {
		t.Fatalf("图标与文字中心差了 %.1f DIP：icon %.1f、text %.1f", d, iconMid, textMid)
	}
}
