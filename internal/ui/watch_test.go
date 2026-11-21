package ui

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
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
	rooms := watchTestRooms(9)
	_, tt := newWatchMock(t, rooms)
	for i, room := range rooms {
		if _, ok := tt.Find(watchTileLabel(room)); !ok {
			t.Fatalf("第 %d 间没有上墙", i)
		}
	}
	// 名字和平台只在 hover 的底栏里出现。
	r, _ := tt.Find(watchTileLabel(rooms[0]))
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if !tt.HasText(rooms[0].Name) {
		t.Fatalf("hover 后底栏没有主播名，texts=%q", tt.Texts())
	}
	if !tt.HasText("B站 · 测试") && !tt.HasText("B站") {
		t.Fatalf("hover 后底栏没有平台，texts=%q", tt.Texts())
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
	// 激活格的音量必须是真实音量，而不是非 active 格的静音 0%。
	if !tt.HasText("60%") {
		t.Fatalf("激活格没有显示真实音量：texts=%q", tt.Texts())
	}
	if _, ok := tt.Find("静音"); !ok {
		t.Fatal("激活格没有静音控件")
	}
	if err := tt.Click("静音"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Volume != 0 {
		t.Fatalf("点了静音，volume=%v", app.Volume)
	}
	if _, ok := tt.Find("取消静音"); !ok {
		t.Fatal("静音后控件没有变成取消静音")
	}
	if !tt.HasText("0%") {
		t.Fatalf("静音后没有显示 0%%：texts=%q", tt.Texts())
	}
	if err := tt.Click("取消静音"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("60%") {
		t.Fatalf("取消静音后没有恢复 60%%：texts=%q", tt.Texts())
	}
	for _, label := range []string{
		"暂停", "重新加载", "音量",
		"放大观测 " + rooms[0].Name, "移出观测 " + rooms[0].Name,
	} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("底栏缺少 %q", label)
		}
	}
	// 播放/暂停 = 切 active 这一路的播放态，不换台。
	active := app.WatchActive
	if err := tt.Click("暂停"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Playing || app.WatchActive != active {
		t.Fatalf("暂停后 playing=%v active %q -> %q", app.Playing, active, app.WatchActive)
	}
	if _, ok := tt.Find("播放"); !ok {
		t.Fatal("暂停后按钮没有变回播放")
	}
}

func TestWatchHoverInactiveShowsControls(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	r, ok := tt.Find(watchTileLabel(rooms[1]))
	if !ok {
		t.Fatal("找不到第二格")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	for _, label := range []string{
		"播放", "重新加载", "取消静音", "音量",
		"放大观测 " + rooms[1].Name, "移出观测 " + rooms[1].Name,
	} {
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("hover 非 active 格缺少 %q", label)
		}
	}
	// 非 active 格显示的是它自己记住的音量，不是固定 0%。用精确匹配：
	// "60%" 里含有 "0%"，HasText 的子串匹配会把这条断言变成恒真。
	if !slices.Contains(tt.Texts(), "60%") {
		t.Fatalf("非 active 格没有显示自己记住的音量：texts=%q", tt.Texts())
	}
	if app.WatchActive != "" {
		t.Fatalf("hover 不该激活，active=%q", app.WatchActive)
	}
	// 播放/暂停只切这一格自己的播放状态，不负责激活/换台。
	before := app.WatchActive
	if err := tt.Click("播放"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.WatchActive != before {
		t.Fatalf("点播放改了 active：%q -> %q", before, app.WatchActive)
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
		t.Fatal("超过上限还加进去了")
	}
	if got := len(app.WatchRooms); got != watch.MaxRooms {
		t.Fatalf("上限后 %d 间", got)
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
	app := &App{}
	app.watch.init()
	app.watch.volumes[watchKey(in)] = 0.35
	rec := app.watchRecordOf(in)
	out := rec.streamer()
	if watchKey(out) != watchKey(in) || out.Title != in.Title || out.CoverURL != in.CoverURL {
		t.Fatalf("round trip %+v -> %+v", in, out)
	}
	if rec.Volume == nil || *rec.Volume != 0.35 {
		t.Fatalf("音量没有随记录往返：%v", rec.Volume)
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
	if err := tt.ChooseMenuItem("悲鸣观测"); err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Fatal("卡片右键没有走收进观测盒")
	}
}

func TestIslandIdleOnWatchWallKeepsClock(t *testing.T) {
	_, tt := newWatchMock(t, watchTestRooms(3))
	tt.Advance(time.Second)
	tt.Frame()
	r, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到灵动岛")
	}
	if r.W < 90 {
		t.Fatalf("没有 active 时岛宽 %.1f，时钟被缩成了球", r.W)
	}
}

// TestIslandShowsActiveRoomOnWatchWall 锁住墙上 active 时的岛：跟单直播间
// 一样展开，显示 active 那一间，点整块回播放页。环是流光这件事由
// TestIslandRings 的表锁住。
func TestIslandShowsActiveRoomOnWatchWall(t *testing.T) {
	app, tt := newWatchMock(t, watchTestRooms(2))
	room := app.WatchRooms[1]
	app.WatchActive = watchKey(room)
	app.Player = room
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	r, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到灵动岛")
	}
	if r.W < 160 {
		t.Fatalf("墙上 active 时岛宽 %.1f，没有展开", r.W)
	}
	if !tt.HasText(room.Title) {
		t.Fatalf("岛显示的不是 active 那间 %q：texts=%q", room.Title, tt.Texts())
	}
	if _, ok := tt.Find("回到直播间"); !ok {
		t.Fatal("展开的岛没有回播放页入口")
	}
}

func TestWatchLoadingUsesPlayerOverlay(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	app.watch.init()
	app.watch.sessions[key] = &wallSession{key: key, room: rooms[0], loading: true}
	tt.Frame()
	if !tt.HasText("正在拉取直播流…") {
		t.Fatalf("加载态没有复用播放页文案：texts=%q", tt.Texts())
	}
}

func TestWatchDragSwapsTiles(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	from, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到第一格")
	}
	to, ok := tt.Find(watchTileLabel(rooms[2]))
	if !ok {
		t.Fatal("找不到第三格")
	}
	sx, sy := from.X+from.W/2, from.Y+from.H/2
	tx, ty := to.X+to.W/2, to.Y+to.H/2
	tt.Move(sx, sy)
	tt.Press(sx, sy)
	tt.Move(sx+8, sy)
	tt.Move(tx, ty)
	tt.Release(tx, ty)
	tt.Frame()
	// 三格才分得出交换和插入：[A B C] 拖 A 到 C，交换是 [C B A]，插入是 [B C A]。
	want := []string{"1002", "1001", "1000"}
	for i, id := range want {
		if got := app.WatchRooms[i].RoomID; got != id {
			t.Fatalf("拖动后顺序是 [%s %s %s]，期望 %v",
				app.WatchRooms[0].RoomID, app.WatchRooms[1].RoomID, app.WatchRooms[2].RoomID, want)
		}
	}
	if app.WatchActive != "" {
		t.Fatalf("拖动不该激活格子，active=%q", app.WatchActive)
	}
}

func TestWatchBarControlsInsideTile(t *testing.T) {
	rooms := watchTestRooms(9)
	_, tt := newWatchMock(t, rooms)
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到第一格")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	label := "放大观测 " + rooms[0].Name
	m, ok := tt.Find(label)
	if !ok {
		t.Fatalf("hover 后找不到 %q", label)
	}
	t.Logf("tile=%+v control=%+v", r, m)
	if m.W <= 0 || m.X < r.X-0.5 || m.X+m.W > r.X+r.W+0.5 || m.Y+m.H > r.Y+r.H+0.5 {
		t.Fatalf("控件不在格子内：tile=%+v control=%+v", r, m)
	}
}

// 墙上每格有自己的音量：调一格不覆盖另一格，切走再切回来还是自己的值。
func TestWatchVolumeIsPerTile(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)

	activate := func(i int) {
		t.Helper()
		// 两次点同一格之间隔开，避免被判成双击跳进播放页。
		tt.Advance(time.Second)
		if err := tt.Click(watchTileLabel(rooms[i])); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}

	activate(0)
	if !almostEqual(float64(app.Volume), 0.6) {
		t.Fatalf("初始音量 %v", app.Volume)
	}
	r, ok := tt.Find("音量")
	if !ok {
		t.Fatal("激活格没有音量条")
	}
	tt.ClickAt(r.X+r.W*0.3, r.Y+r.H/2)
	tt.Frame()
	if !almostEqual(float64(app.Volume), 0.3) {
		t.Fatalf("A 调到 30%% 后 volume=%v", app.Volume)
	}

	// 切到 B：B 用自己记住的音量，不该继承 A 刚调的 30%。
	activate(1)
	if !almostEqual(float64(app.Volume), 0.6) || !slices.Contains(tt.Texts(), "60%") {
		t.Fatalf("切到 B 后 volume=%v texts=%q，期望 B 自己的 60%%", app.Volume, tt.Texts())
	}
	r, ok = tt.Find("音量")
	if !ok {
		t.Fatal("B 激活后没有音量条")
	}
	tt.ClickAt(r.X+r.W*0.2, r.Y+r.H/2)
	tt.Frame()
	if !almostEqual(float64(app.Volume), 0.2) {
		t.Fatalf("B 调到 20%% 后 volume=%v", app.Volume)
	}

	// 切回 A：A 仍是自己调过的 30%，没被 B 的 20% 覆盖。
	activate(0)
	if !almostEqual(float64(app.Volume), 0.3) || !slices.Contains(tt.Texts(), "30%") {
		t.Fatalf("切回 A 后 volume=%v texts=%q，期望 A 自己的 30%%", app.Volume, tt.Texts())
	}
}

// 每格音量也落盘：调过的格重启后还是自己的值，没调过的继续继承全局音量。
func TestWatchVolumesPersist(t *testing.T) {
	t.Setenv("AppData", t.TempDir())
	app := NewMockApp()
	rooms := watchTestRooms(3)
	app.WatchRooms = rooms
	app.watch.init()
	app.watch.volumes[watchKey(rooms[0])] = 0.3
	app.watch.volumes[watchKey(rooms[1])] = 0 // 0 是静音，必须和「没存过」分开
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
	if got := restored.watchVolume(watchKey(rooms[0])); !almostEqual(float64(got), 0.3) {
		t.Fatalf("第一格音量恢复成 %v，期望 0.3", got)
	}
	if got := restored.watchVolume(watchKey(rooms[1])); got != 0 {
		t.Fatalf("第二格静音恢复成 %v，期望 0", got)
	}
	// 没调过的格不写 volume，重新上墙时继承全局音量，而不是被钉成某个旧值。
	if got := restored.watchVolume(watchKey(rooms[2])); !almostEqual(float64(got), 0.6) {
		t.Fatalf("第三格恢复成 %v，期望继承全局 0.6", got)
	}
}

// TestWatchActiveTileShowsGreenDot 锁住 active 格唯一的可见标识：左上角绿点。
//
// 判据用像素而不是元素树：绿点不可交互、没有 Label，不进无障碍树。它曾经
// 是格子自己的 InsetShadow 环，画在填充层里、被铺满整格的视频井盖住，屏幕
// 上从来没有出现过——这条测试要挡的就是那种写法。
//
// 颜色取当前配色的 StatusOnline，和关注页、岛上那颗绿点同一支。
func TestWatchActiveTileShowsGreenDot(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	app.WatchActive = watchKey(rooms[0])
	tt.Frame()

	want := theme.StatusOnline
	// 数格子左上角 24×24 里有多少像素是那颗绿点的颜色。
	count := func(r ui.Rect) int {
		img := tt.Image()
		n := 0
		for y := int(r.Y) + 4; y < int(r.Y)+24; y++ {
			for x := int(r.X) + 4; x < int(r.X)+24; x++ {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				if absInt(int(cr>>8)-int(want.R)) < 26 &&
					absInt(int(cg>>8)-int(want.G)) < 26 &&
					absInt(int(cb>>8)-int(want.B)) < 26 {
					n++
				}
			}
		}
		return n
	}

	activeRect, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatalf("找不到激活格 %q", watchTileLabel(rooms[0]))
	}
	inactiveRect, ok := tt.Find(watchTileLabel(rooms[1]))
	if !ok {
		t.Fatalf("找不到非激活格 %q", watchTileLabel(rooms[1]))
	}

	if count(activeRect) == 0 {
		t.Fatal("激活格左上角没有绿点")
	}
	if n := count(inactiveRect); n != 0 {
		t.Fatalf("非激活格也出现了 %d 个绿点像素", n)
	}
}

// TestWatchDragShowsDropZones 锁住拖动时的落点提示：每格一层底色 + 序号，
// 松手后消失。
//
// 判据两条：第三格既不是拖动源、也不在指针下，没有 ghost 覆盖，它顶边的
// 像素必须在拖动中被底色改变——这一条分辨「画在视频井之上」还是「被盖住」；
// 序号走文本，拖动中 1–3 都在，松手后一个不剩。
func TestWatchDragShowsDropZones(t *testing.T) {
	rooms := watchTestRooms(3)
	app, tt := newWatchMock(t, rooms)
	// 强调色取白墨，底色压在近黑的视频井上才量得出来。
	app.PaletteID = "inkwhite-night"
	app.ThemeMode = themeModeDark
	tt.Frame()

	from, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到拖动源格")
	}
	to, ok := tt.Find(watchTileLabel(rooms[1]))
	if !ok {
		t.Fatal("找不到拖动目标格")
	}
	third, ok := tt.Find(watchTileLabel(rooms[2]))
	if !ok {
		t.Fatal("找不到第三格")
	}
	px, py := int(third.X+third.W/2), int(third.Y+3)
	probe := func() [3]int {
		r, g, b, _ := tt.Image().At(px, py).RGBA()
		return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	}

	if _, ok := tt.Find("第 3 格"); ok {
		t.Fatalf("没拖动就出现了落点序号，texts=%q", tt.Texts())
	}
	before := probe()

	sx, sy := from.X+from.W/2, from.Y+from.H/2
	tx, ty := to.X+to.W/2, to.Y+to.H/2
	tt.Move(sx, sy)
	tt.Press(sx, sy)
	tt.Move(sx+8, sy)
	tt.Frame()
	tt.Move(tx, ty)
	tt.Frame()

	after := probe()
	diff := absInt(after[0]-before[0]) + absInt(after[1]-before[1]) + absInt(after[2]-before[2])
	if diff < 12 {
		t.Fatalf("拖动中第三格没有落点底色：%v -> %v", before, after)
	}
	for _, n := range []string{"第 1 格", "第 2 格", "第 3 格"} {
		if _, ok := tt.Find(n); !ok {
			t.Fatalf("拖动中缺少「%s」，texts=%q", n, tt.Texts())
		}
	}

	tt.Release(tx, ty)
	tt.Frame()
	for _, n := range []string{"第 1 格", "第 2 格", "第 3 格"} {
		if _, ok := tt.Find(n); ok {
			t.Fatalf("松手后「%s」还在", n)
		}
	}
}

// 墙上的格子没有右键菜单：前移 / 后移 / 移出三项已经删掉，重排靠拖动。
func TestWatchTileHasNoContextMenu(t *testing.T) {
	rooms := watchTestRooms(2)
	app, tt := newWatchMock(t, rooms)
	if err := tt.RightClick(watchTileLabel(rooms[0])); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if labels := tt.Menu(); len(labels) != 0 {
		t.Fatalf("格子右键弹出了菜单：%v", labels)
	}
	if len(app.WatchRooms) != len(rooms) || app.Router.Path() != routeFollows {
		t.Fatalf("右键改变了状态：rooms=%d route=%q", len(app.WatchRooms), app.Router.Path())
	}
}

// 12 间是新的产品上限：第 10–12 间也必须上墙，不能还按 9 间截断。
func TestWatchTwelveRoomsOnWall(t *testing.T) {
	rooms := watchTestRooms(watch.MaxRooms)
	_, tt := newWatchMock(t, rooms)
	if watch.MaxRooms != 12 {
		t.Fatalf("MaxRooms=%d，期望 12", watch.MaxRooms)
	}
	for i, room := range rooms {
		if _, ok := tt.Find(watchTileLabel(room)); !ok {
			t.Fatalf("第 %d 间没有上墙", i+1)
		}
	}
}

// 控制条上的「重新加载」对未开播的格子也要真的重开：watchRestart 不清
// offline，watchOpenSession 会在门口直接返回，按钮原来一直是空点。
func TestWatchReloadReopensOfflineSession(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	key := watchKey(rooms[0])
	s := &wallSession{
		key: key, room: rooms[0], offline: true, err: "主播暂时没有开播", failures: 3,
	}
	app.watch.init()
	app.watch.sessions[key] = s
	tt.Frame()
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到格子")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("重新加载"); err != nil {
		t.Fatal(err)
	}
	if s.offline {
		t.Fatal("点「重新加载」后格子仍是未开播态")
	}
	if s.failures != 0 {
		t.Fatalf("手动重试没有重置退避计数：failures=%d", s.failures)
	}
}

// 12 格时格子只有三百来像素宽，控制条要比播放页小一档：
// 热区 24、滑条 64，不能再用 30 热区 + 96 滑条。
func TestWatchControlsAreCompact(t *testing.T) {
	rooms := watchTestRooms(watch.MaxRooms)
	_, tt := newWatchMock(t, rooms)
	r, ok := tt.Find(watchTileLabel(rooms[1]))
	if !ok {
		t.Fatal("找不到第二格")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	for _, label := range []string{
		"播放", "重新加载", "取消静音",
		"放大观测 " + rooms[1].Name, "移出观测 " + rooms[1].Name,
	} {
		b, ok := tt.Find(label)
		if !ok {
			t.Fatalf("找不到 %q", label)
		}
		if b.W > 24.5 || b.H > 24.5 {
			t.Errorf("%q 热区 %.0f×%.0f，超过 24", label, b.W, b.H)
		}
	}
	v, ok := tt.Find("音量")
	if !ok {
		t.Fatal("找不到音量条")
	}
	if v.W > 64.5 {
		t.Errorf("音量条宽 %.0f，超过 64", v.W)
	}
}

// 没有封面时的占位必须铺满整格。
//
// 原来井、视频、封面三个 Fill 层在格子里是流式兄弟，MyGo 的 flex 收缩把它们
// 平分成几段：上半截是媒体黑，下半截才是占位色，未加载态看起来像两块拼的。
func TestWatchPlaceholderFillsTile(t *testing.T) {
	rooms := watchTestRooms(4)
	_, tt := newWatchMock(t, rooms)
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到第一格")
	}
	img := tt.Image()
	cx := int(r.X + r.W/2)
	top := img.RGBAAt(cx, int(r.Y+r.H*0.2))
	bottom := img.RGBAAt(cx, int(r.Y+r.H*0.8))
	if top != bottom {
		t.Fatalf("占位没有铺满：上 %v，下 %v", top, bottom)
	}
	if top.R < 20 && top.G < 20 && top.B < 20 {
		t.Fatalf("占位顶上还是媒体黑 %v", top)
	}
}

// 没有封面时的占位要和首页卡片同一套（coverArt）：中性底 + 中心柔光，
// 不是按房间色相算出来的色块。冷启动时缓存里还没有封面，墙上不能先亮
// 一片绿。
func TestWatchPlaceholderMatchesCoverArt(t *testing.T) {
	rooms := watchTestRooms(4)
	_, tt := newWatchMock(t, rooms)
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到第一格")
	}
	img := tt.Image()
	// 20% / 20% 已经在中心柔光半径之外，读到的是占位底色。
	got := img.RGBAAt(int(r.X+r.W*0.2), int(r.Y+r.H*0.2))
	want := theme.BgTertiary
	if got.R != want.R || got.G != want.G || got.B != want.B {
		t.Fatalf("占位底色 %v，期望首页封面占位的 %v", got, want)
	}
}
