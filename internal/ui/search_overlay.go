package ui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 顶栏搜索浮层。
//
// 浮层挂在搜索按钮下方（top: 100% + 10, right: 0，宽 380, padding 8, 圆角 12）。
// 原生这边用 MyGo 的 PopoverBase：位置由锚点算，
// 点外部 / Esc 关闭也交给它，不用自己写全局监听。
const (
	searchPanelW  float32 = 380
	searchFieldH  float32 = 38
	searchListMax float32 = 300
)

// searchPlaceholder / searchHint 的文案按平台分：
// 四个平台里只有抖音不收关键词，文案也不同。
func searchPlaceholder(platform string) string {
	if platform == "douyin" {
		return "输入抖音直播间号…"
	}
	return "搜索关键词或直播间号…"
}

func searchHint(platform string) string {
	if platform == "douyin" {
		return "抖音仅支持直播间号，输入后按 Enter 进入"
	}
	return "支持关键词与直播间号，↑↓ 选择，Enter 打开"
}

// searchHit 是一条搜索结果：命中项在哪个池子里的下标，以及它本身。
type searchHit struct {
	Remote bool // true 表示来自 a.Remote（接口回来的真实直播间）
	Index  int
	S      Streamer
}

// searchAvailable 报告当前顶栏搜索键是否该出现。
//
// 这颗搜索只搜「当前平台的在播房间」——它按 a.Platform 走接口。而
// 「关注」页聚合的是好几家的主播（关注主播 + 订阅分区），在那上面
// 没有「当前平台」可言，点了只能搜出上一家平台的结果，所以不摆。
//
// 注意：这不等于这两个页面不该有搜索。「在这份列表里找某个主播」
// 照样成立，它们各自带一个页内搜索框（followsBar / subscribeSearch），
// 过滤的是屏幕上这份列表，不发接口。两者的区别是范围，不是有无。
func (a *App) searchAvailable() bool {
	return a.activeTab() != "follows"
}

// authAvailable 报告当前顶栏该不该出现账号入口。
//
// 和 searchAvailable 同一个口径：账号是按平台的，而关注页跨好几家。
// 留在上面就会画出「上一家」的头像，而页面本身不属于那一家。
func (a *App) authAvailable() bool {
	return a.activeTab() != "follows"
}

// searchButton 是顶栏右侧那颗搜索图标按钮，同时是浮层的锚点。
//
// 顶栏不常驻输入框——常驻输入框会把宽度撑开、顶到中间的灵动岛。
func (a *App) searchButton(c *ui.Context) {
	btn := ui.Box(c).Size(chromeH, chromeH).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label("搜索")
	hovered := btn.Hovered()

	switch {
	case a.SearchOpen:
		btn.Background(theme.FillSecondary)
	case hovered:
		btn.Background(theme.FillTertiary)
	}

	if btn.Clicked() {
		a.SearchOpen = !a.SearchOpen
	}

	btn.Children(func() {
		col := theme.TextSecondary
		switch {
		case a.SearchOpen:
			col = theme.AccentInk
		case hovered:
			col = theme.TextPrimary
		}
		ui.Icon(c, iconSearch).FontSize(18).TextColor(col)
	})

	pop := ui.PopoverBase(c, btn, &a.SearchOpen, func(panel ui.Element) {
		panel.Width(a.searchPanelWidth(c)).Padding(8).Gap(6).
			Radius(theme.RadiusMD).Background(theme.BgElevated).
			Border(1, theme.Border).
			Shadow(0, 22, 56, -22, theme.ShadowPanel).Shadow(0, 4, 12, 0, theme.ShadowContact).
			Margin(10, 0, 0, 0).
			AttachTo(btn, ui.AnchorBottomRight, ui.AnchorTopRight)
		a.searchPopover(c)
	})
	if pop.Dismissed() {
		a.SearchQuery = ""
	}
}

func (a *App) searchPanelWidth(c *ui.Context) float32 {
	w, _ := c.Size()
	if w-24 < searchPanelW {
		return w - 24
	}
	return searchPanelW
}

// searchPopover 是浮层内容：输入框 + 结果列表 + 底部提示。
func (a *App) searchPopover(c *ui.Context) {
	// 查询词或平台变了就在这里起一次防抖请求；没变则什么都不做
	a.syncSearch()

	// ---- 输入框：38 高、圆角 8、fill-quaternary 底，描边只在聚焦时出现
	field := ui.Row(c).Height(searchFieldH).Padding(0, 10, 0, 10).Gap(8).
		Radius(theme.RadiusXS).Background(theme.FillQuaternary).Center()
	border := ui.Transparent
	if field.FocusWithin() {
		border = theme.AccentInk
	}
	field.Border(1, border)

	field.Children(func() {
		ui.Icon(c, iconSearch).FontSize(15).TextColor(theme.TextTertiary)
		ui.TextInputBase(c, &a.SearchQuery).FontSize(theme.TextSM).Grow(1).
			Placeholder(searchPlaceholder(a.viewPlatform())).PlaceholderColor(theme.TextTertiary).
			Label("搜索直播间").
			AutoFocus().
			OnTextKey(a.searchKey)
		// 加载中转圈；不在加载且有关键词时才给清空按钮
		switch {
		case a.SearchLoading:
			spinner(c, 14)
		case a.SearchQuery != "":
			clear := ui.Box(c).Size(22, 22).Radius(11).Center().
				Cursor(ui.CursorPointer).Label("清空搜索")
			if clear.Hovered() {
				clear.Background(theme.FillTertiary)
			}
			if clear.Clicked() {
				a.SearchQuery = ""
			}
			clear.Children(func() {
				col := theme.TextTertiary
				if clear.Hovered() {
					col = theme.TextPrimary
				}
				ui.Icon(c, iconX).FontSize(12).TextColor(col)
			})
		}
	})

	// 面板只看「有没有输入」：没结果也要露出来，才能给出空态文案
	if strings.TrimSpace(a.SearchQuery) != "" {
		a.searchResults(c)
	}

	// ---- 底部提示是「平台名 · 平台文案」
	ui.Text(c, fmt.Sprintf("%s · %s", platformLabel(a.viewPlatform()), searchHint(a.viewPlatform()))).
		FontSize(theme.Text2XS).LetterSpacing(0.02).
		TextColor(theme.TextTertiary).Padding(0, 8, 2, 8).SingleLine()
}

// searchResults 是结果区：11px 标题 + 计数胶囊 + 最多 300 高的列表。
//
// 三种内容态：错误原样给出、无结果给空态文案、
// 加载中且还没有结果时只留头部（此时列表是空的）。
func (a *App) searchResults(c *ui.Context) {
	hits := a.searchHits()

	count := fmt.Sprintf("%d 条", len(hits))
	if a.SearchLoading {
		count = "搜索中"
	}
	ui.Row(c).Padding(4, 8, 6, 8).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "搜索结果").FontSize(theme.Text2XS).FontWeight(600).
			LetterSpacing(0.04).TextColor(theme.TextTertiary).Grow(1)
		badge := ui.Row(c).Height(18).Padding(0, 7, 0, 7).Radius(theme.RadiusFull).
			Background(theme.FillQuaternary).Center()
		badge.Children(func() {
			ui.Text(c, count).FontSize(10).FontWeight(600).
				TextColor(theme.TextTertiary).FontFeatures("tnum")
		})
	})

	switch {
	case a.SearchErr != "":
		ui.Text(c, a.SearchErr).FontSize(theme.TextSM).TextColor(theme.TextSecondary).
			TextAlign(ui.Center).Padding(20, 12, 20, 12).Grow(1)
		return
	case len(hits) == 0:
		if a.SearchLoading {
			return
		}
		text := "未找到结果，试试直接输入房间号"
		if a.viewPlatform() == "douyin" {
			text = "未找到该直播间，请检查直播间号"
		}
		ui.Text(c, text).FontSize(theme.TextSM).TextColor(theme.TextSecondary).
			TextAlign(ui.Center).Padding(20, 12, 20, 12).Grow(1)
		return
	}

	ui.Scroll(c).MaxHeight(searchListMax).Children(func() {
		ui.Column(c).Gap(2).Children(func() {
			for i, hit := range hits {
				a.searchItem(c, i, hit)
			}
		})
	})
}

func (a *App) searchItem(c *ui.Context, i int, hit searchHit) {
	active := i == a.SearchHit
	row := ui.Row(c).Padding(8, 10, 8, 10).Gap(11).Radius(theme.RadiusMD).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label("搜索结果:" + hit.S.Name)
	if active {
		row.Background(theme.FillQuaternary)
	}
	// 悬停只影响绘制，不写模型。指针停在结果上时每帧写 SearchHit，会把键盘
	// ↑↓ 刚选中的项覆盖掉——用户看到的是「按 ↑↓ 没反应，挪开鼠标又好了」。
	if !active && row.Hovered() {
		row.Background(theme.FillSecondary)
	}
	if row.Clicked() {
		a.SearchHit = i
		a.openSearchHit(hit)
	}

	row.Children(func() {
		avatarCircle(c, hit.S, 34)
		ui.Column(c).Grow(1).Gap(2).Clip().Children(func() {
			ui.Text(c, hit.S.Name).FontSize(theme.TextSM).FontWeight(600).
				SingleLine().TextColor(theme.TextPrimary)
			ui.Text(c, hit.S.Title).FontSize(theme.TextXS).
				SingleLine().TextColor(theme.TextTertiary)
		})
		// 开播状态点：7×7，在播是绿点 + 2px 18% 绿环
		dot := ui.Box(c).Size(7, 7).Radius(3.5).Background(theme.StatusOffline)
		if hit.S.Live {
			dot.Background(theme.StatusOnline)
			dot.Shadow(0, 0, 0, 2, theme.StatusOnline.Mix(ui.Transparent, 0.82))
		}
	})
}

// searchKey 处理输入框里的方向键 / Enter / Esc。
func (a *App) searchKey(k ui.TextKey) bool {
	hits := a.searchHits()
	switch k.Key {
	case ui.KeyEscape:
		a.SearchQuery = ""
		a.SearchOpen = false
		return true
	case ui.KeyDown:
		if len(hits) == 0 {
			return false
		}
		a.SearchHit = (a.SearchHit + 1) % len(hits)
		return true
	case ui.KeyUp:
		if len(hits) == 0 {
			return false
		}
		a.SearchHit = (a.SearchHit - 1 + len(hits)) % len(hits)
		return true
	case ui.KeyEnter:
		if len(hits) > 0 && a.SearchHit >= 0 && a.SearchHit < len(hits) {
			a.openSearchHit(hits[a.SearchHit])
			return true
		}
		// 没有高亮项时，纯数字直接当房间号进
		if q := strings.TrimSpace(a.SearchQuery); q != "" && isRoomNumber(q) {
			a.openRoomNumber(q)
			return true
		}
		return false
	}
	return false
}

// isRoomNumber 报告关键词是不是一串纯数字（按 /^\d+$/ 判定）。
func isRoomNumber(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// searchHits 是浮层里显示的结果。
//
// 真实数据模式下完全用接口返回的结果；视图测试没有网络，
// 才退回「在当前平台的假数据池里按名字/标题/分区过滤」这套。
func (a *App) searchHits() []searchHit {
	if a.LiveMode {
		hits := make([]searchHit, 0, len(a.SearchResults))
		for i, s := range a.SearchResults {
			hits = append(hits, searchHit{Remote: true, Index: i, S: s})
		}
		if a.SearchHit >= len(hits) {
			a.SearchHit = 0
		}
		return hits
	}

	q := strings.TrimSpace(a.SearchQuery)
	if q == "" {
		if a.searchHitQuery != "" {
			a.searchHitQuery, a.SearchHit = "", 0
		}
		return nil
	}
	if a.searchHitQuery != q {
		a.searchHitQuery, a.SearchHit = q, 0
	}

	var hits []searchHit
	platform := a.viewPlatform()
	collect := func(remote bool, list []Streamer) {
		for i, s := range list {
			if s.Platform != platform {
				continue
			}
			if strings.Contains(s.Name, q) || strings.Contains(s.Title, q) ||
				strings.Contains(s.Category, q) {
				hits = append(hits, searchHit{Remote: remote, Index: i, S: s})
				if len(hits) == 8 {
					return
				}
			}
		}
	}
	collect(false, a.Streamers)
	collect(true, a.Remote)
	if a.SearchHit >= len(hits) {
		a.SearchHit = 0
	}
	return hits
}

func (a *App) openSearchHit(hit searchHit) {
	a.SearchOpen = false
	a.SearchQuery = ""
	a.openRoom(hit.S)
}

// openRoomNumber 用房间号直接进播放页：没有高亮项、而关键词是纯数字时就按号码
// 直达，不等搜索接口。
func (a *App) openRoomNumber(id string) {
	room := Streamer{Platform: a.viewPlatform(), RoomID: id, Name: "直播间 " + id}
	a.SearchOpen = false
	a.SearchQuery = ""
	a.openRoom(room)
}
