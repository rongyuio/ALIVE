package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 命中缓存也要作废更早的在途请求。
//
// loadRooms 命中新鲜缓存时直接 return。如果这一次不推进 liveSeq，先前那个
// 没缓存的分区 A 发出的响应回来时序号仍然对得上，回写守卫就拦不住它：
// a.Rooms 会被换成 A 的列表（A 失败时还会在新分区上写 A 的 ErrText），
// 之后 loadMoreRooms 按当前 key 把第二页拼上去，feedCache[B] 就永久脏了。
//
// 这里不启动 goroutine（Win 为 nil 时响应本来就不会回写），只断言守卫的
// 输入：切到 B 并命中缓存后，A 手里的那个序号必须已经过期。
func TestLoadRoomsCacheHitStillInvalidatesInflight(t *testing.T) {
	a := NewMockApp()
	a.LiveMode = true
	a.Platform = "douyu"
	a.Win = nil
	a.Groups = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories: []string{"英雄联盟", "DOTA2"}, CategoryIDs: []string{"86", "87"},
	}}

	// 分区 A 没有缓存，真的发一次请求：响应回来时带着这里拿到的序号。
	// Win 为 nil，响应不会被回写，但序号已经被这次加载占住了。
	a.CategoryIndex = 0
	keyA := a.feedKey()
	a.loadRooms()
	seqA := a.liveSeq
	if seqA == 0 {
		t.Fatal("loadRooms 没有推进 liveSeq，测试前提不成立")
	}

	// 用户切到 B，B 有新鲜缓存，loadRooms 会直接渲染缓存并提前返回。
	a.CategoryIndex = 1
	keyB := a.feedKey()
	if keyB == keyA {
		t.Fatalf("两个分区的 feedKey 相同：%q", keyB)
	}
	a.feedCache[keyB] = feedEntry{
		rooms:  []Streamer{{Platform: "douyu", RoomID: "b", Name: "B 分区主播"}},
		cursor: listPageSize,
		at:     time.Now(),
	}

	a.loadRooms()

	if len(a.Rooms) != 1 || a.Rooms[0].RoomID != "b" {
		t.Fatalf("缓存没有先渲染：%+v", a.Rooms)
	}
	if a.liveSeq == seqA {
		t.Fatalf("命中缓存没有推进 liveSeq（仍是 %d）：分区 A 的响应会被当成当前列表写进 Rooms 和 feedCache[B]", a.liveSeq)
	}
	if a.ErrText != "" {
		t.Fatalf("命中缓存却带着错误文案：%q", a.ErrText)
	}
}

// 分区树没加载出来时，「重试」必须重拉分区树，不能只重拉列表。
//
// 分区树失败后 Groups 为空，currentCategoryID() 返回 ""，loadRooms 在判空
// 处直接 return，一个请求都不发；而 liveLoaded 早已是 true，ensureLiveData
// 每帧都跳过。于是「重试」什么也没做，首页永远停在空态。
func TestRetryReloadsCategoryTreeWhenMissing(t *testing.T) {
	newAppWithBrokenTree := func(t *testing.T) *App {
		t.Helper()
		a := NewMockApp()
		a.LiveMode = true
		a.Platform = "douyu"
		for _, id := range a.PlatformOrder {
			a.liveLoaded[id] = true
		}
		// 分区树加载失败后的状态：没有分组、没有列表。
		a.Groups = nil
		a.Rooms = nil
		a.ListLoading = false
		return a
	}
	assertReloaded := func(t *testing.T, a *App, before int) {
		t.Helper()
		if a.liveSeq <= before {
			t.Fatalf("点重试没有重新请求分区树：liveSeq %d → %d", before, a.liveSeq)
		}
		if !a.ListLoading {
			t.Fatal("重新请求分区树后没有进入加载态")
		}
		if a.ErrText != "" {
			t.Fatalf("点重试后错误文案还在：%q", a.ErrText)
		}
	}

	t.Run("错误条", func(t *testing.T) {
		a := newAppWithBrokenTree(t)
		// 列表有内容时才只出现错误条，不会和空态里的同名按钮撞车。
		a.Rooms = []Streamer{{Platform: "douyu", RoomID: "1", Name: "主播"}}
		a.ErrText = "分区加载失败：超时"
		tt := ui.NewTester(a.Shell, 1440, 900)
		before := a.liveSeq

		if err := tt.Click("重试"); err != nil {
			t.Fatal(err)
		}
		assertReloaded(t, a, before)
	})

	t.Run("空态", func(t *testing.T) {
		a := newAppWithBrokenTree(t)
		tt := ui.NewTester(a.Shell, 1440, 900)
		before := a.liveSeq

		if err := tt.Click("重试"); err != nil {
			t.Fatal(err)
		}
		assertReloaded(t, a, before)
	})
}

// 播放页的开关类快捷键按住不放也只算按了一次。
//
// ShortcutRepeated 描述的是「最近一次 Shortcut 命中的那个键是不是自动
// 重复」。在 Shortcut 之前读它，读到的是本帧更早的快捷（或者上一帧）留下
// 的状态，多数时候是 false，闸门形同虚设：按住空格每秒翻几十次。
func TestPlayerKeysIgnoreAutoRepeat(t *testing.T) {
	app := NewMockApp()
	focusForTest(app, app.Streamers[0])
	app.Router.Push(routePlayer)
	app.setFocusPlaying(false)
	tt := ui.NewTester(app.Shell, 1440, 900)

	tt.KeyDown(ui.KeySpace)
	if !app.focusPlaying() {
		t.Fatal("首次按下空格没有切换播放状态")
	}
	// 按住不放：系统发来的自动重复不该再切一次。
	tt.KeyRepeat(0, ui.KeySpace)
	if !app.focusPlaying() {
		t.Fatal("第一次自动重复又切换了播放状态")
	}
	tt.KeyRepeat(0, ui.KeySpace)
	if !app.focusPlaying() {
		t.Fatal("第二次自动重复又切换了播放状态")
	}
	tt.KeyUp(ui.KeySpace)
}
