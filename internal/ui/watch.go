package ui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
	"alive/internal/watch"
)

// watchWall 是「悲鸣观测」的内容层：把观测盒里的房间按 1–9 间铺满这一屏。
//
// 它不滚动，也不显示弹幕。每一格都是一个无弹幕的单直播间缩小版：全档取流、
// 静音、按格子物理像素呈现；单击切音频焦点，双击或点岛进播放页。
func (a *App) watchWall(c *ui.Context) {
	rooms := a.WatchRooms
	if len(rooms) == 0 {
		a.watchEmpty(c)
		return
	}
	box := ui.Box(c).Key("watch-wall").Grow(1).Clip().Background(ui.RGB(0, 0, 0))
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
	rects := watch.Layout(items, b.W, b.H, 12)
	rm := make(map[string]watch.Rect, len(rects))
	for i, r := range rects {
		rm[watchKey(rooms[i])] = r
	}
	a.watchShow(rm)

	box.Children(func() {
		for i, room := range rooms {
			a.watchTile(c, room, rects[i])
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

func (a *App) watchTile(c *ui.Context, room Streamer, r watch.Rect) {
	key := watchKey(room)
	active := key == a.WatchActive
	tile := ui.Box(c).Absolute().Left(r.X).Top(r.Y).Width(r.W).Height(r.H).
		Radius(theme.RadiusMD).Clip().Background(ui.RGB(0, 0, 0)).
		Cursor(ui.CursorPointer).Focusable().
		Label(watchTileLabel(room))
	if active {
		tile.InsetShadow(0, 0, 0, 1, theme.AccentInk)
	}
	if tile.Clicked() {
		a.watchActivate(room)
	}
	if tile.DoubleClicked() {
		a.watchActivate(room)
		a.Router.Push(routePlayer)
	}
	tile.Drag(key)
	if _, over := ui.DragOver[string](tile); over {
		tile.InsetShadow(0, 0, 0, 2, theme.Accent.Mix(ui.Transparent, 0.35))
	}
	if dragged, dropped := ui.Drop[string](tile); dropped && dragged != key {
		a.watchMove(dragged, key)
	}
	if tile.Dragging() {
		tile.Opacity(0.35)
	}
	tile.ContextMenu(func(m *ui.Menu) {
		if m.Item("前移").Chosen() {
			a.watchMoveBy(room, -1)
		}
		if m.Item("后移").Chosen() {
			a.watchMoveBy(room, 1)
		}
		if m.Item("移出悲鸣观测").Chosen() {
			a.watchRemove(room)
		}
	})

	player := a.watchPlayer(key)
	sess := a.watch.sessions[key]
	tile.Children(func() {
		if player != nil {
			ui.Video(c, player).Fill().Fit(ui.Contain)
		}
		if player == nil || !player.Ready() {
			a.watchCover(c, room, r, player != nil)
		}
		a.watchTop(c, room, active)
		a.watchBottom(c, room)
		a.watchState(c, sess, player)
		if active || tile.Hovered() || tile.FocusVisible() {
			a.watchControls(c, room, active)
		}
	})
}

// watchCover 是没有画面时的底：真封面（可裁）或渐变占位。
func (a *App) watchCover(c *ui.Context, room Streamer, r watch.Rect, connecting bool) {
	if bmp := media.Default.Bitmap(room.CoverURL, r.W); bmp != nil {
		ui.Image(c, bmp).Fill().Fit(ui.Cover)
	} else {
		ui.Box(c).Fill().Background(ui.Oklch(0.42, 0.10, room.Hue+180))
	}
	if connecting {
		// 有播放器但还没首帧：只压一层很淡的暗，不挡住“正在接通”的提示。
		ui.Box(c).Fill().Background(theme.MediaScrimSoft)
	}
}

func (a *App) watchTop(c *ui.Context, room Streamer, active bool) {
	label := platformLabel(room.Platform)
	if q := a.watchQuality(watchKey(room)); q != "" {
		label += " · " + q
	}
	ui.Row(c).Absolute().Top(8).Left(8).Right(8).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Height(20).Padding(0, 7, 0, 7).Radius(theme.RadiusFull).
			Background(theme.MediaScrimSoft).Center().Children(func() {
			ui.Text(c, label).FontSize(theme.Text2XS).
				TextColor(theme.MediaTextBody).SingleLine()
		})
		ui.Box(c).Grow(1)
		if room.Live {
			ui.Box(c).Size(6, 6).Radius(3).Background(theme.StatusOnline)
		}
		ui.Text(c, viewerText(room.Viewers)).FontSize(theme.Text2XS).
			TextColor(theme.MediaTextBody)
		if active {
			// active 的静音状态常显，用户不用 hover 才能操作。
			ico, label := iconVolumeHigh, "静音观测"
			if a.Volume <= 0 {
				ico, label = iconVolumeMute, "取消静音观测"
			}
			btn := ui.Box(c).Size(24, 24).Radius(12).Center().
				Background(theme.MediaScrimSoft).Cursor(ui.CursorPointer).Label(label)
			if btn.Clicked() {
				a.toggleMute()
			}
			btn.Children(func() {
				ui.Icon(c, ico).FontSize(13).TextColor(theme.MediaText)
			})
		}
	})
}

func (a *App) watchBottom(c *ui.Context, room Streamer) {
	ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(56).
		Background(theme.MediaScrimTile).Children(func() {
		ui.Column(c).Fill().Padding(8, 10, 8, 10).Gap(2).Children(func() {
			ui.Text(c, room.Name).FontSize(theme.TextSM).FontWeight(600).
				TextColor(theme.MediaText).SingleLine()
			title := room.Title
			if title == "" {
				title = "暂无直播标题"
			}
			ui.Text(c, title).FontSize(theme.Text2XS).
				TextColor(theme.MediaTextDim).SingleLine()
		})
	})
}

// watchState 画格子的状态提示：接通中 / 未开播 / 预览失败。
func (a *App) watchState(c *ui.Context, sess *wallSession, player *ui.VideoPlayer) {
	msg := ""
	if sess != nil {
		switch {
		case sess.offline:
			msg = "主播暂时没有开播"
		case sess.err != "" && player == nil:
			msg = sess.err
		case player == nil && sess.loading:
			msg = "接通中…"
		}
	}
	if msg == "" && player != nil && !player.Ready() {
		msg = "接通中…"
	}
	if msg == "" {
		return
	}
	if sess != nil && player == nil && (sess.offline || sess.err != "") {
		msg += " · 点击重试"
	}
	ui.Box(c).Absolute().Left(0).Right(0).Bottom(56).Height(26).Center().Children(func() {
		ui.Box(c).Height(22).Padding(0, 9, 0, 9).Radius(theme.RadiusFull).
			Background(theme.MediaScrimSoft).Center().Children(func() {
			ui.Text(c, msg).FontSize(theme.Text2XS).TextColor(theme.MediaTextBody)
		})
	})
}

func (a *App) watchControls(c *ui.Context, room Streamer, active bool) {
	ui.Row(c).Absolute().Right(8).Bottom(8).Gap(6).Children(func() {
		if active {
			ico, label := iconPause, "暂停观测"
			if !a.Playing {
				ico, label = iconPlay, "继续观测"
			}
			a.watchIconBtn(c, ico, label, func() {
				a.Playing = !a.Playing
				a.watchSyncActiveAudio()
			})
		}
		a.watchIconBtn(c, iconFullscreen, "放大观测 "+room.Name, func() {
			a.watchActivate(room)
			a.Router.Push(routePlayer)
		})
		a.watchIconBtn(c, iconX, "移出观测 "+room.Name, func() {
			a.watchRemove(room)
		})
	})
}

func (a *App) watchIconBtn(c *ui.Context, ico *ui.SVG, label string, on func()) {
	btn := ui.Box(c).Size(26, 26).Radius(13).Center().
		Background(theme.MediaScrimSoft).Cursor(ui.CursorPointer).Label(label)
	if btn.Hovered() {
		btn.Background(theme.MediaRowLine)
	}
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(14).TextColor(theme.MediaText)
	})
}

// watchActions 是墙头的「＋」：只增不减，展开一个从关注里挑的添加面板。
func (a *App) watchActions(c *ui.Context) {
	btn := ui.Box(c).Size(28, 28).Radius(theme.RadiusFull).Center().
		Cursor(ui.CursorPointer).Label("收进悲鸣观测")
	if a.WatchPickerOpen || btn.Hovered() {
		btn.Background(theme.FillTertiary)
	}
	if btn.Clicked() {
		a.WatchPickerOpen = !a.WatchPickerOpen
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if a.WatchPickerOpen {
			col = theme.AccentText
		}
		ui.Icon(c, iconPlus).FontSize(16).TextColor(col)
	})
}

// watchPicker 是从关注里挑房间的一行。只在用户点开「＋」时出现，不是常驻墙头。
func (a *App) watchPicker(c *ui.Context) {
	if len(a.WatchRooms) >= watch.MaxRooms {
		ui.Text(c, "观测盒已满 9 间，先移出一间").
			FontSize(theme.TextXS).TextColor(theme.TextTertiary).Padding(0, 4, 0, 4)
		return
	}
	inBox := make(map[string]bool, len(a.WatchRooms))
	for _, s := range a.WatchRooms {
		inBox[watchKey(s)] = true
	}
	var choices []Streamer
	for _, s := range a.followStreamers() {
		if s.Live && !inBox[watchKey(s)] {
			choices = append(choices, s)
		}
	}
	if len(choices) == 0 {
		ui.Text(c, "没有可添加的在播关注；先去关注几个主播").
			FontSize(theme.TextXS).TextColor(theme.TextTertiary).Padding(0, 4, 0, 4)
		return
	}
	ui.ScrollHorizontal(c).Scrollbars(false).Padding(2).Children(func() {
		ui.Row(c).Gap(6).Children(func() {
			for _, s := range choices {
				a.watchPickerChip(c, s)
			}
		})
	})
}

func (a *App) watchPickerChip(c *ui.Context, s Streamer) {
	chip := ui.Box(c).Height(30).Padding(0, 12, 0, 12).Gap(5).
		Radius(theme.RadiusFull).Background(theme.FillTertiary).Center().
		Cursor(ui.CursorPointer).Label("添加观测:" + platformLabel(s.Platform) + " " + s.Name)
	if chip.Hovered() {
		chip.Background(theme.FillQuaternary)
	}
	if chip.Clicked() {
		a.watchAdd(s)
		a.WatchPickerOpen = false
	}
	chip.Children(func() {
		ui.Text(c, platformLabel(s.Platform)).FontSize(theme.Text2XS).
			TextColor(theme.TextTertiary)
		ui.Text(c, s.Name).FontSize(theme.TextXS).
			FontWeight(600).TextColor(theme.TextPrimary).SingleLine()
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
	btn.Children(func() {
		ui.Text(c, label).FontSize(theme.TextSM).FontWeight(500).
			TextColor(theme.TextPrimary)
	})
}
