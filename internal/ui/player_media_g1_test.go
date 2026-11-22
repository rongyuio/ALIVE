package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 从墙切到别的页面：这条一路已经在后台（background=true），仍要按新表面
// 重求一次呈现尺寸。
//
// 墙上 active 由媒体层持有时，watchAdoptActive(true) 先把 background 置真，
// watchShow 又把 present 求成格子物理像素。此后从墙切到首页/关注页，
// detachMedia 的 `if m.background { return }` 让「无视图 → 160×90」这条
// 规则永远不执行：引擎继续按格子像素解码（软解时最多差 64 倍像素量）。
func TestDetachWhileBackgroundRecomputesPresent(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.media.background = true
	a.media.present = presentSize{W: 1280, H: 720}
	a.Router = ui.NewRouter(routeHome)

	a.detachMedia()

	if got, ok := spy.last(); !ok || got != (presentSize{W: 160, H: 90}) {
		t.Fatalf("后台收听离开页面后呈现尺寸是 %+v（推过 %v），期望 160×90", got, ok)
	}
}

// 过期的热切换失败不能记到新会话头上。
//
// 换房间 / closeMedia 会 cancelSession 推进代号，旧一路的 fetchStream 带着
// context.Canceled 回来时照样进 fail 闭包：swapFailures 记到新会话上，
// 新会话自己的热切换再失败一次就凑满阈值、整轮重开（断音 + 加载遮罩）。
func TestStaleHotSwapFailureDoesNotPolluteSession(t *testing.T) {
	a := seededApp(t)
	useProvider(t, &stubStreamer{err: errors.New("取流失败")})
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool {
		fn()
		return true
	}
	t.Cleanup(func() { runOnMain = oldRun })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.media.ctx, a.media.cancel = ctx, cancel
	a.media.seq = 7

	// 旧会话打包出去的凭据。
	sess := a.session()
	// 新一轮已经接手（beginSession / cancelSession 之后 seq 已推进）。
	a.media.seq++
	a.media.refreshing = true

	a.runHotSwap(sess, swapRequest{platform: live.Douyu, roomID: "123", quality: "原画"}, "地址到期")

	if a.media.swapFailures != 0 {
		t.Fatalf("过期会话的失败记到了新会话上：swapFailures=%d", a.media.swapFailures)
	}
	if !a.media.refreshAt.IsZero() {
		t.Fatalf("过期失败把新会话的换地址推迟到了 %v", a.media.refreshAt)
	}
	if !a.media.refreshing {
		t.Fatal("过期失败清掉了新会话的 refreshing")
	}
}

// 从列表点开另一间房就是真正换台：播放意图必须回到「在播」。
//
// a.Playing 是焦点会话的视图状态：上一个房间暂停过（或点了「取消观测发声」）
// 之后它一直是 false，ensureMedia 换台时若不重置，adoptPlayback 会照着它
// Pause()，新房间以暂停态起播——没画面、没声音、控制条显示「播放」。
func TestEnterNewRoomResetsPlayIntent(t *testing.T) {
	a := seededApp(t)
	// 不支持取流的平台：startStream 同步落错误，不派后台 goroutine。
	useProvider(t, plainProvider{})
	a.Playing = false
	a.Player = Streamer{Platform: "douyu", RoomID: "456", Name: "新房间"}

	a.ensureMedia(nil)

	if !a.Playing {
		t.Fatal("进入新直播间后 a.Playing 仍是 false：新房间会以暂停态起播")
	}
	if got := a.media.roomKey; got != "douyu|456" {
		t.Fatalf("换台后媒体房间是 %q，期望 douyu|456", got)
	}

	// 同一间房的重新起播（热切换、断流重连）不走换台分支，暂停态要保住。
	a.Playing = false
	a.ensureMedia(nil)
	if a.Playing {
		t.Fatal("同一间房重进时把用户的暂停态重置了")
	}
}

// 按 X 从播放页退回墙上：引擎的呈现尺寸必须补推回格子物理像素。
//
// 播放页里引擎是全尺寸（0,0）。watchReleaseActive 把新墙会话的 renderW/H
// 预填成格子尺寸，而 watchShow 只在「renderW/H 与这一帧的格子不一致」时才
// 推——预填之后恒等，于是这一格一直按源分辨率（1080p/2K）渲染。
func TestCloseMediaRestoresTilePresentSize(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	spy := stubEngines(t)
	fakeSession(t, app, rooms[0])
	tt.Frame()

	label := watchTileLabel(rooms[0])
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() != routePlayer {
		t.Fatalf("双击格子没有进播放页，route=%q", app.Router.Path())
	}
	if got, ok := spy.last(); !ok || got != (presentSize{}) {
		t.Fatalf("播放页该按视频原始尺寸呈现，实际 %+v（推过 %v）", got, ok)
	}

	if err := tt.Click("退出直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	r, ok := app.watch.rects[watchKey(rooms[0])]
	if !ok {
		t.Fatal("回到墙上后没有这一格的布局")
	}
	w, h := watchSize(r)
	if got, ok := spy.last(); !ok || got != (presentSize{W: w, H: h}) {
		t.Fatalf("退回墙上后引擎呈现 %+v（推过 %v），期望格子 %d×%d", got, ok, w, h)
	}
}
