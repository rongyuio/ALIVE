package ui

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 通知里的字必须垂直居中。
//
// 用户报的是「通知的字不垂直居中」。量出来的原因是容器有 MinHeight(38)
// 但只做了交叉轴对齐，MinHeight 撑出来的余量全落在下面，文字就贴着上边。
// 修法是把容器本身 Center()（主轴 + 交叉轴都居中），不是给文字加位移。
// 换成 MyGo 的 ToastViewportBase 之后这条仍然守着：胶囊由 ToastBase 的
// Root 给出，尺寸还是这套。
//
// 判据取墨迹：胶囊上下边界之间，文字墨迹到上沿与到下沿的距离必须相等。
// 允许 1px 的差——字形本身不是上下对称的，抗锯齿也会吃掉半像素。
func TestToastTextIsVerticallyCentered(t *testing.T) {
	app := NewMockApp()
	app.toastTone("已刷新 54 位主播", toastSuccess)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	// 入场动画 220ms，推过它再量，否则量到的是透明度中间态。
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	box := toastBox(t, tt, "已刷新 54 位主播")
	img := tt.Image()
	top, bot := int(box.Y), int(box.Y+box.H)-1
	// 文字区：左边距 12 + 图标 15 + 间距 9，从图标右边开始。
	inkTop, inkBot := inkRows(img, int(box.X)+40, int(box.X+box.W)-8, top, bot, func(r, g, b uint8) bool {
		return r > 180 && g > 180 && b > 180
	})
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
	app.toastTone("已刷新 54 位主播", toastSuccess)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(400 * time.Millisecond)
	tt.Frame()

	box := toastBox(t, tt, "已刷新 54 位主播")
	img := tt.Image()
	top, bot := int(box.Y), int(box.Y+box.H)-1
	iconX0 := int(box.X) + 12
	iconTop, iconBot := inkRows(img, iconX0, iconX0+15, top, bot, func(r, g, b uint8) bool {
		return int(r)+int(g)+int(b) > 250
	})
	inkTop, inkBot := inkRows(img, int(box.X)+40, int(box.X+box.W)-8, top, bot, func(r, g, b uint8) bool {
		return r > 180 && g > 180 && b > 180
	})
	if iconTop == 0 || inkTop == 0 {
		t.Fatalf("找不到图标或文字（icon=%d..%d text=%d..%d）", iconTop, iconBot, inkTop, inkBot)
	}
	iconMid := float64(iconTop+iconBot) / 2
	textMid := float64(inkTop+inkBot) / 2
	if d := iconMid - textMid; d < -2 || d > 2 {
		t.Fatalf("图标与文字中心差了 %.1f DIP：icon %.1f、text %.1f", d, iconMid, textMid)
	}
}

// toastBox 按无障碍树里的 status 找通知本体。文本单独一张 Text 量不到
// MinHeight 撑出来的胶囊，只有 Root 带着完整盒子。
func toastBox(t *testing.T, tt *ui.Tester, name string) ui.Rect {
	t.Helper()
	info, ok := tt.AccessByRole(ui.RoleStatus, name)
	if !ok {
		var got []string
		for _, a := range tt.AccessAll() {
			if a.Role == ui.RoleStatus {
				got = append(got, a.Name)
			}
		}
		t.Fatalf("找不到通知 status %q；现有的 status: %v", name, got)
	}
	return info.Bounds
}

// inkRows 返回 [x0,x1)×[y0,y1] 里第一个和最后一个满足 ink 的行号，0 表示没有。
func inkRows(img *image.RGBA, x0, x1, y0, y1 int, ink func(r, g, b uint8) bool) (int, int) {
	top, bot := 0, 0
	for y := y0; y <= y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, b, _ := img.RGBAAt(x, y).RGBA()
			if ink(uint8(r>>8), uint8(g>>8), uint8(b>>8)) {
				if top == 0 {
					top = y
				}
				bot = y
				break
			}
		}
	}
	return top, bot
}
