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

// FollowsPage 是「全部关注」页。
//
// 它原来是灵动岛的展开面板（浮层）。浮层压在页面上、点外面就消失，
// 放到顶栏做成一个 tab 之后它就是普通页面：和首页、订阅页同一层级、
// 同一套正文 padding，切过去看、切回来内容还在。
func (a *App) FollowsPage(c *ui.Context) {
	pad := a.bodyPadX(c)
	list := a.followsList()

	ui.Column(c).Grow(1).Padding(0, pad, 0, pad).Gap(14).Children(func() {
		a.followsBar(c)
		a.followsGrid(c, list)
	})
}

// followsBar 是页头那一整条：平台胶囊 → 搜索框 → 刷新。
//
// 和订阅页的 subscribeBar 同一条语言：一层 BgSecondary 的圆角面板装住里面的
// 胶囊，受光边用 inset 0 1px 0 —— 这样顶部是个整体，不是一堆散 pill 浮在
// 页面上。高度 = 34 的胶囊 + 上下 8/10 的内边距 = 52，和首页分区栏对齐，
// 几个 tab 之间切的时候正文起点不会跳。
//
// 没有标题，也没有常驻的计数文字：页面名已经在顶栏 tab 上，正文里再写一遍
// 是重复；「N 位主播 · N 正在直播」放进每枚胶囊的悬停提示里，常态这一行
// 只留筛选和搜索，指针停住才出数字。
func (a *App) followsBar(c *ui.Context) {
	bar := ui.Row(c).Gap(10).Padding(8, 12, 10, 12).
		Radius(theme.RadiusMD).Background(theme.BgSecondary)
	// inset 0 1px 0 rgba(255,255,255,.04)：和卡片、订阅条同一套受光边
	bar.InsetShadow(0, 1, 0, 0, theme.TopLight)

	bar.Children(func() {
		// 平台胶囊：只列「确实有关注主播」的平台，顺序跟顶栏 tab 一致
		ui.Row(c).Gap(6).Children(func() {
			a.followsPlatformChip(c, "", "全部")
			for _, id := range a.followsPlatforms() {
				a.followsPlatformChip(c, id, platformLabel(id))
			}
		})

		ui.Box(c).Grow(1)

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			// 搜索框：34 高、图标在左，真输入框。动效跟分区搜索同一套：常态 176，聚焦或有输入时 240ms 展开到 220。
			// baseline 的 .panelSearch：focus-within 时边框变 accent-ink，190ms。
			search := ui.Row(c).Height(34).Padding(0, 8, 0, 11).Gap(7).
				Radius(theme.RadiusFull).Background(theme.FillQuaternary).
				Center().Cursor(ui.CursorText)
			border := theme.Border
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
						Cursor(ui.CursorPointer)
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
			// 外面不再套胶囊——bar 本身就是容器，叠第二层底会在同色上留一道边。
			a.followsAction(c, iconSync, a.followRefreshLabel(),
				a.followsBusy, a.followRingProgress(), a.refreshFollowsManual)
		})
	})
}

// followsGrid 是主播行列表。
//
// 行宽按 baseline 的 minmax(248px, 1fr)、gap 4 算：列数随可用宽度增减，
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
		// baseline 的空态：padding 64 20、居中、13px tertiary
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
				a.followsRow(c, list[i], cellW)
			}
		})
	})
}

// followsRow 是一行：40px 圆头像（右下角在线点）+ 昵称 / 直播间标题。
func (a *App) followsRow(c *ui.Context, s Streamer, w float32) {
	row := ui.Row(c).Width(w).Gap(12).Padding(10, 12, 10, 12).
		Radius(theme.RadiusMD).Cursor(ui.CursorPointer)
	hovered := row.Hovered()

	// 动画 key 绑在主播身份上：列表重排时同一行的渐亮状态跟着它走，不会重启。
	key := s.Platform + "|" + s.RoomID + "|" + s.Name
	// baseline 的 .panelCard 是 transition: background-color 190ms ease-out，
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

	row.Children(func() {
		wrap := ui.Box(c).Size(40, 40)
		wrap.Children(func() {
			if bmp := media.Default.Bitmap(s.AvatarURL, 40); bmp != nil {
				ui.Image(c, bmp).Fill().Fit(ui.Cover).Radius(theme.RadiusFull)
			} else {
				ui.Box(c).Fill().Radius(theme.RadiusFull).
					Background(ui.Oklch(0.42, 0.10, s.Hue+180)).Center().
					Children(func() {
						ui.Text(c, initial(s.Name)).FontSize(14).FontWeight(600).
							TextColor(ui.RGBA(255, 255, 255, 0.88))
					})
			}
			// 在线点：13×13，2px 的地面色描边把它从头像上「抠」出来
			//
			// 脉冲环与点同级、先画（压在点下面），并带稳定 Key：
			// Keyframes 的状态绑在元素身份上，身份一抖动画就每帧重置，
			// 环会永远停在 scale 0.75、缩在点内部看不见。
			dotKey := "live-" + s.Platform + "|" + s.Name + "|" + s.Category
			if s.Live {
				// baseline 的 live-pulse：2px 绿环从 scale 0.75 扩到 1.85、
				// 透明度 0.9 → 0，2s ease-out 循环，70%–100% 停在扩满的无形状态。
				//
				// 相位用 Loop 驱动：这版 MyGo 的 Keyframes + RepeatForever
				// 播完一次就停在末值（环会扩满后透明，看起来只剩绿点）。
				ring := ui.Box(c).Key(dotKey+"-ring").
					Absolute().Right(-1).Bottom(-1).Size(15, 15).
					Radius(theme.RadiusFull).Border(2, theme.StatusOnline)
				if c.Preferences().ReduceMotion {
					// baseline 在 reduced motion 下关掉脉冲，只留静态绿点
					ring.Opacity(0)
				} else {
					p := ring.Loop("live-pulse", 2*time.Second, ui.Linear)
					scale, alpha := livePulse(p)
					ring.Scale(scale, scale)
					ring.Opacity(alpha)
				}
			}

			dot := ui.Box(c).Key(dotKey+"-dot").
				Absolute().Right(0).Bottom(0).Size(13, 13).
				Radius(theme.RadiusFull).Border(2, theme.BgSecondary)
			if s.Live {
				dot.Background(theme.StatusOnline)
			} else {
				// baseline：离线点深色主题 #475569，浅色主题 #94a3b8
				off := ui.Hex("#475569")
				if !theme.Current().Dark {
					off = ui.Hex("#94a3b8")
				}
				dot.Background(off)
			}
		})

		ui.Column(c).Grow(1).Gap(3).Children(func() {
			nameCol := theme.TextPrimary
			if hovered {
				nameCol = theme.AccentInk
			}
			// baseline 的 .panelName 也是 190ms 过渡：hover 时名字才慢慢染成强调色
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
// 对齐 baseline 的 .actionBtn：常态只吃 hover 底色；busy 时按 :disabled
// 处理——降透明度、光标变回箭头、不再有悬停反馈也不再响应点击。
// progress ≥ 0 且 busy 时，图标外再套一圈进度环。
func (a *App) followsAction(c *ui.Context, ico *ui.SVG, label string, busy bool, progress float32, on func()) {
	// baseline 的按钮有 aria-label / title，读屏和自动化都靠它认按钮
	btn := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).Center().Label(label)
	if busy {
		// baseline：.actionBtn:disabled { cursor: default; opacity: .72 }
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

	// 进度环：值走 AnimateTo，复现 baseline .indicator 的 220ms
	// stroke-dashoffset 过渡；不显示时也把动画值拉回 0，
	// 否则下一轮会从上一次的满格倒着缩回来。
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
			// baseline：.actionBtn svg 的 transform 有 500ms 过渡，
			// 转圈停下时图标滑回正位，而不是啪地跳回去。
			icon.Rotate(btn.AnimateTo("action-spin-stop", 0, followsSpinStop))
		}
	})
}

// followsAction 的进度环与收尾动画度量，均取自 baseline 的 FollowsList.module.css。
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

// refreshRing 画关注刷新按钮上的圆环进度，对齐 baseline 的 RingProgress：
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
func (a *App) followsPlatformChip(c *ui.Context, id, label string) {
	active := id == a.FollowsPlatform
	// 高度 34 和订阅页的 chip 一致：同一条胶囊条里的东西要同一个身高
	chip := ui.Box(c).Height(34).Padding(0, 15, 0, 15).
		Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
		Border(1, ui.Transparent).
		// 数据规模不常驻：指针停在这枚胶囊上才出「N 位主播 · N 正在直播」
		Tooltip(a.followsChipTip(id))

	// baseline 的 .panelFilter 有 transition: background-color / color 190ms；
	// 切换筛选时整条 chip 是渐变的，硬切会显得列表「跳」一下。
	bg := theme.FillQuaternary
	switch {
	case active:
		bg = theme.Accent
	case chip.Hovered():
		bg = theme.FillTertiary
	}
	chip.Background(chip.AnimateColor("chip-bg-"+id, bg, theme.EaseOut(followsDur2)))
	if chip.Clicked() {
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
		txt.TextColor(txt.AnimateColor("chip-fg-"+id, col, theme.EaseOut(followsDur2)))
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

// liveCount 数一下关注里正在直播的数量（灵动岛 hover 时显示的那个数）。
func (a *App) liveCount() int {
	if a.LiveMode {
		return liveCountOf(a.followStreamers())
	}
	return liveCountOf(a.Streamers)
}

func liveCountOf(list []Streamer) int {
	n := 0
	for _, s := range list {
		if s.Live {
			n++
		}
	}
	return n
}
