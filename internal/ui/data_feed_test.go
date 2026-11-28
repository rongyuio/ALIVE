package ui

import (
	"fmt"
	"testing"
	"time"

	"alive/internal/live"
)

// roomsN 造 n 个房间，房间号从 base 起。
func roomsN(base, n int) []live.Room {
	out := make([]live.Room, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, live.Room{
			Platform: live.Bilibili, RoomID: fmt.Sprint(base + i),
			Nickname: fmt.Sprintf("r%d", base+i), Title: "t",
		})
	}
	return out
}

// 首页的首屏刷新不能把「加载更多」已经取回来的那一段打回去。
//
// 场景很日常：滚到底部时缓存刚好过期（feedTTL 60s）——补页和首屏刷新同时
// 在飞。补页先回（列表 40 → 120），首屏后回（只有 40 条、cursor 40）。
// 旧代码无条件换上去，列表被打回 40 条，而 feedCache 里记的是 120 条的
// cursor：界面和缓存两份真相，中间 80 条要等下次全量重取才回得来。
//
// 守卫在 applyFirstChunk **里面**，不在调用点——所以这条测试直接调它，
// 验的就是那条不变量本身，不依赖某个调用者记得先判一次。
func TestHomeFirstChunkRespectsTopUp(t *testing.T) {
	a := newApp()
	a.LiveMode = true
	a.Platform = "bilibili"

	// 刷新发起时列表停在 40。
	a.Rooms = convertRooms(roomsN(0, 40))
	a.homeCursor = 40
	startCursor := a.homeCursor

	// 补页先回：把列表推到 120。
	if !a.mergeMoreChunk(a.feedKey(), 40, live.Chunk{
		Rooms: roomsN(40, 80), Cursor: 120, HasMore: true,
	}) {
		t.Fatal("前提不成立：补页被判成旧代次")
	}
	if len(a.Rooms) != 120 || a.homeCursor != 120 {
		t.Fatalf("前提不成立：补页后应有 120 条，实际 %d 条、cursor %d", len(a.Rooms), a.homeCursor)
	}

	// 首屏后回：必须被判过期，列表不能被缩短。
	applied := a.applyFirstChunk(a.feedKey(), startCursor,
		live.Chunk{Rooms: roomsN(0, 40), Cursor: 40, HasMore: true})
	if applied {
		t.Fatal("首屏回包被判成仍然有效")
	}
	if len(a.Rooms) != 120 {
		t.Fatalf("列表从 120 条被打回 %d 条：补页结果被整段丢弃", len(a.Rooms))
	}
	if a.homeCursor != 120 {
		t.Fatalf("homeCursor 从 120 退回 %d：和 feedCache 的 cursor 对不上", a.homeCursor)
	}
}

// 没有补页插入时，首屏刷新必须正常生效（别把守卫写成永远拒绝）。
func TestHomeFirstChunkAppliesWhenUntouched(t *testing.T) {
	a := newApp()
	a.LiveMode = true
	a.Platform = "bilibili"
	a.Rooms = convertRooms(roomsN(0, 40))
	a.homeCursor = 40
	startCursor := a.homeCursor

	if !a.applyFirstChunk(a.feedKey(), startCursor,
		live.Chunk{Rooms: roomsN(0, 40), Cursor: 40, HasMore: true}) {
		t.Fatal("列表没动过，首屏却被判过期")
	}
	if len(a.Rooms) != 40 {
		t.Fatalf("首屏没应用：%d 条", len(a.Rooms))
	}
}

// 订阅页同一条不变量。
func TestSubFirstChunkRespectsTopUp(t *testing.T) {
	a := newApp()
	a.LiveMode = true
	a.Platform = "bilibili"

	a.SubRooms = convertRooms(roomsN(0, 40))
	a.subCursor = 40
	startCursor := a.subCursor

	// 补页先回：接到 120。
	a.applySubChunk("k", 40, startCursor,
		live.Chunk{Rooms: roomsN(40, 80), Cursor: 120, HasMore: true})
	if len(a.SubRooms) != 120 || a.subCursor != 120 {
		t.Fatalf("前提不成立：订阅补页后应有 120 条，实际 %d 条、cursor %d",
			len(a.SubRooms), a.subCursor)
	}

	// 首屏后回：必须被判过期。
	applied := a.applySubChunk("k", 0, startCursor,
		live.Chunk{Rooms: roomsN(0, 40), Cursor: 40, HasMore: true})
	if applied {
		t.Fatal("订阅首屏回包被判成仍然有效")
	}
	if len(a.SubRooms) != 120 {
		t.Fatalf("订阅列表从 120 条被打回 %d 条", len(a.SubRooms))
	}
}

// 过期缓存要先渲染出来：切回看过的分区是瞬时的，不闪骨架。
func TestStaleCacheRendersBeforeRefresh(t *testing.T) {
	a := newApp()
	a.LiveMode = true
	a.Platform = "bilibili"
	// loadRooms 要有 areaID 才会往下走：铺一个最小分区。
	a.Groups = []CategoryGroup{{Name: "网游", Categories: []string{"英雄联盟"}, CategoryIDs: []string{"1"}}}
	key := a.feedKey()
	cached := convertRooms(roomsN(0, 40))
	a.feedCache[key] = feedEntry{
		rooms: cached, cursor: 40, hasMore: true,
		at: time.Now().Add(-2 * feedTTL),
	}
	// Win 是 nil：loadRooms 的 goroutine 会立刻返回，只留下"渲染缓存"这一半。
	a.loadRooms()
	if len(a.Rooms) != 40 {
		t.Fatalf("过期缓存没有先渲染：列表 %d 条", len(a.Rooms))
	}
	if a.ListLoading {
		t.Error("有缓存时不该显示骨架")
	}
}
