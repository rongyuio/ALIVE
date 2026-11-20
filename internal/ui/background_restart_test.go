package ui

import "testing"

// 后台收听时重开一路（登录成功要重取流）：新引擎建成后全尺寸呈现要立刻关掉，
// 只留 160×90 的小尺寸解码，后台标记也要补回来，否则岛还会掉回默认态。
func TestAdoptBackgroundKeepsPresentingOff(t *testing.T) {
	a := seededApp(t)
	spy := &trackSpy{}
	useTrack(t, a, spy)

	a.Router.Push(routeHome)
	a.media.roomKey = "huya|333003"
	a.media.loading = false

	a.adoptBackgroundPresentingOff()

	if !a.media.background {
		t.Fatal("后台重开没进后台收听态")
	}
	if on, ok := spy.last(); !ok || on {
		t.Fatalf("全尺寸呈现最后状态是 %v（有没有设过 %v），应当关掉", on, ok)
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
	a.adoptBackgroundPresentingOff()

	if a.media.background {
		t.Fatal("在播放页上被判成了后台收听")
	}
	if n := spy.count(); n != 0 {
		t.Fatalf("在播放页上动了呈现（%d 次）", n)
	}
}

// 重开这一路的几秒里岛仍然算「在收听」，不能掉回时钟。
func TestIslandListensWhileRestarting(t *testing.T) {
	a := seededApp(t)
	a.Router.Push(routeHome)
	a.media.roomKey = "huya|333003"

	a.media.background = false
	a.media.loading = true
	if !a.islandListening() {
		t.Fatal("正在重开这一路时岛掉回了默认态")
	}

	// 取流失败：没声音了就不该再显示在收听。
	a.media.loading = false
	if a.islandListening() {
		t.Fatal("取流失败后岛还挂着「在收听」")
	}

	// 引擎活着、人离开播放页：这才是常态的后台收听。
	a.media.background = true
	if !a.islandListening() {
		t.Fatal("后台收听时岛没显示")
	}

	// 回到播放页：岛切成主播那一态。
	a.Router.Push(routePlayer)
	if a.islandListening() {
		t.Fatal("播放页上岛还在画后台收听")
	}
}
