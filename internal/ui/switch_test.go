package ui

import (
	"testing"
	"time"
)

// 切到已有缓存的平台不该重播入场动画。
//
// applyLiveGroups 曾经无条件 resetCards()：命中 feedCache 时房间数据已经在屏幕上
// 等着了，整屏卡片却会从 opacity 0 再淡入一次（260ms），看起来就是「切平台闪一下」。
// 现在只有真的从网络取回列表时才重置——那条路在 loadRooms 的响应回调里。
func TestPlatformSwitchWarmCacheKeepsCards(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "douyu"
	a.CardsIn = true
	before := a.EnterSeq

	groups := []CategoryGroup{{
		ID:          "1",
		Name:        "网游",
		Categories:  []string{"英雄联盟"},
		CategoryIDs: []string{"86"},
	}}
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms: []Streamer{{Platform: "douyu", RoomID: "42", Name: "主播"}},
		page:  1,
		at:    time.Now(),
	}

	a.applyLiveGroups("douyu", nil, groups)

	if a.EnterSeq != before {
		t.Fatalf("命中缓存却重播了入场动画：EnterSeq %d → %d", before, a.EnterSeq)
	}
	if !a.CardsIn {
		t.Fatal("命中缓存把卡片置成了未入场，整屏会再淡入一次")
	}
	if len(a.Rooms) != 1 {
		t.Fatalf("缓存没被用上：%d 个房间", len(a.Rooms))
	}
	if a.ListLoading {
		t.Fatal("命中缓存却还挂着骨架")
	}
}

// 缓存过期后后台刷新完成时，不该让已经在屏幕上的列表再淡入一次。
//
// 这条只在缓存超过 feedTTL 时触发，所以现象是「切分区偶尔闪一下」：
// 列表明明已经显示着，网络回来后又整屏从 opacity 0 播一遍 260ms 的入场。
func TestStaleCacheRefreshKeepsCards(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "douyu"
	a.Groups = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []string{"英雄联盟"}, CategoryIDs: []string{"86"},
	}}
	a.CardsIn = true
	before := a.EnterSeq
	// 一条过期的缓存：列表已经在屏幕上，但 TTL 过了要静默刷新。
	a.feedCache["douyu|86|-1"] = feedEntry{
		rooms: []Streamer{{Platform: "douyu", RoomID: "1", Name: "旧的"}},
		page:  1,
		at:    time.Now().Add(-2 * feedTTL),
	}

	a.loadRooms()
	if len(a.Rooms) != 1 || a.ListLoading {
		t.Fatalf("过期缓存没先渲染：%d 个房间，loading=%v", len(a.Rooms), a.ListLoading)
	}
	// 只验「渲染阶段」的判定：刷新回调里的 resetCards 被 hit 挡住。
	if a.EnterSeq != before || !a.CardsIn {
		t.Fatalf("渲染过期缓存时重播了入场：EnterSeq %d → %d, CardsIn=%v", before, a.EnterSeq, a.CardsIn)
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
		page: 1,
		at:   time.Now(),
	}

	a.switchTab("douyu")

	if a.pendingSwitch != "" {
		t.Fatalf("封面没到就挂起了切换：pending=%q", a.pendingSwitch)
	}
	if a.Platform != "douyu" {
		t.Fatalf("平台还是 %s，期望 douyu", a.Platform)
	}
}
