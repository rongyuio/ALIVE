package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 光环的几何：baseline 的容器 1px 描边，::before inset:-1px + padding:2px，
// 环压在描边上并向外扩 1px。本体让出环的内侧 1px。
const (
	islandRingInset float32 = -1
	islandRingPad   float32 = 2
	islandBodyInset float32 = 1
)

// 播放页信息岛的尺寸与节奏。
//
// 高度跟画面左上角那颗返回钮对齐（44），所以顶栏 64 高时正好留 10 上下，
// 和 IslandGap 是一个数。收起态缩成一颗正圆，直径等于岛高——只露头像，
// 连观看时长一起去掉之后，椭圆胶囊没有信息可装。展开上限 350，窄窗口
// 按 50% 收——顶栏左右两侧还有平台 tab 和图标格，岛不能把它们盖掉。
const (
	islandPlayerCollapsedW float32 = theme.IslandH
	islandPlayerExpandedW  float32 = 350
	islandPlayerMinW       float32 = 310
	islandAutoCollapse             = 4 * time.Second
)

// Island 是顶部居中的「灵动岛」。
//
// 首页里它是一枚 24 小时制的数字时钟，hover 不换内容，只安静地走秒。
// 全部关注面板的入口在顶栏的星标格子上
// （见 follows_panel.go），点岛不放大成任何面板。
//
// 播放页里它接的是主播信息：进直播间自动展开成整条（头像、标题、关注），
// 指针不在岛上、闲置 4 秒之后收成一枚圆形头像，点一下再展开。宽度用弹簧跑，
// Left 每帧按当前宽度重算，长度是从中心向两侧长出来的——这是灵动岛的手感
// 来源，不是从左往右推。
func (a *App) Island(c *ui.Context) {
	winW, _ := c.Size()
	player := a.Router != nil && a.Router.Path() == routePlayer

	if player {
		a.enterPlayerIsland(c)
	} else {
		// 离开播放页就清掉房间记号：下次进同一个房间也要重新展开一次
		a.infoRoom = ""
	}

	targetW := float32(theme.IslandW)
	openW := float32(0)
	if player {
		openW = islandPlayerExpandedW
		if limit := winW * 0.5; limit < openW {
			openW = limit
		}
		if openW < islandPlayerMinW {
			openW = islandPlayerMinW
		}
		targetW = islandPlayerCollapsedW
		if a.InfoOpen {
			targetW = openW
		}
	}

	// 高度恒为 theme.IslandH（38），和首页那枚眼睛岛一模一样。
	// 展开只让宽度往两边长，高度一像素都不动——这是灵动岛的死规矩。
	island := ui.Box(c.Key("island")).Label("灵动岛").Absolute().Top(theme.IslandGap).
		Height(theme.IslandH).Radius(theme.RadiusFull).
		// baseline 的 .sidebar 有 1px 描边，光环正压在这条描边上
		Border(1, theme.Border)

	w := island.AnimateTo("island-w", targetW, ui.Snappy)
	island.Width(w).Left((winW - w) / 2)

	// ---------- 投影：baseline 深浅两套（.sidebar 与 [data-theme="light"] .sidebar）。
	// 浅色主题用低透明度蓝黑，套深色的纯黑阴影会在白底上压出一圈脏灰。
	//
	// 收起态是很散的一层：0 24px 56px -28px，几乎看不出投影边界。
	if theme.Current().Dark {
		island.Shadow(0, 24, 56, -28, ui.RGBA(0, 0, 0, 0.90)).
			Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.36)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.05))
	} else {
		island.Shadow(0, 22, 52, -28, ui.RGBA(15, 23, 42, 0.34)).
			Shadow(0, 4, 12, 0, ui.RGBA(15, 23, 42, 0.10)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.80))
	}

	island.Children(func() {
		a.islandRing(c, theme.RadiusFull)

		// ---------- 本体：环内侧缩进 1px，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(theme.RadiusFull - islandBodyInset).
			Background(theme.BgSecondary)
		body.ClipInset(0, 0, 0, 0, theme.RadiusFull-islandBodyInset)

		body.Children(func() {
			if player {
				a.playerIsland(c, w, openW)
				return
			}
			a.islandPill(c)
		})
	})

	// 自动收起：指针停在岛上就一直亮着，移开之后数满 4 秒才收。
	// 到点要一帧，否则画面静止时这一帧可能永远不来。
	if player && a.InfoOpen {
		if island.Hovered() {
			a.infoOpenAt = c.Now()
		} else if idle := c.Now().Sub(a.infoOpenAt); idle >= islandAutoCollapse {
			a.InfoOpen = false
		} else {
			c.After(islandAutoCollapse - idle)
		}
	}
}

// enterPlayerIsland 在「换了直播间」的那一帧把岛展开。
//
// 判据是平台 + 房间号 + 主播名：同一个房间从订阅页返回、切换线路、重连，
// 都不该再弹一次；换房间（哪怕是同一个主播换场，标题没变）就得重新弹。
func (a *App) enterPlayerIsland(c *ui.Context) {
	s := a.current()
	room := s.Platform + "|" + s.RoomID + "|" + s.Name
	if room == a.infoRoom {
		return
	}
	a.infoRoom = room
	a.setIslandOpen(c, true)
}

// setIslandOpen 切换展开态并把计时基准拨到现在。
//
// 手动展开之后同样要数满 4 秒才收——点开就是为了看这几秒，
// 不能因为鼠标早就停在岛外而立刻缩回去。
func (a *App) setIslandOpen(c *ui.Context, open bool) {
	a.InfoOpen = open
	a.infoOpenAt = c.Now()
}

// playerIsland 是播放页里岛的内容。
//
// 收起态和展开态是两套图层叠在一起，按当前宽度交叉淡化：收起时宽 38，
// 只剩一枚圆形头像，宽到 300 以上才轮得到标题和关注。用宽度换算透明度、
// 而不是再起一条动画，两层才跟伸缩严丝合缝。
//
// w 是这一帧的动画宽度，openW 是展开目标宽度（跟着窗口钳过）。
func (a *App) playerIsland(c *ui.Context, w, openW float32) {
	s := a.current()

	op := float32(0)
	if openW > islandPlayerCollapsedW {
		op = (w - islandPlayerCollapsedW) / (openW - islandPlayerCollapsedW)
		if op < 0 {
			op = 0
		}
		if op > 1 {
			op = 1
		}
	}

	// ---------- 收起层：一颗圆形头像
	//
	// 岛本身宽高都收到 38，可用的内容区只有 34（1px 描边 + 两侧各 1px
	// 的 body inset）。头像用 32，四周各留 1px——用 36 会溢出内容区，
	// 收缩动画里每帧被布局挤回去，看起来就是头像在抖。和展开态的 32 同尺寸，
	// 交叉淡化时也不会再变一次大小。名字留在展开态。
	collapsed := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		AlignItems(ui.Center).Justify(ui.Center)
	collapsed.Opacity(1 - op)
	if op >= 0.5 {
		collapsed.PassThrough()
	}
	collapsed.Children(func() {
		ui.Box(c).Size(32, 32).Shrink(0).Children(func() {
			a.islandAvatar(c, s, 32, false)
		})
	})

	// ---------- 展开层：头像 + 标题 / 平台·昵称·ID + 状态 + 关注
	expanded := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Padding(0, 12, 0, 6).Gap(10).AlignItems(ui.Center)
	expanded.Opacity(op)
	if op < 0.5 {
		expanded.PassThrough()
	}
	expanded.Children(func() {
		// 头像 32：岛高锁在 38，本体让出 1px 边之后可用 36，32 上下各剩 2。
		// 之前给 36 会正好顶满、贴着描边，看着像被裁过。
		trigger := ui.Box(c).Size(32, 32).Shrink(0).Cursor(ui.CursorPointer).
			Label("主播信息")
		if trigger.Clicked() {
			a.setIslandOpen(c, !a.InfoOpen)
		}
		trigger.Children(func() {
			a.islandAvatar(c, s, 32, true)
		})

		ui.Column(c).Gap(2).Grow(1).Children(func() {
			ui.Text(c, s.Title).FontSize(theme.TextSM).FontWeight(600).
				SingleLine().TextColor(theme.TextPrimary).LineHeight(1.3)
			ui.Text(c, fmt.Sprintf("%s · %s · ID:%s",
				platformLabel(s.Platform), s.Name, s.RoomID)).
				FontSize(theme.Text2XS).SingleLine().LineHeight(1.3).
				TextColor(theme.TextTertiary)
		})

		// 关注：纯图标，不带文字。头像右下角那个绿点已经说明在播，这里
		// 只留一颗星，岛才不至于拖得太长。
		followed := a.isFollowed(s)
		label := "关注"
		if followed {
			label = "取消关注"
		}
		btn := ui.Box(c).Size(26, 26).Radius(theme.RadiusFull).
			Center().Cursor(ui.CursorPointer).Shrink(0).Label(label)
		if followed {
			btn.Background(theme.StatusOnline.Alpha(0.16)).
				Border(1, theme.StatusOnline.Alpha(0.34))
		} else {
			btn.Background(theme.FillTertiary).Border(1, theme.Border)
			if btn.Hovered() {
				btn.Background(theme.FillSecondary).BorderColor(theme.BorderStrong)
			}
		}
		if btn.Clicked() {
			a.toggleFollow(s)
		}
		col := theme.TextPrimary
		if followed {
			col = theme.StatusOnline
		}
		btn.Children(func() {
			ico := iconStar
			if followed {
				ico = iconStarFilled
			}
			ui.Icon(c, ico).FontSize(15).TextColor(col)
		})

		// 登录入口在顶栏头像上，岛屿里只放关注。
	})

	// 收起态整块都能点：一层透明热区压在最上面，收着的时候才存在。
	//
	// 不能给外层的 Row 挂 Clicked——MyGo 把点击只发给命中链最上那一个元素，
	// 点在主播名那行 Text 上，Row 根本收不到。展开态的按钮和头像必须排在
	// 它下面，所以这东西不能常驻，否则会把关注按钮一起挡掉。
	if op < 0.5 {
		hit := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Cursor(ui.CursorPointer).Label("展开灵动岛")
		if hit.Clicked() {
			a.setIslandOpen(c, true)
		}
	}
}

// islandAvatar 画一枚圆形头像。showLive 控制是否在右下角画在线绿点和呼吸环：
// 展开态有足够空间，保留这个指示；收起态就是一整颗圆头像，再压绿点会被裁掉。
// 绿点用 Left/Top 而不是 Right/Bottom：MyGo 里 Right 的基准在缩小后的
// 祖先上会偏出去，Left/Top 从 padding box 左上角算起，size-d+1 就是
// 右下角那一格。
func (a *App) islandAvatar(c *ui.Context, s Streamer, size float32, showLive bool) {
	if bmp := media.Default.Bitmap(s.AvatarURL, size); bmp != nil {
		ui.Image(c, bmp).Size(size, size).Radius(size / 2).Fit(ui.Cover)
	} else {
		ui.Box(c).Size(size, size).Radius(size / 2).
			Background(ui.Oklch(0.42, 0.10, s.Hue+180)).Center().Children(func() {
			ui.Text(c, initial(s.Name)).FontSize(size * 0.36).FontWeight(600).
				TextColor(theme.TextSecondary)
		})
	}
	if !showLive || !s.Live {
		return
	}
	d := float32(10)
	if size < 32 {
		d = 8
	}
	// 呼吸环：和全部关注页那颗在线点同一套 baseline live-pulse——2px 绿环从
	// scale 0.75 扩到 1.85、透明度 0.9 → 0，2 秒一循环，70% 之后停在扩满的
	// 无形状态。环先画，压在点下面。
	//
	// Key 要带上 size：收起和展开两层头像同时存在（交叉淡化），同一个 Key
	// 会让两个环共用一段动画状态，谁后建谁把相位抢走。
	ringSize := d + 4
	ringLeft := size - d - 1
	ringKey := fmt.Sprintf("island-live-%d", int(size))
	ring := ui.Box(c.Key(ringKey)).Absolute().
		Left(ringLeft).Top(ringLeft).Size(ringSize, ringSize).
		Radius(theme.RadiusFull).Border(2, theme.StatusOnline)
	if c.Preferences().ReduceMotion {
		// baseline 在 reduced motion 下关掉脉冲，只留静态绿点
		ring.Opacity(0)
	} else {
		// 相位用 Loop 驱动：这版 MyGo 的 Keyframes + RepeatForever 播完一次
		// 就停在末值，环会扩满后透明，看起来只剩一颗绿点。
		p := ring.Loop(ringKey+"-pulse", 2*time.Second, ui.Linear)
		scale, alpha := livePulse(p)
		ring.Scale(scale, scale)
		ring.Opacity(alpha)
	}
	ui.Box(c).Absolute().Left(size-d+1).Top(size-d+1).Size(d, d).Radius(d/2).
		Background(theme.StatusOnline).Border(2, theme.BgSecondary)
}

// islandPill 是首页态：一枚 24 小时制的数字时钟，hover 不改变任何内容。
//
// 每秒换一次显示，用 c.After 请求下一帧——岛上的流光本来就在持续请求帧，
// 但时钟不能依赖它：reduced motion 下流光停了，时间还得走。
// 秒变化时数字向上滑入并淡入，用 Keyframes 的 Run 绑定 Unix 秒：Run 一变
// 就重播，中间被别的帧打断也从当前值接着走。reduced motion 下不播动画。
func (a *App) islandPill(c *ui.Context) {
	// 统一用 c.Now()：生产里就是系统时钟，Tester 里是它自己的时钟，
	// 显示的秒数和「下一帧」的等待必须同一时基，否则测试里时间不走。
	now := c.Now()
	next := time.Second - now.Sub(now.Truncate(time.Second))
	c.After(next)

	label := now.Format("15:04:05")
	clock := ui.Text(c, label).
		FontSize(theme.TextSM).FontWeight(600).FontFeatures("tnum").
		TextColor(theme.TextPrimary).SingleLine().LineHeight(1)
	if c.Preferences().ReduceMotion {
		return
	}
	// Run 用「今天过了多少秒」：每秒变一次，且非负（Unix 秒在 Tester 的
	// 时钟里是负数，转 uint64 会绕一大圈）。
	run := uint64(now.Hour()*3600 + now.Minute()*60 + now.Second())
	d := clock.Keyframes("clock-roll", []ui.Keyframe{
		{Value: 1},
		{At: 180 * time.Millisecond, Value: 0, Ease: ui.EaseInOut},
	}, ui.KeyframeOptions{Run: run})
	clock.Translate(0, d*4).Opacity(1 - d*0.55)
}

// islandRing 画那圈贴着边缘跑的 conic 流光。
//
// baseline（Sidebar.module.css 的 .sidebar::before）：
//   - conic 渐变，底子是 30% 的主题强调色，105° 处一道全亮的头
//   - 4.5s 线性绕行一圈
//   - 只留 padding 那一圈：content-box 取反的遮罩
//
// 所以环的颜色必须跟着配色走（--accent-ink），不能写死青柠；
// 旋转用 Loop 持续请求帧，动的是光，不是盒子。
func (a *App) islandRing(c *ui.Context, r float32) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	period := 4500 * time.Millisecond
	alpha := float32(1)

	dim := theme.AccentInk.Mix(ui.Transparent, 0.70) // 30% 的强调色
	hot := theme.AccentInk

	ring := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(r - islandRingInset).Padding(islandRingPad)
	angle := ring.Loop("island-ring", period, ui.Linear) * 360
	ring.ConicGradient(ui.ConicGradient{
		Angle: angle,
		Oklab: true,
		Stops: []ui.GradientStop{
			{Color: dim, At: 0},
			{Color: dim, At: 40.0 / 360},
			{Color: hot, At: 105.0 / 360},
			{Color: dim, At: 165.0 / 360},
			{Color: dim, At: 1},
		},
	}).Masks(
		ui.Mask{Image: solid, Box: ui.MaskContentBox, Composite: ui.MaskExclude},
		ui.Mask{Image: solid},
	).Opacity(alpha)
}

// livePulse 把 0–1 的周期相位换成脉冲环的 scale / opacity：
// 0% 时 0.75 / 0.9，70% 时扩到 1.85 并完全透明，之后保持到周期结束。
// 缓动与 baseline 的 --ease-out 同一档。
func livePulse(p float32) (scale, alpha float32) {
	if p >= 0.7 {
		return 1.85, 0
	}
	e := easeOutUnit(p / 0.7)
	return 0.75 + e*1.10, 0.9 * (1 - e)
}

// easeOutUnit 是 baseline --ease-out 在 0–1 区间上的求值。
func easeOutUnit(t float32) float32 {
	return ui.CubicBezier(0.22, 1, 0.36, 1)(t)
}
