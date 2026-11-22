package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 某平台的最后一条关注被删掉后，平台 chip 会消失，筛选值必须跟着收拢。
//
// 不收拢时滑块按「找不到匹配项」落回第一格，也就是「全部」，而列表仍然
// 按那个已经不存在的平台过滤：一块 accent 滑块压在未选中色的「全部」上，
// 下面却是空列表。
func TestFollowsPlatformFilterClearsWhenNoFollowsRemain(t *testing.T) {
	app := NewMockApp()
	app.Follows = []FollowRecord{
		{Platform: "douyu", RoomID: "1", Name: "斗鱼主播"},
		{Platform: "huya", RoomID: "2", Name: "虎牙主播"},
	}
	app.PlatformOrder = []string{"douyu", "huya", "douyin", "bilibili"}
	app.FollowsPlatform = "douyu"
	app.switchTab("follows")
	app.FollowsSection = followsSectionStreamers
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if !app.removeFollow(Streamer{Platform: "douyu", RoomID: "1"}) {
		t.Fatal("取消关注没有命中斗鱼那条记录")
	}
	tt.Frame()

	if app.FollowsPlatform != "" {
		t.Fatalf("该平台已经没有关注了，筛选仍停在 %q：滑块在「全部」上，列表却按它过滤",
			app.FollowsPlatform)
	}
}
