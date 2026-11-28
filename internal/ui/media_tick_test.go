package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// 定时心跳只许有一条链。
//
// stopPlayback 原来只把 tickPending 清成 false，不取消已经排上的
// time.AfterFunc：那一刻标记是 false，kickMediaTick 于是再排一条，两条链各自
// 清标记、各自重排，永久并存——mediaTick 的频率翻倍（watchTick 每拍扫 12 格
// Debug()，trackFirstFrame 每拍跑一遍）。
//
// 用 afterFunc 接缝把「排定时器」拿在手里，就能不 sleep、不按挂钟判过过：
// 谁被排上、什么时候跑，由测试决定。
func TestMediaTickKeepsOneChain(t *testing.T) {
	a := newApp()
	a.Win = new(mygo.Window)
	// 让 needMediaTick 成立：墙上有会话。retryAt 放到一小时后，watchTick 这一拍
	// 什么都不做，测试不会打到网络。
	a.watch.init()
	s := &roomSession{
		key:     "p|1",
		room:    Streamer{Platform: "p", RoomID: "1", Name: "房"},
		retryAt: time.Now().Add(time.Hour),
	}
	a.watch.sessions[s.key] = s

	var queued []func()
	origAfter := afterFunc
	afterFunc = func(d time.Duration, fn func()) *time.Timer {
		queued = append(queued, fn)
		return time.NewTimer(time.Hour)
	}
	defer func() { afterFunc = origAfter }()

	origRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	defer func() { runOnMain = origRun }()

	// 1. 连叫两次，只排一条。
	a.kickMediaTick()
	a.kickMediaTick()
	if len(queued) != 1 {
		t.Fatalf("连叫两次排了 %d 条，应为 1", len(queued))
	}

	// 2. stopPlayback 作废这一代。
	a.stopPlayback()
	if a.media.tickPending {
		t.Fatal("stopPlayback 之后标记还立着")
	}

	// 3. 允许排新的一条。
	a.kickMediaTick()
	if len(queued) != 2 {
		t.Fatalf("stopPlayback 之后排不出来新链：%d 条", len(queued))
	}
	if !a.media.tickPending {
		t.Fatal("新链排上了，标记却是 false")
	}

	// 4. 走旧的那一条（已被作废）：不许碰标记、不许再排。
	stale := queued[0]
	stale()
	if len(queued) != 2 {
		t.Fatalf("作废的那一代又排出了新的：%d 条（旧实现会变成两条活链）", len(queued))
	}
	if !a.media.tickPending {
		t.Fatal("作废的那一代把新链的标记清掉了")
	}

	// 5. 走当前那一代：正常结算并续排下一拍（所以标记又立起来是**对的**）。
	live := queued[1]
	live()
	if len(queued) != 3 {
		t.Fatalf("当前这一代没有续排下一拍：%d 条", len(queued))
	}
	if !a.media.tickPending {
		t.Fatal("续排了下一拍，标记却是 false：下一拍会被重复排")
	}
}

// 窗口最小化时，播放页上的心跳必须继续。
//
// inBackground() 只判「焦点在但没全尺寸」，而最小化时 fullSize() 仍是 true
// （还停在播放页），于是 need 为假。而最小化时 Shell 不再重建
// （main.go:170 的 OnMinimize 注释），没有任何元素在请求帧——地址续期和
// 断流重连就此没人接管：标签里的地址过期 300 秒后恢复窗口，画面是冻住的，
// 日志里连一条 session.swap_* 都没有。
func TestNeedMediaTickCoversMinimizedWindow(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	s := &roomSession{key: watchKey(room), room: room}
	s.attachPlayer(testPlayer(t))
	a.watch.init()
	a.Router = ui.NewRouter(routePlayer)
	a.focus = s

	// 播放页、窗口可见、墙上有会话：墙那条兜住了，是真的。先清掉墙会话，
	// 单独验"最小化"这一条。
	delete(a.watch.sessions, s.key)
	if a.inBackground() {
		t.Fatal("前提不成立：播放页上 inBackground 应为假")
	}
	if a.needMediaTick() {
		t.Fatal("播放页可见、墙上没会话：不该需要心跳")
	}

	a.windowHidden = true
	if !a.needMediaTick() {
		t.Fatal("最小化在播放页上，心跳没起来：地址续期和断流重连没人管")
	}

	// 墙上哪怕没有会话、窗口可见，只要有焦点在出声也要心跳（原来的行为）。
	a.windowHidden = false
	a.Router = ui.NewRouter(routeFollows) // 不在播放页 → inBackground 为真
	if !a.needMediaTick() {
		t.Fatal("后台收听（焦点在没有全尺寸视图）时心跳没起来")
	}
}

// 回到前台要清掉缓冲计时，否则「进后台前正在缓冲」会立刻判定卡死。
//
// deadReason 的缓冲分支判据是 `pos != m.stillPos || bufferStallSince.IsZero()`；
// 回来时 pos 与刚清过的 stillPos 都是 0，第一条不成立，于是只剩
// `now.Sub(bufferStallSince) > 20s`——若那颗时间还停在进后台之前，一回播放页
// 就派一次热切换。
func TestForegroundEnterClearsBufferStall(t *testing.T) {
	a := newApp()
	a.media.bufferStallSince = time.Now().Add(-time.Minute)
	a.media.stillPos = 0
	a.media.bufferStallSince = time.Now().Add(-time.Minute)

	a.foregroundEnter()

	if !a.media.bufferStallSince.IsZero() {
		t.Fatalf("回到前台后 bufferStallSince 还停在 %v：会立刻判定卡死并派热切换",
			a.media.bufferStallSince)
	}
	if !a.media.lastPosAt.IsZero() || !a.media.stillAt.IsZero() {
		t.Error("配套的观测点没一起清")
	}
}

// 用不到但保留导入不报错的占位：testPlayer 在别的文件里。
var _ = ui.Rect{}
