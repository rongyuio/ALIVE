package ui

import (
	"sort"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 路由路径，一个页面一条。订阅分区不是独立页面：它是关注页的一级分区。
const (
	routeHome    = "/"
	routeFollows = "/follows"
	routePlayer  = "/player"
)

// Subscription 是「订阅分区」那一级上的一枚 chip 的数据。
type Subscription struct {
	PlatformID string `json:"platformId"` // 平台 id（douyu / huya / douyin / bilibili），用于匹配
	Platform   string `json:"platform"`   // 2 字平台名，显示在 chip 左侧的小标签里
	Group      string `json:"group"`      // 一级分区
	Name       string `json:"name"`       // 二级分区
	// CategoryID 是二级分区的接口 id：拉这个分区的房间列表要用它。
	// 这之前存下来的订阅没有这个字段，加载后会按「平台+分组名+分区名」回填。
	CategoryID string `json:"categoryId,omitempty"`
}

// subChip 是订阅分区这一级上的一枚 chip：一个分区，加上订阅了它的平台。
//
// 主体是分区而不是平台：同一个分区被几家同时订阅时仍然只有一枚 chip，
// 平台退成 chip 里的成员标签。这样「鸣潮」不会在条上出现两三次。
type subChip struct {
	Name string
	// Subs 是订阅了这个分区的平台，按顶栏平台顺序排好
	Subs []Subscription
}

// subChipKey 是 chip 的身份：只看分区名。
//
// 不能把一级分组算进去：同一个分区在各平台的分组名并不一致。实测
// 「鸣潮」在 B 站归「手游」、在斗鱼归「手游休闲」，按分组聚合会让它们
// 永远合不到一起，正好把要解决的问题留着。
//
// 代价：同一平台若在两个分组下各有一个同名分区，会被合成一枚（成员
// 去重时只留第一个）。这种情形在真实数据里没出现过，而且用户看到的是
// 同一行直播间，混在一起也不比分开更难理解。
func subChipKey(name string) string { return name }

// subChips 把 a.Subs 聚合成 chip：同（分组, 分区）的订阅合并成一枚。
//
// 顺序按首次出现的先后，不按名字排——否则订阅 / 取消一次，整条 chip
// 就会重新洗牌，用户找不到刚才点的那枚。
func (a *App) subChips() []subChip {
	out := make([]subChip, 0, len(a.Subs))
	index := make(map[string]int, len(a.Subs))
	for _, s := range a.Subs {
		key := subChipKey(s.Name)
		if i, ok := index[key]; ok {
			out[i].Subs = append(out[i].Subs, s)
			continue
		}
		index[key] = len(out)
		out = append(out, subChip{Name: s.Name, Subs: []Subscription{s}})
	}
	for i := range out {
		out[i].sortByPlatformOrder(a.PlatformOrder)
	}
	return out
}

// sortByPlatformOrder 把成员按顶栏平台顺序排。
//
// 这条顺序有两层含义：菜单里从上到下的平台，以及默认先显示哪一家。
// 不认识的平台排在最后，保持相对顺序。
func (c *subChip) sortByPlatformOrder(order []string) {
	rank := make(map[string]int, len(order)+1)
	for i, id := range order {
		rank[id] = i
	}
	at := func(id string) int {
		if i, ok := rank[id]; ok {
			return i
		}
		return len(order)
	}
	sort.SliceStable(c.Subs, func(i, j int) bool {
		return at(c.Subs[i].PlatformID) < at(c.Subs[j].PlatformID)
	})
}

// activeSub 是这枚 chip 当前显示哪一家的房间。
//
// 默认取成员里的第一枚（也就是顶栏顺序最靠前的那家）；用户在菜单里切过
// 就记住他的选择。记忆只在本进程内，不落盘。
func (a *App) activeSub(chip subChip) Subscription {
	if id := a.subActive[subChipKey(chip.Name)]; id != "" {
		for _, s := range chip.Subs {
			if s.PlatformID == id {
				return s
			}
		}
	}
	return chip.Subs[0]
}

// subIndex 返回一条订阅在 a.Subs 里的下标，找不到返回 -1。
//
// 按「平台 + 分区名」认人，不比 Group：同一个分区在各平台的分组名
// 本来就不一致（B 站「手游」/ 斗鱼「手游休闲」），带上 Group 反而
// 会让同一条订阅匹配不上自己。
func (a *App) subIndex(s Subscription) int {
	for i, cur := range a.Subs {
		if cur.PlatformID == s.PlatformID && cur.Name == s.Name {
			return i
		}
	}
	return -1
}

// subscribeRow 是「订阅分区」的二级分级：每个分区一枚 chip。
//
// chip 里是「分区名 + 订阅了它的平台」。胶囊本身就是切换平台的按钮：
// hover 时按 macOS Dock 的手感放大（Scale 会一起放大命中区域，所以放大
// 之后更好点，不会点歪），点击即切到那一家的房间。
//
// 取消订阅放在这一行最右侧，和首页分区栏的「＋订阅分区」同一个位置、
// 同一套按钮语言。
func (a *App) subscribeRow(c *ui.Context) {
	if len(a.Subs) == 0 {
		ui.Text(c, "还没有订阅分区。进入任意主播列表，在分区栏点「订阅分区」即可加到这里。").
			FontSize(theme.TextXS).TextColor(theme.TextTertiary).
			Padding(0, 4, 0, 4)
		return
	}
	chips := a.subChips()
	if a.SubIndex < 0 || a.SubIndex >= len(chips) {
		a.SubIndex = 0
	}

	// chip 的宽度从里面量出来：左右内边距、分区名、每个平台胶囊，
	// 外加这两段 gap。量法和真实布局必须一致，否则滑块会停在半个 chip 上。
	box := subscribeBox(c, chips)
	a.subsBox = box

	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.ScrollHorizontal(c).Scrollbars(false).Grow(1).Padding(2).Children(func() {
			ui.Row(c).Gap(6).Children(func() {
				slider(c, "subs-slide", box, a.SubIndex, 34, theme.RadiusFull, theme.Accent)
				for i := range chips {
					a.subscribeChip(c, i, chips[i])
				}
			})
		})
		a.unsubscribeButton(c, chips[a.SubIndex])
	})
}

// unsubscribeButton 是这一行右侧的「取消订阅」。
//
// 取消的是「当前 chip 的当前平台」：用户看到的就是这一屏，胶囊上实底的
// 那家就是它。所以按钮不需要再解释取消谁。
func (a *App) unsubscribeButton(c *ui.Context, chip subChip) {
	active := a.activeSub(chip)
	btn := ui.Row(c).Height(chipH).Padding(0, 13, 0, 13).Gap(6).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).
		Label("取消订阅").Border(1, theme.Border)
	btn.Background(hoverSurface(btn, "unsub-bg", btn.Hovered(), theme.AccentSoft))
	btn.BorderColor(hoverInk(btn, "unsub-border", btn.Hovered(), theme.Border, theme.AccentInk))
	if btn.Clicked() {
		a.removeSubPlatform(chip, active.PlatformID)
	}
	btn.Children(func() {
		col := theme.AccentInk
		ui.Icon(c, iconMinus).FontSize(13).TextColor(col)
		ui.Text(c, "取消订阅").FontSize(theme.TextSM).
			FontWeight(600).TextColor(col)
	})
}

// subscribeChip 是订阅条上的一枚 chip：分区名在前，平台胶囊在后。
func (a *App) subscribeChip(c *ui.Context, idx int, chip subChip) ui.Element {
	selected := idx == a.SubIndex
	active := a.activeSub(chip)

	el := ui.Row(c).Height(34).Padding(0, 14, 0, 10).Gap(6).
		Radius(theme.RadiusFull).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label("订阅分区:" + chip.Name).
		// 平台集合是这枚 chip 的关键信息，读屏也要拿到
		Description("订阅了 " + chipPlatforms(chip))
	// 选中态由滑块画。未选中不画底，否则会把经过的滑块盖住。
	el.Background(hoverSurfaceOn(el, "subs-chip-bg", el.Hovered(), !selected, theme.FillTertiary))
	if el.Clicked() {
		if idx != a.SubIndex {
			// 换 chip 后列表回到顶部。换的是另一套行，
			// 整份状态重置（只挪偏移会先用旧位置铺新数据，闪一下）。
			a.SubList = ui.ListState{}
			a.navFlip(&a.followsSeq)
		}
		a.SubIndex = idx
	}

	el.Children(func() {
		// 分区名是主体，比平台大一档
		nameColor := theme.TextPrimary
		if selected {
			nameColor = theme.AccentText
		}
		txt := ui.Text(c, chip.Name).FontSize(theme.TextSM).SingleLine()
		txt.TextColor(txt.AnimateColor("sub-name", nameColor, theme.Slide()))
		if selected {
			txt.FontWeight(600)
		} else {
			txt.FontWeight(500)
		}

		// 平台胶囊：当前看的那家实底，其余只留字
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			for _, sub := range chip.Subs {
				a.subPlatformTag(c, idx, chip, sub, selected, sub.PlatformID == active.PlatformID)
			}
		})
	})
	return el
}

// subTagHoverScale 是平台胶囊在指针下的放大倍数。
//
// 20 高的胶囊放大到 1.3 倍是 26：纵向还在 34 高的 chip 里，横向每侧
// 多出 3px，正好吃掉胶囊之间 4 的 gap——和 Dock 里相邻图标靠拢是一个
// 观感。再大会吞掉整枚相邻胶囊，难以判断点的是哪一家。
const subTagHoverScale float32 = 1.3

// subTagGrow 是胶囊放大的动画。
//
// 用 Duration 而不是 theme.Slide()：Slide 是刚度 460、阻尼 42 的弹簧，
// 位移类动效用它收尾干净，但 1 → 1.3 这种小跨度上尾段会拖好几帧，
// 鼠标扫过去像是「慢慢地胖起来」。Dock 的放大本来就该跟手：180ms 的
// ease-out，指针到哪儿它就到哪儿，离开时同样快。
func subTagGrow() ui.Animation {
	return theme.Micro()
}

// subPlatformTag 是 chip 里的一枚平台胶囊，也是切换平台的按钮。
//
// 指针移上来时按 Dock 的手感放大。放大走 Scale（transform），不参与
// 布局：chip 宽度和滑块位置全程不动，否则 hover 一下整行就会抖。
// MyGo 的命中测试走反变换后的世界矩阵，所以放大后的区域连点击范围
// 一起变大——这也是放大有意义的地方。
func (a *App) subPlatformTag(c *ui.Context, idx int, chip subChip, sub Subscription, selected, current bool) {
	tag := ui.Box(c).Height(20).Padding(0, 6, 0, 6).
		Radius(theme.RadiusFull).Center().
		Cursor(ui.CursorPointer).
		Label("平台:" + sub.Platform + " " + chip.Name)

	hovered := tag.Hovered()
	bg, fg := ui.Transparent, theme.TextTertiary
	// 指针下的胶囊取当前平台那套实底：放大只有字变大时，看起来像字号
	// 变了而不是「这里是个按钮」；底色一起亮起来，放大才有块的分量。
	if current || hovered {
		bg, fg = theme.FillSecondary, theme.TextPrimary
	}
	if selected {
		fg = theme.AccentText
		if current || hovered {
			bg, fg = theme.AccentText, theme.Accent
		}
	}
	tag.Background(tag.AnimateColor("sub-tag-bg-"+sub.PlatformID, bg, theme.Slide()))

	target := float32(1)
	if hovered {
		target = subTagHoverScale
	}
	scale := tag.AnimateTo("sub-tag-scale-"+sub.PlatformID, target, subTagGrow())
	tag.Scale(scale, scale).TransformOrigin(0.5, 0.5)

	if tag.Clicked() {
		a.activateSubChipPlatform(idx, chip, sub.PlatformID)
	}

	tag.Children(func() {
		txt := ui.Text(c, sub.Platform).FontSize(10).LetterSpacing(0.02)
		txt.TextColor(txt.AnimateColor("sub-tag-"+sub.PlatformID, fg, theme.Slide()))
		if current {
			txt.FontWeight(600)
		} else {
			txt.FontWeight(500)
		}
	})
}

// activateSubChipPlatform 把某枚 chip 切到指定平台，并把它选为当前 chip。
//
// 点胶囊时两件事可能同时发生：换平台、换 chip。代次只加一次。
func (a *App) activateSubChipPlatform(idx int, chip subChip, platformID string) {
	key := subChipKey(chip.Name)
	if idx == a.SubIndex && a.subActive[key] == platformID {
		return
	}
	a.subActive[key] = platformID
	a.SubIndex = idx
	a.SubList = ui.ListState{}
	a.followsSeq++
}

// removeSubPlatform 取消一枚 chip 上某个平台的订阅。
//
// 取消的正是当前看的平台时，activeSub 会自动回落到成员里的第一枚——
// 也就是顶栏顺序里的下一家。一枚 chip 的成员全被取消后，chip 自己
// 会从 subChips 里消失，选中项跟着收拢。
func (a *App) removeSubPlatform(chip subChip, platformID string) {
	idx := -1
	for i, s := range a.Subs {
		if s.PlatformID == platformID && s.Name == chip.Name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	a.Subs = append(a.Subs[:idx], a.Subs[idx+1:]...)

	key := subChipKey(chip.Name)
	if a.subActive[key] == platformID {
		delete(a.subActive, key)
	}
	a.clampSubIndex()
	a.SubList = ui.ListState{}
	a.markDirty()
	a.toastTone("已取消订阅"+chip.Name, toastSuccess)
}

// clampSubIndex 把选中项收回 chip 数量的范围内。
func (a *App) clampSubIndex() {
	n := len(a.subChips())
	if a.SubIndex >= n {
		a.SubIndex = max(0, n-1)
	}
}

// chipPlatforms 是 chip 成员平台名的串联，给读屏用。
func chipPlatforms(chip subChip) string {
	out := ""
	for i, s := range chip.Subs {
		if i > 0 {
			out += "、"
		}
		out += s.Platform
	}
	return out
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

// subscribeBox 量订阅条上每个 chip 的格子。
//
// chip 是 14/10 的内边距、分区名（13px）、一段 6 的 gap、每个平台胶囊
// （6+6 内边距、10px 字）、胶囊之间 4 的 gap。量法和真实布局必须一致，
// 否则滑块会停在半个 chip 上。
func subscribeBox(c *ui.Context, chips []subChip) rowBox {
	box := rowBox{x: make([]float32, len(chips)), w: make([]float32, len(chips))}
	var x float32
	for i, chip := range chips {
		w := 14 + textWidth(c, chip.Name, theme.TextSM, labelWeight) + 6 + 10
		for j, sub := range chip.Subs {
			if j > 0 {
				w += 4
			}
			w += 12 + textWidth(c, sub.Platform, 10, 600)
		}
		box.x[i], box.w[i] = x, w
		x += w + 6 // bar 的 gap
	}
	return box
}
