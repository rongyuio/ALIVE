package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 启动层：从冷启动到首屏数据就绪之间盖一层，用户不会先看到分区栏和卡片
// 骨架再跳变。
//
// 最短停留 splashMin，避免启动很快时只闪一帧；
// 最长 splashMax，网络慢的时候不能让这层一直挡着。
const (
	splashMin     = 520 * time.Millisecond
	splashMax     = 2500 * time.Millisecond
	splashFadeDur = 240 * time.Millisecond
)

// splash 画启动层：四根呼吸的竖条 + 应用名 + 一条扫光进度线 + 说明文字。
func (a *App) splash(c *ui.Context) {
	if !a.splashFading && a.splashReady() {
		a.splashFading = true
	}
	target := float32(1)
	if a.splashFading {
		target = 0
	}

	// 三层：不透明底色 → 中心光晕 → 居中内容。
	// 底色必须单独一层：RadialGradient 会替换背景填充，直接写在底色上
	// 这层就变成半透明的了。
	el := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(theme.BgPrimary)
	op := el.AnimateTo("splash", target, theme.EaseOut(splashFadeDur))
	el.Opacity(op)
	// 淡出结束就彻底拿掉这层：之后它不该再参与布局和绘制
	if a.splashFading && el.Arrived("splash") {
		a.Splash = false
	}

	el.Children(func() {
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			RadialGradient(ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.42},
				Radius: 460,
				Stops: []ui.GradientStop{
					{Color: theme.Accent.Alpha(0.055), At: 0},
					{Color: ui.Transparent, At: 0.7},
				},
			})
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Children(func() {
			ui.Column(c).Gap(16).AlignItems(ui.Center).Children(func() {
				a.splashMark(c)
				ui.Text(c, "ALIVE").FontSize(theme.TextXL).FontWeight(700).
					LetterSpacing(0.3).TextColor(theme.TextPrimary)
				a.splashLine(c)
				ui.Text(c, "正在载入").FontSize(theme.Text2XS).
					LetterSpacing(0.14).TextColor(theme.TextTertiary)
			})
		})
	})
}

// splashReady 报告启动层可以开始淡出了。
//
// 三条路都算就绪：
//   - 首屏有列表、而且可视范围内的封面都已经画出来了——列表有数据不等于有
//     封面，撤早了用户看到的就是一片带颜色的占位卡；
//   - 没有列表可等：请求出错、或者这个分区本来就空，那就直接露出空态；
//   - 等到 splashMax：网络卡住时，让用户看到骨架也好过一直黑屏。
func (a *App) splashReady() bool {
	elapsed := time.Since(a.splashAt)
	if elapsed < splashMin {
		return false
	}
	if elapsed >= splashMax {
		return true
	}

	switch {
	case a.ErrText != "":
		return true // 出错就别再挡着了，错误条本身要让人看见
	case len(a.Rooms) == 0:
		return !a.ListLoading // 加载完了还是空的：这个分区就没人播
	}

	// 等首屏可视范围的封面到齐
	first, last := a.HomeList.Visible()
	if last < first || a.gridCols <= 0 {
		return false
	}
	start := first * a.gridCols
	end := min(len(a.Rooms), (last+1)*a.gridCols)
	for i := start; i < end; i++ {
		if !media.Default.Ready(a.Rooms[i].CoverURL, a.gridCardW) {
			return false
		}
	}

	// 四个平台的首屏列表也要到齐。
	//
	// 切平台时目标平台的首屏数据不在缓存里，界面就得先摆骨架、再摆空封面
	// 卡、等图回来才成形——三次跳变，用户看到的就是「一大片闪」。
	// 首页第一帧就在预取这些数据，启动层多等它一下，换的是每次切平台
	// 都不再有这个过渡。网络卡住时仍由 splashMax 兜底。
	for _, id := range a.PlatformOrder {
		if id == a.Platform {
			continue // 当前平台的首屏封面在上面等过了
		}
		groups := a.GroupsByPlatform[id]
		if len(groups) == 0 || len(groups[0].CategoryIDs) == 0 {
			return false
		}
		if _, ok := a.feedCache[id+"|"+groups[0].CategoryIDs[0]+"|-1"]; !ok {
			return false
		}
	}

	// 各平台的登录头像也要到齐。
	//
	// 顶栏头像在切平台的第一步就要换人。冷启动后头像和首屏封面抢同一组
	// 下载槽，撤层时头像常常还排在路上——用户这时候点另一个平台，看到
	// 的就是先画灰色人形、再跳回真头像的那一下。头像一共四张，等齐再撤。
	// 加载失败的 Ready 也返回 true，死链不会把启动层拖住。
	for _, id := range a.PlatformOrder {
		auth := authFor(id)
		if auth == nil {
			continue
		}
		if u := auth.Auth().Avatar; u != "" && !media.Default.Ready(u, authAvatarW) {
			return false
		}
	}
	return true
}

// splashMark 是那四根高低不同的竖条：每根独立呼吸，相位错开。
func (a *App) splashMark(c *ui.Context) {
	// 四根竖条的高度与动画延迟
	heights := [4]float32{12, 22, 30, 18}
	// CSS 的 animation-delay 是负的，等价于把相位往前推
	offsets := [4]float32{0.39, 0.26, 0.13, 0}

	ui.Row(c).Height(30).Gap(4).AlignItems(ui.End).Children(func() {
		for i, h := range heights {
			bar := ui.Box(c).Width(5).Height(h).Radius(3).
				Background(theme.Accent)
			// 1.15s 一轮，四条错开形成来回的波浪
			phase := bar.Loop(fmt.Sprintf("splash-bar-%d", i),
				1150*time.Millisecond, ui.Linear)
			s := splashPulse(wrap01(phase + offsets[i]))
			// CSS 里 scaleY 和 opacity 是同步变化的
			bar.Scale(1, s)
			bar.Opacity(0.55 + 0.45*s)
		}
	})
}

// splashPulse 是竖条的呼吸曲线：0 → 0.55，45% → 1，100% → 0.55。
// 三角波两端套 smoothstep，转折才不会发硬。
func splashPulse(p float32) float32 {
	var t float32
	if p < 0.45 {
		t = p / 0.45
	} else {
		t = (1 - p) / 0.55
	}
	t = t * t * (3 - 2*t)
	return 0.55 + 0.45*t
}

// wrap01 把相位折回 [0,1)。
func wrap01(p float32) float32 {
	p = float32(int(p*1000000)%1000000) / 1000000
	if p < 0 {
		p++
	}
	return p
}

// splashLine 是那条扫光进度线：一道 accent 光带从左扫到右，循环。
func (a *App) splashLine(c *ui.Context) {
	const w float32 = 76
	track := ui.Box(c).Width(w).Height(2).Radius(999).Clip().
		Background(theme.TextPrimary.Alpha(0.12))
	// 1.2s 一轮，ease-out：光带冲出去时快、回来时慢
	phase := track.Loop("splash-run", 1200*time.Millisecond, ui.EaseOut)
	x := -w + 2*w*phase

	track.Children(func() {
		ui.Box(c).Width(w).Height(2).
			Translate(x, 0).
			LinearGradient(ui.LinearGradient{
				Angle: 90, // 从左到右
				Stops: []ui.GradientStop{
					{Color: ui.Transparent, At: 0},
					{Color: theme.Accent, At: 0.45},
					{Color: theme.Accent, At: 0.55},
					{Color: ui.Transparent, At: 1},
				},
			})
	})
}
