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

// EnterAnim 是一张卡片的入场动画参数。
//
// 入场动画：260ms ease-out，opacity 0→1 + translateY 10px→0，
// 前 6 张各错峰 20ms。MyGo 的 Animation 自带 Delay，默认曲线就是 ease-out；
// Key 每代递增，切分区时从头播，而不是倒着播。
type EnterAnim struct {
	Key   string
	To    float32
	Delay time.Duration
}

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

// CardHeight 返回给定列宽下卡片的总高度。
//
// 封面严格 16:9（`aspect-ratio: 16/9`），信息条固定高度，
// 这样 GridView 的 rowHeight 才能和实际布局一致。
func CardHeight(w float32) float32 {
	return w*9/16 + theme.CardMetaH
}

// Card 构建一张主播卡片。
//
// 结构是封面（16:9，悬停放大 1.02）+
// 信息条（头像 32、昵称 13/600、标题 12、分类胶囊）。
// hover 时表面亮一档、播放按钮与关注按钮淡入。
func Card(c *ui.Context, s Streamer, w float32, followed bool, in EnterAnim, onOpen, onToggleFollow func()) ui.Element {
	coverH := w * 9 / 16

	card := ui.Column(c).Width(w).Radius(theme.RadiusMD)
	// inset 0 1px 0 rgba(255,255,255,.04)：顶部受光边，让平涂表面有厚度
	card.InsetShadow(0, 1, 0, 0, theme.TopLight)

	// 入场：透明度与位移共用同一个进度。opacity 不标 NonMotion——
	// reduced motion 下整段动画关掉，卡片直接出现。
	if in.Key != "" {
		o := card.AnimateTo(in.Key, in.To, ui.Animation{
			Duration: 260 * time.Millisecond,
			Delay:    in.Delay,
		})
		card.Opacity(o)
		card.Translate(0, 10*(1-o))
	}

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
				// 真封面优先；还没下载完就先用渐变占位撑住版式，加载完成后
				// 图片缓存会触发一次 Update，下一帧自动换成真图。
				if bmp := media.Default.Bitmap(s.CoverURL, w); bmp != nil {
					ui.Image(c, bmp).Fill().Fit(ui.Cover)
				} else {
					coverArt(c, s, w, coverH)
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

			// 播放提示：圆心 46，accent 底，hover 淡入
			if hovered {
				ui.Box(c).Absolute().Left(w/2-23).Top(coverH/2-23).Size(46, 46).Radius(23).
					Background(theme.Accent).Center().Children(func() {
					ui.Text(c, "▶").FontSize(15).TextColor(theme.AccentText)
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

// coverArt 是封面图还没到时的占位：两段同色相渐变 + 一处柔光，
// 让版式和明暗关系先立住，不至于是一块死色。

func coverArt(c *ui.Context, s Streamer, w, h float32) {
	// 封面没到之前是一块安静的底色加一个旋转的圆环。
	//
	// 之前每张卡片按 Hue 生成彩色渐变，一屏十几张一起亮起来像打翻的调色盘，
	// 而且和真实封面切换时视觉跳变很大。统一底色 + 一个转动的环，
	// 加载中的卡片是"安静地在转"，而不是"一片彩色的噪点"。
	art := ui.Box(c).Width(w).Height(h).Background(theme.BgTertiary)
	art.Children(func() {
		art.Center().Children(func() {
			// 尺寸跟封面大小挂钩，最窄的卡片上也看得清
			size := min(w*0.12, 22)
			if size < 14 {
				size = 14
			}
			spinnerTint(c, size, theme.TextTertiary)
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
			TextColor(ui.RGBA(255, 255, 255, 0.88))
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
