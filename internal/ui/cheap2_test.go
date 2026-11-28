package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 弹幕面板的 Key 不能捕获某一帧的局部切片。
//
// Key 存进长期 ListState：面板收起后 danmuPanelList 不再执行，闭包一直是旧
// 快照；别处调 Key 会拿到过期切片并越界。现在它每次现读 a.DanmuLog 并自己
// clamp，日志变长之后照样安全。
func TestPanelListKeySurvivesLogGrowth(t *testing.T) {
	a := newApp()
	a.DanmuLog = nil
	tt := ui.NewTester(func(c *ui.Context) { a.danmuPanelList(c) }, 400, 600)
	tt.Frame() // 面板建一帧，装好 Key

	// 面板不再重建的情况下日志变长：Key 必须读到新的那份，且不许越界。
	for i := 0; i < 5; i++ {
		a.DanmuLog = append(a.DanmuLog, danmuLine{Seq: uint64(100 + i)})
	}
	if got := a.PanelList.Key(4); got != uint64(104) {
		t.Fatalf("Key(4) = %v，应为 104：闭包还在读旧快照", got)
	}
	// 越界下标不许 panic。
	if got := a.PanelList.Key(999); got == nil {
		t.Error("越界下标返回了 nil，容易和「没有身份」混淆")
	}
}

// 「死焦点」判据不许依赖 stream 这个可选字段。
//
// 很多路径根本不填 stream，`stream.RoomID != ""` 恒假——「没有引擎、没在取流、
// 也没在加载」的焦点会留在岛上，岛显示一条不存在的路。
func TestBackgroundEnterDropsDeadFocusWithoutStream(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, offline: true} // 没有 stream，也没引擎
	a.watch.init()
	a.focus = s

	a.backgroundEnter()

	if a.focus != nil {
		t.Fatal("没有引擎、没在取流、已落终态的焦点还留在岛上")
	}
}

// leavePlayer 只在「刚从播放页离开」那一帧调。
//
// 原来写成「只要不在播放页就每帧调」：leavePlayer 里无条件
// windowFullScreen(a.Win)（一次平台往返），首页静置每秒白花 ~60 次。
func TestLeavePlayerRunsOnlyOnTheLeavingFrame(t *testing.T) {
	src, err := os.ReadFile("home.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if strings.Contains(text, "if a.Router.Path() != routePlayer {\n\t\ta.leavePlayer()") {
		t.Error("还在每帧调 leavePlayer")
	}
	if !strings.Contains(text, "if !onPlayer && a.wasOnPlayer {") {
		t.Error("没有按「离开那一帧」判据调用 leavePlayer")
	}
}

// restore 要把认不出的关注记录挡掉。
//
// 空平台 / 空昵称的记录落地后既画不出来也删不掉（sameFollow 先比平台），
// 只会变成一条永远刷不到、却一直被写回文件的幽灵关注。带空格的值顺手修掉。
func TestRestoreDropsUnidentifiableFollows(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfg, err := os.UserConfigDir()
	if err != nil || cfg == "" {
		t.Skip("拿不到配置目录")
	}
	full := filepath.Join(cfg, "alive")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Skip(err)
	}
	body := `{"followRecords":[
		{"platform":"","name":"没有平台"},
		{"platform":"bilibili","name":""},
		{"platform":"douyu ","name":"甲"}
	]}`
	if err := os.WriteFile(filepath.Join(full, "state.json"), []byte(body), 0o644); err != nil {
		t.Skip(err)
	}

	a := newApp()
	a.restore()
	if len(a.Follows) != 1 {
		t.Fatalf("留下了 %d 条关注，应只留 1 条：%+v", len(a.Follows), a.Follows)
	}
	if a.Follows[0].Platform != "douyu" {
		t.Errorf("平台没去空格：%q", a.Follows[0].Platform)
	}
}

// 缓冲观测不许在构建路径里做。
//
// bufferingPill 原来直接调 stalled(p)，而它写 m.lastPos / m.lastPosAt——构建
// 路径写状态。观测挪到 trackFirstFrame，绘制只读 m.bufferingNow。
func TestBufferingPillOnlyReadsObservation(t *testing.T) {
	a := newApp()
	sess := &roomSession{key: "p|1", room: Streamer{Platform: "p", RoomID: "1", Name: "房"}}
	sess.attachPlayer(testPlayer(t))
	a.focus = sess

	// 观测结果在，胶囊就画。
	a.media.bufferingNow = true
	tt := ui.NewTester(func(c *ui.Context) { a.bufferingPill(c) }, 400, 200)
	tt.Frame()
	if !tt.HasText("缓冲中") {
		t.Fatal("观测为真时没画胶囊")
	}

	// 观测结果不在，就不画。
	a.media.bufferingNow = false
	tt2 := ui.NewTester(func(c *ui.Context) { a.bufferingPill(c) }, 400, 200)
	tt2.Frame()
	if tt2.HasText("缓冲中") {
		t.Error("观测为假时还画胶囊")
	}

	// 构建本身不许改观测量。
	before := a.media.lastPos
	a.media.bufferingNow = true
	tt3 := ui.NewTester(func(c *ui.Context) { a.bufferingPill(c) }, 400, 200)
	tt3.Frame()
	if a.media.lastPos != before {
		t.Error("构建路径改了 lastPos：观测还在构建里")
	}
}
