package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 过滤面板的尺寸。宽度和弹幕设置面板对齐（232）；输入框 34 高，
// 列表最高 168——词多了在面板里滚，不把浮层顶出窗口。
const (
	danmuFilterFieldH float32 = 34
	danmuFilterMaxH   float32 = 168
)

// danmuFilterButton 是控制台里的弹幕过滤入口（漏斗图标）。
//
// 只有点击才开，不做 hover 打开：面板里有输入框，指针路过就弹出来会打断
// 正在打字的操作，也不像个能久留的面板。关闭交给 PopoverBase 自己处理——
// 点面板外面、按 Esc、再点一次按钮都收得回去。
func (a *App) danmuFilterButton(c *ui.Context) {
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label("弹幕屏蔽")
	if btn.Clicked() {
		a.DanmuFilterOpen = !a.DanmuFilterOpen
	}
	col := playerIconColor(btn, a.DanmuFilterOpen)
	btn.Children(func() { danmuTVIcon(c, iconDanmuBadgeFilter, col, col) })

	ui.PopoverBase(c, btn, &a.DanmuFilterOpen, func(panel ui.Element) {
		panel.Width(danmuPanelW).Padding(14, 16, 14, 16).Radius(16).
			Background(ui.RGBA(17, 20, 28, 0.88)).
			Border(1, ui.RGBA(255, 255, 255, 0.12)).
			Shadow(0, 18, 40, 0, ui.RGBA(0, 0, 0, 0.4)).
			Margin(0, 0, 10, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		panel.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				a.danmuFilterHeader(c)
				a.danmuFilterInput(c)
				a.danmuFilterList(c)
			})
		})
	})
}

func (a *App) danmuFilterHeader(c *ui.Context) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		ui.Text(c, "屏蔽词").FontSize(theme.TextXS).FontWeight(600).
			TextColor(ui.RGBA(255, 255, 255, 0.92)).Grow(1)
		ui.Text(c, fmt.Sprintf("%d 个", len(a.DanmuFilters))).
			FontSize(theme.Text2XS).TextColor(theme.Accent)
	})
}

// danmuFilterInput 是关键词输入框：回车添加，Esc 收起面板。
func (a *App) danmuFilterInput(c *ui.Context) {
	field := ui.Row(c).Height(danmuFilterFieldH).Padding(0, 10, 0, 10).Gap(8).
		Radius(theme.RadiusXS).Background(ui.RGBA(255, 255, 255, 0.05)).Center()
	border := ui.RGBA(255, 255, 255, 0.08)
	if field.FocusWithin() {
		border = theme.AccentInk
	}
	field.Border(1, border)

	field.Children(func() {
		ui.Icon(c, iconFilter).FontSize(13).TextColor(ui.RGBA(255, 255, 255, 0.4))
		ui.TextInputBase(c, &a.DanmuFilterQuery).FontSize(theme.TextXS).Grow(1).
			Placeholder("输入关键词，回车添加").
			PlaceholderColor(ui.RGBA(255, 255, 255, 0.35)).
			OnTextKey(a.danmuFilterKey)
	})
}

func (a *App) danmuFilterKey(k ui.TextKey) bool {
	switch k.Key {
	case ui.KeyEnter:
		a.addDanmuFilter()
		return true
	case ui.KeyEscape:
		a.DanmuFilterOpen = false
		return true
	}
	return false
}

func (a *App) addDanmuFilter() {
	word := strings.TrimSpace(a.DanmuFilterQuery)
	if word == "" {
		return
	}
	a.DanmuFilterQuery = ""
	// 同一个词（忽略大小写）不重复加：过滤是包含匹配，重复项没有意义
	for _, w := range a.DanmuFilters {
		if strings.EqualFold(w, word) {
			return
		}
	}
	a.DanmuFilters = append(a.DanmuFilters, word)
	a.markDirty()
}

func (a *App) removeDanmuFilter(i int) {
	if i < 0 || i >= len(a.DanmuFilters) {
		return
	}
	a.DanmuFilters = append(a.DanmuFilters[:i], a.DanmuFilters[i+1:]...)
	a.markDirty()
}

func (a *App) danmuFilterList(c *ui.Context) {
	// 空列表不画任何占位文案：面板就剩标题和输入框，一眼就知道还没加词。
	if len(a.DanmuFilters) == 0 {
		return
	}
	ui.Scroll(c).FillWidth().MaxHeight(danmuFilterMaxH).
		TrackScroll(&a.danmuFilterScroll).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			for i, word := range a.DanmuFilters {
				a.danmuFilterRow(c, i, word)
			}
		})
	})
}

func (a *App) danmuFilterRow(c *ui.Context, i int, word string) {
	row := ui.Row(c).FillWidth().Padding(6, 8, 6, 8).Gap(8).Radius(theme.RadiusXS).
		AlignItems(ui.Center).
		Background(ui.RGBA(255, 255, 255, 0.05)).
		Border(1, ui.RGBA(255, 255, 255, 0.08))
	if row.Hovered() {
		row.Background(ui.RGBA(255, 255, 255, 0.08))
	}
	row.Children(func() {
		// 长词截断：面板只有 200 宽，一个超长关键词不能把删除按钮挤出去
		ui.Box(c).Grow(1).Children(func() {
			ui.Text(c, word).FontSize(theme.TextXS).
				TextColor(ui.RGBA(255, 255, 255, 0.88)).SingleLine()
		})
		del := ui.Box(c).Size(18, 18).Radius(9).Center().
			Cursor(ui.CursorPointer).Label("删除屏蔽词 " + word)
		if del.Hovered() {
			del.Background(ui.RGBA(255, 255, 255, 0.12))
		}
		if del.Clicked() {
			a.removeDanmuFilter(i)
		}
		del.Children(func() {
			ui.Icon(c, iconX).FontSize(11).TextColor(ui.RGBA(255, 255, 255, 0.6))
		})
	})
}

// danmuBlocked 报告一条弹幕是否命中屏蔽词。
//
// 命中就整条丢：既不上屏也不进右侧历史——只挡画面、面板里照常滚动，
// 等于没过滤。
func (a *App) danmuBlocked(text string) bool {
	if len(a.DanmuFilters) == 0 || text == "" {
		return false
	}
	low := strings.ToLower(text)
	for _, word := range a.DanmuFilters {
		if word == "" {
			continue
		}
		if strings.Contains(low, strings.ToLower(word)) {
			return true
		}
	}
	return false
}
