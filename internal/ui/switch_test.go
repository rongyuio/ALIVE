package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 切到已有缓存的平台：数据立刻在屏幕上，不摆骨架、不动列表。
//
// applyLiveGroups 曾经无条件 resetCards()：命中 feedCache 时房间数据已经在
// 屏幕上等着了，整屏卡片却会从 opacity 0 再淡入一次，看起来就是「切平台闪
// 一下」。入场动画现在已经整个去掉，这条测试守的是更底下的那件事：命中
// 缓存就是即时可用。
func TestPlatformSwitchWarmCacheRendersAtOnce(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "douyu"

	groups := []CategoryGroup{{
		ID:          "1",
		Name:        "网游",
		Categories:  []string{"英雄联盟"},
		CategoryIDs: []string{"86"},
	}}
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms:  []Streamer{{Platform: "douyu", RoomID: "42", Name: "主播"}},
		cursor: listPageSize,
		at:     time.Now(),
	}

	a.applyLiveGroups("douyu", nil, groups)

	if len(a.Rooms) != 1 {
		t.Fatalf("缓存没被用上：%d 个房间", len(a.Rooms))
	}
	if a.ListLoading {
		t.Fatal("命中缓存却还挂着骨架")
	}
}

// 缓存过期时先把列表留在屏幕上，后台刷新不闪、不清空。
//
// 这条只在缓存超过 feedTTL 时触发：如果渲染阶段把列表清掉再等网络，
// 现象就是「切分区偶尔闪一下」。
func TestStaleCacheRefreshKeepsListOnScreen(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "douyu"
	a.Groups = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []string{"英雄联盟"}, CategoryIDs: []string{"86"},
	}}
	// 一条过期的缓存：列表已经在屏幕上，但 TTL 过了要静默刷新。
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms:  []Streamer{{Platform: "douyu", RoomID: "1", Name: "旧的"}},
		cursor: listPageSize,
		at:     time.Now().Add(-2 * feedTTL),
	}

	a.loadRooms()
	if len(a.Rooms) != 1 || a.ListLoading {
		t.Fatalf("过期缓存没先渲染：%d 个房间，loading=%v", len(a.Rooms), a.ListLoading)
	}
}

// 首屏封面还没解码完，不该拦住切平台。
//
// 卡片的封面位本来就有同尺寸的渐变占位（coverArt），图晚到只是从渐变
// 换成真图，版式不跳。platformSwitchReady 却要求首屏每一张封面都有纹理，
// 只要有一张在路上就白等满 700ms：点完 tab 滑块已经过去了，内容一动不动。
func TestPlatformSwitchDoesNotWaitForCovers(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "bilibili"
	a.GroupsByPlatform["douyu"] = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []string{"英雄联盟"}, CategoryIDs: []string{"86"},
	}}
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms: []Streamer{
			{Platform: "douyu", RoomID: "1", Name: "甲", CoverURL: "https://example.invalid/a.jpg"},
			{Platform: "douyu", RoomID: "2", Name: "乙", CoverURL: "https://example.invalid/b.jpg"},
		},
		cursor: listPageSize,
		at:     time.Now(),
	}

	a.switchTab("douyu")

	if a.pendingSwitch != "" {
		t.Fatalf("封面没到就挂起了切换：pending=%q", a.pendingSwitch)
	}
	if a.Platform != "douyu" {
		t.Fatalf("平台还是 %s，期望 douyu", a.Platform)
	}
}

// 数据到达、卡片换上屏幕时，只能在原位直接出现，不能位移。
//
// 卡片曾经带 translateY 10→0、前 6 张再各错峰 20ms：同一行最左和最右在
// 切换瞬间差出 6.6 DIP，整屏先下落 10 DIP 再上浮（Tester 逐帧量 bounds）。
// 现在没有入场：第一帧就在最终位置，版式全程不动。
func TestDataArrivalKeepsCardsInPlace(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	// 测试里不发真实请求：预取闸门全部关掉。
	for _, id := range a.PlatformOrder {
		a.liveLoaded[id] = true
	}
	a.Groups = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories:  []string{"英雄联盟", "DOTA2"},
		CategoryIDs: []string{"86", "87"},
	}}
	a.ListLoading = false
	rooms := make([]Streamer, 24)
	for i := range rooms {
		rooms[i] = Streamer{
			Platform: "douyu", RoomID: fmt.Sprint(i),
			Name: fmt.Sprintf("主播%02d", i), Title: "标题",
		}
	}

	tt := ui.NewTester(a.Shell, 1391, 845)
	tt.Frame()

	// 换分区：先回骨架，再让数据到达——和 loadRooms 的响应回调同序。
	a.selectCategory(1)
	a.Rooms = nil
	a.ListLoading = true
	tt.Frame()
	a.Rooms = rooms
	a.ListLoading = false
	tt.Frame()

	names := cardNames(rooms, 5)
	p := newProbe(t, tt, contentRect(t, tt, names...), 16*time.Millisecond, names...)
	p.Run(40)
	p.RequireNoShift(0.01)
	p.RequireAlwaysPresent()
}
