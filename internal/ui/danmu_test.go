package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 弹幕层必须铺在画面上方区域：轨道从 stage 顶部往下排。
// （曾经因为 Fill() 在 Box 里不撑开高度，整层塌到画面中部。）
func TestDanmuPosition(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	app.Danmu = []danmuLine{
		{Text: "测试弹幕", Track: 0, Born: time.Now(), Dur: 10 * time.Second},
	}
	tt := ui.NewTester(app.Shell, 1440, 900)

	r, ok := tt.Find("测试弹幕")
	if !ok {
		t.Fatal("弹幕没有渲染")
	}
	// 顶栏 64 + 轨道 0 的 16px 起点：应落在 100 以内，而不是画面中部
	if r.Y > 100 {
		t.Errorf("弹幕 y=%.0f，掉出了画面顶部区域", r.Y)
	}
}

// 纯图弹幕的判定和尺寸换算：整条正文为空、只带一条表情时才算「图弹幕」。
func TestDanmuImageOnly(t *testing.T) {
	pic := []live.Emote{{URL: "https://example.com/a.png", Width: 100, Height: 50}}

	if !(danmuLine{Emotes: pic}).imageOnly() {
		t.Fatal("正文为空 + 有表情，应判为纯图弹幕")
	}
	if (danmuLine{Text: "hi", Emotes: pic}).imageOnly() {
		t.Fatal("正文非空时不算纯图弹幕")
	}
	if (danmuLine{Text: "   "}).imageOnly() {
		t.Fatal("没有表情时不算纯图弹幕")
	}
}

// 屏蔽词是包含匹配、忽略大小写；没有词时一律放行。
func TestDanmuBlocked(t *testing.T) {
	app := NewMockApp()
	app.DanmuFilters = nil

	if app.danmuBlocked("随便说点什么") {
		t.Fatal("没有屏蔽词时不该拦截任何弹幕")
	}

	app.DanmuFilters = []string{"加微信", "GG"}
	if !app.danmuBlocked("快来加微信啊") {
		t.Fatal("命中关键词的弹幕应被拦截")
	}
	if !app.danmuBlocked("打得好 gg") {
		t.Fatal("大小写不同也应命中")
	}
	if app.danmuBlocked("这波操作可以") {
		t.Fatal("没命中的弹幕不该被拦截")
	}
}

// 屏蔽词增删：空词不加、重复（忽略大小写）不重复加。
func TestDanmuFilterAddRemove(t *testing.T) {
	app := NewMockApp()
	// NewMockApp 会 restore 真实落盘状态，屏蔽词可能带着上次跑出来的词，
	// 这组断言只关心本测试自己的增删，先清干净。
	app.DanmuFilters = nil

	app.DanmuFilterQuery = "   "
	app.addDanmuFilter()
	if len(app.DanmuFilters) != 0 {
		t.Fatalf("空词不该加进去，得到 %v", app.DanmuFilters)
	}

	app.DanmuFilterQuery = " 广告 "
	app.addDanmuFilter()
	if len(app.DanmuFilters) != 1 || app.DanmuFilters[0] != "广告" {
		t.Fatalf("加词后应是 [广告]，得到 %v", app.DanmuFilters)
	}
	if app.DanmuFilterQuery != "" {
		t.Fatalf("加词后输入框应清空，得到 %q", app.DanmuFilterQuery)
	}

	app.DanmuFilterQuery = "广告"
	app.addDanmuFilter()
	if len(app.DanmuFilters) != 1 {
		t.Fatalf("重复词不该重复加，得到 %v", app.DanmuFilters)
	}

	app.removeDanmuFilter(0)
	if len(app.DanmuFilters) != 0 {
		t.Fatalf("删词后应为空，得到 %v", app.DanmuFilters)
	}
}

// emoteSize 按目标高度等比缩放；宽高缺失时按 1:1 处理，不能除零。
func TestEmoteSize(t *testing.T) {
	w, h := emoteSize(live.Emote{Width: 100, Height: 50}, 26)
	if h != 26 || w != 52 {
		t.Fatalf("100×50 缩到高 26：得到 %.1f×%.1f，期望 52×26", w, h)
	}
	w, h = emoteSize(live.Emote{}, 26)
	if w != 26 || h != 26 {
		t.Fatalf("宽高缺失应按 1:1：得到 %.1f×%.1f", w, h)
	}
}
