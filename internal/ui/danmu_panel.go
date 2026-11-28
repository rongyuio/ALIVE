package ui

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 右侧弹幕列表的尺寸：默认 196 宽（可拖 168–320），列表 padding 12 10 16、
// 行 gap 3、每条 6×9 padding、圆角 8、13px / line-height 1.5。
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
// 收起时它仍然占一个零宽的位置——面板本身不渲染，但边缘把手始终在，
// 鼠标移到右边缘才浮出小舌。之前我把把手写在面板内部，一收起就整个
// 消失，再没有入口能把它叫回来。
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

// danmuResizer 是面板左沿 5px 的可拉伸热区。
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

	// 指针被系统抢走（PressCanceled）：显式复位，不靠「pressed/held 同时为假」
	// 这个巧合。窗口失焦、触摸被中断都会走到这里。
	if rz.PressCanceled() {
		a.panelDragW = 0
		return
	}
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
// 面板左沿的把手：20×168 的透明热区垂直居中，里面 20×64 的
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
			tab.Shadow(0, 8, 20, -10, theme.MediaShadowStrong)
			// .panelHandleTab { opacity: 0; transition: opacity var(--dur-2) }
			op := tab.AnimateTo("panel-tab", boolF(hot.Hovered()),
				theme.Chrome())
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
	// 贴底由 ListState.FollowEnd 自己管：贴底时新弹幕把底推上来，用户上滑就脱离、
	// 滑回底部又跟上，视口变高变矮也不用谁来重新钉 Y。这里只负责在脱离时累计
	// 「N 条新弹幕」。
	n := a.panelNewDanmu()
	if a.PanelList.AtEnd() {
		a.panelPending = 0
	} else if n > 0 {
		a.panelPending += n
	}

	// 数据层只产出 chat 和 SC，这里不用再过滤——礼物、进场、公告
	// 根本不会进 DanmuLog。只按上限截断。
	list := a.DanmuLog
	if len(list) > danmuPanelMaxLines {
		list = list[len(list)-danmuPanelMaxLines:]
	}
	// Key 不捕获这一帧的局部 list。它被存进长期 ListState：面板收起后
	// danmuPanelList 不再执行，闭包一直是旧快照；任何在别的路径上对这个
	// ListState 调 Key 的地方（ScrollToEnd 一族）都会拿到过期切片并越界。
	a.PanelList.Key = func(i int) any {
		log := a.DanmuLog
		if len(log) > danmuPanelMaxLines {
			log = log[len(log)-danmuPanelMaxLines:]
		}
		if i < 0 || i >= len(log) {
			return i
		}
		return log[i].Seq
	}

	// 空列表不画占位文案：连接状态在后台，界面根本拿不到「正在连接」
	// 这个事实——连上了只是没人在说话，也会一直挂着那行字。
	ui.List(c, &a.PanelList, len(list), func(i int) {
		a.danmuPanelItem(c, list[i])
	}).Grow(1).Padding(12, 10, 16, 10).Gap(danmuPanelListGap)

	if a.panelPending > 0 {
		pill := ui.Row(c).Height(28).Padding(0, 12, 0, 12).Gap(5).
			Radius(theme.RadiusFull).Background(theme.Accent).Center().
			Cursor(ui.CursorPointer).Label("回到最新弹幕").
			Attach(ui.AnchorBottom, ui.AnchorBottom).Bottom(12)
		if pill.Clicked() {
			a.PanelList.ScrollToEnd()
			a.panelPending = 0
		}
		pill.Children(func() {
			// 胶囊前面有一枚 ArrowDown（12px / 2.6）
			ui.Icon(c, iconArrowDown).FontSize(12).TextColor(theme.AccentText)
			ui.Text(c, fmt.Sprintf("%d 条新弹幕", a.panelPending)).
				FontSize(theme.TextXS).FontWeight(600).TextColor(theme.AccentText)
		})
	}
}

// panelLineMark 是「上次结算时最后看到的那一条」的身份。
//
// danmuLine 带 Emotes 切片，不能整体比较；只取随每条消息变化、又足够
// 区分相邻几条的标量。同一毫秒收到两条完全相同的弹幕时可能少记一条，
// 但对「N 条新弹幕」这个提示没有可见影响。
type panelLineMark struct {
	text, nick string
	born       time.Time
	track      int
}

func panelMarkOf(d danmuLine) panelLineMark {
	return panelLineMark{text: d.Text, nick: d.Nick, born: d.Born, track: d.Track}
}

// panelNewDanmu 返回自上次结算以来新收到的弹幕条数，并把结算点挪到末尾。
//
// 不能用 len(DanmuLog) - panelSeen 算：DanmuLog 到 danmuLogMax（300）后
// 长度恒定，差值永远是 0，新弹幕照进，面板却既不跟随也不累计未读。
// 这里记住上次看到的最后一条，从尾部往前找它：它后面都是新的；找不到
// 说明它已经被挤出窗口，或者整份日志被清空换台，就把当前窗口全算新。
func (a *App) panelNewDanmu() int {
	log := a.DanmuLog
	if len(log) == 0 {
		a.panelSeen, a.panelMark = 0, panelLineMark{}
		return 0
	}

	n := 0
	switch {
	case a.panelSeen == 0 || len(log) < a.panelSeen:
		// 还没有基准，或日志短于上次结算（被清空过）：窗口里全部是新的。
		n = len(log)
	default:
		found := -1
		for i := len(log) - 1; i >= 0; i-- {
			if panelMarkOf(log[i]) == a.panelMark {
				found = i
				break
			}
		}
		if found >= 0 {
			n = len(log) - 1 - found
		} else {
			n = len(log)
		}
	}

	a.panelSeen = len(log)
	a.panelMark = panelMarkOf(log[len(log)-1])
	return n
}

// danmuPanelItem 是面板里的一条弹幕。
//
// 弹幕行分两种：
//   - chat：两行，上面 Lv 胶囊 + 昵称，下面正文；
//   - 其它（礼物 / 系统）：一行，`·` + 正文，11px、text-tertiary，上下 padding 压到 4。
//
// 正文的 line-height 是 1.5（继承自 .item），Meta 行也一样——不写的话
// MyGo 的默认行高会把每条弹幕撑得高出一截。
func (a *App) danmuPanelItem(c *ui.Context, d danmuLine) {
	if d.Kind == danmuSuperChat {
		a.danmuPanelSuperChat(c, d)
		return
	}

	item := ui.Column(c).Gap(danmuPanelRowGap).
		FillWidth().
		Padding(danmuPanelPadV, danmuPanelPadH, danmuPanelPadV, danmuPanelPadH).
		Radius(theme.RadiusXS)
	item.Background(hoverSurface(item, "danmu-item-bg", item.Hovered(), theme.FillQuaternary))

	item.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Baseline).Children(func() {
			if d.Level > 0 {
				a.danmuChip(c, fmt.Sprintf("Lv.%d", d.Level), levelChipColor(d.Platform, d.Level), ui.Color{})
			}
			// 粉丝牌：名称 + 牌子等级。名称过长时截断——牌子名可以很长，
			// 不能把昵称挤没。
			if d.Medal.Name != "" {
				bg, bgEnd := medalChipColors(d)
				a.danmuChip(c, fmt.Sprintf("%s %d", d.Medal.Name, d.Medal.Level), bg, bgEnd)
			}
			// .user { nowrap; overflow: hidden; text-overflow: ellipsis }
			// 靠一层 Grow 的盒子给昵称一个上界，长昵称才不会顶出面板。
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, d.Nick).FontSize(theme.Text2XS).FontWeight(600).
					SingleLine().LineHeight(1.5).TextColor(theme.TextPrimary)
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
		//
		// 正文颜色跟「彩色弹幕」开关走：开了用平台下发的原生字色（白映射回
		// 主题正文色，浅色主题下才是白字压白底），关了统一用主题正文色。
		fg := danmuColor(d, a.DanmuColorful, theme.TextPrimary)
		if rs := buildDanmuSpans(d.Platform, d.Text, d.Emotes, theme.TextSM); rs.hasEmote {
			rs.richText(c, theme.TextSM, fg).LineHeight(1.5)
			return
		}
		ui.Text(c, d.Text).FontSize(theme.TextSM).LineHeight(1.5).
			TextColor(fg)
	})
}

// danmuChip 是 meta 行里的小胶囊（用户等级、粉丝牌）。
//
// 名字最多占一行、宽度有上限：粉丝牌名可以很长，不能把昵称挤没；
// 数字用等宽字形，等级从 9 跳到 10 时胶囊不会忽宽忽窄。
//
// bg 是平台徽章底色，零值表示平台没有对应配色规则，用中性胶囊；
// bgEnd 非零且与 bg 不同时按平台的 45° 渐变画（B 站 v2 双色牌子）。
//
// 字色不按底色深浅变：有平台底色就是纯白，四个平台网页端都这么画。
// 之前按亮度给浅底白字加过一圈 1 DIP、黑 45% 的描边，在 11px 中文上
// 直接糊成黑框，浅橙 / 浅金牌子的字看着像被圈了一圈。
func (a *App) danmuChip(c *ui.Context, text string, bg, bgEnd ui.Color) {
	chip := ui.Box(c).Padding(1, 5, 1, 5).Radius(theme.RadiusFull).
		Shrink(0).MaxWidth(96)
	fg := chipTextColor(bg)
	if bg.A != 0 {
		if bgEnd.A != 0 && bgEnd != bg {
			chip.Gradient(bg, bgEnd, 45)
		} else {
			chip.Background(bg)
		}
	} else {
		chip.Background(theme.FillTertiary)
	}
	chip.Children(func() {
		ui.Text(c, text).FontSize(theme.Text2XS).FontWeight(600).
			SingleLine().LineHeight(1.5).TextColor(fg).
			FontFeatures("tnum")
	})
}

// chipTextColor 是胶囊上的字色。
//
// 有平台底色：纯白，不描边、不按亮度换深字——底色是平台定的，网页端
// 也是白字直铺，我们自己加描边只会在 11px 中文笔画间糊出黑框。
// 没有平台配色（中性胶囊）时退一档灰，不抢平台上真牌子的位置。
func chipTextColor(bg ui.Color) ui.Color {
	if bg.A == 0 {
		return theme.TextSecondary
	}
	return theme.White
}

// validHexRGB 检查平台下发的是不是合法的 #RRGGBB。
//
// ui.Hex 遇到非十六进制字符直接 panic，而醒目留言的背景色是平台 JSON
// 里的任意字符串（B 站只校验过长度和 '#' 前缀）。不合法就退回强调色，
// 不能让一条 '#ZZZZZZ' 打崩整个进程。
func validHexRGB(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// danmuPanelSuperChat 是醒目留言那一行。
//
// 它比普通弹幕重一档：整行铺平台下发的背景色（压到 14% 透明度），
// 左侧一条 2px 竖线用原色，金额做成徽章。颜色和金额都来自平台，
// 不自己编——不同档位的 SC 平台给的色本来就不一样。
func (a *App) danmuPanelSuperChat(c *ui.Context, d danmuLine) {
	tint := theme.Accent
	if validHexRGB(d.Background) {
		tint = theme.HexColor(d.Background)
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
				// 金额徽章固定白字：四个平台的 SC 协议都不下发字色（B 站只给
				// 背景色系、斗鱼只给 danmucr 头色、虎牙只给背景色），网页端也是一
				// 律白字。按亮度分深浅会让同一条列表里 ¥10 白字、¥100 黑字。
				ui.Text(c, fmt.Sprintf("¥%d", d.Price)).
					FontSize(theme.Text2XS).FontWeight(700).
					TextColor(theme.White).FontFeatures("tnum")
			})
			ui.Box(c).Grow(1).Children(func() {
				ui.Text(c, d.Nick).FontSize(theme.Text2XS).FontWeight(600).
					SingleLine().LineHeight(1.5).TextColor(theme.TextPrimary)
			})
		})

		ui.Text(c, d.Text).FontSize(theme.TextSM).LineHeight(1.5).
			TextColor(theme.TextPrimary)
	})
}
