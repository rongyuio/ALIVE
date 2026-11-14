package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 展开后的面板尺寸。baseline 是 min(1000, 100% - 80) × min(720, 100% - 48)，
// 圆角 28。
const (
	islandPanelW float32 = 1000
	islandPanelH float32 = 720
	islandOpenR  float32 = 28
	// baseline 的光环：容器 1px 描边，::before inset:-1px + padding:2px，
	// 环压在描边上并向外扩 1px。本体让出环的内侧 1px。
	islandRingInset float32 = -1
	islandRingPad   float32 = 2
	islandBodyInset float32 = 1
	// 内容要等盒子长到这么宽才淡入，否则中途会看到网格被裁切重排
	islandContentW float32 = 640
)

// Island 是顶部居中的「灵动岛」。
//
// 收起态（baseline FollowsList.pill）：一双会眨的眼睛（9×11 的青柠色椭圆、
// 间隔 11），hover 时眼睛淡出、换成「N个直播」——常态不显示数字，更安静。
// 展开态是一块 1000×720 的关注面板，内容顺序：标题行 → 平台筛选 → 主播列表。
//
// 宽高与圆角走 AnimateTo，所以展开/收起是连续的；左上角每帧按当前宽度重算，
// 始终水平居中。
func (a *App) Island(c *ui.Context) {
	winW, winH := c.Size()

	// ---------- 目标尺寸
	tw, th, tr := theme.IslandW, theme.IslandH, theme.RadiusFull
	if a.IslandOpen {
		tw = min(islandPanelW, winW-80)
		th = min(islandPanelH, winH-48)
		tr = islandOpenR
	}

	// ---------- 动画驱动尺寸
	// 稳定 Key：展开时 Shell 会在岛前面插入 backdrop，兄弟节点位置变了，
	// 没有 Key 的话 MyGo 会把它当成新元素，AnimateTo 的动画状态随之丢失，
	// 面板就会瞬间跳到终态。
	//
	// baseline 的 .sidebar transition：width / height / border-radius 都是
	// 340ms cubic-bezier(0.33, 1.12, 0.6, 1)。1.12 带一点过冲，展开是
	// 「弹开」而不是慢慢撑；换成 spring 尾段会明显更慢。
	islandAnim := ui.Animation{
		Duration: 340 * time.Millisecond,
		Ease:     ui.CubicBezier(0.33, 1.12, 0.6, 1),
	}
	island := ui.Box(c).Key("island").Label("灵动岛").Absolute().Top(theme.IslandGap)
	w := island.AnimateTo("island-w", tw, islandAnim)
	h := island.AnimateTo("island-h", th, islandAnim)
	r := island.AnimateTo("island-r", tr, islandAnim)
	island.Width(w).Height(h).Radius(r).Left((winW-w)/2).
		// baseline 的 .sidebar 有 1px 描边，光环正压在这条描边上
		Border(1, theme.Border)

	// ---------- 投影：baseline 深浅两套（.sidebar 与 [data-theme="light"] .sidebar）。
	// 浅色主题用低透明度蓝黑，套深色的纯黑阴影会在白底上压出一圈脏灰。
	dark := theme.Current().Dark
	if a.IslandOpen {
		if dark {
			island.Shadow(0, 48, 96, -40, ui.RGBA(0, 0, 0, 0.95)).
				Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.42)).
				// color-mix(accent 55%, transparent)
				Shadow(0, 0, 70, -34, theme.Accent.Mix(ui.Transparent, 0.45)).
				InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.06))
		} else {
			island.Shadow(0, 34, 76, -40, ui.RGBA(15, 23, 42, 0.50)).
				Shadow(0, 12, 28, 0, ui.RGBA(15, 23, 42, 0.12)).
				Shadow(0, 0, 60, -34, theme.Accent.Mix(ui.Transparent, 0.45)).
				InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.80))
		}
	} else if dark {
		// 收起态是很散的一层：0 24px 56px -28px，几乎看不出投影边界。
		island.Shadow(0, 24, 56, -28, ui.RGBA(0, 0, 0, 0.90)).
			Shadow(0, 4, 14, 0, ui.RGBA(0, 0, 0, 0.36)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.05))
	} else {
		island.Shadow(0, 22, 52, -28, ui.RGBA(15, 23, 42, 0.34)).
			Shadow(0, 4, 12, 0, ui.RGBA(15, 23, 42, 0.10)).
			InsetShadow(0, 1, 0, 0, ui.RGBA(255, 255, 255, 0.80))
	}

	island.Children(func() {
		a.islandRing(c, r)

		// ---------- 本体：环内侧缩进 1.5px，和环做成兄弟节点
		body := ui.Box(c).Absolute().Top(islandBodyInset).Left(islandBodyInset).
			Right(islandBodyInset).Bottom(islandBodyInset).
			Radius(r - islandBodyInset).
			Background(theme.BgSecondary)
		// 圆角裁切跟着半径一起变，否则长到一半时内容会从直角处露出来
		body.ClipInset(0, 0, 0, 0, r-islandBodyInset)

		if !a.IslandOpen {
			// baseline：岛收起时清掉搜索词，下次展开是干净的全量列表
			a.IslandQuery = ""
			body.Cursor(ui.CursorPointer)
			if body.Clicked() {
				a.IslandOpen = true
				a.IslandSeq++
			}
			body.Children(func() {
				a.islandPill(c, body)
			})
			return
		}

		// 盒子还没长开时不画内容，避免看到网格被压扁再重排
		if w <= islandContentW {
			return
		}
		body.Children(func() {
			// baseline 的 island-body-in：180ms ease-out、延迟 200ms，
			// 等盒子长到位再淡入。淡入只作用在内容层，面板底色不跟着闪。
			content := ui.Box(c).Fill()
			op := content.Keyframes(fmt.Sprintf("island-body-in-%d", a.IslandSeq),
				[]ui.Keyframe{
					{Value: 0},
					{At: 200 * time.Millisecond, Value: 0},
					{At: 380 * time.Millisecond, Value: 1, Ease: ui.EaseOut},
				}, ui.KeyframeOptions{})
			content.Opacity(op)
			content.Children(func() {
				a.islandContent(c, w)
			})
		})
	})
}

// islandPill 是收起态：一双眼睛，hover 换成数字。
func (a *App) islandPill(c *ui.Context, body ui.Element) {
	hovered := body.Hovered()

	faceTarget, countTarget := float32(1), float32(0)
	if hovered {
		faceTarget, countTarget = 0, 1
	}

	// ---- 眼睛
	face := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0)
	faceOp := face.AnimateTo("pill-face", faceTarget, theme.EaseOut(150*time.Millisecond))
	face.Opacity(faceOp)
	face.Children(func() {
		ui.Row(c).Fill().Center().Gap(11).Children(func() {
			// baseline 的 eye-blink：4.6s ease-in-out 循环，94% 时 scaleY 0.12。
			// transform-origin 默认居中；reduced motion 下框架直接停在最终值 1。
			eye := func() {
				e := ui.Box(c).Width(9).Height(11).Radius(theme.RadiusFull).
					Background(theme.AccentInk)
				// 用 Loop 而不是 Keyframes：这版 MyGo 的 RepeatForever 播完
				// 一次就停，眼睛只会眨一下。reduced motion 下不眨眼。
				if !c.Preferences().ReduceMotion {
					sy := blinkScale(e.Loop("blink", 4600*time.Millisecond, ui.Linear))
					e.Scale(1, sy)
				}
			}
			eye()
			eye()
		})
	})

	// ---- 数字：从下方 2px 淡入
	count := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0)
	countOp := count.AnimateTo("pill-count", countTarget, theme.EaseOut(220*time.Millisecond))
	count.Opacity(countOp)
	count.Translate(0, (1-countOp)*2)
	count.Children(func() {
		// 有关注才报「N个直播」，一个都没有就只写「关注」
		label := "关注"
		if n := len(a.followStreamers()); n > 0 {
			label = fmt.Sprintf("%d个直播", a.liveCount())
		}
		ui.Box(c).Fill().Center().Children(func() {
			ui.Text(c, label).FontSize(theme.TextSM).FontWeight(600).
				TextColor(theme.TextPrimary).SingleLine()
		})
	})
}

// islandBackdrop 是展开态背后的一整张透明层：点空白处收起。
func (a *App) islandBackdrop(c *ui.Context) {
	bg := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0)
	if bg.Clicked() {
		a.IslandOpen = false
	}
}

// islandRing 画那圈贴着边缘跑的 conic 流光。
//
// baseline（Sidebar.module.css 的 .sidebar::before）：
//   - conic 渐变，底子是 30% 的主题强调色，105° 处一道全亮的头
//   - 4.5s 线性绕行一圈；展开成面板后放慢到 9s、整体压到 0.55，
//     只负责把面板边缘「点着」
//   - 只留 padding 那一圈：content-box 取反的遮罩
//
// 所以环的颜色必须跟着配色走（--accent-ink），不能写死青柠；
// 旋转用 Loop 持续请求帧，动的是光，不是盒子。
func (a *App) islandRing(c *ui.Context, r float32) {
	black := ui.RGB(0, 0, 0)
	solid := ui.LinearGradient{From: black, To: black}

	period := 4500 * time.Millisecond
	alpha := float32(1)
	if a.IslandOpen {
		period = 9 * time.Second
		alpha = 0.55
	}

	dim := theme.AccentInk.Mix(ui.Transparent, 0.70) // 30% 的强调色
	hot := theme.AccentInk

	ring := ui.Box(c).Absolute().
		Top(islandRingInset).Left(islandRingInset).
		Right(islandRingInset).Bottom(islandRingInset).
		Radius(r - islandRingInset).Padding(islandRingPad)
	angle := ring.Loop("island-ring", period, ui.Linear) * 360
	ring.ConicGradient(ui.ConicGradient{
		Angle: angle,
		Oklab: true,
		Stops: []ui.GradientStop{
			{Color: dim, At: 0},
			{Color: dim, At: 40.0 / 360},
			{Color: hot, At: 105.0 / 360},
			{Color: dim, At: 165.0 / 360},
			{Color: dim, At: 1},
		},
	}).Masks(
		ui.Mask{Image: solid, Box: ui.MaskContentBox, Composite: ui.MaskExclude},
		ui.Mask{Image: solid},
	).Opacity(alpha)
}

// livePulse 把 0–1 的周期相位换成脉冲环的 scale / opacity：
// 0% 时 0.75 / 0.9，70% 时扩到 1.85 并完全透明，之后保持到周期结束。
// 缓动与 baseline 的 --ease-out 同一档。
func livePulse(p float32) (scale, alpha float32) {
	if p >= 0.7 {
		return 1.85, 0
	}
	e := easeOutUnit(p / 0.7)
	return 0.75 + e*1.10, 0.9 * (1 - e)
}

// blinkScale 把 4.6s 的周期相位换成眼睛的 scaleY：
// 90% 之前睁着，90%–94% 闭到 0.12，94%–100% 睁开。
func blinkScale(p float32) float32 {
	switch {
	case p < 0.90:
		return 1
	case p < 0.94:
		return 1 - (p-0.90)/0.04*0.88
	default:
		return 0.12 + (p-0.94)/0.06*0.88
	}
}

// easeOutUnit 是 baseline --ease-out 在 0–1 区间上的求值。
func easeOutUnit(t float32) float32 {
	return ui.CubicBezier(0.22, 1, 0.36, 1)(t)
}

// islandContent 是展开后的关注面板。
//
// 按 baseline FollowsList 的三段结构：
//
//	header  20/20/14：24px 粗标题 + 「N 位主播 · M 正在直播」+ 搜索框 + 动作组
//	filters 0/20/14：平台筛选用 30 高的胶囊
//	grid    0/14/16：minmax(248px,1fr)、gap 4 的头像行列表
func (a *App) islandContent(c *ui.Context, panelW float32) {
	list := a.islandList()
	live := liveCountOf(list)
	total := len(list)

	ui.Column(c).Fill().Children(func() {
		// ------------------------------------------------ header
		ui.Row(c).Padding(20, 20, 14, 20).Gap(16).AlignItems(ui.Start).
			Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, "全部关注").FontSize(theme.TextXL).FontWeight(700).
						LetterSpacing(-0.02).TextColor(theme.TextPrimary)
					// baseline：有搜索词时副标题显示「N 位匹配」
					subtitle := fmt.Sprintf("%d 位主播 · %d 正在直播", total, live)
					if strings.TrimSpace(a.IslandQuery) != "" {
						subtitle = fmt.Sprintf("%d 位匹配", total)
					}
					ui.Text(c, subtitle).
						FontSize(theme.TextSM).TextColor(theme.TextTertiary)
				})

				ui.Box(c).Grow(1)

				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					// 搜索框：220 宽、34 高、图标在左，真输入框。
					// baseline 的 .panelSearch：focus-within 时边框变 accent-ink，190ms。
					search := ui.Row(c).Width(220).Height(34).Padding(0, 8, 0, 11).Gap(7).
						Radius(theme.RadiusFull).Background(theme.FillQuaternary).
						Center().Cursor(ui.CursorText)
					border := theme.Border
					if search.FocusWithin() {
						border = theme.AccentInk
					}
					search.Border(1, search.AnimateColor("island-search-border", border,
						theme.EaseOut(190*time.Millisecond)))
					search.Children(func() {
						ui.Icon(c, iconSearch).FontSize(14).TextColor(theme.TextTertiary)
						ui.TextInputBase(c, &a.IslandQuery).FontSize(theme.TextXS).Grow(1).
							Bind(&a.IslandSearch).
							Placeholder("搜索主播").PlaceholderColor(theme.TextTertiary).
							Label("搜索已关注的主播").
							OnTextKey(func(k ui.TextKey) bool {
								// 和顶栏搜索、分区搜索一致：一次 Esc 清空并收起所在浮层
								if k.Key != ui.KeyEscape {
									return false
								}
								a.IslandQuery = ""
								a.IslandOpen = false
								a.IslandSearch.CancelFocus(c)
								return true
							})
						if a.IslandQuery != "" {
							clear := ui.Box(c).Size(18, 18).Radius(9).Center().
								Cursor(ui.CursorPointer)
							if clear.Hovered() {
								clear.Background(theme.FillTertiary)
							}
							if clear.Clicked() {
								a.IslandQuery = ""
							}
							clear.Children(func() {
								ui.Icon(c, iconX).FontSize(12).TextColor(theme.TextTertiary)
							})
						}
					})

					// 动作组：刷新 + 收起
					ui.Row(c).Gap(2).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
						Background(theme.FillQuaternary).Border(1, theme.Border).
						Children(func() {
							a.islandAction(c, iconSync, a.followsBusy, a.RefreshFollows)
							a.islandAction(c, iconClose, false, func() {
								a.IslandOpen = false
							})
						})
				})
			})

		// ------------------------------------------------ 平台筛选
		//
		// baseline 的筛选条不是固定四个平台：只列「确实有关注主播」的平台，
		// 顺序跟顶栏的 tab 顺序一致；一个关注都没有时整条不出现。
		if platforms := a.islandPlatforms(); len(platforms) > 0 {
			ui.Row(c).Gap(6).Padding(0, 20, 14, 20).Children(func() {
				a.islandPlatformChip(c, "", "全部")
				for _, id := range platforms {
					a.islandPlatformChip(c, id, platformLabel(id))
				}
			})
		}

		// ------------------------------------------------ 列表
		gridW := panelW - 2*14
		const gap = float32(4)
		const minW = float32(248)

		cols := int((gridW + gap) / (minW + gap))
		if cols < 1 {
			cols = 1
		}
		cellW := (gridW - gap*float32(cols-1)) / float32(cols)

		ui.Scroll(c).Grow(1).Padding(0, 14, 16, 14).Children(func() {
			// baseline 的空态：flex 1、padding 64 20、居中、13px tertiary
			if len(list) == 0 {
				// baseline 的空态分两种：一个都没关注，和筛选/搜索之后没命中
				empty := "没有匹配的主播"
				if len(a.followStreamers()) == 0 {
					empty = "还没有关注的主播"
				}
				ui.Box(c).Width(gridW).Padding(64, 20, 64, 20).Center().Children(func() {
					ui.Text(c, empty).FontSize(theme.TextSM).
						TextColor(theme.TextTertiary)
				})
				return
			}
			ui.Grid(c).Width(gridW).Columns(cols).Gap(gap).Children(func() {
				for i := range list {
					a.islandRow(c, list[i], cellW)
				}
			})
		})
	})
}

// islandRow 是面板里的一行：40px 圆头像（右下角在线点）+ 昵称 / 直播间标题。
func (a *App) islandRow(c *ui.Context, s Streamer, w float32) {
	row := ui.Row(c).Width(w).Gap(12).Padding(10, 12, 10, 12).
		Radius(theme.RadiusMD).Cursor(ui.CursorPointer)
	hovered := row.Hovered()
	if hovered {
		row.Background(theme.FillQuaternary)
	}
	if row.Clicked() {
		a.Player = s
		a.IslandOpen = false
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
			ui.Text(c, s.Name).FontSize(theme.TextSM).FontWeight(600).
				SingleLine().TextColor(nameCol)

			title := s.Title
			if title == "" {
				title = "暂无直播标题"
			}
			ui.Text(c, title).FontSize(theme.TextXS).SingleLine().
				TextColor(theme.TextTertiary)
		})
	})
}

// islandAction 是头部右侧动作组里的一枚 28×28 圆形按钮。
func (a *App) islandAction(c *ui.Context, ico *ui.SVG, busy bool, on func()) {
	btn := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer)
	hovered := btn.Hovered()
	if hovered {
		btn.Background(theme.FillTertiary)
	}
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if hovered {
			col = theme.TextPrimary
		}
		icon := ui.Icon(c, ico).FontSize(16).TextColor(col)
		if busy {
			// 刷新中：图标匀速转一圈，兼作进度提示
			icon.Rotate(btn.Loop("action-spin", 900*time.Millisecond, ui.Linear) * 360)
		}
	})
}

// islandPlatformChip 是关注面板顶部的一枚平台筛选 chip。id 为空表示「全部」。
func (a *App) islandPlatformChip(c *ui.Context, id, label string) {
	active := id == a.IslandPlatform
	chip := ui.Box(c).Height(30).Padding(0, 14, 0, 14).
		Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
		Border(1, ui.Transparent)
	if active {
		chip.Background(theme.Accent)
	} else {
		chip.Background(theme.FillQuaternary)
		if chip.Hovered() {
			chip.Background(theme.FillTertiary)
		}
	}
	if chip.Clicked() {
		a.IslandPlatform = id
	}
	chip.Children(func() {
		col := theme.TextSecondary
		if active {
			col = theme.AccentText
		}
		txt := ui.Text(c, label).FontSize(theme.TextXS).TextColor(col)
		if active {
			txt.FontWeight(600)
		} else {
			txt.FontWeight(500)
		}
	})
}

// islandPlatforms 是筛选条要列出的平台：按顶栏顺序，只保留有关注的平台。
func (a *App) islandPlatforms() []string {
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

// islandList 是关注面板在当前平台筛选和搜索词下显示的主播。
//
// 真实数据模式下数据源是关注记录本身（关注了什么就显示什么），
// 视图测试没有关注记录时退回示例池。关键字按昵称包含匹配。
func (a *App) islandList() []Streamer {
	base := a.Streamers
	if a.LiveMode {
		base = a.followStreamers()
	}
	if a.IslandPlatform != "" {
		id := a.IslandPlatform
		filtered := make([]Streamer, 0, 16)
		for _, s := range base {
			if s.Platform == id {
				filtered = append(filtered, s)
			}
		}
		base = filtered
	}

	keyword := strings.ToLower(strings.TrimSpace(a.IslandQuery))
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

// liveCount 数一下关注里正在直播的数量（岛收起时显示的那个数）。
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
