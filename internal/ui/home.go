package ui

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/media"
	"alive/internal/theme"
)

// App 是首页的全部状态。协议层接上之前，streamers 用一份假数据撑版式。
type App struct {
	Win       *mygo.Window
	Platform  string
	Streamers []Streamer

	// 分区选择：一级 / 二级 / 三级（SubCatIndex 的 -1 表示「全部」）
	GroupIndex    int
	CategoryIndex int
	SubCatIndex   int

	Groups []CategoryGroup

	// subCategories 是二级分区到三级分区的映射。真实数据源目前没有三级
	// 分区（B 站本身也没有），生产模式下一直是空的，三级分区控件自然不
	// 出现。示例数据由 NewApp 填。
	subCategories map[string][]string

	// 真实数据模式：分区树与直播列表来自 internal/live。
	// 假数据（Streamers）只留给视图测试，界面不显示。
	LiveMode         bool
	Rooms            []Streamer
	ListLoading      bool
	ListLoadingMore  bool // 正在追加下一页
	ListHasMore      bool // 上游还有下一页（由各平台的分页字段推断）
	GroupsByPlatform map[string][]CategoryGroup
	liveLoaded       map[string]bool
	liveSeq          int
	// 分区级列表缓存：切回看过的分区先渲染缓存，再判断是否过期刷新。
	feedCache map[string]feedEntry

	// 分区搜索：查询词、输入框是否展开、当前高亮项
	CategoryQuery        string
	CategorySearchOpen   bool
	CategoryHit          int
	catHitQuery          string
	CategorySearchHandle ui.Handle

	// 「全部」浮层（二级分区放不下时出现）是否展开
	MoreOpen bool

	// Remote 是接口拿到的真实直播间（B 站）。真实模式下 Streamers 是 nil，
	// Remote 为空时列表就是空态，不会回落到 fake 池。
	Remote []Streamer

	// Router 记着三个页面（首页 / 订阅 / 播放）的历史与转场
	Router *ui.Router

	// 订阅分区
	Subs     []Subscription
	SubIndex int
	// 订阅页的列表：平台和分区可以跟首页不同，所以状态独立一份
	SubRooms       []Streamer
	SubLoading     bool
	SubLoadingMore bool
	SubHasMore     bool
	SubErr         string
	subKey         string // 已经发起过的 platform|areaId
	subSeq         int

	// 播放页：当前播放项与音量（0–1）
	Player       Streamer // 当前直播间（从任意列表点进来）
	PlayerIndex  int
	PlayerRemote bool // 播放项来自 a.Remote 而不是示例池
	Volume       float32
	Playing      bool // 播放页的播放 / 暂停状态
	WinFull      bool // 窗口全屏（baseline 的 cssFullscreen）：播放页铺满窗口，应用顶栏让位
	// InfoOpen 是播放页灵动岛是否展开：进直播间自动展开，闲置几秒收起，
	// 点岛可以反复开关。infoOpenAt 是最近一次展开 / 悬停刷新的时刻，
	// infoRoom 记着当前房间，用来判断「刚进直播间」。
	InfoOpen   bool
	infoOpenAt time.Time
	infoRoom   string
	// watch 是当前这间直播间已经看了多久，暂停时不走；watchAt 是上一次
	// 累计的时刻——每帧只加一次差值，结果不受帧率影响。换房间归零。
	// watchSeed 只在截图验收时预置起始时长（ALIVE_WATCH=2h），tickWatch
	// 消费一次就清掉；生产路径上它恒为 0。
	watch       time.Duration
	watchAt     time.Time
	watchSeed   time.Duration
	Quality     string
	QualityOpen bool
	Line        string
	LineOpen    bool
	// 这三个浮层是 hover 触发的（baseline 的 ControlsDropdown / 设置面板都是
	// onMouseEnter 开、onMouseLeave 排一个 220ms 后关的定时器），
	// hoverLeft 记指针最后一次离开按钮和面板的时刻。
	QualityHover       hoverClose
	LineHover          hoverClose
	DanmuSettingsHover hoverClose
	// Mini 是小窗播放的独立窗口；nil 表示没开小窗
	Mini *mygo.Window
	// media 是播放页的媒体层：转封装 + 播放器 + 弹幕连接。
	// 它们要跨帧活着，所以挂在 App 上而不是视图里。
	media playerMedia
	// pushedVolume 是最后一次推给播放器的音量。
	// ui.VideoPlayer 没有 Volume() getter，只能自己记一份来避免每帧重设。
	pushedVolume float64
	// volumeDragging 记住音量条是否正被按住：按下后指针可以拖出热区，
	// 不能每帧靠 Pressed 判断（它要求指针仍在元素内）
	volumeDragging bool

	// 弹幕层：开关、正在飞的弹幕、推动器状态
	DanmuOn bool
	// Danmu 是画面上正在飞的那批，飞完就剪掉。
	// DanmuLog 是右侧面板的历史，收到就留着，和飞行状态无关——
	// baseline 里两者本来就是两份数据（messages 留 300，Overlay 只管当帧）。
	Danmu    []danmuLine
	DanmuLog []danmuLine
	danmuSeq int
	danmu    danmuState

	// 弹幕设置（baseline 的 dtv_danmu_preferences_v1）
	DanmuSettingsOpen bool
	DanmuFont         float32
	DanmuDur          time.Duration
	DanmuArea         float32
	DanmuOpacity      float32
	DanmuColorful     bool
	DanmuColor        ui.Color
	DanmuStroke       ui.Color

	// 弹幕屏蔽词（控制条漏斗面板）：命中的弹幕不上屏、也不进右侧历史。
	// 面板只有点击才开，所以不需要 hoverClose。
	DanmuFilters      []string
	DanmuFilterOpen   bool
	DanmuFilterQuery  string
	danmuFilterScroll ui.ScrollState

	// 右侧弹幕列表（baseline 的 DanmuPanel）
	DanmuPanelOn bool
	DanmuPanelW  float32
	PanelScroll  ui.ScrollState
	panelSeen    int
	panelPending int
	panelStick   bool
	panelDragW   float32
	panelDragX   float32

	// 配色：当前配色 ID、配色面板是否展开，以及夜/日/跟随系统的选择。
	// ThemeMode 取 "system"（默认）/ "dark" / "light"：它决定用哪一侧的配色，
	// PaletteID 决定用哪个色系，两者一起确定最终配色。
	PaletteID   string
	PaletteOpen bool
	ThemeMode   string
	// systemDark 是最近一帧读到的系统外观，供「跟随系统」按钮当场使用，
	// 不用等下一帧重新读窗口主题。
	systemDark bool

	// 登录：顶栏右上角的头像入口。
	//
	// 账号按平台隔离——B 站的号拿不到斗鱼的清晰度，切平台就该换个人，
	// 所以这里不存一个「全局登录态」，而是每次按当前平台去查。
	AuthOpen bool
	// authLogged 记住各平台上一次看到的登录状态，用来判断「刚登录成功」。
	authLogged map[string]bool
	// authQR / authQRSeq 是按平台缓存的二维码纹理：换码才重新解码。
	authQR    map[string]*ui.Bitmap
	authQRSeq map[string]int

	// 「全部关注」页是一等页面（顶栏 tab 组第一格），和首页 / 订阅页同级。
	// 这三个字段是它自己的筛选状态，切走再回来还留着。
	FollowsQuery  string
	FollowsSearch ui.Handle
	// 关注页里的平台筛选：空串 = 全部，否则是平台 id
	FollowsPlatform string

	// 顶栏搜索浮层：输入、远端结果与请求状态
	SearchOpen     bool
	SearchQuery    string
	SearchHit      int
	SearchResults  []Streamer
	SearchLoading  bool
	SearchErr      string
	searchHitQuery string
	// 已经发起过请求的查询词与平台：view 每帧重建，靠这两个值判断要不要重新调度
	searchQuery    string
	searchPlatform string
	// 请求代次：连续输入时只让最后一次的结果上屏
	searchSeq atomic.Int64

	// 列表滚动：列表本身是虚拟化的 ui.List，滚动位置由 ListState 自己保持
	//（同一个分区来回进出、从播放页返回都还在原位）。
	// scrollKey 只用来判断「换了分区」，换分区要回到顶部。
	HomeList  ui.ListState
	SubList   ui.ListState
	scrollKey string
	// 上一帧真正构建了多少张卡片：虚拟化是否生效的回归指标（测试用）
	builtCards int

	// 卡片入场动画：切平台 / 分区时重置；EnterSeq 让动画 key 换代，从头重播
	CardsIn  bool
	EnterSeq int

	// 最近一次网格布局：列数与卡片宽度。启动层判断「首屏封面是否到齐」要用
	gridCols  int
	gridCardW float32

	// 播放页：静音前的音量、全屏退出时刻（防抖）、快捷键说明浮层
	lastEscape         time.Time // 上一次处理 Esc 的时刻，同一次按键只认一次
	volumeBeforeMute   float32
	lastFullscreenExit time.Time
	ShortcutsOpen      bool

	// 顶栏「数据同步」面板：导出 / 导入两个页签。
	// SyncBusy 期间按钮不响应，SyncNotice / SyncError 是上一次动作的反馈。
	SyncOpen   bool
	SyncTab    string // "export" / "import"
	SyncBusy   bool
	SyncNotice string
	SyncError  string

	// 播放页悬浮 chrome（顶栏 / 控制条 / 窗口按钮）的自动隐藏，
	// baseline 的 chromeHidden：指针静止 2.8 秒后淡出，一动就回来。
	ChromeHidden  bool
	chromeIdleAt  time.Time // 空闲计时的起点：指针最后一次活动的时刻
	chromePtrX    float32
	chromePtrY    float32
	chromePtrOver bool // 指针是否还停在画面上
	chromeFull    bool // 上一帧是否处于全屏：进出全屏要先把 chrome 亮出来

	// 启动层：冷启动到首屏数据就绪之间盖一层（baseline 的 #dtv-splash）
	Splash       bool
	splashFading bool
	splashAt     time.Time
	// 截图验收用：启动层不自动淡出
	SplashHold bool

	// 顶栏平台顺序（拖拽排序与持久化共用）
	PlatformOrder []string

	// 状态落盘：300ms 内的连续变更合并成一次写盘
	stateDirty       bool
	stateSavePending bool

	// 关注（键是 platform|name）
	Follows []FollowRecord
	// 关注状态刷新的闸门：自动刷新和手动刷新共用，避免两轮请求叠在一起
	followsBusy bool
	// 刷新进度：面板按钮上的圆环按 done/total 画弧（baseline 的 refreshProgress）。
	// 每查完一条就推进一格，所以环是连续往前走的，不是等整批结束才跳。
	followRefreshDone  int
	followRefreshTotal int

	// Toast：右下角一条短提示，3.2s 后自动消失（baseline 的 ToastProvider）。
	// Tone 决定左侧图标与颜色；Seq 每条自增，用来换代播入场动画。
	ToastText string
	ToastTone string
	ToastSeq  int

	// 列表加载态：数据层接上之前由 main.go 的拉取结果驱动
	Loading bool
	ErrText string
}

// CategoryGroup 是一级分区及其下的二级分区。
type CategoryGroup struct {
	ID          string
	Name        string
	Categories  []string
	CategoryIDs []string // 与 Categories 平行；真实数据模式下用于请求接口
}

// newApp 只造配置默认值：路由、配色、音量、弹幕参数。不带任何直播数据。
//
// 生产（NewApp）和测试（NewMockApp）都从这里起步，区别只在于要不要挂示例
// 数据。这样生产路径在结构上就够不着 mock——不是「造完再逐项清空」，那种
// 写法每加一个数据字段就要多记一次清理，漏一次就漏进真实界面。
func newApp() *App {
	a := &App{
		Platform:      "douyu",
		PlatformOrder: normalizePlatformOrder(nil),
		Follows:       []FollowRecord{},
		// 首帧网格还没量过，先用最小卡片宽占位：预取封面时不能是 0，
		// 0 在图片缓存里表示「按原图解码」。
		gridCardW: theme.CardMinWidth,
		Router:    ui.NewRouter(routeHome),
		PaletteID: "signal",
		ThemeMode: themeModeSystem,
		Volume:    0.6,
		Playing:   true,
		// 清晰度 / 线路都只能来自平台返回值，本地不留默认：B 站没有叫
		// 「原画」的档位（它是「1080P 原画」），虎牙抖音的档位名又各不相同。
		// 空串表示「还不知道是哪一档」，取到档位后由 player_media 校正成第一档。
		Quality:       "",
		Line:          "",
		DanmuOn:       true,
		DanmuFont:     20,
		DanmuDur:      10 * time.Second,
		DanmuArea:     0.5,
		DanmuOpacity:  1,
		DanmuColorful: true,
		DanmuColor:    ui.RGB(255, 255, 255),
		DanmuStroke:   ui.RGB(68, 68, 68),
		DanmuPanelOn:  true,
		DanmuPanelW:   196,
		SubCatIndex:   -1,

		GroupsByPlatform: map[string][]CategoryGroup{},
		liveLoaded:       map[string]bool{},
		feedCache:        map[string]feedEntry{},
		subCategories:    map[string][]string{},
	}
	// 顶栏这些 tab 是同一层级的横向页面，没有谁是「进入谁的下一层」。
	// Router 按路径层级决定往哪边滑，在这套结构上只会滑出三种不一样的结果，
	// 所以干脆关掉页面过渡：切过去就是切过去，动的只有那颗滑块。
	a.Router.Transition = ui.TransitionNone
	return a
}

// NewApp 是应用本体：分区与直播列表都来自 internal/live。
func NewApp() *App {
	a := newApp()
	a.LiveMode = true
	// 目前只有 B 站接入了真实数据，默认落在这里。
	a.Platform = "bilibili"
	a.restore()
	// 冷启动盖一层启动画面，首屏数据到了再淡出
	a.Splash = true
	a.splashAt = time.Now()
	a.bindAuth()
	return a
}

// NewMockApp 造一份带示例数据的 App，供视图测试与截图验收使用。
//
// 列表内容来自 mock.go。真实接口可能为空、可能不够铺满一屏、封面还要联网
// 下载，靠它没法稳定断言布局——这正是 mock 层存在的理由。名字里带 Mock，
// 是为了在任何调用点都一眼看出它不该出现在应用里。
func NewMockApp() *App {
	a := newApp()
	a.Streamers = mockStreamers()
	a.Groups = mockGroups()
	a.subCategories = mockSubCategories
	a.restore()
	return a
}

// bindAuth 给每个支持扫码登录的平台各挂一个状态监听。
//
// 不是一个「全局登录」：每个平台一份会话、一份监听，互不影响。
func (a *App) bindAuth() {
	a.authLogged = map[string]bool{}
	a.authQR = map[string]*ui.Bitmap{}
	a.authQRSeq = map[string]int{}
	for _, p := range []live.Platform{live.Bilibili, live.Douyu, live.Huya, live.Douyin} {
		auth, ok := live.AuthFor(p)
		if !ok {
			continue
		}
		a.bindPlatformAuth(p, auth)
	}
}

// bindPlatformAuth 给单个平台挂监听。
//
// 参数单独传而不是直接用循环变量：监听是稍后才触发的，闭包共享循环变量
// 会让所有平台都指向最后一个。
func (a *App) bindPlatformAuth(p live.Platform, auth live.Authenticator) {
	key := string(p)
	a.authLogged[key] = auth.Auth().LoggedIn
	auth.SetAuthListener(func() {
		a.onAuthChange(p, auth)
	})
}

// onAuthChange 在某个平台的登录态变化时重画头像；刚登录成功还要重开流。
//
// 回调在后台 goroutine 上（扫码轮询），必须切回主线程再碰视图状态。
// 登录与否会改变服务端下发的清晰度档位，不重取会一直停在匿名那一档，
// 用户会以为「登录了没用」。
func (a *App) onAuthChange(p live.Platform, auth live.Authenticator) {
	if a.Win == nil {
		return
	}
	key := string(p)
	logged := auth.Auth().LoggedIn
	a.Win.Update(func() {
		if a.authLogged[key] == logged {
			return
		}
		a.authLogged[key] = logged
		if logged && a.media.roomKey != "" && a.current().Platform == key {
			a.startStream(true)
		}
	})
}

// currentAuth 返回当前平台的登录入口；该平台不支持登录时返回 nil。
func (a *App) currentAuth() live.Authenticator {
	auth, ok := live.AuthFor(live.Platform(a.Platform))
	if !ok {
		return nil
	}
	return auth
}

// Shell 是窗口的根视图：地面 + 主区（顶栏 + 内容）+ 悬浮灵动岛。
//
// baseline 的背景只有两层——实色地面和一层极淡的胶片颗粒。颗粒是 SVG
// feTurbulence 平铺、opacity 5%，原生侧暂时省掉（视觉上几乎不可见，
// 而且每帧重绘一张噪点纹理不划算）。
func (a *App) Shell(c *ui.Context) {
	// 一帧只让第一个 spinner 驱动动画时钟，其余的读它的相位
	resetSpinFrame()

	// 系统外观要在 SetTheme 之前读：SetTheme 之后 Context 返回的是我们这套
	// 配色，就再也看不到系统是深色还是浅色了。
	a.syncThemeMode(c.Theme().Dark)
	// 每帧开头按当前配色重写包级颜色，之后所有绘制读到的就是这套色
	theme.Apply(a.PaletteID)
	c.SetTheme(theme.Theme())

	// baseline：Ctrl/Cmd + K 唤起搜索；输入框里打字时不抢
	// 没有平台上下文的页面连搜索入口都没有，快捷键也不该把它打开。
	if a.searchAvailable() && !c.FocusTakesText() && c.Shortcut(ui.Cmd, ui.KeyK) {
		a.SearchOpen = true
	}

	// 离开播放页先把窗口级和界面级的状态收干净。统一放在这里而不是各个
	// 返回按钮上：离开播放页的路不止一条（左上返回钮、Esc、顶栏 tab），
	// 漏一条就是窗口卡死（见 leavePlayer）。
	if a.Router.Path() != routePlayer {
		a.leavePlayer()
	}

	// 播放页的两种全屏下应用外壳整体让位给画面（baseline 的 playerShell 是
	// position:fixed inset:0 z-index:9999，顶栏和灵动岛都被盖住）。
	// 窗口全屏是我们自己的状态，原生全屏是系统状态，两者都算。
	fullscreen := a.playerFullscreen()

	ui.Box(c).Fill().Background(theme.BgPrimary).Children(func() {
		ui.Column(c).Fill().Children(func() {
			if !fullscreen {
				a.Navbar(c)
			}
			a.Router.View(c, func(r *ui.Route) {
				switch {
				case r.Match(routeFollows):
					a.FollowsPage(c)
				case r.Match(routeSubscribe):
					a.SubscribePage(c)
				case r.Match(routePlayer):
					a.PlayerPage(c)
				default:
					a.Home(c)
				}
			})
			// 离开播放页就把 ffmpeg / 播放器 / 弹幕连接一起收掉，
			// 否则换房间回来时会有两路流同时占带宽。
			if a.Router.Path() != routePlayer && a.media.roomKey != "" {
				a.closeMedia()
			}
		})
		// 灵动岛浮在内容之上。两种全屏下整块不画：画面上只留返回钮和控制条，
		// 岛连同它那圈流光和投影压在满屏画面上纯属干扰。
		//
		// 但观看时长照常累计。全屏时没人调 tickWatch，退出全屏那一次
		// now−watchAt 会把全屏这一段整段补进时长里——中间暂停过的时间也会
		// 被算进去，所以这里补上这一次调用。
		if fullscreen {
			a.tickWatch(c.Now())
		} else {
			a.Island(c)
		}
		// Toast 永远在最上层（数据同步面板是模态，压在它下面一次即可）
		if a.SyncOpen {
			a.syncOverlay(c)
		}
		if a.ToastText != "" {
			a.toastView(c)
		}
		// 播放页的快捷键说明
		if a.ShortcutsOpen {
			a.shortcutsOverlay(c)
		}
		// 冷启动的启动层盖住一切，包括浮层
		if a.Splash {
			a.splash(c)
		}
	})
	// 全局键盘放在整棵视图构建完之后：PopoverBase 的 Esc 是在构建浮层时
	// 消费的，这里如果先跑，一次 Esc 会同时关掉浮层和它外面的东西。
	a.shellKeys(c)
}

// Home 是首页内容：分区栏 + 卡片网格。
//
// baseline 的 appBody 是 padding 0 14，分区栏自己带 margin-bottom 14，
// 列表区不再额外留间距。
func (a *App) Home(c *ui.Context) {
	if a.LiveMode {
		a.ensureLiveData()
	}
	pad := a.bodyPadX(c)
	// 换分区就回到顶部；同一个分区内（去播放页再回来）位置由 ListState 自己记住。
	key := fmt.Sprintf("%s|%d|%d|%d", a.Platform, a.GroupIndex, a.CategoryIndex, a.SubCatIndex)
	if a.scrollKey != key {
		a.scrollKey = key
		a.HomeList.ScrollTo(0, ui.Start)
	}
	ui.Column(c).Grow(1).Padding(0, pad, 0, pad).Gap(14).Children(func() {
		// 分区树还没到时给一块同高的占位，避免列表区先铺满、再被分区栏顶下去。
		if a.LiveMode && len(a.Groups) == 0 && a.ListLoading {
			a.categoryBarSkeleton(c)
		} else {
			a.categoryBar(c)
		}
		if a.ErrText != "" {
			a.errorBar(c)
		}
		a.grid(c, a.feed(), &a.HomeList, a.loadMoreRooms)
	})
}

// categoryBarSkeleton 是分区栏的加载占位：和真面板同高、同圆角。
func (a *App) categoryBarSkeleton(c *ui.Context) {
	ui.Row(c).Height(52).Padding(0, 16, 0, 16).Radius(theme.RadiusXL).
		Border(1, theme.Border).Background(theme.BgSecondary).Gap(10).
		AlignItems(ui.Center).Children(func() {
		ui.Text(c, "正在加载分区…").FontSize(theme.TextSM).
			TextColor(theme.TextTertiary)
	})
}

// resetCards 让列表的入场动画从头播一遍：
// 切平台、切分区、切订阅都算换了一份新列表，baseline 也是重新挂载卡片。
func (a *App) resetCards() {
	a.CardsIn = false
	a.EnterSeq++
}

// bodyPadX 是正文左右 padding：baseline 常态 14，1240 以下收到 10。
func (a *App) bodyPadX(c *ui.Context) float32 {
	w, _ := c.Size()
	if w <= 1240 {
		return 10
	}
	return 14
}

// errorBar 是加载失败的提示条。
//
// baseline：padding 10 14、圆角 12、background color-mix(danger 8%, bg-secondary)，
// 右侧一枚 30 高的重试按钮。
func (a *App) errorBar(c *ui.Context) {
	ui.Row(c).Padding(10, 14, 10, 14).Gap(12).Radius(theme.RadiusMD).
		AlignItems(ui.Center).Background(theme.BgSecondary.Mix(theme.Danger, 0.08)).
		Children(func() {
			ui.Text(c, a.ErrText).FontSize(theme.TextSM).TextColor(theme.TextPrimary).Grow(1)
			retry := ui.Row(c).Height(30).Padding(0, 14, 0, 14).Gap(6).
				Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
				Background(theme.FillTertiary)
			if retry.Hovered() {
				retry.Background(theme.FillSecondary)
			}
			if retry.Clicked() {
				a.ErrText = ""
				if a.LiveMode {
					a.loadRooms()
				} else {
					a.Loading = true
				}
			}
			retry.Children(func() {
				ui.Icon(c, iconSync).FontSize(12).TextColor(theme.Danger)
				ui.Text(c, "重试").FontSize(theme.TextXS).FontWeight(600).
					TextColor(theme.Danger)
			})
		})
}

// feed 返回首页当前应该显示的直播间。
//
// 数据层接上之前按「平台 + 一级/二级/三级分区」从示例池里筛；B 站的远程
// 列表由接口按推荐位返回，不再本地过滤。
func (a *App) feed() []Streamer {
	if a.LiveMode {
		return a.Rooms
	}
	if a.Platform == "bilibili" && len(a.Remote) > 0 {
		return a.Remote
	}
	return a.filterStreamers(a.Platform, a.currentCategory(), a.currentSub())
}

// feedFor 返回订阅页某个订阅分区下的直播间。
func (a *App) feedFor(sub Subscription) []Streamer {
	if sub.PlatformID == "bilibili" && len(a.Remote) > 0 {
		return a.Remote
	}
	return a.filterStreamers(sub.PlatformID, sub.Name, "")
}

func (a *App) filterStreamers(platform, category, sub string) []Streamer {
	out := make([]Streamer, 0, 16)
	for _, s := range a.Streamers {
		switch {
		case s.Platform != platform:
		case category != "" && s.Category != category:
		case sub != "" && s.Sub != sub:
		default:
			out = append(out, s)
		}
	}
	return out
}

// grid 铺卡片网格。
//
// baseline 是 `grid-template-columns: repeat(auto-fill, minmax(220px, 1fr))`：
// 列数随可用宽度自动增减、每列等宽、内容区始终满宽。
// 这里先按同一条公式自己算出列数与卡片宽度，再用 Grid 的固定列数落下——
// GridView 内部还叠了一层自己的取整，会少一列、右侧留白。
//
// 列表交给 ui.List 做虚拟化：一次只构建可视范围内的那几行卡片。
// 滑到第 3000 张和第 30 张的每帧成本一样——这是「一直往下滑」的前提。
// 行高由 List 自己测量（我们的卡片高度是算出来的固定值），不用手动喂。
func (a *App) grid(c *ui.Context, list []Streamer, ls *ui.ListState, onMore func()) {
	w, _ := c.Size()
	// appBody 的左右 padding，再减掉 baseline 里常驻滚动条占的 10px：
	// 列宽按 baseline 的可用宽度算，滚动条也不会压在最后一列上。
	avail := w - 2*a.bodyPadX(c) - theme.ScrollbarW
	if avail < theme.CardMinWidth {
		avail = theme.CardMinWidth
	}

	cols := int((avail + theme.GridGap) / (theme.CardMinWidth + theme.GridGap))
	if cols < 1 {
		cols = 1
	}
	cardW := (avail - theme.GridGap*float32(cols-1)) / float32(cols)
	a.gridCols, a.gridCardW = cols, cardW

	// 空列表与骨架不需要虚拟化：它们只有十来个元素。
	if len(list) == 0 {
		ui.Scroll(c).Grow(1).Children(func() {
			if a.ListLoading {
				a.skeletonGrid(c, avail, cols, cardW)
			} else {
				a.emptyGrid(c)
			}
		})
		return
	}

	// 入场动画的代次：切平台 / 分区时 resetCards 会换代，这里重新从 0 播
	enter := EnterAnim{Key: fmt.Sprintf("card-in-%d", a.EnterSeq), To: 1}
	if !a.CardsIn {
		a.CardsIn = true
		enter.To = 0
	}

	rows := (len(list) + cols - 1) / cols

	// 行数 + 1：最后一行是页脚（「正在加载更多…」/「继续下滑加载更多」/「已显示最近 N 条」），
	// 它跟着内容一起滚，位置和 baseline 一样在列表末尾。
	ui.List(c, ls, rows+1, func(r int) {
		if r == rows {
			a.gridFooter(c, avail, len(list))
			return
		}
		ui.Row(c).Gap(theme.GridGap).Children(func() {
			for i := r * cols; i < min(len(list), (r+1)*cols); i++ {
				i := i
				st := list[i]
				in := enter
				// baseline 只给前 6 张错峰；这里也只让首屏可见的卡参与入场，
				// 一屏之外的行 List 根本不会构建。
				if i >= 12 {
					in = EnterAnim{}
				} else if i > 0 && i < 6 {
					in.Delay = time.Duration(i) * 20 * time.Millisecond
				}
				a.builtCards++
				Card(c, st, cardW, a.isFollowed(st), in, func() {
					a.Player = st
					a.Router.Push(routePlayer)
				}, func() {
					a.toggleFollow(st)
				})
			}
			// 最后一行不足 cols 时补空位，卡片宽度才不会被拉宽
			if r == rows-1 {
				for i := len(list); i < (r+1)*cols; i++ {
					ui.Box(c).Width(cardW)
				}
			}
		})
	}).Gap(theme.GridGap).Grow(1)

	first, last := ls.Visible()
	if last >= first {
		// 预取下面两行的图：滚到那一行时已经在路上了，不会滑一屏空一屏。
		// 同一个 URL 在缓存里有去重，这里每帧重复调用只是几次 map 查找。
		for i := (last + 1) * cols; i < min(len(list), (last+3)*cols); i++ {
			media.Default.Prefetch(cardW, list[i].CoverURL)
			media.Default.Prefetch(32, list[i].AvatarURL)
		}
	}

	// 触底补页：还剩两行就拉下一页。List 的 Visible 是上一帧构建过的行范围，
	// 本来就是虚拟化的产物，不用自己去量滚动距离。
	if onMore != nil && a.LiveMode && last >= rows-2 {
		onMore()
	}
}

// skeletonGrid 是首屏骨架：12 张卡片，封面 16:9，信息条与真卡片同尺寸。
func (a *App) skeletonGrid(c *ui.Context, avail float32, cols int, cardW float32) {
	ui.Grid(c).Width(avail).Columns(cols).Gap(theme.GridGap).Children(func() {
		for i := 0; i < 12; i++ {
			sk := ui.Column(c).Width(cardW).Radius(theme.RadiusMD).
				Background(theme.BgSecondary)
			// baseline 的 skeleton-pulse：1.6s ease-in-out 循环，opacity 0.55 ↔ 1；
			// reduced motion 下 animation: none、固定 0.8。
			if c.Preferences().ReduceMotion {
				sk.Opacity(0.8)
			} else {
				// Loop 驱动三角波：0 → 1 → 0，两端套 ease-in-out。
				p := sk.Loop("pulse", 1600*time.Millisecond, ui.EaseInOut)
				k := 1 - abs32(2*p-1)
				sk.Opacity(0.55 + 0.45*k)
			}
			sk.Children(func() {
				ui.Box(c).Width(cardW).Height(cardW * 9 / 16).Background(theme.FillTertiary)
				ui.Row(c).Padding(13, 12, 15, 12).Gap(10).Children(func() {
					ui.Box(c).Size(34, 34).Radius(17).Background(theme.FillTertiary)
					ui.Column(c).Grow(1).Gap(7).Children(func() {
						ui.Box(c).Height(12).Width(cardW * 0.55).Radius(6).
							Background(theme.FillTertiary)
						ui.Box(c).Height(10).Width(cardW * 0.80).Radius(6).
							Background(theme.FillTertiary)
					})
				})
			})
		}
	})
}

// emptyGrid 是列表的空态：标题 + 提示 + 两个按钮。
func (a *App) emptyGrid(c *ui.Context) {
	hint := "换个分区看看，或者稍后重试。"
	if a.LiveMode {
		if _, ok := live.ProviderFor(live.Platform(a.Platform)); !ok {
			hint = "该平台暂未接入真实数据。"
		}
	}
	ui.Column(c).Padding(88, 16, 88, 16).Gap(8).Center().Children(func() {
		ui.Text(c, "这里还没有直播间").FontSize(theme.TextMD).FontWeight(600).
			TextColor(theme.TextSecondary)
		ui.Text(c, hint).FontSize(theme.TextSM).
			TextColor(theme.TextTertiary)
		ui.Row(c).Gap(10).Padding(10, 0, 0, 0).Children(func() {
			clear := ui.Row(c).Height(34).Padding(0, 16, 0, 16).Gap(6).
				Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
				Background(theme.Accent)
			if clear.Hovered() {
				clear.Background(theme.AccentHover)
			}
			if clear.Clicked() {
				a.SubCatIndex = -1
			}
			clear.Children(func() {
				ui.Text(c, "查看全部分区").FontSize(theme.TextSM).FontWeight(600).
					TextColor(theme.AccentText)
			})

			retry := ui.Row(c).Height(34).Padding(0, 16, 0, 16).Gap(6).
				Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).
				Background(theme.FillTertiary)
			if retry.Hovered() {
				retry.Background(theme.FillSecondary)
			}
			if retry.Clicked() {
				a.ErrText = ""
				if a.LiveMode {
					a.loadRooms()
				} else {
					a.Loading = true
				}
			}
			retry.Children(func() {
				ui.Text(c, "重新加载").FontSize(theme.TextSM).FontWeight(600).
					TextColor(theme.TextSecondary)
			})
		})
	})
}

// gridFooter 是列表底部那行状态：加载中 / 已显示最近 N 条。
func (a *App) gridFooter(c *ui.Context, avail float32, n int) {
	ui.Row(c).Width(avail).Height(52).Padding(4, 0, 18, 0).Center().
		Children(func() {
			// baseline 的三态：加载中 / 还有下一页 / 到底了
			switch {
			case a.ListLoadingMore:
				ui.Row(c).Gap(8).Center().Children(func() {
					spinner(c, 14)
					ui.Text(c, "正在加载更多…").FontSize(theme.TextSM).
						TextColor(theme.TextSecondary)
				})
			case a.ListHasMore:
				ui.Text(c, "继续下滑加载更多").FontSize(theme.TextXS).
					LetterSpacing(0.02).TextColor(theme.TextTertiary)
			default:
				ui.Text(c, fmt.Sprintf("已显示最近 %d 条", n)).FontSize(theme.TextXS).
					LetterSpacing(0.02).TextColor(theme.TextTertiary)
			}
		})
}

// ============================================================ 顶栏

// Navbar 是自绘标题栏。
//
// baseline：整条高 64、左右 padding 14、gap 14、背景就是 bg-primary；
// 左侧平台 tablist，右侧一组 actions（gap 10）：
// 搜索按钮 → 工具组 → 1px 分隔线 → 窗口按钮。
// 工具组把「同步 / 配色」两颗 38×38 的图标按钮包进一层 fill-quaternary 的胶囊里
// （gap 4 + padding 3），和窗口按钮组是同一套语言。
func (a *App) Navbar(c *ui.Context) {
	pad, gap := float32(14), float32(14)
	if a.narrow(c) {
		// baseline ≤1240：gap 8、左右 padding 10
		pad, gap = 10, 8
	}
	bar := ui.Row(c).Height(theme.NavbarHeight).Padding(0, pad, 0, pad).Gap(gap).
		AlignItems(ui.Center).Background(theme.BgPrimary)
	// frameless 窗口靠这里拖动；双击空白处最大化
	bar.DragWindow()

	bar.Children(func() {
		a.platformTabs(c)

		ui.Box(c).Grow(1) // 中间弹性空白

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			// baseline 的 toolGroup：搜索、同步、配色三颗按钮在同一个胶囊里；
			// 值得搜索的只有某个平台的直播列表：订阅页、全部关注页都没有平台上下文，
			// 全部关注页自带页内搜索，那里不摆搜索按钮。
			ui.Row(c).Gap(4).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
				Background(theme.FillQuaternary).Children(func() {
				if a.searchAvailable() {
					a.searchButton(c)
				}
				a.iconButton(c, iconMonitorSmartphone, "数据同步", a.openSyncPanel)
				a.paletteButton(c)
				// 登录头像和上面三颗在同一个胶囊里：顶栏右侧本来就是一组
				// 工具，单独拎出去会多出一层视觉噪音。
				a.authButton(c)
			})

			ui.Box(c).Width(1).Height(24).Background(theme.Separator)

			a.windowControls(c)
		})
	})
}

// toast 弹一条右下角提示（baseline 的默认色调是 info），3.2 秒后由后台清掉。
func (a *App) toast(msg string) { a.toastTone(msg, toastInfo) }

// toastTone 是带色调的 toast：success / info / error 决定图标和图标颜色。
func (a *App) toastTone(msg, tone string) {
	a.ToastText = msg
	a.ToastTone = tone
	// 每条新提示换一次序号，入场动画的 key 跟着换代，才会重新播
	a.ToastSeq++
	if a.Win == nil {
		return
	}
	win := a.Win
	time.AfterFunc(3200*time.Millisecond, func() {
		win.Update(func() {
			if a.ToastText == msg {
				a.ToastText = ""
			}
		})
	})
}

// toast 的三种色调，取值与 baseline 的 ToastTone 一致。
const (
	toastSuccess = "success"
	toastInfo    = "info"
	toastError   = "error"
)

// toastIcon 是每种色调左侧的那枚图标，对应 baseline 的 TONE_ICON。
func toastIcon(tone string) *ui.SVG {
	switch tone {
	case toastSuccess:
		return iconCheck
	case toastError:
		return iconTriangleAlert
	}
	return iconAlert
}

// toastIconColor 是图标颜色：success / danger / text-tertiary，
// 和 baseline ToastProvider.module.css 里 .icon 的三条规则一致。
func toastIconColor(tone string) ui.Color {
	switch tone {
	case toastSuccess:
		return theme.Success
	case toastError:
		return theme.Danger
	}
	return theme.TextTertiary
}

// toastView 是右下角那条提示。
//
// baseline ToastProvider：right/bottom 18、min-height 38、padding 7 8 7 12、
// 圆角 12、bg-elevated + 描边 + popover 阴影；左侧 15px 图标，间距 9；
// 入场是 opacity 0→1、translateY 8→0，220ms ease-out（reduced motion 下不播）。
func (a *App) toastView(c *ui.Context) {
	w, _ := c.Size()
	maxW := w - 36
	if maxW > 420 {
		maxW = 420
	}
	box := ui.Box(c).Absolute().Right(18).Bottom(18).MaxWidth(maxW).
		MinHeight(38).Padding(7, 8, 7, 12).Radius(theme.RadiusMD).
		Background(theme.BgElevated).Border(1, theme.Border).
		Shadow(0, 22, 56, -22, theme.ShadowPopover)

	if !c.Preferences().ReduceMotion {
		in := box.Keyframes(fmt.Sprintf("toast-in-%d", a.ToastSeq), []ui.Keyframe{
			{Value: 0},
			{At: 220 * time.Millisecond, Value: 1, Ease: ui.EaseOut},
		}, ui.KeyframeOptions{})
		box.Opacity(in).Translate(0, (1-in)*8)
	}

	box.Children(func() {
		ui.Row(c).Gap(9).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, toastIcon(a.ToastTone)).FontSize(15).
				TextColor(toastIconColor(a.ToastTone))
			ui.Text(c, a.ToastText).FontSize(theme.TextSM).
				TextColor(theme.TextPrimary).SingleLine()
		})
	})
}

// narrow 报告窗口是否落在 baseline 的 ≤1240 断点里。
func (a *App) narrow(c *ui.Context) bool {
	w, _ := c.Size()
	return w <= 1240
}

// activeTab 返回顶栏该高亮的 tab。
//
// 「订阅」和「全部关注」不是平台，但和平台同级：在对应页面时高亮它们，
// 其余按当前平台。
func (a *App) activeTab() string {
	switch a.Router.Path() {
	case routeFollows:
		return "follows"
	case routeSubscribe:
		return "subscribe"
	case routePlayer:
		// 播放页看的是某一个具体主播，顶栏必须跟着主播所属平台走。
		// 读 a.Platform 会停在进播放页之前那个列表的平台：从订阅页点开
		// 别家主播、或者从搜索里跳进来时，高亮就留在上一家。
		if p := a.current().Platform; p != "" {
			return p
		}
	}
	return a.Platform
}

// switchTab 处理 tab 点击。
func (a *App) switchTab(id string) {
	switch id {
	case "follows":
		a.Router.Push(routeFollows)
		return
	case "subscribe":
		a.resetCards()
		a.Router.Push(routeSubscribe)
		return
	}
	a.Platform = id
	// 账号是分平台的，头像浮层里的内容会跟着换人；换 tab 就关掉，
	// 别让上一家的二维码和昵称留在屏幕上。
	a.AuthOpen = false
	// 换平台了，旧平台的纹理对新平台没有任何用处：立刻清掉 L1。
	// 但要保住新平台已经预取好的首屏封面，否则用户还是要等一遍解码。
	// 原始字节（L2）全部留着——切回去时能直接解码，不用重新下载。
	media.Default.ReleaseTexturesExcept(a.feedURLs(id))
	// baseline：切平台清空搜索，上一个平台的结果不能留在浮层里。
	// 代次 +1 让还在路上的那次请求作废。
	a.SearchQuery, a.SearchResults, a.SearchErr, a.SearchLoading = "", nil, "", false
	a.SearchHit = 0
	a.searchSeq.Add(1)
	a.Router.Push(routeHome)
	a.onPlatformChanged()
}

// platformTabs 是顶栏左侧的 tablist：关注 + 订阅 + 四个平台。
//
// baseline 用一条**绝对定位的 accent 滑块**在 tab 之间滑过去（220ms），
// 而不是给选中的 tab 换底色——切换时是「光块移动」，不是「色块跳变」。
// tab 等宽（每列至少 64px，中文两字 + 28 padding 正好 64），
// 所以滑块位置直接按序号乘宽度算，不用量 DOM。
func (a *App) platformTabs(c *ui.Context) {
	// 顺序读 a.PlatformOrder：baseline 的 tab 顺序可拖拽重排并持久化。
	// 前两格固定：「全部关注」和「订阅」——它们不是平台，但和平台同级。
	type tabItem struct{ id, label string }
	tabs := []tabItem{{"follows", ""}, {"subscribe", ""}}
	for _, id := range a.PlatformOrder {
		tabs = append(tabs, tabItem{id, platformLabel(id)})
	}

	tabW := float32(64)
	tabH := float32(34)
	if a.narrow(c) {
		// baseline ≤1240：列宽 56、tab 高 36
		tabW, tabH = 56, 36
	}

	// 滑块跟着当前页面（关注 / 订阅 / 平台）走。
	selected := 0
	active := a.activeTab()
	for i, t := range tabs {
		if t.id == active {
			selected = i
			break
		}
	}

	track := ui.Row(c).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
		Background(theme.FillQuaternary)
	if !theme.Current().Dark {
		// baseline 的 [data-theme="light"] .track：白底 + 1px 描边
		track.Background(ui.Hex("#ffffff")).Border(1, theme.Border)
	}

	track.Children(func() {
		list := ui.Box(c).Width(tabW * float32(len(tabs))).Height(tabH)
		list.Children(func() {
			// 滑块先画，压在 tab 下面
			slider := ui.Box(c).Absolute().Top(0).Width(tabW).Height(tabH).
				Radius(theme.RadiusFull).Background(theme.Accent)
			x := slider.AnimateTo("tab-x", float32(selected)*tabW, theme.EaseOut(220*time.Millisecond))
			slider.Left(x)

			// tab 行要压在滑块上面：绝对定位的滑块默认在普通子元素之后绘制，
			// 不抬层级会把选中的 tab 文字整块盖住（截图里只剩一块青柠胶囊）。
			ui.Row(c).Fill().ZIndex(1).Children(func() {
				for i, t := range tabs {
					t, idx := t, i

					// 「全部关注」和其他 tab 一样是页面入口，点了切路由
					tab := ui.Box(c).Width(tabW).Height(tabH).Radius(theme.RadiusFull).
						Center().Cursor(ui.CursorPointer)
					if t.id == "follows" {
						tab.Label("全部关注")
					}
					if tab.Clicked() {
						a.switchTab(t.id)
					}
					// baseline：平台 tab 可拖拽重排（关注与订阅不参与）。
					// 拖起来的那枚 dim 到 0.35，经过的目标描一圈 accent 65% 的内阴影。
					if t.id != "subscribe" && t.id != "follows" {
						tab.Drag(t.id)
						if _, over := ui.DragOver[string](tab); over {
							tab.InsetShadow(0, 0, 0, 1, theme.Accent.Mix(ui.Transparent, 0.35))
						}
						if dragged, dropped := ui.Drop[string](tab); dropped && dragged != t.id {
							a.movePlatform(dragged, t.id)
						}
						if tab.Dragging() {
							tab.Opacity(0.35)
						}
					}
					tab.Children(func() {
						col := theme.TextTertiary
						if idx == selected {
							col = theme.AccentText
						} else if tab.Hovered() {
							col = theme.TextPrimary
						}

						switch t.id {
						case "follows":
							// 选中时实心星，和滑块一起构成「这里是关注页」
							ico := iconStar
							if idx == selected {
								ico = iconStarFilled
							}
							ui.Icon(c, ico).FontSize(14).TextColor(col)
							return
						case "subscribe":
							ui.Icon(c, iconBookmark).FontSize(14).TextColor(col)
							return
						}
						txt := ui.Text(c, t.label).FontSize(theme.TextSM).TextColor(col)
						if idx == selected {
							txt.FontWeight(600)
						} else {
							txt.FontWeight(500)
						}
					})
				}
			})
		})
	})
}

// iconButton 是工具组里的一枚图标按钮：38×38 圆形（≤1240 时 34×34），图标 18。
func (a *App) iconButton(c *ui.Context, ico *ui.SVG, label string, on func()) {
	size := float32(38)
	if a.narrow(c) {
		size = 34
	}
	btn := ui.Box(c).Size(size, size).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
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
		ui.Icon(c, ico).FontSize(18).TextColor(col)
	})
}

// windowControls 是右上角的最小化 / 最大化 / 关闭。
//
// baseline 是实色胶囊组：外层 3px padding + fill-quaternary 底 + 1px 描边，
// 里面三枚 40×34 的圆角按钮，图标 16px、1.8 描边，关闭键 hover 变红。
func (a *App) windowControls(c *ui.Context) {
	ui.Row(c).Gap(2).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
		Background(theme.FillQuaternary).Border(1, theme.Border).
		Children(func() {
			a.caption(c, iconMinimize, false, func() {
				if a.Win != nil {
					a.Win.Minimize()
				}
			})
			a.caption(c, iconMaximize, false, func() {
				if a.Win != nil {
					a.Win.Maximize()
				}
			})
			a.caption(c, iconClose, true, func() {
				if a.Win != nil {
					a.Win.Close()
				}
			})
		})
}

// caption 是一枚窗口按钮。
func (a *App) caption(c *ui.Context, ico *ui.SVG, danger bool, on func()) {
	w, h := float32(40), float32(34)
	if a.narrow(c) {
		// baseline ≤1240：34×32
		w, h = 34, 32
	}
	btn := ui.Box(c).Size(w, h).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer)
	hovered := btn.Hovered()
	if hovered {
		if danger {
			btn.Background(ui.Hex("#e5344a"))
		} else {
			btn.Background(theme.FillTertiary)
		}
	}
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if hovered {
			if danger {
				col = ui.RGB(255, 255, 255)
			} else {
				col = theme.TextPrimary
			}
		}
		ui.Icon(c, ico).FontSize(16).TextColor(col)
	})
}
