package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 两个聚合页面（全部关注 / 订阅）上有条件地收起顶栏入口。
//
// 账号是按平台的、顶栏搜索是搜一家平台的在播房间，而这两个页面聚合的是
// 好几家。留在上面就会画出「上一家」的账号，点开还是另一家的二维码。
// 与此同时它们各自有页内搜索——收起的只是顶栏那颗，不是搜索本身。
func TestAggregatePagesHidePlatformChrome(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	for _, page := range []string{"follows", "subscribe"} {
		app.switchTab(page)
		tt.Advance(time.Second)
		tt.Frame()

		if app.searchAvailable() {
			t.Errorf("%s 页：顶栏搜索不该出现", page)
		}
		if app.authAvailable() {
			t.Errorf("%s 页：账号入口不该出现", page)
		}
		if _, ok := tt.Find("搜索"); ok {
			t.Errorf("%s 页：顶栏搜索按钮还在屏幕上", page)
		}
		if _, ok := tt.Find("账号"); ok {
			t.Errorf("%s 页：账号按钮还在屏幕上", page)
		}
		if _, ok := tt.Find("登录"); ok {
			t.Errorf("%s 页：登录按钮还在屏幕上", page)
		}
	}

	// 页内搜索在：两个页面各有一个。
	app.switchTab("follows")
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Error("全部关注页没有页内搜索")
	}
	app.switchTab("subscribe")
	app.Subs = []Subscription{{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "鸣潮", CategoryID: "1"}}
	tt.Advance(time.Second)
	tt.Frame()
	// 订阅页也不该有页内搜索：它只显示一个订阅分区的列表，而搜索框会
	// 暗示「能搜这页全部内容」——跨平台、跨未加载的分页，都做不到。
	if _, ok := tt.Find("搜索订阅分区的主播"); ok {
		t.Error("订阅页不该有搜索框")
	}
}

// 回到平台页，两样都回来。
func TestPlatformPageShowsChromeAgain(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.switchTab("follows")
	tt.Advance(time.Second)
	tt.Frame()
	app.switchTab(app.Platform)
	tt.Advance(time.Second)
	tt.Frame()

	if !app.searchAvailable() || !app.authAvailable() {
		t.Errorf("回到平台页后入口没回来：search=%v auth=%v",
			app.searchAvailable(), app.authAvailable())
	}
}
