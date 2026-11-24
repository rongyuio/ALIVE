package ui

import "testing"

// 后台收听时重开一路（登录成功要重取流）：新引擎建成后呈现尺寸要立刻收到
// 160×90，后台标记也要补回来，否则岛还会掉回默认态。
func TestAdoptBackgroundKeepsPresentingOff(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.Router.Push(routeHome)
	a.media.setLoading(false)

	a.backgroundEnter()

	if !a.inBackground() {
		t.Fatal("后台重开没进后台收听态")
	}
	if got, ok := spy.last(); !ok || got != (presentSize{W: 160, H: 90}) {
		t.Fatalf("后台收听该收成 160×90，实际 %+v（推过 %v）", got, ok)
	}
	if a.media.detachedAt.IsZero() {
		t.Fatal("没记下进入后台的时刻")
	}
}

// 人在播放页时开出来的引擎当然要出画面。
func TestAdoptBackgroundIgnoredOnPlayerPage(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.Router.Push(routePlayer)
	a.foregroundEnter()

	if a.inBackground() {
		t.Fatal("在播放页上被判成了后台收听")
	}
	// 播放页本来就是视频原始尺寸，呈现值没变不该重复推。
	if n := spy.count(); n != 0 {
		t.Fatalf("在播放页上动了呈现（%d 次）", n)
	}
}

// 重开这一路的几秒里岛仍然算「在收听」，不能掉回时钟。
func TestIslandListensWhileRestarting(t *testing.T) {
	a := seededApp(t)
	a.Router.Push(routeHome)

	// 正在重开：加载中，岛保留这一间。
	a.media.setLoading(true)
	if a.islandFormOf() != islandOpen {
		t.Fatal("正在重开这一路时岛掉回了默认态")
	}

	// 引擎活着、人离开播放页：常态的后台收听。
	a.focus.player = gifPlayer(t)
	a.media.setLoading(false)
	if a.islandFormOf() != islandOpen {
		t.Fatal("后台收听时岛没显示")
	}

	// 没引擎、没在取流、也不在播放页：没声音就不该占着岛。
	a.focus.player = nil
	a.focus.err = "取流失败"
	if a.islandFormOf() != islandIdle {
		t.Fatal("取流失败后岛还挂着「在收听」")
	}

	// 播放页上即使失败也保留：错误层和退出键都还要用。
	a.Router.Push(routePlayer)
	if a.islandFormOf() != islandOpen {
		t.Fatalf("播放页上岛是 %v，期望展开态", a.islandFormOf())
	}
}
