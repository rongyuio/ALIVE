package ui

import "testing"

// 导入清晰度 / 线路时，如果正在播的就是这一间，必须真的重开这一路。
//
// 只写房间记录的话，播放页芯片显示导入的档位，画面还是旧的那一路。
func TestImportQualityRestreamsCurrentRoom(t *testing.T) {
	app := NewMockApp()
	st := app.Streamers[0]
	st.RoomID = "1000"
	focusForTest(app, st)
	app.Router.Push(routePlayer)
	key := watchKey(st)
	before := app.media.seq

	app.applyConfig(configFile{Rooms: map[string]roomChoice{
		key: {Quality: "超清 720P", Line: "线路一"},
	}})

	if got := app.qualityFor(key); got != "超清 720P" {
		t.Fatalf("清晰度没有写进房间记录：%q", got)
	}
	if app.media.seq == before {
		t.Fatal("导入清晰度只写了记录，正在播的这一路没有重开：芯片和画面会对不上")
	}
}
