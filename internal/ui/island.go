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

// 播放页信息岛的尺寸。
//
// 高度跟前几版一样是 38，由 theme.IslandH 给定。垂直位置按**顶栏居中**算，
// 不写死间距：顶栏右侧那颗登录头像也是垂直居中的，岛必须和它在同一条
// 水平线上，否则顶栏中间那颗东西看起来是浮着的。
//
// 收起态缩成一颗正圆，直径等于岛高——只露头像。展开上限 350，窄窗口按
// 50% 收——顶栏左右两侧还有平台 tab 和图标格，岛不能把它们盖掉。
const (
	islandPlayerCollapsedW float32 = theme.IslandH
	islandPlayerExpandedW  float32 = 350
	islandPlayerMinW       float32 = 310
)

// islandTop 是 island 的绝对上边距：在 64 高的顶栏里垂直居中。
//
// 这里曾经用 theme.IslandGap（10），那是照 44 高的返回钮算出来的数；
// 岛只有 38 高，于是整体偏上 3px，和右侧头像对不齐。
const islandTop = (theme.NavbarHeight - theme.IslandH) / 2

// islandListenAvatar 是后台收听态里那颗头像的直径。
//
// 岛高 38，body 让出 1px 描边之后内容区 36。这里取 36 正好填满内容区——
// 后台收听的岛就是一颗实心的圆，头像和环之间不该露出一圈底色（那会读成
// 三层圆）。用 37 会顶到 body 的描边上，看着像被裁过。
const islandListenAvatar float32 = theme.IslandH - 2

// islandPlayerAvatar 是播放页展开态里那颗头像的直径。
//
// 比后台收听那颗小 4：展开态的岛是 350 宽的整条，头像左边贴着岛沿、
// 右边接标题和关注键，上下必须留出呼吸。取 36 会正好顶满内容区，
// 圆形的上下两端压着描边，看着像被裁掉一块——32 上下各留 2 才成立。
const islandPlayerAvatar float32 = theme.IslandH - 6

// Island 是顶部居中的「灵动岛」，三种形态：
//
//	空闲（没有播放器）  24 小时制数字时钟
//	播放页              主播信息，进直播间自动展开，展开态常驻不再收起
//	后台收听            一颗圆形头像胶囊，点一下回到那个直播间
//
// 展开态不再自动收起：它现在同时承担「正在看谁」和「声音从哪来」两件事，
// 自己缩回去等于把状态藏起来。宽度仍然用弹簧跑，Left 每帧按当前宽度重算，
// 长度是从中心向两侧长出来的——这是灵动岛的手感来源。
func (a *App) Island(c *ui.Context) {
	winW, _ := c.Size()
	player := a.Router != nil && a.Router.Path() == routePlayer
	// 后台收听：引擎还活着但人已经不在播放页了。
	//
	// 判据只看 background：它由 detachMedia 在有引擎时才置上，而 stopPlayback
	// （引擎一关）会把它清掉。再叠一个 player != nil 反而会漏掉截图验收那种
	// 「只有状态位、没有真引擎」的态。
	listening := !player && a.media.background

	if player {
		a.enterPlayerIsland()
	} else {
		// 离开播放页就清掉房间记号：下次进同一个房间也要重新展开一次
		a.infoRoom = ""
	}

	targetW := float32(theme.IslandW)
	openW := float32(0)
	switch {
	case player:
		openW = islandPlayerExpandedW
		if limit := winW * 0.5; limit < openW {
			openW = limit
		}
		if openW < islandPlayerMinW {
			openW = islandPlayerMinW
		}
		// 展开态常驻：不再有 InfoOpen 这一档收起
		targetW = openW
	case listening:
		// 后台收听缩成一枚正圆，和播放页收起态同一个尺寸——它们是同一件事
		// 的两种呈现：那一路还开着。
		targetW = islandPlayerCollapsedW
	}

	// 高度恒为 theme.IslandH（38），和首页那枚眼睛岛一模一样。
	// 展开只让宽度往两边长，高度一像素都不动——这是灵动岛的死规矩。
	island := ui.Box(c.Key("island")).Label("灵动岛").Absolute().Top(islandTop).
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
		// 后台收听不用那圈 conic 流光：流光表达的是「这是活的界面」，
		// 而这里要表达的是「这一路还在放」。换成主题色的呼吸环。
		if listening {
			a.islandBreath(c)
		} else {
			a.islandRing(c, theme.RadiusFull)
		}

		// ---------- 本体：环内侧缩进 1px，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(theme.RadiusFull - islandBodyInset).
			Background(theme.BgSecondary)
		body.ClipInset(0, 0, 0, 0, theme.RadiusFull-islandBodyInset)

		body.Children(func() {
			switch {
			case player:
				a.playerIsland(c, w, openW)
			case listening:
				a.listeningIsland(c)
			default:
				a.islandPill(c)
			}
		})
	})
}

// islandBreath 是后台收听态的呼吸环：主题色，从岛边缘向外扩、同时淡出。
//
// 节奏照抄全部关注页那颗在线点的 livePulse（2 秒一循环），只是颜色取主题
// 强调色——这里的语义是「还在放」，不是「在线」。
//
// 环带比顶部那圈 conic 流光细一半（1.5 而不是 2）：流光是一整圈的装饰，
// 呼吸环只在边缘提示一下，粗了会抢头像。
//
// 必须是「描边环」而不是实心块：body 在它之后画，实心块会被整个盖住，
// 屏幕上什么都看不见。这里用和 islandRing 同一套 mask 挖空中间，只留
// 外圈那一线——差别只在颜色是静态的主题色，位置和透明度随呼吸走。
func (a *App) islandBreath(c *ui.Context) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	ring := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(theme.RadiusFull).Padding(islandBreathPad)
	ring.Background(theme.Accent).Masks(
		ui.Mask{Image: solid, Box: ui.MaskContentBox, Composite: ui.MaskExclude},
		ui.Mask{Image: solid},
	)
	if c.Preferences().ReduceMotion {
		// reduced motion 下不脉冲，只留一圈静态的主题色描边
		ring.Opacity(0.55)
		return
	}
	p := ring.Loop("island-breath", 2*time.Second, ui.Linear)
	scale, alpha := breathPulse(p)
	ring.Scale(scale, scale)
	ring.Opacity(alpha)
}

// islandBreathPad 是呼吸环的粗细，也就是 mask 挖空之后留下的那一圈。
const islandBreathPad float32 = 1.5

// breathPulse 把 0–1 的周期相位换成呼吸环的 scale / opacity。
//
// 扩幅几乎为零（1.06，38 的岛最多到 40）：环本来就压在岛边缘那一像素上，
// 只要一开始放大，它就会和 body 拉开一道黑缝，读起来是「双圈」而不是
// 「这颗圆在呼吸」。呼吸感交给透明度——从 0.9 一路淡到 0，缩放只是让边缘
// 有一点活的余地。
func breathPulse(p float32) (scale, alpha float32) {
	e := easeOutUnit(p)
	return 1 + e*0.06, 0.90 * (1 - e)
}

// enterPlayerIsland 记下「现在在哪个直播间」。
//
// 判据是平台 + 房间号 + 主播名：同一个房间从订阅页返回、切换线路、重连，
// 都不该再当成新房间；换房间（哪怕是同一个主播换场，标题没变）就得重来。
//
// 展开态现在常驻，所以这里只维护记号，不再有「弹一次」的动作。
func (a *App) enterPlayerIsland() {
	s := a.current()
	room := s.Platform + "|" + s.RoomID + "|" + s.Name
	if room == a.infoRoom {
		return
	}
	a.infoRoom = room
	a.InfoOpen = true
}

// listeningIsland 是后台收听态的岛：一颗圆形头像，点一下回到那个直播间。
//
// 尺寸照抄顶栏右上角的账号按钮（auth_panel.go）：容器 38，里头那颗头像
// 直径 = 容器 - 12 = 26。两处并排在一条水平线上，里面的大小也得一样，
// 否则一个是「实心圆点」一个是「满铺圆」，看着就是两套东西。
//
// 外面套一层整圆的透明热区：MyGo 的点击只发给命中链最上那一个元素，
// 头像只有 26，边缘那几像素点不动。
func (a *App) listeningIsland(c *ui.Context) {
	s := a.current()
	hit := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Cursor(ui.CursorPointer).Label("回到直播间")
	if hit.Hovered() {
		hit.Background(ui.RGBA(255, 255, 255, 0.06))
	}
	if hit.Clicked() {
		// 回播放页只走路由：attachMedia 在 PlayerPage 里把视频轨打开。
		a.Router.Push(routePlayer)
	}
	hit.Children(func() {
		ui.Row(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
			ui.Box(c).Size(islandListenAvatar, islandListenAvatar).Shrink(0).Children(func() {
				a.islandAvatar(c, s, islandListenAvatar, false)
			})
		})
	})
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

	// 展开层：头像 + 标题 / 平台·昵称·ID + 关注
	//
	// 展开态常驻，op 只会从 0 走到 1（进直播间那一下的伸缩），所以这里
	// 按宽度淡入即可，不再有和收起层的交叉淡化——那一层已经没有去处了。
	//
	// 左右内边距取同一个数：原来是 (右 12 / 左 6)，实测右内边距 14、左 8，
	// 头像和关注键的留白差了 6px，整条信息看着是往左偏的。收到 4 之后两边
	// 都贴着岛的边缘，和头像上下各 1px 的留白是同一档。
	expanded := ui.Row(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Padding(0, 4).Gap(10).AlignItems(ui.Center)
	expanded.Opacity(op)
	if op < 0.5 {
		expanded.PassThrough()
	}
	expanded.Children(func() {
		// 这里比后台收听那颗小一圈：整条展开态的头像上下要留白，顶满内容区
		// 会让圆形上下两端压着描边，看着像被裁掉一块。
		trigger := ui.Box(c).Size(islandPlayerAvatar, islandPlayerAvatar).
			Shrink(0).Label("主播信息")
		trigger.Children(func() {
			a.islandAvatar(c, s, islandPlayerAvatar, true)
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
//
// 每一位数字各自一个裁剪窗，只有值变的那一位翻牌：旧数字向上滚出、
// 新数字从下方滚入。秒位每秒翻一次，分位每 60 秒，时位每小时——
// 整串一起动会显得整块在抖，逐位翻才是翻页钟的手感。
func (a *App) islandPill(c *ui.Context) {
	// 统一用 c.Now()：生产里就是系统时钟，Tester 里是它自己的时钟，
	// 显示的秒数和「下一帧」的等待必须同一时基，否则测试里时间不走。
	now := c.Now()
	next := time.Second - now.Sub(now.Truncate(time.Second))
	c.After(next)

	ui.Row(c).Fill().AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		h, m, sec := now.Hour(), now.Minute(), now.Second()
		a.clockDigits(c, now, fmt.Sprintf("%02d", h), "h")
		clockColon(c)
		a.clockDigits(c, now, fmt.Sprintf("%02d", m), "m")
		clockColon(c)
		a.clockDigits(c, now, fmt.Sprintf("%02d", sec), "s")
	})
}

// clockDigitW 是每一位数字的固定宽度。
//
// 等宽字体下 Source Code Pro 的 advance 是 0.6em，17px 时约 10.2px；
// 留一点余量取 11，翻牌时旧数字与新数字不会互相挤。
const clockDigitW float32 = 11

// clockFontSize 与 clockDigitH 一起决定翻牌窗口：窗口比字号略高，
// 数字滚过边界时才不会露出半截。
const (
	clockFontSize float32 = 17
	// clockTextH 是 17px 文本盒的实际高度（实测 21.4），用于在窗口里居中。
	clockTextH float32 = 21.4
	// 窗口要比文本盒（21.4px）高：数字滚过边界时才不会露半截。
	clockDigitH float32 = 30
)

// clockRollDur 是一次翻牌的时长。
//
// 每秒都会翻一次，占空比不能高：太长的话随手截图就抓在动画中途，
// 看起来像字没对齐。160ms 占一秒的 16%，翻得出来又不碍事。
const clockRollDur = 160 * time.Millisecond

// clockDigits 画一组数字（两位或一位），每一位独立翻牌。
func (a *App) clockDigits(c *ui.Context, now time.Time, text, slot string) {
	for i := 0; i < len(text); i++ {
		ch := text[i : i+1]
		// 窗口自己在岛里垂直居中；里面的两层再用 Absolute 相对窗口定位。
		ui.Box(c).Width(clockDigitW).Height(clockDigitH).ClipY().Children(func() {
			// 两层：旧数字向上滚出，新数字从下方滚入。只用一个文本做位移
			// 看起来是「整块滑进来」，不是翻牌。
			if c.Preferences().ReduceMotion {
				ui.Text(c, ch).Font(FontClock).FontSize(clockFontSize).
					FontWeight(600).TextColor(theme.TextPrimary).SingleLine()
				return
			}
			// 进度直接用时间算，不走 Keyframes。
			//
			// Keyframes 的 Run 变化时是「从当前显示值接着播」，上一段已经停在
			// 终值 0，新一段就从 0 开始——数字根本不会滚。这条在 motion.go 的
			// 注释里写着（interrupted, it starts from the value shown），但和
			// 「每秒重播一次」的用法正好相反。
			// 只有这一位真的变了才翻：秒的个位每秒变，十位每 10 秒，
			// 分位每 60 秒。整排一起滚看起来像整块在抖。
			d := float32(0)
			if clockDigitChanged(now, slot, i, ch) {
				d = clockRollProgress(now)
			}
			// 两层都垂直居中：窗口 26 比文本盒 21.4 高，贴 Top(0) 会偏上。
			mid := (clockDigitH - clockTextH) / 2
			// 新数字：从下方升到正中。
			ui.Text(c, ch).Font(FontClock).FontSize(clockFontSize).
				FontWeight(600).TextColor(theme.TextPrimary).SingleLine().
				Absolute().Top(mid).Translate(0, d*clockDigitH)
			// 旧数字：从正中升出窗口上方。
			// 必须是**上一个**字符，不是当前这个——两层用同一个字符的话，
			// 看到的只是同一个数字在滑动，不是翻牌。9→0 时要显示 9。
			prev := "9"
			if ch[0] > '0' {
				prev = string(rune(ch[0] - 1))
			}
			ui.Text(c, prev).Font(FontClock).FontSize(clockFontSize).
				FontWeight(600).TextColor(theme.TextPrimary).SingleLine().
				Absolute().Top(mid).Translate(0, (d-1)*clockDigitH)
		})
	}
}

// clockDigitChanged 报告这一位在「上一秒」和现在之间变没变。
//
// 判据是拿一秒前的时刻算出同一个槽位的字符，和当前字符比。
// 秒的个位每秒都变，十位每 10 秒变一次，分位每 60 秒，时位每小时——
// 只有变的那一位翻，整排一起滚会显得整块在抖。
func clockDigitChanged(now time.Time, slot string, i int, ch string) bool {
	prev := now.Add(-time.Second)
	var text string
	switch slot {
	case "h":
		text = fmt.Sprintf("%02d", prev.Hour())
	case "m":
		text = fmt.Sprintf("%02d", prev.Minute())
	default:
		text = fmt.Sprintf("%02d", prev.Second())
	}
	if i >= len(text) {
		return true
	}
	return text[i:i+1] != ch
}

// clockRollProgress 是翻牌进度：整秒那一刻为 1，之后在 clockRollDur 内
// 缓动到 0。整秒之后越久越接近 0，所以数字从下方升上来、旧数字升出去。
//
// 用时间直接算而不是 Keyframes：那里 Run 变化时从「当前显示值」接着播，
// 而每秒都要从满值重来一次，两者语义相反。
func clockRollProgress(now time.Time) float32 {
	since := now.Sub(now.Truncate(time.Second))
	if since >= clockRollDur {
		return 0
	}
	return 1 - easeOutUnit(float32(since)/float32(clockRollDur))
}

// clockColon 是时分秒之间的冒号，不参与翻牌。
//
// 颜色压到三等灰：六位数字才是要读的东西，冒号只是分隔。同级白字时它和
// 数字一起抢边缘，整条读起来更糊；压暗之后数字浮出来，视觉上也透气。
func clockColon(c *ui.Context) {
	ui.Text(c, ":").Font(FontClock).FontSize(clockFontSize).FontWeight(600).
		TextColor(theme.TextTertiary).SingleLine()
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
