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

	// Router 记着三个页面（首页 / 关注 / 播放）的历史与转场
	Router *ui.Router

	// 订阅分区
	Subs     []Subscription
	SubIndex int
	// 订阅分区的列表：平台和分区可以跟首页不同，所以状态独立一份
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
	WinFull      bool // 窗口全屏：播放页铺满窗口，应用顶栏让位
	// InfoOpen 是播放页灵动岛是否展开。展开态现在常驻（岛同时承担「正在看谁」
	// 和「声音从哪来」），所以这个字段只表示「进过这个房间」，不再有收起。
	// infoRoom 记着当前房间，用来判断是不是换了直播间。
	InfoOpen    bool
	infoRoom    string
	Quality     string
	QualityOpen bool
	Line        string
	LineOpen    bool
	// 这三个浮层是 hover 触发的（onMouseEnter 开、onMouseLeave 排一个 220ms
	// 后关的定时器），
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
	// volumeWheelAccum 是滚轮调音量还没落成 1% 档位的那点余量。
	// 触控板一次只推几 DIP，没有它这些小位移会被取整全吃掉。
	volumeWheelAccum float32

	// 弹幕层：开关、正在飞的弹幕、推动器状态
	DanmuOn bool
	// Danmu 是画面上正在飞的那批，飞完就剪掉。
	// DanmuLog 是右侧面板的历史，收到就留着，和飞行状态无关——
	// 两者本来就是两份数据（messages 留 300，Overlay 只管当帧）。
	Danmu    []danmuLine
	DanmuLog []danmuLine
	danmuSeq int
	danmu    danmuState

	// 弹幕设置（落盘键 dtv_danmu_preferences_v1）
	DanmuSettingsOpen bool
	// 颜色 / 描边的取色浮层各自一个开关。取色器比设置面板大，指针得挪到
	// 面板外面去用，所以设置面板的 hover 关门逻辑要把这两个算进去。
	DanmuColorOpen  bool
	DanmuStrokeOpen bool
	DanmuFont       float32
	DanmuDur        time.Duration
	DanmuArea       float32
	DanmuOpacity    float32
	DanmuColorful   bool
	DanmuColor      ui.Color
	DanmuStroke     ui.Color

	// 弹幕屏蔽词（控制条漏斗面板）：命中的弹幕不上屏、也不进右侧历史。
	// 面板只有点击才开，所以不需要 hoverClose。
	DanmuFilters      []string
	DanmuFilterOpen   bool
	DanmuFilterQuery  string
	danmuFilterScroll ui.ScrollState

	// 右侧弹幕列表
	DanmuPanelOn bool
	DanmuPanelW  float32
	PanelScroll  ui.ScrollState
	panelSeen    int
	panelPending int
	panelStick   bool
	// panelViewH / panelViewFull 记着上一帧列表的视口。开关全屏或拖窗口边缘
	// 时视口高度会变：MaxY 跟着跳，而 Y 还停在旧的底上，跟随判据会当场
	// 失效。变了的这一帧要把 Y 重新钉到末尾。
	panelViewH    float32
	panelViewFull bool
	panelDragW    float32
	panelDragX    float32

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
	// authReturn 记录「为了登录临时离开的直播间」。
	//
	// 虎牙、抖音的登录要占满主区域（平台自己的真网页），播放页没有主区域
	// 可让，所以点账号时先切回平台首页去登，登完（或用户放弃）再自动回到
	// 这个直播间。nil 表示当前没有这趟来回。
	authReturn *Streamer
	// authLogged 记住各平台上一次看到的登录状态，用来判断「刚登录成功」。
	authLogged map[string]bool
	// pendingSwitch 是点了但还没真正切过去的平台。
	//
	// 切平台不立刻换内容：目标平台的首屏列表和封面通常已经在缓存里，
	// 这时是瞬时的；没赶上的话等它一下，也好过先摆骨架、再摆空封面卡、
	// 等图回来才成形——三次跳变叠在一起，就是用户说的「一大片闪」。
	pendingSwitch string
	pendingAt     time.Time
	// navDir / navSeq 是内容层换向的代次：每次真的换了一份内容
	// （平台、一级、二级、三级分区），序号 +1，方向按选项索引差算。
	// 内容层以序号为 key，旧的一屏按当次方向退出，新的从同一侧进来。
	navDir navDir
	// navSeq / followsSeq 分别是首页、关注页内容的代次。
	// 关注页的两级分区（关注主播 / 订阅分区）共用 followsSeq：
	// 换的是同一块内容区。
	navSeq     int
	followsSeq int
	// navBox / followsBox / subsBox 是三条选项轨道量出来的格子。
	//
	// 滑块的位置和宽度都从这里来，所以它必须和真实布局重合；留着它，
	// 测试才能把「算出来的格子」和「布局摆出来的位置」摆在一起对账。
	// 三条轨道的量法各不相同（一级纯文字、二级胶囊、订阅 chip 塞三样），
	// 错一项就是滑块停在半格上。
	navBox     rowBox
	followsBox rowBox
	subsBox    rowBox
	// authAvatarPrefetched 记住每个平台已经预取过的头像地址。
	// 切平台要换头像，四个平台各一张，提前备好才不会有占位的空窗。
	authAvatarPrefetched map[string]string
	// authQR / authQRSeq 是按平台缓存的二维码纹理：换码才重新解码。
	authQR    map[string]*ui.Bitmap
	authQRSeq map[string]int

	// 浏览器登录：虎牙和抖音没有能纯 HTTP 复现的扫码协议，登录面板里
	// 挂一个系统 webview 完成登录，登录成功读走 cookie 就把窗口销毁。
	//
	// 只在面板打开期间存在。loginSess 是临时会话（InPrivate），cookie
	// 不落盘；loginStop 停掉轮询的 goroutine。
	loginPage *mygo.WebView
	loginSess *mygo.Session
	loginKey  string
	loginStop chan struct{}
	// loginReady 表示登录页已经渲染好。在那之前主区域盖一层 loading，
	// 免得用户看到白屏或者半渲染的中间态。
	loginReady bool

	// 「关注」页是一等页面（顶栏 tab 组第一格），和首页同级。
	// 这些字段是它自己的筛选状态，切走再回来还留着。
	FollowsQuery  string
	FollowsSearch ui.Handle
	// 关注页里的平台筛选：空串 = 全部，否则是平台 id
	FollowsPlatform string
	// FollowsSection 是关注页的一级分区：0 关注主播、1 订阅分区。
	// 两级分区共用一份内容代次（followsSeq），方向按点击项的序号差算。
	FollowsSection int
	// subActive 记住每枚订阅 chip 当前显示哪个平台的房间，键是 subChipKey。
	// 不落盘：重启回到「顶栏顺序的第一家」。
	subActive map[string]string
	// followsSectionBox 是一级分区那一行量出来的格子（滑块要停在上面）
	followsSectionBox rowBox

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
	// 指针静止 2.8 秒后淡出，一动就回来。
	ChromeHidden  bool
	chromeIdleAt  time.Time // 空闲计时的起点：指针最后一次活动的时刻
	chromePtrX    float32
	chromePtrY    float32
	chromePtrOver bool // 指针是否还停在画面上
	chromeFull    bool // 上一帧是否处于全屏：进出全屏要先把 chrome 亮出来

	// 启动层：冷启动到首屏数据就绪之间盖一层
	Splash       bool
	splashFading bool
	splashAt     time.Time

	// 顶栏平台顺序（拖拽排序与持久化共用）
	PlatformOrder []string

	// 状态落盘：300ms 内的连续变更合并成一次写盘
	stateDirty       bool
	stateSavePending bool

	// 关注（键是 platform|name）
	Follows []FollowRecord
	// 关注状态刷新的闸门：自动刷新和手动刷新共用，避免两轮请求叠在一起
	followsBusy bool
	// 刷新进度：面板按钮上的圆环按 done/total 画弧。
	// 每查完一条就推进一格，所以环是连续往前走的，不是等整批结束才跳。
	followRefreshDone  int
	followRefreshTotal int

	// Toast：右下角一条短提示，3.2s 后自动消失。
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

		GroupsByPlatform:     map[string][]CategoryGroup{},
		subActive:            map[string]string{},
		liveLoaded:           map[string]bool{},
		feedCache:            map[string]feedEntry{},
		subCategories:        map[string][]string{},
		authAvatarPrefetched: map[string]string{},
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

// bindAuth 给每个支持登录的平台各挂一个状态监听。
//
// 不是一个「全局登录」：每个平台一份会话、一份监听，互不影响。
// B 站和斗鱼走扫码，虎牙和抖音走系统 webview。
func (a *App) bindAuth() {
	a.authLogged = map[string]bool{}
	a.authQR = map[string]*ui.Bitmap{}
	a.authQRSeq = map[string]int{}
	for _, p := range []live.Platform{live.Bilibili, live.Douyu, live.Huya, live.Douyin} {
		if auth, ok := live.AuthFor(p); ok {
			a.bindPlatformAuth(p, auth)
			continue
		}
		if wl, ok := live.BrowserAuthFor(p); ok {
			a.bindPlatformAuth(p, wl)
			// 会话失效时不急着重登：先用同一个 profile 起一次后台页面，
			// 平台自己可能就把它续上了。
			login := wl
			wl.SetInvalidHandler(func() bool {
				if a.Win == nil {
					return false
				}
				a.renewLoginSession(login)
				return true
			})
		}
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

// onAuthChange 在某个平台的登录态变化时重画头像，并按新身份重开这一路流。
//
// 回调在后台 goroutine 上（扫码轮询），必须切回主线程再碰视图状态。
// 登录与否会改变服务端下发的清晰度档位：不重取会一直停在原来那一档，
// 用户会以为「登录了没用」；退出后不重取则更糟——播的是一个已经没有
// 权限的地址。
func (a *App) onAuthChange(p live.Platform, auth live.Authenticator) {
	if a.Win == nil {
		return
	}
	logged := auth.Auth().LoggedIn
	a.Win.Update(func() { a.applyAuthChange(p, logged) })
}

// applyAuthChange 在主线程上落地一次登录态变化。
func (a *App) applyAuthChange(p live.Platform, logged bool) {
	key := string(p)
	if a.authLogged[key] == logged {
		return
	}
	a.authLogged[key] = logged
	// 为了登录临时离开的直播间：登上了就把直播间接回来。放在重取流之前——
	// 重取流那步要求人已经回到播放页。
	if logged && a.authReturn != nil && a.authReturn.Platform == key {
		a.returnFromLogin()
	}
	// 登录态一变就要重取流，两个方向都是：登录后能拿到更高的档位；退出后
	// 那些「仅登录可见」的档位必须立刻作废——继续播原来的地址，等于拿着
	// 一个已经没有权限的流，界面上还写着那个档位。
	if a.media.roomKey != "" && a.current().Platform == key {
		a.startStream(true)
	}
}

// viewPlatform 返回「屏幕上这一屏属于哪个平台」。
//
// 绝大多数时候就是顶栏选中的那个 tab，播放页例外：那一屏看的是某一个
// 具体主播，账号、搜索这些按平台走的东西都得跟着主播，而不是跟着进房
// 之前停留的列表。从「关注」页点开 B 站主播、而 tab 还停在虎牙时，
// 照 a.Platform 画就会把虎牙的账号挂在 B 站直播间里。
func (a *App) viewPlatform() string {
	// 为登录临时离开直播间时，这一屏就是「那个平台的登录流程」：账号入口、
	// 登录页、顶栏高亮都得跟着它，而不是跟着脚下这页首页的 tab。
	if a.authReturn != nil {
		return a.authReturn.Platform
	}
	if a.Router.Path() == routePlayer {
		if p := a.current().Platform; p != "" {
			return p
		}
	}
	return a.Platform
}

// currentAuth 返回当前平台的登录入口；该平台不支持登录时返回 nil。
//
// 扫码和浏览器登录都满足 Authenticator，界面不关心会话是怎么拿到的。
func (a *App) currentAuth() live.Authenticator {
	return authFor(a.viewPlatform())
}

// currentWebLogin 返回当前平台的浏览器登录入口，没有则 false。
func (a *App) currentWebLogin() (*live.WebLogin, bool) {
	return live.BrowserAuthFor(live.Platform(a.viewPlatform()))
}

// Shell 是窗口的根视图：地面 + 主区（顶栏 + 内容）+ 悬浮灵动岛。
//
// 外壳背景只有两层——实色地面和一层极淡的胶片颗粒。颗粒是 SVG
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

	// 各平台登录头像提前进缓存：切平台换头像时才不会先画灰人再跳图。
	a.syncAuthAvatars()
	// 挂起的切平台在这里收尾：目标平台的首屏数据和封面齐了才换。
	a.tickPlatformSwitch()

	// Ctrl/Cmd + K 唤起搜索；输入框里打字时不抢
	// 没有平台上下文的页面连搜索入口都没有，快捷键也不该把它打开。
	if a.searchAvailable() && !c.FocusTakesText() && c.Shortcut(ui.Cmd, ui.KeyK) {
		a.SearchOpen = true
	}

	// 离开播放页先把窗口级和界面级的状态收干净。统一放在这里而不是各个
	// 返回按钮上：离开播放页的路不止一条（左上返回钮、Esc、顶栏 tab），
	// 漏一条就是窗口卡死（见 leavePlayer）。
	//
	// 注意它不管媒体层：后台收听要的正是「界面收了、声音留着」，
	// 引擎的开关在下面和 closeMedia 里。
	if a.Router.Path() != routePlayer {
		a.leavePlayer()
	}

	// 播放页的两种全屏下应用外壳整体让位给画面（外壳是 position:fixed
	// inset:0 z-index:9999，顶栏和灵动岛都被盖住）。
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
				case r.Match(routePlayer):
					a.PlayerPage(c)
				default:
					a.Home(c)
				}
			})
			// 离开播放页：默认进后台收听，声音留着、画面关掉。
			//
			// 只有两条路是真退出——左上角的叉和 Esc——它们自己调 closeMedia。
			// 其余离开方式（顶栏 tab、灵动岛、搜索跳转）都保留这一路的声音，
			// 回来时接着看。
			//
			// 换台不在这里判：从后台收听点开另一个直播间时，ensureMedia 会发现
			// roomKey 变了，reset 之后重开一路，旧引擎在 stopPlayback 里关掉。
			if a.Router.Path() != routePlayer && a.media.roomKey != "" {
				a.detachMedia()
			}
			// 媒体层的心跳：后台收听时视频元素不在了，没有东西替它请求帧，
			// 地址续期和断流重连会跟着一起停摆。
			a.kickMediaTick()
		})
		// 灵动岛浮在内容之上。两种全屏下整块不画：画面上只留返回钮和控制条，
		// 岛连同它那圈流光和投影压在满屏画面上纯属干扰。
		if !fullscreen {
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
// 正文区是 padding 0 14，分区栏自己带 margin-bottom 14，
// 列表区不再额外留间距。
func (a *App) Home(c *ui.Context) {
	if a.LiveMode {
		a.ensureLiveData()
	}
	// 浏览器登录：主体区域整个换成登录页。页面原样铺开，登录框由平台
	// 自己弹出——不裁剪、不代点，页面改版也不影响这里。
	if wl, ok := a.currentWebLogin(); ok && a.AuthOpen && !wl.Auth().LoggedIn {
		if page := a.ensureLoginWebView(wl); page != nil {
			ui.Box(c).Grow(1).Children(func() {
				ui.WebView(c, page).Fill().Radius(theme.RadiusXL)
				// 页面画好之前盖住：标题栏、空白、半渲染的骨架都不给看。
				if !a.loginReady {
					ui.Box(c).Absolute().Fill().Radius(theme.RadiusXL).
						Background(theme.BgPrimary).Center().Children(func() {
						a.loginLoading(c)
					})
				}
			})
			return
		}
	}
	pad := a.bodyPadX(c)
	// 换分区就回到顶部；同一个分区内（去播放页再回来）位置由 ListState 自己记住。
	key := fmt.Sprintf("%s|%d|%d|%d", a.Platform, a.GroupIndex, a.CategoryIndex, a.SubCatIndex)
	if a.scrollKey != key {
		a.scrollKey = key
		// 换的是另一套行，整份状态从头开始，而不是只把偏移挪到 0。
		// ScrollTo(0) 只改位置：第一帧仍会带着旧的分区位置去铺新数据，
		// 屏幕先闪一下列表中间那几行（那些封面多半还没预取），下一帧才
		// 跳回顶部；旧的可见范围还会顺手触发一次「加载更多」。MyGo 的
		// list.md 对这种情况写得很清楚：换一套行就 `ListState{}`。
		a.HomeList = ui.ListState{}
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
		// 内容层单独换代：切平台 / 分区时旧的一屏按方向退出、新的从同一侧
		// 进来。分区栏和错误条留在原地——它们是这一屏的边框，不是内容。
		feed := ui.Column(c).Key(a.navFeedKey()).Grow(1).
			Transition(feedTransition(c, a.navDir))
		feed.Children(func() {
			a.grid(c, a.feed(), &a.HomeList, a.loadMoreRooms)
		})
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
// 切平台、切分区、切订阅都算换了一份新列表，卡片因此重新挂载。
func (a *App) resetCards() {
	a.CardsIn = false
	a.EnterSeq++
}

// bodyPadX 是正文左右 padding：常态 14，1240 以下收到 10。
func (a *App) bodyPadX(c *ui.Context) float32 {
	w, _ := c.Size()
	if w <= 1240 {
		return 10
	}
	return 14
}

// errorBar 是加载失败的提示条。
//
// 尺寸与配色：padding 10 14、圆角 12、background color-mix(danger 8%, bg-secondary)，
// 右侧一枚 30 高的重试按钮。
func (a *App) errorBar(c *ui.Context) {
	ui.Row(c).Padding(10, 14, 10, 14).Gap(12).Radius(theme.RadiusMD).
		AlignItems(ui.Center).Background(theme.BgSecondary.Mix(theme.Danger, 0.08)).
		Children(func() {
			ui.Text(c, a.ErrText).FontSize(theme.TextSM).TextColor(theme.TextPrimary).Grow(1)
			retry := ui.Row(c).Height(30).Padding(0, 14, 0, 14).Gap(6).
				Radius(theme.RadiusFull).Center().Cursor(ui.CursorPointer).Label("重试").
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

// feedFor 返回订阅分区某个订阅项下的直播间。
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
// 网格是 `grid-template-columns: repeat(auto-fill, minmax(220px, 1fr))`：
// 列数随可用宽度自动增减、每列等宽、内容区始终满宽。
// 这里先按同一条公式自己算出列数与卡片宽度，再用 Grid 的固定列数落下——
// GridView 内部还叠了一层自己的取整，会少一列、右侧留白。
//
// 列表交给 ui.List 做虚拟化：一次只构建可视范围内的那几行卡片。
// 滑到第 3000 张和第 30 张的每帧成本一样——这是「一直往下滑」的前提。
// 行高由 List 自己测量（我们的卡片高度是算出来的固定值），不用手动喂。
func (a *App) grid(c *ui.Context, list []Streamer, ls *ui.ListState, onMore func()) {
	w, _ := c.Size()
	// 正文的左右 padding，再减掉常驻滚动条占的 10px：
	// 列宽按可用宽度算，滚动条也不会压在最后一列上。
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
	// 它跟着内容一起滚，位置在列表末尾。
	// 触底补页交给 EndReached：它在用户真的滚到末尾附近时调用一次，
	// 不像自己读 Visible 那样，换数据源的头一帧就被旧的可见范围触发。
	if onMore != nil && a.LiveMode {
		ls.NearEnd = 2
		ls.EndReached = func(int) { onMore() }
	}
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
				// 错峰只给前 6 张；这里也只让首屏可见的卡参与入场，
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
			media.Default.PrefetchAvatars(32, list[i].AvatarURL)
		}
	}

}

// skeletonGrid 是首屏骨架：12 张卡片，封面 16:9，信息条与真卡片同尺寸。
func (a *App) skeletonGrid(c *ui.Context, avail float32, cols int, cardW float32) {
	ui.Grid(c).Width(avail).Columns(cols).Gap(theme.GridGap).Children(func() {
		for i := 0; i < 12; i++ {
			sk := ui.Column(c).Width(cardW).Radius(theme.RadiusMD).
				Background(theme.BgSecondary)
			// 骨架的脉冲：1.6s ease-in-out 循环，opacity 0.55 ↔ 1；
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
				Label("查看全部分区").Background(theme.Accent)
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
				Label("重试").Background(theme.FillTertiary)
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
			// 页脚的三态：加载中 / 还有下一页 / 到底了
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
// 顶栏整条高 64、左右 padding 14、gap 14、背景就是 bg-primary；
// 左侧平台 tablist，右侧一组 actions（gap 10）：
// 搜索按钮 → 工具组 → 1px 分隔线 → 窗口按钮。
// 工具组把「同步 / 配色」两颗 38×38 的图标按钮包进一层 fill-quaternary 的胶囊里
// （gap 4 + padding 3），和窗口按钮组是同一套语言。
func (a *App) Navbar(c *ui.Context) {
	pad, gap := float32(14), float32(14)
	if a.narrow(c) {
		// ≤1240：gap 8、左右 padding 10
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
			// 工具组：搜索、同步、配色三颗按钮在同一个胶囊里；
			// 值得搜索的只有某个平台的直播列表：关注页没有平台上下文，
			// 它自带页内搜索，那里不摆搜索按钮。
			ui.Row(c).Gap(4).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
				Background(theme.FillQuaternary).Children(func() {
				if a.searchAvailable() {
					a.searchButton(c)
				} else {
					// 按钮不在屏幕上时把浮层一起收掉：不然从搜索浮层跳去
					// 「关注」，回来时浮层会自己弹出来。
					a.SearchOpen = false
				}
				a.iconButton(c, iconMonitorSmartphone, "数据同步", a.openSyncPanel)
				a.paletteButton(c)
				// 登录头像和上面三颗在同一个胶囊里：顶栏右侧本来就是一组
				// 工具，单独拎出去会多出一层视觉噪音。
				//
				// 只在某一家平台的页面里画：账号是**按平台**的，而「关注」页
				// 聚合的是好几家的主播。在那种页面上摆
				// 一家的头像，等于暗示这里的操作以那家身份进行——用户点开
				// 还会看到另一家的二维码，前后对不上。登录入口回到对应平台
				// 的 tab 再点。
				if a.authAvailable() {
					a.authButton(c)
				} else {
					a.closeAuthPanel()
				}
			})

			ui.Box(c).Width(1).Height(24).Background(theme.Separator)

			a.windowControls(c)
		})
	})
}

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

// toast 的三种色调。
const (
	toastSuccess = "success"
	toastInfo    = "info"
	toastError   = "error"
)

// toastIcon 是每种色调左侧的那枚图标。
func toastIcon(tone string) *ui.SVG {
	switch tone {
	case toastSuccess:
		return iconCheck
	case toastError:
		return iconTriangleAlert
	}
	return iconAlert
}

// toastIconColor 是图标颜色：success / danger / text-tertiary。
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
// 尺寸与位置：right/bottom 18、min-height 38、padding 7 8 7 12、
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

// narrow 报告窗口是否落在 ≤1240 断点里。
func (a *App) narrow(c *ui.Context) bool {
	w, _ := c.Size()
	return w <= 1240
}

// activeTab 返回顶栏该高亮的 tab。
//
// 「关注」不是平台，但和平台同级：在关注页时高亮那颗星星，
// 其余按当前平台。
func (a *App) activeTab() string {
	// 挂起的切换先占住高亮：点了就要有回应，内容等就绪再换。
	if a.pendingSwitch != "" {
		return a.pendingSwitch
	}
	switch a.Router.Path() {
	case routeFollows:
		return "follows"
	case routePlayer:
		// 播放页看的是某一个具体主播，顶栏必须跟着主播所属平台走。
		// 读 a.Platform 会停在进播放页之前那个列表的平台：从关注页点开
		// 别家主播、或者从搜索里跳进来时，高亮就留在上一家。
		return a.viewPlatform()
	}
	return a.Platform
}

// tabName 是 tab 在无障碍树里的名字。
func tabName(id string) string {
	switch id {
	case "follows":
		return "关注"
	}
	return platformLabel(id)
}

// switchTab 处理 tab 点击。
func (a *App) switchTab(id string) {
	if id == "follows" {
		a.pendingSwitch = ""
		if a.Router.Path() == routeFollows {
			return
		}
		a.Router.Push(routeFollows)
		return
	}
	if id == a.Platform {
		// 平台没换，但页面可能在别处：从关注 / 播放页点回当前平台的
		// tab，要回首页。平台本身没换，所以内容不播换向。
		if a.Router.Path() == routeHome {
			return
		}
		a.pendingSwitch = ""
		a.Router.Push(routeHome)
		return
	}
	if a.platformSwitchReady(id) {
		a.commitPlatformSwitch(id)
		return
	}
	// 目标平台的首屏还没到位：先挂起，界面保持原样，由 tickPlatformSwitch
	// 在就绪（或等够）时收尾。等待期间不摆任何中间态，用户看到的就是
	// 上一家的完整画面，直到下一家的完整画面接上。
	a.pendingSwitch = id
	a.pendingAt = time.Now()
}

// platformSwitchWait 是切平台时等目标平台首屏就绪的上限。
//
// 超过它就不再等：宁可闪一次，也不能让点击几百毫秒没有内容上的回应。
const platformSwitchWait = 700 * time.Millisecond

// platformSwitchReady 报告切到 id 时该有的东西是不是都准备好了：首屏列表在缓存里。
//
// 只看列表，不看封面。卡片的封面位本来就有一块同尺寸的渐变占位
// （coverArt），图晚到是从渐变换成真图，版式不动；把它当前置条件，
// 代价是只要有一张封面还在路上，点击之后界面就要静止整整 700ms——
// 滑块已经滑到目标，内容一动不动，这就是「切平台要一秒」的来源。
func (a *App) platformSwitchReady(id string) bool {
	if id == a.Platform {
		return true
	}
	groups := a.GroupsByPlatform[id]
	if len(groups) == 0 || len(groups[0].CategoryIDs) == 0 {
		return false
	}
	entry, ok := a.feedCache[id+"|"+groups[0].CategoryIDs[0]+"|-1"]
	return ok && len(entry.rooms) > 0
}

// tickPlatformSwitch 在挂起的切换就绪（或等够了）时真正切过去。
func (a *App) tickPlatformSwitch() {
	if a.pendingSwitch == "" {
		return
	}
	id := a.pendingSwitch
	if !a.platformSwitchReady(id) && time.Since(a.pendingAt) < platformSwitchWait {
		return
	}
	a.pendingSwitch = ""
	a.commitPlatformSwitch(id)
}

// commitPlatformSwitch 执行真正的切换：换数据源、换显存、换路由。
func (a *App) commitPlatformSwitch(id string) {
	// 方向在换之前算：顶栏里往右点，内容就从右边来。
	a.navFlip(&a.navSeq, a.tabIndex(a.Platform), a.tabIndex(id))
	a.Platform = id
	// 账号是分平台的，头像浮层里的内容会跟着换人；换 tab 就关掉，
	// 别让上一家的二维码和昵称留在屏幕上。
	a.AuthOpen = false
	// 用户自己换了平台，就不再是「去登录那一趟」了。startRoomLogin 会
	// 在这之后重新设上。
	a.authReturn = nil
	// 换平台了，旧平台的纹理对新平台没有任何用处：立刻清掉 L1。
	// 但要保住新平台已经预取好的首屏封面，否则用户还是要等一遍解码。
	// 原始字节（L2）全部留着——切回去时能直接解码，不用重新下载。
	media.Default.ReleaseTexturesExcept(a.feedURLs(id))
	// 切平台清空搜索，上一个平台的结果不能留在浮层里。
	// 代次 +1 让还在路上的那次请求作废。
	a.SearchQuery, a.SearchResults, a.SearchErr, a.SearchLoading = "", nil, "", false
	a.SearchHit = 0
	a.searchSeq.Add(1)
	a.Router.Push(routeHome)
	a.onPlatformChanged()
}

// platformTabs 是顶栏左侧的 tablist：关注 + 四个平台。
//
// 选中态靠一条**绝对定位的 accent 滑块**在 tab 之间滑过去（220ms），
// 而不是给选中的 tab 换底色——切换时是「光块移动」，不是「色块跳变」。
// tab 等宽（每列至少 64px，中文两字 + 28 padding 正好 64），
// 所以滑块位置直接按序号乘宽度算，不用量 DOM。
func (a *App) platformTabs(c *ui.Context) {
	// 顺序读 a.PlatformOrder：tab 顺序可拖拽重排并持久化。
	// 第一格固定：关注（那颗星星）——它不是平台，但和平台同级；
	// 订阅分区已经并进关注页，不再单独占一格。
	type tabItem struct{ id, label string }
	tabs := make([]tabItem, 0, len(a.tabIDs()))
	for _, id := range a.tabIDs() {
		label := ""
		if id != "follows" {
			label = platformLabel(id)
		}
		tabs = append(tabs, tabItem{id, label})
	}

	tabW := float32(64)
	tabH := float32(34)
	if a.narrow(c) {
		// ≤1240：列宽 56、tab 高 36
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
		// 浅色下的轨道：白底 + 1px 描边
		track.Background(ui.Hex("#ffffff")).Border(1, theme.Border)
	}

	track.Children(func() {
		list := ui.Box(c).Width(tabW * float32(len(tabs))).Height(tabH)
		list.Children(func() {
			// 滑块先画，压在 tab 下面
			slider := ui.Box(c).Absolute().Top(0).Width(tabW).Height(tabH).
				Radius(theme.RadiusFull).Background(theme.Accent)
			// 滑块走弹簧：连点两个 tab，它带着当前速度从当前位置折返，
			// 而不是先停在前一个目标再重新起步。曲线做不到这一点。
			slider.Left(slider.AnimateTo("tab-x", float32(selected)*tabW, theme.Slide()))

			// tab 行要压在滑块上面：绝对定位的滑块默认在普通子元素之后绘制，
			// 不抬层级会把选中的 tab 文字整块盖住（截图里只剩一块青柠胶囊）。
			ui.Row(c).Fill().ZIndex(1).Children(func() {
				for i, t := range tabs {
					t, idx := t, i

					// 「全部关注」和其他 tab 一样是页面入口，点了切路由
					tab := ui.Box(c).Width(tabW).Height(tabH).Radius(theme.RadiusFull).
						Center().Cursor(ui.CursorPointer)
					// 每个 tab 都要有可读的名字：自绘 Box 不进无障碍树，
					// 没有 Label 的话自动化（和读屏）看到的只是一串无名元素。
					tab.Label(tabName(t.id))
					if tab.Clicked() {
						a.switchTab(t.id)
					}
					// 平台 tab 可拖拽重排（关注与订阅不参与）。
					// 拖起来的那枚 dim 到 0.35，经过的目标描一圈 accent 65% 的内阴影。
					if t.id != "follows" {
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
// 一组实色胶囊：外层 3px padding + fill-quaternary 底 + 1px 描边，
// 里面三枚 40×34 的圆角按钮，图标 16px、1.8 描边，关闭键 hover 变红。
func (a *App) windowControls(c *ui.Context) {
	ui.Row(c).Gap(2).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
		Background(theme.FillQuaternary).Border(1, theme.Border).
		Children(func() {
			a.caption(c, iconMinimize, "最小化", false, func() {
				if a.Win != nil {
					a.Win.Minimize()
				}
			})
			a.caption(c, iconMaximize, "最大化", false, func() {
				if a.Win != nil {
					a.Win.Maximize()
				}
			})
			a.caption(c, iconClose, "关闭窗口", true, func() {
				if a.Win != nil {
					a.Win.Close()
				}
			})
		})
}

// caption 是一枚窗口按钮。
func (a *App) caption(c *ui.Context, ico *ui.SVG, label string, danger bool, on func()) {
	w, h := float32(40), float32(34)
	if a.narrow(c) {
		// ≤1240：34×32
		w, h = 34, 32
	}
	btn := ui.Box(c).Size(w, h).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
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
