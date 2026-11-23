package ui

import "testing"

// 点一格已知没有声音的格子：只重试，不抢焦点。
//
// 抢焦点会先亮绿点、展开岛，等这一路再失败又一起收掉——用户看到一次没有
// 意义的闪动，还把原来的声音焦点弄丢了。这是 e2e 里真实撞到的：
// 点离线格之后绿点一直挂着，岛却回到时钟。
func TestWatchActivateDeadTileDoesNotStealFocus(t *testing.T) {
	app := NewMockApp()
	app.watch.init()

	live := Streamer{Platform: "bilibili", RoomID: "1", Name: "在播"}
	dead := Streamer{Platform: "bilibili", RoomID: "2", Name: "离线"}
	app.WatchRooms = []Streamer{live, dead}

	liveKey, deadKey := watchKey(live), watchKey(dead)
	app.WatchActive = liveKey
	app.watch.sessions[liveKey] = &roomSession{key: liveKey, room: live}
	app.watch.sessions[deadKey] = &roomSession{
		key: deadKey, room: dead, offline: true, err: "主播暂时没有开播",
	}

	app.watchActivate(dead)

	if app.WatchActive != liveKey {
		t.Fatalf("点离线格抢走了焦点：WatchActive=%q，期望仍是 %q", app.WatchActive, liveKey)
	}
	if s := app.watch.sessions[deadKey]; s.offline || s.err != "" {
		t.Fatalf("离线格没有走重试：offline=%v err=%q", s.offline, s.err)
	}
}

// active 这一路失败/下播：焦点必须一起收掉。
//
// 引擎已经关了，绿点却还挂着，岛按「没有引擎」回时钟——两个地方各说各话。
// 收掉之后绿点消失、岛回时钟，和 surfaces.md 不变量 5 一致。
func TestWatchFailReleasesFocus(t *testing.T) {
	for _, tc := range []struct {
		name string
		kill func(a *App, s *roomSession)
	}{
		{"取流失败", func(a *App, s *roomSession) { a.watchFail(s, "取流失败") }},
		{"下播", func(a *App, s *roomSession) { a.watchOffline(s) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewMockApp()
			app.watch.init()
			room := Streamer{Platform: "bilibili", RoomID: "1", Name: "在播"}
			key := watchKey(room)
			app.WatchRooms = []Streamer{room}
			app.WatchActive = key
			app.Player = room
			app.Playing = true
			s := &roomSession{key: key, room: room}
			app.watch.sessions[key] = s

			tc.kill(app, s)

			if app.WatchActive != "" || app.WatchPending != "" {
				t.Fatalf("失败后焦点没收：active=%q pending=%q", app.WatchActive, app.WatchPending)
			}
			if app.Playing {
				t.Fatal("失败后 a.Playing 还是 true")
			}
			if got := app.focusKey(); got != "" {
				t.Fatalf("失败后仍有声音焦点：%q", got)
			}
		})
	}
}
