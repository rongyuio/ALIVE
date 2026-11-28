package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 分区栏的全部尺寸都在这里定，不要改写成整十数。
const (
	groupTabH    float32 = 34
	groupTabPadH float32 = 14
	groupGap     float32 = 4
	groupFont    float32 = 14 // .groupTab font-size: 14px（不在字号阶梯上，就取 14）

	chipH    float32 = 32
	chipPadH float32 = 14
	chipGap  float32 = 8
	chipMaxW float32 = 200

	catSearchH    float32 = 34
	catSearchW    float32 = 176
	catSearchWide float32 = 220
	catPanelW     float32 = 300
	catPanelMaxH  float32 = 320

	morePanelMaxW float32 = 720
	morePanelMaxH float32 = 400
	moreItemMinW  float32 = 108
	moreItemGap   float32 = 8
	moreItemH     float32 = 38
)

// 浮层阴影统一走 theme.ShadowPanel + ShadowContact（随明暗主题切换）。

// categoryHit 是分区搜索的一条结果：命中的二级分区连同它所属的一级分组。
type categoryHit struct {
	GroupIndex    int
	CategoryIndex int
	Group         string
	Category      string
}

// ---------------------------------------------------------------- 选择状态

func (a *App) currentGroup() CategoryGroup {
	if len(a.Groups) == 0 {
		return CategoryGroup{}
	}
	if a.GroupIndex < 0 || a.GroupIndex >= len(a.Groups) {
		return a.Groups[0]
	}
	return a.Groups[a.GroupIndex]
}

func (a *App) currentCategories() []string { return a.currentGroup().Categories }

func (a *App) currentCategory() string {
	cats := a.currentCategories()
	if len(cats) == 0 {
		return ""
	}
	if a.CategoryIndex < 0 || a.CategoryIndex >= len(cats) {
		return cats[0]
	}
	return cats[a.CategoryIndex]
}

func (a *App) selectGroup(i int) {
	if i < 0 || i >= len(a.Groups) {
		return
	}
	// 点的就是当前这一组、而且已经停在它的第一个二级分区上：这是一次
	// 空操作。仍然换代会把内容层整棵子树重建一遍（换 key + 重拉列表）——
	// 表现是「同一个分区点一下，内容重铺/滚动位置跳」，而且刚按下的卡片
	// 会和松手时的不是同一个元素，点击就此丢掉。
	if i == a.GroupIndex && a.CategoryIndex == 0 {
		a.MoreOpen = false
		return
	}
	a.navFlip(&a.navSeq)
	// 切一级分区时自动选中它的第一个二级分区。
	a.GroupIndex = i
	a.CategoryIndex = 0
	a.MoreOpen = false
	a.reloadRooms()
}

func (a *App) selectCategory(i int) {
	if i == a.CategoryIndex {
		// 同上：点已选中的二级分区是空操作，不能重建内容层。
		a.MoreOpen = false
		a.CategoryQuery = ""
		a.CategorySearchOpen = false
		return
	}
	a.navFlip(&a.navSeq)
	a.CategoryIndex = i
	a.MoreOpen = false
	a.CategoryQuery = ""
	a.CategorySearchOpen = false
	a.reloadRooms()
}

// ---------------------------------------------------------------- 订阅

func platformLabel(id string) string {
	switch id {
	case "douyu":
		return "斗鱼"
	case "huya":
		return "虎牙"
	case "douyin":
		return "抖音"
	case "bilibili":
		return "B站"
	}
	return id
}

// subscribed 报告当前平台 + 一级分区 + 二级分区是否已订阅。
func (a *App) subscribed() bool {
	group, category := a.currentGroup().Name, a.currentCategory()
	for _, s := range a.Subs {
		if s.PlatformID == a.Platform && s.Group == group && s.Name == category {
			return true
		}
	}
	return false
}

// toggleSubscribe 订阅 / 取消订阅当前分区。
func (a *App) toggleSubscribe() {
	group, category := a.currentGroup().Name, a.currentCategory()
	if category == "" {
		return
	}
	if a.subscribed() {
		// 新分配，不原地压缩：[:0] 会把底层数组左移，而同一帧里还有别人
		// 握着旧切片头的遍历者（订阅行、菜单、导出快照），它们会读到错位
		// 的内容。watchMedia 的墙格删除早就因为这条改成新分配了。
		kept := make([]Subscription, 0, len(a.Subs))
		for _, s := range a.Subs {
			if s.PlatformID == a.Platform && s.Group == group && s.Name == category {
				continue
			}
			kept = append(kept, s)
		}
		a.Subs = kept
		if a.subActive[subChipKey(category)] == a.Platform {
			delete(a.subActive, subChipKey(category))
		}
		a.clampSubIndex()
		a.toastTone("已取消订阅该分区", toastSuccess)
		a.markDirty()
		return
	}
	a.Subs = append(a.Subs, Subscription{
		PlatformID: a.Platform,
		Platform:   platformLabel(a.Platform),
		Group:      group,
		Name:       category,
		CategoryID: a.currentCategoryID(),
	})
	// 刚订阅的这家就是这人想看的：把这枚 chip 的活动平台指过去，
	// 否则它会默认落在顶栏顺序的第一家，看起来像「订了没反应」。
	a.subActive[subChipKey(category)] = a.Platform
	a.toastTone("已订阅该分区", toastSuccess)
	a.markDirty()
}

// ---------------------------------------------------------------- 布局

// categoryBar 是分区栏：一级文字 tab + 二级 chip + 三级 chip（仅斗鱼）。
//
// 一整块面板和卡片同一块材质：radius 16 + 受光边，不描边。
// 三行之间 gap 10，行内一律横滑、永不换行。
func (a *App) categoryBar(c *ui.Context) {
	if len(a.Groups) == 0 {
		return
	}
	panel := ui.Column(c).Gap(10).Padding(12, 14, 14, 14).
		Radius(theme.RadiusXL).
		Background(theme.BgSecondary)
	panel.Shadow(0, 1, 0, 0, theme.TopLight)

	panel.Children(func() {
		a.primaryRow(c)
		a.secondaryRow(c)
	})
}

// primaryRow 是一级分区行：左侧可横滑的分组 tab，右侧分区搜索框。
func (a *App) primaryRow(c *ui.Context) {
	names := make([]string, len(a.Groups))
	for i, g := range a.Groups {
		names[i] = g.Name
	}
	// 滑块要有一个跟着内容走的定位基准，所以横滑容器里再套一层 Row：
	// 绝对定位的元素量的是它父级的边，挂在 ScrollHorizontal 上会在
	// 滚动时和内容错开。
	box := measureRow(c, names, groupFont, labelWeight, groupTabPadH, groupGap, 0)

	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		// 这一行是 overflow-x:auto + 隐藏滚动条，只是能横滑
		ui.ScrollHorizontal(c).Scrollbars(false).Grow(1).Padding(2).Children(func() {
			ui.Row(c).Gap(groupGap).Children(func() {
				slider(c, "group-slide", box, a.GroupIndex, groupTabH, theme.RadiusMD, theme.AccentSoft)
				for i, g := range a.Groups {
					a.groupTab(c, i, g.Name, box.w[i])
				}
			})
		})
		a.categorySearch(c)
	})
}

// labelWeight 是给分区标签量宽度时用的字重：按最宽的那个状态量。
//
// 选中态是 600、其余 500。必须按 600 量——按 500 量的话，一选中字就变宽，
// 单行文字会被自己的容器截成「鸣…」，而滑块还停在正确的格子上，很难对上号。
// 宽度定死之后，选中不再改变格子大小，后面的项也不会跟着挪。
const labelWeight float32 = 600

func (a *App) groupTab(c *ui.Context, i int, name string, w float32) {
	selected := i == a.GroupIndex
	// 自绘 Box 不进无障碍树，没有 Label 时自动化只能按坐标点。
	tab := ui.Box(c).Height(groupTabH).
		Padding(0, groupTabPadH, 0, groupTabPadH).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).Label("分区:" + name)
	// 选中态的底色由下面的滑块画：同一块材质移过去，而不是旧的白一下、
	// 新的亮一下。这里只留 hover 的反应，而且选中项不叠 hover 底，
	// 否则会把滑块盖掉半块。
	// 悬停底色和文字色用同一个动效：底色瞬间跳、文字在弹，读起来是两种反馈。
	if !selected {
		tab.Background(hoverSurface(tab, "group-bg", tab.Hovered(), theme.FillQuaternary))
	}
	if tab.Clicked() {
		a.selectGroup(i)
	}
	tab.Children(func() {
		txt := ui.Text(c, name).FontSize(groupFont)
		switch {
		case selected:
			txt.FontWeight(600).
				TextColor(txt.AnimateColor("group-ink", theme.AccentInk, theme.Slide()))
		case tab.Hovered():
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("group-ink", theme.TextPrimary, theme.Slide()))
		default:
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("group-ink", theme.TextSecondary, theme.Slide()))
		}
	})
}

// categorySearch 是分区搜索：胶囊输入框 + 结果浮层。
//
// 搜索框常态宽 176，聚焦或有输入时 240ms 过渡到 220；边框在聚焦时变 accent-ink。
func (a *App) categorySearch(c *ui.Context) {
	field := ui.Row(c).Height(catSearchH).Padding(0, 8, 0, 12).Gap(7).
		Radius(theme.RadiusFull).Background(theme.FillQuaternary).
		Center().Cursor(ui.CursorText).Label("分区搜索框")

	// 输入框拿到焦点、或者用户又改了词，就要展开到 220 并给出结果面板。
	//
	// 两个条件缺一不可：
	//
	//   - 焦点进来这个**边沿**：不能每帧按 FocusWithin 无条件写，写在渲染
	//     路径里等于每次 build 都重开，Esc / Enter 关掉的下一帧就被翻回来。
	//   - 查询词变了：Esc 收起面板、选中一条结果之后焦点**还在框里**
	//     （Handle.CancelFocus 只清「待处理的聚焦请求」，放不掉已经生效的
	//     焦点，third_party/mygo/ui/ref.go），边沿永远不会再来。只认边沿的话
	//     「Esc 关掉后再打字」和「点了一条结果后接着改词」这两种最常见的连击
	//     都不出面板——输入框收窄、一个字不显示结果。
	//
	// 只在词非空时按"变了"重开：Esc 会把词清成空串，那一步不能算用户又想搜。
	focused := field.FocusWithin()
	if focused && (!a.catSearchFocused || (a.CategoryQuery != "" && a.CategoryQuery != a.catSearchQuery)) {
		a.CategorySearchOpen = true
	}
	a.catSearchFocused = focused
	a.catSearchQuery = a.CategoryQuery
	width := catSearchW
	if a.CategorySearchOpen || a.CategoryQuery != "" {
		width = catSearchWide
	}

	// 静态不描边：填充和卡片差 8.8 个 L*，形状自己就站得住；描边只留给聚焦。
	// .catSearch { transition: width 240ms var(--ease-out) }
	// .catField:focus-within { border-color: accent-ink }，190ms
	border := ui.Transparent
	if field.FocusWithin() {
		border = theme.AccentInk
	}
	field.Border(1, field.AnimateColor("cat-search-border", border,
		theme.Chrome()))
	field.Width(field.AnimateTo("cat-search-w", width,
		theme.Reveal()))

	field.Children(func() {
		ui.TextInputBase(c, &a.CategoryQuery).
			Bind(&a.CategorySearchHandle).
			FontSize(theme.TextXS).Grow(1).
			Placeholder("搜索分区").PlaceholderColor(theme.TextTertiary).
			Label("搜索分区").
			OnTextKey(func(k ui.TextKey) bool { return a.categorySearchKey(c, k) })

		if a.CategoryQuery != "" {
			clear := ui.Box(c).Size(18, 18).Radius(9).Center().
				Cursor(ui.CursorPointer).Label("清空分区搜索")
			clear.Background(hoverSurface(clear, "cat-clear-bg", clear.Hovered(), theme.FillTertiary))
			if clear.Clicked() {
				// 清空后焦点还给输入框，保持展开
				a.CategoryQuery = ""
				a.CategorySearchHandle.Focus(c)
			}
			clear.Children(func() {
				col := hoverInk(clear, "cat-clear-ink", clear.Hovered(), theme.TextTertiary, theme.TextPrimary)
				ui.Icon(c, iconX).FontSize(12).TextColor(col)
			})
		}

		ui.Icon(c, iconSearch).FontSize(14).TextColor(theme.TextTertiary)
	})

	// 浮层始终跟着输入框的展开状态走：空查询时渲染成 0×0 的不可见占位，
	// 这样点外部仍会通过 PopoverBase 关闭并把焦点放掉，输入框才会收回去。
	pop := ui.PopoverBase(c, field, &a.CategorySearchOpen, func(panel ui.Element) {
		if strings.TrimSpace(a.CategoryQuery) == "" {
			panel.Width(0).Height(0).Padding(0)
			return
		}
		panel.Width(catPanelW).Padding(6).Radius(theme.RadiusMD).
			Background(theme.BgElevated).Border(1, theme.Border).
			Shadow(0, 22, 56, -22, theme.ShadowPanel).Shadow(0, 4, 12, 0, theme.ShadowContact).
			Margin(8, 0, 0, 0).
			AttachTo(field, ui.AnchorBottomRight, ui.AnchorTopRight)
		a.categorySearchPanel(c)
	})
	if pop.Dismissed() {
		// 点外部关掉浮层：清空查询，并且把焦点一起放掉，
		// 否则下一帧「聚焦就展开」的逻辑会把面板又打开。
		a.CategoryQuery = ""
		a.CategorySearchOpen = false
		a.CategorySearchHandle.CancelFocus(c)
	}
}

// categorySearchKey 处理输入框里的方向键 / Enter / Esc。
// 返回值 true 表示这次按键被输入框吃掉，不再传给窗口。
func (a *App) categorySearchKey(c *ui.Context, k ui.TextKey) bool {
	hits := a.categoryHits()
	switch k.Key {
	case ui.KeyEscape:
		a.CategoryQuery = ""
		a.CategorySearchOpen = false
		// 焦点要一起放掉，否则下一帧 FocusWithin 又把它展开
		a.CategorySearchHandle.CancelFocus(c)
		return true
	case ui.KeyDown:
		if len(hits) == 0 {
			return false
		}
		a.CategoryHit = (a.CategoryHit + 1) % len(hits)
		return true
	case ui.KeyUp:
		if len(hits) == 0 {
			return false
		}
		a.CategoryHit = (a.CategoryHit - 1 + len(hits)) % len(hits)
		return true
	case ui.KeyEnter:
		if len(hits) == 0 || a.CategoryHit < 0 || a.CategoryHit >= len(hits) {
			return false
		}
		a.pickCategoryHit(hits[a.CategoryHit])
		return true
	}
	return false
}

// categoryHits 把全部一级分组下的二级分区摊平，按这套规则匹配并排序：
// 分区名命中排在前面，其次是分组名命中带出来的那些，最多 40 条。
func (a *App) categoryHits() []categoryHit {
	q := strings.TrimSpace(a.CategoryQuery)
	if q == "" {
		if a.catHitQuery != "" {
			a.catHitQuery, a.CategoryHit = "", 0
		}
		return nil
	}
	if a.catHitQuery != q {
		a.catHitQuery, a.CategoryHit = q, 0
	}

	var hits []categoryHit
	for gi, g := range a.Groups {
		for ci, name := range g.Categories {
			if strings.Contains(name, q) || strings.Contains(g.Name, q) {
				hits = append(hits, categoryHit{
					GroupIndex: gi, CategoryIndex: ci,
					Group: g.Name, Category: name,
				})
			}
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return strings.Contains(hits[i].Category, q) && !strings.Contains(hits[j].Category, q)
	})
	if len(hits) > 40 {
		hits = hits[:40]
	}
	// 越界时收到**末项**，不要甩回第一条。
	//
	// 归 0 会让还在连续打字的人看到选中项从末尾跳回开头，回车落到一条他
	// 根本没看过的分区上。查询继续变短时，末项才是"离原来那条最近的"。
	if n := len(hits); a.CategoryHit >= n {
		a.CategoryHit = max(0, n-1)
	}
	return hits
}

func (a *App) pickCategoryHit(h categoryHit) {
	if h.GroupIndex == a.GroupIndex && h.CategoryIndex == a.CategoryIndex {
		// 搜到的就是当前这一档：只收掉搜索，不换代。
		a.MoreOpen = false
		a.CategoryQuery = ""
		a.CategorySearchOpen = false
		return
	}
	a.navFlip(&a.navSeq)
	a.GroupIndex = h.GroupIndex
	a.CategoryIndex = h.CategoryIndex
	a.MoreOpen = false
	a.CategoryQuery = ""
	a.CategorySearchOpen = false
	a.reloadRooms()
}

func (a *App) categorySearchPanel(c *ui.Context) {
	hits := a.categoryHits()
	if len(hits) == 0 {
		ui.Text(c, "没有匹配的分区").FontSize(theme.TextXS).
			TextColor(theme.TextTertiary).Padding(18, 12, 18, 12)
		return
	}
	ui.Scroll(c).MaxHeight(catPanelMaxH).Children(func() {
		for i, h := range hits {
			a.categorySearchItem(c, i, h)
		}
	})
}

func (a *App) categorySearchItem(c *ui.Context, i int, h categoryHit) {
	active := i == a.CategoryHit
	row := ui.Row(c).Padding(8, 10, 8, 10).Gap(10).Radius(theme.RadiusXS).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).
		Label("分区结果:" + h.Category)
	// 选中与悬停共用一处赋值：先 Background(选中色) 再被 hoverSurfaceOn
	// 覆盖，键盘 ↑↓ 选中的那一条就没有底色。常态色当场传进去。
	rest := ui.Transparent
	if active {
		rest = theme.FillQuaternary
	}
	// 指针只影响绘制，不写模型：指针停在列表上时每帧写 CategoryHit 会把
	// 键盘 ↑↓ 刚改好的选中项覆盖掉。
	row.Background(hoverSurfaceRestOn(row, "cat-hit-bg", row.Hovered(), !active, rest, theme.FillSecondary))
	if row.Clicked() {
		a.CategoryHit = i
		a.pickCategoryHit(h)
	}
	row.Children(func() {
		ui.Text(c, h.Category).FontSize(theme.TextSM).SingleLine().
			TextColor(theme.TextPrimary).Grow(1)
		ui.Text(c, h.Group).FontSize(theme.Text2XS).
			TextColor(theme.TextTertiary).SingleLine()
	})
}

// secondaryRow 是二级分区行：chip 横滑 + 「全部」浮层入口 + 订阅按钮。
func (a *App) secondaryRow(c *ui.Context) {
	winW, _ := c.Size()
	// 可用宽度按**这一行真实拿到的宽度**算，不能按整窗减两个 14。
	//
	// 这一行嵌在 Home 的 Column（Padding 用 bodyPadX：常态 14、≤1240 时 10）
	// + 分区面板 Padding(12,14,14,14) + ScrollHorizontal.Padding(2) 里。
	// 用裸的 2*14 会把可用宽度算大 10~20 DIP：二级分区刚好落在这段误差带里
	// 时 overflow 判假，「全部」按钮不画，最后几枚 chip 被 Scroll 裁掉——
	// 还能横滑，但入口没了。
	avail := winW - 2*a.bodyPadX(c) - 2*14 - 2*2 - a.subscribeWidth(c) - 12

	cats := a.currentCategories()
	// 二级分区是这一套里最"重"的一层：滑块是实心的 accent，
	// 一级和三级都是柔底。层的轻重按它在信息结构里的分量来，不都画成一样。
	box := measureChips(c, cats, theme.TextSM, labelWeight, chipPadH, chipGap, chipMaxW)
	a.navBox = box
	// 是否溢出直接问同一份格子：另写一遍量法，改一处忘另一处就会错。
	overflow := box.last()+4 > avail

	row := ui.Row(c).Gap(12).AlignItems(ui.Center)
	row.Children(func() {
		ui.ScrollHorizontal(c).Scrollbars(false).Grow(1).Padding(2).Children(func() {
			ui.Row(c).Gap(chipGap).Children(func() {
				slider(c, "chip-slide", box, a.CategoryIndex, chipH, theme.RadiusMD, theme.Accent)
				for i, name := range cats {
					a.categoryChip(c, i, name, box.w[i])
				}
			})
		})
		if overflow {
			a.moreButton(c, row)
		}
		a.subscribeButton(c)
	})
}

func (a *App) categoryChip(c *ui.Context, i int, name string, w float32) {
	selected := i == a.CategoryIndex
	chip := ui.Box(c).Height(chipH).
		Padding(0, chipPadH, 0, chipPadH).MaxWidth(chipMaxW).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).
		Border(1, ui.Transparent).Label("分类:" + name)
	// 底部那层淡色底不画：滑块就在这一层下面滑过去，chip 一旦有自己的底色，
	// 就会把正在经过的滑块整块盖住——切换时看起来像被吃掉。
	// 高亮只由滑块这一块材质承担，未选中就什么都不画。
	hovered := chip.Hovered()
	if !selected {
		chip.Background(hoverSurface(chip, "chip-bg", hovered, theme.FillTertiary))
	}
	if chip.Clicked() {
		a.selectCategory(i)
	}
	chip.Children(func() {
		txt := ui.Text(c, name).FontSize(theme.TextSM).SingleLine()
		switch {
		case selected:
			txt.FontWeight(600).
				TextColor(txt.AnimateColor("chip-ink", theme.AccentText, theme.Slide()))
		case hovered:
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("chip-ink", theme.TextPrimary, theme.Slide()))
		default:
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("chip-ink", theme.TextSecondary, theme.Slide()))
		}
	})
}

// subscribeWidth 量出订阅按钮的宽度，用于判断二级分区是否溢出。
// 宽度 = 左右 padding 26 + 边框 2 + 图标 13 + gap 6 + 文字。
func (a *App) subscribeWidth(c *ui.Context) float32 {
	label := "订阅分区"
	if a.subscribed() {
		label = "已订阅"
	}
	return 26 + 2 + 13 + 6 + textWidth(c, label, theme.TextSM, 600)
}

// moreButton 是二级分区放不下时出现的「全部」入口，展开该分组的全部分区。
func (a *App) moreButton(c *ui.Context, row ui.Element) {
	btn := ui.Row(c).Height(chipH).Padding(0, 13, 0, 13).Gap(4).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).Label("更多分区")
	btn.Background(hoverSurface(btn, "more-bg", btn.Hovered(), theme.FillTertiary))
	if a.MoreOpen {
		btn.Background(theme.AccentSoft)
	}
	// 用 OnClick 推迟。这一帧的底色（上面几行）已经按旧的 a.MoreOpen 算过，
	// 内联翻转会让底色停在收起态、字色和箭头当场变成展开态——同一颗按钮在
	// 同一帧两副长相；而下面的 PopoverBase 又已经用新值把浮层打开了。
	btn.OnClick(func() { a.MoreOpen = !a.MoreOpen })

	rest, on := theme.TextSecondary, theme.TextPrimary
	if a.MoreOpen {
		rest, on = theme.AccentInk, theme.AccentInk
	}
	col := hoverInk(btn, "more-ink", btn.Hovered(), rest, on)
	angle := btn.AnimateTo("more-caret", caretAngle(a.MoreOpen), theme.Chrome())

	btn.Children(func() {
		ui.Text(c, "全部").FontSize(theme.TextSM).FontWeight(500).TextColor(col)
		ui.Icon(c, iconChevron).FontSize(12).TextColor(col).Rotate(angle)
	})

	ui.PopoverBase(c, row, &a.MoreOpen, func(panel ui.Element) {
		w := a.morePanelWidth(c)
		panel.Width(w).Padding(16).Radius(theme.RadiusXL).
			Background(theme.BgElevated).Border(1, theme.Border).
			Shadow(0, 22, 56, -22, theme.ShadowPanel).Shadow(0, 4, 12, 0, theme.ShadowContact).
			Margin(12, 0, 0, 0).
			AttachTo(row, ui.AnchorBottomRight, ui.AnchorTopRight)
		a.morePanel(c, w)
	})
}

func caretAngle(open bool) float32 {
	if open {
		return 180 // 展开时箭头旋转 180°
	}
	return 0
}

// morePanelWidth 是「全部」浮层的宽度：min(720px, 二级分区行的宽度)。
func (a *App) morePanelWidth(c *ui.Context) float32 {
	winW, _ := c.Size()
	w := winW - 2*14 // 分区栏面板的左右 padding
	if w > morePanelMaxW {
		w = morePanelMaxW
	}
	return w
}

func (a *App) morePanel(c *ui.Context, w float32) {
	cats := a.currentCategories()

	ui.Row(c).Padding(0, 4, 10, 4).Gap(10).AlignItems(ui.Baseline).Children(func() {
		ui.Text(c, a.currentGroup().Name).FontSize(theme.TextMD).
			FontWeight(600).TextColor(theme.TextPrimary).Grow(1)
		ui.Text(c, fmt.Sprintf("%d 个分区", len(cats))).FontSize(theme.Text2XS).
			TextColor(theme.TextTertiary)
	})

	inner := w - 2*16 // 浮层自身的 padding
	cols := int((inner + moreItemGap) / (moreItemMinW + moreItemGap))
	if cols < 1 {
		cols = 1
	}
	ui.Scroll(c).MaxHeight(morePanelMaxH).Children(func() {
		ui.Grid(c).Columns(cols).Gap(moreItemGap).Children(func() {
			for i, name := range cats {
				a.morePanelItem(c, i, name)
			}
		})
	})
}

func (a *App) morePanelItem(c *ui.Context, i int, name string) {
	active := i == a.CategoryIndex
	item := ui.Box(c).Height(moreItemH).Padding(0, 10, 0, 10).
		Radius(theme.RadiusXS).Center().Cursor(ui.CursorPointer).
		Label("分类:" + name)
	if active {
		item.Background(theme.Accent)
	} else {
		item.Background(hoverSurface(item, "more-item-bg", item.Hovered(), theme.FillQuaternary))
	}
	if item.Clicked() {
		a.selectCategory(i)
	}
	item.Children(func() {
		txt := ui.Text(c, name).FontSize(theme.TextSM).SingleLine()
		rest, on := theme.TextSecondary, theme.TextPrimary
		if active {
			rest, on = theme.AccentText, theme.AccentText
		}
		txt.FontWeight(500).TextColor(hoverInk(item, "more-item-ink", item.Hovered(), rest, on))
		if active {
			txt.FontWeight(600)
		}
	})
}

// subscribeButton 是二级分区行右侧的订阅入口。
// 未订阅：1px 描边 + accent-ink 字；已订阅：accent 实底 + accent-text 字。
func (a *App) subscribeButton(c *ui.Context) {
	subscribed := a.subscribed()
	btn := ui.Row(c).Height(chipH).Padding(0, 13, 0, 13).Gap(6).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).Label("订阅分区")
	if subscribed {
		btn.Background(theme.Accent).Border(1, ui.Transparent)
	} else {
		btn.Background(hoverSurface(btn, "subscribe-bg", btn.Hovered(), theme.AccentSoft)).
			Border(1, hoverInk(btn, "subscribe-border", btn.Hovered(), theme.BorderFunctional, theme.AccentInk))
	}
	if btn.Clicked() {
		a.toggleSubscribe()
	}

	col := theme.AccentInk
	if subscribed {
		col = theme.AccentText
	}
	ico, label := iconPlus, "订阅分区"
	if subscribed {
		ico, label = iconCheck, "已订阅"
	}
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(13).TextColor(col)
		ui.Text(c, label).FontSize(theme.TextSM).FontWeight(600).TextColor(col)
	})
}

// textWidth 量一段文字在给定字号 / 字重下的宽度，用于复刻 DOM 的溢出判断。
func textWidth(c *ui.Context, s string, size, weight float32) float32 {
	w, _ := c.MeasureText(0, ui.Span{Text: s, Size: size, Weight: int(weight)})
	return w
}
