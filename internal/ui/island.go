package ui

import (
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/media"
	"alive/internal/theme"
)

// 光面的几何：整颗岛由同一笔实色渐变换出，body 从里侧盖上。
//
// 岛沿是圆角胶囊，直边上的抗锯齿恰好落在像素格里，弧段上却是半覆盖。
// 以前墨底是一个圆角 Background，流光另画一层、带宽遮罩的外沿又压在同一
// 条圆周上——两条边各自抗一次锯齿，弧段上的边界像素就成了「光 × a +
// 墨 × 墨覆盖 + 顶栏 × 余下」，比两侧都暗。浅色主题里最清楚：黑岛白栏
// 之间那道弧被描了一圈黑边。
//
// 现在整颗岛只由 islandGlow 这一笔填色画出（弧外是岛墨、弧内是强调色，
// 拖影预先混进同一条色带），岛沿只抗一次锯齿。环的内沿交给 body：它从
// 岛沿往里 2px 处盖上墨面，那一条两边都是墨，不会再混出第三种颜色。
const (
	islandRingInset float32 = 0
	islandBodyInset float32 = 2
)

// 播放页信息岛的尺寸。
//
// 高度跟前几版一样是 38，由 theme.IslandH 给定。垂直位置按**顶栏居中**算，
// 不写死间距：顶栏右侧那颗登录头像也是垂直居中的，岛必须和它在同一条
// 水平线上，否则顶栏中间那颗东西看起来是浮着的。
//
// 宽度按内容量出来：主播名和标题短就窄、长就宽，只在 [MinW, MaxW] 之间钳，
// 并且不超过窗口的一半——顶栏左右两侧还有平台 tab 和图标格，岛不能把它们
// 盖掉。有焦点就有这一条，它不会再缩成别的形状：后台听收时也是同一条。
const (
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

// islandContentH 是岛的内容区高度：岛高减掉 body 的 2px 内缩，上下各一份。
// 38 - 2×2 = 34。岛没有描边，那 2px 全部让给了环带。
const islandContentH float32 = theme.IslandH - 2*islandBodyInset

// islandPlayerAvatar 是播放页展开态里那颗头像的直径。
//
// 比后台收听那颗小 4：展开态的岛是整条，头像左边贴着岛沿、右边接标题和
// 关注键，上下留一点呼吸。内容区 34，32 上下各留 1。
const islandPlayerAvatar float32 = theme.IslandH - 6

// onWatchWall 报告当前是不是「悲鸣观测」那一屏。
func (a *App) onWatchWall() bool {
	return a.Router != nil && a.Router.Path() == routeFollows &&
		a.FollowsSection == followsSectionWatch
}

// Island 是顶部居中的「灵动岛」，两种形态：
//
//	空闲（没有活动）  24 小时制数字时钟
//	有活动            Live Activity：主播信息、关注键、结束键
//
// 它就是 iPhone 的 Live Activity，判据只有 activityKey 一条（见 surface.go）：
// App（播放页）打开着的时候它也在，收起之后它还在——那是同一个活动换了个
// 呈现位置，不是两件事。
//
// 它不参与任何共享元素过渡。岛每一帧都在构建，不是「正在出现的元素」，
// ElementTransition 的 Match 表达不了「App 收进岛」；而且两端都挂 Fly 时
// 源元素自己也会飞（third_party/mygo/ui/match.go:794）——那正是「点岛进
// 直播间，岛自己也朝舞台飞」的机器层原因。画面在舞台与岛之间的搬运交给
// 飞行层（surface_flight.go），岛只负责在原地收窄 / 展开自己的形状。
func (a *App) Island(c *ui.Context) {
	winW, _ := c.Size()
	form := a.islandFormOf()
	open := form == islandOpen

	showX := open
	// 首页不量：那时岛上没有主播信息，量出来也没人用。
	openW := float32(theme.IslandW)
	if open {
		openW = islandPlayerContentW(c, a.current(), winW, islandButtonsW(showX))
	}

	targetW := float32(theme.IslandW)
	if open {
		targetW = openW
	}

	// 高度恒为 theme.IslandH（38），和首页那枚眼睛岛一模一样。
	// 展开只让宽度往两边长，高度一像素都不动——这是灵动岛的死规矩。
	//
	// 岛自己带 DragWindow：它挂在 Shell 根下、在顶栏之后构建，是 Navbar
	// （frameless 窗口唯一的拖窗入口）的兄弟。MyGo 的命中链只取指针下最上
	// 层的元素和它的祖先，不会穿到兄弟，所以指针落在岛的非交互区域（时钟、
	// 头像、文字、留白）时，顶栏的 DragWindow 根本不在链里——顶栏正中就成了
	// 拖不动、双击也不最大化的死区。根元素当拖窗目标，岛里的热区和按钮仍
	// 是更深一层的命中目标，点击照常归它们；只有装饰层让位给拖窗。
	island := ui.Box(c.Key("island")).Label("灵动岛").Absolute().Top(islandTop).
		Height(theme.IslandH).Radius(theme.RadiusFull).DragWindow()

	// 弹簧跑宽度：过冲是超出目标（岛比目标宽一点再回来），内容居中，
	// 裁不到任何东西，回弹正是「灵动」的来源。
	w := island.AnimateTo("island-w", targetW, ui.Snappy)
	// 岛自己的左边缘也取整。宽度是亚像素的，Left 按 (winW-w)/2 每帧只挪
	// 零点几像素；岛在窗口里不在整像素上，挂在上面的内容也跟着抖。取整
	// 之后岛每次只挪一整像素。
	x := roundDIP((winW - w) / 2)
	island.Width(w).Left(x)

	// ---------- 投影：走主题的两级阶梯。
	//
	// 岛是唯一一直悬在内容上方的元素，影子必须像"离地几毫米"：一层散的
	// 环境光加一层贴着边的接触影。
	island.Shadow(0, 24, 56, -28, theme.ShadowModal).
		Shadow(0, 4, 14, 0, theme.ShadowContact).
		InsetShadow(0, 1, 0, 0, theme.IslandTopLight)

	island.Children(func() {
		// 光面必须**每一帧都画**：它就是岛的墨底本身。不画的话岛就只剩
		// body 那一块，边缘没有那圈流光。
		a.islandGlow(c, theme.RadiusFull)

		// ---------- 本体：从环的内沿接上，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(theme.RadiusFull - islandBodyInset).
			Background(theme.IslandInk)
		body.ClipInset(0, 0, 0, 0, theme.RadiusFull-islandBodyInset)

		body.Children(func() {
			if open {
				a.playerIsland(c, w)
			} else {
				a.islandPill(c)
			}
		})
	})
}

// islandTap 是点岛这一下：照 iPhone，点 Live Activity 就是打开对应的 App。
//
// 播放页上不会走到这里：那时岛是时钟（见 islandFormOf），没有热区。
// 「点岛收起」这条手势本来就不该有——收起是 App 自己的返回，走 Esc 或
// 播放页顶栏的返回钮。
func (a *App) islandTap() {
	if a.focus == nil {
		return
	}
	a.openRoom(a.focus.room)
}

// islandClose 是岛上那颗 X：结束这一路。
//
// 动作只有一个——closeFocus；文案按这一路此刻的真实效果走：
//
//	在观测墙上    停止观测    收声，格子留在墙上做静音预览
//	别处          关闭直播间  结束这一路，岛回时钟
//
// 播放页上没有这颗 X（那时岛是时钟）：退出走播放页顶栏的返回钮。
func (a *App) islandClose() {
	a.closeFocus()
}

// islandCloseLabel 是岛上那颗 X 的可读名字。
//
// 只按「这一下会得到什么」写，不按「你在哪一页」写：已经离开墙时预览是
// 停掉的，说「停止观测」会承诺一个不存在的东西。
func (a *App) islandCloseLabel() string {
	if a.wallActiveKey() != "" {
		return "停止观测"
	}
	return "关闭直播间"
}

// roundDIP 把长度取整到整数 DIP。
//
// MyGo 最终按物理像素画，亚像素值在跨过半像素边界时会整体挪一格。岛收缩
// 时每帧只走零点几像素，连续跨边界就变成肉眼可见的抖动；取整之后每一步
// 都是一整像素，位移干净。
func roundDIP(v float32) float32 {
	return float32(math.Round(float64(v)))
}

// islandButtonsW 是右侧按钮组占的宽度：收藏一颗，播放页与墙上 active 时
// 收藏右边多一颗 X。
//
// 量宽度和画布局都必须用这同一个数：两边各算一次，增删按钮时文字列就会
// 和画出来的按钮对不上。
func islandButtonsW(showX bool) float32 {
	if showX {
		return 2*islandPlayerFollowW + islandPlayerGap
	}
	return islandPlayerFollowW
}

// islandPlayerContentW 量出播放页展开态真正需要的宽度。
//
// 文字交给 c.MeasureText 按真实字体量，而不是按字数估：中英文混排、
// 平台名与 ID 数字的宽度差很多，估算会让岛两头多出或少掉一截空白。
func islandPlayerContentW(c *ui.Context, s Streamer, winW, buttonsW float32) float32 {
	titleW, _ := c.MeasureText(0, ui.Span{Text: s.Title, Size: theme.TextSM, Weight: 600})
	metaW, _ := c.MeasureText(0, ui.Span{Text: islandPlayerMeta(s), Size: theme.Text2XS})
	return islandPlayerWidth(titleW, metaW, winW, buttonsW)
}

// islandPlayerWidth 把量出的两行文字宽度换成岛的目标宽度。
//
// 组成：body 内缩 × 2 + 左右内边距 + 头像 + 两段间距 + 较宽的那行文字 +
// 文字缓冲 + 右侧按钮组。结果再钳进 [下限, 上限] 和顶栏中间真正可用的宽度：
// 顶栏两端还有平台 tab 与工具格，岛不能把它们盖掉；内容再短也不能缩到文字
// 列只剩一条缝。
//
// 宽度只在这几个数之间确定，不依赖当前帧的动画值——同一份内容永远得到
// 同一个目标宽度，弹簧怎么回弹都不会改变最终长度。
func islandPlayerWidth(titleW, metaW, winW, buttonsW float32) float32 {
	textW := titleW
	if metaW > textW {
		textW = metaW
	}
	w := 2*islandBodyInset + 2*islandPlayerPadX + islandPlayerAvatar +
		2*islandPlayerGap + textW + islandPlayerTextSlack + buttonsW
	limit := winW * 0.5
	if winW >= islandMinWindowW {
		// 只按窗口一半钳是不够的：MinWidth=900、长标题取 360 时，岛占
		// 270..630，而窄断点下最后一枚平台 tab 到 296、搜索按钮从 617 起。
		// 岛是顶层命中目标，盖住的那一截点击会落进岛根，tab 和搜索都收不到。
		// 改成按两端固定组的实际占宽来算，岛居中，所以两侧对称留。
		if side := winW - 2*islandNavReserve(winW); side < limit {
			limit = side
		}
	}
	if limit < w {
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

// islandMinWindowW 是应用窗口的最小宽度，与 main.go 的 MinWidth 一致。
//
// 更窄的窗口只存在于合成视图的测试里；那时顶栏两组本身已经挨在一起，
// 再按固定组宽钳岛没有意义，保留原来的「窗口一半」规则。
const islandMinWindowW float32 = 900

// islandNavReserve 是顶栏两端固定控件各自占掉的宽度（含外侧 padding 与 gap）。
//
// 几何从 navbarMetrics 来，不在这里抄一份字面量：顶栏和岛的宽度上限必须
// 用同一组数，否则改一次断点就会让岛压住 tab 或搜索键。
//
// 岛居中，左右对称地让，所以取更宽的一侧。按钮数按最宽形态算：播放页的
// activeTab 必然是某个平台，搜索与登录头像都在。
func islandNavReserve(winW float32) float32 {
	m := navbarMetricsFor(winW)
	// 左：关注 + 四个平台 = 5 格；右：工具胶囊四颗（搜索、同步、配色、登录）
	// + 三枚窗口按钮。
	return max(m.leftReserve(5), m.rightReserve(4))
}

// islandPlayerMeta 是岛上的第二行：平台 · 主播 · 房间号。
//
// 测量和绘制共用这一处：两边各拼一次字符串，改文案时量出来的宽度就会
// 和画出来的对不上。
func islandPlayerMeta(s Streamer) string {
	return fmt.Sprintf("%s · %s · ID:%s", platformLabel(s.Platform), s.Name, s.RoomID)
}

// playerIsland 是岛里那一条主播信息。有活动就只有这一条，不按表面分叉：
// 播放页、墙上、桌面都是同一棵内容树、同一个宽度。
//
// 点它做一件事：打开这一间的播放页（在播放页上则是把它收起来）。文案
// 「打开直播间」/「收起到后台」跟着走，见 islandTap。
//
// 那颗 X 的动作只有一个——closeFocus；文案按这一下会得到什么走，见
// islandCloseLabel。
func (a *App) playerIsland(c *ui.Context, w float32) {
	s := a.current()

	// 头像锚在**内容框左边**。位置和直径都取整到整数 DIP：亚像素坐标在
	// 跨过半像素边界时整颗圆会平移一个物理像素，看起来是头像自己在跳。
	avatarD := roundDIP(islandPlayerAvatar)
	avatarX := roundDIP(islandPlayerPadX)
	avatarY := roundDIP((islandContentH - avatarD) / 2)

	// 一切横向位置都按**当前**宽度 w（弹簧值）算，不按目标宽度 openW。
	//
	// 按目标宽度排的后果：展开那几百毫秒里岛沿还在往两边长，内容已经按
	// 最终宽度摆好——右边那颗 X 落在岛外被 body 的 ClipInset 整颗裁掉
	//（实测 t=20ms 时 X 的右沿露出 -160px），收缩时右侧空出一大截。
	// 按当前宽度排，内容贴着沿一起长，才是「岛在展开」而不是「先拉宽再
	// 冒出内容」。
	textX := islandPlayerPadX + islandPlayerAvatar + islandPlayerGap
	buttonsW := 2*islandPlayerFollowW + islandPlayerGap
	textW := w - 2*islandBodyInset - 2*islandPlayerPadX -
		islandPlayerAvatar - 2*islandPlayerGap - islandPlayerTextSlack - buttonsW
	if textW < 0 {
		textW = 0
	}
	// 最右一颗固定是 X；收藏贴在它左边。
	edgeX := w - islandBodyInset - islandPlayerPadX - islandPlayerFollowW
	followX, cancelX := edgeX-islandPlayerGap-islandPlayerFollowW, edgeX

	// 文字列：垂直居中，两行固定，SingleLine 保证它不会自己换行。
	//
	// ContentTransition：换台 / 换这一路时旧的两行淡出、新的淡入，而不是
	// 硬换字。iPhone 上 Live Activity 更新内容就是这么走的（HIG：Animate
	// layout changes，尽量保住已有布局、只让变化的元素动）。
	text := ui.Column(c).Absolute().Top(0).Bottom(0).Left(textX).Width(textW).
		Justify(ui.Center).Gap(2)
	text.Children(func() {
		components.Text(c, s.Title).FontSize(theme.TextSM).FontWeight(600).
			SingleLine().TextColor(theme.MediaText).LineHeight(1.3).
			ContentTransition(ui.ContentTransition{})
		components.Text(c, islandPlayerMeta(s)).
			FontSize(theme.Text2XS).SingleLine().LineHeight(1.3).
			TextColor(theme.MediaTextDim).
			ContentTransition(ui.ContentTransition{})
	})

	// 头像自己带名字：它是一颗独立的元素，测试和无障碍树都要能量到它
	// （外面那层热区铺满内容区，宽度不说明任何事）。
	avatar := ui.Box(c).Absolute().Left(avatarX).Top(avatarY).
		Size(avatarD, avatarD).Shrink(0).Label("主播头像")
	avatar.Children(func() {
		a.islandAvatar(c, s, avatarD)
	})

	// 整块内容区是打开 / 收起这一路的热区，压在文字和头像之上。
	//
	// 它必须画在按钮之前、头像之后。MyGo 的命中链只取指针下最上层的元素
	// 和它的祖先，不会穿过兄弟往下找：热区排在按钮之后会把收藏键整个吃掉
	// （点收藏等于点整块岛），头像虽然不可点，压在热区上面同样会挡住它。
	// 顺序因此是 文字 → 头像 → 热区 → 按钮。
	{
		hitLabel := "打开直播间"
		if a.Router != nil && a.Router.Path() == routePlayer {
			hitLabel = "收起到后台"
		}
		hit := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Cursor(ui.CursorPointer).Label(hitLabel)
		hit.Background(hoverSurface(hit, "island-hit-bg", hit.Hovered(), theme.IslandHover))
		if hit.Clicked() {
			a.islandTap()
		}
	}

	// X：同一个位置同一颗按钮，动作永远是「结束焦点」——closeFocus。
	//	播放页      结束整路并退回上一页
	//	墙上        收声，格子留在墙上做静音预览
	//	后台收听    结束这一路，岛回到时钟
	// 文案按这一下会得到什么走，见 islandCloseLabel。
	cancelLabel := a.islandCloseLabel()
	cancel := ui.Box(c).Absolute().Left(cancelX).Top((islandContentH-islandPlayerFollowW)/2).
		Size(islandPlayerFollowW, islandPlayerFollowW).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(cancelLabel)
	cancel.Background(hoverSurfaceRest(cancel, "island-cancel-bg", cancel.Hovered(),
		theme.MediaRow, theme.MediaHair)).
		Border(1, theme.MediaRowLine)
	cancel.BorderColor(hoverInk(cancel, "island-cancel-border", cancel.Hovered(), theme.MediaRowLine, theme.MediaHairStrong))
	if cancel.Clicked() {
		a.islandClose()
	}
	cancelDown := cancel.Pressed()
	cancel.Children(func() {
		components.Icon(c, iconX).FontSize(15).TextColor(theme.MediaText)
		pressDim(c, cancelDown, theme.RadiusFull)
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
		// 岛是墨底，不能用相对地面推导的 Fill/Border：浅色主题里那是近白的
		// 填充，白星压上去就没了。用「压在图上的墨」那一族。
		btn.Background(hoverSurface(btn, "island-follow-bg", btn.Hovered(), theme.MediaHair)).
			Border(1, hoverInk(btn, "island-follow-border", btn.Hovered(), theme.MediaRowLine, theme.MediaHairStrong))
	}
	if btn.Clicked() {
		a.toggleFollow(s)
	}
	col := theme.MediaText
	if followed {
		col = theme.StatusOnlineInk
	}
	followDown := btn.Pressed()
	btn.Children(func() {
		ico := iconStar
		if followed {
			ico = iconStarFilled
		}
		components.Icon(c, ico).FontSize(15).TextColor(col)
		pressDim(c, followDown, theme.RadiusFull)
	})
}

// islandAvatar 画一枚圆形头像，直播中时右下角带在线绿点和呼吸环。
//
// 绿点用 Left/Top 而不是 Right/Bottom：MyGo 里 Right 的基准在缩小后的
// 祖先上会偏出去，Left/Top 从 padding box 左上角算起，size-d+1 就是
// 右下角那一格。
func (a *App) islandAvatar(c *ui.Context, s Streamer, size float32) {
	if bmp := media.Default.Avatar(s.AvatarURL, islandPlayerAvatar); bmp != nil {
		components.Image(c, bmp).Size(size, size).Radius(size / 2).Fit(ui.Cover)
	} else {
		ui.Box(c).Size(size, size).Radius(size / 2).
			Background(ui.Oklch(0.42, 0.10, s.Hue+180)).Center().Children(func() {
			components.Text(c, initial(s.Name)).FontSize(size * 0.36).FontWeight(600).
				TextColor(theme.MediaTextBody)
		})
	}
	if !s.Live {
		return
	}
	// 直径跟着头像走：0.3 倍加钳位，32 的头像上是 9.6。
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
		Background(theme.StatusOnline).Border(2, theme.IslandInk)
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
				components.Text(c, ch).Font(FontClock).FontSize(clockFontSize).
					FontWeight(600).TextColor(theme.MediaText).SingleLine()
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
			components.Text(c, ch).Font(FontClock).FontSize(clockFontSize).
				FontWeight(600).TextColor(theme.MediaText).SingleLine().
				Absolute().Top(mid).Translate(0, d*clockDigitH)
			// 旧数字：从正中升出窗口上方。
			// 必须是**上一个**时刻同一个槽位的真实字符，不是「当前字符减一」。
			// ch-1 只在 9→0 时碰巧对；十位 5→0（每个整分钟）会把 5 画成 9，
			// 而 d=1 时它正停在窗口正中，肉眼可见约 160ms。
			var prev string
			p := now.Add(-time.Second)
			switch slot {
			case "h":
				prev = clockDigitChar(p.Hour(), i)
			case "m":
				prev = clockDigitChar(p.Minute(), i)
			default:
				prev = clockDigitChar(p.Second(), i)
			}
			components.Text(c, prev).Font(FontClock).FontSize(clockFontSize).
				FontWeight(600).TextColor(theme.MediaText).SingleLine().
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

// clockDigitChar 返回两位十进制数 v 的第 i 个字符（i 越界返回空串）。
func clockDigitChar(v, i int) string {
	s := fmt.Sprintf("%02d", v)
	if i < 0 || i >= len(s) {
		return ""
	}
	return s[i : i+1]
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
	components.Text(c, ":").Font(FontClock).FontSize(clockFontSize).FontWeight(600).
		TextColor(theme.MediaTextDim).SingleLine()
}

// islandGlow 画出整颗岛的面：墨底和 conic 流光由同一笔填色画出。
//
// 一笔画完不是为了少一层，是为了岛沿只抗一次锯齿。实色渐变的停靠点
// 已经是「墨里混进多少强调色」的结果（见 islandGlowStops），环外是纯墨、
// 环内是 AccentOnInk——所以这一笔同时承担底色和光，body 在上面盖住内部，
// 一条 2px 的光带只在岛的边缘露出来。
//
// 拖影也在这条色带里：以前它是一层独立的、角度落后 12° 的环，压在芯上。
// 两层各有各的抗锯齿，弧段上照样会叠出暗边；现在它的 alpha 剖面先和芯
// 合成，再一起换成实色停靠点，屏幕上仍然是一道头亮尾淡的弧。
//
// 角速度不是匀速：头部快、尾部慢，回绕处速度连续，没有顿挫。reduced
// motion 下不转，留一段固定的弧，光还在，只是不跑。
func (a *App) islandGlow(c *ui.Context, r float32) {
	glow := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(r - islandRingInset)

	angle := islandRingRestAngle
	if !c.Preferences().ReduceMotion {
		angle = glow.Loop("island-ring", islandRingPeriod, islandSweep) * 360
	}
	glow.ConicGradient(ui.ConicGradient{
		Angle: angle,
		// 停靠点是在 sRGB 里预混过的实色，再走 Oklab 会把尾梢的颜色
		// 带偏：alpha 叠墨本来是一条 sRGB 直线，这里要的就是它。
		Stops: islandGlowStops(),
	})
}

const (
	islandRingPeriod = 4500 * time.Millisecond
	// islandRingRestAngle 是 reduced motion 下热段的落点：0° 让头停在
	// 右侧偏下——不压标题，也不在正中，读起来仍是一段光。
	islandRingRestAngle float32 = 0
	// islandRingEchoLag 是拖影落后的角度（度）。太小看不出两层，太大就
	// 变成两道光。12° 约等于头宽的 1/3。
	islandRingEchoLag float32 = 12
	// islandRingEchoAlpha 是拖影的不透明度：芯的四成不到。
	islandRingEchoAlpha float32 = 0.38
)

// islandSweep 把 0–1 的线性相位换成角度进度。
//
// 匀速转圈读起来是转盘，不是光。这里让头在中段快、两头慢，且
// f(0)=0、f(1)=1、两端速度相等（都是 1+bias），所以每圈回绕不会顿一下。
// bias 0.6 时最快与最慢约 4:1：肉眼能看出「扫过去」，又不至于忽快忽慢。
func islandSweep(p float32) float32 {
	const bias float32 = 0.6
	return p + bias*float32(math.Sin(2*math.Pi*float64(p)))/(2*math.Pi)
}

// ringStop 是流光 alpha 剖面上的一个折点：at 是全周相位（0–1），a 是强调色
// 占了多少（0 是纯岛墨，1 是全亮的 AccentOnInk）。
type ringStop struct {
	at, a float32
}

// islandRingProfile 是核心那道弧的 alpha 剖面：72° 起尾、108° 最亮、
// 137° 散尽，其余整圈为 0。
//
// 只改 alpha 不改 RGB：混到纯透明黑会在暗底上拖出一圈灰边——上一版那圈
// 「脏描边」就是 30% 的强调色铺满圆周换来的。

var islandRingProfile = []ringStop{
	{0, 0},
	{0.20, 0},    // 尾巴从 72° 开始接上
	{0.24, 0.30}, // 尾梢：几乎看不见，只是一点提亮
	{0.30, 1},    // 头部：108°
	{0.34, 0.35}, // 头前的余光
	{0.38, 0},    // 到 137° 散尽
	{1, 0},
}

// islandRingAlphaAt 是剖面在相位 pos 上的值，段内线性。
func islandRingAlphaAt(pos float32) float32 {
	if pos <= islandRingProfile[0].at {
		return islandRingProfile[0].a
	}
	for i := 1; i < len(islandRingProfile); i++ {
		a, b := islandRingProfile[i-1], islandRingProfile[i]
		if pos <= b.at {
			if b.at == a.at {
				return b.a
			}
			return a.a + (b.a-a.a)*(pos-a.at)/(b.at-a.at)
		}
	}
	return islandRingProfile[len(islandRingProfile)-1].a
}

// islandGlowStops 把「核心 + 拖影」合成一条实色色带，供 islandGlow 一笔画完。
//
// 芯先画、拖影压在上面的效果是 e + c·(1-e)，再乘整体淡入淡出 alpha；
// 这个值换成「墨里混进多少强调色」就是停靠点的颜色。拖影是芯平移
// islandRingEchoLag 度。
//
// 停靠点取两条剖面折点的并集，长段再补一个中点：两个折点之间这个合成
// 值是二次的，线性插值最多差 1% 的 alpha，肉眼看不出来。
func islandGlowStops() []ui.GradientStop {
	lag := islandRingEchoLag / 360
	at := []float32{0, 1}
	for _, s := range islandRingProfile {
		for _, p := range []float32{s.at, s.at - lag} {
			if p > 0 && p < 1 {
				at = append(at, p)
			}
		}
	}
	slices.Sort(at)
	at = slices.Compact(at)

	pos := make([]float32, 0, len(at))
	for i, p := range at {
		pos = append(pos, p)
		if i+1 < len(at) && at[i+1]-p > 0.03 {
			pos = append(pos, (p+at[i+1])/2)
		}
	}

	hot := theme.AccentOnInk
	stops := make([]ui.GradientStop, 0, len(pos))
	for _, p := range pos {
		c := islandRingAlphaAt(p)
		e := islandRingEchoAlpha * islandRingAlphaAt(p-lag)
		u := e + c*(1-e)
		stops = append(stops, ui.GradientStop{
			Color: theme.IslandInk.Mix(hot, u),
			At:    p,
		})
	}
	return stops
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
