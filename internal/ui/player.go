package ui

import (
	"fmt"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/media"
	"alive/internal/theme"
)

// 播放页里常用的半透明底色。控制条和顶栏胶囊都是
// 「深色半透明 + 1px 白色 12% 描边」，叠在画面上才压得住。
var (
	playerBarBg     = theme.MediaBarBg
	playerBarBorder = theme.MediaHair
	playerIcon      = theme.MediaTextBody
	playerIconOn    = theme.White
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
	// 焦点就是播放页要画的那一间。setFocus 已经在换焦点时收好旧的一路：
	// 在墙上静音留着，不在墙上连引擎一起关；墙上已经活着的引擎不会重取。
	a.ensureFocusMedia()
	// 弹幕只属于播放页：不管画面是刚起的还是从墙上接过来的，进页面就连一条新的。
	a.ensureDanmaku()
	a.syncPlayer()
	// 首帧可用时刻是起播耗时的最后一段。
	a.trackFirstFrame()
	// MyGo 0.9.0 的原生播放器在 Loading 阶段不定时请求帧，不能依赖灵动岛
	// 或弹幕恰好在动画；首帧预算和健康检查都靠这一帧续上。
	//
	// 首帧后按 250ms 看一眼：播放器停帧不会主动报错，只靠用户操作触发帧会
	// 漏掉故障。
	if a.focusPlaying() && a.media.eng() != nil && a.media.eng().Err() == nil {
		if !a.media.firstFrame() {
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
	// 弹幕推动器的生命周期绑在这一屏上：离开播放页或关窗时由 OnUnmount 收掉。
	page := ui.Box(c).Grow(1).Padding(0, pad, 0, pad).
		OnUnmount(a.stopDanmuLoop)

	page.Children(func() {
		// 外壳不描边：和设备页、卡片一样，边界交给地→画面的色阶和圆角。
		// 以前那 1px 是为了遮住「描边压在圆角裁剪线上」的接缝，不画描边就没有
		// 这条接缝——Clip 直接把舞台裁成圆角。
		shell := ui.Column(c).Fill().Background(theme.BgSecondary)
		if !full {
			shell.Radius(theme.RadiusXL).Clip()
		}

		shell.Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
				stage := ui.Box(c).Grow(1).Background(theme.Black).
					Label("播放画面（双击全屏）")

				// 画面这一层是什么状态，一帧只求一次：状态层按它画，
				// 浮层的显隐也按它判——状态层带着「重试」，画面没在走
				// 的时候浮层不能自己让位。
				state := a.stageStateNow()
				// 悬浮浮层的显隐：顶栏、控制条、窗口按钮和它们打开的下拉
				// 全部读这一份，先在这一帧定下来，后面照它画。
				a.syncChrome(c, state)
				chromeOff := a.chrome.Hidden
				if chromeOff {
					// 空闲态：控件让位之后连光标一起藏掉
					stage.Cursor(ui.CursorNone)
				}
				stage.Children(func() {
					a.stageFrame(c, stage)
					a.danmuLayer(c)
					// 状态层压在画面和弹幕之上、chrome 之下：它是 z-index 15，
					// 控制条和顶栏是 20。
					a.stageOverlay(c, state)
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
					case windowFullScreen(a.Win):
						windowSetFullScreen(a.Win, false)
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
				//
				// 它不跟着浮层的自动隐藏走：弹幕列表是内容，不是控件，而且
				// 它自己有收起入口。跟着计时一起淡掉只会在画面右边留一条
				// 宽度没变、里面什么都没有的空洞。
				if !a.sysFullscreen() {
					a.danmuPanel(c)
				}
			})
		})
	})
}

// 浮层的自动隐藏参数：2800ms 后淡出，280ms 淡完。
const (
	chromeIdleAfter = 2800 * time.Millisecond
	chromeFadeDur   = theme.MotionSurface
)

// playerChrome 是播放页所有浮层的显隐：一份状态，一个判据。
//
// 顶栏（返回 + 身份胶囊）、控制条和它上面的清晰度 / 线路下拉、弹幕设置和
// 过滤面板、两种全屏下的窗口按钮，全部读这一份。以前它们各读各的：窗口
// 模式下顶栏常驻、控制条自己计时、四个面板写死在「开着就不许隐藏」里，
// 于是同一屏画面里跑着三套逻辑，指针一动，有的回来了有的没有。
//
// 不在这份状态里的是「内容」和「状态」：弹幕列表（自己有收起入口）、
// 缓冲中 / 降档提示这两个小胶囊、画面上的状态层（拉流中 / 未开播 / 失败）。
// 它们说的是画面怎么了，不是可以点的按钮，跟着计时一起消失只会让人错过
// 一条正在说的信息。
type playerChrome struct {
	Hidden  bool
	idleAt  time.Time // 指针最后一次动的时刻：空闲从这里算起
	ptrX    float32
	ptrY    float32
	ptrSeen bool // 指针在这个窗口里出现过：见过之后「不在窗口里」才叫离开
	full    bool // 上一帧是否全屏：进出全屏要先把浮层亮出来
}

// syncChrome 是播放页浮层显隐的唯一判据。
//
// 规则只有两条，和系统播放器一样：
//
//	指针离开窗口 → 立刻让位，不等计时；
//	指针在窗口里停住 chromeIdleAfter → 一起让位（连光标一起藏掉）。
//
// 一动就全回来。判「在窗口里」用的是窗口根元素，不是舞台：舞台只是画面
// 那一块，指针移到右侧弹幕栏、顶栏或窗口按钮上时它报「不在画面上」，于是
// 浮层在手底下自己跑掉——那种「按坐标猜手在不在」就是以前那套。
//
// 「离开窗口」排在最前：人不在这一屏了，也就是最该让位的时候，画面在
// 不在走都一样。剩下的判断只在指针还在窗口里时才有意义——
//
//	没在播（暂停等人）、状态层开着（它带着「重试」）、快捷键说明开着
//	（模态）：一律常显，那几种时候屏幕上的字才是要点，不能自己消失；
//	刚进出全屏：先亮出来，别让人切回来时对着空画面找按钮。
//
// 指针位置读 Root().PointerPosition()：它会顺手把根元素标成
// flagTrackPointer，于是指针在窗口里动一下 MyGo 一定来一帧——哪怕动的是
// 没有控件的空白处（hover 链没变也一样），浮层照样叫得回来。
func (a *App) syncChrome(c *ui.Context, state stageState) {
	now := c.Now()
	show := func() {
		a.chrome.Hidden = false
		a.chrome.idleAt = now
	}

	x, y, over := c.Root().PointerPosition()
	// 「离开窗口」排在最前，而且只认「指针确实进来过」（ptrSeen）：窗口刚
	// 建起来、指针一直没动过时也报不在，那不是人走了，只是还没有事件。
	if !over && (a.chrome.ptrSeen || a.chrome.Hidden) {
		// 指针不在窗口里了：立刻让位。ptrSeen 同时清掉——再回来就是
		// 「刚进窗口」，哪怕落点还是原来那一像素，也要把浮层叫回来。
		a.chrome.ptrSeen = false
		a.hideChrome()
		return
	}
	moved := false
	if over {
		moved = !a.chrome.ptrSeen || x != a.chrome.ptrX || y != a.chrome.ptrY
		a.chrome.ptrSeen = true
		a.chrome.ptrX, a.chrome.ptrY = x, y
	}
	if moved {
		// 刚进窗口，或者在窗口里动了：全亮出来。
		show()
		return
	}
	if !a.focusPlaying() || state != stageLive || a.ShortcutsOpen {
		show()
		return
	}
	// 进出全屏先把浮层亮出来：切回来的那一刻不该对着空画面找按钮。
	if full := a.playerFullscreen(); full != a.chrome.full {
		a.chrome.full = full
		show()
		return
	}

	if a.chrome.idleAt.IsZero() {
		a.chrome.idleAt = now
	}
	idle := now.Sub(a.chrome.idleAt)
	if idle >= chromeIdleAfter {
		a.hideChrome()
		return
	}
	// 画面完全静止（没视频帧、没弹幕）时不会有别的帧，靠这一帧收尾。
	// 基准固定在 idleAt 上，每帧重排也不会把到点时刻往后推。
	c.After(chromeIdleAfter - idle)
}

// hideChrome 让全部浮层一起让位，顺手把开着的浮层收掉。
//
// 下拉开着的时候指针停住，触发条和菜单一起淡出；菜单必须真的关掉：只是
// 「看不见但还开着」的话，指针一动它就原样弹回来，而人早就忘了自己点过它。
// suppress 是给 hover 开菜单的那两个下拉用的——关掉那一帧指针还停在触发条
// 上，不压住的话 hover 当场又把它拉开。
func (a *App) hideChrome() {
	if a.chrome.Hidden {
		return
	}
	a.chrome.Hidden = true
	a.chrome.idleAt = time.Time{}
	a.QualityOpen = false
	a.LineOpen = false
	a.QualityHover.suppress = true
	a.LineHover.suppress = true
	a.DanmuSettingsOpen = false
	a.DanmuFilterOpen = false
}

// chromeFade 让一块浮层跟着隐藏淡出，返回当前透明度。
//
// 隐藏靠 opacity 过渡；隐藏态再补三样，缺一样都留个坑：
//
//	IgnoreInput  淡到 0 的顶栏和控制条仍然占着命中矩形，画面上的双击和
//	             点击播放会被它们吃掉；它们也还在 Tab 序列里。
//	AccessHidden 读屏仍然会念到一组「看不见的按钮」（IgnoreInput 明确
//	             把元素留给辅助技术，见 element.go 的文档）。
//	Translate    调用方自己补：控制条往下让 10px、顶栏往上让 10px。
//
// 挂在谁身上，谁就得有自己的身份：容器建之前给 c.Key(...)。MyGo 的元素
// 身份按位置派生（third_party/mygo/ui/context.go:247 的
// `e.id = mix(p.id, nchild+kind<<56)`），而播放页里排在浮层之前的兄弟是
// 条件建的——窗口全屏的拖窗区、画面状态层、缓冲和降档两个小胶囊。兄弟走掉
// 或者回来的那一帧，后面所有元素的身份都挪一位，AnimateTo 的通道跟着新建，
// 而新通道是从目标值开始的：淡出变成一帧到位。实测的就是这个——窗口全屏下
// 顶栏一帧消失（拖窗区让位那一帧正好被撤掉），回来时又轮到控制条一帧出现。
// 挂了 Key，身份不再取决于前面站着谁。
func (a *App) chromeFade(e ui.Element) float32 {
	target := float32(1)
	if a.chrome.Hidden {
		target = 0
	}
	op := e.AnimateTo("chrome-fade", target, theme.EaseOut(chromeFadeDur))
	e.Opacity(op)
	e.IgnoreInput(a.chrome.Hidden)
	e.AccessHidden(a.chrome.Hidden)
	return op
}

// playerFullscreen 报告播放页是否处于任意一种全屏：应用内的窗口全屏
// （WinFull）或系统原生全屏。两者都会让顶栏让位给画面。
func (a *App) playerFullscreen() bool {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return false
	}
	return a.WinFull || a.winFullscreen
}

// sysFullscreen 报告窗口是否处于系统原生全屏。
//
// 单拎出来是因为有两处只关心系统全屏、不关心窗口全屏：系统全屏下不画
// 弹幕侧栏，飞幕的宽度也就不能扣掉面板。
func (a *App) sysFullscreen() bool {
	return windowFullScreen(a.Win)
}

// windowFullScreen 问系统这一扇窗是不是全屏。测试里换成常量：零值
// *mygo.Window 的任何方法都要走 onMain，而 Tester 的 backend 有 mainID，
// 非主线程等 done 会永久阻塞。真实播放用例需要一个「有窗口」的凭据，
// 但不需要真的问系统。
var windowFullScreen = func(w *mygo.Window) bool {
	return w != nil && w.IsFullScreen()
}

// windowMaximized 同理：全屏时画面右上角那三个窗口按钮要按「现在是不是
// 最大化」画图标，而 IsMaximized 在 Tester 里一样会等主线程。测试要验
// 「系统全屏时那三个按钮在不在」，就得能替换它。
var windowMaximized = func(w *mygo.Window) bool {
	return w != nil && w.IsMaximized()
}

// windowSetFullScreen 进 / 退系统全屏。
//
// 同样是为了能在 Tester 里验：SetFullScreen 走 onMain，测试里会永久阻塞。
// 换掉它之后，「Esc 只退全屏、不把用户踢出直播间」这条规则才测得到——
// 它是 shell_keys.go 里写明的意图，也是用户按 Esc 时最容易踩错的一处。
var windowSetFullScreen = func(w *mygo.Window, on bool) {
	if w != nil {
		w.SetFullScreen(on)
	}
}

// leavePlayer 收掉播放页留下的窗口级与界面级状态，离开播放页时调用。
//
// 两种全屏必须在这里退出。系统全屏留着不走，窗口回到首页时还是整屏、
// 没有 WS_THICKFRAME 边框——拖边缘改不了大小，而 Esc 在首页只管返回、
// 控制条又已经不在了，人没有任何办法把窗口变回普通窗口。WinFull 留着
// 不走，下次进直播间会直接是全屏态，看着像「退出没生效」。
//
// 其余是播放页的跨帧状态：浮层显隐、几个浮层的开关、拖面板和拖
// 音量的标记。不清的话下次进直播间会以上一次的隐藏 / 展开态开局。
//
// 幂等，每帧调用都没问题；判据由调用方给（Router 不是播放页）。
func (a *App) leavePlayer() {
	a.WinFull = false
	if windowFullScreen(a.Win) {
		windowSetFullScreen(a.Win, false)
	}
	a.chrome = playerChrome{}
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
func (a *App) stageFrame(c *ui.Context, stage ui.Element) {
	// 四边贴死：只写 Fill 的话元素会按自己的比例排，容器比 16:9 高时
	// 底下就空出一条黑边（控制条会浮在那条黑边上）。
	// contain 是有意选的：宁可留黑边也不裁、不拉伸，
	// 弹幕层再盖到黑边上去。
	p := a.media.eng()
	// 这一帧舞台画不画画面，由 videoOwnerOf 裁决（见 surface.go）。
	ownsStage := a.videoOwnerOf(a.focusKey()) == videoStage
	// 封面只在这两种时候出现：播放器还没开，或者开了但首帧没到。
	//
	// 只有舞台画画面时才铺封面：这一帧画面归别处（墙上那格）就留黑底，
	// 否则两处同时露出「这一个直播间」。
	var cover *ui.Bitmap
	if ownsStage && (p == nil || !p.Ready()) {
		if w, _ := c.Size(); w > 0 {
			cover = media.Default.Bitmap(a.currentCover(), w)
		}
	}
	// 黑边底必须排在画面之前。它是一张**静态**图：抓一帧、缩小、模糊，
	// 之后就不再变。
	if vw, vh := stageSourceSize(p, cover); a.backdropShown(c, stage, vw, vh) {
		a.stageBackdrop(c, p, cover)
	}
	if p != nil {
		// Video 元素必须一直在：视频只有在被显示时才会往下走，藏起来
		// 或干脆不画，解码就停，首帧永远等不到。
		//
		// 「一直在」不等于「舞台这一帧就画它」：画面归属由 videoOwnerOf
		// 裁决，舞台这一帧可能只画底。一帧里两个 ui.Video 画同一个播放器，
		// 实测只有一个出画，另一个是黑的。
		if a.videoOwnerOf(a.focusKey()) == videoStage {
			ui.Video(c, p).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
			if p.Ready() {
				return
			}
		} // 播放器已经开出来了，但首帧还没到——此时 Video 是一块纯黑。
		// 拿封面盖住，看起来是「正在起播」而不是「播坏了」。
	}
	if cover != nil {
		components.Image(c, cover).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
	}
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Background(theme.MediaScrimSoft)
}

// stageBackdrop 在黑边区铺一层**静态**模糊底。
//
// 配方直接照 B 站的实现（2026-11-23 从 room-player 与 mobile-player 的
// bundle 里读出来的，不是看着调的）：
//
//	background-image: url(<主播设的房间背景图，没有就用封面>)
//	background-size: cover;  background-position: center;
//	filter: blur(35px);
//	+ 一层 rgba(0,0,0,.5) 压暗（H5 的 .background-cover）
//
// 两个和「看起来像」有关、但容易漏掉的细节：
//
//   - B 站的底是**平台给的一张静态图**（room_info.background / app_background），
//     不是视频帧。它不跟着画面动——所以每帧只是画一张贴图。
//   - 模糊会把元素边缘之外的透明吸进来，四周出现一圈发白的渐变。H5 用
//     transform: scale(1.2) 把图放大 20% 盖住它。这里同样让底图过扫。
func (a *App) stageBackdrop(c *ui.Context, p *ui.VideoPlayer, cover *ui.Bitmap) {
	bmp := a.backdropFor(c, a.current())
	if bmp == nil {
		bmp = cover
	}
	if bmp == nil {
		return
	}
	backdropLayers(c, bmp)
}

// backdropLayers 在当前元素里铺黑边模糊底：过扫的底图 + 模糊 + 压暗。
//
// 舞台的黑边底和封面底都走这一处：各写一套的话，切过去的第一帧会从「糊」
// 跳到「实」——那是用户报的「闪一下」里最容易漏掉的一种。
func backdropLayers(c *ui.Context, bmp *ui.Bitmap) {
	// 过扫：底图铺得比元素大一圈，模糊把边缘吃掉之后四边仍然是满的。
	// 这是 H5 的 scale(1.2)：元素本身仍占舞台那么大，只是内容被放大了。
	pad := theme.MediaBackdropOverscan
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Children(func() {
		components.Image(c, bmp).Absolute().Top(-pad).Left(-pad).Right(-pad).Bottom(-pad).
			Fit(ui.Cover)
	}).Blur(theme.MediaBackdropBlur)
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().
		Background(theme.MediaBackdropScrim)
}

// backdropFor 取这一间该铺的黑边底图：主播设的背景图优先，没有就用封面。
//
// 宽度按**窗口**算，不按元素算：同一个 URL + 同一个宽度才是同一个缓存键，
// 换尺寸前后拿到的才是同一张图。
func (a *App) backdropFor(c *ui.Context, room Streamer) *ui.Bitmap {
	url := room.BackdropURL
	if url == "" {
		url = room.CoverURL
	}
	if url == "" {
		return nil
	}
	w, _ := c.Size()
	if w <= 0 {
		return nil
	}
	return backdropBitmap(url, w*(1+theme.MediaBackdropOverscan))
}

// backdropBitmap 取一张背景图。测试里换成直接给图，不联网。
var backdropBitmap = func(url string, width float32) *ui.Bitmap {
	return media.Default.Bitmap(url, width)
}

// stageSourceSize 是背景层要用的那一帧的尺寸：有画面就是视频帧，起播前是封面。
func stageSourceSize(p *ui.VideoPlayer, cover *ui.Bitmap) (w, h float32) {
	if p != nil && p.Ready() {
		iw, ih := p.Size()
		return float32(iw), float32(ih)
	}
	if cover != nil {
		iw, ih := cover.Size()
		return float32(iw), float32(ih)
	}
	return 0, 0
}

// backdropShown 决定这一帧画不画模糊底。
//
// 稳态按舞台的黑边宽度判（backdropNeeded），两条例外也要画：
//
//   - 窗口刚变过：这一帧的布局还是旧的，按旧尺寸判会晚一帧，拖窗时黑边
//     区就闪一下纯黑。多画一帧模糊比闪一下便宜。
//   - 舞台还没有上一帧的布局（刚进页面）：尺寸未知，先画。
func (a *App) backdropShown(c *ui.Context, stage ui.Element, vw, vh float32) bool {
	if vw <= 0 || vh <= 0 {
		return false
	}
	ww, wh := c.Size()
	resized := ww != a.stageWinW || wh != a.stageWinH
	a.stageWinW, a.stageWinH = ww, wh
	if resized {
		return true
	}
	b := stage.LayoutBounds()
	if b.W <= 0 || b.H <= 0 {
		return true
	}
	return backdropNeeded(b.W, b.H, vw, vh)
}

// letterbox 返回舞台四边因等比 contain 留下的黑边宽度，DIP。
// 视频比舞台宽时是上下两条，否则是左右两条。
func letterbox(stageW, stageH, videoW, videoH float32) (top, right, bottom, left float32) {
	if stageW <= 0 || stageH <= 0 || videoW <= 0 || videoH <= 0 {
		return 0, 0, 0, 0
	}
	if videoW*stageH > stageW*videoH {
		bar := (stageH - stageW*videoH/videoW) / 2
		return bar, 0, bar, 0
	}
	bar := (stageW - stageH*videoW/videoH) / 2
	return 0, bar, 0, bar
}

// backdropNeeded 判断黑边是否宽到值得开一层模糊底。窄于
// theme.MediaBackdropMinBar 的多半是取整余量，为它开一层全屏模糊不值。
func backdropNeeded(stageW, stageH, videoW, videoH float32) bool {
	top, right, bottom, left := letterbox(stageW, stageH, videoW, videoH)
	return max(max(top, bottom), max(left, right)) >= theme.MediaBackdropMinBar
}

// stageState 是画面这一层此刻的状态。它同时决定两件事：状态层画什么，
// 以及浮层有没有资格自己让位（syncChrome 只让 stageLive 让位）。
//
// 两个用户、一个判据：状态层带着「重试」和「立即刷新」，画面没在走的时候
// 它不能跟着计时一起消失——分开各判一次，迟早会有一处漏掉某个状态。
type stageState int

const (
	// stageLive 是画面真在走：没有状态层。
	stageLive stageState = iota
	// stageFetching 是正在取流，或者解码器还没出首帧。
	stageFetching
	// stageFetchFailed 是取流失败，平台或者网络没给出地址。
	stageFetchFailed
	// stagePlaybackFailed 是播放器开出来了但解码失败（首片坏、编码不支持）。
	stagePlaybackFailed
	// stageOffline 是平台说这一间没开播。
	stageOffline
)

// stageStateNow 求这一帧画面是什么状态。
//
// 转封装就绪不等于画面就绪：播放器开出来之后，Media Foundation 还要下首片、
// 建解码器，实测 2~4 秒才出第一帧。所以加载态延到播放器真的 Ready 为止，
// 否则观感是「loading → 黑屏几秒 → 突然有画面」。
//
// 播放器开出来了但解码失败时它永远 Ready 不了，必须单独一档，不然会一直
// 卡在加载层上。
func (a *App) stageStateNow() stageState {
	m := &a.media
	if m.roomKey() == "" {
		return stageLive
	}
	switch {
	case m.isLoading():
		return stageFetching
	case m.eng() != nil && !m.eng().Ready() && m.eng().Err() == nil && !a.media.settling():
		// 刚换完地址的那几秒不报加载态。新引擎要按新的呈现尺寸重配一次输出，
		// Ready 会短暂变假，可屏幕上的画面是连续的——这时候压一层加载态、把
		// 浮层一起叫回来，比什么都不显示更糟。真加载（isLoading）照报。
		return stageFetching
	case m.errText() != "":
		return stageFetchFailed
	case m.eng() != nil && m.eng().Err() != nil:
		return stagePlaybackFailed
	case m.streamNow().RoomID != "" && !m.streamNow().Live:
		return stageOffline
	}
	return stageLive
}

// stageOverlay 画画面上的状态层。画哪一档由 stageStateNow 求，这里只负责画。
func (a *App) stageOverlay(c *ui.Context, state stageState) {
	m := &a.media

	switch state {
	case stageFetching:
		a.overlayLayer(c, func() {
			components.Spinner(c, components.SpinnerRing).Size(30, 30)
			a.overlayText(c, "正在拉取直播流…")
		})

	case stageFetchFailed:
		a.overlayLayer(c, func() {
			a.overlayText(c, m.errText())
			// 多线路平台失败过才提示，单线路说了也没用
			if len(m.streamNow().Lines) > 1 {
				components.Text(c, "可以在控制条切换线路后重试").
					FontSize(theme.TextXS).TextColor(theme.White.Alpha(0.6)).
					TextAlign(ui.Center).LineHeight(1.7)
			}
			a.overlayButton(c, "重试", a.retryStream)
		})

	case stagePlaybackFailed:
		a.overlayLayer(c, func() {
			a.overlayText(c, "播放失败，请重试或切换线路")
			a.overlayButton(c, "重试", a.retryStream)
		})

	case stageOffline:
		a.overlayLayer(c, func() {
			// 58 圆底 + 26 图标
			icon := ui.Box(c).Size(58, 58).Radius(29).Margin(0, 0, 4, 0).
				Background(theme.MediaRowLine).
				Border(1, theme.MediaHair).Center()
			icon.Children(func() {
				components.Icon(c, iconMonitorOff).FontSize(26).
					TextColor(theme.MediaTextDim)
			})
			components.Text(c, "主播暂时没有开播").FontSize(theme.TextLG).FontWeight(600).
				TextColor(theme.White94)
			a.overlayText(c, "主播开播后请点「立即刷新」；回到列表重新进入也会重连")
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
		Background(theme.MediaOverlayScrim).Children(func() {
		ui.Column(c).Fill().Center().Gap(14).Children(fn)
	})
}

// overlayText 是状态层的说明文字：13px / 白 86% / 居中 / 行高 1.7，
// 最大宽度取窗口的 70%。
func (a *App) overlayText(c *ui.Context, text string) {
	w, _ := c.Size()
	components.Text(c, text).FontSize(theme.TextSM).
		TextColor(theme.MediaTextBody).
		LineHeight(1.7).TextAlign(ui.Center).MaxWidth(w * 0.7)
}

// overlayButton 是状态层上的主按钮：
// 40 高、左右 24、纯白胶囊、深色字。
//
// 白底必须从 hoverSurfaceRest 传进去，不能先 Background(theme.White) 再
// Background(hoverSurface(...))——后者常态返回 Transparent，会把白底抹掉，
// 深色字就压在深色遮罩上了（对比度≈1.05:1）。这是取流失败/未开播时唯一
// 能点的按钮。
func (a *App) overlayButton(c *ui.Context, label string, on func()) {
	btn := ui.Row(c).Height(40).Padding(0, 24, 0, 24).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
	btn.Background(hoverSurfaceRest(btn, "overlay-btn-bg", btn.Hovered(),
		theme.White, theme.MediaOverlayBtnHover))
	if btn.Clicked() {
		on()
	}
	down := btn.Pressed()
	btn.Children(func() {
		components.Icon(c, iconSync).FontSize(13).TextColor(theme.MediaOverlayBtnInk)
		components.Text(c, label).FontSize(theme.TextSM).FontWeight(600).
			TextColor(theme.MediaOverlayBtnInk)
		pressDim(c, down, theme.RadiusFull)
	})
}

// bufferingPill 是左下角的「缓冲中…」小胶囊：
// left 16 / bottom 76 / 30 高 / 胶囊底，压在控制条下面。
//
// 判定看的是「画面有没有在动」，不是 VideoPlayer.Buffering() ——
// 后者在直播 HLS 上会永久卡在 true（原因见 playerMedia.stalled）。
func (a *App) bufferingPill(c *ui.Context) {
	// 只读观测结果，不在构建路径里观测：stalled(p) 会写 lastPos/lastPosAt，
	// 那是状态变更，归 trackFirstFrame。
	if a.media.eng() == nil || a.media.isLoading() || !a.media.bufferingNow {
		return
	}
	ui.Row(c).Absolute().Left(16).Bottom(76).Height(30).
		Padding(0, 12, 0, 12).Gap(7).Radius(theme.RadiusFull).
		AlignItems(ui.Center).
		Background(playerBarBg).Border(1, playerBarBorder).Children(func() {
		components.Spinner(c, components.SpinnerRing).Size(14, 14)
		components.Text(c, "缓冲中…").FontSize(theme.TextXS).TextColor(playerIcon)
	})
}

// noticePill 是降档一类临时提示：底部居中的小胶囊，几秒后自己消失。
// 不放在错误层里——画面还在正常播，只是平台的档位和点选的不一样。
func (a *App) noticePill(c *ui.Context) {
	// 视图时钟，不是墙钟：noticeUntil 由 notifyPlayback 按帧钟写，
	// c.After 排的也是帧钟。三处必须同一个时基，否则 Tester 里推不动。
	now := c.Now()
	if !a.media.noticeActive(now) {
		return
	}
	// 让下一帧在提示到期时到来，清理逻辑在 noticeActive 里。
	c.After(a.media.noticeUntil.Sub(now))
	ui.Box(c).Absolute().Left(0).Right(0).Bottom(76).Center().PassThrough().Children(func() {
		ui.Row(c).Height(30).Padding(0, 14, 0, 14).Radius(theme.RadiusFull).
			AlignItems(ui.Center).
			Background(playerBarBg).Border(1, playerBarBorder).Children(func() {
			components.Text(c, a.media.notice).FontSize(theme.TextXS).TextColor(playerIcon)
		})
	})
}

// currentCover 返回当前播放项对应卡片的封面。
func (a *App) currentCover() string { return a.current().CoverURL }

// current 返回焦点那一间。没有焦点时给一个空的占位，播放页各层按"没有房间"
// 处理（取流、弹幕、状态层都会早退）。
func (a *App) current() Streamer {
	if a.focus != nil {
		return a.focus.room
	}
	return Streamer{Name: "未选择直播间", Title: "", Category: ""}
}

// playerTopbar 是播放页自己的顶栏：返回、这一间是谁、关注。
//
// 这三件事以前都归灵动岛。现在岛在播放页回时钟（HIG：App 打开时它自己的
// Live Activity 不该占着岛），所以它们必须有一个自己的位置——照 iPhone，
// 那个位置就是 App 自己的顶栏。
//
// 它和其余浮层一样随 chrome 淡出，窗口模式也不例外：以前这边常驻、控制条
// 自己计时，同一屏画面里两套规矩。退出直播间的入口还在——动一下指针它就
// 回来，Esc 也一直有效（shell_keys.go），不必为它留一块常驻的浮层。
//
// 这条 Row 只是布局容器，自己不该吃指针：它整条压在画面顶部，不让穿透的话，
// 返回钮与胶囊之间那段空白会吃掉「双击画面切全屏」的点击（全屏时还会连
// 拖窗条一起挡死）。按钮与胶囊是子元素，照常命中。
func (a *App) playerTopbar(c *ui.Context) {
	// Key 不是装饰，见 chromeFade 的说明：窗口全屏时排在它前面的拖窗区是
	// 条件建的，让位那一帧正好消失，身份一挪这条顶栏就一帧到位。
	row := ui.Row(c.Key("player-topbar")).Absolute().Top(14).Left(14).Right(14).Gap(10).PassThrough()
	// 淡出的同时整条往上让 10px，和控制条往下让 10px 对称。
	row.Translate(0, -(1-a.chromeFade(row))*10)
	row.Children(func() {
		// ------------------------------ 返回：44 圆钮
		//
		// 胶囊底走 hoverSurfaceRest 传常态色：先 Background(MediaCapsuleBg)
		// 再 Background(hoverSurface(...)) 会让常态变成透明，画面亮的时候
		// 这颗键就只剩一根白描边。
		back := ui.Box(c).Size(44, 44).Radius(999).
			Border(1, theme.MediaTrack).
			Center().Cursor(ui.CursorPointer).Label("退出直播间")
		back.Background(hoverSurfaceRest(back, "back-bg", back.Hovered(),
			theme.MediaCapsuleBg, theme.MediaCapsuleBgHover)).
			BorderColor(hoverInk(back, "back-border", back.Hovered(), theme.MediaTrack, theme.MediaHairStrong))
		if back.Clicked() {
			// 叉是明确退出：结束焦点、断弹幕，不留后台声音。别的离开方式
			// （顶栏 tab、Esc、搜索跳转）只进后台收听。
			a.closeFocus()
		}
		back.Children(func() {
			// 关闭用 X，不是返回箭头
			components.Icon(c, iconPlayerBack).FontSize(19).TextColor(theme.MediaText)
		})

		a.playerIdentity(c)
	})
}

// playerIdentity 是顶栏里「这一间是谁」的那一枚胶囊：头像 + 标题 + 平台·ID + 关注。
//
// 和岛上那一条（playerIsland）是同一份信息、同一套墨底语言，但排版更宽：
// 这里不需要为 38 高的胶囊省像素。
func (a *App) playerIdentity(c *ui.Context) {
	s := a.current()
	if s.Platform == "" {
		return
	}
	const avatarD float32 = 28

	pill := ui.Row(c).Height(44).Padding(0, 8, 0, 8).Gap(9).Radius(999).
		AlignItems(ui.Center).Label("正在看 " + s.Name)
	pill.Background(hoverSurfaceRest(pill, "identity-bg", pill.Hovered(),
		theme.MediaCapsuleBg, theme.MediaCapsuleBgHover)).
		Border(1, theme.MediaTrack)
	pill.Children(func() {
		// 头像：和岛上是同一套（真图，没有就色块首字）。
		box := ui.Box(c).Size(avatarD, avatarD).Shrink(0)
		box.Children(func() {
			if bmp := media.Default.Avatar(s.AvatarURL, avatarD); bmp != nil {
				components.Image(c, bmp).Size(avatarD, avatarD).Radius(avatarD / 2).Fit(ui.Cover)
			} else {
				ui.Box(c).Size(avatarD, avatarD).Radius(avatarD / 2).
					Background(ui.Oklch(0.42, 0.10, s.Hue+180)).Center().Children(func() {
					components.Text(c, initial(s.Name)).FontSize(avatarD * 0.4).FontWeight(600).
						TextColor(theme.MediaTextBody)
				})
			}
		})

		ui.Column(c).Gap(1).Justify(ui.Center).Children(func() {
			components.Text(c, s.Title).FontSize(theme.TextXS).FontWeight(600).
				SingleLine().TextColor(theme.MediaText)
			components.Text(c, islandPlayerMeta(s)).FontSize(theme.Text2XS).
				SingleLine().TextColor(theme.MediaTextDim)
		})

		// 关注：和岛上那颗星同一个动作、同一个语言。
		followed := a.isFollowed(s)
		label := "关注"
		if followed {
			label = "取消关注"
		}
		star := ui.Box(c).Size(26, 26).Radius(theme.RadiusFull).
			Center().Cursor(ui.CursorPointer).Label(label)
		col := theme.MediaText
		if followed {
			star.Background(theme.StatusOnline.Alpha(0.16)).
				Border(1, theme.StatusOnline.Alpha(0.34))
			col = theme.StatusOnlineInk
		} else {
			star.Background(hoverSurface(star, "identity-follow-bg", star.Hovered(), theme.MediaHair)).
				Border(1, hoverInk(star, "identity-follow-border", star.Hovered(),
					theme.MediaRowLine, theme.MediaHairStrong))
		}
		if star.Clicked() {
			a.toggleFollow(s)
		}
		star.Children(func() {
			ico := iconStar
			if followed {
				ico = iconStarFilled
			}
			components.Icon(c, ico).FontSize(14).TextColor(col)
			pressDim(c, star.Pressed(), theme.RadiusFull)
		})
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
	// 身份用 Key 给，不用位置（见 chromeFade）：排在它前面的拖窗区会在
	// 让位那一帧消失，按位置算的身份跟着挪，淡出就变成一帧到位。
	dock := ui.Box(c.Key("player-dock")).Absolute().Left(14).Right(14).Bottom(12)
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
				if a.focusPlaying() {
					playIcon = iconPause
				}
				playLabel := "播放"
				if a.focusPlaying() {
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
				//
				// 显示的值读当前这一路自己的房间记录，不读全局：同一个
				// 控件在切房间后必须立刻换成那一间的档位名。
				key := a.choiceKey()
				if qOpts := a.qualityOptions(); len(qOpts) > 0 {
					a.playerDropdown(c, "清晰度", qOpts,
						a.qualityFor(key), &a.QualityOpen, &a.QualityHover, a.PickQuality)
				}
				// 线路只列平台真实给出的节点：抖音这类没有多线路概念的平台
				// 整块不出现——网页端也没有「自动线路」这一项。
				if lineOpts := a.lineOptions(); len(lineOpts) > 0 {
					a.playerDropdown(c, "线路", lineOpts,
						a.lineFor(key), &a.LineOpen, &a.LineHover, a.pickLine)
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

				isFull := windowFullScreen(a.Win)
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
//
// 24 → 21：描边在 icons.go 已经压到光栅器的下限（1.7，再往下画出来一样），
// 想让这一排轻一点只能动绝对尺寸。
const playerIconSize float32 = 21

// playerIconHit 是控制条图标按钮的点击热区；观测墙用同一套按钮但缩到 24。
const playerIconHit float32 = 30

// controlMetrics 是控制条在当前窗口宽度下的尺寸。
//
// 控制条的尺寸分 @media (max-width: 1100px) / (max-width: 980px) 两档：
// 窄一档间距 12 收到 8、滑条 96 收 90；再窄一档滑条 72，
// 并且百分比整个让位，免得控制条被挤到换行。
type controlMetrics struct {
	leftGap, rightGap float32
	volumeW           float32
	iconSize          float32
	iconHit           float32
	showPercent       bool
}

func metricsFor(c *ui.Context) controlMetrics {
	m := controlMetrics{
		leftGap: 12, rightGap: 12, volumeW: 96,
		iconSize: playerIconSize, iconHit: playerIconHit,
		showPercent: true,
	}
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
	a.volumeControlFor(c, m, "")
}

// volumeControlFor 是带名字的音量组：墙上 12 格各有一个，必须能分辨。
//
// 无障碍名同屏不能重名（AGENTS.md）。播放页只有一组，名字就用「静音」「音量」；
// 墙上要带上房间名，读屏和 mygo-automation 才点得准是哪一格。
func (a *App) volumeControlFor(c *ui.Context, m controlMetrics, name string) {
	suffix := ""
	if name != "" {
		suffix = " " + name
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 0, 0, 6).Children(func() {
		muteIcon := iconVolumeLow
		switch {
		case a.focusVolume() <= 0:
			muteIcon = iconVolumeMute
		case a.focusVolume() > 0.5:
			muteIcon = iconVolumeHigh
		}
		muteLabel := "静音" + suffix
		if a.focusVolume() <= 0 {
			muteLabel = "取消静音" + suffix
		}
		a.iconBtnSized(c, muteIcon, m.iconSize, m.iconHit, false, muteLabel, a.toggleMute)

		// 音量条是**滑块**，不是一块会发光的装饰：读屏要念得出当前值，
		// 也要知道方向键可以调。只挂 Label 的话，无障碍树里 role 是普通
		// 分组、value 是空字符串——读屏只会说「音量」，念不出 60%，
		// 而得靠屏幕上那个 "60%" 文本去猜。MyGo 的写法是
		// Role(RoleSlider) + Range，后者进 aria-valuemin/valuemax/valuenow
		// （third_party/mygo/ui/access.go:998）。
		hot := ui.Box(c).Width(m.volumeW).Height(18).Center().
			Cursor(ui.CursorPointer).Label("音量"+suffix).
			Role(ui.RoleSlider).
			Range(0, 100, math.Round(float64(a.focusVolume())*100)).
			Step(volumeKeyStep * 100).
			Focusable()
		// 读屏的「增大/减小」（AccessIncrement/Decrement）在 MyGo 里被换成
		// 方向键交给这个元素（third_party/mygo/ui/access.go:1240），Tab 也能
		// 停在这儿。两条路都得有人接，否则滑块只是"读得出、调不动"。
		if hot.Shortcut(0, ui.KeyLeft) {
			a.setVolume(max(0, a.focusVolume()-volumeKeyStep))
		}
		if hot.Shortcut(0, ui.KeyRight) {
			a.setVolume(min(1, a.focusVolume()+volumeKeyStep))
		}
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
				if v != a.focusVolume() {
					a.setVolume(v)
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
				// 填充用强调色，而且必须是**墨面那一支**（AccentOnInk）。
				//
				// 控制条是恒定暗面：直接放 theme.Accent，浅色主题里那条就是
				// 墨白·日 / 纸·日 的近黑强调色，压在墨玻璃上等于没画。
				// AccentOnInk 是 palette.go 对 IslandInk 推导的同族强调色——
				// 深色主题下它就是 Accent 本身，浅色主题下翻成亮的那一支。
				ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).
					Width(m.volumeW * a.focusVolume()).Radius(999).Background(theme.AccentOnInk)
			})
		})

		if m.showPercent {
			components.Text(c, fmt.Sprintf("%d%%", int(a.focusVolume()*100+0.5))).
				FontSize(theme.TextXS).TextColor(theme.MediaTextDim).
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
func (a *App) playerDropdown(c *ui.Context, label string, options []string, value string, open *bool, h *hoverClose, onPick func(string)) {
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
		components.Text(c, value).FontSize(theme.TextXS).TextColor(col)
		caret := btn.AnimateTo("dd-"+label, caretAngle(*open), theme.Chrome())
		components.Icon(c, iconChevron).FontSize(12).TextColor(col).Rotate(caret)
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

func (a *App) playerDropdownItem(c *ui.Context, opt string, value string, open *bool, onPick func(string)) {
	active := opt == value
	// --player-accent-strong = color-mix(in srgb, accent 24%, transparent)
	// 注意用 Alpha 而不是 Mix(Transparent)：后者逐通道插值，会把 RGB 一起
	// 压到 24%，选中底色变成一块发黑的东西。
	strong := theme.Accent.Alpha(0.24)

	row := ui.Row(c).FillWidth().Padding(8, 12, 8, 12).Gap(12).Radius(8).
		AlignItems(ui.Center).Cursor(ui.CursorPointer).Label("选项:" + opt)
	// 选中和悬停用同一块底，但都要过渡：瞬间跳会和面板的淡入不同步。
	if active {
		row.Background(strong)
	} else {
		row.Background(hoverSurface(row, "dd-row-bg", row.Hovered(), strong))
	}
	if row.Clicked() {
		// 选中值写进房间记录（由 onPick 完成），这里只负责收起面板：
		// 组件不再持有一份自己的值，免得和模型里的那一份对不上。
		*open = false
		if onPick != nil {
			onPick(opt)
		}
	}
	row.Children(func() {
		col := theme.MediaTextBody
		if active {
			col = theme.Accent
		}
		components.Text(c, opt).FontSize(theme.TextSM).TextColor(col).NoWrap()
		// 弹性空间交给空 Box，不能让文本自己 Grow：MyGo 的 Grow 从 0
		// 尺寸起步、允许缩到内容宽度以下，最宽的那项（「自动（720P 超清）」）
		// 会被压成两行。
		ui.Box(c).Grow(1)
		if active {
			components.Icon(c, iconCheck).FontSize(12).TextColor(theme.Accent)
		}
	})
}

// playerIconBtn 是控制台里的一枚图标按钮：30×30 热区，没有底色也没有圆角，
// 只靠颜色变化表达 hover / 激活——加方块底会让控制条看起来碎。
//
// size 是图标字号。label 就是 aria-label：既是无障碍名，也让自动化
// 能按名字点到这颗按钮——自绘 Box 不进无障碍树，没有 Label 就只能靠坐标猜。
func (a *App) playerIconBtn(c *ui.Context, ico *ui.SVG, size float32, active bool, label string, on func()) {
	a.iconBtnSized(c, ico, size, playerIconHit, active, label, on)
}

// iconBtnSized 是同一颗图标按钮的可缩放版本。
//
// 热区和字号一起给：观测墙的一格只有三百来像素宽，播放页的 30 热区 +
// 24 图标会把整格塞满，那边按 24 / 16 画。
func (a *App) iconBtnSized(c *ui.Context, ico *ui.SVG, size, hit float32, active bool, label string, on func()) {
	btn := ui.Box(c).Size(hit, hit).Center().Cursor(ui.CursorPointer).Label(label)
	if btn.Clicked() {
		on()
	}
	col := playerIconColor(btn, active)
	down := btn.Pressed()
	btn.Children(func() {
		components.Icon(c, ico).FontSize(size).TextColor(col)
		pressDim(c, down, hit/2)
	})
}

// playerIconColor 是控制条图标的三态颜色：激活走主题色、hover 纯白、
// 其余常态。弹幕电视控件要单独给徽标取色，所以从 playerIconBtn 里提出来。
func playerIconColor(btn ui.Element, active bool) ui.Color {
	if active {
		return theme.Accent
	}
	return hoverInk(btn, "player-icon-ink", btn.Hovered(), playerIcon, playerIconOn)
}

// 滚轮调音量的灵敏度：一格滚轮（Windows 默认 3 行 × 33 DIP ≈ 100 DIP）5%，
// 触控板的精确滚动按同一比例折算成更细的步进。
const volumeWheelPerDIP = 0.0005

// volumeKeyStep 是方向键和读屏「增大/减小」跨一步的音量：和滚轮一格一致（5%）。
// 滑块的无障碍步长（aria-valuetext 的 step）也用它，读屏念的步进和实际步进
// 必须是同一个数，否则读屏说"涨了 1%"而实际涨了 5%。
const volumeKeyStep = 0.05

// wheelVolume 把滚轮位移折算成音量，余量攒着下一次用。
//
// 上滚（DY 为负）变大、下滚变小；1% 取整和拖动那条路径一致，
// 这样百分比数字不会在滑行的每一帧乱跳。取整会丢掉不足 1% 的部分，
// 而触控板一次只推几 DIP，直接丢就永远不动——所以余量记在
// volumeWheelAccum 上，攒够一档才落到 Volume。
func (a *App) wheelVolume(dy float32) {
	target := min(1, max(0, a.focusVolume()+a.volumeWheelAccum-dy*volumeWheelPerDIP))
	next := float32(math.Round(float64(target)*100)) / 100
	a.volumeWheelAccum = target - next
	if next == a.focusVolume() {
		return
	}
	a.setVolume(next)
}
