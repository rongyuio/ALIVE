package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 光环的几何：容器带 1px 描边，::before inset:-1px + padding:2px，
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
// 收起态缩成一颗正圆，直径等于岛高——只露头像。展开宽度按内容量出来：
// 主播名和标题短就窄、长就宽，只在 [MinW, MaxW] 之间钳，并且不超过窗口
// 的一半——顶栏左右两侧还有平台 tab 和图标格，岛不能把它们盖掉。
const (
	islandPlayerCollapsedW float32 = theme.IslandH
	// islandPlayerMaxW 是展开上限：再长会压住顶栏两端的 tab 与图标格。
	//
	// 360 而不是 350：文字量出来的宽度就是排版宽度，绘制时亚像素取整还会
	// 上下浮动一两像素。上限正好卡在标题末端时，同一个标题会一会儿完整、
	// 一会儿以省略号收尾。多出的这 10px 就是留给那一刻的缓冲。
	islandPlayerMaxW float32 = 360
	// islandPlayerMinW 是展开下限：内容再短也留出这条，免得关注键贴着文字。
	//
	// 180 大约是「头像 + 一行四五个字 + 关注键」的宽度；再往下文字列就只剩
	// 一条缝，标题和平台名会连着省略号一起消失。
	islandPlayerMinW float32 = 180
)

// 展开层的排版数值。宽度测量和实际布局必须用同一套数，否则量出来的岛
// 和画出来的内容会差几像素，短标题那侧就会露出多余的空白或省略号。
const (
	islandPlayerPadX    float32 = 4
	islandPlayerGap     float32 = 10
	islandPlayerFollowW float32 = 26
	// islandPlayerTextSlack 给量出来的文字宽度补一点余量：排版和绘制对
	// 亚像素的取整不同，正好等宽时最后一个字偶尔会被省略号顶掉，同一个
	// 标题于是有了两种状态。
	//
	// 8 是量出来之后仍然稳定不截断的余量（约半个汉字）；再大就只是在
	// 关注键和文字之间多留一段空白。
	islandPlayerTextSlack float32 = 8
)

// islandTop 是 island 的绝对上边距：在 64 高的顶栏里垂直居中。
//
// 这里曾经用 theme.IslandGap（10），那是照 44 高的返回钮算出来的数；
// 岛只有 38 高，于是整体偏上 3px，和右侧头像对不齐。
const islandTop = (theme.NavbarHeight - theme.IslandH) / 2

// islandLiveDotRoom 是「放得下在线绿点」所需的额外宽度。
//
// 绿点压在头像右下角、画在头像圆的外沿（见 islandAvatar 的 size-d+1），
// 直径 10 加 2px 描边。头像 32 时它只探出圆一点点，内容区左右各 1px 的
// 余量就够；头像 34（收起态）时内容区正好等于头像，绿点整个出界。
//
// 8 是量出来的：岛宽 ≥ 32 + 2×4 + 8 = 48 时绿点完整可见，再窄就只剩
// 一截弧。取整到 8 而不是逐像素卡边界——边界上会一帧有一帧无。
const islandLiveDotRoom float32 = 8

// islandContentH 是岛的内容区高度：岛高减掉外层 1px 描边，再减掉 body 的
// 1px 内缩，上下各一份。38 - 2×1 - 2×1 = 34。
const islandContentH float32 = theme.IslandH - 2 - 2*islandBodyInset

// islandListenAvatar 是后台收听态里那颗头像的直径，正好填满内容区。
//
// 必须取 34，不是 36。取 36 会在上下左右各溢出 1px，被 body 切掉——圆的
// 四条边各有一段直线，收缩收尾那一帧裁切量骤变，看起来就是「缩成圆以后
// 头像跳了一下」。
const islandListenAvatar float32 = islandContentH

// islandPlayerAvatar 是播放页展开态里那颗头像的直径。
//
// 比后台收听那颗小 2：展开态的岛是整条，头像左边贴着岛沿、右边接标题和
// 关注键，上下留一点呼吸。内容区 34，32 上下各留 1。
const islandPlayerAvatar float32 = theme.IslandH - 6

// islandListening 报告岛该不该画「后台收听」那一枚。
//
// 判据是「人不在播放页，但这一路还开着」，两种时候成立：
//
//   - background：引擎活着、视频轨关着，声音在走（detachMedia 置上，
//     stopPlayback 清掉）；
//   - loading：这一路正在重开（登录成功要重取流、后台断流重连）。这段时间
//     background 已经被 stopPlayback 清掉、新引擎还没建出来，只看 background
//     的话岛会掉回默认态几秒，看着像「听着听着没了」。
//
// 取流失败时 loading 落回 false，岛也就不会再假装在收听。
func (a *App) islandListening() bool {
	if a.Router != nil && a.Router.Path() == routePlayer {
		return false
	}
	return a.media.background || (a.media.loading && a.media.roomKey != "")
}

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
	listening := a.islandListening()

	if player {
		a.enterPlayerIsland()
	} else {
		// 离开播放页就清掉房间记号：下次进同一个房间也要重新展开一次
		a.infoRoom = ""
	}

	// openW 是「展开成整条」的目标宽度。后台收听态也要算它：收起进度是从
	// 这个宽度和当前宽度之间归一化出来的，没有它，收起来就只能另画一颗
	// 居中的头像——头像先闪到中间、岛再慢慢缩。
	//
	// 首页不量：那时岛上没有主播信息，量出来也没人用。
	openW := islandPlayerCollapsedW
	if player || listening {
		openW = islandPlayerContentW(c, a.current(), winW)
	}

	targetW := float32(theme.IslandW)
	switch {
	case player:
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
		// 这颗岛带 1px 描边，光环正压在这条描边上
		Border(1, theme.Border)

	// 收缩用 Smooth、展开用 Snappy。
	//
	// 圆是硬边界：Snappy 收到 38 时会过冲到 37.1 再弹回来，宽度一旦小于
	// 38，内容区就窄于 34，那颗正好填满内容区的头像被 body 的圆角切掉一条
	// 边——于是收尾约 0.9 秒里，头像边缘一帧宽一帧窄，看着是头像自己在抖。
	// 以前用 clamp 把宽度钉在 38 挡住它，但弹簧内部值还在 37.1↔38.2 之间
	// 来回穿，每穿一次就是一次整岛重绘：第 54 帧那 437 个像素的变化就是
	// 它。不回弹就没有这段往返，收尾是干净的。
	//
	// 展开方向保留 Snappy：那里过冲是超出目标（岛比目标宽一点再回来），
	// 头像居中，裁不到任何东西，回弹正是「灵动」的来源。
	curve := ui.Snappy
	if targetW <= islandPlayerCollapsedW {
		curve = ui.Smooth
	}
	w := island.AnimateTo("island-w", targetW, curve)
	// 岛自己的左边缘也取整。
	//
	// 这一条是收尾抖动的真正来源。岛宽是亚像素的（38.93、38.78……），
	// Left 按 (winW-w)/2 每帧只挪零点几像素。头像挂在岛里面，于是它的
	// 绝对位置跟着抖：一帧跨过物理像素边界，整颗圆就平移一格，下一帧再
	// 回来。头像自己取整没有用——它在岛里是整数，岛在窗口里不是。
	// 取整之后岛每次只挪一整像素，头像跟着走也是整像素。
	island.Width(w).Left(roundDIP((winW - w) / 2))

	// ---------- 投影：走主题的两级阶梯。
	//
	// 岛是唯一一直悬在内容上方的元素，影子必须像"离地几毫米"：一层散的
	// 环境光加一层贴着边的接触影。以前这里按深浅主题各写死一对手调值，
	// 和浮层用的 ShadowPopover/ShadowLg 是两套语言。
	island.Shadow(0, 24, 56, -28, theme.ShadowModal).
		Shadow(0, 4, 14, 0, theme.ShadowContact).
		InsetShadow(0, 1, 0, 0, theme.TopLight)

	island.Children(func() {
		// 两种环，按状态分（见 islandRings）：
		//
		//	首页 / 播放页    conic 流光——表达「这是活的界面」
		//	后台收听         主题色呼吸环——表达「这一路还在放」
		ring, breath := islandRings(player, listening, islandMorph(w, openW))
		if ring > 0 {
			a.islandRing(c, theme.RadiusFull, ring)
		}
		if breath > 0 {
			a.islandBreath(c, breath)
		}

		// ---------- 本体：环内侧缩进 1px，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(theme.RadiusFull - islandBodyInset).
			Background(theme.BgSecondary)
		body.ClipInset(0, 0, 0, 0, theme.RadiusFull-islandBodyInset)

		body.Children(func() {
			switch {
			case player || listening:
				a.playerIsland(c, w, openW, listening)
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
func (a *App) islandBreath(c *ui.Context, alpha float32) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	ring := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(theme.RadiusFull).Padding(islandBreathPad)
	ring.Background(theme.AccentLine).Masks(
		ui.Mask{Image: solid, Box: ui.MaskContentBox, Composite: ui.MaskExclude},
		ui.Mask{Image: solid},
	)
	if c.Preferences().ReduceMotion {
		// reduced motion 下不脉冲，只留一圈静态的主题色描边
		ring.Opacity(0.55 * alpha)
		return
	}
	p := ring.Loop("island-breath", 2*time.Second, ui.Linear)
	scale, pulse := breathPulse(p)
	// 脉冲的放大也要乘上收起进度：breathPulse 的 1.06 是按 38 的圆算的，
	// 岛还有一百多宽时 6% 就是三四像素的缝——环和 body 分开，看起来是
	// 双圈。收缩途中它几乎不放大，收定之后才恢复整段呼吸。
	ring.Scale(1+(scale-1)*alpha, 1+(scale-1)*alpha)
	ring.Opacity(pulse * alpha)
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

// islandRings 决定画哪几圈环，以及各自的不透明度。
//
//	首页        ring=1，breath=0   只有流光
//	播放页      ring=1-m，breath=m  展开时纯流光，收缩时交叉淡化
//	后台收听    ring=0，breath=1   纯呼吸环
//
// 抽成纯函数是因为它没法用像素验证：两个环同一个主题色、都在动、都在同一条
// 环带上，截图和像素统计分不出谁是谁。上一版就是在这里出错——首页 openW
// 等于收起宽度（岛上没有主播信息可量），islandMorph 恒为 1，跟着收起进度
// 走就把时钟岛画成了呼吸环，流光整个消失。
//
// 所以首页那一支必须显式写出来，不能靠 m 的取值碰巧对。
func islandRings(player, listening bool, m float32) (ring, breath float32) {
	switch {
	case player:
		return 1 - m, m
	case listening:
		return 0, 1
	default:
		// 首页：时钟岛。m 在这里没有意义（openW == 收起宽度），不参与。
		return 1, 0
	}
}

// islandMorph 把当前宽度换成「收成圆」的进度：0 是完整展开，1 是正圆。
//
// 宽度本身就是弹簧给的动画值，所以头像是被这条曲线拖着走的，不是自己再
// 起一段动画——两段动画永远对不齐，那正是以前头像先闪到中间的原因。
func islandMorph(w, openW float32) float32 {
	span := openW - islandPlayerCollapsedW
	if span <= 0 {
		return 1
	}
	return 1 - clamp01((w-islandPlayerCollapsedW)/span)
}

// roundDIP 把长度取整到整数 DIP。
//
// MyGo 最终按物理像素画，亚像素值在跨过半像素边界时会整体挪一格。岛收缩
// 时每帧只走零点几像素，连续跨边界就变成肉眼可见的抖动；取整之后每一步
// 都是一整像素，位移干净。
func roundDIP(v float32) float32 {
	return float32(math.Round(float64(v)))
}

// clamp01 把值钳进 0–1。
func clamp01(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// islandPlayerContentW 量出播放页展开态真正需要的宽度。
//
// 文字交给 c.MeasureText 按真实字体量，而不是按字数估：中英文混排、
// 平台名与 ID 数字的宽度差很多，估算会让岛两头多出或少掉一截空白。
func islandPlayerContentW(c *ui.Context, s Streamer, winW float32) float32 {
	titleW, _ := c.MeasureText(0, ui.Span{Text: s.Title, Size: theme.TextSM, Weight: 600})
	metaW, _ := c.MeasureText(0, ui.Span{Text: islandPlayerMeta(s), Size: theme.Text2XS})
	return islandPlayerWidth(titleW, metaW, winW)
}

// islandPlayerWidth 把量出的两行文字宽度换成岛的目标宽度。
//
// 组成：岛 1px 描边 × 2 + 左右内边距 + 头像 + 两段间距 + 较宽的那行文字 +
// 文字缓冲 + 关注键。结果再钳进 [下限, 上限] 和窗口一半：顶栏两侧还有平台
// tab 与图标格，岛不能把它们盖掉；内容再短也不能缩到文字列只剩一条缝。
//
// 宽度只在这几个数之间确定，不依赖当前帧的动画值——同一份内容永远得到
// 同一个目标宽度，弹簧怎么回弹都不会改变最终长度。
func islandPlayerWidth(titleW, metaW, winW float32) float32 {
	textW := titleW
	if metaW > textW {
		textW = metaW
	}
	w := 2*islandBodyInset + 2*islandPlayerPadX + islandPlayerAvatar +
		2*islandPlayerGap + textW + islandPlayerTextSlack + islandPlayerFollowW
	if limit := winW * 0.5; limit < w {
		w = limit
	}
	if w > islandPlayerMaxW {
		w = islandPlayerMaxW
	}
	if w < islandPlayerMinW {
		w = islandPlayerMinW
	}
	return w
}

// islandPlayerMeta 是岛上的第二行：平台 · 主播 · 房间号。
//
// 测量和绘制共用这一处：两边各拼一次字符串，改文案时量出来的宽度就会
// 和画出来的对不上。
func islandPlayerMeta(s Streamer) string {
	return fmt.Sprintf("%s · %s · ID:%s", platformLabel(s.Platform), s.Name, s.RoomID)
}

// playerIsland 是岛里那一条主播信息，播放页和后台收听共用同一棵内容树。
//
// 只有一棵树是这次改动的重点：以前后台收听另画一颗居中的圆头像，切过去的
// 那一帧头像就跳到正中，再等岛慢慢缩——看起来是「闪现」而不是「收起」。
// 现在头像的位置和直径都跟着岛宽走（islandMorph），宽度缩到 38 时它正好
// 停在内容区正中、直径正好填满内容区，和那颗圆胶囊是同一帧同一个像素。
//
// w 是这一帧的动画宽度，openW 是展开目标宽度（跟着窗口钳过）。
// listening 为真时整块内容区是「回到直播间」的点击热区。
func (a *App) playerIsland(c *ui.Context, w, openW float32, listening bool) {
	s := a.current()

	// m：0 完整展开，1 收成正圆。只有头像用这条曲线；文字和关注键不参与，
	// 它们的位置在整个收缩过程中一像素都不动，是被岛边裁掉的。
	m := islandMorph(w, openW)

	avatarD := islandPlayerAvatar + (islandListenAvatar-islandPlayerAvatar)*m
	// 头像锚在**内容框左边**，不是锚在当前宽度的中心。
	//
	// 这个区别就是「真收缩」和「假收缩」的分界。锚中心的话，岛宽到 90 时
	// 头像会被推到内容框正中，正好压住文字的左端——那是遮挡，不是收缩。
	// 锚左边的话头像只在自己的位置上小幅度移动（展开 4 → 收起 0，因为
	// 34 的头像正好填满 34 的内容框），文字在它右边原样待着，两者的位置
	// 关系在整个过程中不变；变的是窗口——岛窄下去，右边的东西被裁掉。
	//
	// 位置和直径都取整到整数 DIP。这不是为了好看，是必须的：弹簧每帧只走
	// 零点几像素，亚像素坐标一跨过 .5 的取整边界，整颗圆就平移一个物理
	// 像素——收尾段连续跨好几次，屏幕上就是「头像一下一下地跳」。取整之后
	// 直径只有 32/33/34 三档、位置只有 4/3/2/1/0 五个值，每一次变化都是
	// 一整像素的干净位移，没有半像素的抖动。
	avatarX := roundDIP(islandPlayerPadX * (1 - m))
	avatarD = roundDIP(avatarD)
	avatarY := roundDIP((islandContentH - avatarD) / 2)

	// 文字与关注键：位置**完全固定**在展开态那一份排版上，不重排、不淡出、
	// 也不跟着头像走。岛窄下去时它们原样待着，超出窗口的部分被 body 的
	// 圆角裁掉——这就是收起该有的样子。
	//
	// 以前两套图层交叉淡化，或者用 Row 的 Grow 按剩余宽度重新分配：前者是
	// 文字先跑掉，后者岛每窄一像素就重排一次，最后挤成几像素宽的残条。
	// 两种都不是收缩，是换了一屏东西。
	//
	// 位置写死之后还有个必须守住的点：文字的左端必须永远在头像右沿之外，
	// 否则头像滑过去时会从字上碾过去。展开态 4 + 32 + 10 = 46，而头像在
	// 收起时最右也只到 0 + 34 = 34，中间始终留着那 12px。
	textX := islandPlayerPadX + islandPlayerAvatar + islandPlayerGap
	textW := openW - 2*islandBodyInset - 2*islandPlayerPadX -
		islandPlayerAvatar - 2*islandPlayerGap - islandPlayerTextSlack - islandPlayerFollowW
	if textW < 0 {
		textW = 0
	}
	followX := openW - islandBodyInset - islandPlayerPadX - islandPlayerFollowW

	// 文字列：垂直居中，两行固定，SingleLine 保证它不会自己换行。
	text := ui.Column(c).Absolute().Top(0).Bottom(0).Left(textX).Width(textW).
		Justify(ui.Center).Gap(2)
	text.Children(func() {
		ui.Text(c, s.Title).FontSize(theme.TextSM).FontWeight(600).
			SingleLine().TextColor(theme.TextPrimary).LineHeight(1.3)
		ui.Text(c, islandPlayerMeta(s)).
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
	btn := ui.Box(c).Absolute().Left(followX).Top((islandContentH-islandPlayerFollowW)/2).
		Size(islandPlayerFollowW, islandPlayerFollowW).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
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

	// 头像：位置和直径都是插值出来的，所以它是「被岛宽拖着走」的。
	//
	// 在线绿点只在展开态画。收起后头像 34 撑满内容区，绿点画在头像外沿
	// （size-d+1）就整个出了内容区，被 body 的圆角裁掉大半，剩一小截弧
	// 挂在圆的右下角——那不是「绿点被收进去」，那是一颗没擦干净的脏点。
	// 判据用宽度而不是进度：绿点是可见与否的二值状态，给它一段淡出反而
	// 会在半透明时露出那截残弧。岛宽到「放得下绿点」才画。
	showLive := w >= islandPlayerAvatar+2*islandPlayerPadX+islandLiveDotRoom

	// 头像自己带名字：它的位置和直径就是这条动画的主体，测试和无障碍树
	// 都要能单独量到它（外面那层热区铺满内容区，宽度不说明任何事）。
	avatar := ui.Box(c).Absolute().Left(avatarX).Top(avatarY).
		Size(avatarD, avatarD).Shrink(0).Label("主播头像")
	avatar.Children(func() {
		a.islandAvatar(c, s, avatarD, showLive)
	})

	// 后台收听：整块内容区都是回播放页的热区，压在头像和文字之上。
	if listening {
		hit := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Cursor(ui.CursorPointer).Label("回到直播间")
		if hit.Hovered() {
			hit.Background(ui.RGBA(255, 255, 255, 0.06))
		}
		if hit.Clicked() {
			// 回播放页只走路由：attachMedia 在 PlayerPage 里把视频轨打开。
			a.Router.Push(routePlayer)
		}
	}
}

// islandAvatar 画一枚圆形头像。showLive 控制是否在右下角画在线绿点和呼吸环。
//
// 绿点不参与收缩动画：它要么整颗在内容区里，要么整颗不画。画出来一半、
// 剩一截弧挂在圆边上，比没有更难解释。调用方按岛宽决定 showLive。
//
// 绿点用 Left/Top 而不是 Right/Bottom：MyGo 里 Right 的基准在缩小后的
// 祖先上会偏出去，Left/Top 从 padding box 左上角算起，size-d+1 就是
// 右下角那一格。
func (a *App) islandAvatar(c *ui.Context, s Streamer, size float32, showLive bool) {
	// 纹理按**固定档位**取，不按这一帧的 size 取。
	//
	// Avatar(url, width) 会按 width 算缓存 key（targetPixels 取整到 8 的倍数）。
	// 收缩时 size 从 32 连续变到 34，跨过档位的那一帧就换了一张纹理：
	// 尺寸只差 0.04px，屏幕上却是整块重绘——那就是收尾看到的那一下。
	// 用展开态的 32 取图，缩放交给 Image 的 Size，纹理全程只有一张。
	if bmp := media.Default.Avatar(s.AvatarURL, islandPlayerAvatar); bmp != nil {
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
	// 直径跟着头像走：以前是按 32 一刀切（10 / 8），头像现在连续缩放，
	// 那一刀会变成一次可见的跳档。0.3 倍加钳位，展开态正好 10、收起态 8。
	d := size * 0.3
	if d > 10 {
		d = 10
	}
	if d < 8 {
		d = 8
	}
	// 呼吸环：和全部关注页那颗在线点同一套 live-pulse——2px 绿环从
	// scale 0.75 扩到 1.85、透明度 0.9 → 0，2 秒一循环，70% 之后停在扩满的
	// 无形状态。环先画，压在点下面。
	//
	// Key 固定：岛上现在只有一颗头像（收起和展开共用同一棵内容树），
	// 不再有两层同时存在。带 size 的旧写法在这里反而有害——size 每帧都
	// 在变，Key 每帧都是新的，脉冲相位每帧重来一次，收缩途中绿点会抖。
	ringSize := d + 4
	ringLeft := size - d - 1
	const ringKey = "island-live"
	ring := ui.Box(c.Key(ringKey)).Absolute().
		Left(ringLeft).Top(ringLeft).Size(ringSize, ringSize).
		Radius(theme.RadiusFull).Border(2, theme.StatusOnline)
	if c.Preferences().ReduceMotion {
		// reduced motion 下关掉脉冲，只留静态绿点
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
// 流光的构成：
//   - conic 渐变，底子是 30% 的主题强调色，105° 处一道全亮的头
//   - 4.5s 线性绕行一圈
//   - 只留 padding 那一圈：content-box 取反的遮罩
//
// 所以环的颜色必须跟着配色走（--accent-ink），不能写死青柠；
// 旋转用 Loop 持续请求帧，动的是光，不是盒子。
func (a *App) islandRing(c *ui.Context, r, alpha float32) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	period := 4500 * time.Millisecond

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
// 缓动用同一档 --ease-out。
func livePulse(p float32) (scale, alpha float32) {
	if p >= 0.7 {
		return 1.85, 0
	}
	e := easeOutUnit(p / 0.7)
	return 0.75 + e*1.10, 0.9 * (1 - e)
}

// easeOutUnit 是 --ease-out 在 0–1 区间上的求值。
func easeOutUnit(t float32) float32 {
	return ui.CubicBezier(0.22, 1, 0.36, 1)(t)
}
