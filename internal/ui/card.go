package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// Streamer 是首页一张卡片需要的数据。协议层接上之前先用假数据。
type Streamer struct {
	Platform string
	RoomID   string // 平台房间号；进入播放页 / 关注时使用
	Name     string
	Title    string
	Category string
	Sub      string // 三级分区（斗鱼才有），空串表示「全部」
	Viewers  int
	Live     bool
	Hue      float32 // 封面渐变的色相种子，让每张卡有各自的气质
	// 真封面和头像；为空或还没下载完时回落到渐变占位
	CoverURL  string
	AvatarURL string
}

// cardLineHeight 是 MyGo 文本的默认行高：字号 × 4/3。
//
// 13 → 17.33、12 → 16：两行加 3 的行距正好撑起 64.33 的信息条。
// 骨架按同一个数撑高，数据到达换屏时整行才不会矮一截。
func cardLineHeight(size float32) float32 { return size * 4 / 3 }

// cardMetaHeight 是信息条的固定高度：上下 padding + 两行文字 + 行距。
//
// 13 + 15 + 17.33 + 3 + 16 = 64.33。骨架和真卡都用这一条公式撑高，
// theme.CardMetaH（60）只是名义值，比真实高度矮 4.33——它撑不住两行字。
func cardMetaHeight() float32 {
	return 13 + 15 + cardLineHeight(theme.TextSM) + 3 + cardLineHeight(theme.TextXS)
}

// CardHeight 返回给定列宽下卡片的总高度。
//
// 封面严格 16:9，信息条高度由 cardMetaHeight 给出，和真实布局一致。
func CardHeight(w float32) float32 {
	return w*9/16 + cardMetaHeight()
}

// Card 构建一张主播卡片。
//
// 结构是封面（16:9，悬停放大 1.02）+
// 信息条（头像 32、昵称 13/600、标题 12）。
// hover 时表面亮一档、播放按钮与关注按钮淡入。
//
// 卡片本身没有任何进出场动画：切换平台 / 分区时它要么还在（旧一屏），
// 要么已经在新位置上（新一屏），中间没有「淡入」「上浮」这一档。
// 之前这里带 opacity 0→1 + translateY 10→0，量出来整屏下落 10 DIP、
// 行内最多错开 6.6 DIP，而封面还在加载——这就是「切换后画面一直在抖」。
func Card(c *ui.Context, s Streamer, w float32, followed bool, onOpen, onToggleFollow, onWatch func()) ui.Element {
	coverH := w * 9 / 16

	card := ui.Column(c).Width(w).Radius(theme.RadiusMD)
	// inset 0 1px 0 rgba(255,255,255,.04)：顶部受光边，让平涂表面有厚度
	card.InsetShadow(0, 1, 0, 0, theme.TopLight)

	hovered := card.Hovered()
	card.Cursor(ui.CursorPointer)
	// 卡片整块可点：无障碍树里要能读到是哪位主播。
	card.Label("直播间:" + s.Name)
	cardBg := theme.BgSecondary
	if hovered {
		// 悬停色 color-mix(bg-secondary 88%, bg-tertiary)
		cardBg = theme.BgSecondary.Mix(theme.BgTertiary, 0.12)
	}
	card.Background(cardBg)
	if onOpen != nil && card.Clicked() {
		onOpen()
	}
	card.ContextMenu(func(m *ui.Menu) {
		if onWatch != nil && m.Item("收进悲鸣观测").Chosen() {
			onWatch()
		}
	})

	card.Children(func() {
		// ---------------------------------------------------------- 封面区
		// 封面只把上两个角切圆：底部负 inset 让封面延伸到信息条底下，
		// 信息条用同色不透明底封住，等价于 overflow: hidden。
		cover := ui.Box(c).Width(w).Height(coverH)
		cover.ClipInset(0, 0, -theme.RadiusMD, 0, theme.RadiusMD)
		cover.Children(func() {
			// 封面本体单独包一层：悬停时把封面从 1 放大到 1.02
			//（320ms ease-out），缩放只作用于画面，不带动角标。
			art := ui.Box(c).Width(w).Height(coverH)
			target := float32(1)
			if hovered {
				target = 1.02
			}
			scale := art.AnimateTo("cover", target, theme.EaseOut(320*time.Millisecond))
			art.Scale(scale, scale)
			art.Children(func() {
				// 真封面优先；还没下载完就先用静止的占位撑住版式，加载完成后
				// 图片缓存会触发一次 Update，下一帧从这里换成真图。
				//
				// 换的那一下没有动画：同一块 16:9 的几何，只把像素从占位色
				// 换成图。这里曾经套过一层 Transition（Enter 缩放 0.985），
				// 但占位和图片共用同一个元素身份，Enter 根本不会播——留着
				// 只会在以后某次结构改动里突然开始播，所以直接去掉。
				if bmp := media.Default.Bitmap(s.CoverURL, w); bmp != nil {
					ui.Image(c, bmp).Fill().Fit(ui.Cover)
				} else {
					coverArt(c, w, coverH)
				}
			})

			// 封面上没有压暗遮罩，LIVE 角标直接落在画面上。

			// 观看人数胶囊：右上角，高 22，圆角胶囊
			ui.Row(c).Absolute().Top(10).Right(10).Height(22).
				Padding(0, 9, 0, 9).Radius(theme.RadiusFull).
				Background(theme.OverlayInk).Center().
				Children(func() {
					ui.Text(c, viewerText(s.Viewers)).
						FontSize(theme.Text2XS).FontWeight(600).
						TextColor(theme.White94).FontFeatures("tnum")
				})

			// 卡片不显示直播 / 未开播状态：首页拉的直播间天然是在播的

			// 关注按钮：左上角。hover 淡入，已关注时一直显示
			if hovered || followed {
				follow := ui.Box(c).Absolute().Left(8).Top(8).Size(32, 32).Radius(16).
					Center().Cursor(ui.CursorPointer)
				if followed {
					follow.Label("取消关注:" + s.Name)
				} else {
					follow.Label("关注:" + s.Name)
				}
				switch {
				case followed:
					follow.Background(theme.Accent).Border(1, ui.Transparent)
				case follow.Hovered():
					// rgba(8,9,11,.9)：比常态再深一档
					follow.Background(ui.RGBA(8, 9, 11, 0.90)).Border(1, theme.White12)
				default:
					// rgba(8,9,11,.72)
					follow.Background(ui.RGBA(8, 9, 11, 0.72)).Border(1, theme.White12)
				}
				if follow.Clicked() && onToggleFollow != nil {
					onToggleFollow()
				}
				follow.Children(func() {
					// 未关注是空心星（白 90%），已关注是实心星（accent 底上的深色）
					if followed {
						ui.Icon(c, iconStarFilled).FontSize(15).TextColor(theme.AccentText)
						return
					}
					ui.Icon(c, iconStar).FontSize(15).TextColor(theme.White90)
				})
			}

			// 播放提示：圆心 46，深色遮罩 + 白三角，hover 淡入。
			//
			// 这里曾经是 accent 圆底。首页一屏十几张封面，鼠标扫过去每张都亮一次
			// 青柠，强调色就不叫强调色了——封面本来就是彩色的，这个圆只需要
			// 让"点它会播"这件事看得见，不需要品牌色。
			if hovered {
				ui.Box(c).Absolute().Left(w/2-23).Top(coverH/2-23).Size(46, 46).Radius(23).
					Background(theme.OverlayInk).Center().Children(func() {
					ui.Text(c, "▶").FontSize(15).TextColor(theme.White94)
				})
			}
		})

		// ---------------------------------------------------------- 信息条
		// 同色底 + 圆角：既封住封面底部的那点溢出，又保住卡片底部的两个圆角。
		ui.Row(c).Padding(13, 12, 15, 12).Gap(10).
			Background(cardBg).Radius(theme.RadiusMD).Children(func() {
			avatar(c, s)

			texts := ui.Column(c).Grow(1).Gap(3)
			texts.Clip()
			texts.Children(func() {
				name := ui.Text(c, s.Name).FontSize(theme.TextSM).FontWeight(600).
					SingleLine().TextColor(theme.TextPrimary)
				if hovered {
					name.TextColor(theme.AccentInk)
				}
				ui.Text(c, s.Title).FontSize(theme.TextXS).SingleLine().
					TextColor(theme.TextTertiary)
			})

			// 分类胶囊已去掉：卡片本来就列在该分区下，再标一次是重复信息，
			// 还会挤掉标题的可用宽度。
		})
	})
	return card
}

// coverArt 是封面还没到时的占位：一块安静的深色场，不转、不闪、不循环。
//
// 这里原来是转动的圆环。一屏十几张卡一起转，等于每一帧都在提醒「你还在等」，
// 而且每张卡各挂一个 Loop、一直在请求帧。占位要做的只是把版式撑住——
// 信息条上已经写着主播名和标题，用户第一眼就知道这是谁，缺的只是画面。
// 动的那一下留给封面到达，不留给等待本身。
func coverArt(c *ui.Context, w, h float32) {
	art := ui.Box(c).Width(w).Height(h).Background(theme.BgTertiary)
	// 和卡片一样的顶部受光边：占位也要有厚度，不能是一块死色。
	art.InsetShadow(0, 1, 0, 0, theme.TopLight)
	art.Children(func() {
		// 中心一层极淡的柔光，给这块空场一个重心。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			RadialGradient(ui.RadialGradient{
				Center: ui.Point{X: 0.5, Y: 0.5},
				Radius: h * 0.55,
				Stops: []ui.GradientStop{
					{Color: theme.TextPrimary.Alpha(0.035), At: 0},
					{Color: ui.Transparent, At: 1},
				},
			})
	})
}

// avatar 画圆形头像：先用真头像，没有就退回底色 + 名字首字。
func avatar(c *ui.Context, s Streamer) {
	avatarCircle(c, s, 32)
}

// avatarCircle 是任意直径的头像（卡片 32、搜索结果 34、关注面板 40）。
func avatarCircle(c *ui.Context, s Streamer, size float32) {
	if bmp := media.Default.Avatar(s.AvatarURL, size); bmp != nil {
		ui.Image(c, bmp).Size(size, size).Radius(size / 2).Fit(ui.Cover)
		return
	}
	base := ui.Oklch(0.42, 0.10, s.Hue+180)
	ui.Box(c).Size(size, size).Radius(size / 2).Background(base).Center().Children(func() {
		ui.Text(c, initial(s.Name)).FontSize(size * 0.4).FontWeight(600).
			TextColor(theme.MediaTextDim)
	})
}

// initial 取名字的第一个字符（中文取一个字，英文取首字母大写）。
func initial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// viewerText 把人气格式化成 1.6万 / 982 这种写法。
func viewerText(n int) string {
	if n >= 10000 {
		return fmt.Sprintf("%.1f万", float64(n)/10000)
	}
	return fmt.Sprint(n)
}

// 让编译器记住 math 可能被后续的动画曲线用到。
var _ = math.Round
