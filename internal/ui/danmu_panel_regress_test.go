package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 弹幕设置里的滑杆：按在 4px 轨道上开始拖之后，指针纵向离开轨道也要继续
// 跟手，松手时最后一格也要落值。
//
// PointerPosition 的 over / Pressed 都是几何包含，指针一离开那条 4px 带
// 就一起变假；Dragged 是 press 级状态，指针跑到元素外仍然为真，x 也仍然
// 是轨道内的局部坐标。
func TestDanmuSliderKeepsDraggingOutsideTrack(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuSettingsOpen = true
	app.DanmuFont = 20
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	var track ui.Rect
	found := false
	for _, n := range tt.AccessAll() {
		// 同一行里还有一枚同名的文字标签；轨道只有 4 高。
		if n.Name == "字体" && n.Bounds.W > 0 && n.Bounds.H <= 6 {
			track, found = n.Bounds, true
			break
		}
	}
	if !found {
		t.Fatal("无障碍树里找不到字体滑杆的 4px 轨道")
	}

	// 按在轨道中点：值先落到 14 + round(0.5*8)*2 = 22
	tt.Press(track.X+track.W/2, track.Y+track.H/2)
	tt.Frame()
	if app.DanmuFont != 22 {
		t.Fatalf("按下轨道中点后字体是 %.0f，期望 22", app.DanmuFont)
	}

	// 横向拖到右端、纵向离开 4px 轨道：拖出轨道之后仍然要跟手
	outY := track.Y + track.H + 24
	tt.Move(track.X+track.W-1, outY)
	tt.Frame()
	if app.DanmuFont != 30 {
		t.Fatalf("指针拖出轨道后字体停在 %.0f，期望跟到 30", app.DanmuFont)
	}

	// 松手在轨道外，最后一格也要落值：x=25% 对应 14 + 2*2 = 18
	tt.Move(track.X+track.W/4, outY)
	tt.Frame()
	tt.Release(track.X+track.W/4, outY)
	tt.Frame()
	if app.DanmuFont != 18 {
		t.Fatalf("在轨道外松手后字体是 %.0f，期望 18", app.DanmuFont)
	}
}

// 弹幕面板的历史满 300 条之后，新弹幕仍然要跟随 / 累计。
//
// 新消息检测原来拿 len(DanmuLog) 和 panelSeen 相减，可日志到 danmuLogMax
// 后长度恒定；差值永远是 0，于是既不把列表钉回底部，也不出现「N 条新弹幕」。
func TestDanmuPanelCountsNewAfterLogFull(t *testing.T) {
	app := NewMockApp()
	// 这一间必须已经出画：否则首帧会走取流，startStream(false) 会按
	// 「换房间」清空弹幕历史，这条用例准备的那 300 条就没了。
	focusWithPlayer(t, app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuPanelOn = true

	base := time.Now()
	for i := 0; i < danmuLogMax; i++ {
		app.DanmuLog = append(app.DanmuLog, danmuLine{
			Text:     "历史弹幕",
			Nick:     "甲",
			Platform: live.Bilibili,
			Born:     base.Add(time.Duration(i) * time.Millisecond),
			Dur:      time.Second,
		})
	}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	// 首帧会把这 300 条都算成未读；手动回到「已读且不在底部」的基线。
	app.panelPending = 0
	app.PanelScroll.Y = 0
	tt.Frame()
	if app.panelPending != 0 {
		t.Fatalf("没有新弹幕却累计了 %d 条", app.panelPending)
	}

	// 日志已经满了：再收一条只会顶掉最旧的一条，长度仍是 300。
	app.DanmuLog = append(app.DanmuLog, danmuLine{
		Text:     "最后一条新弹幕",
		Nick:     "乙",
		Platform: live.Bilibili,
		Born:     base.Add(time.Hour),
		Dur:      time.Second,
	})
	app.DanmuLog = app.DanmuLog[len(app.DanmuLog)-danmuLogMax:]
	if len(app.DanmuLog) != danmuLogMax {
		t.Fatalf("日志长度 %d，期望 %d", len(app.DanmuLog), danmuLogMax)
	}

	tt.Frame()
	if app.panelPending != 1 {
		t.Fatalf("日志满之后新弹幕累计 %d 条，期望 1", app.panelPending)
	}
}

// 醒目留言的平台背景色只校验长度不够：ui.Hex 遇到非十六进制会 panic。
// 平台字段是外部输入，不合法时退回强调色，不能让一条 '#ZZZZZZ' 打崩进程。
func TestSuperChatInvalidBackgroundFallsBack(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuPanelOn = true
	app.DanmuLog = []danmuLine{{
		Text:       "醒目留言正文",
		Nick:       "甲",
		Platform:   live.Bilibili,
		Kind:       danmuSuperChat,
		Price:      30,
		Background: "#ZZZZZZ",
		Born:       time.Now(),
		Dur:        time.Second,
	}}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if !tt.HasText("醒目留言正文") {
		t.Fatalf("坏背景色的醒目留言没有画出来，文本树 = %v", tt.Texts())
	}
}
