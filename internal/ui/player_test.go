package ui

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/theme"
)

// 控制条的三档尺寸：
// 常态间距 12、滑条 96，≤1100 间距收 8、滑条 90，≤980 滑条 72 且百分比让位。
// 间距只有一档：弹幕组曾经比别的控件紧 2px（10 对 12），整条控制条的节奏
// 就多出一个看不出来但能感觉到的断点，现在全条统一。
// 这几档是纯数值，用 tester 直接读，不依赖真实窗口。
func TestControlMetrics(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want controlMetrics
	}{
		{"宽屏", 1440, 900, controlMetrics{leftGap: 12, rightGap: 12, volumeW: 96, showPercent: true}},
		{"1101 仍是宽屏", 1101, 900, controlMetrics{leftGap: 12, rightGap: 12, volumeW: 96, showPercent: true}},
		{"1100 收到第一档", 1100, 900, controlMetrics{leftGap: 8, rightGap: 8, volumeW: 90, showPercent: true}},
		{"981 仍是第一档", 981, 900, controlMetrics{leftGap: 8, rightGap: 8, volumeW: 90, showPercent: true}},
		{"980 收到第二档", 980, 900, controlMetrics{leftGap: 8, rightGap: 8, volumeW: 72, showPercent: false}},
		{"最小窗口", 900, 600, controlMetrics{leftGap: 8, rightGap: 8, volumeW: 72, showPercent: false}},
	}

	for _, tc := range cases {
		var got controlMetrics
		ui.NewTester(func(c *ui.Context) {
			got = metricsFor(c)
			ui.Box(c).Fill()
		}, tc.w, tc.h)
		if got != tc.want {
			t.Errorf("%s（%d 宽）：得到 %+v，期望 %+v", tc.name, tc.w, got, tc.want)
		}
	}
}

// 复现 playerDropdown 的悬停路径：确认 MyGo 里「指针移到 Row 上」真的
// 会让 Hovered() 在重建帧里为 true。
func TestHoverOpensDropdown(t *testing.T) {
	var open bool
	var h hoverClose
	tt := ui.NewTester(func(c *ui.Context) {
		btn := ui.Row(c).Height(30).Padding(0, 6, 0, 6).Gap(6).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("清晰度")
		h.hover(c, btn.Hovered(), &open)
		btn.Children(func() {
			ui.Text(c, "原画").FontSize(12)
		})
		ui.PopoverBase(c, btn, &open, func(panel ui.Element) {
			panel.Width(130).Padding(10).AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
			h.hover(c, panel.Hovered(), &open)
			panel.Children(func() {
				ui.Text(c, "蓝光").FontSize(13)
			})
		})
	}, 600, 400)

	r, ok := tt.Find("清晰度")
	if !ok {
		t.Fatal("找不到触发条")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	if !open {
		t.Fatalf("悬停没有打开下拉（bounds=%+v）", r)
	}
	tt.Advance(600 * time.Millisecond)
	if !open {
		t.Fatal("悬停中被关掉了")
	}
	tt.Move(500, 380)
	tt.Advance(600 * time.Millisecond)
	// 关门的判定发生在「到点后的第一帧」里；真实窗口因为弹幕一直在跑
	// 帧，测试得自己推一帧。
	tt.Frame()
	if open {
		t.Fatal("离开后没有关闭")
	}
}

// 双击画面切窗口全屏：
// 画面空白处生效，控制条上的按钮不参与——连点一颗按钮不该把画面顶成全屏。
func TestDoubleClickStageTogglesWindowFullscreen(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if app.Router.Path() != routePlayer {
		t.Fatalf("没有进入播放页，路由为 %q", app.Router.Path())
	}
	// 播放页是从右侧滑进来的，动画没走完之前整页都还在窗口外
	tt.Advance(1500 * time.Millisecond)

	doubleClick := func(x, y float32) {
		for range 2 {
			tt.Press(x, y)
			tt.Release(x, y)
		}
	}

	r, ok := tt.Find("重新加载")
	if !ok {
		t.Fatal("控制条没有渲染出来")
	}
	doubleClick(r.X+r.W/2, r.Y+r.H/2)
	if app.WinFull {
		t.Fatal("双击控制条按钮误切了窗口全屏")
	}

	// 画面中央没有任何控件，命中链最里层就是 stage
	doubleClick(700, 450)
	if !app.WinFull {
		t.Fatal("双击画面没有进入窗口全屏")
	}

	doubleClick(700, 450)
	if app.WinFull {
		t.Fatal("再次双击画面没有退出窗口全屏")
	}
}

// 两种全屏下灵动岛整块不画：画面上只留返回钮和控制条，岛连同它的流光
// 和投影压满屏画面上纯属干扰。退出全屏要回来。
func TestFullscreenHidesIsland(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Move(700, 450)
	tt.Advance(5 * time.Second)

	// 进场会自动展开，先让它闲置收起，确认窗口模式下岛确实在
	if _, ok := tt.Find("灵动岛"); !ok {
		t.Fatal("窗口模式下灵动岛应该还在")
	}

	app.WinFull = true
	tt.Frame()
	if _, ok := tt.Find("灵动岛"); ok {
		t.Fatal("窗口全屏下灵动岛还在渲染")
	}

	app.WinFull = false
	tt.Frame()
	if _, ok := tt.Find("灵动岛"); !ok {
		t.Fatal("退出窗口全屏后灵动岛没有回来")
	}
}

// 悬浮 chrome 的自动隐藏：指针静止 2.8 秒后
// 淡出，一动就回来；暂停时一律常显。
func TestChromeAutoHidesWhenIdle(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(1500 * time.Millisecond)

	// 指针停在画面中间：不在顶栏、也不在控制条那两条带上
	tt.Move(700, 450)
	tt.Advance(3 * time.Second)
	if !app.ChromeHidden {
		t.Fatal("指针静止 2.8 秒后 chrome 没有淡出")
	}

	tt.Move(704, 454)
	if app.ChromeHidden {
		t.Fatal("指针一动 chrome 没有回来")
	}

	// 暂停时不隐藏：那会儿人往往离开鼠标去干别的
	app.Playing = false
	tt.Advance(3 * time.Second)
	if app.ChromeHidden {
		t.Fatal("暂停时 chrome 不该淡出")
	}
}

// 进房间默认播第一档：菜单按 qn 降序，第一档就是能拿到的最高档；
// 按标签选档位时命中对应项。
func TestPickVariantDefaultsToTop(t *testing.T) {
	variants := []live.Variant{
		{ID: "source", Label: "1080P 原画"},
		{ID: "high", Label: "720P 超清"},
	}
	if got := defaultVariantLabel(variants); got != "1080P 原画" {
		t.Errorf("默认档位应是最高的第一档，得到 %q", got)
	}
	if v := pickVariant(variants, "720P 超清"); v.ID != "high" {
		t.Errorf("按标签选档位应命中 high，得到 %q", v.ID)
	}
}

// 播放页顶栏高亮跟着主播所属平台走，而不是进播放页之前那个列表的平台。
//
// 从订阅页点开别家主播最容易撞上：a.Platform 还是订阅页原来那个平台的，
// 顶栏却应该显示主播自己的平台。
func TestPlayerTabFollowsStreamerPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "douyin"
	app.Player = Streamer{Platform: "bilibili", RoomID: "170001", Name: "某主播"}
	app.Router.Push(routePlayer)

	if got := app.activeTab(); got != "bilibili" {
		t.Fatalf("顶栏高亮的是 %q，期望 %q", got, "bilibili")
	}
}

// 没有主播上下文时回落到当前平台，不能把顶栏高亮清空。
func TestPlayerTabFallsBackToPlatform(t *testing.T) {
	app := NewMockApp()
	app.Platform = "huya"
	app.Player = Streamer{}
	app.PlayerIndex = -1
	app.Router.Push(routePlayer)

	if got := app.activeTab(); got != "huya" {
		t.Fatalf("顶栏高亮的是 %q，期望 %q", got, "huya")
	}
}

// 悬停在音量条上滚轮调音量：上滚变大、下滚变小，1% 取整，夹在 0–100。
//
// 滚轮走的是 HandleInput，只有指针在音量条上时才收得到——所以「悬停才生效」
// 是结构上的，不是额外判断出来的。这条测试同时盯住「指针在画面别处滚不动音量」，
// 免得以后把 handler 挂到整条控制条或 stage 上。
func TestWheelOverVolumeChangesVolume(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(1500 * time.Millisecond)

	app.Volume = 0.5
	tt.Frame()
	r, ok := tt.Find("音量")
	if !ok {
		t.Fatal("控制条里没有音量条")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2

	// 指针在画面上、不在音量条上：滚轮不碰音量
	tt.Move(700, 450)
	tt.Wheel(700, 450, 0, -99)
	tt.Frame()
	if app.Volume != 0.5 {
		t.Fatalf("画面上的滚轮改了音量：%v", app.Volume)
	}

	tt.Move(x, y)
	tt.Wheel(x, y, 0, -99) // 一格上滚
	tt.Frame()
	if got := app.Volume; got != 0.55 {
		t.Fatalf("上滚一格后音量为 %v，期望 0.55", got)
	}

	tt.Wheel(x, y, 0, 99) // 一格下滚
	tt.Frame()
	if got := app.Volume; got != 0.5 {
		t.Fatalf("下滚一格后音量为 %v，期望 0.5", got)
	}

	// 上限夹到 1：连续上滚不会溢出
	for range 20 {
		tt.Wheel(x, y, 0, -99)
	}
	tt.Frame()
	if got := app.Volume; got != 1 {
		t.Fatalf("连续上滚后音量为 %v，期望夹到 1", got)
	}

	// 下限夹到 0：连续下滚不会掉到负数
	for range 40 {
		tt.Wheel(x, y, 0, 99)
	}
	tt.Frame()
	if got := app.Volume; got != 0 {
		t.Fatalf("连续下滚后音量为 %v，期望夹到 0", got)
	}
}

// 触控板一次只推几 DIP：不足 1% 的余量要攒着，不能每次取整都丢掉。
// 没有余量的话这串小位移会把音量锁死在原值上——测试在改前取整版本里会红。
func TestWheelVolumeAccumulatesPreciseScroll(t *testing.T) {
	app := NewMockApp()
	app.Router.Push(routePlayer)
	app.Volume = 0.5
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Advance(1500 * time.Millisecond)

	r, ok := tt.Find("音量")
	if !ok {
		t.Fatal("控制条里没有音量条")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Move(x, y)
	// 每格 5 DIP = 0.25%：单次都到不了一档，四次攒成 1%
	for range 4 {
		tt.Scroll(x, y, 0, -5)
	}
	tt.Frame()
	if got := app.Volume; got != 0.51 {
		t.Fatalf("四次 5 DIP 上滚后音量为 %v，期望 0.51", got)
	}
}

// 播放页外壳的圆角描边必须连着。
//
// 外壳是「圆角 16 + 1px 描边」的大容器，以前用 ClipInset(0,0,0,0,16) 切内容。
// ClipInset 是 CSS 的 clip-path: inset()，它连边框一起裁，而且裁剪的圆角
// 正好落在描边中线上：直边处像素中心落在裁剪内、描边完整，弧线上覆盖度被
// 乘了两次，只剩四分之一，肉眼就是「四个角的线条没了」。
// Clip() 才是 overflow:hidden：裁 padding box（圆角自动减掉边框宽），
// 描边在裁剪之后补画一遍，弧线完整。
//
// 断言方式：沿左上角弧线逐行取最亮的像素，每行都得亮过阈值。直边不受影响，
// 所以只查弧线覆盖的那一段——那里整段暗下去才是这个 bug。
func TestPlayerShellCornerBorderIsContinuous(t *testing.T) {
	const radius = 16
	// theme.Border #24272e 的 RGB 和是 121，窗口底 #0a0b0d 是 34，画面纯黑是 0。
	// 弧线是斜的，1px 宽在像素上覆盖不满，45° 处峰值掉到 89；而被裁坏的
	// 弧线整段只剩 27。取 60 分得开。
	const lit = 60
	sum := func(img *image.RGBA, x, y int) int {
		p := img.RGBAAt(x, y)
		return int(p.R) + int(p.G) + int(p.B)
	}

	for _, scale := range []float32{1, 2} {
		app := NewMockApp()
		// 下面按 signal 夜色的描边取值扫描，主题不能跟着本机 state.json 走。
		app.PaletteID = "signal"
		app.ThemeMode = themeModeDark
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.SetScale(scale)
		if err := tt.Click("旭旭宝宝"); err != nil {
			t.Fatal(err)
		}
		// 播放页从右侧滑进来，动画走完再截图
		tt.Advance(1500 * time.Millisecond)
		img := tt.Image()

		// 描边的亮度区间跟着主题走，不写死：配色换了，这里不该跟着红。
		borderRow := int(theme.Border.R) + int(theme.Border.G) + int(theme.Border.B)
		lo, hi := borderRow-14, borderRow+14

		// 顶边：第一行横贯大半幅画面的描边
		top, left := -1, -1
		for y := 0; y < img.Bounds().Dy() && top < 0; y++ {
			n := 0
			for x := 0; x < img.Bounds().Dx(); x++ {
				if sum(img, x, y) >= lo && sum(img, x, y) <= hi {
					n++
				}
			}
			if n > img.Bounds().Dx()/2 {
				top = y
			}
		}
		if top < 0 {
			t.Fatalf("scale %v：找不到外壳的顶边", scale)
		}
		// 左边：顶边往下走满一个圆角之后，第一条竖着的描边
		for x := 0; x < img.Bounds().Dx(); x++ {
			if sum(img, x, top+int(radius*scale)+2) >= lo {
				left = x
				break
			}
		}
		if left < 0 {
			t.Fatalf("scale %v：找不到外壳的左边", scale)
		}

		// 逐行扫弧线：top 这一行是顶边本身，往下 radius 行都该有亮像素
		for y := top + 1; y < top+int(radius*scale); y++ {
			peak := 0
			for x := left; x < left+int(radius*scale) && x < img.Bounds().Dx(); x++ {
				peak = max(peak, sum(img, x, y))
			}
			if peak < lit {
				t.Fatalf("scale %v：外壳左上角第 %d 行（角顶下 %d px）最亮只有 %d，圆角弧线断了",
					scale, y, y-top, peak)
			}
		}
	}
}

// iconInk 量一颗控制条按钮里亮像素的上下边（相对热区中心，单位 DIP）。
func iconInk(t *testing.T, tt *ui.Tester, img *image.RGBA, name string) (top, bottom float32) {
	t.Helper()
	r, ok := tt.Find(name)
	if !ok {
		t.Fatalf("找不到「%s」", name)
	}
	top, bottom = 99, -99
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			c := img.RGBAAt(x, y)
			if int(c.R)+int(c.G)+int(c.B) <= 120 {
				continue
			}
			dy := float32(y) - (r.Y + r.H/2)
			if dy < top {
				top = dy
			}
			if dy > bottom {
				bottom = dy
			}
		}
	}
	if bottom < top {
		t.Fatalf("「%s」没有墨迹：采样坐标偏了", name)
	}
	return top, bottom
}

// 控制条上的图标必须在同一条上边、同一条下边上——「一样大」在视觉上就是这件事。
//
// lucide 各个字形的实际墨迹高度不一样：同样 24px，暂停、刷新是 20px，喇叭
// 和小窗只有 18px。同一字号排在一条线上，上下边缘就参差 1-3px。所以字号按
// 墨迹反推（见 playerIconSize / playerIconSizeTall），这条把结果钉住。
func TestPlayerIconsShareInkBand(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	app.DanmuOn = true
	// 音量决定静音键的 Label（0 时是「取消静音」），而这条量的是图标墨迹：
	// 钉住音量，测试才不会跟着本机 state.json 变。
	app.Volume = 0.6
	tt := ui.NewTester(app.Shell, 1440, 900)
	// 控制条现在是玻璃：下面有画面时，亮像素会从背景透进来，量到的墨迹
	// 就不再只是图标。这条量的是图标几何，不是材质——把透明关掉再量。
	tt.SetPreferences(ui.Preferences{ReduceTransparency: true})
	img := tt.Image()

	const (
		wantTop    = -10
		wantBottom = 10
		slack      = 1
	)
	for _, n := range []string{"暂停", "重新加载", "静音", "关闭弹幕", "弹幕设置", "弹幕屏蔽", "小窗播放", "窗口全屏", "全屏"} {
		top, bottom := iconInk(t, tt, img, n)
		if top < wantTop-slack || top > wantTop+slack {
			t.Errorf("「%s」墨迹上边 %.1f，期望 %d±%d：和同排图标不在同一条上边", n, top, wantTop, slack)
		}
		if bottom < wantBottom-slack || bottom > wantBottom+slack {
			t.Errorf("「%s」墨迹下边 %.1f，期望 %d±%d：和同排图标不在同一条下边", n, bottom, wantBottom, slack)
		}
	}
}
