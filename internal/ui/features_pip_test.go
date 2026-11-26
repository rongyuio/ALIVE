package ui

// 小窗：主窗口藏起来，画面还在走。
//
// 这一条从没被断言过。原因不是它不重要，而是旧代码把 `mygo.NewWindow` 直接
// 写死在 togglePip 里——`ui.Tester` 里那个调用会一直等平台就绪，测试挂到
// 3 分钟超时。于是"一测就卡死"，于是没人测，于是没人知道小窗现在到底行不行。
//
// 现在窗口创建走一行间接（newMiniWindow），测试把它换成 nil：小窗开着、
// 没有真窗口，剩下的行为全都能验。

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pipApp 造一个在小窗模式下、没有真窗口的 App。
func pipApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	old := newMiniWindow
	newMiniWindow = func(mygo.WindowOptions) *mygo.Window { return nil }
	t.Cleanup(func() { newMiniWindow = old })

	app, tt := home(t)
	inPlayer(t, app, tt)
	return app, tt
}

// 小窗接管画面：主窗口藏起来之后，播放状态仍然每帧被对齐。
//
// 判据是播放器自己的 Playing()，不是 a.focus.playing 这个字段。
//
// 为什么不能靠"点暂停"来验：小窗的按钮走 setFocusPlaying，它自己就会调
// applyFocusAudio 把意图推下去——把 MiniPlayer 里的 syncPlayer 整行删掉，
// 那条路照样绿。syncPlayer 管的是**没有用户操作时的漂移**：mpv 自己停了、
// 断流重连、播到结尾。所以这里先让播放器单方面停下，再看小窗的下一帧有没有
// 把它拉回来。
func TestFeaturePipKeepsThePictureMoving(t *testing.T) {
	app, tt := pipApp(t)

	// mock 的流没有任何档位，起播停在封面上，所以这里自己挂一个播放器。
	// 用的是同一批夹具（gifPlayer），不是另造一个假接口。
	p := gifPlayer(t)
	p.SetVolume(0)
	app.focus.player = p
	app.focus.firstFrame = true
	app.focus.playing = true
	app.syncPlayer()
	if !p.Playing() {
		t.Fatal("焦点说在播，播放器没在播")
	}

	mustClick(t, tt, "小窗播放")

	// 播放器单方面停下——模拟 mpv 自己停帧、断流后停住。
	p.Pause()
	if p.Playing() {
		t.Fatal("暂停没生效，这条测不到漂移")
	}

	// 小窗跑一帧：它是此刻唯一的视图，得把状态对齐回去。
	mini := ui.NewTester(app.MiniPlayer, miniW, miniH)
	mini.Frame()

	if !p.Playing() {
		t.Fatal("播放器自己停了，小窗的下一帧没把它拉回来：主窗口藏起来之后没人每帧对齐")
	}
}

// 小窗里的控制条：播放/暂停、关闭，两个都得能用。
// 小窗没有系统给的关闭键，这两个控件是它唯一的出口。
func TestFeaturePipControlsWork(t *testing.T) {
	app, tt := pipApp(t)
	mustClick(t, tt, "小窗播放")

	// 小窗的根视图在小窗自己的窗口里构建；这里直接建一个 Tester 挂上去，
	// 走的是同一条 MiniPlayer 代码路径。
	mini := ui.NewTester(app.MiniPlayer, miniW, miniH)
	mini.Frame()

	// 控制条常态淡出且不接输入，指针移上去才出来。
	if _, ok := mini.Find("暂停"); !ok {
		t.Fatal("小窗控制条里没有播放/暂停")
	}
	mini.Move(miniW/2, miniH-26)
	mini.Frame()
	mini.Advance(400e6)
	mini.Frame()

	before := app.focusPlaying()
	label := "暂停"
	if !before {
		label = "播放"
	}
	if err := mini.Click(label); err != nil {
		t.Fatalf("点不到小窗的 %q：%v", label, err)
	}
	mini.Frame()
	if app.focusPlaying() == before {
		t.Fatal("点了小窗的播放/暂停，播放状态没变")
	}

	// 关闭小窗：主界面回来。
	if err := mini.Click("关闭小窗"); err != nil {
		t.Fatalf("点不到关闭小窗：%v", err)
	}
	mini.Frame()
	if app.miniOpen() {
		t.Fatal("点了关闭小窗，小窗还开着")
	}
}

// 再点一次「小窗播放」也要能关：开关是同一个按钮。
func TestFeaturePipToggles(t *testing.T) {
	app, tt := pipApp(t)

	mustClick(t, tt, "小窗播放")
	if !app.miniOpen() {
		t.Fatal("第一次点没开")
	}
	// 主窗口收起来了，所以第二次点在播放页上——播放页此刻可能不再构建，
	// 直接调同一个入口，验的是"再点一次会关"这条语义。
	app.togglePip()
	if app.miniOpen() {
		t.Fatal("再点一次没关")
	}
}
