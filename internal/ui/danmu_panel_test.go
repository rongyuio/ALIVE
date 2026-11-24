package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 右侧弹幕面板按默认宽度 196 展开，且必须和舞台分列。
func TestDanmuPanelWidth(t *testing.T) {
	app := NewMockApp()
	app.DanmuPanelOn = true
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)

	r, ok := tt.Find("弹幕列表")
	if !ok {
		t.Fatal("找不到弹幕面板")
	}
	if r.W != app.DanmuPanelW {
		t.Errorf("面板宽 %.1f，期望 %.1f", r.W, app.DanmuPanelW)
	}
	if r.X <= 0 || r.X+r.W > 1440 {
		t.Errorf("面板位置越界：%+v", r)
	}
}

// 面板的 meta 行：用户等级胶囊 + 粉丝牌胶囊（名称 + 牌子等级）+ 昵称。
// 两个等级不是同一个数，各占一个胶囊。
func TestDanmuPanelShowsMedalAndLevel(t *testing.T) {
	app := NewMockApp()
	app.DanmuPanelOn = true
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuLog = []danmuLine{{
		Text:     "测试弹幕",
		Nick:     "小明",
		Level:    12,
		Medal:    live.Medal{Name: "德云色", Level: 24},
		Platform: live.Bilibili,
		Born:     time.Now(),
		Dur:      time.Second,
	}}
	tt := ui.NewTester(app.Shell, 1440, 900)

	if !tt.HasText("Lv.12") {
		t.Fatalf("面板里没有用户等级胶囊，文本树 = %v", tt.Texts())
	}
	if !tt.HasText("德云色 24") {
		t.Fatalf("面板里没有粉丝牌胶囊，文本树 = %v", tt.Texts())
	}
}

// 正文里的 [表情名] 要换成图；换不了的时候原文必须还在。
func TestDanmuPanelReplacesEmotePlaceholder(t *testing.T) {
	app := NewMockApp()
	app.DanmuPanelOn = true
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuLog = []danmuLine{
		{Text: "哈哈[财神]", Nick: "甲", Platform: live.Douyu, Born: time.Now(), Dur: time.Second},
		{Text: "好[不存在的表情]", Nick: "乙", Platform: live.Douyu, Born: time.Now(), Dur: time.Second},
	}
	tt := ui.NewTester(app.Shell, 1440, 900)

	if tt.HasText("哈哈[财神]") {
		t.Fatal("已知表情的占位符没有被换成图")
	}
	if !tt.HasText("好[不存在的表情]") {
		t.Fatalf("未知占位符被吞掉了，文本树 = %v", tt.Texts())
	}
}

// 开关窗口全屏会改列表视口的高度：视口一变 MaxY 就跳，而 Y 还停在旧的底上。
// 跟随判据必须在这一帧重新钉到底，否则「取消全屏」之后新弹幕只会累计，
// 列表定在原地不动。
func TestDanmuPanelFollowsAfterFullscreenToggle(t *testing.T) {
	app := NewMockApp()
	app.DanmuPanelOn = true
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.DanmuPanelOn = true
	for i := 0; i < 80; i++ {
		app.DanmuLog = append(app.DanmuLog, danmuLine{
			Text: "弹幕", Nick: "甲", Born: time.Now(), Dur: time.Second,
		})
	}
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if app.PanelScroll.MaxY <= 0 {
		t.Fatalf("列表没有溢出（MaxY=%v），这条测试没有分辨力", app.PanelScroll.MaxY)
	}
	if app.PanelScroll.Y < app.PanelScroll.MaxY-1 {
		t.Fatalf("开场就不在底部：Y=%v MaxY=%v", app.PanelScroll.Y, app.PanelScroll.MaxY)
	}

	for _, full := range []bool{true, false} {
		app.WinFull = full
		tt.Frame()
		app.DanmuLog = append(app.DanmuLog, danmuLine{
			Text: "新弹幕", Nick: "乙", Born: time.Now(), Dur: time.Second,
		})
		tt.Frame()
		if app.panelPending != 0 {
			t.Fatalf("WinFull=%v 后新弹幕进了未读数 %d：跟随断了", full, app.panelPending)
		}
		if app.PanelScroll.Y < app.PanelScroll.MaxY-1 {
			t.Fatalf("WinFull=%v 后没有滚到底：Y=%v MaxY=%v", full, app.PanelScroll.Y, app.PanelScroll.MaxY)
		}
	}
}
