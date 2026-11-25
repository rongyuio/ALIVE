package ui

import "testing"

// 从墙上点进某一间：其余格子继续播。
//
// 播放页背后还是这面墙，退出（Esc / 点岛）就回来了。那一段把墙上的预览
// 停掉再重建，代价全落在用户按 Esc 的那一刻：回墙看到一圈封面在重新连。
func TestWallPreviewsKeepWhileInPlayerFromWall(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()
	app.setFocus(app.watch.sessions[watchKey(rooms[0])])
	tt.Frame()

	openTile(t, tt, rooms[0])
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进播放页：%q", app.Router.Path())
	}
	if app.watch.parked {
		t.Fatal("从墙上点进播放页把墙上的预览停了")
	}
	if n := len(app.watch.sessions); n != len(rooms) {
		t.Fatalf("墙上的会话被拆了：%d/%d", n, len(rooms))
	}
}

// 真正离开墙（去首页 / 别的分区）：停掉墙上的预览，焦点留着出声。
//
// 「回墙重连」是停掉的自然结果：签名地址留在 wall.resume，回去时
// watchShow 缺谁补谁。
func TestWallPreviewsStopWhenLeavingWall(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	for _, r := range rooms {
		fakeSession(t, app, r)
	}
	tt.Frame()
	app.setFocus(app.watch.sessions[watchKey(rooms[0])])
	tt.Frame()

	app.pushPage(routeHome)
	tt.Frame()
	if !app.watch.parked {
		t.Fatal("离开墙没有停掉墙上的预览")
	}
	if n := len(app.watch.sessions); n != 1 {
		t.Fatalf("离开墙之后还剩 %d 路会话，期望只留焦点那一路", n)
	}
	if app.watch.sessions[watchKey(rooms[0])] == nil {
		t.Fatal("焦点那一间被停掉了：离开墙之后它还要出声")
	}

	// 回墙：停掉的标记清掉，缺的格子交给 watchShow 重建（视图测试没有
	// 窗口，不真的取流；这里只验状态回到「墙在用」）。
	app.pushPage(routeFollows)
	tt.Frame()
	if app.watch.parked {
		t.Fatal("回到墙上还标着「预览已停」")
	}
	for _, r := range rooms {
		if _, ok := tt.Find(watchTileLabel(r)); !ok {
			t.Fatalf("回到墙上缺了一格：%s", r.Name)
		}
	}
}

// 离开墙之后再换焦点：墙上那间也不留静音预览。
//
// 墙上的预览已经停了，留着它只会继续解码；「在墙上就留」这条规则只在
// 墙还在用的时候成立。
func TestSwitchingFocusOffWallClosesTheOldRoom(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	old := fakeSession(t, app, rooms[0])
	fakeSession(t, app, rooms[1])
	app.setFocus(old)
	tt.Frame()

	app.pushPage(routeHome)
	tt.Frame()
	if !app.watch.parked {
		t.Fatal("离开墙没有停掉墙上的预览")
	}

	other := Streamer{Platform: "bilibili", RoomID: "8888", Name: "别的"}
	focusForTest(app, other)
	if app.watch.sessions[watchKey(rooms[0])] != nil {
		t.Fatal("离开墙之后换焦点，旧的那间还留着预览")
	}
}
