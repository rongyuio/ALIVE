package ui

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/live"
	"alive/internal/media"
	"alive/internal/theme"
)

// App 是首页的全部状态。协议层接上之前，streamers 用一份假数据撑版式。
type App struct {
	Win       *mygo.Window
	Platform  string
	Streamers []Streamer

	// 分区选择：一级 / 二级。四个平台的分区树都只有两级
	// （internal/live 的 CategoryGroup.Categories），没有第三级。
	GroupIndex    int
	CategoryIndex int

	Groups []CategoryGroup

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
	// catSeq 是分区树请求自己的代次，和列表的 liveSeq 分开。
	//
	// 两者曾经共用一个序号：切平台时 loadRooms 推进会顺带作废在途的分区树
	// 响应——A→B→A 快切一次，B 的分区树就永远填不进 GroupsByPlatform，下次
	// 切过去又要重拉一遍。
	catSeq int
	// 分区级列表缓存：切回看过的分区先渲染缓存，再判断是否过期刷新。
	feedCache map[string]feedEntry
	// homeCursor 是首页列表自己的续取位置。不能读 feedCache 的 cursor：
	// 那个 cursor 首页和订阅页共用，另一边续过之后会把这一边没取到的一段
	// 整段跳过（首页只到 40、缓存被订阅页推到 120，再续就从 120 起，
	// 41..120 永久缺失）。订阅页的 subCursor 同理。
	homeCursor int
	// topUpAfter / topUpFailures 是自动补页的失败退避。补页写在渲染路径里，
	// 失败回包复位 LoadingMore 后如果不退避，下一帧就会再次发起——每帧一次
	// 上游请求。冷却只拦自动补页，不拦用户滚动触发的 EndReached。
	topUpAfter    time.Time
	topUpFailures int

	// 分区搜索：查询词、输入框是否展开、当前高亮项
	CategoryQuery      string
	CategorySearchOpen bool
	CategoryHit        int
	catHitQuery        string
	// catSearchFocused 是「上一帧输入框有没有焦点」，用来取聚焦边沿。
	// catSearchQuery 是「上一帧的查询词」，用来取"用户又改了词"这个边沿。
	// 两个边沿缺一不可——Esc 收起面板后焦点还在框里，只认聚焦边沿就再也不会
	// 展开（见 category_bar.go）。
	catSearchFocused     bool
	catSearchQuery       string
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
	// subResolve 记「缺分区 id 的旧订阅」上一次回填请求的时刻，值是下一次
	// 可以再试的时刻（键见 subResolveKey）。ensureSubFeed 在渲染路径上每帧
	// 都跑，没有它一条旧订阅就是每帧一次 Categories 请求。
	subResolve map[string]time.Time
	// subCursor 是订阅列表自己的续取位置（平台 offset）。不能读 feedCache
	// 的 cursor：那个 cursor 首页和订阅页共用，另一边续过之后会把这一边
	// 没取到的一段整段跳过。
	subCursor int

	// focus 是唯一的焦点：用户此刻「在」的那一间，至多一个或为空。
	//
	// 播放页画它、岛上显示它（Live Activity）、出声的也是它；墙上的 active、
	// 后台收听、呈现尺寸、岛形态全部从它推导（见 surface.go）。以前的
	// a.Player / a.WatchActive / a.media.roomKey 三份平行状态已经删掉。
	//
	// 只通过 setFocus / closeFocus 改，不直接赋值。
	focus *roomSession

	// DefaultVolume 是新房间的初始音量（0–1），也就是落盘的 volume。
	// 某间房自己的音量住在 roomSession.volume 里。
	DefaultVolume float32
	WinFull       bool // 窗口全屏：播放页铺满窗口，应用顶栏让位

	// drawnMenus 让窗口自己画弹出菜单（ui.PopoverMenus），而不是交给系统。
	//
	// 只由 ALIVE_DRAWN_MENUS=1 打开，给 e2e 用：系统的弹出菜单不在这套
	// 自绘元素树里，automation 够不着；画在窗口里的菜单条目是元素。
	drawnMenus bool
	// roomChoices 是每个直播间自己的清晰度 / 线路（键 platform|roomID）。
	// 这两项属于房间，不属于应用；全局存一份会在切台、接管时串到别的平台。
	// 读写走 room_choice.go。
	roomChoices map[string]roomChoice
	QualityOpen bool
	LineOpen    bool
	// 清晰度和线路两个下拉是 hover 触发的（onMouseEnter 开、onMouseLeave
	// 排一个 220ms 后关的定时器），hoverLeft 记指针最后一次离开按钮和面板
	// 的时刻。弹幕设置面板不是：它有滑杆和取色器，只认点击。
	QualityHover hoverClose
	LineHover    hoverClose
	// Mini 是小窗播放的独立窗口；nil 表示没开小窗
	Mini *mygo.Window
	// pipOn 是小窗模式本身，和窗口指针分开。
	//
	// 分开的理由是测试：ui.Tester 里开不出真窗口（mygo.NewWindow 会一直等
	// 平台就绪），把 newMiniWindow 换成返回 nil 的假实现之后，"模式开着但
	// 窗口不在"这个状态才断言得了——miniOpen 此时必须报没开。窗口在不在看
	// Mini，模式开没开看它。
	pipOn bool
	// media 是播放页的媒体层：转封装 + 播放器 + 弹幕连接。
	// 它们要跨帧活着，所以挂在 App 上而不是视图里。
	media playerMedia

	// 观测墙：最多 watch.MaxRooms 间，顺序即墙上的布局顺序。
	WatchRooms []Streamer
	// watch 是所有直播间的运行态：一间房一个 roomSession，外加墙面布局、
	// 冷存的音量与签名地址。播放页和墙格画的是同一张表里的同一个对象。
	watch wallMedia
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
	danmu    danmuState

	// 弹幕时钟：只有画面真的在前进时才走。
	//
	// 墙钟在暂停、缓冲、换路时照走，弹幕于是继续从静止的画面上飘过去——
	// 用户看到的是「画面停了，弹幕还在剧透」。这里把弹幕时间做成可停的钟：
	// 画面在前进时按墙钟均匀推进，位置停住超过 danmuStallGrace 才冻结。
	// 步长取自墙钟而不是位置增量，因为 time-pos 只按视频帧率更新（实测
	// 30fps 流上约 30 次/秒、间隔 20–40ms），拿它做步长就会一帧走一帧停。
	// clock 可注入，测试直接推进它，不 sleep。
	clock     func() time.Time
	danmuNow  time.Time
	danmuLast time.Time
	danmuPos  time.Duration
	// danmuStill 是画面连续没有前进的时长，用来区分「采样间隔」和真停顿。
	danmuStill time.Duration
	// danmuTracks 是上一帧算出来的实际轨道数；danmuLanes 记每条轨道最后
	// 一条弹幕的出生时刻与估算宽度。分配必须用实际轨道数，不能拿 seq 取模
	// 一个写死的 14；同轨能不能接力，要看后一条会不会追上前一条。
	danmuTracks int
	danmuLanes  []danmuLane
	danmuStageW float32

	// 弹幕设置（落盘键 danmu）
	DanmuSettingsOpen bool
	// 颜色 / 描边的取色浮层各自一个开关。取色器比设置面板大，指针得挪到
	// 面板外面去用，所以设置面板的 hover 关门逻辑要把这两个算进去。
	DanmuFont     float32
	DanmuDur      time.Duration
	DanmuArea     float32
	DanmuOpacity  float32
	DanmuColorful bool
	DanmuColor    ui.Color
	DanmuStroke   ui.Color

	// 弹幕屏蔽词（控制条漏斗面板）：命中的弹幕不上屏、也不进右侧历史。
	// 面板只有点击才开，所以不需要 hoverClose。
	DanmuFilters      []string
	DanmuFilterOpen   bool
	danmuFilterScroll ui.ScrollState

	// 右侧弹幕列表
	DanmuPanelOn bool
	DanmuPanelW  float32
	// PanelList 是弹幕列表。FollowEnd 让它贴底：新弹幕把底推上来，用户上滑就
	// 脱离、滑回底部又跟上，视口变高变矮也不用谁来重新钉 Y。行的身份是每条
	// 弹幕的序号（danmuLine.Seq）。
	PanelList components.ListState
	panelSeen int
	// panelMark 是上次结算时日志里最后一条的身份。日志满 danmuLogMax 后
	// 长度恒定，只有回看「上次看到哪一条」才能数出新弹幕（见 panelNewDanmu）。
	panelMark    panelLineMark
	panelPending int
	panelDragW   float32
	panelDragX   float32

	// 配色：当前配色 ID、配色面板是否展开，以及夜/日/跟随系统的选择。
	// ThemeMode 取 "system"（默认）/ "dark" / "light"：它决定用哪一侧的配色，
	// PaletteID 决定用哪个色系，两者一起确定最终配色。
	PaletteID   string
	PaletteOpen bool
	ThemeMode   string
	// 主题切换的中间状态：themeFrom 是当前显示（可能是混合后）的调色板；
	// themeMixKey 每换一次目标 +1，根元素的 AnimateTo 用这个 key 从 0 走到 1。
	themeFrom     *theme.Palette
	themeTargetID string
	themeHC       bool
	themeActive   bool
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
	// pendingSwitch 是点了但还没真正切过去的平台。
	//
	// 切平台不立刻换内容：目标平台的首屏列表和封面通常已经在缓存里，
	// 这时是瞬时的；没赶上的话等它一下，也好过先摆骨架、再摆空封面卡、
	// 等图回来才成形——三次跳变叠在一起，就是用户说的「一大片闪」。
	pendingSwitch string
	// pendingTries 是已经问过几次「目标平台首屏好了没」；到 platformSwitchTries
	// 就不再等。
	pendingTries int
	// navSeq / followsSeq 分别是首页、关注页内容的代次：每次真的换了一份
	// 内容（平台、一级、二级、三级分区），序号 +1，内容层据此换 key。
	// 只换代，不播位移——整块横滑会让内容在切换瞬间和分区栏错开。
	navSeq int
	// 关注页的两级分区（关注主播 / 订阅分区）共用 followsSeq：
	// 换的是同一块内容区。
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

	// 「关注」页是一等页面（顶栏 tab 组第一格），和首页同级。
	// 这些字段是它自己的筛选状态，切走再回来还留着。
	FollowsQuery  string
	FollowsSearch ui.Handle
	// followsSearchOpen 是关注页搜索框的展开状态；由聚焦边沿置真、Esc 置假。
	followsSearchOpen    bool
	followsSearchFocused bool
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
	HomeList ui.ListState
	SubList  ui.ListState
	// FollowsGrid 是关注页主播行的网格：虚拟化，只建可见的行；滚动位置和
	// 行的身份都由它自己保持。
	FollowsGrid components.GridState
	scrollKey   string

	// 最近一次网格布局：列数与卡片宽度。启动层判断「首屏封面是否到齐」要用
	gridCols  int
	gridCardW float32

	// 播放页：静音前的音量、上一帧的系统全屏状态（认「这次 Esc 是不是已经被窗口用掉」）、
	// 快捷键说明浮层
	volumeBeforeMute float32
	wasOSFullScreen  bool
	ShortcutsOpen    bool

	// 顶栏「数据同步」面板：导出 / 导入 / 诊断三个页签。
	// SyncBusy 期间按钮不响应，SyncBusyAction 记住忙的是哪一个动作——三个
	// 动作共用一个位，但界面要能说清「现在跑的是哪个」。
	// SyncPath 是最近一次产物的完整路径，诊断页拿它做复制 / 定位；
	// SyncNotice 只放文件名，省得长路径把结果条撑成两行。
	SyncOpen       bool
	SyncTab        string // "export" / "import" / "diag"
	SyncBusy       bool
	SyncBusyAction string
	SyncNotice     string
	SyncPath       string
	SyncPlan       *importPlan
	SyncCounts     configCounts
	SyncError      string
	// ProfStarted 是性能剖析的起点：按钮用它算还剩几秒。
	// ProfCancel 是同一份采样的取消入口；30 秒太长，用户必须能中途停。
	ProfStarted time.Time
	ProfCancel  context.CancelFunc

	// DiagGroups / DiagEmpty 是诊断页签的指标快照。切到该页签、动作完成或
	// 点「刷新指标」时才重算，不在每帧构建路径里读 registry、分配字符串。
	DiagGroups []diagGroup
	DiagEmpty  bool

	// CheckUpdates 是诊断页「检查更新」按钮的动作，由 main 注入 mygo 的更新插件。
	//
	// 走注入而不是在这里 import 插件：插件只在 main 里 mygo.Use 之后才活着
	// （updater 包里取不到实例会 panic），而这一包的测试直接 NewMockApp，
	// 从不 Use 任何插件。nil 表示这个构建没有更新能力——按钮就不出现，
	// 而不是画一个点了会 panic 的按钮。
	CheckUpdates func()

	// 播放页所有浮层（顶栏 / 控制条 / 下拉 / 窗口按钮）的显隐：一份状态，
	// 一个判据（syncChrome）。指针在窗口里停住 2.8 秒一起让位，指针离开
	// 窗口立刻让位，一动就全回来。
	chrome playerChrome

	// 上一帧的窗口内容尺寸。窗口刚变过的那一帧，stage 的 LayoutBounds 还是
	// 旧布局，拿它判黑边会晚一帧——拖窗时黑边区就会闪一下纯黑。
	stageWinW float32
	stageWinH float32

	// 关注页一级分区的显示顺序（可拖动换位，落盘）
	FollowsSectionOrder []string

	// 顶栏平台顺序（拖拽排序与持久化共用）
	PlatformOrder []string

	// 状态落盘：300ms 内的连续变更合并成一次写盘
	stateDirty       bool
	stateSavePending bool
	// stateTimer 是 markDirty 排上的那颗合并定时器。必须留句柄：FlushState
	// 之后它还在跑，到点又异步写一次盘——进程随时先退出，那次写盘悬空。
	// wasOnPlayer 是上一帧在不在播放页：leavePlayer 只在离开那一帧调一次。
	wasOnPlayer bool
	// playerShownLastFrame 是**再上一帧**的在不在播放页。
	//
	// 外壳挂不挂 Match 要用它，不能用 wasOnPlayer：那个在 Shell 开头就被
	// 更新成本帧的值，外壳读到的永远是「本帧在播放页」，判据恒真。见
	// shellMatchKey。
	playerShownLastFrame bool
	stateTimer           *time.Timer
	// stateSaveSeq 是快照序号（只在主线程自增）。写盘只往新写，旧的那份丢掉。
	stateSaveSeq uint64
	// stateSaveFailed 是后台写盘失败留下的旗标：writeState 只置这一位，
	// Shell 下一帧取走并变成一条通知（取一次就清）。stateSaveWarned
	// 保证一次会话只提醒一次：持续失败反复弹会盖住别的通知。
	stateSaveFailed atomic.Bool
	stateSaveWarned bool

	// 关注（键是 platform|name）
	Follows []FollowRecord
	// 关注状态刷新的闸门：自动刷新和手动刷新共用，避免两轮请求叠在一起
	followsBusy bool
	// 关注状态的新鲜度：上一轮刷完的时刻、失败退避的截止时刻、连续失败轮数。
	//
	// 这三个字段只有 refreshFollows 的收尾会写（见 follow.go），读的人
	// 只有 followsDue。自动刷新的三个触发点都靠它们判断「该不该刷」——
	// 不要在别处对着它们加减。
	followsRefreshedAt time.Time
	followsRetryAt     time.Time
	followsFailStreak  int
	// windowHidden 是窗口正处于最小化。窗口可见性只有 WatchHidden /
	// WatchShown 两个写者；关注页的心跳靠它判断「有没有人在看」，
	// 不在渲染路径里回头去问窗口。
	windowHidden bool
	// lastRoute 是上一帧的路由，Shell 用它求「进入关注页」那一帧的边沿。
	lastRoute string
	// 刷新进度：面板按钮上的圆环按 done/total 画弧。
	// 每查完一条就推进一格，所以环是连续往前走的，不是等整批结束才跳。
	followRefreshDone  int
	followRefreshTotal int

	// toastQueue 是待显示的通知，按到达顺序排队。
	//
	// toastTone 只入队，不直接 AddToast：刷新关注完成落在 win.Update
	// 回调里，那里没有构建帧的 Context。Shell 每帧认领一次并清空，
	// 同一次事件重建的后续帧看到的是空队列，不会重复弹。
	toastQueue []ui.Toast

	// 列表加载态：数据层接上之前由 main.go 的拉取结果驱动
	Loading bool
	ErrText string
	// 冷启动的读屏播报只发一次：每帧 Announce 会把读屏变成复读机
	bootAnnounced bool

	// routeTransitionUntil 是 Match 过渡的结束时刻；零值表示当前不播页面
	// 过渡。由 enterPlayer / leavePlayerTo 置、tickRouteTransition 收尾。
	routeTransitionUntil time.Time
	// frameNow 是这一帧的视图时钟：生产里是系统时钟，Tester 里是它自己的。
	// 过渡的截止时刻必须和 Router 的进度用同一个时钟——用墙钟的话，测试里
	// 墙钟先到、动画还在原地，tickRouteTransition 会在半路把过渡抽掉。
	frameNow time.Time
	// winFullscreen 是系统全屏的缓存：问窗口只能在主线程，Shell 每帧记一次。
	// 事件处理器（离开播放页）读缓存，不直接问窗口——测试里的零值窗口一问
	// 就永远阻塞。
	winFullscreen bool
	// playerReturnRoute / playerReturnSection 是进播放页时记下的「退回哪一页」。
	playerReturnRoute   string
	playerReturnSection int
	// wasFullSize 是上一帧 fullSize() 的值，只用来做边沿触发：进入/离开
	// 播放页时各做一次收尾（断弹幕、拨看门狗基准、重求呈现尺寸）。
	// 它不是真相，真相是 fullSize()。
	wasFullSize bool
	// wasPlayerFullscreen 是上一帧 playerFullscreen() 的值。应用内全屏
	// 切换不改变窗口尺寸，但会撤掉 Navbar、把左右 padding 收到 0；带
	// Match 的外壳会把这次布局变化做成弹簧动画，视频逐帧重排。边沿处
	// 复用 resize 的 400ms 抑制（matchable 的同一个写者）。离开播放页
	// 时不算：那一路的 Match 落点是墙格，不能被这次复位挡掉。
	wasPlayerFullscreen bool
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
		// 一级分区默认「关注主播 → 订阅分区」，用户拖过一次就按存下来的排
		FollowsSectionOrder: normalizeFollowsSectionOrder(nil),
		Follows:             []FollowRecord{},
		// 首帧网格还没量过，先用最小卡片宽占位：预取封面时不能是 0，
		// 0 在图片缓存里表示「按原图解码」。
		gridCardW:     theme.CardMinWidth,
		Router:        ui.NewRouter(routeHome),
		PaletteID:     theme.Palettes[0].ID,
		ThemeMode:     themeModeSystem,
		DefaultVolume: 0.6,
		clock:         time.Now,
		DanmuOn:       true,
		// 弹幕面板贴底：新弹幕推着底走，用户上滑就脱离。
		PanelList:     components.ListState{FollowEnd: true},
		DanmuFont:     20,
		DanmuDur:      10 * time.Second,
		DanmuArea:     0.5,
		DanmuOpacity:  1,
		DanmuColorful: true,
		DanmuColor:    theme.White,
		DanmuStroke:   theme.DanmuStrokeInk,
		DanmuPanelOn:  true,
		DanmuPanelW:   196,

		GroupsByPlatform:     map[string][]CategoryGroup{},
		subActive:            map[string]string{},
		liveLoaded:           map[string]bool{},
		feedCache:            map[string]feedEntry{},
		authAvatarPrefetched: map[string]string{},
	}
	// 顶栏这些 tab 是同一层级的横向页面，没有谁是「进入谁的下一层」。
	// Router 按路径层级决定往哪边滑，在这套结构上只会滑出三种不一样的结果，
	// 所以干脆关掉页面过渡：切过去就是切过去，动的只有那颗滑块。
	a.Router.Transition = ui.TransitionNone
	// 媒体层读焦点用；焦点是 App 上的唯一真相，媒体层不再自己存一份。
	a.media.app = a
	return a
}

// NewApp 是应用本体：分区与直播列表都来自 internal/live。
func NewApp() *App {
	a := newApp()
	a.LiveMode = true
	// 目前只有 B 站接入了真实数据，默认落在这里。
	a.Platform = "bilibili"
	a.restore()
	a.bindAuth()
	// 菜单画在窗口里还是交给系统。
	//
	// 默认是系统的（Windows 上是 Win32 弹出菜单，不在这套自绘元素树里）。
	// 设 ALIVE_DRAWN_MENUS=1 时改成 ui.PopoverMenus：菜单条目变成元素，
	// 读屏和 automation 都看得见——e2e 要驱动右键菜单（把直播间收进
	// 观测墙）只能走这一档。见 third_party/mygo/docs/ui/context-menu.md
	// 的「Drawn in the window」。
	a.drawnMenus = os.Getenv("ALIVE_DRAWN_MENUS") == "1"
	return a
}

// bindAuth 给每个支持登录的平台各挂一个状态监听。
//
// 不是一个「全局登录」：每个平台一份会话、一份监听，互不影响。
// 只有 B 站和斗鱼有登录（扫码）。
func (a *App) bindAuth() {
	a.authLogged = map[string]bool{}
	a.authQR = map[string]*ui.Bitmap{}
	a.authQRSeq = map[string]int{}
	// 只有扫码登录的两个平台。虎牙和抖音都不接登录：画质档位和弹幕匿名
	// 就全有，登录换不到东西。
	for _, p := range []live.Platform{live.Bilibili, live.Douyu} {
		if auth, ok := live.AuthFor(p); ok {
			a.bindPlatformAuth(p, auth)
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
	// 登录态一变就要重取流，两个方向都是：登录后能拿到更高的档位；退出后
	// 那些「仅登录可见」的档位必须立刻作废——继续播原来的地址，等于拿着
	// 一个已经没有权限的流，界面上还写着那个档位。
	if a.media.roomKey() != "" && a.current().Platform == key {
		if a.onWatchWall() {
			// 墙上这一格由墙自己驱动：重开它，别把播放页那套取流塞进来。
			a.watchReload(a.current())
		} else {
			a.startStream(true)
		}
	}
}

// viewPlatform 返回「屏幕上这一屏属于哪个平台」。
//
// 绝大多数时候就是顶栏选中的那个 tab，播放页例外：那一屏看的是某一个
// 具体主播，账号、搜索这些按平台走的东西都得跟着主播，而不是跟着进房
// 之前停留的列表。从「关注」页点开 B 站主播、而 tab 还停在虎牙时，
// 照 a.Platform 画就会把虎牙的账号挂在 B 站直播间里。
func (a *App) viewPlatform() string {
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

// Shell 是窗口的根视图：地面 + 主区（顶栏 + 内容）+ 悬浮灵动岛。
//
// 外壳背景只有两层——实色地面和一层极淡的胶片颗粒。颗粒是 SVG
// feTurbulence 平铺、opacity 5%，原生侧暂时省掉（视觉上几乎不可见，
// 而且每帧重绘一张噪点纹理不划算）。
func (a *App) Shell(c *ui.Context) {
	a.frameNow = c.Now()
	a.winFullscreen = a.Win != nil && windowFullScreen(a.Win)
	// 一帧的边沿观察全放在构建之前：收尾总是先发生，这一帧建出来的界面
	// 看到的就是收好的状态。写在构建里的话，同一个 build 里先建的元素看到
	// 旧的、后建的看到新的——那正是「构建值是临时的」这条规矩要挡的东西。
	a.tickActivity()
	a.tickLeavePlayer()

	// 菜单的表现形式：系统的（默认），或者画在窗口里。
	//
	// 画在窗口里的菜单，条目是元素——读屏和 automation 都看得见。e2e 要
	// 驱动右键菜单（把直播间收进观测墙）只能走这一档；系统的 Win32 弹出
	// 菜单不在这套自绘元素树里。
	if a.drawnMenus {
		c.SetMenuPresentation(ui.PopoverMenus)
	}

	// 系统外观要在 SetTheme 之前读：SetTheme 之后 Context 返回的是我们这套
	// 配色，就再也看不到系统是深色还是浅色了。
	a.syncThemeMode(c.Theme().Dark)
	// 根元素先建出来：主题切换的进度挂在它身上，0→1 的 AnimateTo 会
	// 替我们请求帧。颜色在 applyTheme 里按进度混合，再给根和内容用。
	root := ui.Box(c).Fill()
	a.applyTheme(c, root)
	c.SetTheme(theme.Theme())

	// 进入关注页那一帧的边沿：从别处过来才刷一次，不是每帧问。
	// 放在这里和 tickSurface 同一层——读的是路由这个事实，做的是它的
	// 一次性收尾，不写在任何页面的渲染路径里。
	if p := a.Router.Path(); p != a.lastRoute {
		a.lastRoute = p
		if p == routeFollows {
			a.FollowsEntered()
		}
	}

	// 表面之间的连续过渡到点收尾已在构建前跑过（tickRouteTransition）。
	// 这里只剩全尺寸视图出现/消失的边沿：断弹幕、拨看门狗基准、重求呈现
	// 尺寸。真相由 fullSize() 推导，这里只做一次性收尾。
	a.tickSurface()
	// 呈现尺寸每帧从 presentSizeFor 求一次，值没变就不推。墙出现/消失、
	// 全屏切换、后台收听都走这一条，不再有第二条推送路径。
	a.applyPresent()

	// 各平台登录头像提前进缓存：切平台换头像时才不会先画灰人再跳图。
	a.syncAuthAvatars()
	// 挂起的切平台在这里收尾：目标平台的首屏数据和封面齐了才换。
	a.tickPlatformSwitch(c)

	// Ctrl/Cmd + K 唤起搜索；输入框里打字时不抢
	// 没有平台上下文的页面连搜索入口都没有，快捷键也不该把它打开。
	if a.searchAvailable() && !c.FocusTakesText() && c.Shortcut(ui.Cmd, ui.KeyK) {
		a.SearchOpen = true
	}

	// 离开播放页的窗口级收尾在 tickLeavePlayer 里（构建之前）。
	// 注意它不管媒体层：后台收听要的正是「界面收了、声音留着」，引擎的
	// 开关由焦点的存活决定（setFocus / closeFocus / tickActivity）。

	// 播放页的两种全屏下应用外壳整体让位给画面（外壳是 position:fixed
	// inset:0 z-index:9999，顶栏和灵动岛都被盖住）。
	// 窗口全屏是我们自己的状态，原生全屏是系统状态，两者都算。
	fullscreen := a.playerFullscreen()

	a.wasPlayerFullscreen = fullscreen

	root.Background(theme.BgPrimary).Children(func() {
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
			// 只有 X / 不在播放页的 Esc 才真关——它们走 closeFocus。Esc 在
			// 播放页收进岛，顶栏 tab、点岛回直播间也都保留这一路的声音，
			// 回来时接着看。
			//
			// 进/出播放页的收尾不在这里逐条判断：fullSize() 的边沿由
			// tickSurface 统一处理（Shell 开头），呈现尺寸也由它一处求值。
			//
			// 墙不在了只是标记；真正离开墙（去首页 / 别的分区）才停掉墙上的
			// 预览。播放页不算离开：从墙上点进某一间，背后还是这面墙，退出
			// 就回来了，其余格子继续播。
			if !a.onWatchWall() {
				a.watchHide()
				if a.Router.Path() != routePlayer {
					a.watchPark()
				}
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
		// 后台写盘失败：旗标只置位，这里取一次变成通知。取走即清，
		// 一次会话只提醒一次——持续失败反复弹会盖住别的通知。
		if a.stateSaveFailed.CompareAndSwap(true, false) && !a.stateSaveWarned {
			a.stateSaveWarned = true
			a.toastTone("设置没能保存，重启后可能丢失", toastError)
		}
		// 通知：先认领本帧排队的条目，再画视口。视口挂在 MyGo 的 overlay
		// 层上，排在整棵视图之后——不管当前是哪个表面、模态开没开，它都
		// 在最上面。视口常驻，最后一条也要把退场过渡播完。
		//
		// 长相与入场交给 Onevra 的 Toasts：它建自己的 ToastViewportBase，
		// 所以这里不能再包一层（toast.go:69-70：先建的视口保留自己的样子）。
		// 队列、计时、悬停暂停、Esc 与读屏播报仍然由 MyGo 管。
		a.flushToasts(c)
		components.Toasts(c, components.ToastOptions{
			Placement: components.ToastBottomEnd,
			Inset:     18,
		})
		// 播放页的快捷键说明
		if a.ShortcutsOpen {
			a.shortcutsOverlay(c)
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
	// 首屏还空着的时候，读屏用户看到的是一块没有内容的网格——它不会自己
	// 念「正在加载」。启动层以前也没管这件事，现在这块由播报补上。
	//
	// 只播一次：这个分支每帧都会走到，不设闸门就是复读机。
	if !a.bootAnnounced && a.ListLoading && len(a.Rooms) == 0 {
		a.bootAnnounced = true
		c.Announce("正在载入直播间")
	}
	pad := a.bodyPadX(c)
	// 换分区就回到顶部；同一个分区内（去播放页再回来）位置由 ListState 自己记住。
	key := fmt.Sprintf("%s|%d|%d", a.Platform, a.GroupIndex, a.CategoryIndex)
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
			a.errorBar(c, a.ErrText, a.retryLive)
		}
		// 内容层单独换代：切平台 / 分区时换 key，新旧内容直接交换。
		// 分区栏和错误条留在原地——它们是这一屏的边框，不是内容。
		feed := ui.Column(c.Key(a.navFeedKey())).Grow(1)
		feed.Children(func() {
			a.grid(c, a.feed(), &a.HomeList, a.loadMoreRooms)
		})
	})
}

// categoryBarSkeleton 是分区栏的加载占位：和真面板同高、同圆角。
func (a *App) categoryBarSkeleton(c *ui.Context) {
	ui.Row(c).Height(52).Padding(0, 16, 0, 16).Radius(theme.RadiusXL).
		Background(theme.BgSecondary).Gap(10).
		AlignItems(ui.Center).Children(func() {
		components.Text(c, "正在加载分区…").FontSize(theme.TextSM).
			TextColor(theme.TextTertiary)
	})
}

// bodyPadX 是正文左右 padding：常态 14，1240 以下收到 10。
func (a *App) bodyPadX(c *ui.Context) float32 {
	w, _ := c.Size()
	if w <= 1240 {
		return 10
	}
	return 14
}

// retryLive 是错误条和空态两处「重试」的共同实现。
//
// 分区树加载失败时 Groups 为空，currentCategoryID() 返回 ""，只调 loadRooms
// 会在判空处直接 return，一个请求都不发；而 liveLoaded 早已是 true，
// ensureLiveData 每帧都跳过它。结果就是点了「重试」什么也没发生，首页永远
// 停在空态。所以没有分区树时先重拉分区树，并清掉已加载标记，让整条加载
// 流程重新走一遍。
func (a *App) retryLive() {
	a.ErrText = ""
	if !a.LiveMode {
		a.Loading = true
		return
	}
	if a.currentCategoryID() == "" {
		if a.liveLoaded != nil {
			delete(a.liveLoaded, a.Platform)
		}
		a.loadCategories(a.Platform)
		return
	}
	a.loadRooms()
}

// errorBar 是加载失败的提示条。文案和重试动作由调用方给：
// 首页和订阅分区各有各的错误字段与重发路径，共用一条错误条时会显示成
// 另一个分区的文案、点重试也去重拉另一个分区。
//
// 尺寸与配色：padding 10 14、圆角 12、background color-mix(danger 8%, bg-secondary)，
// 右侧一枚 30 高的重试按钮。
func (a *App) errorBar(c *ui.Context, text string, onRetry func()) {
	ui.Row(c).Padding(10, 14, 10, 14).Gap(12).Radius(theme.RadiusMD).
		AlignItems(ui.Center).Background(theme.BgSecondary.Mix(theme.Danger, 0.08)).
		Children(func() {
			components.Text(c, text).FontSize(theme.TextSM).TextColor(theme.TextPrimary).Grow(1)
			retry := components.Button(c, "", components.ButtonOutline)
			retry.Height(30).Padding(0, 14).Radius(theme.RadiusFull).
				FontSize(theme.TextXS).Label("重试")
			if retry.Clicked() && onRetry != nil {
				onRetry()
			}
			retry.Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					components.Icon(c, iconSync).FontSize(12).TextColor(theme.DangerInk)
					components.Text(c, "重试").FontWeight(600).TextColor(theme.DangerInk)
				})
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
	return a.filterStreamers(a.Platform, a.currentCategory())
}

// feedFor 返回订阅分区某个订阅项下的直播间。
func (a *App) feedFor(sub Subscription) []Streamer {
	if sub.PlatformID == "bilibili" && len(a.Remote) > 0 {
		return a.Remote
	}
	return a.filterStreamers(sub.PlatformID, sub.Name)
}

func (a *App) filterStreamers(platform, category string) []Streamer {
	out := make([]Streamer, 0, 16)
	for _, s := range a.Streamers {
		switch {
		case s.Platform != platform:
		case category != "" && s.Category != category:
		default:
			out = append(out, s)
		}
	}
	return out
}

// gridView 是网格外层的分区状态：是否加载中、空态里的平台文案与重试动作。
// 首页和订阅分区共用同一套网格几何，但加载态和空态入口不能共用——订阅
// 分区曾经读首页的 ListLoading/retryLive，骨架和空态都画错分区。
type gridView struct {
	loading       bool
	loadingMore   bool
	hasMore       bool
	platform      string
	retry         func()
	allCategories bool
}

// grid 铺卡片网格。可选的 view 描述这一层网格属于哪个分区；不传时按首页
// 处理（首页是默认调用方，现有调用点都不用改签名）。
//
// 网格是 `grid-template-columns: repeat(auto-fill, minmax(220px, 1fr))`：
// 列数随可用宽度自动增减、每列等宽、内容区始终满宽。
// 这里先按同一条公式自己算出列数与卡片宽度，再用 Grid 的固定列数落下——
// GridView 内部还叠了一层自己的取整，会少一列、右侧留白。
//
// 列表交给 ui.List 做虚拟化：一次只构建可视范围内的那几行卡片。
// 滑到第 3000 张和第 30 张的每帧成本一样——这是「一直往下滑」的前提。
// 行高由 List 自己测量（我们的卡片高度是算出来的固定值），不用手动喂。
func (a *App) grid(c *ui.Context, list []Streamer, ls *ui.ListState, onMore func(), views ...gridView) {

	view := gridView{
		loading:       a.ListLoading,
		loadingMore:   a.ListLoadingMore,
		hasMore:       a.ListHasMore,
		platform:      a.Platform,
		retry:         a.retryLive,
		allCategories: true,
	}
	if len(views) > 0 {
		view = views[0]
	}
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

	// 空列表不需要虚拟化。
	//
	// 正在取列表时**什么都不画**：切分区后屏幕上只剩页面底色，新数据到了直接
	// 出现。以前这里画 12 张骨架卡（灰封面 + 灰头像 + 两条灰文字），那是同一块
	// 区域里的第三种颜色——旧列表 → 骨架灰 → 新列表，多出来的那一次灰就是
	// 用户报的「切换分区画面闪烁」。见 loadRooms 里那段注释。
	if len(list) == 0 {
		if view.loading {
			ui.Box(c).Grow(1)
			return
		}
		components.ScrollView(c, components.ScrollViewOptions{}).Grow(1).Children(func() {
			a.emptyGrid(c, view)
		})
		return
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
			a.gridFooter(c, avail, len(list), view)
			return
		}
		ui.Row(c).Gap(theme.GridGap).Children(func() {
			for i := r * cols; i < min(len(list), (r+1)*cols); i++ {
				i := i
				st := list[i]
				Card(c, st, cardW, a.isFollowed(st), func() {
					a.openRoom(st)
				}, func() {
					a.toggleFollow(st)
				}, func() {
					a.watchAdd(st)
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

	// 内容铺不满一屏时列表没有可滚动范围，EndReached 永远不会来，下一页
	// 就永远取不到。抖音一页 40 条在超大窗口里也可能刚好不够一屏，这跟
	// 「用户滚到底」无关，是布局本身没有可滚的余量。
	//
	// AtEnd 读的是上一帧的布局，所以数据刚到的那一帧不会误判。onMore 自己
	// 用 ListLoadingMore 去重：同一次事件重建的多帧里只会真正发起一次。
	if onMore != nil && a.LiveMode && ls.AtEnd() && view.hasMore &&
		!view.loadingMore && !view.loading && a.topUpAllowed() {
		onMore()
	}

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

// emptyGrid 是列表的空态：标题 + 提示 + 按钮。文案里的平台和按钮动作
// 都跟着调用方的分区走，订阅分区不提供只属于首页的「查看全部分区」。
func (a *App) emptyGrid(c *ui.Context, view gridView) {
	hint := "换个分区看看，或者稍后重试。"
	if a.LiveMode {
		if _, ok := live.ProviderFor(live.Platform(view.platform)); !ok {
			hint = "该平台暂未接入真实数据。"
		}
	}
	ui.Column(c).Padding(88, 16, 88, 16).Gap(8).Center().Children(func() {
		components.Text(c, "这里还没有直播间").FontSize(theme.TextMD).FontWeight(600).
			TextColor(theme.TextSecondary)
		components.Text(c, hint).FontSize(theme.TextSM).
			TextColor(theme.TextTertiary)
		ui.Row(c).Gap(10).Padding(10, 0, 0, 0).Children(func() {
			if view.allCategories {
				// 按钮交给 Onevra：尺寸、圆角、字号都在返回的元素上覆盖，
				// 字色与底色来自主题（Primary 就是 Accent + AccentText）。
				// 换掉的是悬停反馈：原来走 hoverSurfaceRest 的弹簧渐亮，
				// Onevra 在构造期把 hover 面定稿，这里是瞬时切换。
				clear := components.Button(c, "查看全部分区", components.ButtonPrimary)
				clear.Height(34).Padding(0, 16).Radius(theme.RadiusFull).FontSize(theme.TextSM)
				if clear.Clicked() {
					// 换到「全部」是换了一份列表：必须重新取。
					a.reloadRooms()
				}
			}

			retry := components.Button(c, "重新加载", components.ButtonSecondary)
			retry.Height(34).Padding(0, 16).Radius(theme.RadiusFull).FontSize(theme.TextSM)
			if retry.Clicked() && view.retry != nil {
				view.retry()
			}
		})
	})
}

// gridFooter 是列表底部那行状态：加载中 / 已显示最近 N 条。
func (a *App) gridFooter(c *ui.Context, avail float32, n int, view gridView) {
	ui.Row(c).Width(avail).Height(52).Padding(4, 0, 18, 0).Center().
		Children(func() {
			// 页脚的三态：加载中 / 还有下一页 / 到底了
			switch {
			case view.loadingMore:
				ui.Row(c).Gap(8).Center().Children(func() {
					components.Spinner(c, components.SpinnerRing).Size(14, 14)
					components.Text(c, "正在加载更多…").FontSize(theme.TextSM).
						TextColor(theme.TextSecondary)
				})
			case view.hasMore:
				components.Text(c, "继续下滑加载更多").FontSize(theme.TextXS).
					LetterSpacing(0.02).TextColor(theme.TextTertiary)
			default:
				components.Text(c, fmt.Sprintf("已显示最近 %d 条", n)).FontSize(theme.TextXS).
					LetterSpacing(0.02).TextColor(theme.TextTertiary)
			}
		})
}

// ============================================================ 顶栏

// chromeH 是顶栏三种托盘里所有按钮的内高。托盘高 = chromeH + 2×3。
//
// 三组托盘曾经各用各的：轨道 34、图标按钮 38、窗口按钮 34，窄屏又是 36/34/32。
// 同一条 64 高的栏里，三个盒子高度各不相同，眼睛先看见的就是这个。
const chromeH float32 = 34

// Navbar 是自绘标题栏。
//
// 顶栏整条高 64、左右 padding 14、gap 14、背景就是 bg-primary；
// 左侧平台 tablist，右侧一组 actions（gap 10）：工具组 → 窗口按钮组。
// 三组托盘同一条配方：surface-chrome 底、3px 内边距、无描边。
// 以前这里有一根 1px 分隔线，那是窗口按钮还没有自己的托盘时留下的；
// 两边都有托盘之后它就是第二道边。
// navbarMetrics 是顶栏的几何：两档断点下的内边距、间距、tab 宽、窗口按钮宽。
//
// 它存在的唯一理由是**只有一个来源**。顶栏自己按一套数排版，岛的宽度上限
// 又按另一套数算「中间还有多少可用」，两份抄写迟早会走散——走散的后果是
// 岛压住 tab 或搜索键，或者中间留着一段没人用的空隙。
type navbarMetrics struct {
	pad, gap   float32
	tabW       float32
	captionW   float32
	chromeH    float32
	groupPad   float32 // 托盘自己的内边距（3）
	captionGap float32 // 窗口按钮之间的 2
	rowGap     float32 // 工具胶囊与窗口按钮之间的 10
}

// navbarMetricsFor 按窗口宽度取这一档的顶栏几何。
func navbarMetricsFor(winW float32) navbarMetrics {
	m := navbarMetrics{
		pad: 14, gap: 14, tabW: 64, captionW: 40,
		chromeH: chromeH, groupPad: 3, captionGap: 2, rowGap: 10,
	}
	if winW <= 1240 {
		// ≤1240：只收列宽；高度跟另外两组托盘一样，否则顶栏里三个托盘高低不齐
		m.pad, m.gap, m.tabW, m.captionW = 10, 8, 56, 34
	}
	return m
}

// leftReserve 是左侧 tab 轨道占的宽度：托盘内边距 ×2 + 每格等宽 + 外侧一个 gap。
func (m navbarMetrics) leftReserve(tabs int) float32 {
	return m.pad + 2*m.groupPad + m.tabW*float32(tabs) + m.gap
}

// rightReserve 是右侧两组占的宽度：工具胶囊 + 间距 + 窗口按钮托盘 + 外侧一个 gap。
func (m navbarMetrics) rightReserve(tools int) float32 {
	capsule := 2*m.groupPad + m.chromeH*float32(tools)
	if tools > 1 {
		capsule += 4 * float32(tools-1)
	}
	captions := 2*m.groupPad + 3*m.captionW + 2*m.captionGap
	return m.pad + capsule + m.rowGap + captions + m.gap
}

// Navbar 是顶栏。
func (a *App) Navbar(c *ui.Context) {
	w, _ := c.Size()
	m := navbarMetricsFor(w)
	bar := ui.Row(c).Height(theme.NavbarHeight).Padding(0, m.pad, 0, m.pad).Gap(m.gap).
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
				Background(theme.SurfaceChrome).Children(func() {
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

			a.windowControls(c)
		})
	})
}

// toastTone 把一条通知排进队列：success / info / error 决定图标和图标颜色。
func (a *App) toastTone(msg, tone string) {
	if msg == "" {
		return
	}
	a.toastQueue = append(a.toastQueue, ui.Toast{
		// 同一条消息重复触发只刷新那一条的计时，不叠第二条。
		ID:      msg,
		Title:   msg,
		Type:    tone,
		Timeout: toastLife(tone),
	})
	a.invalidateWindows()
}

// toastLife 是通知停留的时长。错误要给足读完的时间，其余保持原来的 3.2 秒。
func toastLife(tone string) time.Duration {
	if tone == toastError {
		return 6 * time.Second
	}
	return 3200 * time.Millisecond
}

// flushToasts 把队列里的通知交给 MyGo 的 toast 管理器，然后清空。
//
// 认领即出队：一次事件可能重建好几帧，后续帧拿到的是空队列，不会重弹。
func (a *App) flushToasts(c *ui.Context) {
	if len(a.toastQueue) == 0 {
		return
	}
	q := a.toastQueue
	a.toastQueue = nil
	for _, t := range q {
		c.AddToast(t)
	}
}

// toast 的三种色调。
const (
	toastSuccess = "success"
	toastInfo    = "info"
	toastError   = "error"
)

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
func (a *App) switchTab(c *ui.Context, id string) {
	if id == "follows" {
		a.pendingSwitch = ""
		if a.Router.Path() == routeFollows {
			return
		}
		a.pushPage(routeFollows)
		return
	}
	if id == a.Platform {
		// 平台没换，但页面可能在别处：从关注 / 播放页点回当前平台的
		// tab，要回首页。平台本身没换，所以内容不播换向。
		if a.Router.Path() == routeHome {
			return
		}
		a.pendingSwitch = ""
		a.pushPage(routeHome)
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
	a.pendingTries = 0
	// 用计时器问下一次，不跨帧比时间戳：问够 platformSwitchTries 次就不再等。
	c.After(platformSwitchPoll)
}

// 切平台等目标平台首屏就绪：每 platformSwitchPoll 问一次，问够
// platformSwitchTries 次就不再等——宁可闪一次，也不能让点击几百毫秒没有
// 内容上的回应。用计时器计数，不跨帧比较时间戳。
const (
	platformSwitchPoll  = 50 * time.Millisecond
	platformSwitchTries = 14 // ≈ 700ms
)

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

// tickPlatformSwitch 在挂起的切换就绪（或问够了）时真正切过去。
func (a *App) tickPlatformSwitch(c *ui.Context) {
	if a.pendingSwitch == "" {
		return
	}
	id := a.pendingSwitch
	if a.platformSwitchReady(id) || a.pendingTries >= platformSwitchTries {
		a.pendingSwitch = ""
		a.commitPlatformSwitch(id)
		return
	}
	a.pendingTries++
	c.After(platformSwitchPoll)
}

// commitPlatformSwitch 执行真正的切换：换数据源、换显存、换路由。
func (a *App) commitPlatformSwitch(id string) {
	a.navFlip(&a.navSeq)
	a.Platform = id
	// 账号是分平台的，头像浮层里的内容会跟着换人；换 tab 就关掉，
	// 别让上一家的二维码和昵称留在屏幕上。
	a.AuthOpen = false
	// 换平台了，旧平台的纹理对新平台没有任何用处：立刻清掉 L1。
	// 但要保住新平台已经预取好的首屏封面，否则用户还是要等一遍解码。
	// 原始字节（L2）全部留着——切回去时能直接解码，不用重新下载。
	media.Default.ReleaseTexturesExcept(a.feedURLs(id))
	// 切平台清空搜索，上一个平台的结果不能留在浮层里。
	// 代次 +1 让还在路上的那次请求作废。
	a.SearchQuery, a.SearchResults, a.SearchErr, a.SearchLoading = "", nil, "", false
	a.SearchHit = 0
	a.searchSeq.Add(1)
	a.pushPage(routeHome)
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

	// tab 宽走 navbarMetrics：岛在算自己的宽度上限时用的是同一个数，
	// 两份抄写迟早会走散。高度跟另外两组托盘一样，否则顶栏里三个托盘
	// 高低不齐。
	winW, _ := c.Size()
	tabW := navbarMetricsFor(winW).tabW
	tabH := chromeH
	// 滑块跟着当前页面（关注 / 订阅 / 平台）走。
	selected := 0
	active := a.activeTab()
	for i, t := range tabs {
		if t.id == active {
			selected = i
			break
		}
	}

	// 轨道和右侧两组按钮是同一类容器：surface-chrome 底、3px 内边距、无描边。
	//
	// 这里先后踩过两次：先在浅色主题下写死 #ffffff，顶栏左边白、右边灰；
	// 后来改用 fill-quaternary，那是相对卡片定的浅填充，放到地上只剩 1.9
	// 个 L*，于是又补了 1px 描边——形状是线画的，不是色块画的。
	// surface-chrome 是地和卡片的 L* 中点，托盘自己就站得住。
	track := ui.Row(c).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
		Background(theme.SurfaceChrome)

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
						a.switchTab(c, t.id)
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
					// 未选中的 tab 悬停时铺一块 FillTertiary：右侧工具组和
					// 窗口按钮用的是同一支，两边都坐在同一种 SurfaceChrome
					// 托盘里。以前这里只把字色提亮，整颗胶囊是空的，看起来
					// 和右边不是一套语言。
					//
					// 选中项不叠底：它的底由 Accent 滑块画，tab 压在上面
					// （ZIndex 1），再铺一层就把滑块盖掉了。
					tab.Background(hoverSurfaceOn(tab, "nav-tab-bg", tab.Hovered(), idx != selected, theme.FillTertiary))
					rest, on := theme.TextTertiary, theme.TextPrimary
					if idx == selected {
						rest, on = theme.AccentText, theme.AccentText
					}
					col := hoverInk(tab, "nav-tab-ink", tab.Hovered(), rest, on)
					tab.Children(func() {

						switch t.id {
						case "follows":
							// 选中时实心星，和滑块一起构成「这里是关注页」
							ico := iconStar
							if idx == selected {
								ico = iconStarFilled
							}
							components.Icon(c, ico).FontSize(14).TextColor(col)
							return
						}
						txt := components.Text(c, t.label).FontSize(theme.TextSM).TextColor(col)
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

// iconButton 是工具组里的一枚图标按钮：chromeH 见方，图标 18。
func (a *App) iconButton(c *ui.Context, ico *ui.SVG, label string, on func()) {
	size := chromeH
	btn := ui.Box(c).Size(size, size).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
	hovered := btn.Hovered()
	bg := ui.Transparent
	if hovered {
		bg = theme.FillTertiary
	}
	btn.Background(btn.AnimateColor("icon-btn-bg", bg, theme.HoverInk()))
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		col := theme.TextSecondary
		if hovered {
			col = theme.TextPrimary
		}
		components.Icon(c, ico).FontSize(18).TextColor(col)
	})
}

// windowControls 是右上角的最小化 / 最大化 / 关闭。
//
// 一组托盘：外层 3px padding + surface-chrome 底、无描边，
// 里面三枚 40×chromeH 的圆角按钮，图标 16px、1.8 描边，关闭键 hover 变红。
func (a *App) windowControls(c *ui.Context) {
	ui.Row(c).Gap(2).Padding(3, 3, 3, 3).Radius(theme.RadiusFull).
		Background(theme.SurfaceChrome).
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
	winW, _ := c.Size()
	w, h := navbarMetricsFor(winW).captionW, chromeH
	btn := ui.Box(c).Size(w, h).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer).Label(label)
	hovered := btn.Hovered()
	if hovered {
		if danger {
			btn.Background(theme.MediaDangerInk)
		} else {
			btn.Background(theme.FillTertiary)
		}
	}
	if btn.Clicked() {
		on()
	}
	down := btn.Pressed()
	btn.Children(func() {
		col := theme.TextSecondary
		if hovered {
			if danger {
				col = theme.White
			} else {
				col = theme.TextPrimary
			}
		}
		components.Icon(c, ico).FontSize(16).TextColor(col)
		pressDim(c, down, theme.RadiusFull)
	})
}
