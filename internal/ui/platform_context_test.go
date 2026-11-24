package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 播放页属于正在看的主播，不属于进房之前停留的那个 tab。
func TestViewPlatformFollowsPlayer(t *testing.T) {
	app := NewMockApp()
	app.Platform = "huya"
	focusForTest(app, Streamer{Platform: "bilibili", RoomID: "170001", Name: "某主播"})

	if got := app.viewPlatform(); got != "huya" {
		t.Fatalf("首页应当跟着 tab 走，得到 %q", got)
	}

	app.Router.Push(routePlayer)
	if got := app.viewPlatform(); got != "bilibili" {
		t.Fatalf("播放页应当跟着主播走，得到 %q", got)
	}
}

// 没有主播上下文时回落到 tab 的平台，不能返回空串。
func TestViewPlatformFallsBack(t *testing.T) {
	app := NewMockApp()
	app.Platform = "douyin"
	app.setFocus(nil)
	app.Router.Push(routePlayer)

	if got := app.viewPlatform(); got != "douyin" {
		t.Fatalf("得到 %q，期望 douyin", got)
	}
}

// 从「全部关注」点开 B 站主播、tab 还停在虎牙时，搜索浮层搜的必须是
// 主播所在的平台，房间号直达也要落到 B 站。
func TestSearchFollowsPlayerPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "huya"
	focusForTest(app, Streamer{Platform: "bilibili", RoomID: "170001", Name: "某主播"})
	app.Router.Push(routePlayer)
	app.Streamers = []Streamer{
		{Platform: "huya", RoomID: "1", Name: "虎牙主播"},
		{Platform: "bilibili", RoomID: "2", Name: "B站主播"},
	}
	app.SearchQuery = "主播"

	hits := app.searchHits()
	if len(hits) != 1 || hits[0].S.Platform != "bilibili" {
		t.Fatalf("搜索结果 = %+v，期望只有 B 站那位", hits)
	}

	app.SearchOpen = false
	app.openRoomNumber("999")
	if app.current().Platform != "bilibili" {
		t.Fatalf("房间号直达落到 %q，期望 bilibili", app.current().Platform)
	}
}

// 搜索浮层底部的平台提示同样跟着主播走。
func TestSearchHintFollowsPlayerPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "huya"
	focusForTest(app, Streamer{Platform: "bilibili", RoomID: "170001", Name: "某主播"})
	app.Router.Push(routePlayer)
	app.SearchOpen = true
	// 这一帧只看顶栏文案，不要顺手起一轮真实取流。

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "B站 · ") {
			return
		}
	}
	t.Fatalf("搜索浮层底部提示没跟着主播走，文本树 = %v", tt.Texts())
}
