package ui

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/watch"
)

func watchTestRooms(n int) []Streamer {
	out := make([]Streamer, n)
	for i := range out {
		out[i] = Streamer{
			Platform: "bilibili",
			RoomID:   fmt.Sprintf("%d", 1000+i),
			Name:     fmt.Sprintf("观测主播%d", i),
			Title:    fmt.Sprintf("观测画面%d", i),
			Live:     true,
			Viewers:  1000 + i,
		}
	}
	return out
}

// openWatchWall 把 App 放到关注页的「悲鸣观测」分区，并跑到墙完成布局。
func openWatchWall(t *testing.T, app *App) *ui.Tester {
	t.Helper()
	app.Router = ui.NewRouter(routeFollows)
	app.FollowsSection = followsSectionWatch
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	tt.Advance(time.Millisecond)
	tt.Frame()
	return tt
}

func newWatchMock(t *testing.T, rooms []Streamer) (*App, *ui.Tester) {
	t.Helper()
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.WatchRooms = rooms
	return app, openWatchWall(t, app)
}

func TestWatchEmptyState(t *testing.T) {
	app, tt := newWatchMock(t, nil)
	if !tt.HasText("观测盒是空的") {
		t.Fatalf("空盒没有空态，texts=%q", tt.Texts())
	}
	if _, ok := tt.Find("去关注列表"); !ok {
		t.Fatal("空态没有「去关注列表」")
	}
	if _, ok := tt.Find("去首页"); !ok {
		t.Fatal("空态没有「去首页」")
	}
	// 空态点「去关注列表」应切回关注主播分区，不离开关注页。
	if err := tt.Click("去关注列表"); err != nil {
		t.Fatal(err)
	}
	if app.FollowsSection != followsSectionStreamers || app.Router.Path() != routeFollows {
		t.Fatalf("去关注列表后 section=%d route=%q", app.FollowsSection, app.Router.Path())
	}
}

func TestWatchTwoRoomsFillTheWall(t *testing.T) {
	rooms := watchTestRooms(2)
	_, tt := newWatchMock(t, rooms)
	var rects []ui.Rect
	for _, room := range rooms {
		r, ok := tt.Find(watchTileLabel(room))
		if !ok {
			t.Fatalf("找不到格子 %q，labels=%v", watchTileLabel(room), tt.Texts())
		}
		rects = append(rects, r)
	}
	// 1440 宽下两间各占半屏，不是固定 280 的小卡片。
	if rects[0].W < 600 || rects[0].H < 300 {
		t.Fatalf("两间格子太小：%.0fx%.0f", rects[0].W, rects[0].H)
	}
	if rects[0].X == rects[1].X && rects[0].Y == rects[1].Y {
		t.Fatal("两间格子叠在一起")
	}
}

func TestWatchNineRoomsLayout(t *testing.T) {
	_, tt := newWatchMock(t, watchTestRooms(9))
	for i, room := range watchTestRooms(9) {
		if _, ok := tt.Find(watchTileLabel(room)); !ok {
			t.Fatalf("第 %d 间没有上墙", i)
		}
	}
	if !tt.HasText("观测主播0") {
		t.Fatalf("墙上没有主播名，texts=%q", tt.Texts())
	}
}

func TestWatchClickActivatesWithoutLeaving(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	if err := tt.Click(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routeFollows {
		t.Fatalf("单击不该离开观测盒，route=%q", app.Router.Path())
	}
	if app.WatchActive != watchKey(rooms[0]) {
		t.Fatalf("active=%q，期望 %q", app.WatchActive, watchKey(rooms[0]))
	}
	if app.Player.Name != rooms[0].Name || !app.Playing {
		t.Fatalf("播放项 %+v playing=%v", app.Player, app.Playing)
	}
	// 激活后出现常显的静音控件。
	if _, ok := tt.Find("静音观测"); !ok {
		t.Fatal("激活格没有静音控件")
	}
	if err := tt.Click("静音观测"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Volume != 0 {
		t.Fatalf("点了静音，volume=%v", app.Volume)
	}
	if _, ok := tt.Find("取消静音观测"); !ok {
		t.Fatal("静音后控件没有变成取消静音")
	}
}

func TestWatchDoubleClickOpensPlayer(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("双击没有进播放页，route=%q", app.Router.Path())
	}
}

func TestWatchAddCapAndDedupe(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(watch.MaxRooms + 2)
	for i := 0; i < watch.MaxRooms; i++ {
		if !app.watchAdd(rooms[i]) {
			t.Fatalf("第 %d 间没加进去", i)
		}
	}
	if len(app.WatchRooms) != watch.MaxRooms {
		t.Fatalf("盒里 %d 间，期望 %d", len(app.WatchRooms), watch.MaxRooms)
	}
	if app.watchAdd(rooms[0]) {
		t.Fatal("重复房间被加进去了")
	}
	if app.watchAdd(rooms[watch.MaxRooms]) {
		t.Fatal("超过 9 间还加进去了")
	}
	if got := len(app.WatchRooms); got != watch.MaxRooms {
		t.Fatalf("上限后 %d 间", got)
	}
}

func TestWatchPickerAddsFollowedRoom(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.Follows = []FollowRecord{{
		Platform: "bilibili", RoomID: "9001", Name: "被关注的主播", Live: true,
	}}
	app.WatchPickerOpen = true
	tt := openWatchWall(t, app)
	label := "添加观测:B站 被关注的主播"
	if _, ok := tt.Find(label); !ok {
		t.Fatalf("添加面板没有关注项，texts=%q", tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if len(app.WatchRooms) != 1 || app.WatchRooms[0].RoomID != "9001" {
		t.Fatalf("添加后 %+v", app.WatchRooms)
	}
	if app.WatchPickerOpen {
		t.Fatal("添加后面板没有收起")
	}
}

func TestWatchRoomsPersist(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	app.WatchRooms = watchTestRooms(3)
	app.stateDirty = true
	app.saveState()

	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	restored := NewMockApp()
	if len(restored.WatchRooms) != 3 {
		t.Fatalf("恢复 %d 间，期望 3：%+v", len(restored.WatchRooms), restored.WatchRooms)
	}
	for i, s := range restored.WatchRooms {
		if watchKey(s) != watchKey(app.WatchRooms[i]) {
			t.Fatalf("第 %d 间恢复成 %q，期望 %q", i, watchKey(s), watchKey(app.WatchRooms[i]))
		}
	}
}

func TestNormalizeWatchRooms(t *testing.T) {
	in := []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "A"},
		{Platform: "bilibili", RoomID: "1", Name: "A 重复"},
		{Platform: "", RoomID: "2", Name: "无平台"},
		{Platform: "douyu", RoomID: "", Name: "无房间"},
	}
	out := normalizeWatchRooms(in)
	if len(out) != 1 || out[0].RoomID != "1" {
		t.Fatalf("去重后 %+v", out)
	}
	if got := normalizeWatchRooms(watchTestRooms(watch.MaxRooms + 4)); len(got) != watch.MaxRooms {
		t.Fatalf("上限后 %d 间", len(got))
	}
}

func TestWatchRecordRoundTrip(t *testing.T) {
	in := Streamer{
		Platform: "douyu", RoomID: "123", Name: "主播", Title: "标题",
		Category: "网游", Sub: "LPL", Viewers: 42, Live: true, Hue: 120,
		CoverURL:  "https://example.invalid/cover.jpg",
		AvatarURL: "https://example.invalid/avatar.jpg",
	}
	out := watchRecordOf(in).streamer()
	if watchKey(out) != watchKey(in) || out.Title != in.Title || out.CoverURL != in.CoverURL {
		t.Fatalf("round trip %+v -> %+v", in, out)
	}
}

func TestCardAddsToWatchBox(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	room := watchTestRooms(1)[0]
	added := false
	tt := ui.NewTester(func(c *ui.Context) {
		Card(c, room, 280, false, nil, nil, func() { added = true })
	}, 400, 400)
	tt.Frame()
	if err := tt.RightClick("直播间:" + room.Name); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("收进悲鸣观测"); err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("卡片右键没有走收进观测盒")
	}
}

func TestIslandExpandsOnWatchWall(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(1))
	room := app.WatchRooms[0]
	app.WatchActive = watchKey(room)
	app.Player = room
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	hit, ok := tt.Find("回到直播间")
	if !ok {
		t.Fatalf("墙上 active 的岛没有回播放页入口，texts=%q", tt.Texts())
	}
	if hit.W < 100 {
		t.Fatalf("墙上的岛没有展开：宽 %.1f", hit.W)
	}
}

func TestWatchDragReorders(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	from, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到第一格")
	}
	to, ok := tt.Find(watchTileLabel(rooms[1]))
	if !ok {
		t.Fatal("找不到第二格")
	}
	sx, sy := from.X+from.W/2, from.Y+from.H/2
	tx, ty := to.X+to.W/2, to.Y+to.H/2
	tt.Move(sx, sy)
	tt.Press(sx, sy)
	tt.Move(sx+8, sy)
	tt.Move(tx, ty)
	tt.Release(tx, ty)
	tt.Frame()
	if got := app.WatchRooms[0].RoomID; got != "1001" {
		t.Fatalf("拖动后第一间是 %s，期望 1001（顺序 %v）", got, app.WatchRooms)
	}
	if got := app.WatchRooms[1].RoomID; got != "1000" {
		t.Fatalf("拖动后第二间是 %s，期望 1000", got)
	}
	if app.WatchActive != "" {
		t.Fatalf("拖动不该激活格子，active=%q", app.WatchActive)
	}
}

func TestWatchMoveBy(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(3)
	app.WatchRooms = append([]Streamer(nil), rooms...)

	app.watchMoveBy(rooms[2], -1)
	if got := app.WatchRooms[1].RoomID; got != "1002" {
		t.Fatalf("后移前移后第二间是 %s，顺序 %v", got, app.WatchRooms)
	}
	app.watchMoveBy(rooms[0], -1) // 已经在最前，不动
	if got := app.WatchRooms[0].RoomID; got != "1000" {
		t.Fatalf("越界前移改了顺序：%v", app.WatchRooms)
	}
	app.watchMoveBy(rooms[0], 1)
	if got := app.WatchRooms[1].RoomID; got != "1000" {
		t.Fatalf("前移后第二间是 %s，顺序 %v", got, app.WatchRooms)
	}
}
