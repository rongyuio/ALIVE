package ui

import (
	"os"
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
//
// 收尾从 backgroundEnter（全尺寸边沿）挪到了 tickActivity（每帧观察，构建
// 之前跑）：离开播放页只是它的一种情形，不是唯一入口。
func TestTickActivityDropsDeadFocusWithoutStream(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, offline: true} // 没有 stream，也没引擎
	a.watch.init()
	a.focus = s
	a.Router = ui.NewRouter(routeHome)

	a.tickActivity()

	if a.focus != nil {
		t.Fatal("没有引擎、没在取流、已落终态的焦点还留在岛上")
	}
}

// 在播放页上不收：那一页自己画错误层、有「重试」。
func TestTickActivityKeepsDeadFocusOnPlayerPage(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, offline: true}
	a.watch.init()
	a.focus = s
	a.Router = ui.NewRouter(routePlayer)

	a.tickActivity()

	if a.focus == nil {
		t.Fatal("播放页上的死焦点被收掉了：错误层和重试一起消失")
	}
}

// leavePlayer 只在「刚从播放页离开」那一帧调。
//
// 原来写成「只要不在播放页就每帧调」：leavePlayer 里无条件
// windowFullScreen(a.Win)（一次平台往返），首页静置每秒白花 ~60 次。
// 现在它在 Shell 开头（构建之前）由 tickLeavePlayer 做边沿判断。
func TestLeavePlayerRunsOnlyOnTheLeavingFrame(t *testing.T) {
	assertNoEveryFrameLeavePlayer(t, "home.go")
	assertNoEveryFrameLeavePlayer(t, "surface_motion.go")
	src, err := os.ReadFile("surface_motion.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "if !onPlayer && a.wasOnPlayer {") {
		t.Error("没有按「离开那一帧」判据调用 leavePlayer")
	}
}

func assertNoEveryFrameLeavePlayer(t *testing.T, file string) {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "if a.Router.Path() != routePlayer {\n\t\ta.leavePlayer()") {
		t.Errorf("%s 还在每帧调 leavePlayer", file)
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

// 弹幕推动器的状态迁移必须在锁里做，且退出前要在锁里再确认一次 active。
//
// 原来是「active=true 再 CAS(loop false→true)」，退出时把 loop 放回 false。
// stop→start 撞在旧 goroutine「已读到 active=false、还没跑到 defer」的窗口里，
// CAS 失败就 return，随后旧循环退出并置 loop=false——没有任何 goroutine 在跑
// 而 active 为 true，弹幕冻住，只能等下一次构建再拉起。
func TestDanmuLoopStateMigratesUnderLock(t *testing.T) {
	body := funcBody(t, "danmu.go", "func (a *App) ensureDanmuLoop()")
	if strings.Contains(body, "CompareAndSwap") {
		t.Error("还在用 CAS 抢闸门：stop→start 的窗口里会留下没人跑的推动器")
	}
	if !strings.Contains(body, "a.danmu.startMu.Lock()") {
		t.Error("状态迁移没有持锁")
	}
	// 退出路径必须在锁里再确认 active。
	i := strings.Index(body, "a.danmu.running = false")
	if i < 0 {
		t.Fatal("找不到退出路径")
	}
	if !strings.Contains(body[:i], "if a.danmu.active.Load() {") {
		t.Error("退出前没有在锁里再确认 active")
	}

	stop := funcBody(t, "danmu.go", "func (a *App) stopDanmuLoop()")
	if !strings.Contains(stop, "a.danmu.startMu.Lock()") {
		t.Error("stopDanmuLoop 没持锁")
	}
}

// 取具体引擎只有一处类型断言，其余全走 playbackEngineOf。
//
// 原来 trackContinuity / forwardWallMpv / 首帧诊断各自写死断言：换实现时呈现
// 那一半走 engineOf 静默降级、诊断那一半静默丢日志，两半行为不一致且都不报错。
func TestPlaybackEngineAssertedInOnePlace(t *testing.T) {
	for _, f := range []string{"player_media.go", "watch_media.go", "player.go"} {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		lines := strings.Split(string(src), "\n")
		for i, l := range lines {
			if !strings.Contains(l, "Engine().(*playback.Engine)") {
				continue
			}
			ctx := strings.Join(lines[maxInt(0, i-6):i], "\n")
			if strings.Contains(ctx, "func playbackEngineOf(") {
				continue
			}
			t.Errorf("%s:%d 又写死了一处引擎类型断言，应走 playbackEngineOf", f, i+1)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
