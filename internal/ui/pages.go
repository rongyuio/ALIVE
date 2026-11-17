package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 路由路径，一个页面一条。
const (
	routeHome      = "/"
	routeFollows   = "/follows"
	routeSubscribe = "/subscribe"
	routePlayer    = "/player"
)

// Subscription 是订阅分区页顶部的一枚 chip 的数据。
type Subscription struct {
	PlatformID string // 平台 id（douyu / huya / douyin / bilibili），用于匹配
	Platform   string // 2 字平台名，显示在 chip 左侧的小标签里
	Group      string // 一级分区
	Name       string // 二级分区
	// CategoryID 是二级分区的接口 id：订阅页拉列表要用它。
	// 这之前存下来的订阅没有这个字段，加载后会按「平台+分组名+分区名」回填。
	CategoryID string
}

// SubscribePage 是订阅分区页。
//
// 页面结构：不设页头标题、不按平台分组，
// 所有已订阅分区在顶部平铺成一行 chip，把竖向空间尽量留给主播列表。
// 选中哪个 chip 就拉那个分区自己的列表（平台可以和当前顶栏平台不同）。
func (a *App) SubscribePage(c *ui.Context) {
	pad := a.bodyPadX(c)
	ui.Column(c).Grow(1).Padding(0, pad, 0, pad).Gap(12).Children(func() {
		if len(a.Subs) == 0 {
			a.subscribeEmpty(c)
			return
		}
		if a.SubIndex < 0 || a.SubIndex >= len(a.Subs) {
			a.SubIndex = 0
		}
		a.subscribeBar(c)
		// 换订阅项就是换了一份列表：和首页换分区同一个语法。
		// 订阅条留在原地，它是这一屏的边框。
		feed := ui.Column(c).Key(navKey("subs", a.subFeedSeq)).Grow(1).
			Transition(feedTransition(c, a.navDir))
		feed.Children(func() {
			if a.LiveMode {
				a.ensureSubFeed()
				if a.SubErr != "" {
					a.errorBar(c)
				}
				a.grid(c, a.SubRooms, &a.SubList, a.loadMoreSubRooms)
				return
			}
			a.grid(c, a.feedFor(a.Subs[a.SubIndex]), &a.SubList, nil)
		})
	})
}

// subscribeBar 是顶部那条 chip 面板。
func (a *App) subscribeBar(c *ui.Context) {
	bar := ui.Row(c).Padding(8, 12, 10, 12).
		Radius(theme.RadiusMD).Background(theme.BgSecondary)
	// inset 0 1px 0 rgba(255,255,255,.04)：和卡片、灵动岛同一套受光边
	bar.InsetShadow(0, 1, 0, 0, theme.TopLight)

	// 订阅 chip 的宽度从里面量出来：左右内边距、平台小标签、分区名、
	// 取消按钮，外加两段 gap。量法和真实布局必须一致，否则滑块会停在
	// 半个 chip 上。
	box := subscribeBox(c, a.Subs)
	a.subsBox = box

	// 滑块必须挂在一层没有 padding 的 Row 上。
	// Absolute 量的是父级的 padding box（含 padding），挂在 bar 上会从
	// 内边距外面起步——12 的左边距就是滑块比 chip 左移的那 12。
	bar.Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			slider(c, "subs-slide", box, a.SubIndex, 34, theme.RadiusFull, theme.Accent)
			for i, sub := range a.Subs {
				sub, idx := sub, i
				active := idx == a.SubIndex

				chip := ui.Row(c).Height(34).Padding(0, 4, 0, 7).Gap(2).
					Radius(theme.RadiusFull).
					Cursor(ui.CursorPointer).Label("订阅:" + sub.Platform + " " + sub.Name)
				// 选中态由滑块画。未选中不画底，否则会把经过的滑块盖住。
				if !active && chip.Hovered() {
					chip.Background(theme.FillTertiary)
				}
				if chip.Clicked() {
					if idx != a.SubIndex {
						a.resetCards()
						// 换 chip 后列表回到顶部。换的是另一套行，
						// 整份状态重置（只挪偏移会先用旧位置铺新数据，闪一下）。
						a.SubList = ui.ListState{}
						a.navFlip(&a.subFeedSeq, a.SubIndex, idx)
					}
					a.SubIndex = idx
				}

				chip.Children(func() {
					// 平台小标签：高 20，圆角胶囊，字号比标题小一档。
					//
					// 两层都要和底有反差，否则标签等于不存在：未选中时底要比
					// 订阅条亮一档（BgSecondary 和条底一样，等于没画）；选中时
					// 正相反——深色块压在青柠上，字取 accent，读作一枚反色的
					// 印记，而不是青柠上再叠一块青柠。
					tagBg, tagFg := theme.FillTertiary, theme.TextSecondary
					if active {
						tagBg = theme.AccentText
						tagFg = theme.Accent
					}
					tag := ui.Box(c).Height(20).Padding(0, 7, 0, 7).
						Radius(theme.RadiusFull).Background(tagBg).Center()
					tag.Children(func() {
						ui.Text(c, sub.Platform).FontSize(10).FontWeight(600).
							TextColor(tagFg).LetterSpacing(0.02)
					})

					// 分区名
					nameColor := theme.TextPrimary
					if active {
						nameColor = theme.AccentText
					}
					txt := ui.Text(c, sub.Name).FontSize(theme.TextSM).SingleLine().
						Padding(0, 3, 0, 3)
					txt.TextColor(txt.AnimateColor("sub-name", nameColor, theme.Slide()))
					if active {
						txt.FontWeight(600)
					} else {
						txt.FontWeight(500)
					}

					// 取消订阅：默认 50% 透明，悬停才实
					remove := ui.Box(c).Size(24, 24).Radius(12).Center().
						Cursor(ui.CursorPointer).Label("取消订阅:" + sub.Name)
					if remove.Hovered() {
						remove.Background(nameColor.Mix(ui.Transparent, 0.16))
					}
					if remove.Clicked() {
						a.removeSub(idx)
					}
					remove.Children(func() {
						col := nameColor
						if !active {
							col = nameColor.Mix(ui.Transparent, 0.5)
						}
						ui.Icon(c, iconClose).FontSize(11).TextColor(col)
					})
				})
			}
		})
	})
}

// subscribeEmpty 是还没有任何订阅时的空态。
func (a *App) subscribeEmpty(c *ui.Context) {
	ui.Box(c).Grow(1).Center().Children(func() {
		ui.Column(c).Gap(9).Center().Children(func() {
			box := ui.Box(c).Size(64, 64).Radius(theme.RadiusXL).
				Background(theme.AccentSofter).
				Border(1, theme.AccentSoft).Center()
			box.Children(func() {
				ui.Icon(c, iconBookmark).FontSize(26).TextColor(theme.AccentInk)
			})
			ui.Text(c, "还没有订阅任何分区").FontSize(theme.TextLG).
				FontWeight(600).TextColor(theme.TextPrimary).Padding(0, 0, 6, 0)
			ui.Text(c, "进入任意主播列表，在分区栏点击「订阅分区」即可加到这里。").
				FontSize(theme.TextSM).TextColor(theme.TextTertiary)
		})
	})
}

// removeSub 取消一个订阅；当前选中项被删掉时回落到第一个。
func (a *App) removeSub(i int) {
	if i < 0 || i >= len(a.Subs) {
		return
	}
	a.Subs = append(a.Subs[:i], a.Subs[i+1:]...)
	if a.SubIndex >= len(a.Subs) {
		a.SubIndex = max(0, len(a.Subs)-1)
	}
}

// subscribeBox 量订阅条上每个 chip 的格子。
//
// chip 是 4 + 7 的内边距、gap 2、平台小标签（7+7 内边距）、
// 分区名（3+3 内边距）、一个 24 的取消按钮。
func subscribeBox(c *ui.Context, subs []Subscription) rowBox {
	box := rowBox{x: make([]float32, len(subs)), w: make([]float32, len(subs))}
	var x float32
	for i, s := range subs {
		w := 4 + 7 +
			(14 + textWidth(c, s.Platform, 10, 600)) + 2 +
			(6 + textWidth(c, s.Name, theme.TextSM, labelWeight)) + 2 +
			24
		box.x[i], box.w[i] = x, w
		x += w + 6 // bar 的 gap
	}
	return box
}
