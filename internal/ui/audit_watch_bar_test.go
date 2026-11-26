package ui

import (
	"image"
	"testing"
	"time"
)

// 墙格底栏：**仍然只在 hover / 键盘聚焦时显示**。
//
// 改的是「构建」，不是「显示」：元素树里常驻（读屏找得到、指针扫过时树不抖），
// 但常态 `Opacity(0)` + `IgnoreInput(true)`，屏幕上一个像素都不画。
//
// 这条用整条底栏区域的像素盯住「显示」这一半。格子里故意不放播放器：GIF 假
// 播放器每帧换颜色，逐像素比会把它当成"底栏变了"。没有播放器时格子画的是静止
// 的封面渐变，两个时刻的差异只可能来自底栏本身。
func TestWatchBarHiddenAtRestShownOnHover(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	// 有会话、没有播放器：格子画静止封面，不画会动的 GIF。
	app.watch.init()
	app.watch.sessions[watchKey(rooms[0])] = &roomSession{
		key: watchKey(rooms[0]), room: rooms[0],
	}
	tt.Frame()

	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到格子")
	}
	// 底栏高 84，贴在格子底部；取内部一块（避开圆角）。
	x0, y0 := int(r.X)+14, int(r.Y+r.H)-70
	x1, y1 := int(r.X+r.W)-14, int(r.Y+r.H)-8

	changed := func(a, b *image.RGBA) int {
		n := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				p, q := a.RGBAAt(x, y), b.RGBAAt(x, y)
				dr, dg, db := int(p.R)-int(q.R), int(p.G)-int(q.G), int(p.B)-int(q.B)
				if dr*dr+dg*dg+db*db > 64 {
					n++
				}
			}
		}
		return n
	}
	area := (y1 - y0) * (x1 - x0)

	rest := tt.Image()
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	tt.Advance(200 * time.Millisecond) // 等透明度过渡跑完
	tt.Frame()
	hover := tt.Image()

	if n := changed(rest, hover); n*100/area < 20 {
		t.Fatalf("悬停前后底栏区域只有 %d/%d 像素不同：底栏要么常驻可见、要么根本没显示", n, area)
	}

	// 移开：又回到「只有画面」。
	tt.Move(r.X+r.W/2, 20)
	tt.Frame()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	if n := changed(rest, tt.Image()); n*100/area >= 20 {
		t.Fatalf("移开指针后底栏没有消失：%d/%d 像素仍与常态不同", n, area)
	}
}
