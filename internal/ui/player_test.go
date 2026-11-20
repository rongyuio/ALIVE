package ui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
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

// 播放页外壳不描边，圆角照旧。
//
// 外壳曾经是全项目唯一一个静态表面带 1px 描边的例外：那圈线参与圆角裁剪
// （ClipInset 会把描边一起裁掉，四个角的弧线断成空白），于是用 Clip() 绕过，
// 并在 container_test.go 里挂了 allow。边框删掉之后接缝这个前提消失了，
// 约束只剩一条——舞台仍然要按 16 的圆角裁开。
//
// 两条都靠像素断言：
//   - 直边处从地往里的第一个像素就是画面（纯黑），中间不许有一层过渡色；
//     以前那圈 theme.Border 会在这里被第一个抓到。
//   - 左上角逐行找画面入点，越往下越靠左且不能有断行；角要是被切方了，
//     首行入点会直接贴到左边。
func TestPlayerShellEdgeHasNoStroke(t *testing.T) {
	const radius = 16
	sum := func(img *image.RGBA, x, y int) int {
		p := img.RGBAAt(x, y)
		return int(p.R) + int(p.G) + int(p.B)
	}
	same := func(a, b color.RGBA) bool {
		return a.R == b.R && a.G == b.G && a.B == b.B
	}

	for _, tc := range []struct {
		name    string
		palette string
		mode    string
		scale   float32
	}{
		{"signal 夜 100%", "signal", themeModeDark, 1},
		{"signal 夜 200%", "signal", themeModeDark, 2},
		{"daylight 日 200%", "daylight", themeModeLight, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := NewMockApp()
			// 主题不能跟着本机 state.json 走。
			app.PaletteID = tc.palette
			app.ThemeMode = tc.mode
			tt := ui.NewTester(app.Shell, 1440, 900)
			tt.SetScale(tc.scale)
			if err := tt.Click("旭旭宝宝"); err != nil {
				t.Fatal(err)
			}
			// 播放页从右侧滑进来，动画走完再截图
			tt.Advance(1500 * time.Millisecond)
			img := tt.Image()

			ground := img.RGBAAt(0, img.Bounds().Dy()/2)
			midY := img.Bounds().Dy() / 2

			// 直边：地之后第一个像素必须是画面。任何过渡色都说明又画了一条线。
			left := -1
			for x := 0; x < img.Bounds().Dx(); x++ {
				if !same(img.RGBAAt(x, midY), ground) {
					left = x
					break
				}
			}
			if left < 0 {
				t.Fatal("找不到外壳的左边")
			}
			if got := sum(img, left, midY); got > 24 {
				t.Fatalf("外壳左边第一个像素 RGB 和是 %d，不是纯黑画面——描边又回来了", got)
			}

			// 弧线：在直边往里一个圆角的位置找顶边。
			probeX := left + int(radius*tc.scale)
			top := -1
			for y := 0; y < img.Bounds().Dy(); y++ {
				if !same(img.RGBAAt(probeX, y), ground) {
					top = y
					break
				}
			}
			if top < 0 {
				t.Fatal("找不到外壳的顶边")
			}

			// 圆角还在：顶边那一行，画面得从右边一个圆角处才开始。
			firstAtTop := -1
			for x := 0; x < img.Bounds().Dx(); x++ {
				if !same(img.RGBAAt(x, top), ground) {
					firstAtTop = x
					break
				}
			}
			if want := left + int(radius*tc.scale) - 2; firstAtTop < want {
				t.Fatalf("顶边第一行从 x=%d 就开始有画面（应在 %d 附近），圆角被切方了", firstAtTop, want)
			}

			// 弧线不许断：每行都要能扫到画面入点，且入点只往左走。
			prev := firstAtTop
			for y := top + 1; y < top+int(radius*tc.scale); y++ {
				edge := -1
				for x := 0; x < img.Bounds().Dx(); x++ {
					if !same(img.RGBAAt(x, y), ground) {
						edge = x
						break
					}
				}
				if edge < 0 {
					t.Fatalf("第 %d 行（顶边下 %d px）整行都是地色，弧线断了", y, y-top)
				}
				if edge > prev {
					t.Fatalf("第 %d 行画面入点 x=%d 比上一行 %d 还靠右，弧线不是单调的圆角", y, edge, prev)
				}
				prev = edge
			}
		})
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
