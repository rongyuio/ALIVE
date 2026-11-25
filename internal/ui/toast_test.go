package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 通知栈：两条不同的通知要同时在场，不能后一条把前一条顶掉。
//
// 旧实现只有一条 a.ToastText，连点两次关注只剩最后一条——用户「经常
// 看不到通知」的一半原因就在这里。
func TestToastsStack(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	app.toastTone("已关注 甲", toastSuccess)
	app.toastTone("已关注 乙", toastSuccess)
	tt.Frame()

	if !tt.HasText("已关注 甲") || !tt.HasText("已关注 乙") {
		t.Fatalf("两条通知没有同时显示：texts=%q", tt.Texts())
	}
	a := toastBox(t, tt, "已关注 甲")
	b := toastBox(t, tt, "已关注 乙")
	// 新的在下、旧的在上面一档，两条都贴右边，不能互相压住。
	if b.Y <= a.Y {
		t.Errorf("新通知不在旧通知下方：甲 y=%.0f，乙 y=%.0f", a.Y, b.Y)
	}
	if a.X+a.W < 1400 || b.X+b.W < 1400 {
		t.Errorf("通知没贴住右边：甲 %.0f..%.0f，乙 %.0f..%.0f", a.X, a.X+a.W, b.X, b.X+b.W)
	}
	if a.Y+a.H > b.Y {
		t.Errorf("两条通知叠在一起：甲 %.0f..%.0f，乙 %.0f..%.0f", a.Y, a.Y+a.H, b.Y, b.Y+b.H)
	}
}

// 同一条消息重复触发只刷新那一条，不堆成两条。
func TestSameToastReplacesNotStacks(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	app.toastTone("已刷新 54 位主播", toastSuccess)
	tt.Frame()
	app.toastTone("已刷新 54 位主播", toastSuccess)
	tt.Frame()

	n := 0
	for _, s := range tt.Texts() {
		if s == "已刷新 54 位主播" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同一条消息显示了 %d 次：texts=%q", n, tt.Texts())
	}
}

// 通知要能被读屏读出来。
func TestToastIsAnnounced(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.UseScreenReader()
	app.toastTone("已订阅该分区", toastSuccess)
	tt.Frame()

	found := false
	for _, s := range tt.Announcements() {
		if s == "已订阅该分区" {
			found = true
		}
	}
	if !found {
		t.Fatalf("读屏没收到通知：announcements=%v", tt.Announcements())
	}
}

// 指针停在通知上时计时暂停：这是「来不及看清就没了」的修法之一。
func TestToastPausesUnderPointer(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	app.toastTone("已刷新 54 位主播", toastSuccess)
	tt.Frame()

	box := toastBox(t, tt, "已刷新 54 位主播")
	tt.Move(box.X+box.W/2, box.Y+box.H/2)
	// 远超 3.2 秒的停留时间，指针压着就不该消失。
	tt.Advance(10 * time.Second)
	if !tt.HasText("已刷新 54 位主播") {
		t.Fatal("指针停在通知上，它还是到点消失了")
	}
	// 指针移开，计时继续，到点消失。
	tt.Move(1, 1)
	tt.Advance(5 * time.Second)
	if tt.HasText("已刷新 54 位主播") {
		t.Fatal("指针移开后通知没有按时消失")
	}
}

// 后台写盘失败要在界面上说一声，而不是只落进日志；一次会话只提醒一次。
func TestStateSaveFailureNotifiesOnce(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	const want = "设置没能保存，重启后可能丢失"

	app.warnStateSave()
	tt.Frame()
	if !tt.HasText(want) {
		t.Fatalf("写盘失败没有通知：texts=%q", tt.Texts())
	}
	// 等这条过期，再失败一次：不该再弹。
	tt.Advance(7 * time.Second)
	app.warnStateSave()
	tt.Frame()
	if tt.HasText(want) {
		t.Fatal("同一次会话里写盘失败提醒了第二次")
	}
}
