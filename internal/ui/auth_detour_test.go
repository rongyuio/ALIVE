package ui

import "testing"

// 播放页点账号去登录：先切到平台首页，登完/放弃后回到原来的直播间。
func TestRoomLoginDetourReturnsToRoom(t *testing.T) {
	app := NewMockApp()
	room := Streamer{Platform: "huya", RoomID: "333003", Name: "某主播"}
	app.Platform = "bilibili"
	app.Player = room
	app.Router.Push(routePlayer)

	app.startRoomLogin(room)

	if app.Router.Path() != routeHome {
		t.Fatalf("应当先回首页去登录，实际停在 %s", app.Router.Path())
	}
	if !app.AuthOpen {
		t.Fatal("登录面板没打开，首页不会铺登录页")
	}
	// 这一趟不改浏览上下文：tab 还是原来那个，只是这一屏按登录平台来看。
	if app.Platform != "bilibili" {
		t.Fatalf("不该顺手换掉 tab，实际 %q", app.Platform)
	}
	if got := app.viewPlatform(); got != "huya" {
		t.Fatalf("登录页应当按房间的平台铺，得到 %q", got)
	}
	if app.authReturn == nil || app.authReturn.RoomID != "333003" {
		t.Fatalf("没记住要回到哪个直播间：%+v", app.authReturn)
	}

	// 用户放弃登录：把面板关掉就是全部动作。
	app.AuthOpen = false
	app.returnFromLogin()

	if app.Router.Path() != routePlayer {
		t.Fatalf("放弃登录后应当回到播放页，实际 %s", app.Router.Path())
	}
	if app.authReturn != nil {
		t.Fatal("回来之后还留着「待返回」标记")
	}
	if app.Player.RoomID != "333003" {
		t.Fatalf("回到的不是原来那间：%+v", app.Player)
	}
	if got := app.viewPlatform(); got != "huya" {
		t.Fatalf("回到播放页后应当跟着主播走，得到 %q", got)
	}
}

// 去登录这一趟不进历史：回来之后按返回，仍然回到进直播间之前那一页。
func TestRoomLoginDetourKeepsHistory(t *testing.T) {
	app := NewMockApp()
	room := Streamer{Platform: "huya", RoomID: "333003", Name: "某主播"}

	app.Router.Push(routeFollows)
	app.Player = room
	app.Router.Push(routePlayer)

	app.startRoomLogin(room)
	app.returnFromLogin()

	app.Router.Back()
	if got := app.Router.Path(); got != routeFollows {
		t.Fatalf("返回后落在 %q，期望回到进直播间之前的 %q", got, routeFollows)
	}
}

// 用户在看登录页时自己开了别的房间：不再把人拉回原来那间。
func TestReturnFromLoginLeavesWhenUserNavigated(t *testing.T) {
	app := NewMockApp()
	app.startRoomLogin(Streamer{Platform: "douyin", RoomID: "1", Name: "A"})
	app.Player = Streamer{Platform: "bilibili", RoomID: "2", Name: "B"}
	app.Router.Push(routePlayer)

	app.returnFromLogin()

	if app.Router.Path() != routePlayer || app.Player.RoomID != "2" {
		t.Fatalf("把用户从自己开的房间拽走了：path=%s player=%+v", app.Router.Path(), app.Player)
	}
	if app.authReturn != nil || app.AuthOpen {
		t.Fatalf("标记没清干净：back=%+v open=%v", app.authReturn, app.AuthOpen)
	}
}
