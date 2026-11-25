package ui

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"alive/internal/live"
)

// stubMainThread 把 runOnMain 换成同步执行，并在接纳回调落地后放行。
//
// 平台确认是 goroutine + 回主线程的结构，不换掉 runOnMain 就得轮询。
func stubMainThread(t *testing.T) <-chan struct{} {
	t.Helper()
	old := runOnMain
	var once sync.Once
	done := make(chan struct{})
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		fn()
		once.Do(func() { close(done) })
		return true
	}
	t.Cleanup(func() { runOnMain = old })
	return done
}

// mediaCtx 给媒体层一个活的 ctx。
//
// scheduleSwap 会用它去取流；没有会话 ctx 时那条 goroutine 在 fail 回调里
// 解引用 nil，测试会以 panic 收场——那验的不是接线，是夹具缺了一块。
func mediaCtx(t *testing.T, app *App) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	app.media.ctx, app.media.cancel = ctx, cancel
	t.Cleanup(cancel)
}

// 播放页判死「播放器自己停了」时必须先问平台。
//
// 主播下播和地址过期在画面上分不出来：两者都是连接被关、mpv 当成文件结束。
// 不问就换地址，下播的房间会被无休止地重取——平台一直给不出流，三次失败整轮
// 重开（loading 覆盖层），再判死。墙上有这条判据（watchEnded），播放页漏了。
func TestPlayerEndedAsksPlatformBeforeSwap(t *testing.T) {
	app := seededApp(t)
	mediaCtx(t, app)
	s := app.focus
	s.player = gifPlayer(t)
	s.track = &trackSpy{}

	prov := &offlineRoomProvider{}
	useProvider(t, prov)
	done := stubMainThread(t)

	app.playerEnded(s)

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("等不到播放页的平台确认结果")
	}
	if prov.lookups.Load() == 0 {
		t.Fatal("播放页判死没问平台，直接去换地址了")
	}
	if !s.offline {
		t.Fatalf("平台说已经下播，播放页却还在重连：err=%q refreshing=%v", s.err, app.media.refreshing)
	}
	if s.err != "主播暂时没有开播" {
		t.Fatalf("落到的提示是 %q，期望「主播暂时没有开播」", s.err)
	}
}

// 接线：判死链上「播放器自己停了」这一支必须走平台确认，不能直接换地址。
//
// 上面那条测的是 playerEnded 本身；把它接回 scheduleSwap("dead-ended")，
// 那条测试照样绿——所以这条从 trackFirstFrame 进，盯的是接线。
func TestTrackFirstFrameRoutesEndedToPlatform(t *testing.T) {
	app := seededApp(t)
	mediaCtx(t, app)
	s := app.focus
	p := gifPlayer(t)
	s.player = p
	s.track = &trackSpy{}
	s.firstFrame = true
	s.playing = true

	prov := &offlineRoomProvider{}
	useProvider(t, prov)
	done := stubMainThread(t)

	// 播放意图还在播，播放器自己停了——这正是 deadReason 返回 ended 的条件。
	p.Pause()
	app.trackFirstFrame()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("判死走了换地址那条路，没有问平台")
	}
	if prov.lookups.Load() == 0 {
		t.Fatal("判死没有问平台")
	}
	if !s.offline {
		t.Fatalf("平台上说下播了，会话没落未开播：err=%q", s.err)
	}
}

// 平台说还在播：交给 onLive 继续，不能把一路断流当成下播。
//
// 这里直接测 confirmEnded 的回调契约，不走 playerEnded：接上去真会派一次
// 换地址（取流 + 等首帧 + 起引擎），那是 scheduleSwap 自己的测试范围。
func TestConfirmEndedStillLiveHandsToOnLive(t *testing.T) {
	app := seededApp(t)
	mediaCtx(t, app)
	s := app.focus
	s.player = gifPlayer(t)
	s.track = &trackSpy{}

	prov := &liveRoomProvider{}
	useProvider(t, prov)
	done := stubMainThread(t)

	var gotWhy string
	called := false
	app.confirmEnded(s, "dead-ended", func(_ *roomSession, why string) {
		called, gotWhy = true, why
	})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("等不到平台确认结果")
	}
	if prov.lookups.Load() == 0 {
		t.Fatal("没有问平台")
	}
	if s.offline {
		t.Fatal("平台说还在播，却被落成了未开播")
	}
	if !called {
		t.Fatal("平台说还在播，却没有交给 onLive")
	}
	if gotWhy != "dead-ended" {
		t.Fatalf("onLive 拿到的原因是 %q，期望原样传下去的「dead-ended」", gotWhy)
	}
}

// liveRoomProvider 回答「这个房间还在播」。
type liveRoomProvider struct {
	plainProvider
	lookups atomic.Int32
}

func (p *liveRoomProvider) RoomInfo(context.Context, string) (live.Room, error) {
	p.lookups.Add(1)
	return live.Room{Live: true}, nil
}
