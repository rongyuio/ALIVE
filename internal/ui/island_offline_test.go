package ui

import "testing"

// 墙上点一格离线房间：焦点可以落在它身上（那是用户的选择），但没有引擎、
// 没有声音时岛不能画成「后台收听」。
//
// 和 media.roomKey 那条是同一个洞：focusKey 曾经无条件返回 WatchActive，
// 离线格被点成 active 后岛照样显示「回到直播间」，点回去还是未开播提示。
// 违反 doc/architecture/surfaces.md 不变量 5。
func TestIslandSkipsOfflineActiveTile(t *testing.T) {
	app := NewMockApp()
	app.watch.init()
	room := Streamer{Platform: "bilibili", RoomID: "9001", Name: "离线的主播"}
	key := watchKey(room)
	app.WatchRooms = []Streamer{room}
	app.WatchActive = key
	app.watch.sessions[key] = &roomSession{
		key: key, room: room, offline: true,
	}

	if got := app.focusKey(); got != "" {
		t.Fatalf("离线 active 格仍被当成声音焦点：%q", got)
	}
	if got := app.islandFormOf(); got != islandIdle {
		t.Fatalf("离线 active 格的岛形态是 %v，期望时钟", got)
	}

	// 这一格还在取流时（loading）允许出现在岛上：声音正在来。
	app.watch.sessions[key].loading = true
	if got := app.focusKey(); got != key {
		t.Fatalf("在途会话被误判成没有声音：%q", got)
	}
}
