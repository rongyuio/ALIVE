package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	"alive/internal/theme"
)

// 播放页里常用的半透明底色。控制条和顶栏胶囊都是
// 「深色半透明 + 1px 白色 12% 描边」，叠在画面上才压得住。
var (
	playerBarBg     = ui.RGBA(9, 10, 12, 0.62)
	playerBarBorder = theme.MediaHair
	playerIcon      = theme.MediaTextBody
	playerIconOn    = ui.Hex("#ffffff")
)

// PlayerPage 是播放页。
//
// 版式分这几层：
//
//	playerShell（圆角 16 的整块）
//	└─ stage（黑底画面，铺满）
//	   ├─ 画面
//	   └─ chrome（顶栏 + 底部控制台，浮在画面上）
//
// 直播流最后再接，现在画面先用当前直播间的封面顶着——版式、圆角、
// 压暗、chrome 对比度都能照常验证。
func (a *App) PlayerPage(c *ui.Context) {
	a.ensureDanmuLoop()
	// 接真实流：房间变了才重新取流 + 连弹幕
	a.ensureMedia(c)
	// 从后台收听回来：把视频轨重新打开。
	//
	// 必须排在 ensureMedia 之后：换台时 ensureMedia 已经 reset 并重开一路，
	// background 在那条路上被清掉了，这里就是空操作；只有「还是同一个房间」
	// 时才轮到它把旧引擎的视频轨打开。
	a.attachMedia()
	a.syncPlayer()
	// 首帧可用时刻是起播耗时的最后一段。
	a.trackFirstFrame()
	// MyGo 0.9.0 的原生播放器在 Loading 阶段不定时请求帧，不能依赖灵动岛
	// 或弹幕恰好在动画；首帧预算和健康检查都靠这一帧续上。
	//
	// 首帧后按 250ms 看一眼：播放器停帧不会主动报错，只靠用户操作触发帧会
	// 漏掉故障。
	if a.Playing && a.media.player != nil && a.media.player.Err() == nil {
		if !a.media.firstFrame {
			c.After(firstFramePollInterval)
		} else {
			c.After(playbackHealthPollInterval)
		}
	}
	// 左右 14（窄屏 10）、底部不留：整块一直铺到窗口下沿。
	// 两种全屏下四边都不留（shellFull 是 inset:0）。
	//
	// 判据必须和 Shell 一样用 playerFullscreen()：之前只认 WinFull，系统全屏时
	// 顶栏让位了，画面四周却还留着 14 的留白和 16 的圆角。
	full := a.playerFullscreen()
	pad := a.bodyPadX(c)
	if full {
		pad = 0
	}
	ui.Box(c).Grow(1).Padding(0, pad, 0, pad).Children(func() {
		// 外壳不描边：和设备页、卡片一样，边界交给地→画面的色阶和圆角。
		// 以前那 1px 是为了遮住「描边压在圆角裁剪线上」的接缝，不画描边就没有
		// 这条接缝——Clip 直接把舞台裁成圆角。
		shell := ui.Column(c).Fill().Background(theme.BgSecondary)
		if !full {
			shell.Radius(theme.RadiusXL).Clip()
		}

		shell.Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				stage := ui.Box(c).Grow(1).Background(ui.RGB(0, 0, 0))
				// 悬浮 chrome 的自动隐藏：先按指针位置和空闲时长算出这一帧
				// 的显隐，拖窗条和 chrome 都跟着它走。
				a.syncChromeIdle(c, stage)
				chromeOff := a.ChromeHidden
				if chromeOff {
					// 空闲态：控件让位之后连光标一起藏掉
					stage.Cursor(ui.CursorNone)
				}
				stage.Children(func() {
					a.stageFrame(c)
					a.danmuLayer(c)
					// 状态层压在画面和弹幕之上、chrome 之下：它是 z-index 15，
					// 控制条和顶栏是 20。
					a.stageOverlay(c)
					a.bufferingPill(c)
					a.noticePill(c)
					// 窗口全屏时顶栏没了，画面上得有一块能拖动窗口的区域
					// （拖窗区：navbar 高度 64，压在视频之上、
					// chrome 之下）。只铺顶部一条，整块铺满会把画面下方的
					// 双击也吃掉。
					//
					// 位置必须排在这里：MyGo 的命中链只取最上层的那个元素和
					// 它的祖先，不会穿过兄弟往下找。原来它画在 stageFrame 之前，
					// 于是视频、状态层、顶栏、顶栏里那根弹性空 Box 每一层都能
					// 把它挡掉，实际只剩 14px 高的一条能拖。
					//
					// chrome 淡出时这层必须撤掉：它在原生侧就是标题栏，鼠标一
					// 进去页面就收不到 move，控制条再也唤不回来（那段注释
					// 就是为这个写的）。
					if a.WinFull && !chromeOff {
						ui.Box(c).Absolute().Top(0).Left(0).Right(0).
							Height(theme.NavbarHeight).DragWindow()
					}
					a.playerTopbar(c)
					a.playerControls(c)
					a.playerCaption(c)
				})
				// 双击画面：已经在全屏里就退出去，没全屏就进窗口全屏。
				// 原来只翻 WinFull，系统全屏下双击等于没反应。
				//
				// 按钮、下拉、头像这些命中目标自带点击身份，在命中链里排在
				// stage 前面，双击落在它们身上时这里收不到——控制条上连点
				// 不会误切全屏。
				if stage.DoubleClicked() {
					switch {
					case a.WinFull:
						a.WinFull = false
					case a.Win != nil && a.Win.IsFullScreen():
						a.Win.SetFullScreen(false)
					default:
						a.WinFull = true
					}
				}
				// 右侧弹幕列表。
				//
				// 只有系统全屏不给：那是真全屏，画面独占整块屏幕。窗口全屏
				// （应用内沉浸、任务栏还在）照常保留——那边弹幕列表本身就是
				// 功能，没有理由跟着画面一起消失。收起 / 展开仍然走面板左沿
				// 那颗小舌。
				if !a.sysFullscreen() {
					a.danmuPanel(c)
				}
			})
		})
	})
}

// 悬浮 chrome 的自动隐藏参数：2800ms 后淡出，280ms 淡完。
const (
	chromeIdleAfter = 2800 * time.Millisecond
	chromeFadeDur   = 280 * time.Millisecond
)

// 指针落在这两条带里就算「手在 chrome 上」：顶栏（top 14 + 44 高）和
// 控制条（bottom 12 + 44 高），各自留一点余量，贴着边走也别让它跑掉。
const (
	chromeTopBand    = 70
	chromeBottomBand = 76
)

// syncChromeIdle 决定这一帧悬浮 chrome（顶栏 / 控制条 / 窗口按钮）要不要淡出。
//
// 时序：指针静止 2800ms 之后淡出，一动立刻回来；暂停、信息岛
// 展开、任一下拉或面板开着时一律常显——那几种状态人正在操作，按钮不能自己
// 跑掉。进出全屏也先把 chrome 亮出来，别让人切回来的那一刻对着空画面找按钮。
//
// 指针活动靠 stage 的 PointerPosition 读：它会顺手把 stage 标成
// flagTrackPointer，于是指针在画面上移动时 MyGo 一定来一帧，哪怕停的是
// 没有控件的空白处（hover 链没变也一样）。
func (a *App) syncChromeIdle(c *ui.Context, stage ui.Element) {
	now := c.Now()
	stay := func() {
		a.ChromeHidden = false
		a.chromeIdleAt = now
	}

	// 岛自动展开的那几秒不算「人在操作」；指针停在岛上时本来就落在
	// 顶部那条带里，下面的坐标判断已经把它兜住了。
	if !a.Playing || a.QualityOpen || a.LineOpen ||
		a.DanmuSettingsOpen || a.DanmuFilterOpen || a.ShortcutsOpen {
		stay()
		return
	}
	if full := a.playerFullscreen(); full != a.chromeFull {
		a.chromeFull = full
		stay()
		return
	}

	_, winH := c.Size()
	stageH := winH
	if !a.playerFullscreen() {
		stageH -= theme.NavbarHeight
	}
	x, y, over := stage.PointerPosition()
	if over != a.chromePtrOver || x != a.chromePtrX || y != a.chromePtrY {
		a.chromePtrOver, a.chromePtrX, a.chromePtrY = over, x, y
		stay()
		return
	}
	if over && (y <= chromeTopBand || y >= stageH-chromeBottomBand) {
		stay()
		return
	}

	if a.chromeIdleAt.IsZero() {
		a.chromeIdleAt = now
	}
	idle := now.Sub(a.chromeIdleAt)
	if idle >= chromeIdleAfter {
		a.ChromeHidden = true
		return
	}
	// 画面完全静止（没视频帧、没弹幕）时不会有别的帧，靠这一帧收尾。
	// 基准固定在 chromeIdleAt 上，每帧重排也不会把到点时刻往后推。
	c.After(chromeIdleAfter - idle)
}

// chromeFade 让一块 chrome 跟着自动隐藏淡出，返回当前透明度。
//
// 隐藏靠 opacity + visibility 过渡；这边 opacity 走 AnimateTo，隐藏态
// 再补一个 PassThrough——不穿透的话，淡到 0 的顶栏和控制条仍然占着命中
// 矩形，画面上的双击和点击播放会被它们吃掉。
func (a *App) chromeFade(e ui.Element) float32 {
	target := float32(1)
	if a.ChromeHidden {
		target = 0
	}
	op := e.AnimateTo("chrome-fade", target, theme.EaseOut(chromeFadeDur))
	e.Opacity(op)
	if a.ChromeHidden {
		e.PassThrough()
	}
	return op
}

// playerFullscreen 报告播放页是否处于任意一种全屏：应用内的窗口全屏
// （WinFull）或系统原生全屏。两者都会让顶栏让位给画面。
func (a *App) playerFullscreen() bool {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return false
	}
	return a.WinFull || a.sysFullscreen()
}

// sysFullscreen 报告窗口是否处于系统原生全屏。
//
// 单拎出来是因为有两处只关心系统全屏、不关心窗口全屏：系统全屏下不画
// 弹幕侧栏，飞幕的宽度也就不能扣掉面板。
func (a *App) sysFullscreen() bool {
	return a.Win != nil && a.Win.IsFullScreen()
}

// leavePlayer 收掉播放页留下的窗口级与界面级状态，离开播放页时调用。
//
// 两种全屏必须在这里退出。系统全屏留着不走，窗口回到首页时还是整屏、
// 没有 WS_THICKFRAME 边框——拖边缘改不了大小，而 Esc 在首页只管返回、
// 控制条又已经不在了，人没有任何办法把窗口变回普通窗口。WinFull 留着
// 不走，下次进直播间会直接是全屏态，看着像「退出没生效」。
//
// 其余是播放页的跨帧状态：控制条自动隐藏、几个浮层的开关、拖面板和拖
// 音量的标记。不清的话下次进直播间会以上一次的隐藏 / 展开态开局。
//
// 幂等，每帧调用都没问题；判据由调用方给（Router 不是播放页）。
func (a *App) leavePlayer() {
	a.WinFull = false
	if a.Win != nil && a.Win.IsFullScreen() {
		a.Win.SetFullScreen(false)
	}
	a.ChromeHidden = false
	a.chromeIdleAt = time.Time{}
	a.InfoOpen = false
	a.QualityOpen = false
	a.LineOpen = false
	a.DanmuSettingsOpen = false
	a.DanmuFilterOpen = false
	a.ShortcutsOpen = false
	a.volumeDragging = false
	a.volumeWheelAccum = 0
	a.panelDragW, a.panelDragX = 0, 0
}

// stageFrame 是画面本身。
//
// 有播放器就画真流，没有（还在转封装、或者房间没开播）就先用封面顶着，
// 这样版式不会跳。
func (a *App) stageFrame(c *ui.Context) {
	// 四边贴死：只写 Fill 的话元素会按自己的比例排，容器比 16:9 高时
	// 底下就空出一条黑边（控制条会浮在那条黑边上）。
	// contain 是有意选的：宁可留黑边也不裁、不拉伸，
	// 弹幕层再盖到黑边上去。
	p := a.media.player
	if p != nil {
		// Video 元素必须一直在：视频只有在被显示时才会往下走，藏起来
		// 或干脆不画，解码就停，首帧永远等不到。
		ui.Video(c, p).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
		if p.Ready() {
			return
		}
		// 播放器已经开出来了，但首帧还没到——此时 Video 是一块纯黑。
		// 拿封面盖住，看起来是「正在起播」而不是「播坏了」。
	}
	w, _ := c.Size()
	if bmp := media.Default.Bitmap(a.currentCover(), w); bmp != nil {
		ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
	}
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(theme.MediaScrimSoft)
}

// stageOverlay 是画面上的状态层。状态一共五种：拉流中、断流重连、
// 缓冲中、未开播、取流失败。这里先做三种——断流自动重连还没做（需要
// ffmpeg 退出的信号），其余三种的状态都已经在 media 上。
func (a *App) stageOverlay(c *ui.Context) {
	m := &a.media
	if m.roomKey == "" {
		return
	}

	switch {
	// 转封装就绪不等于画面就绪。播放器开出来之后，Media Foundation 还要
	// 下首片、建解码器，实测 2~4 秒才出第一帧。原来 loading 一撤覆盖层就
	// 没了，而 Video 还是黑的——观感就是「loading → 黑屏几秒 → 突然有画面」。
	// 所以这里把加载态延到播放器真的 Ready 为止。
	case m.loading || (m.player != nil && !m.player.Ready() && m.player.Err() == nil):
		a.overlayLayer(c, func() {
			spinner(c, 30)
			a.overlayText(c, "正在拉取直播流…")
		})

	case m.err != "":
		a.overlayLayer(c, func() {
			a.overlayText(c, m.err)
			// 多线路平台失败过才提示，单线路说了也没用
			if len(m.stream.Lines) > 1 {
				ui.Text(c, "可以在控制条切换线路后重试").
					FontSize(theme.TextXS).TextColor(ui.RGBA(255, 255, 255, 0.6)).
					TextAlign(ui.Center).LineHeight(1.7)
			}
			a.overlayButton(c, "重试", a.retryStream)
		})

	// 播放器开出来了但解码失败（首片坏、编码不支持）。这种时候它永远
	// Ready 不了，不单独列一个分支就会一直卡在加载层上。
	case m.player != nil && m.player.Err() != nil:
		a.overlayLayer(c, func() {
			a.overlayText(c, "播放失败，请重试或切换线路")
			a.overlayButton(c, "重试", a.retryStream)
		})

	case m.stream.RoomID != "" && !m.stream.Live:
		a.overlayLayer(c, func() {
			// 58 圆底 + 26 图标
			icon := ui.Box(c).Size(58, 58).Radius(29).Margin(0, 0, 4, 0).
				Background(theme.MediaRowLine).
				Border(1, theme.MediaHair).Center()
			icon.Children(func() {
				ui.Icon(c, iconMonitorOff).FontSize(26).
					TextColor(theme.MediaTextDim)
			})
			ui.Text(c, "主播暂时没有开播").FontSize(theme.TextLG).FontWeight(600).
				TextColor(theme.White94)
			a.overlayText(c, "开播后会自动加载，也可以手动刷新试试")
			a.overlayButton(c, "立即刷新", a.retryStream)
		})
	}
}

// overlayLayer 是状态层的底板：铺满 stage 的深色半透明 + 居中的一列。
//
// 底板是 pointer-events: none（只有按钮重新开启命中），
// 为的是样式全屏时窗口拖拽还能穿透。这边控制在它之后画，所以直接把
// 底板做实——状态层出现时本来也没有别的东西要点。
func (a *App) overlayLayer(c *ui.Context, fn func()) {
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(ui.RGBA(6, 7, 9, 0.82)).Children(func() {
		ui.Column(c).Fill().Center().Gap(14).Children(fn)
	})
}

// overlayText 是状态层的说明文字：13px / 白 86% / 居中 / 行高 1.7，
// 最大宽度取窗口的 70%。
func (a *App) overlayText(c *ui.Context, text string) {
	w, _ := c.Size()
	ui.Text(c, text).FontSize(theme.TextSM).
		TextColor(theme.MediaTextBody).
		LineHeight(1.7).TextAlign(ui.Center).MaxWidth(w * 0.7)
}

// overlayButton 是状态层上的主按钮：
// 40 高、左右 24、纯白胶囊、深色字。
func (a *App) overlayButton(c *ui.Context, label string, on func()) {
	btn := ui.Row(c).Height(40).Padding(0, 24, 0, 24).Radius(theme.RadiusFull).
		Background(ui.Hex("#ffffff")).Center().Cursor(ui.CursorPointer).Label(label)
	if btn.Hovered() {
		btn.Background(ui.Hex("#f1f1f1"))
	}
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		ui.Icon(c, iconSync).FontSize(13).TextColor(ui.Hex("#0b0c08"))
		ui.Text(c, label).FontSize(theme.TextSM).FontWeight(600).
			TextColor(ui.Hex("#0b0c08"))
	})
}

// bufferingPill 是左下角的「缓冲中…」小胶囊：
// left 16 / bottom 76 / 30 高 / 胶囊底，压在控制条下面。
//
// 判定看的是「画面有没有在动」，不是 VideoPlayer.Buffering() ——
// 后者在直播 HLS 上会永久卡在 true（原因见 playerMedia.stalled）。
func (a *App) bufferingPill(c *ui.Context) {
	p := a.media.player
	if p == nil || a.media.loading || !a.media.stalled(p) {
		return
	}
	ui.Row(c).Absolute().Left(16).Bottom(76).Height(30).
		Padding(0, 12, 0, 12).Gap(7).Radius(theme.RadiusFull).
		AlignItems(ui.Center).
		Background(playerBarBg).Border(1, playerBarBorder).Children(func() {
		spinner(c, 14)
		ui.Text(c, "缓冲中…").FontSize(theme.TextXS).TextColor(playerIcon)
	})
}

// noticePill 是降档一类临时提示：底部居中的小胶囊，几秒后自己消失。
// 不放在错误层里——画面还在正常播，只是平台的档位和点选的不一样。
func (a *App) noticePill(c *ui.Context) {
	now := time.Now()
	if !a.media.noticeActive(now) {
		return
	}
	// 让下一帧在提示到期时到来，清理逻辑在 noticeActive 里。
	c.After(a.media.noticeUntil.Sub(now))
	ui.Box(c).Absolute().Left(0).Right(0).Bottom(76).Center().PassThrough().Children(func() {
		ui.Row(c).Height(30).Padding(0, 14, 0, 14).Radius(theme.RadiusFull).
			AlignItems(ui.Center).
			Background(playerBarBg).Border(1, playerBarBorder).Children(func() {
			ui.Text(c, a.media.notice).FontSize(theme.TextXS).TextColor(playerIcon)
		})
	})
}

// currentCover 返回当前播放项对应卡片的封面。
func (a *App) currentCover() string { return a.current().CoverURL }

// current 返回当前播放的主播。
func (a *App) current() Streamer {
	if a.Player.Name != "" {
		return a.Player
	}
	if a.PlayerIndex >= 0 && a.PlayerIndex < len(a.Streamers) {
		return a.Streamers[a.PlayerIndex]
	}
	return Streamer{Name: "未选择直播间", Title: "", Category: ""}
}

// playerTopbar 是画面顶部那条：只有一颗返回钮。
//
// 直播间信息在顶栏那枚灵动岛里（见 island.go），这里不再重复一份——
// 同一个直播间在画面上出现两条一模一样的头像加标题，属于历史遗留。
func (a *App) playerTopbar(c *ui.Context) {
	// 这条 Row 只是布局容器，自己不该吃指针：窗口全屏时它整条压在画面
	// 顶部的拖窗条上，不穿透的话这条 44 高的带子会被它连同下面那根弹性
	// 空 Box 一起挡死。按钮和信息岛是子元素，照常命中。
	row := ui.Row(c).Absolute().Top(14).Left(14).Right(14).Gap(10).PassThrough()
	a.chromeFade(row)
	row.Children(func() {
		// ------------------------------ 返回：44 圆钮
		back := ui.Box(c).Size(44, 44).Radius(999).
			Background(ui.RGBA(10, 10, 12, 0.62)).
			Border(1, theme.MediaTrack).
			Center().Cursor(ui.CursorPointer).Label("退出直播间")
		if back.Hovered() {
			back.Background(ui.RGBA(10, 10, 12, 0.72)).
				BorderColor(theme.MediaHairStrong)
		}
		if back.Clicked() {
			// 叉是明确退出：关引擎、断弹幕，不留后台声音。别的离开方式
			// （顶栏 tab、灵动岛、搜索跳转）只进后台收听。
			a.closeMedia()
			a.Router.Back()
		}
		back.Children(func() {
			// 关闭用 X，不是返回箭头
			ui.Icon(c, iconPlayerBack).FontSize(19).TextColor(theme.MediaText)
		})

		// 弹性空白也穿透：它占了顶栏右侧整段，不穿透就等于把拖窗条
		// 右边那一大半又盖回去。
		ui.Box(c).Grow(1).PassThrough()
	})
}

// playerControls 是底部控制台。
//
// 控制条是一条半透明毛玻璃长条，左边播放/刷新/音量，
// 右边依次是清晰度 + 线路 + 弹幕组 + 小窗 + 两种全屏（清晰度和线路排在弹幕组
// 左侧，是这里有意选的顺序）。按钮本身没有底色和圆角，
// 只在 hover 时变白、激活时变强调色——之前给每个按钮加方块底是错的。
func (a *App) playerControls(c *ui.Context) {
	m := metricsFor(c)
	dock := ui.Box(c).Absolute().Left(14).Right(14).Bottom(12)
	// 淡出的同时整个控制条往下让 10px
	dock.Translate(0, (1-a.chromeFade(dock))*10)
	dock.Children(func() {
		bar := ui.Row(c).Height(44).Padding(7, 14, 7, 14).Radius(16).FillWidth().
			Border(1, playerBarBorder).AlignItems(ui.Center)
		// 整条控制条下面是真的画面，用真玻璃让它透上来。
		// 系统要求减少透明时退回原来的实色——这不是建议。
		if c.Preferences().ReduceTransparency {
			bar.Background(playerBarBg)
		} else {
			bar.Material(glass.Glass{Style: glass.Regular, Tint: playerBarBg})
		}
		bar.Shadow(0, 18, 44, -20, theme.MediaShadowStrong).
			InsetShadow(0, 0, 0, 1, theme.TopLight)

		bar.Children(func() {
			// ------------------------------ 左：播放 / 刷新 / 音量
			ui.Row(c).Gap(m.leftGap).AlignItems(ui.Center).Children(func() {
				// 这三颗是同一档：30×30 热区 + playerIconSize。原来播放/暂停是 22、
				// 刷新 19、音量又套了个 24 的框，三个排在一起大小不齐。
				playIcon := iconPlay
				if a.Playing {
					playIcon = iconPause
				}
				playLabel := "播放"
				if a.Playing {
					playLabel = "暂停"
				}
				// 播放键在错误态交给 togglePlayback 走显式重试，避免出现
				// 「图标翻了、画面不动」的死键。
				a.playerIconBtn(c, playIcon, playerIconSize, false, playLabel, a.togglePlayback)
				a.playerIconBtn(c, iconSync, playerIconSize, false, "重新加载", a.retryStream)

				a.volumeControl(c, m)
			})

			ui.Box(c).Grow(1)

			// ------------------------------ 右：清晰度 / 线路 / 弹幕组 / 小窗 / 两种全屏
			ui.Row(c).Gap(m.rightGap).AlignItems(ui.Center).Children(func() {
				// 清晰度 / 线路：选项全部来自平台返回值，还没取到流时整块
				// 不出现。摆一个本平台根本不存在的档位，只是先骗一下眼睛。
				// 这两个是「看什么流」的选择，排在弹幕那组前面更顺手。
				if qOpts := a.qualityOptions(); len(qOpts) > 0 {
					a.playerDropdown(c, "清晰度", qOpts,
						&a.Quality, &a.QualityOpen, &a.QualityHover, a.PickQuality)
				}
				// 线路只列平台真实给出的节点：抖音这类没有多线路概念的平台
				// 整块不出现——网页端也没有「自动线路」这一项。
				if lineOpts := a.lineOptions(); len(lineOpts) > 0 {
					a.playerDropdown(c, "线路", lineOpts,
						&a.Line, &a.LineOpen, &a.LineHover, a.pickLine)
				}

				// 弹幕组：开关 / 设置 / 屏蔽词；三颗和外层用同一个 gap，
				// 整条控制条只有一个节奏（12，窄屏 8）。
				ui.Row(c).Gap(m.rightGap).AlignItems(ui.Center).Children(func() {
					a.danmuToggleButton(c)
					a.danmuSettingsButton(c)
					a.danmuFilterButton(c)
				})

				// 小窗播放：真开一个独立的置顶小窗，不是换张图标了事
				pipLabel := "小窗播放"
				if a.miniOpen() {
					pipLabel = "退出小窗"
				}
				a.playerIconBtn(c, iconPip, playerIconSize, a.miniOpen(), pipLabel, a.togglePip)

				// 窗口全屏：铺满应用窗口、任务栏仍可见。
				// 和右边的系统全屏是两件事，但两者互斥：进来一个就退掉另一个，
				// 收尾统一走 toggleWinFull / toggleFullscreen。
				//
				// 图标是成对的：进 Maximize（四角外扩）、退 Minimize（四角收拢）。
				// 原来退出态借用了系统全屏的 Minimize2，两颗按钮全屏时长得一样。
				winFullIcon := iconWinFull
				if a.WinFull {
					winFullIcon = iconWinShrink
				}
				winFullLabel := "窗口全屏"
				if a.WinFull {
					winFullLabel = "退出窗口全屏"
				}
				a.playerIconBtn(c, winFullIcon, playerIconSize, a.WinFull, winFullLabel, a.toggleWinFull)

				isFull := a.Win != nil && a.Win.IsFullScreen()
				fullIcon := iconFullscreen
				if isFull {
					fullIcon = iconMinimize2
				}
				fullLabel := "全屏"
				if isFull {
					fullLabel = "退出全屏"
				}
				a.playerIconBtn(c, fullIcon, playerIconSize, isFull, fullLabel, a.toggleFullscreen)
			})
		})
	})
}

// 控制条图标按「墨迹高度」对齐，不是按字号。
//
// playerIconSize 是控制条图标的字号，只有这一档。
//
// 字形之间的墨迹差异已经在图标层解决了（见 icons.go 的 fitIcon）：每颗
// 都归一到同一个墨迹高度和同一个描边粗细，所以这里给同一个字号就够。
// 以前这里是 24 / 26 两档，靠给矮字形放大字号来补齐墨迹——代价是描边
// 跟着粗 10%，一排里粗细不齐，而且换图标就得重量一次。
const playerIconSize float32 = 24

// controlMetrics 是控制条在当前窗口宽度下的尺寸。
//
// 控制条的尺寸分 @media (max-width: 1100px) / (max-width: 980px) 两档：
// 窄一档间距 12 收到 8、滑条 96 收 90；再窄一档滑条 72，
// 并且百分比整个让位，免得控制条被挤到换行。
type controlMetrics struct {
	leftGap, rightGap float32
	volumeW           float32
	showPercent       bool
}

func metricsFor(c *ui.Context) controlMetrics {
	m := controlMetrics{leftGap: 12, rightGap: 12, volumeW: 96, showPercent: true}
	winW, _ := c.Size()
	switch {
	case winW <= 980:
		m.leftGap, m.rightGap = 8, 8
		m.volumeW, m.showPercent = 72, false
	case winW <= 1100:
		m.leftGap, m.rightGap = 8, 8
		m.volumeW = 90
	}
	return m
}

// volumeControl 是「静音键 + 滑条 + 百分比」这一组。
//
// 滑条没有拖动手柄：整条 18 高的热区都能按，按下即跳到该位置并开始拖动。
// 关键在于拖动不能靠 Pressed 判断——它要求指针仍在热区矩形内，一拖出去就断。
// Dragged 是 press 级的状态，指针离开元素后仍然为 true，用它判断「还按着」。
func (a *App) volumeControl(c *ui.Context, m controlMetrics) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 6).Children(func() {
		muteIcon := iconVolumeLow
		switch {
		case a.Volume <= 0:
			muteIcon = iconVolumeMute
		case a.Volume > 0.5:
			muteIcon = iconVolumeHigh
		}
		muteLabel := "静音"
		if a.Volume <= 0 {
			muteLabel = "取消静音"
		}
		a.playerIconBtn(c, muteIcon, playerIconSize, false, muteLabel, a.toggleMute)

		hot := ui.Box(c).Width(m.volumeW).Height(18).Center().
			Cursor(ui.CursorPointer).Label("音量")
		// 悬停在音量条上滚轮调音量。挂在这条热区上而不是控制条或画面：
		// MyGo 的滚轮按指针位置找接收者，只有指针真在这 18px 高的一条上
		// 才收得到，所以「悬停才生效」是结构保证的。
		hot.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || ev.DY == 0 {
				return false
			}
			a.wheelVolume(ev.DY)
			return true
		})
		// PointerPosition 的 x 是相对热区的，除以热区宽度就是音量比例
		x, _, _ := hot.PointerPosition()
		_, _, held := hot.Dragged()
		pressed := hot.Pressed()
		if pressed {
			a.volumeDragging = true
			// 拖动是按位置取绝对值，攒下的滚轮余量再留着就会在松手后
			// 突然多跳 1%，这里清掉
			a.volumeWheelAccum = 0
		}
		if a.volumeDragging {
			if !pressed && !held {
				// 松开、或系统取消了这次按压
				a.volumeDragging = false
			} else {
				// 音量取整到 1%：既不抖，百分比数字也稳
				v := min(1, max(0, x/m.volumeW))
				v = float32(math.Round(float64(v)*100)) / 100
				if v != a.Volume {
					a.Volume = v
					a.markDirty()
				}
			}
		}

		// .slider.active .rail：hover 或拖动时轨道 4 → 6、底色 18% → 26%
		active := a.volumeDragging || hot.Hovered()
		hot.Children(func() {
			track := ui.Box(c).Width(m.volumeW).Height(4).Radius(999).
				Background(theme.MediaTrack)
			if active {
				track.Height(6).Background(theme.MediaTrackFill)
			}
			track.Children(func() {
				ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).
					Width(m.volumeW * a.Volume).Radius(999).Background(theme.Accent)
			})
		})

		if m.showPercent {
			ui.Text(c, fmt.Sprintf("%d%%", int(a.Volume*100+0.5))).
				FontSize(theme.TextXS).TextColor(theme.Accent).
				FontFeatures("tnum").Width(36).TextAlign(ui.End)
		}
	})
}

// playerDropdown 是控制台里的清晰度 / 线路下拉。
//
// 下拉面板：触发条 30 高，菜单在按钮**上方**（bottom 44），
// 圆角 14、玻璃底 rgba(16,19,28,.82)、1px 白 12% 描边，
// 选项 8×12 padding、13px，选中项 accent 字 + accent 24% 底 + 勾。
// hoverClose 是 hover 触发浮层的关门计时。
//
// 清晰度和线路两个下拉是 onMouseEnter 打开、
// onMouseLeave 排一个 220ms 的定时器关闭，指针从按钮移到面板上会 cancelClose。
// MyGo 没有可取消的定时器，就记「最后一次确认离开」的时刻：按钮和面板
// 都不 hover 时开始计时，到点后的第一帧关门。
//
// suppress 处理「点一下关掉」：指针还停在按钮上时，每帧的 hover 会立刻
// 又把它打开，所以关掉之后要等指针先离开才允许 hover 再开。
type hoverClose struct {
	leftAt   time.Time
	suppress bool
}

// 关门计时是 window.setTimeout(..., 220)。
const hoverCloseDelay = 220 * time.Millisecond

// hover 收一次悬停状态：true 取消计时并打开，false 开始计时，
// 到点的第一帧把 open 置 false 并请求那一帧。
//
// 顺序很关键：先判 hovering 再判 open。反过来写就成了「只有已经打开
// 的人才取消计时」，hover 永远打不开任何东西。
func (h *hoverClose) hover(c *ui.Context, hovering bool, open *bool) {
	if hovering {
		h.leftAt = time.Time{}
		if !h.suppress {
			*open = true
		}
		return
	}
	// 指针离开按钮就解除点击后的抑制
	h.suppress = false
	if !*open {
		h.leftAt = time.Time{}
		return
	}
	now := c.Now()
	if h.leftAt.IsZero() {
		h.leftAt = now
		c.After(hoverCloseDelay)
		return
	}
	if now.Sub(h.leftAt) >= hoverCloseDelay {
		*open = false
		h.leftAt = time.Time{}
	}
}

// clicked 是浮层触发条被点下的处理：开着就关（并压住 hover 重开），
// 关着就开：点一下就是 toggle。
func (h *hoverClose) clicked(open *bool) {
	if *open {
		*open = false
		h.suppress = true
		return
	}
	*open = true
}

// playerDropdown 是控制条上的清晰度 / 线路下拉。
//
// onPick 在选中一项之后调用：值和菜单的关闭在本函数里完成，但「选中之后
// 要做什么」是调用方的事（换清晰度要重起一路流，换线路要重新取流）。
func (a *App) playerDropdown(c *ui.Context, label string, options []string, value *string, open *bool, h *hoverClose, onPick func(string)) {
	// 触发条：gap 6、左右各 6 的 padding、12px 白字，
	// 没有底色也没有圆角，hover / 展开时只是把字提亮到纯白。
	btn := ui.Row(c).Height(30).Padding(0, 6, 0, 6).Gap(6).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(label)
	if btn.Clicked() {
		h.clicked(open)
	}
	// .menuWrap 的 onMouseEnter={() => { cancelClose(); setOpen(true) }}
	h.hover(c, btn.Hovered(), open)
	// lit 必须在 hover 之后读：同一次构建里 hover 可能刚把 open 打开，
	// 提前算会让文字慢一帧才提亮。
	lit := *open || btn.Hovered()
	btn.Children(func() {
		col := playerIcon
		if lit {
			col = playerIconOn
		}
		ui.Text(c, *value).FontSize(theme.TextXS).TextColor(col)
		caret := btn.AnimateTo("dd-"+label, caretAngle(*open), theme.EaseOut(190*time.Millisecond))
		ui.Icon(c, iconChevron).FontSize(12).TextColor(col).Rotate(caret)
	})

	ui.PopoverBase(c, btn, open, func(panel ui.Element) {
		// 下拉面板是 min-width: 130px，不是固定宽：档位名带「1080P 原画」
		// 这种前缀，写死 130 会把「自动（720P 超清）」挤成两行。
		panel.MinWidth(130).Padding(10, 12, 10, 12).Radius(14).
			Border(1, theme.MediaHair).
			Shadow(0, 18, 36, 0, theme.MediaShadow).
			Margin(0, 0, 8, 0).
			AttachTo(btn, ui.AnchorTopRight, ui.AnchorBottomRight)
		panel = mediaPanel(c, panel)
		// 指针从按钮滑进面板时要取消关闭计时（面板自己也有
		// onMouseEnter={cancelClose}），否则菜单会从手底下消失。
		h.hover(c, panel.Hovered(), open)
		panel.Children(func() {
			ui.Column(c).Gap(6).Children(func() {
				for _, opt := range options {
					a.playerDropdownItem(c, opt, value, open, onPick)
				}
			})
		})
	})
}

func (a *App) playerDropdownItem(c *ui.Context, opt string, value *string, open *bool, onPick func(string)) {
	active := opt == *value
	// --player-accent-strong = color-mix(in srgb, accent 24%, transparent)
	// 注意用 Alpha 而不是 Mix(Transparent)：后者逐通道插值，会把 RGB 一起
	// 压到 24%，选中底色变成一块发黑的东西。
	strong := theme.Accent.Alpha(0.24)

	row := ui.Row(c).FillWidth().Padding(8, 12, 8, 12).Gap(12).Radius(8).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("选项:" + opt)
	if active {
		row.Background(strong)
	} else if row.Hovered() {
		row.Background(strong)
	}
	if row.Clicked() {
		*value = opt
		*open = false
		a.markDirty()
		if onPick != nil {
			onPick(opt)
		}
	}
	row.Children(func() {
		col := theme.MediaTextBody
		if active {
			col = theme.Accent
		}
		ui.Text(c, opt).FontSize(theme.TextSM).TextColor(col).NoWrap()
		// 弹性空间交给空 Box，不能让文本自己 Grow：MyGo 的 Grow 从 0
		// 尺寸起步、允许缩到内容宽度以下，最宽的那项（「自动（720P 超清）」）
		// 会被压成两行。
		ui.Box(c).Grow(1)
		if active {
			ui.Icon(c, iconCheck).FontSize(12).TextColor(theme.Accent)
		}
	})
}

// playerIconBtn 是控制台里的一枚图标按钮：30×30 热区，没有底色也没有圆角，
// 只靠颜色变化表达 hover / 激活——加方块底会让控制条看起来碎。
//
// size 是图标字号。label 就是 aria-label：既是无障碍名，也让自动化
// 能按名字点到这颗按钮——自绘 Box 不进无障碍树，没有 Label 就只能靠坐标猜。
func (a *App) playerIconBtn(c *ui.Context, ico *ui.SVG, size float32, active bool, label string, on func()) {
	btn := ui.Box(c).Size(30, 30).Center().Cursor(ui.CursorPointer).Label(label)
	if btn.Clicked() {
		on()
	}
	col := playerIconColor(btn, active)
	btn.Children(func() {
		ui.Icon(c, ico).FontSize(size).TextColor(col)
	})
}

// playerIconColor 是控制条图标的三态颜色：激活走主题色、hover 纯白、
// 其余常态。弹幕电视控件要单独给徽标取色，所以从 playerIconBtn 里提出来。
func playerIconColor(btn ui.Element, active bool) ui.Color {
	switch {
	case active:
		return theme.Accent
	case btn.Hovered():
		return playerIconOn
	}
	return playerIcon
}

// 滚轮调音量的灵敏度：一格滚轮（Windows 默认 3 行 × 33 DIP ≈ 100 DIP）5%，
// 触控板的精确滚动按同一比例折算成更细的步进。
const volumeWheelPerDIP = 0.0005

// wheelVolume 把滚轮位移折算成音量，余量攒着下一次用。
//
// 上滚（DY 为负）变大、下滚变小；1% 取整和拖动那条路径一致，
// 这样百分比数字不会在滑行的每一帧乱跳。取整会丢掉不足 1% 的部分，
// 而触控板一次只推几 DIP，直接丢就永远不动——所以余量记在
// volumeWheelAccum 上，攒够一档才落到 Volume。
func (a *App) wheelVolume(dy float32) {
	target := min(1, max(0, a.Volume+a.volumeWheelAccum-dy*volumeWheelPerDIP))
	next := float32(math.Round(float64(target)*100)) / 100
	a.volumeWheelAccum = target - next
	if next == a.Volume {
		return
	}
	a.Volume = next
	a.markDirty()
}
