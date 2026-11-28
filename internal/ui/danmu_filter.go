package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/theme"
)

// 过滤面板的尺寸。宽度和弹幕设置面板对齐（232）；标签区最高 168，
// 词多了在面板里滚，不把浮层顶出窗口。
const danmuFilterMaxH float32 = 168

// danmuFilterButton 是控制台里的弹幕过滤入口（漏斗图标）。
//
// 只有点击才开，不做 hover 打开：面板里有输入框，指针路过就弹出来会打断
// 正在打字的操作，也不像个能久留的面板。关闭交给 Popover 自己处理——
// 点面板外面、按 Esc、再点一次按钮都收得回去。
func (a *App) danmuFilterButton(c *ui.Context) {
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label("弹幕屏蔽")
	if btn.Clicked() {
		a.DanmuFilterOpen = !a.DanmuFilterOpen
	}
	col := playerIconColor(btn, a.DanmuFilterOpen)
	btn.Children(func() { danmuTVIcon(c, iconDanmuBadgeFilter, col, col) })

	components.Popover(c, btn, &a.DanmuFilterOpen, func(panel ui.Element) {
		panel.Width(danmuPanelW).Padding(14, 16, 14, 16).Radius(16).
			Border(1, theme.MediaHair).
			Shadow(0, 18, 40, 0, theme.MediaShadow).
			Margin(0, 0, 10, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		panel = mediaPanel(c, panel)
		panel.Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				a.danmuFilterHeader(c)
				// 输入框与已加的词语合成一个标签输入：回车 / 逗号 / 失焦提交，
				// 点药丸上的叉删除，去重忽略大小写（tag_input.go:176 的
				// slices.IndexFunc + EqualFold）——和原来 addDanmuFilter 那套
				// 「同一个词不重复加」是同一条规则，只是搬进了组件。
				//
				// 词多的时候在面板里滚：TagInput 自己不限高，套一层 Scroll
				// 才顶得住长列表。
				components.ScrollView(c, components.ScrollViewOptions{}).FillWidth().MaxHeight(danmuFilterMaxH).
					TrackScroll(&a.danmuFilterScroll).Children(func() {
					tag := components.TagInput(c, &a.DanmuFilters)
					tag.Label("弹幕屏蔽词").Placeholder("输入关键词，回车添加")
					tag.Background(theme.MediaRow).Border(1, theme.MediaRowLine)
					if tag.Changed() {
						a.markDirty()
					}
				})
			})
		})
	})
}

func (a *App) danmuFilterHeader(c *ui.Context) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
		components.Text(c, "屏蔽词").FontSize(theme.TextXS).FontWeight(600).
			TextColor(theme.MediaText).Grow(1)
		components.Text(c, fmt.Sprintf("%d 个", len(a.DanmuFilters))).
			FontSize(theme.Text2XS).TextColor(theme.Accent)
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
