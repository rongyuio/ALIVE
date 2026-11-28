package ui

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 播放页的浮层只有一条显隐规则（syncChrome）：指针在窗口里停住就过几秒
// 一起让位，指针离开窗口立刻让位，一动就全回来。
//
// 这条规则以前有三份不同的实现：窗口模式下顶栏常驻、控制条自己按舞台坐标
// 计时、四个面板写死在「开着就不许隐藏」里。所以这里断言的是「一起」——
// 拿顶栏的返回钮和控制条上的按钮各取一颗，一帧里它们必须在同一档。

// endlessDecoder 一直在出帧：播放页的伪装，不联网也不需要真窗口。
//
// 和 player_swap_test.go 的 oneFrameDecoder 只差「不 EOF」——那边验换台，
// 出完一帧就结束正合适；这边要验「画面在走时浮层才让位」，画面不能停。
type endlessDecoder struct{}

func (d *endlessDecoder) Next() (ui.VideoFrame, error) {
	return ui.VideoFrame{
		Image:    image.NewRGBA(image.Rect(0, 0, 16, 9)),
		Duration: 100 * time.Millisecond,
	}, nil
}

func (d *endlessDecoder) Rewind() error { return nil }

// newPlayingPlayerApp 造一屏「正在播的播放页」。
//
// 焦点这一间直接挂一个假解码器的播放器：ensureFocusMedia 看到 player 非空
// 就不再取流，stageStateNow 也因此判成 stageLive——浮层才有资格让位。
func newPlayingPlayerApp(t *testing.T, playing bool) *App {
	t.Helper()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	s := &roomSession{key: watchKey(room), room: room, playing: playing, firstFrame: true}
	p, err := ui.NewVideoPlayer(&endlessDecoder{})
	if err != nil {
		t.Fatalf("造播放器失败：%v", err)
	}
	t.Cleanup(p.Close)
	s.attachPlayer(p)
	a.focus = s
	a.playerReturnRoute, a.playerReturnSection = routeHome, 0
	a.Router.Push(routePlayer)
	return a
}

// chromeProbes 是两颗分属不同浮层的按钮：顶栏一颗、控制条一颗。
// 一条规则管全部，就要能看到它们同进同退。
//
// 按无障碍名找，不按角色：这些自绘 Box 是可点但不可聚焦的，
// MyGo 给这种元素的角色是分组而不是按钮（third_party/mygo/ui/access.go
// 的 accessRole：RoleButton 要求 clickable && focusable）。
var chromeProbes = []string{"退出直播间", "重新加载"}

func assertChromeShown(t *testing.T, tt *ui.Tester, when string) {
	t.Helper()
	for _, name := range chromeProbes {
		if _, ok := tt.Access(name); !ok {
			t.Errorf("%s：%q 不在无障碍树里，浮层没回来", when, name)
		}
	}
}

func assertChromeGone(t *testing.T, tt *ui.Tester, a *App, when string) {
	t.Helper()
	for _, name := range chromeProbes {
		if _, ok := tt.Access(name); ok {
			t.Errorf("%s：%q 还在无障碍树里，浮层没让位", when, name)
		}
	}
	// 让位之后控件不该再吃点击：看不见的返回钮要是还接得住一下，
	// 人就会在「点画面」的时候莫名其妙退出直播间。
	if err := tt.Click(chromeProbes[0]); err == nil {
		t.Errorf("%s：看不见的顶栏还在吃点击", when)
	}
	if a.Router.Path() != routePlayer {
		t.Fatalf("%s：点了看不见的返回钮之后离开了播放页", when)
	}
}

// 指针在窗口里停住：过几秒，顶栏和控制条一起让位；动一下就全回来。
func TestPlayerChromeHidesWhenPointerRests(t *testing.T) {
	a := newPlayingPlayerApp(t, true)
	tt := ui.NewTester(a.Shell, 1280, 800)

	// 进直播间那一下指针本来就在窗口里。
	tt.Move(640, 420)
	tt.Frame()
	tt.Frame()
	assertChromeShown(t, tt, "指针刚进窗口")

	// 停住不动：到点让位，而且是本帧立刻反映到无障碍树和命中上。
	tt.Advance(chromeIdleAfter + 100*time.Millisecond)
	tt.Frame()
	assertChromeGone(t, tt, a, "指针停住之后")

	// 动一下：全部回来，不用再等。
	tt.Move(700, 420)
	tt.Frame()
	tt.Frame()
	assertChromeShown(t, tt, "指针再动一下")
}

// 指针离开窗口：立刻让位，不等那几秒。
//
// Tester 没有单独的 PointerLeave；FingerUp 末尾会发一条
// （third_party/mygo/ui/headless.go 的 FingerUp），鼠标离开窗口在 Windows
// 后端上走的正是同一个事件（internal/windows/surface.go 的 wmMouseLeave）。
// 这次「抬手」没有对应的按下，不构成点击。
func TestPlayerChromeHidesWhenPointerLeavesWindow(t *testing.T) {
	a := newPlayingPlayerApp(t, true)
	tt := ui.NewTester(a.Shell, 1280, 800)

	tt.Move(640, 420)
	tt.Frame()
	tt.Frame()
	assertChromeShown(t, tt, "指针在窗口里")

	tt.FingerUp(640, 420)
	tt.Frame()
	tt.Frame()
	assertChromeGone(t, tt, a, "指针离开窗口")

	// 回到窗口里：立刻亮，不用等计时。
	tt.Move(640, 420)
	tt.Frame()
	tt.Frame()
	assertChromeShown(t, tt, "指针回到窗口")
}

// 暂停时浮层不让位：那是「等人操作」的状态，按钮不能自己跑掉。
func TestPlayerChromeStaysWhilePaused(t *testing.T) {
	a := newPlayingPlayerApp(t, false)
	tt := ui.NewTester(a.Shell, 1280, 800)

	tt.Move(640, 420)
	tt.Frame()
	tt.Advance(chromeIdleAfter * 2)
	tt.Frame()
	if a.chrome.Hidden {
		t.Fatal("暂停时浮层还是让位了：那是一屏等着人点按钮的画面")
	}
	assertChromeShown(t, tt, "暂停")

	// 但指针离开窗口照样立刻让位：人不在这一屏了，这一条排在「暂停常显」前面。
	tt.FingerUp(640, 420)
	tt.Frame()
	tt.Frame()
	assertChromeGone(t, tt, a, "暂停 + 指针离开窗口")
}

// 窗口全屏下两块浮层必须一起过渡，两个方向都算。
//
// 这条守的是一个很隐蔽的坑：MyGo 的元素身份按位置派生
// （third_party/mygo/ui/context.go:247），而画面顶上那块拖窗区只有窗口
// 全屏才建，还正好排在顶栏之前。让位那一帧它被撤掉 → 顶栏身份挪一位 →
// AnimateTo 的通道新建 → 新通道从目标值开始，于是顶栏一帧到位，而底部
// 控制条接住了顶栏那条旧通道，动得刚刚好。回来那一帧反过来：底部一帧
// 到位。窗口模式没这块拖窗区，所以只有全屏露出马脚。
func TestPlayerChromeFadesTogetherInWindowFullscreen(t *testing.T) {
	a := newPlayingPlayerApp(t, true)
	a.WinFull = true
	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Move(640, 420)
	tt.Frame()
	tt.Frame()

	back0, okB := tt.Find("退出直播间")
	play0, okP := tt.Find("暂停")
	if !okB || !okP {
		t.Fatal("前提不成立：顶栏或控制条不在画面上")
	}

	// 让位：顶栏往上 10px、控制条往下 10px，两边都得走过中间态。
	tt.Advance(chromeIdleAfter - 60*time.Millisecond)
	tt.Frame()
	hideBack, hidePlay := chromeTravel(t, tt, 18)
	if n := midSamples(hideBack, back0.Y, back0.Y-10); n < 3 {
		t.Errorf("让位时顶栏只有 %d 帧中间态（共 %d 帧采样 %.1f→%.1f）：一帧到位",
			n, len(hideBack), back0.Y, back0.Y-10)
	}
	if n := midSamples(hidePlay, play0.Y, play0.Y+10); n < 3 {
		t.Errorf("让位时控制条只有 %d 帧中间态（共 %d 帧采样 %.1f→%.1f）：一帧到位",
			n, len(hidePlay), play0.Y, play0.Y+10)
	}

	// 回来：动了指针之后同样要过渡，不是啪一下全亮。
	tt.Move(700, 420)
	tt.Frame()
	showBack, showPlay := chromeTravel(t, tt, 18)
	if n := midSamples(showBack, back0.Y-10, back0.Y); n < 3 {
		t.Errorf("回来时顶栏只有 %d 帧中间态（共 %d 帧）：一帧到位", n, len(showBack))
	}
	if n := midSamples(showPlay, play0.Y+10, play0.Y); n < 3 {
		t.Errorf("回来时控制条只有 %d 帧中间态（共 %d 帧）：一帧到位", n, len(showPlay))
	}
}

// chromeTravel 逐帧量顶栏返回钮和控制条播放键的 y，各 20ms 一帧。
func chromeTravel(t *testing.T, tt *ui.Tester, frames int) (back, play []float32) {
	t.Helper()
	for i := 0; i < frames; i++ {
		tt.Advance(20 * time.Millisecond)
		tt.Frame()
		b, okB := tt.Find("退出直播间")
		p, okP := tt.Find("暂停")
		if !okB || !okP {
			t.Fatalf("第 %d 帧找不到顶栏或控制条", i)
		}
		back = append(back, b.Y)
		play = append(play, p.Y)
	}
	return back, play
}

// midSamples 数 series 里严格落在 a 与 b 之间的采样点数：一帧到位时是 0。
func midSamples(series []float32, a, b float32) int {
	lo, hi := min(a, b), max(a, b)
	n := 0
	for _, v := range series {
		if v > lo+0.01 && v < hi-0.01 {
			n++
		}
	}
	return n
}
