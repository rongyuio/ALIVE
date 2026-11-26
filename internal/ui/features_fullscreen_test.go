package ui

// 系统全屏：Esc 只退全屏，不把用户踢出直播间。
//
// 这条规则写在 shell_keys.go 的注释里（"全屏时 Esc 只退出全屏，绝不顺手
// 把用户踢出直播间"），但从来没有断言过。它测不到的原因和别的几条一样：
// windowFullScreen / windowMaximized / SetFullScreen 都直接调 *mygo.Window
// 的方法，而零值 Window 在 Tester 里会走 onMain 永久阻塞。
//
// 三个接缝抽出来之后（player.go），这条能验了。

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// sysFullApp 造一个「系统全屏中」的 App。
//
// 返回的 setFull 让测试能模拟用户 / 系统改变全屏状态。
func sysFullApp(t *testing.T) (app *App, tt *ui.Tester, isFull *bool) {
	t.Helper()
	// 零值 *mygo.Window 的方法会走 onMain；这三个接缝全换成内存状态。
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = oldRun })

	full := false
	oldFS := windowFullScreen
	windowFullScreen = func(*mygo.Window) bool { return full }
	t.Cleanup(func() { windowFullScreen = oldFS })

	oldMax := windowMaximized
	windowMaximized = func(*mygo.Window) bool { return false }
	t.Cleanup(func() { windowMaximized = oldMax })

	oldSet := windowSetFullScreen
	windowSetFullScreen = func(_ *mygo.Window, on bool) { full = on }
	t.Cleanup(func() { windowSetFullScreen = oldSet })

	app, tt = home(t)
	app.Win = &mygo.Window{}
	t.Cleanup(func() { app.Win = nil })
	tt.Frame()
	inPlayer(t, app, tt)
	return app, tt, &full
}

func TestFeatureEscLeavesFullscreenBeforeTheRoom(t *testing.T) {
	app, tt, full := sysFullApp(t)

	// 进系统全屏。
	*full = true
	tt.Frame()
	mustFind(t, tt, "退出全屏")

	// 第一下 Esc：只退全屏，人还在直播间。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if *full {
		t.Fatal("Esc 没有退出系统全屏")
	}
	mustFind(t, tt, "播放画面（双击全屏）")
	if _, ok := tt.Find("直播间:旭旭宝宝"); ok {
		t.Fatal("第一下 Esc 就把人踢回列表了：全屏状态没先退")
	}
	_ = app

	// 防抖窗口内再按一下：忽略。它是给「系统已经把全屏吃掉、这里又收到一次
	// Esc」留的余量，不是给用户连按的。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	mustFind(t, tt, "播放画面（双击全屏）")

	// 等过防抖再按：这才回列表。
	time.Sleep(600 * time.Millisecond)
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	mustNotFind(t, tt, "播放画面（双击全屏）")
}

// 系统全屏时画面右上角要有窗口按钮：应用自己的标题栏被画面盖住了，
// 最小化 / 最大化 / 关闭得有地方可去。
func TestFeatureFullscreenShowsWindowButtons(t *testing.T) {
	_, tt, full := sysFullApp(t)

	if _, ok := tt.Find("关闭窗口"); ok {
		// 非全屏时它们也在顶栏里；这里只是确认这个标签存在，避免误判。
		t.Log("顶栏的关闭窗口在")
	}

	*full = true
	tt.Frame()

	mustFind(t, tt, "播放画面（双击全屏）")
	// 三个按钮都在（标签和顶栏的重名，所以数一下出现次数）。
	n := 0
	for _, s := range tt.Texts() {
		switch s {
		case "最小化", "最大化", "关闭窗口":
			n++
		}
	}
	if n < 3 {
		t.Fatalf("系统全屏下只找到 %d 个窗口按钮（最小化/最大化/关闭），画面被盖住就没地方点了", n)
	}
}
