package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 右侧弹幕列表的尺寸取自 baseline 的 DanmuPanel.module.css：
// 默认 196 宽（可拖 168–320），列表 padding 12 10 16、行 gap 3、
// 每条 6×9 padding、圆角 8、13px / line-height 1.5。
const (
	danmuPanelMinW     float32 = 168
	danmuPanelMaxW     float32 = 320
	danmuPanelMaxLines         = 200
	danmuPanelRowGap   float32 = 3
	danmuPanelPadV     float32 = 6
	danmuPanelPadH     float32 = 9
	danmuPanelListGap  float32 = 3
)

// panelWidth 是钳制到 168–320 之后的面板宽度。
func (a *App) panelWidth() float32 {
	w := a.DanmuPanelW
	if w < danmuPanelMinW {
		w = danmuPanelMinW
	}
	if w > danmuPanelMaxW {
		w = danmuPanelMaxW
	}
	return w
}

// danmuPanel 是播放页右侧的弹幕列表。
//
// 收起时它仍然占一个零宽的位置：baseline 的 .panelSlot 就是这样——面板
// 本身不渲染，但边缘把手始终在，鼠标移到右边缘才浮出小舌。之前我把把手
// 写在面板内部，一收起就整个消失，再没有入口能把它叫回来。
func (a *App) danmuPanel(c *ui.Context) {
	open := a.DanmuPanelOn
	w := a.panelWidth()
	if !open {
		w = 0
	}

	// 高度由父级 Row 的 Stretch 撑满，这里只定宽——
	// 写成 Fill 会把宽度也一起改成 100%。
	panel := ui.Column(c).Width(w)
	if open {
		panel.Background(theme.BgSecondary).Label("弹幕列表")
	}
	panel.Children(func() {
		if open {
			// 左侧 1px 分隔线
			ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(1).
				Background(theme.Separator)
			a.danmuResizer(c)
			a.danmuPanelList(c)
		}
		a.danmuPanelHandle(c, open)
	})
}

// danmuResizer 是面板左沿 5px 的可拉伸热区（baseline 的 .resizer）。
//
// 两处坑：
//
//  1. Pressed / PointerPosition 的 over 都要求指针还停在那 5px 里，一拖出去
//     信号就断，松手前再也收不到位置。Dragged 是 press 级状态，指针跑到
//     元素外仍然为真。
//  2. 热区自己会跟着面板左移，用它的局部坐标算位移会自我放大——实测拖 60
//     宽了 66。改成拿面板**右沿**做参考系：拖动中右沿不动（面板是 Row 的
//     定宽尾元素，只会往左长），贴右沿那块 1px 锚点的局部 x 就是稳定的
//     指针坐标。（Bounds 也不能用来拼绝对坐标：文档写明它给的是上一帧的
//     盒，和当帧的局部 x 混用每帧都会叠一点误差。）
func (a *App) danmuResizer(c *ui.Context) {
	// 右沿锚点：只提供参考系，不画、不吃鼠标
	anchor := ui.Box(c).Absolute().Right(0).Top(0).Bottom(0).Width(1)
	ax, _, _ := anchor.PointerPosition()

	rz := ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(5).
		Cursor(ui.CursorResizeEW).Label("拖动调整弹幕面板宽度")
	if rz.Hovered() || a.panelDragW != 0 {
		// .resizer:hover { background: color-mix(in srgb, var(--accent) 46%, transparent) }
		//
		// 这里必须是 Alpha 而不是 Mix(Transparent, …)：MyGo 的 Mix 是逐通道
		// 不预乘插值，混透明色会把 RGB 一起乘到 46%，拖出来是一条几乎看不见
		// 的暗线；CSS 的 color-mix 预乘，只掉 alpha、颜色本身保持原样。
		rz.Background(theme.Accent.Alpha(0.46))
	}

	_, _, held := rz.Dragged()
	pressed := rz.Pressed()

	if pressed && a.panelDragW == 0 {
		a.panelDragW = a.panelWidth()
		a.panelDragX = ax
	}
	if a.panelDragW == 0 {
		return
	}
	if !pressed && !held {
		a.panelDragW = 0
		return
	}
	// 指针左移 → ax 变小 → 宽度变大
	next := a.panelDragW - (ax - a.panelDragX)
	if next < danmuPanelMinW {
		next = danmuPanelMinW
	}
	if next > danmuPanelMaxW {
		next = danmuPanelMaxW
	}
	if next != a.DanmuPanelW {
		a.DanmuPanelW = next
		a.markDirty()
	}
}

// danmuPanelHandle 是面板左沿那个收起 / 展开小舌。
//
// baseline 的 .panelHandle：20×168 的透明热区垂直居中，里面 20×64 的
// 强调色标签，border-radius 只有靠面板外侧的两个角是圆的，图标 14px /
// 2.8 描边——展开时指向面板里，收起时指回来。
//
// 外层铺满整列并 Center，把固定高度的热区摆到正中，这样不用自己算
// 面板高度（顶栏在不在、是不是全屏都会变）。
func (a *App) danmuPanelHandle(c *ui.Context, open bool) {
	wrap := ui.Box(c).Absolute().Left(-20).Top(0).Bottom(0).Width(20).Center()
	wrap.Children(func() {
		label := "展开弹幕"
		if open {
			label = "收起弹幕"
		}
		hot := ui.Box(c).Width(20).Height(168).Center().
			Cursor(ui.CursorPointer).Label(label)
		if hot.Clicked() {
			a.DanmuPanelOn = !open
			a.markDirty()
		}
		hot.Children(func() {
			// border-radius: 10px 0 0 10px —— 只有靠面板外侧的两个角是圆的
			tab := ui.Box(c).Width(20).Height(64).Radius(10, 0, 0, 10).
				Background(theme.Accent).Center()
			tab.Shadow(0, 8, 20, -10, ui.RGBA(0, 0, 0, 0.7))
			// .panelHandleTab { opacity: 0; transition: opacity var(--dur-2) }
			op := tab.AnimateTo("panel-tab", boolF(hot.Hovered()),
				theme.EaseOut(190*time.Millisecond))
			tab.Opacity(op)
			ico := iconChevronLeftBold
			if open {
				ico = iconChevronRightBold
			}
			tab.Children(func() {
				ui.Icon(c, ico).FontSize(14).TextColor(theme.AccentText)
			})
		})
	})
}

func (a *App) danmuPanelList(c *ui.Context) {
	// 跟随底部；用户上滑后累计新弹幕，浮出「N 条新弹幕」胶囊。
	//
	// 面板读的是 DanmuLog（收到就留的历史），不是 Danmu（画面上正在飞的那批）——
	// baseline 里 messages 和 Overlay 本来就是两份数据，后者飞完就没了。
	a.panelStick = a.PanelScroll.Y >= a.PanelScroll.MaxY-32
	if n := len(a.DanmuLog) - a.panelSeen; n != 0 {
		if n > 0 {
			if a.panelStick {
				a.PanelScroll.Y = math.MaxFloat32
			} else {
				a.panelPending += n
			}
		}
		a.panelSeen = len(a.DanmuLog)
	}

	// 数据层只产出 chat 和 SC，这里不用再过滤——礼物、进场、公告
	// 根本不会进 DanmuLog。只按上限截断。
	list := a.DanmuLog
	if len(list) > danmuPanelMaxLines {
		list = list[len(list)-danmuPanelMaxLines:]
	}

	// 空列表不画占位文案：连接状态在后台，界面根本拿不到「正在连接」
	// 这个事实——连上了只是没人在说话，也会一直挂着那行字。
	ui.Scroll(c).Grow(1).Padding(12, 10, 16, 10).TrackScroll(&a.PanelScroll).
		Children(func() {
			ui.Column(c).FillWidth().Gap(danmuPanelListGap).Children(func() {
				for _, d := range list {
					a.danmuPanelItem(c, d)
				}
			})
		})

	if a.panelPending > 0 {
		pill := ui.Row(c).Height(28).Padding(0, 12, 0, 12).Gap(5).
			Radius(theme.RadiusFull).Background(theme.Accent).Center().
			Cursor(ui.CursorPointer).Label("回到最新弹幕").
			Attach(ui.AnchorBottom, ui.AnchorBottom).Bottom(12)
		if pill.Clicked() {
			a.PanelScroll.Y = math.MaxFloat32
			a.panelPending = 0
		}
		pill.Children(func() {
			// baseline 的胶囊前面有一枚 ArrowDown（12px / 2.6）
			ui.Icon(c, iconArrowDown).FontSize(12).TextColor(theme.AccentText)
			ui.Text(c, fmt.Sprintf("%d 条新弹幕", a.panelPending)).
				FontSize(theme.TextXS).FontWeight(600).TextColor(theme.AccentText)
		})
	}
}

// danmuPanelItem 是面板里的一条弹幕。
//
// baseline 的 DanmuRow 分两种：
//   - chat：两行，上面 Lv 胶囊 + 哈希色昵称，下面正文；
//   - 其它（礼物 / 系统）：一行，`·` + 正文，11px、text-tertiary，上下 padding 压到 4。
//
// 正文的 line-height 是 1.5（继承自 .item），Meta 行也一样——不写的话
// MyGo 的默认行高会把每条弹幕撑得比 baseline 高出一截。
func (a *App) danmuPanelItem(c *ui.Context, d danmuLine) {
	if d.Kind == danmuSuperChat {
		a.danmuPanelSuperChat(c, d)
		return
	}

	item := ui.Column(c).Gap(danmuPanelRowGap).
		FillWidth().
		Padding(danmuPanelPadV, danmuPanelPadH, danmuPanelPadV, danmuPanelPadH).
		Radius(theme.RadiusXS)
	if item.Hovered() {
		item.Background(theme.FillQuaternary)
	}

	item.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
			if d.Level > 0 {
				a.danmuChip(c, fmt.Sprintf("Lv.%d", d.Level))
			}
			// 粉丝牌：名称 + 牌子等级。名称过长时截断——牌子名可以很长，
			// 不能把昵称挤没。
			if d.Medal.Name != "" {
				a.danmuChip(c, fmt.Sprintf("%s %d", d.Medal.Name, d.Medal.Level))
			}
			// .user { nowrap; overflow: hidden; text-overflow: ellipsis }
			// 靠一层 Grow 的盒子给昵称一个上界，长昵称才不会顶出面板。
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, d.Nick).FontSize(theme.Text2XS).FontWeight(600).
					SingleLine().LineHeight(1.5).TextColor(hashColor(d.Nick))
			})
		})

		// 纯图弹幕：整条就是一张表情，正文是空串。
		if d.imageOnly() {
			w, h := emoteSize(d.Emotes[0], theme.TextSM*danmuStickerScale)
			if bmp, wait, anim := media.Default.EmoteFrame(d.Emotes[0].URL, w, c.Now()); bmp != nil {
				ui.Image(c, bmp).Size(w, h)
				if anim && wait > 0 {
					c.After(wait)
				}
			} else {
				// 图还在路上：留一块同尺寸的空位，别让这一行塌掉再回弹
				ui.Box(c).Size(w, h)
			}
			return
		}

		// 正文里的 [表情名] 换成图。没有可渲染的表情时还是普通文本，
		// 不为了一个 U+FFFC 走富文本。
		if rs := buildDanmuSpans(d.Platform, d.Text, d.Emotes, theme.TextSM); rs.hasEmote {
			rs.richText(c, theme.TextSM, theme.TextPrimary).LineHeight(1.5)
			return
		}
		ui.Text(c, d.Text).FontSize(theme.TextSM).LineHeight(1.5).
			TextColor(theme.TextPrimary)
	})
}

// danmuChip 是 meta 行里的小胶囊（用户等级、粉丝牌）。
//
// 名字最多占一行、宽度有上限：粉丝牌名可以很长，不能把昵称挤没；
// 数字用等宽字形，等级从 9 跳到 10 时胶囊不会忽宽忽窄。
func (a *App) danmuChip(c *ui.Context, text string) {
	chip := ui.Box(c).Padding(1, 5, 1, 5).Radius(theme.RadiusFull).
		Background(theme.FillTertiary).Shrink(0).MaxWidth(96)
	chip.Children(func() {
		ui.Text(c, text).FontSize(theme.Text2XS).FontWeight(600).
			SingleLine().LineHeight(1.5).TextColor(theme.TextSecondary).
			FontFeatures("tnum")
	})
}

// danmuPanelSuperChat 是醒目留言那一行。
//
// 它比普通弹幕重一档：整行铺平台下发的背景色（压到 14% 透明度），
// 左侧一条 2px 竖线用原色，金额做成徽章。颜色和金额都来自平台，
// 不自己编——不同档位的 SC 平台给的色本来就不一样。
func (a *App) danmuPanelSuperChat(c *ui.Context, d danmuLine) {
	tint := theme.Accent
	if len(d.Background) == 7 {
		tint = ui.Hex(d.Background)
	}

	item := ui.Column(c).Gap(danmuPanelRowGap).
		FillWidth().
		Padding(danmuPanelPadV, danmuPanelPadH, danmuPanelPadV, danmuPanelPadH).
		Radius(theme.RadiusXS).
		Background(tint.Alpha(0.14))

	item.Children(func() {
		ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(2).
			Radius(1).Background(tint)

		ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
			badge := ui.Box(c).Padding(1, 6, 1, 6).Radius(theme.RadiusFull).
				Background(tint).Shrink(0)
			badge.Children(func() {
				ui.Text(c, fmt.Sprintf("¥%d", d.Price)).
					FontSize(theme.Text2XS).FontWeight(700).
					TextColor(ui.Hex("#0b0c08")).FontFeatures("tnum")
			})
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, d.Nick).FontSize(theme.Text2XS).FontWeight(600).
					SingleLine().LineHeight(1.5).TextColor(hashColor(d.Nick))
			})
		})

		ui.Text(c, d.Text).FontSize(theme.TextSM).LineHeight(1.5).
			TextColor(theme.TextPrimary)
	})
}

// hashColor 复刻 baseline：h = (h*31 + charCode) % 360 → hsl(h 70% 72%)。
func hashColor(seed string) ui.Color {
	h := 0
	for _, r := range seed {
		h = (h*31 + int(r)) % 360
	}
	return hsl(h, 0.70, 0.72)
}

func hsl(h int, s, l float32) ui.Color {
	c := (1 - abs32(2*l-1)) * s
	hp := float32(h%360) / 60
	x := c * (1 - abs32(frac(hp/2)*2-1))
	var r, g, b float32
	switch {
	case hp < 1:
		r, g, b = c, x, 0
	case hp < 2:
		r, g, b = x, c, 0
	case hp < 3:
		r, g, b = 0, c, x
	case hp < 4:
		r, g, b = 0, x, c
	case hp < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	return ui.RGB(
		uint8((r+m)*255+0.5),
		uint8((g+m)*255+0.5),
		uint8((b+m)*255+0.5),
	)
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func frac(v float32) float32 { return v - float32(int(v)) }
