package ui

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 焦点那一间的「重新加载」必须归媒体层，不管它在不在全尺寸视图里。
//
// 原来按 `s == a.focus && a.fullSize()` 分流：焦点收进岛（后台收听）时
// fullSize() 为假，落到墙路径，watchOpenSession 再开一枚引擎，而
// acceptWallEngine 里 attachPlayer 只是 `s.player = p`——旧引擎不关。同一间房
// 两个引擎，媒体层的 m.ctx 还描述着上一轮。
func TestWatchReloadOwnedByMediaRegardlessOfFullSize(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	s := &roomSession{key: watchKey(room), room: room}
	other := &roomSession{key: watchKey(Streamer{Platform: "bilibili", RoomID: "9"})}
	a.watch.init()
	a.focus = s

	// 播放页上（fullSize 为真）：归媒体层。
	a.Router = ui.NewRouter(routePlayer)
	if !a.watchReloadBelongsToMedia(s) {
		t.Fatal("播放页上的焦点不归媒体层")
	}

	// **收进岛**（不在播放页，fullSize 为假）：仍然归媒体层。
	// 这一条是修复的核心——旧实现在这里返回假，转去墙路径开第二个引擎。
	a.Router = ui.NewRouter(routeFollows)
	if a.fullSize() {
		t.Fatal("前提不成立：此时 fullSize 应为假")
	}
	if !a.watchReloadBelongsToMedia(s) {
		t.Fatal("焦点收进岛之后被判成墙层管：会再开一枚引擎，旧的不关")
	}

	// 非焦点：归墙层。
	if a.watchReloadBelongsToMedia(other) {
		t.Error("非焦点会话被判成媒体层管")
	}
	if a.watchReloadBelongsToMedia(nil) {
		t.Error("nil 会话被判成媒体层管")
	}
}

// acceptWallEngine 必须接住 runOnMain 的返回值。
//
// runOnMain 在窗口为 nil 或已销毁时返回 false，回调永远不会跑；而
// `e.Close()` 写在回调里。这一枚刚开出来的引擎（一条流、一个解码器、一块
// 显存）就此无人管——窗口销毁/重建的间隙每走过一次就漏一个。
//
// 这条用结构性判据：引擎是具体类型 *playback.Engine，没有库造不出实例，
// 所以断言"返回值被当成条件、且 else 分支里关掉引擎"这层形状。
func TestAcceptWallEngineClosesEngineWhenCallbackCannotRun(t *testing.T) {
	src, err := os.ReadFile("watch_media.go")
	if err != nil {
		t.Fatalf("读不到 watch_media.go：%v", err)
	}
	text := string(src)
	i := strings.Index(text, "func (a *App) acceptWallEngine(")
	if i < 0 {
		t.Fatal("找不到 acceptWallEngine")
	}
	body := text[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "if !runOnMain(win, func() {") {
		t.Error("acceptWallEngine 没有把 runOnMain 的返回值当成条件：窗口没了引擎会漏")
	}
	if !strings.Contains(body, "_ = e.Close()") {
		t.Error("acceptWallEngine 的 else 分支里没有关引擎")
	}
}

// 探测失败要记上，退避才会升级。
//
// 原来两条早退分支用 `offlineProbeBackoff(s.failures+1, ...)` 算延迟却不写回
// s.failures：每一次都拿到同一个延迟，退避永远升不了级，一个接不进来的平台
// 会被按固定节拍一直问（其中一条还会每次都完整取流一次）。
func TestWatchProbeBackoffEscalates(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	s := &roomSession{key: watchKey(room), room: room}
	a.watch.init()
	a.watch.sessions[s.key] = s

	origProv := providerFor
	providerFor = func(live.Platform) (live.Provider, bool) { return nil, false }
	defer func() { providerFor = origProv }()

	now := time.Now()
	a.watchProbe(s, now)
	if s.failures != 1 {
		t.Fatalf("第一次探测失败后 failures = %d，应为 1（没记上退避就永远不升级）", s.failures)
	}
	first := s.retryAt.Sub(now)

	// 第二次：退避要更长。
	now2 := now.Add(time.Hour)
	a.watchProbe(s, now2) // lastProbe 的 500ms 错峰已过
	if s.failures != 2 {
		t.Fatalf("第二次之后 failures = %d，应为 2", s.failures)
	}
	second := s.retryAt.Sub(now2)
	if second <= first {
		t.Errorf("第二次退避 %v 不比第一次 %v 长：退避没有升级", second, first)
	}
}
