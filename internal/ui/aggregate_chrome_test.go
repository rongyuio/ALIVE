package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 聚合页面（关注：关注主播 + 订阅分区）上有条件地收起顶栏入口。
//
// 账号是按平台的、顶栏搜索是搜一家平台的在播房间，而关注页聚合的是
// 好几家。留在上面就会画出「上一家」的账号，点开还是另一家的二维码。
// 关注主播那一级有自己的页内搜索——收起的只是顶栏那颗，不是搜索本身。
func TestAggregatePagesHidePlatformChrome(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	for _, section := range []int{followsSectionStreamers, followsSectionSubs} {
		app.switchTab("follows")
		app.FollowsSection = section
		tt.Advance(time.Second)
		tt.Frame()

		if app.searchAvailable() {
			t.Errorf("关注页（分区 %d）：顶栏搜索不该出现", section)
		}
		if app.authAvailable() {
			t.Errorf("关注页（分区 %d）：账号入口不该出现", section)
		}
		if _, ok := tt.Find("搜索"); ok {
			t.Errorf("关注页（分区 %d）：顶栏搜索按钮还在屏幕上", section)
		}
		if _, ok := tt.Find("账号"); ok {
			t.Errorf("关注页（分区 %d）：账号按钮还在屏幕上", section)
		}
		if _, ok := tt.Find("登录"); ok {
			t.Errorf("关注页（分区 %d）：登录按钮还在屏幕上", section)
		}
	}

	// 页内搜索在：关注主播这一级有。
	app.switchTab("follows")
	app.FollowsSection = followsSectionStreamers
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find("搜索已关注的主播"); !ok {
		t.Error("关注主播没有页内搜索")
	}
	app.switchTab("follows")
	app.FollowsSection = followsSectionSubs
	app.Subs = []Subscription{{PlatformID: "bilibili", Platform: "B站", Group: "网游", Name: "鸣潮", CategoryID: "1"}}
	tt.Advance(time.Second)
	tt.Frame()
	// 订阅分区也不该有页内搜索：它只显示一个订阅分区的列表，而搜索框会
	// 暗示「能搜这页全部内容」——跨平台、跨未加载的分页，都做不到。
	if _, ok := tt.Find("搜索已关注的主播"); ok {
		t.Error("订阅分区不该有搜索框")
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
