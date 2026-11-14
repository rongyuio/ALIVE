package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 路由路径。和 baseline 的 ROUTES 保持同一套形状，方便对照。
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
// 结构对齐 baseline 的 SubscriptionsPage：不设页头标题、不按平台分组，
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
}

// subscribeBar 是顶部那条 chip 面板。
func (a *App) subscribeBar(c *ui.Context) {
	bar := ui.Row(c).Gap(6).Padding(8, 12, 10, 12).
		Radius(theme.RadiusMD).Background(theme.BgSecondary)
	// inset 0 1px 0 rgba(255,255,255,.04)：和卡片、灵动岛同一套受光边
	bar.InsetShadow(0, 1, 0, 0, theme.TopLight)

	bar.Children(func() {
		for i, sub := range a.Subs {
			sub, idx := sub, i
			active := idx == a.SubIndex

			chip := ui.Row(c).Height(34).Padding(0, 4, 0, 7).Gap(2).
				Radius(theme.RadiusFull).Background(theme.FillQuaternary).
				Cursor(ui.CursorPointer)
			if active {
				chip.Background(theme.Accent)
			} else if chip.Hovered() {
				chip.Background(theme.FillTertiary)
			}
			if chip.Clicked() {
				if idx != a.SubIndex {
					a.resetCards()
					// baseline：换 chip 后列表回到顶部
					a.SubList.ScrollTo(0, ui.Start)
				}
				a.SubIndex = idx
			}

			chip.Children(func() {
				// 平台小标签：高 20，圆角胶囊，字号比标题小一档
				tagBg, tagFg := theme.BgSecondary, theme.TextTertiary
				if active {
					tagBg = theme.AccentText.Mix(theme.Accent, 0.90)
					tagFg = theme.AccentText
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
					TextColor(nameColor).Padding(0, 3, 0, 3)
				if active {
					txt.FontWeight(600)
				} else {
					txt.FontWeight(500)
				}

				// 取消订阅：默认 50% 透明，悬停才实
				remove := ui.Box(c).Size(24, 24).Radius(12).Center().
					Cursor(ui.CursorPointer)
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
