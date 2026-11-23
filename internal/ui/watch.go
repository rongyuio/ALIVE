package ui

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
	"alive/internal/watch"
)

// 激活绿点的几何。直径 10 和灵动岛上那颗保持一致；离格子边 12——间距
// 令牌里的一档，也正好落在 12 的格子圆角内侧，不贴视频井的裁切边。
const (
	watchLiveDotSize  float32 = 10
	watchLiveDotInset float32 = 12

	// 观测墙控制条的紧凑尺寸。一格只有三百来像素宽，播放页那套
	// 44 高玻璃条 + 30 热区 + 24 图标会占掉近半格，压在画面上很重。
	watchBarRowH   float32 = 34
	watchBarRadius float32 = 12
	watchIconSize  float32 = 16
	watchIconHit   float32 = 24
	watchVolumeW   float32 = 64
)

// watchWall 是「悲鸣观测」的内容层：把观测盒里的房间按 1–12 间铺满这一屏。
//
// 它不滚动，也不显示弹幕。每一格都是一个无弹幕的单直播间缩小版：全档取流、
// 静音、按格子物理像素呈现；单击切音频焦点，双击或点岛进播放页。
func (a *App) watchWall(c *ui.Context) {
	rooms := a.WatchRooms
	if len(rooms) == 0 {
		a.watchEmpty(c)
		return
	}
	box := ui.Box(c).Key("watch-wall").Grow(1).Clip()
	b := box.Bounds()
	if b.W <= 0 || b.H <= 0 {
		// Bounds 是上一帧的布局。第一帧先把容器交给布局，下一帧再摆格子。
		c.After(time.Millisecond)
		return
	}

	items := make([]watch.Item, len(rooms))
	for i, room := range rooms {
		key := watchKey(room)
		items[i] = watch.Item{Key: key, Aspect: a.watchAspect(key)}
	}
	rects := watch.Layout(items, b.W, b.H, theme.GridGap)
	rm := make(map[string]watch.Rect, len(rects))
	for i, r := range rects {
		rm[watchKey(rooms[i])] = r
	}
	a.watchShow(rm)

	box.Children(func() {
		// 落点提示要等所有格子都建完才知道谁在拖：Dragging / DragOver 只有
		// 格子自己读得到。先画格子，收集这两个信号，再把编号层画在它们上面。
		dragging, overKey := false, ""
		for i, room := range rooms {
			drag, over := a.watchTile(c, room, rects[i])
			dragging = dragging || drag
			if over {
				overKey = watchKey(room)
			}
		}
		if !dragging {
			return
		}
		for i, room := range rooms {
			a.watchDropZone(c, rects[i], i+1, watchKey(room) == overKey)
		}
	})
}

// watchAspect 是观测墙统一的格子比例。
//
// 墙上不再按每一路源比例排版：平台声明值并不可靠（同一批 B 站房间会给出
// 4:3，实际是 16:9），混排会把一面墙切成高低不齐的几条。统一 16:9，
// 视频用 Contain 放进去：16:9 的流正好铺满，竖屏/4:3 左右留黑，不裁剪。
func (a *App) watchAspect(key string) float32 {
	_ = key
	return watch.DefaultAspect
}

func watchTileLabel(s Streamer) string {
	return "观测:" + platformLabel(s.Platform) + " " + s.Name + " " + s.RoomID
}

// watchTile 画一格，并回报它是不是拖动源、是不是指针下的落点。这两个信号
// 只有格子自己读得到，watchWall 收集后再画落点提示。
func (a *App) watchTile(c *ui.Context, room Streamer, r watch.Rect) (dragging, dropOver bool) {
	key := watchKey(room)
	active := key == a.WatchActive
	tile := ui.Box(c).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Radius(theme.RadiusMD).Clip().Background(theme.BgSecondary).
		Cursor(ui.CursorPointer).Focusable().
		Label(watchTileLabel(room))
	tile.InsetShadow(0, 1, 0, 0, theme.TopLight)
	// 这一格是"同一间房"在墙上的那一份。进播放页时舞台拿同一个身份，
	// 于是画面从这块矩形长到整屏，而不是硬切（见 surface_motion.go）。
	tile.Transition(ui.ElementTransition{
		Animation: &ui.Snappy, Match: surfaceMatch{key}, Fly: true,
	})
	if tile.Clicked() {
		// 点任何一间 = 看这一间。和首页卡片、关注行、搜索结果同一个语义：
		// 全应用里"点一张直播画面"只有这一种结果。
		//
		// 以前这里切的是声音焦点：画面不动，只多一颗 10px 绿点，声音从别的
		// 格子换过来。用户点完看不出发生了什么——长得一样的卡片做了不一样
		// 的事，这是最贵的一类不一致。
		a.watchOpenPlayer(room)
	}
	tile.Drag(key)
	_, dropOver = ui.DragOver[string](tile)
	if dragged, dropped := ui.Drop[string](tile); dropped && dragged != key {
		a.watchSwap(dragged, key)
	}
	dragging = tile.Dragging()
	if dragging {
		tile.Opacity(0.35)
	}
	player := a.watchPlayer(key)
	sess := a.watch.sessions[key]
	tile.Children(func() {
		// 视频井、视频、封面都是绝对定位的图层，不参与流式布局。
		// 原来它们是三个 Fill 兄弟，MyGo 的 flex 收缩会把 100% 高度
		// 平分：井占上半截、封面只在下半截，未加载态就是黑+占位色两块。
		// 播放页的 stageFrame 也是绝对四边贴死，同一个理由。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(theme.IslandInk)
		if player != nil {
			ui.Video(c, player).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Fit(ui.Contain)
		}
		if player == nil || !player.Ready() {
			a.watchCover(c, room, r, player != nil)
		}
		// 激活标记必须画在视频井之后：格子自己的 InsetShadow 在填充层里，
		// 被铺满整格的井盖住，屏幕上看不见。
		if active {
			a.watchLiveDot(c, key)
		}
		// 信息与控件收成一条底栏，遮罩就是它本身；常态不显示，hover 或
		// 键盘聚焦才出现。墙上因此只剩画面和 active 那颗绿点。
		if tile.Hovered() || tile.FocusVisible() {
			a.watchBar(c, room, active, r.W)
		}
		a.watchState(c, room, sess, player)
	})
	return dragging, dropOver
}

// watchDropZone 是拖动格子时的落点提示：整格一层强调色底、居中一个大号序号。
//
// 每个候选格都画，编号就是松手后的位置——拖拽是交换，目标格拿到拖动项，
// 源格拿到目标项。这一层画在墙的层级上、所有格子之后：格子内部的标记会被
// 视频井盖住，墙层的兄弟节点才轮得到画在最上面。
//
// 指针下的目标格只把底色加重，不另加描边：MyGo 会把被拖格子的副本跟着
// 指针画在最上层，描边不是被副本盖掉，就是变成一圈游离在格子外的框。
func (a *App) watchDropZone(c *ui.Context, r watch.Rect, n int, over bool) {
	alpha := float32(0.16)
	if over {
		alpha = 0.34
	}
	zone := ui.Box(c).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Radius(theme.RadiusMD).
		Background(theme.Accent.Mix(ui.Transparent, alpha)).
		Label(fmt.Sprintf("第 %d 格", n)).
		IgnoreInput(true)
	zone.Center().Children(func() {
		// 序号本身就是名字，别让读屏把数字再读一遍。
		ui.Text(c, strconv.Itoa(n)).
			FontSize(theme.Text2XL).FontWeight(700).
			TextColor(theme.MediaText).
			TextShadow(0, 2, 8, theme.MediaShadowStrong).
			AccessHidden(true)
	})
}

// watchLiveDot 是激活格的标记：左上角一颗会脉冲的绿点。
//
// 视觉语言照抄关注页和灵动岛：2px 绿环从 scale 0.75 扩到 1.85、透明度
// 0.9 → 0，2 秒一循环（livePulse），点本身常亮。墙上因此不需要第三种
// 「正在出声」的表达。
//
// 两个元素都 PassThrough：它们只是装饰，压在格子上不能吞掉激活/进播放页
// 的点击——否则左上角十几个像素会变成死区。
func (a *App) watchLiveDot(c *ui.Context, key string) {
	dotKey := "watch-live-" + key
	ringSize := watchLiveDotSize + 4
	// 环比点宽 2，保持同心：两边各外扩 2。
	ringInset := watchLiveDotInset - (ringSize-watchLiveDotSize)/2

	ring := ui.Box(c.Key(dotKey+"-ring")).Absolute().
		Left(ringInset).Top(ringInset).Size(ringSize, ringSize).
		Radius(theme.RadiusFull).Border(2, theme.StatusOnline).PassThrough()
	if c.Preferences().ReduceMotion {
		// reduced motion 下不脉冲，只留静态绿点
		ring.Opacity(0)
	} else {
		// 相位用 Loop 驱动，理由同关注页：这版 MyGo 的 Keyframes +
		// RepeatForever 播完一次就停在末值，环会扩满后透明。
		p := ring.Loop(dotKey+"-pulse", 2*time.Second, ui.Linear)
		scale, alpha := livePulse(p)
		ring.Scale(scale, scale)
		ring.Opacity(alpha)
	}

	// 深色描边把绿点从亮画面上「抠」出来，和关注页那颗是同一套做法。
	ui.Box(c.Key(dotKey)).Absolute().
		Left(watchLiveDotInset).Top(watchLiveDotInset).
		Size(watchLiveDotSize, watchLiveDotSize).
		Radius(watchLiveDotSize/2).
		Background(theme.StatusOnline).
		Border(2, theme.OverlayInk).
		PassThrough()
}

// watchCover 是没有画面时的底：真封面（可裁）或首页卡片同款占位。
//
// 每层都绝对铺满整格：和视频井一样不能走流式 Fill，否则会被平分。
func (a *App) watchCover(c *ui.Context, room Streamer, r watch.Rect, connecting bool) {
	if bmp := media.Default.Bitmap(room.CoverURL, r.W); bmp != nil {
		ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover)
	} else {
		// 和首页卡片同一套占位（coverArt）：中性底 + 中心柔光。
		// 原来按房间色相画色块，冷启动缓存里还没有封面时整面墙先亮一片绿，
		// 和真封面的质感完全对不上。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Children(func() {
			coverArt(c, r.W, r.H)
		})
	}
	if connecting {
		// 有播放器但还没首帧：只压一层很淡的暗，不挡住“正在接通”的提示。
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
			Background(theme.MediaScrimSoft)
	}
}

// watchBar 是格子唯一的叠层：底栏、遮罩、信息、控件是一块。
//
// 第一行：品牌色平台胶囊 + 主播名 + 标题。
// 第二行：直接复用播放页控制台的结构——44 高玻璃条、左侧播放/刷新/音量，
// 右侧放大/移出。弹幕、清晰度、线路、小窗、两种全屏都精简掉。
// 只有 hover / 键盘聚焦时构建。
func (a *App) watchBar(c *ui.Context, room Streamer, active bool, w float32) {
	bar := ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(84).
		Background(theme.MediaScrimTile).Padding(6, 10, 6, 10)
	bar.Children(func() {
		ui.Column(c).Fill().Gap(4).Justify(ui.Center).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Height(20).Padding(0, 8, 0, 8).Radius(theme.RadiusFull).
					Background(platformBrand(room.Platform)).Center().Children(func() {
					ui.Text(c, platformLabel(room.Platform)).
						FontSize(theme.Text2XS).FontWeight(600).
						TextColor(theme.IslandInk).SingleLine()
				})
				ui.Text(c, room.Name).FontSize(theme.TextSM).FontWeight(600).
					TextColor(theme.MediaText).SingleLine()
				title := room.Title
				if title == "" {
					title = "暂无直播标题"
				}
				ui.Box(c).Grow(1).Clip().Children(func() {
					ui.Text(c, title).FontSize(theme.Text2XS).
						TextColor(theme.MediaTextDim).SingleLine()
				})
			})
			a.watchControlBar(c, room, active, w)
		})
	})
}

// watchControlBar 是播放页 playerControls 的精简版。
//
// 玻璃条比播放页小一档：34 高、12 圆角、24 热区 + 16 图标、音量条 64。
// 结构和左右两组不变，只是去掉弹幕组、清晰度/线路下拉、小窗和两种全屏，
// 右边只留放大和移出。
func (a *App) watchControlBar(c *ui.Context, room Streamer, active bool, w float32) {
	s := watchBarSizeFor(w)
	m := s.controls
	bar := ui.Row(c).Height(s.rowH).Padding(s.padY, s.padX, s.padY, s.padX).
		Radius(s.radius).FillWidth().
		Border(1, playerBarBorder).AlignItems(ui.Center)
	if c.Preferences().ReduceTransparency {
		bar.Background(playerBarBg)
	} else {
		bar.Material(glass.Glass{Style: glass.Regular, Tint: playerBarBg})
	}
	bar.Shadow(0, 18, 44, -20, theme.MediaShadowStrong).
		InsetShadow(0, 0, 0, 1, theme.TopLight)

	bar.Children(func() {
		// 左：播放 / 重新加载 / 音量（音量带滑条和百分比）。
		// 播放/暂停切的是这一格自己的播放状态，不改变 active、不换台。
		// 音量仍是单直播间那条全局音量；刷新只重载这一格。
		ui.Row(c).Gap(m.leftGap).AlignItems(ui.Center).Children(func() {
			key := watchKey(room)
			playing := active && a.Playing
			if !active {
				if s := a.watch.sessions[key]; s != nil && s.player != nil {
					playing = s.player.Playing()
				}
			}
			playIcon, playLabel := iconPlay, "播放"
			if playing {
				playIcon, playLabel = iconPause, "暂停"
			}
			a.iconBtnSized(c, playIcon, m.iconSize, m.iconHit, false, playLabel, func() {
				if active {
					a.togglePlayback()
					return
				}
				s := a.watch.sessions[key]
				if s == nil || s.player == nil {
					return
				}
				if s.player.Playing() {
					s.player.Pause()
				} else {
					s.player.Play()
				}
			})
			a.iconBtnSized(c, iconSync, m.iconSize, m.iconHit, false, "重新加载", func() {
				// active 不总在媒体层：取流中 / 未开播的 active 只是墙上的
				// pending 会话，retryStream 会因为 media.roomKey=="" 直接
				// 返回。watchReload 自己分这两条路。
				a.watchReload(room)
			})
			if active {
				a.volumeControlFor(c, m, room.Name)
			} else {
				a.watchTileAudio(c, m, room)
			}
		})

		ui.Box(c).Grow(1)

		// 右：放大 / 移出。放大就是进播放页，移出就是离开观测盒。
		ui.Row(c).Gap(m.rightGap).AlignItems(ui.Center).Children(func() {
			a.iconBtnSized(c, iconFullscreen, m.iconSize, m.iconHit, false, "放大观测 "+room.Name, func() {
				a.watchOpenPlayer(room)
			})
			a.iconBtnSized(c, iconX, m.iconSize, m.iconHit, false, "移出观测 "+room.Name, func() {
				a.watchRemove(room)
			})
		})
	})
}

// watchBarSize 是格子里控制条的一档尺寸。
//
// 播放页那套（44 高 / 30 热区 / 24 图标 / 96 滑条）只在格子够大时沿用；
// 12 格时一格只有三百来像素宽，同样的条子会占掉近半格，压得画面透不过气。
type watchBarSize struct {
	rowH, radius, padY, padX float32
	controls                 controlMetrics
}

func watchBarSizeFor(w float32) watchBarSize {
	switch {
	case w >= 520:
		return watchBarSize{
			rowH: 44, radius: 16, padY: 7, padX: 14,
			controls: controlMetrics{
				leftGap: 12, rightGap: 12, volumeW: 96, showPercent: true,
				iconSize: playerIconSize, iconHit: playerIconHit,
			},
		}
	case w >= 300:
		return watchBarSize{
			rowH: watchBarRowH, radius: watchBarRadius, padY: 4, padX: 8,
			controls: controlMetrics{
				leftGap: 6, rightGap: 6, volumeW: watchVolumeW, showPercent: true,
				iconSize: watchIconSize, iconHit: watchIconHit,
			},
		}
	default:
		// 再窄一档：百分比让位、滑条收到 40，否则左组会顶到右组，
		// 放大和移出被挤出格。
		return watchBarSize{
			rowH: watchBarRowH, radius: watchBarRadius, padY: 4, padX: 8,
			controls: controlMetrics{
				leftGap: 4, rightGap: 4, volumeW: 40, showPercent: false,
				iconSize: watchIconSize, iconHit: watchIconHit,
			},
		}
	}
}

// watchWheelStep 把一次滚轮位移折算成音量增量，取整到 1%。
//
// 和播放页那条 wheelVolume 同一个灵敏度，但没有跨帧余量：墙上每格各存一份
// 值，余量要么得按格存一份状态，要么就在一次事件里算完。触控板一次只推
// 几 DIP，取整之后会掉成 0——所以不足 1% 时按 1% 走，宁可稍快也不卡住。
func watchWheelStep(dy float32) float32 {
	step := -dy * volumeWheelPerDIP
	rounded := float32(math.Round(float64(step)*100)) / 100
	if rounded == 0 {
		if step > 0 {
			return 0.01
		}
		if step < 0 {
			return -0.01
		}
	}
	return rounded
}

// watchTileAudio 是非 active 格音量组的全部内容：一个"在墙上听"的键，加一条
// 只改这格记住的值的滑条。
//
// 以前这里是一个"取消静音"的静音图标：点它等于激活这一格，也就是换掉整个
// 声音焦点。那是墙格上唯一能把语义说清楚的控件，却藏在 hover 里、名字还
// 指向了错误的东西（这一格从来没被静音过，它只是没在出声）。
//
// 现在它就叫"在墙上听"——唯一一个只换声音、不进播放页的入口。滑条和滚轮
// 不再激活这一格：调预览格的音量，改的就只是那一格记下的数。
func (a *App) watchTileAudio(c *ui.Context, m controlMetrics, room Streamer) {
	volume := a.watchVolume(watchKey(room))
	ui.Row(c).Gap(m.leftGap).AlignItems(ui.Center).Children(func() {
		a.iconBtnSized(c, iconVolumeMute, m.iconSize, m.iconHit, false,
			"在墙上听 "+room.Name, func() {
				a.watchActivate(room)
				a.watchSyncActiveAudio()
			})
		hot := ui.Box(c).Width(m.volumeW).Height(18).Center().
			Cursor(ui.CursorPointer).Label("音量 " + room.Name)
		hot.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || ev.DY == 0 {
				return false
			}
			// 滚轮走同一套 1% 取整，但只落在这一格自己的值上。
			a.setTileVolume(room, volume-watchWheelStep(ev.DY))
			return true
		})
		if hot.Clicked() {
			x, _, _ := hot.PointerPosition()
			v := min(1, max(0, x/m.volumeW))
			a.setTileVolume(room, float32(math.Round(float64(v)*100))/100)
		}
		hot.Children(func() {
			track := ui.Box(c).Width(m.volumeW).Height(4).Radius(999).
				Background(theme.MediaTrack)
			track.Children(func() {
				if volume > 0 {
					// 暗一档的 accent：这格设了这个音量，但现在没在响。
					ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).
						Width(m.volumeW * volume).Radius(999).
						Background(theme.Accent.Mix(ui.Transparent, 0.45))
				}
			})
		})
		if m.showPercent {
			// 玻璃条是恒定暗面，不能用跟配色走的 TextSecondary：
			// 浅色主题里那是深墨，数字直接消失。
			ui.Text(c, fmt.Sprintf("%d%%", int(volume*100+0.5))).
				FontSize(theme.TextXS).TextColor(theme.MediaTextDim).
				FontFeatures("tnum").Width(36).TextAlign(ui.End)
		}
	})
}

// platformBrand 是平台胶囊的底色；认不出来的平台退到中性填充。
func platformBrand(id string) ui.Color {
	switch id {
	case "bilibili":
		return theme.BrandBilibili
	case "douyu":
		return theme.BrandDouyu
	case "huya":
		return theme.BrandHuya
	case "douyin":
		return theme.BrandDouyin
	}
	return theme.FillTertiary
}

// watchState 画格子的状态提示。
//
// 加载态直接复用播放页的整块遮罩、30px spinner 和同一句文案，不再自己画
// 小胶囊；未开播/失败才是顶部的小提示。
func (a *App) watchState(c *ui.Context, room Streamer, sess *wallSession, player *ui.VideoPlayer) {
	key := watchKey(room)
	msg, loading := "", false
	switch {
	case key == a.WatchActive && a.media.loading:
		loading = true
	case key == a.WatchActive && a.media.err != "" && a.media.player == nil:
		msg = a.media.err
	case key == a.WatchActive && player != nil && !player.Ready():
		loading = true
	case sess != nil && sess.offline:
		msg = "主播暂时没有开播"
	case sess != nil && sess.err != "":
		// 线路都试过仍不稳定时会保留最后一帧；这条提示必须压在画面上，
		// 否则用户只看到一张冻住的图，不知道发生了什么。
		msg = sess.err
	case sess != nil && player == nil && sess.loading:
		loading = true
	case player != nil && !player.Ready():
		loading = true
	case sess != nil && sess.stalled:
		// 有画面、但时间轴停住了：这一格在缓冲。不报出来的话，用户看到的
		// 是一张冻住的图，会以为那是实时画面——比缓冲本身更糟。
		msg = "缓冲中…"
	}
	if loading {
		a.overlayLayer(c, func() {
			spinner(c, 30)
			a.overlayText(c, "正在拉取直播流…")
		})
		return
	}
	if msg == "" {
		return
	}
	// 这里以前补一句「· 点击重试」。单击改成"进这一间"之后那句话不成立了：
	// 点下去会打开播放页，播放页里有「立即刷新」。真正的一步重试在 hover
	// 底栏的「重新加载」上，所以这里只报状态，不承诺动作。
	ui.Box(c).Absolute().Top(8).Left(0).Right(0).Height(26).Center().Children(func() {
		ui.Box(c).Height(22).Padding(0, 9, 0, 9).Radius(theme.RadiusFull).
			Background(theme.MediaScrimSoft).Center().Children(func() {
			ui.Text(c, msg).FontSize(theme.Text2XS).TextColor(theme.MediaTextBody)
		})
	})
}

// watchEmpty 是空盒：不放示例房间，只给两条去路。
func (a *App) watchEmpty(c *ui.Context) {
	ui.Box(c).Grow(1).Center().Children(func() {
		ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "观测盒是空的").FontSize(theme.TextLG).FontWeight(600).
				TextColor(theme.TextPrimary)
			ui.Text(c, "从关注列表或首页把直播间收进来，这里会同时显示它们的实时画面。").
				FontSize(theme.TextXS).TextColor(theme.TextTertiary)
			ui.Row(c).Gap(8).Children(func() {
				a.watchEmptyBtn(c, "去关注列表", func() {
					a.selectFollowsSection(followsSectionStreamers)
				})
				a.watchEmptyBtn(c, "去首页", func() {
					a.Router.Push(routeHome)
				})
			})
		})
	})
}

func (a *App) watchEmptyBtn(c *ui.Context, label string, on func()) {
	btn := ui.Box(c).Height(32).Padding(0, 14, 0, 14).Radius(theme.RadiusMD).
		Background(theme.FillTertiary).Center().Cursor(ui.CursorPointer).Label(label)
	if btn.Hovered() {
		btn.Background(theme.FillQuaternary)
	}
	if btn.Clicked() {
		on()
	}
	down := btn.Pressed()
	btn.Children(func() {
		ui.Text(c, label).FontSize(theme.TextSM).FontWeight(500).
			TextColor(theme.TextPrimary)
		pressDim(c, down, theme.RadiusMD)
	})
}
