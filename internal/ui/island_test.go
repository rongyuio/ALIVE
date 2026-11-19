package ui

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 灵动岛在首页只报状态：点它不改路由、也不弹任何东西。
func TestIslandClickDoesNothing(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	before := app.Router.Path()
	if err := tt.Click("灵动岛"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if got := app.Router.Path(); got != before {
		t.Fatalf("点灵动岛把路由切到了 %s", got)
	}
}

// 播放页的岛接的是主播信息：进直播间展开，展开态常驻——它同时承担
// 「正在看谁」和「声音从哪来」两件事，自己缩回去等于把状态藏起来。
func TestPlayerIslandStaysExpanded(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	if !app.InfoOpen {
		t.Fatal("进直播间没有展开信息岛")
	}

	// 指针移开、闲置远超原来那 4 秒：仍然展开。
	tt.Move(700, 450)
	tt.Advance(10 * time.Second)
	if !app.InfoOpen {
		t.Fatal("闲置 10 秒之后信息岛收起了：展开态应当常驻")
	}

	// 宽度也必须是展开态，不是收起那枚圆。
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	if b.W < islandPlayerMinW-1 {
		t.Fatalf("岛宽 %v，比最小展开宽 %v 还窄", b.W, islandPlayerMinW)
	}
}

// islandWidth 按给定的主播与窗口宽度渲染一帧播放页，返回灵动岛的宽度。
//
// 状态在建 Tester 之前就摆好：岛的宽度是 AnimateTo 从目标值起步的，
// 先摆状态再建视图，拿到的是终值本身，不必再推进时钟等弹簧停下。
func islandWidth(t *testing.T, s Streamer, winW int) float32 {
	t.Helper()
	app := NewMockApp()
	app.Player = s
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, winW, 900)
	tt.Frame()
	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	return b.W
}

// 直播间标题有长有短：岛宽跟着内容走，短标题收窄，长标题撑开。
//
// 固定的 350 在这个场景里会同时给两种标题一样宽——短标题右边空一大截，
// 长标题又要靠省略号硬裁。宽度必须是量出来的，不是写死的。
func TestPlayerIslandWidthFollowsContent(t *testing.T) {
	base := Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	short := base
	short.Title = "短"
	long := base
	long.Title = "这是一个明显更长的直播间标题"

	shortW := islandWidth(t, short, 1440)
	longW := islandWidth(t, long, 1440)
	if longW <= shortW {
		t.Fatalf("长标题岛宽 %v、短标题 %v：宽度没有跟着内容走", longW, shortW)
	}
	if shortW >= islandPlayerMaxW-1 {
		t.Fatalf("短标题岛宽 %v 顶到了上限 %v：没有按内容收窄", shortW, islandPlayerMaxW)
	}
}

// 内容再短也不能缩成一颗胶囊，再长也不能把顶栏两端盖掉。
func TestPlayerIslandWidthClamped(t *testing.T) {
	tiny := Streamer{Platform: "douyu", RoomID: "1", Name: "甲"}
	if w := islandWidth(t, tiny, 1440); w < islandPlayerMinW-0.5 {
		t.Fatalf("极短内容的岛宽 %v 小于下限 %v", w, islandPlayerMinW)
	}

	huge := Streamer{
		Platform: "douyu",
		RoomID:   "1",
		Name:     "甲",
		Title:    strings.Repeat("很长很长的直播间标题", 20),
	}
	if w := islandWidth(t, huge, 1440); w > islandPlayerMaxW+0.5 {
		t.Fatalf("超长内容的岛宽 %v 超过上限 %v", w, islandPlayerMaxW)
	}
	if w := islandWidth(t, huge, 600); w > float32(600)*0.5+0.5 {
		t.Fatalf("窄窗口里岛宽 %v 超过了窗口一半", w)
	}
}

// 后台收听态：离开播放页之后岛缩成一枚圆形头像胶囊，点它回到那个直播间。
//
// 这是「不点叉就离开」之后唯一的路——声音还在放，但界面上得有东西说明
// 它在放、并且点得回去。
func TestListeningIslandReturnsToPlayer(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	if err := tt.Click("旭旭宝宝"); err != nil {
		t.Fatal(err)
	}
	// 切到关注页：这是「离开但不退出」的一条路（顶栏 tab）
	if err := tt.Click("关注"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if app.Router.Path() == routePlayer {
		t.Fatal("点了关注页还停在播放页")
	}
	// 没有真播放器时后台收听不成立（media.player 为 nil），
	// 所以这里验的是「岛按后台收听态渲染」的那条分支本身。
	if !app.media.background {
		t.Skip("没有真实播放器，后台收听态起不来")
	}

	b, ok := tt.Find("回到直播间")
	if !ok {
		t.Fatal("后台收听态没有画出回到直播间的入口")
	}
	if d := b.W - b.H; d < -0.1 || d > 0.1 {
		t.Fatalf("后台收听胶囊 %vx%v，不是正圆", b.W, b.H)
	}
	if err := tt.Click("回到直播间"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := app.Router.Path(); got != routePlayer {
		t.Fatalf("点胶囊之后路由是 %s，期望 %s", got, routePlayer)
	}
}

// islandAvatar 是岛里那颗头像的名字。
//
// 它必须是头像自己那一格，不能是外面铺满内容区的热区：热区的宽度恒等于
// 内容区，量它看不出头像到底在哪、多大。旧实现把 34 的圆当热区、头像没有
// 名字，所以这些用例在旧代码上会以「找不到主播头像」失败——那正是要钉的
// 东西：头像必须是一个独立元素，位置和直径由岛宽插值出来。
const islandAvatarLabel = "主播头像"

// 离开播放页时，头像必须是被岛宽拖着走过去的，不能先跳到中间。
//
// 以前后台收听另画一颗居中头像：切到关注页的第一帧，头像就在正中了，岛
// 还在 180 宽的位置慢慢缩——看起来是「闪现」而不是「收起」。
//
// 判据是**连续性**：逐帧位移不许出现一次几十像素的跳变（弹簧回弹最多十几
// 像素），并且收尾必须正好居中、直径正好填满内容区。
func TestListeningAvatarCollapsesWithIsland(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.media.background = true
	app.Router.Push(routeHome)
	tt.Frame()

	// 一帧 16ms、行程约 70px，弹簧峰值一帧十几像素；硬跳是几十像素。
	const maxStep = 16

	var prevOffset, prevW float32
	var moving int
	first := true
	for i := 0; i < 120; i++ {
		tt.Advance(16 * time.Millisecond)
		isl, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("第 %d 帧岛不见了", i)
		}
		av, ok := tt.Find(islandAvatarLabel)
		if !ok {
			t.Fatalf("第 %d 帧找不到头像：它必须是被岛宽插值出来的独立元素", i)
		}
		// 头像不许跑出岛外：那是布局基准算错了，不是动画。
		if av.X < isl.X-0.5 || av.X+av.W > isl.X+isl.W+0.5 {
			t.Fatalf("第 %d 帧头像 %v..%v 跑出了岛 %v..%v",
				i, av.X, av.X+av.W, isl.X, isl.X+isl.W)
		}
		offset := av.X - isl.X
		if !first {
			if d := offset - prevOffset; d > maxStep || d < -maxStep {
				t.Fatalf("第 %d 帧头像横移了 %v 像素（%v → %v）：一帧里跳过去的",
					i, d, prevOffset, offset)
			}
		}
		first = false
		if prevW > 0 && isl.W < prevW-0.01 {
			moving++
		}
		prevOffset, prevW = offset, isl.W
	}
	if moving == 0 {
		t.Fatal("岛宽一帧都没变：收缩动画根本没跑，这条测试没有分辨力")
	}
	isl, _ := tt.Find("灵动岛")
	av, _ := tt.Find(islandAvatarLabel)
	if d := av.W - islandListenAvatar; d < -0.01 || d > 0.01 {
		t.Fatalf("收尾头像直径 %v，期望填满内容区的 %v", av.W, islandListenAvatar)
	}
	if d := (av.X + av.W/2) - (isl.X + isl.W/2); d < -0.6 || d > 0.6 {
		t.Fatalf("收尾头像中心偏离岛中心 %v", d)
	}
}

// 切走的第一帧，头像必须还在展开态那个左位上——那正是以前「闪现」的那一帧。
//
// 判据用「第一帧的左边距」对「展开态的左边距」：两者必须一致。旧实现第一帧
// 就把头像摆到正中（或者根本没有这个元素），这条会失败。
func TestListeningFirstFrameStartsFromLeftAvatar(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	beforeIsl, _ := tt.Find("灵动岛")
	before, ok := tt.Find(islandAvatarLabel)
	if !ok {
		t.Fatal("播放页没有可命名的头像")
	}
	wantOffset := before.X - beforeIsl.X

	app.media.background = true
	app.Router.Push(routeHome)
	tt.Frame()

	isl, _ := tt.Find("灵动岛")
	av, ok := tt.Find(islandAvatarLabel)
	if !ok {
		t.Fatal("后台收听态找不到头像：头像被换成了另一棵树")
	}
	if isl.W < islandPlayerMinW {
		t.Fatalf("第一帧岛宽已经是 %v，没有可观察的收缩过程", isl.W)
	}
	if got := av.X - isl.X; got != wantOffset {
		t.Fatalf("切走第一帧头像左边距 %v，展开态是 %v：头像先跳了，再等岛缩",
			got, wantOffset)
	}
}

// 收缩收尾不许来回跳。
//
// 这里钉的是两件具体的事，都是量出来才发现的：
//
//   - 岛的左边缘必须取整。它按 (winW-w)/2 算，弹簧每帧只走零点几像素，
//     亚像素坐标一跨过半像素边界，整颗岛就平移一个物理像素，下一帧再跨
//     回来——头像跟着一来一回，就是肉眼看到的「抖一下」。头像自己取整
//     没有用，它在岛里是整数，岛在窗口里不是。
//   - 收缩用 Smooth 不用 Snappy。Snappy 收到 38 会过冲到 37.1，宽度一旦
//     小于 38 内容区就窄于头像，被 body 的圆角切掉一条边。
//
// 判据是「左边缘单调」+「宽度单调」，而不是像素变化量：整岛平移一整像素
// 时像素差本来就大（那是正确的位移），有分辨力的是它有没有回退。
func TestListeningShrinkDoesNotJitter(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	app.media.background = true
	app.Router.Push(routeHome)
	tt.Frame()

	var prevX, prevW float32
	var shrank, moved int
	for i := 0; i < 90; i++ {
		isl, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("第 %d 帧岛不见了", i)
		}
		// 左边缘取整：岛的位置必须是整数 DIP，否则亚像素会在半像素边界上来回。
		if isl.X != float32(math.Round(float64(isl.X))) {
			t.Fatalf("第 %d 帧岛左边距 %v 不是整数：亚像素会在取整边界上抖", i, isl.X)
		}
		if prevW > 0 {
			if isl.W > prevW+0.001 {
				t.Fatalf("第 %d 帧岛宽从 %v 回弹到 %v", i, prevW, isl.W)
			}
			if isl.W < prevW-0.001 {
				shrank++
			}
			if isl.X < prevX-0.001 {
				t.Fatalf("第 %d 帧岛左边缘从 %v 退回 %v", i, prevX, isl.X)
			}
			if isl.X > prevX+0.001 {
				moved++
			}
		}
		prevX, prevW = isl.X, isl.W
		tt.Advance(16 * time.Millisecond)
	}
	if shrank == 0 || moved == 0 {
		t.Fatalf("岛没有收缩（%d 帧变窄、%d 帧移动）：这条测试没有分辨力", shrank, moved)
	}
	if prevW != islandPlayerCollapsedW {
		t.Fatalf("收尾岛宽 %v，期望 %v", prevW, islandPlayerCollapsedW)
	}
}

// 收起态不画在线绿点。
//
// 收起后头像 34 撑满内容区，绿点画在头像外沿（size-d+1）就整个出了内容区，
// 被 body 的圆角裁掉大半，剩一小截弧挂在圆的右下角——那不是「绿点被收进
// 去」，那是一颗没擦干净的脏点。
//
// 判据用像素：绿点是 #22c55e，扫整条岛。展开态必须找得到，收起态必须一颗
// 都没有——中间不许出现「半颗」，那正是残弧的形状。
func TestListeningHidesLiveDot(t *testing.T) {
	count := func(tt *ui.Tester) int {
		img := tt.Image()
		n := 0
		for y := 0; y < 60; y++ {
			for x := 600; x < 840; x++ {
				r, g, b, _ := img.At(x, y).RGBA()
				if absInt(int(r>>8)-34) < 26 && absInt(int(g>>8)-197) < 26 &&
					absInt(int(b>>8)-94) < 26 {
					n++
				}
			}
		}
		return n
	}

	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()
	if n := count(tt); n == 0 {
		t.Fatal("展开态没有画出在线绿点：判据扫错了颜色，这条测试没有分辨力")
	}

	app.media.background = true
	app.Router.Push(routeHome)
	tt.Frame()
	for i := 0; i < 90; i++ {
		tt.Advance(16 * time.Millisecond)
		isl, _ := tt.Find("灵动岛")
		n := count(tt)
		switch {
		case isl.W < islandPlayerCollapsedW+4 && n != 0:
			t.Fatalf("第 %d 帧岛宽 %v，还剩 %d 个绿点像素：收起态不该有绿点", i, isl.W, n)
		case isl.W > islandPlayerAvatar+2*islandPlayerPadX+islandLiveDotRoom+4 && n == 0:
			t.Fatalf("第 %d 帧岛宽 %v，绿点已经没了：判据关得太早", i, isl.W)
		}
	}
	if b, _ := tt.Find("灵动岛"); b.W != islandPlayerCollapsedW {
		t.Fatalf("收尾岛宽 %v，期望 %v", b.W, islandPlayerCollapsedW)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// 首页的时钟岛画流光，后台收听画呼吸环。
//
// 这条钉的是一个回归：岛上的环一度按收起进度分配，而首页 openW 等于收起
// 宽度（岛上没有主播信息可量），islandMorph 恒为 1，于是时钟岛被画成了
// 呼吸环——流光整个消失。
//
// 用纯函数测而不是像素：两个环同一个主题色（都取 theme.AccentInk）、都在
// 动、都压在同一条环带上，截图和像素统计分不出谁是谁。这一点是量过的：
// 环带亮度比在首页是 21–22、在后台收听是 4.6–11.6，区间重叠；峰值位置在
// 两边都不移动，也分不开。
func TestIslandRings(t *testing.T) {
	cases := []struct {
		name             string
		player, listen   bool
		m                float32
		wantRing, wantBr float32
	}{
		{"首页只有流光", false, false, 1, 1, 0},
		{"首页 m 取任何值都不影响", false, false, 0, 1, 0},
		{"播放页展开：纯流光", true, false, 0, 1, 0},
		{"播放页收起途中：交叉淡化", true, false, 0.4, 0.6, 0.4},
		{"播放页收定：纯呼吸环", true, false, 1, 0, 1},
		{"后台收听：纯呼吸环", false, true, 1, 0, 1},
		{"后台收听不看 m", false, true, 0, 0, 1},
	}
	for _, tc := range cases {
		ring, breath := islandRings(tc.player, tc.listen, tc.m)
		if ring != tc.wantRing || breath != tc.wantBr {
			t.Errorf("%s：ring=%v breath=%v，期望 %v / %v",
				tc.name, ring, breath, tc.wantRing, tc.wantBr)
		}
	}
}

// 收起宽度常量仍然锁在岛高：后台收听那颗圆胶囊用的就是它。
func TestPlayerIslandCollapsedIsCircle(t *testing.T) {
	if islandPlayerCollapsedW != theme.IslandH {
		t.Fatalf("收起宽 %v、高 %v：圆胶囊该是正圆", islandPlayerCollapsedW, theme.IslandH)
	}
}

// 圆形态里的头像必须正好填满内容区。
//
// 内容区不是岛高减 2：island 的 1px 描边占的是内容盒，body 再往里缩 1px，
// 实际是 38 - 2×1 - 2×1 = 34。头像取 36 会上下左右各溢出 1px，被 body 的
// 圆角裁掉一圈，收缩收尾时裁切量骤变，看起来是头像在抖。
//
// 直接拿热区的实测尺寸作参照，而不是自己再算一遍公式：热区铺满内容区，
// 它量出来是几，内容区就是几。
func TestListeningAvatarFillsContentBox(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.media.background = true
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Advance(3 * time.Second)

	hit, ok := tt.Find("回到直播间")
	if !ok {
		t.Fatal("后台收听态没有画出回到直播间的入口")
	}
	if islandListenAvatar != hit.W {
		t.Fatalf("头像直径 %v、内容区 %v：溢出的部分会被 body 的圆角裁掉",
			islandListenAvatar, hit.W)
	}
}

// 从展开态收缩到圆形态，宽度不该被弹簧的回弹压到 38 以下。
//
// Snappy 会过冲到 37.1 再弹回。宽度一小于 38，内容区就窄于头像，圆边每帧
// 被裁掉一条——屏幕上看到的是头像在颤。改动前这条的最小值是 37.11。
func TestListeningIslandShrinkDoesNotUndershoot(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "标题", Live: true}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	// 离开播放页，岛从展开宽度收缩到圆
	app.media.background = true
	app.Router.Push(routeHome)
	tt.Frame()

	minW := float32(math.MaxFloat32)
	for i := 0; i < 180; i++ {
		tt.Advance(16 * time.Millisecond)
		b, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("第 %d 帧岛不见了", i)
		}
		if b.W < minW {
			minW = b.W
		}
	}
	if minW < islandPlayerCollapsedW-0.01 {
		t.Fatalf("收缩过程中岛宽最低到 %v，低于圆形态的 %v：头像会被裁",
			minW, islandPlayerCollapsedW)
	}
	if b, _ := tt.Find("灵动岛"); b.W != islandPlayerCollapsedW {
		t.Fatalf("收缩结束岛宽 %v，期望 %v", b.W, islandPlayerCollapsedW)
	}
}

// 岛宽由文字宽度算出：取较宽的那行，加一圈固定开销与缓冲，再钳进范围。
//
// 关键的一行是「贴着旧上限」那条：文字宽 262 时，旧实现被 350 卡住，
// 画出来正好把标题末端裁掉——同一标题于是有了完整和省略号两种状态。
func TestPlayerIslandWidthFromText(t *testing.T) {
	cases := []struct {
		name          string
		titleW, metaW float32
		winW, want    float32
	}{
		// body 从岛沿往里 2px（让给环带），宽度公式因此比环带 1px 那版多 2。
		{"内容极短抬到下限", 10, 10, 1440, islandPlayerMinW},
		{"按较宽的那行算", 100, 60, 1440, 198},
		{"第二行更宽时按第二行", 50, 200, 1440, 298},
		{"贴着旧上限也不裁字", 260, 100, 1440, 358},
		{"超过上限", 400, 100, 1440, islandPlayerMaxW},
		{"窄窗口按一半", 400, 100, 600, 300},
		{"窗口再窄也保住下限", 10, 10, 200, islandPlayerMinW},
	}
	for _, tc := range cases {
		if got := islandPlayerWidth(tc.titleW, tc.metaW, tc.winW); got != tc.want {
			t.Errorf("%s：宽 %v，期望 %v", tc.name, got, tc.want)
		}
	}
}

// 上限是产品尺寸，360 再往上会压住顶栏两端。用常量钉住，改这里必须先截图确认。
//
// 360 而不是 350：文字量出来的宽度就是排版宽度，画的时候亚像素取整还会
// 上下浮动一两像素。上限正好卡在标题末端时，同一个标题会在「完整」和
// 「省略号」之间来回跳——抬高这 10px 就是给那一刻留的缓冲。
func TestPlayerIslandMaxWidth(t *testing.T) {
	if islandPlayerMaxW != 360 {
		t.Fatalf("展开上限 %v，期望 360", islandPlayerMaxW)
	}
}

// 首页岛是 24 小时制数字时钟，每秒走一次。
//
// 每位数字是独立元素（翻牌需要逐位裁剪），所以按字符验证：时分秒六位
// 都在文本树里，冒号两个。用 Tester 的时钟推进，不 sleep。
func TestHomeIslandClock(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)

	digits := func() string {
		h, m, s := tt.Now().Clock()
		return fmt.Sprintf("%02d%02d%02d", h, m, s)
	}
	texts := func() map[string]bool {
		set := map[string]bool{}
		for _, x := range tt.Texts() {
			set[x] = true
		}
		return set
	}
	if want := digits(); !texts()[want[:1]] {
		t.Fatalf("时钟缺小时十位 %q，文本树 = %v", want[:1], tt.Texts())
	}
	before := tt.Now().Format("15:04:05")
	tt.Advance(2 * time.Second)
	after := tt.Now().Format("15:04:05")
	if before == after {
		t.Fatalf("推进两秒后时间没变：%s", after)
	}
	// 秒的个位必须已经换成新值
	if want := after[7:]; !texts()[want] {
		t.Fatalf("秒个位应为 %q，文本树里没有", want)
	}
}

// 秒变化时只有秒的个位在翻，其余位保持静止——整排一起滚看起来像整块在抖。
// 用 Tester 的时钟逐帧采样，不 sleep、不截图。
//
// 采样必须按**位置**而不是按字符：时钟里出现重复数字（11:55:19 这种）时，
// 两个 `1` 是两个不同的元素，按字符聚合会把它们的位置混在一起，明明只有
// 一位在动也会被算成「多位在动」。这条以前是偶发失败，根因就在这儿。
func TestClockRollsOnlyChangedDigit(t *testing.T) {
	app := NewMockApp()
	tt := ui.NewTester(app.Shell, 1440, 900)
	// 对齐到「整秒 + 950ms」，往后推必然跨过一次整秒
	now := tt.Now()
	tt.Advance(time.Second - now.Sub(now.Truncate(time.Second)) + 950*time.Millisecond)

	// 采样：按元素位置记 y。翻牌中的位 y 会离开静止值。
	type slot struct{ x, w float32 }
	settled := map[slot]float32{}
	moving := map[slot]bool{}
	for i := 0; i < 12; i++ {
		for _, a := range tt.AccessAll() {
			if len(a.Name) != 1 || a.Name[0] < '0' || a.Name[0] > '9' {
				continue
			}
			// 位置 = 左边界 + 宽度。x 相同、宽也相同的两个元素就是同一位；
			// 同一秒里重复的数字不会落在同一格上。
			k := slot{x: a.Bounds.X, w: a.Bounds.W}
			y := a.Bounds.Y
			if prev, ok := settled[k]; !ok {
				settled[k] = y
			} else if y != prev {
				moving[k] = true
			}
		}
		tt.Advance(25 * time.Millisecond)
	}
	if len(moving) == 0 {
		t.Fatal("跨了一整秒，没有任何一位数字在翻——翻牌动画没跑")
	}
	// 跨一秒只会让秒的个位换值；十位只在 9→0 时变，分位不动。
	// 所以参与翻牌的位置数应当很少（这里只要求「不是整排」）。
	if len(moving) > 4 {
		t.Fatalf("一次走秒有 %d 位数字在动（%v）：应该只有变化的那一位", len(moving), moving)
	}
}

// 切平台时，正在收听的直播间头像不能被当成「旧平台的纹理」清掉。
//
// 清掉的那一帧 islandAvatar 拿不到位图，退回文字占位；解码回来又换回头像，
// 屏幕上就是「闪一下」。keep 集合必须带上当前主播的图。
func TestSwitchTabKeepsNowPlayingAvatar(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{
		Platform:  "douyu",
		RoomID:    "9999",
		Name:      "主播",
		AvatarURL: "https://example.invalid/avatar.jpg",
		CoverURL:  "https://example.invalid/cover.jpg",
	}
	app.media.background = true

	keep := app.feedURLs("bilibili")
	if !keep["https://example.invalid/avatar.jpg"] {
		t.Fatal("切平台要保住正在收听主播的头像：否则岛会闪回文字占位")
	}
	if !keep["https://example.invalid/cover.jpg"] {
		t.Fatal("切平台要保住正在收听主播的封面")
	}
}

// 没在听任何东西时不必保留——keep 集合里不该凭空多出空串。
func TestFeedURLsWithoutPlayer(t *testing.T) {
	app := NewMockApp()
	keep := app.feedURLs("bilibili")
	if keep[""] {
		t.Fatal("keep 集合里出现了空 URL")
	}
}
