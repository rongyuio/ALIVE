package ui

// 生产崩溃 2026-11-25 16:49（crash-20261125-164733-29564-1.log）：
//
//	panic: invalid memory address or nil pointer dereference, addr=0x28
//	alive/internal/ui.(*App).runHotSwap.func1.1()  player_media.go:917
//
// 现场：焦点交接走 setFocus → cancelSession 把 media.ctx 置 nil，而焦点会话的
// 播放器还活着——m.eng() 读的是「焦点会话的 player」，「这一轮取流还在不在」看
// 的是 m.ctx，两者不是同一件事。于是 mediaTick 照样能走到 playerEnded →
// scheduleSwap，后台一路拿着 nil ctx 去问平台要地址；失败回调里 `sess.ctx.Err()`
// 对 nil 接口调方法，地址 0x28 正是 itab 里 Err 的槽位。
//
// 两条各自独立：scheduleSwap 不该在没有活会话时开跑（否则白白打一次平台接口），
// expired() 不该假设 ctx 存在（后台回调可能拿到任何一份凭据）。

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 没有活会话时，热切换压根不该开跑。
//
// 判据是 media.ctx，不是 eng()：cancelSession 只清 ctx，焦点会话的播放器照旧
// 活着（墙里的静音预览、交接中间态都是这个形态）。
func TestScheduleSwapRequiresLiveSession(t *testing.T) {
	app := seededApp(t)
	s := app.focus
	if s == nil {
		t.Fatal("夹具里没有焦点会话")
	}
	// 一个活着的播放器：eng() 因此非 nil，旧判据会放行。
	s.player = ui.NewEngineVideoPlayer(&steadyEngine{})
	app.media.cancelSession()
	if app.media.ctx != nil {
		t.Fatal("cancelSession 之后 ctx 应该为空")
	}

	app.scheduleSwap("dead-ended")

	if app.media.refreshing {
		t.Error("没有活会话仍派了热切换：这一路已经取消了，没有地址可换")
	}
}

// 作废的会话失败回来时：不崩、不计数。
//
// 改前这一句就是 nil 接口调用——`sess.ctx.Err()`，直接 0xc0000005。
func TestSwapFailAfterCancelDoesNotCount(t *testing.T) {
	app := seededApp(t)
	runOnMainNow(t)
	useProvider(t, &legacyStreamer{err: errors.New("取流失败")})

	app.beginSession("测试")
	app.media.cancelSession() // ctx = nil，seq++
	sess := app.session()     // 与 scheduleSwap 取凭据的同一个方法
	if sess.ctx != nil {
		t.Fatalf("作废会话的 ctx 应该是 nil，实际 %v", sess.ctx)
	}

	app.runHotSwap(sess, swapRequest{platform: live.Douyu, roomID: "123"}, "dead-ended")

	if app.media.swapFailures != 0 {
		t.Errorf("作废会话的失败被记进了退避：swapFailures=%d，期望 0", app.media.swapFailures)
	}
}

// 活会话失败回来时仍然计数：expired() 不能写成恒真，否则热切换退避整条失效，
// 一路坏流会把整轮重开打成死循环。
func TestSwapFailCountsForLiveSession(t *testing.T) {
	app := seededApp(t)
	runOnMainNow(t)
	useProvider(t, &legacyStreamer{err: errors.New("取流失败")})

	sess := app.beginSession("测试")
	if sess.expired() {
		t.Fatal("刚刚 beginSession 的会话不该被判作废")
	}

	app.runHotSwap(sess, swapRequest{platform: live.Douyu, roomID: "123"}, "dead-ended")

	if app.media.swapFailures != 1 {
		t.Errorf("活会话的失败没记进退避：swapFailures=%d，期望 1", app.media.swapFailures)
	}
}

// applyMedia 是所有异步结果的收口，它同样不能假设凭据还活着。
//
// 这条和上面那条是同一个事实的两端：后台拿到的 playbackSession 可能在真正
// 交给主线程之前就作废了（换房间、关直播间、收进墙）。判据只有 expired()
// 一处，谁都不许自己写 ctx.Err()。
func TestApplyMediaDropsCanceledSession(t *testing.T) {
	app := seededApp(t)
	runOnMainNow(t)

	app.beginSession("测试")
	app.media.cancelSession()
	sess := app.session()
	if sess.ctx != nil {
		t.Fatalf("作废会话的 ctx 应该是 nil，实际 %v", sess.ctx)
	}

	called, staled := false, false
	app.applyMedia(sess, func() { called = true }, func() { staled = true })

	if called {
		t.Error("作废会话的结果被接纳了")
	}
	if !staled {
		t.Error("作废会话的结果应该走 stale 分支，让调用方自己收资源")
	}
}

// 从墙上带进播放页的那一路，也要有自己的一轮媒体会话。
//
// 画面是墙格开的播放器，没经过 beginSession，而 setFocus 已经清掉了上一轮的
// ctx。少了这一轮，地址不会续、ended 不会重拉，热切换连凭据都拿不到——这正是
// 生产崩溃的前半段（后半段是拿着空凭据去问平台）。
func TestAdoptLiveSessionGivesTransferredPlayerItsRound(t *testing.T) {
	app := seededApp(t)
	s := app.focus
	if s == nil {
		t.Fatal("夹具里没有焦点会话")
	}
	p := ui.NewEngineVideoPlayer(&steadyEngine{})
	s.player = p
	app.media.cancelSession()
	if app.media.ctx != nil {
		t.Fatal("cancelSession 之后 ctx 应该为空")
	}

	app.ensureFocusMedia()

	if app.media.ctx == nil {
		t.Fatal("带进播放页的播放器没有补上媒体轮次")
	}
	if s.player != p {
		t.Error("接管把画面换掉了：应该接着用同一枚播放器，不该重新取流")
	}
	if !app.media.firstFrame() {
		t.Error("已经在出图的一路不该按「还没首帧」处理，否则首帧预算会把它判成失败")
	}
}

// 已经有一轮在跑时，接管不能再来一次：每帧调用的守卫不许推进代号。
func TestAdoptLiveSessionDoesNotRestartRunningRound(t *testing.T) {
	app := seededApp(t)
	s := app.focus
	if s == nil {
		t.Fatal("夹具里没有焦点会话")
	}
	s.player = ui.NewEngineVideoPlayer(&steadyEngine{})
	app.beginSession("取流")
	seq := app.media.seq

	app.ensureFocusMedia()

	if app.media.seq != seq {
		t.Errorf("已经在跑的一轮被接管重开了：seq %d → %d", seq, app.media.seq)
	}
}
