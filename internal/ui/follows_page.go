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

// FollowsPage 是「关注」页。
//
// 顶栏只剩一颗星星（关注）之后，关注主播和订阅分区就在这里合并成同一页的
// 两个一级分区：上面一行切「关注主播 / 订阅分区」，下面一行是各自的分级。
// 关注主播下面是平台（全部 / B站 / 斗鱼……），订阅分区下面是已订阅的分区。
// 版式和首页分区栏一致——同一块面板、同一套滑块、同一套换向语法。
//
// 它原来是灵动岛的展开面板（浮层）。浮层压在页面上、点外面就消失，
// 放到顶栏做成一个 tab 之后它就是普通页面：和首页同一层级、同一套正文
// padding，切过去看、切回来内容还在。
func (a *App) FollowsPage(c *ui.Context) {
	pad := a.bodyPadX(c)
	ui.Column(c).Grow(1).Padding(0, pad, 0, pad).Gap(12).Children(func() {
		a.followsPanel(c)
		// 换分区就是换了一份列表，和首页换分区同一个语法：换代换 key，
		// 新旧内容直接交换。分区面板留在原地，它是这一屏的边框。
		//
		// key 里带上当前一级分区：两级分区共用一份代次，序号在各层
		// 各自从 0 起，只按代次换 key 的话，从「关注主播」的平台 0 切到
		// 「订阅分区」的分区 0 会被当成同一块内容，不播进出。
		feed := ui.Column(c).Key(navKey(followsFeedKind(a.FollowsSection), a.followsSeq)).
			Grow(1)
		feed.Children(func() {
			if a.FollowsSection == followsSectionSubs {
				a.subscribeFeed(c)
				return
			}
			a.followsGrid(c, a.followsList())
		})
	})
}

// 关注页的两个一级分区。
const (
	followsSectionStreamers = iota
	followsSectionSubs
)

// followsFeedKind 是内容层的身份前缀：两级分区各算各的内容。
func followsFeedKind(section int) string {
	if section == followsSectionSubs {
		return "follows-subs"
	}
	return "follows"
}

// followsSectionNames 是一级分区的名字，下标和常量的取值一致。
// 显示顺序不在这里——那由 FollowsSectionOrder 决定，用户拖一次就变。
var followsSectionNames = []string{"关注主播", "订阅分区"}

// 一级分区在顺序表里的身份。
//
// 用字符串而不是常量序号：顺序要落盘，文件里写 ["subs","streamers"]
// 比 [1,0] 更难读错，也不会因为以后加第三个分区而整体错位。
const (
	sectionIDStreamers = "streamers"
	sectionIDSubs      = "subs"
)

// defaultFollowsSectionOrder 是第一次启动时的分区顺序。
var defaultFollowsSectionOrder = []string{sectionIDStreamers, sectionIDSubs}

// followsSectionOf 把顺序表里的身份换回分区常量；认不出来就退到关注主播。
func followsSectionOf(id string) int {
	if id == sectionIDSubs {
		return followsSectionSubs
	}
	return followsSectionStreamers
}

// followsSectionOrder 是当前一级分区的显示顺序。
//
// 每帧现算而不缓存：顺序表只在拖动和读盘时写入，normalize 的代价是
// 两个元素的切片，不值得为它维护第二份状态。
func (a *App) followsSectionOrder() []string {
	return normalizeFollowsSectionOrder(a.FollowsSectionOrder)
}

// normalizeFollowsSectionOrder 把任意顺序整理成「两个分区各一次，缺的补在后面」。
func normalizeFollowsSectionOrder(in []string) []string {
	seen := make(map[string]bool, len(defaultFollowsSectionOrder))
	out := make([]string, 0, len(defaultFollowsSectionOrder))
	for _, id := range in {
		if (id == sectionIDStreamers || id == sectionIDSubs) && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range defaultFollowsSectionOrder {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}

// moveFollowsSection 把拖起来的一级分区放到目标分区的位置。
//
// 和 movePlatform 同一套落位规则：先摘掉拖拽项，再插到目标原来的索引上。
// 左右只有两格时它等价于交换，但公式不为两格特判——以后加第三个分区
// 就不用再改一次。
func (a *App) moveFollowsSection(dragID, targetID string) {
	order := a.followsSectionOrder()
	from, to := -1, -1
	for i, id := range order {
		switch id {
		case dragID:
			from = i
		case targetID:
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	order = append(order[:from], order[from+1:]...)
	order = append(order, "")
	copy(order[to+1:], order[to:])
	order[to] = dragID
	a.FollowsSectionOrder = order
	a.markDirty()
}

// followsPanel 是关注页顶部的分区面板：一级分区 + 当前分区的二级分级。
//
// 外壳和首页的 categoryBar 同一块材质（RadiusXL + 受光边、不描边，
// 三行之间 gap 10），这样从平台页切到关注页正文起点不跳。
func (a *App) followsPanel(c *ui.Context) {
	panel := ui.Column(c).Gap(10).Padding(12, 14, 14, 14).
		Radius(theme.RadiusXL).
		Background(theme.BgSecondary)
	panel.Shadow(0, 1, 0, 0, theme.TopLight)

	panel.Children(func() {
		a.followsSectionRow(c)
		if a.FollowsSection == followsSectionSubs {
			a.subscribeRow(c)
			return
		}
		a.followsPlatformRow(c)
	})
}

// followsSectionRow 是一级分区行：左边两枚 tab，右边是当前分区自己的工具。
//
// tab 的左右顺序读 FollowsSectionOrder，可以拖动换位；选中项跟着分区身份
// 走，不跟格子序号走——把「订阅分区」拖到前面，正在看的那一屏不会跟着变。
//
// 搜索和刷新只属于「关注主播」：订阅分区那一屏是某个分区自己的房间列表，
// 关注刷新不会改它，摆着只会让人以为按了会变。
func (a *App) followsSectionRow(c *ui.Context) {
	order := a.followsSectionOrder()
	names := make([]string, len(order))
	selected := 0
	for i, id := range order {
		section := followsSectionOf(id)
		names[i] = followsSectionNames[section]
		if section == a.FollowsSection {
			selected = i
		}
	}
	box := measureRow(c, names, groupFont, labelWeight, groupTabPadH, groupGap, 0)
	a.followsSectionBox = box

	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Row(c).Gap(groupGap).Children(func() {
			slider(c, "follows-section-slide", box, selected,
				groupTabH, theme.RadiusMD, theme.AccentSoft)
			for _, id := range order {
				a.followsSectionTab(c, id)
			}
		})

		ui.Box(c).Grow(1)

		if a.FollowsSection == followsSectionStreamers {
			a.followsActions(c)
		}
	})
}

// followsSectionTab 是一级分区的一枚 tab。选中态的底色由滑块画。
//
// 拖动重排和顶栏平台 tab 同一套语言：拖起来的那枚降到 0.35，
// 指针经过的目标描一圈 accent 内阴影；松手时一次落位。
func (a *App) followsSectionTab(c *ui.Context, id string) {
	section := followsSectionOf(id)
	name := followsSectionNames[section]
	selected := section == a.FollowsSection
	tab := ui.Box(c).Height(groupTabH).
		Padding(0, groupTabPadH, 0, groupTabPadH).
		Radius(theme.RadiusMD).Center().Cursor(ui.CursorPointer).
		Label(name)
	if !selected && tab.Hovered() {
		tab.Background(theme.FillQuaternary)
	}
	if tab.Clicked() {
		a.selectFollowsSection(section)
	}
	tab.Drag(id)
	if _, over := ui.DragOver[string](tab); over {
		tab.InsetShadow(0, 0, 0, 1, theme.Accent.Mix(ui.Transparent, 0.35))
	}
	if dragged, dropped := ui.Drop[string](tab); dropped && dragged != id {
		a.moveFollowsSection(dragged, id)
	}
	if tab.Dragging() {
		tab.Opacity(0.35)
	}
	tab.Children(func() {
		txt := ui.Text(c, name).FontSize(groupFont)
		switch {
		case selected:
			txt.FontWeight(600).
				TextColor(txt.AnimateColor("follows-section-ink", theme.AccentInk, theme.Slide()))
		case tab.Hovered():
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("follows-section-ink", theme.TextPrimary, theme.Slide()))
		default:
			txt.FontWeight(500).
				TextColor(txt.AnimateColor("follows-section-ink", theme.TextSecondary, theme.Slide()))
		}
	})
}

// selectFollowsSection 切一级分区：内容层换代。
func (a *App) selectFollowsSection(i int) {
	if i == a.FollowsSection {
		return
	}
	a.navFlip(&a.followsSeq)
	a.FollowsSection = i
}

// followsPlatformRow 是「关注主播」的二级分级：平台胶囊（全部 / B站 / 斗鱼……）。
//
// 只列「确实有关注主播」的平台，顺序跟顶栏 tab 一致；数字放进悬停提示里，
// 常态这一行只留筛选。
func (a *App) followsPlatformRow(c *ui.Context) {
	followsIDs := append([]string{""}, a.followsPlatforms()...)
	followsNames := make([]string, len(followsIDs))
	for i, id := range followsIDs {
		followsNames[i] = "全部"
		if id != "" {
			followsNames[i] = platformLabel(id)
		}
	}
	box := measureChips(c, followsNames, theme.TextSM, labelWeight, 15, 6, 0)
	a.followsBox = box
	followsActive := 0
	for i, id := range followsIDs {
		if id == a.FollowsPlatform {
			followsActive = i
		}
	}

	ui.ScrollHorizontal(c).Scrollbars(false).Padding(2).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			slider(c, "follows-slide", box, followsActive, 34, theme.RadiusFull, theme.Accent)
			for i, id := range followsIDs {
				a.followsPlatformChip(c, i, id, followsNames[i], box.w[i])
			}
		})
	})
}

// followsActions 是「关注主播」上的页内搜索与刷新。
func (a *App) followsActions(c *ui.Context) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		// 搜索框：34 高、图标在左，真输入框。动效跟分区搜索同一套：常态 176，聚焦或有输入时 240ms 展开到 220。
		// 搜索框在 focus-within 时边框变 accent-ink，190ms。
		search := ui.Row(c).Height(34).Padding(0, 8, 0, 11).Gap(7).
			Radius(theme.RadiusFull).Background(theme.FillQuaternary).
			Center().Cursor(ui.CursorText)
		// 和分区搜索一样：静态不描边，聚焦才画
		border := ui.Transparent
		if search.FocusWithin() {
			border = theme.AccentInk
		}
		width := catSearchW
		if search.FocusWithin() || a.FollowsQuery != "" {
			width = catSearchWide
		}
		search.Width(search.AnimateTo("follows-search-w", width,
			theme.EaseOut(240*time.Millisecond)))
		search.Border(1, search.AnimateColor("follows-search-border", border,
			theme.EaseOut(190*time.Millisecond)))
		search.Children(func() {
			ui.Icon(c, iconSearch).FontSize(14).TextColor(theme.TextTertiary)
			ui.TextInputBase(c, &a.FollowsQuery).FontSize(theme.TextXS).Grow(1).
				Bind(&a.FollowsSearch).
				Placeholder("搜索主播").PlaceholderColor(theme.TextTertiary).
				Label("搜索已关注的主播").
				OnTextKey(func(k ui.TextKey) bool {
					// 和顶栏搜索、分区搜索一致：一次 Esc 清空（页面本身不收起，
					// 只把搜索退掉）
					if k.Key != ui.KeyEscape {
						return false
					}
					a.FollowsQuery = ""
					a.FollowsSearch.CancelFocus(c)
					return true
				})
			if a.FollowsQuery != "" {
				clear := ui.Box(c).Size(18, 18).Radius(9).Center().
					Cursor(ui.CursorPointer).Label("清空关注搜索")
				if clear.Hovered() {
					clear.Background(theme.FillTertiary)
				}
				if clear.Clicked() {
					a.FollowsQuery = ""
				}
				clear.Children(func() {
					ui.Icon(c, iconX).FontSize(12).TextColor(theme.TextTertiary)
				})
			}
		})

		// 刷新：28×28 的一枚圆按钮，busy 时带进度环。
		// 外面不再套胶囊——面板本身就是容器，叠第二层底会在同色上留一道边。
		a.followsAction(c, iconSync, a.followRefreshLabel(),
			a.followsBusy, a.followRingProgress(), a.refreshFollowsManual)
	})
}

// subscribeFeed 是「订阅分区」的内容层：当前选中订阅项的房间列表。
func (a *App) subscribeFeed(c *ui.Context) {
	chips := a.subChips()
	if len(chips) == 0 {
		a.subscribeEmpty(c)
		return
	}
	if a.SubIndex < 0 || a.SubIndex >= len(chips) {
		a.SubIndex = 0
	}
	if a.LiveMode {
		a.ensureSubFeed()
		if a.SubErr != "" {
			a.errorBar(c)
		}
		a.grid(c, a.SubRooms, &a.SubList, a.loadMoreSubRooms)
		return
	}
	a.grid(c, a.feedFor(a.activeSub(chips[a.SubIndex])), &a.SubList, nil)
}

// followsGrid 是主播行列表。
//
// 行宽按 minmax(248px, 1fr)、gap 4 算：列数随可用宽度增减，
// 和首页网格同一条公式，只是最小列宽更大（每格是一整行信息）。
func (a *App) followsGrid(c *ui.Context, list []Streamer) {
	w, _ := c.Size()
	avail := w - 2*a.bodyPadX(c) - theme.ScrollbarW
	const gap = float32(4)
	const minW = float32(248)
	avail = max(avail, minW)
	cols := max(int((avail+gap)/(minW+gap)), 1)
	cellW := (avail - gap*float32(cols-1)) / float32(cols)

	ui.Scroll(c).Grow(1).Children(func() {
		// 空态：padding 64 20、居中、13px tertiary
		if len(list) == 0 {
			// 空态分两种：一个都没关注，和筛选/搜索之后没命中
			empty := "没有匹配的主播"
			if len(a.followStreamers()) == 0 {
				empty = "还没有关注的主播"
			}
			ui.Box(c).Width(avail).Padding(64, 20, 64, 20).Center().Children(func() {
				ui.Text(c, empty).FontSize(theme.TextSM).
					TextColor(theme.TextTertiary)
			})
			return
		}
		ui.Grid(c).Width(avail).Columns(cols).Gap(gap).Children(func() {
			for i := range list {
				a.followsRow(c, i, list[i], cellW)
			}
		})
	})
}

// followsRow 是一行：40px 圆头像（右下角在线点）+ 昵称 / 直播间标题。
func (a *App) followsRow(c *ui.Context, i int, s Streamer, w float32) {
	// 行的身份由位置决定：右键菜单的选中结果要回到同一个元素才生效，
	// 而 Mock/老记录里可能出现同名同房间的重复项，用名字拼 key 会撞键。
	row := ui.Row(c.Key(fmt.Sprintf("follow-row-%d", i))).
		Width(w).Gap(12).Padding(10, 12, 10, 12).
		Radius(theme.RadiusMD).Cursor(ui.CursorPointer).
		Label("关注的主播 " + s.Name)
	hovered := row.Hovered()

	// 动画 key 绑在主播身份上：列表重排时同一行的渐亮状态跟着它走，不会重启。
	key := s.Platform + "|" + s.RoomID + "|" + s.Name
	// 行底色的过渡是 background-color 190ms ease-out，
	// 鼠标扫过列表是整片渐亮，不是一格一格地闪。
	rowBg := ui.Transparent
	if hovered {
		rowBg = theme.FillQuaternary
	}
	row.Background(row.AnimateColor("follows-row-bg-"+key, rowBg,
		theme.EaseOut(followsDur2)))
	if row.Clicked() {
		a.Player = s
		a.Router.Push(routePlayer)
	}
	// 右键菜单只在“全部关注”里提供取消关注：这里列出的都是已关注的主播，
	// 菜单只做减法，不能反过来把记录加回去。
	row.ContextMenu(func(m *ui.Menu) {
		if m.Item("取消关注").Chosen() {
			a.removeFollow(s)
		}
	})

	row.Children(func() {
		wrap := ui.Box(c).Size(40, 40)
		wrap.Children(func() {
			if bmp := media.Default.Avatar(s.AvatarURL, 40); bmp != nil {
				ui.Image(c, bmp).Fill().Fit(ui.Cover).Radius(theme.RadiusFull)
			} else {
				ui.Box(c).Fill().Radius(theme.RadiusFull).
					Background(ui.Oklch(0.42, 0.10, s.Hue+180)).Center().
					Children(func() {
						ui.Text(c, initial(s.Name)).FontSize(14).FontWeight(600).
							TextColor(theme.MediaTextDim)
					})
			}
			// 在线点：13×13，2px 的地面色描边把它从头像上「抠」出来
			//
			// 脉冲环与点同级、先画（压在点下面），并带稳定 Key：
			// Keyframes 的状态绑在元素身份上，身份一抖动画就每帧重置，
			// 环会永远停在 scale 0.75、缩在点内部看不见。
			dotKey := "live-" + s.Platform + "|" + s.Name + "|" + s.Category
			if s.Live {
				// 脉冲环：2px 绿环从 scale 0.75 扩到 1.85、
				// 透明度 0.9 → 0，2s ease-out 循环，70%–100% 停在扩满的无形状态。
				//
				// 相位用 Loop 驱动：这版 MyGo 的 Keyframes + RepeatForever
				// 播完一次就停在末值（环会扩满后透明，看起来只剩绿点）。
				ring := ui.Box(c.Key(dotKey+"-ring")).
					Absolute().Right(-1).Bottom(-1).Size(15, 15).
					Radius(theme.RadiusFull).Border(2, theme.StatusOnline)
				if c.Preferences().ReduceMotion {
					// reduced motion 下关掉脉冲，只留静态绿点
					ring.Opacity(0)
				} else {
					p := ring.Loop("live-pulse", 2*time.Second, ui.Linear)
					scale, alpha := livePulse(p)
					ring.Scale(scale, scale)
					ring.Opacity(alpha)
				}
			}

			dot := ui.Box(c.Key(dotKey+"-dot")).
				Absolute().Right(0).Bottom(0).Size(13, 13).
				Radius(theme.RadiusFull).Border(2, theme.BgSecondary)
			if s.Live {
				dot.Background(theme.StatusOnline)
			} else {
				dot.Background(theme.StatusOffline)
			}
		})

		ui.Column(c).Grow(1).Gap(3).Children(func() {
			nameCol := theme.TextPrimary
			if hovered {
				nameCol = theme.AccentInk
			}
			// 名字同样是 190ms 过渡：hover 时才慢慢染成强调色
			name := ui.Text(c, s.Name).FontSize(theme.TextSM).FontWeight(600).
				SingleLine()
			name.TextColor(name.AnimateColor("follows-row-name-"+key, nameCol,
				theme.EaseOut(followsDur2)))

			title := s.Title
			if title == "" {
				title = "暂无直播标题"
			}
			ui.Text(c, title).FontSize(theme.TextXS).SingleLine().
				TextColor(theme.TextTertiary)
		})
	})
}

// followsAction 是页头里的刷新按钮（28×28 圆形）。
//
// 按钮常态只吃 hover 底色；busy 时按 :disabled
// 处理——降透明度、光标变回箭头、不再有悬停反馈也不再响应点击。
// progress ≥ 0 且 busy 时，图标外再套一圈进度环。
func (a *App) followsAction(c *ui.Context, ico *ui.SVG, label string, busy bool, progress float32, on func()) {
	// 按钮有 aria-label / title，读屏和自动化都靠它认按钮
	btn := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).Center().Label(label)
	if busy {
		// 禁用态：cursor: default; opacity: .72
		btn.Opacity(0.72).Cursor(ui.CursorDefault)
	} else {
		btn.Cursor(ui.CursorPointer)
	}
	hovered := !busy && btn.Hovered()
	if hovered {
		btn.Background(theme.FillTertiary)
	}
	if btn.Clicked() && !busy {
		on()
	}

	// 进度环：值走 AnimateTo，220ms 的 stroke-dashoffset 过渡；不显示时也要把
	// 动画值拉回 0，否则下一轮会从上一次的满格倒着缩回来。
	ringTarget := float32(0)
	if busy && progress >= 0 {
		ringTarget = progress
	}
	ring := btn.AnimateTo("action-ring", ringTarget, theme.EaseOut(followsRingDur))
	if busy && progress >= 0 {
		btn.Draw(func(p *ui.Painter, r ui.Rect) { refreshRing(p, r, ring) })
	}

	btn.Children(func() {
		col := theme.TextSecondary
		if hovered {
			col = theme.TextPrimary
		}
		icon := ui.Icon(c, ico).FontSize(16).TextColor(col)
		if busy {
			// 刷新中：0.9s 匀速转一圈，兼作进度提示
			phase := btn.Loop("action-spin", 900*time.Millisecond, ui.Linear) * 360
			// 停下的那一帧要从当前角度接着往回转，先把角度写进收尾动画
			btn.Jump("action-spin-stop", phase)
			icon.Rotate(phase)
		} else {
			// 图标的 transform 有 500ms 过渡，
			// 转圈停下时图标滑回正位，而不是啪地跳回去。
			icon.Rotate(btn.AnimateTo("action-spin-stop", 0, followsSpinStop))
		}
	})
}

// followsAction 的进度环与收尾动画度量。
const (
	// RingProgress size=26 / stroke=2
	followsRingSize   float32 = 26
	followsRingStroke float32 = 2
	// .indicator 的 transition: stroke-dashoffset 220ms var(--ease-out)
	followsRingDur = 220 * time.Millisecond
	// --dur-2：.panelCard / .panelName / .panelFilter 的 hover 与选中态过渡
	followsDur2 = 190 * time.Millisecond
)

// followsSpinStop 是 .actionBtn svg 的 transition：
// transform 500ms cubic-bezier(0.16, 1, 0.3, 1)。和 --ease-out 不是同一条曲线。
var followsSpinStop = ui.Animation{
	Duration: 500 * time.Millisecond,
	Ease:     ui.CubicBezier(0.16, 1, 0.3, 1),
}

// refreshRing 画关注刷新按钮上的圆环进度：
// 0% 在 12 点方向顺时针，轨道是 text-secondary 26%，指示色 accent-ink。
func refreshRing(p *ui.Painter, r ui.Rect, v float32) {
	v = min(max(v, 0), 1)
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	rad := followsRingSize/2 - followsRingStroke/2

	track := arcPath(cx, cy, rad, 0, 1)
	p.StrokePath(&track, followsRingStroke, theme.TextSecondary.Alpha(0.26))
	if v > 0 {
		arc := arcPath(cx, cy, rad, 0, v)
		p.StrokePath(&arc, followsRingStroke, theme.AccentInk)
	}
}

// arcPath 是圆心 (cx,cy)、半径 rad、从 12 点顺时针扫过 from→to 圈的圆弧。
// Path 没有弧线原语，按每圈 48 段折线逼近——和 spinner 的做法一致。
func arcPath(cx, cy, rad, from, to float32) ui.Path {
	const perTurn = 48
	steps := max(int(math.Ceil(float64((to-from)*perTurn))), 1)
	var path ui.Path
	for i := 0; i <= steps; i++ {
		turn := from + (to-from)*float32(i)/float32(steps)
		// 减 90° 把 0% 摆到 12 点方向
		ang := float64(turn*2*math.Pi) - math.Pi/2
		x := cx + rad*float32(math.Cos(ang))
		y := cy + rad*float32(math.Sin(ang))
		if i == 0 {
			path.MoveTo(x, y)
		} else {
			path.LineTo(x, y)
		}
	}
	return path
}

// followsPlatformChip 是筛选条上的一枚平台 chip。id 为空表示「全部」。
func (a *App) followsPlatformChip(c *ui.Context, idx int, id, label string, w float32) {
	active := id == a.FollowsPlatform
	// 高度 34 和订阅页的 chip 一致：同一条胶囊条里的东西要同一个身高
	chip := ui.Box(c).Height(34).Padding(0, 15, 0, 15).
		Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
		Border(1, ui.Transparent).Label("关注筛选:" + label).
		// 数据规模不常驻：指针停在这枚胶囊上才出「N 位主播 · N 正在直播」
		Tooltip(a.followsChipTip(id))

	// 选中态由滑块画：同一块材质滑过去，而不是旧的白一下、新的亮一下。
	// 未选中不画底——底下一有颜色，就会把正在经过的滑块整块盖住。
	if !active && chip.Hovered() {
		chip.Background(theme.FillTertiary)
	}
	if chip.Clicked() {
		a.navFlip(&a.followsSeq)
		a.FollowsPlatform = id
	}
	chip.Children(func() {
		col := theme.TextSecondary
		if active {
			col = theme.AccentText
		} else if chip.Hovered() {
			col = theme.TextPrimary
		}
		txt := ui.Text(c, label).FontSize(theme.TextSM)
		txt.TextColor(txt.AnimateColor("chip-fg-"+id, col, theme.Slide()))
		if active {
			txt.FontWeight(600)
		} else {
			txt.FontWeight(500)
		}
	})
}

// followsChipTip 是一枚筛选项的悬停提示：这组筛选下有多少主播、多少在播。
// id 为空表示「全部」。搜索词生效时只报匹配数，和列表口径一致——
// 提示说的数字必须是屏幕上这份列表的数字，不然就成了两套统计。
func (a *App) followsChipTip(id string) string {
	base := a.Streamers
	if a.LiveMode {
		base = a.followStreamers()
	}
	keyword := strings.ToLower(strings.TrimSpace(a.FollowsQuery))
	total, live := 0, 0
	for _, s := range base {
		if id != "" && s.Platform != id {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(s.Name), keyword) {
			continue
		}
		total++
		if s.Live {
			live++
		}
	}
	if keyword != "" {
		return fmt.Sprintf("%d 位匹配", total)
	}
	return fmt.Sprintf("%d 位主播 · %d 正在直播", total, live)
}

// followsPlatforms 是筛选条要列出的平台：按顶栏顺序，只保留有关注的平台。
func (a *App) followsPlatforms() []string {
	present := make(map[string]bool, 4)
	for _, r := range a.Follows {
		present[r.Platform] = true
	}
	out := make([]string, 0, len(present))
	for _, id := range a.PlatformOrder {
		if present[id] {
			out = append(out, id)
		}
	}
	return out
}

// followsList 是关注页在当前平台筛选和搜索词下显示的主播。
//
// 真实数据模式下数据源是关注记录本身（关注了什么就显示什么），
// 视图测试没有关注记录时退回示例池。关键字按昵称包含匹配。
func (a *App) followsList() []Streamer {
	base := a.Streamers
	if a.LiveMode {
		base = a.followStreamers()
	}
	if a.FollowsPlatform != "" {
		id := a.FollowsPlatform
		filtered := make([]Streamer, 0, 16)
		for _, s := range base {
			if s.Platform == id {
				filtered = append(filtered, s)
			}
		}
		base = filtered
	}

	keyword := strings.ToLower(strings.TrimSpace(a.FollowsQuery))
	if keyword == "" {
		return base
	}
	out := make([]Streamer, 0, len(base))
	for _, s := range base {
		if strings.Contains(strings.ToLower(s.Name), keyword) {
			out = append(out, s)
		}
	}
	return out
}
