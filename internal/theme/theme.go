// Package theme 是全部视觉令牌的唯一来源：颜色、字号、圆角、间距、动效。
//
// 视觉语言一句话：平涂表面 + 色阶分层，只给真正需要边界的浮层加 1px 描边；
// 六族身份（墨白、幕红、屏青、灯暖、科技蓝、应援粉）各有日夜两曝光，
// 强调色只出现在主操作、激活态、LIVE 和当前项。
package theme

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// ---------------------------------------------------------------- 表面色阶

// 这四组颜色由 palette.go 的 specs 推导，init 时按默认配色填一遍。
// 这里不再写十六进制：一份颜色只能有一个来源，否则两套说法迟早对不上。
var (
	// 窗口地面 / 卡片、顶栏、浮层底 / 封面占位、更深一档 / 弹层
	BgPrimary, BgSecondary, BgTertiary, BgElevated ui.Color
	// 顶栏里那类「装一排控件」的托盘：地和卡片的正中间，比地高、比卡片低。
	SurfaceChrome ui.Color
	// 填充四档，从最浅到最深
	FillQuaternary, FillTertiary, FillSecondary, HoverBg ui.Color

	// 文字四档：正文 / 次级 / 三级 / 元数据
	TextPrimary, TextSecondary, TextTertiary, TextMeta ui.Color

	// 强调色：填充、悬停、按下、上面的字、当字用、当线条用
	Accent, AccentHover, AccentActive, AccentText ui.Color
	AccentInk, AccentLine                         ui.Color
	AccentSoft, AccentSofter, AccentGlow          ui.Color
	// AccentOnInk 是强调色压在灵动岛的墨底上那一版。
	//
	// 前两支都对 FillSecondary 推导：浅色主题里它们被压深去当字/当线，压到墨底上
	// 就什么都不剩。环要像光，就得有一支只对墨底负责的颜色。
	AccentOnInk ui.Color

	// 描边与分隔。BorderFunctional 是可点控件的形状边界，对卡片 ≥3:1；
	// 公共 Border 只管分隔和浮层，不承担"这里能点"。
	Border, BorderStrong, BorderFunctional, Separator ui.Color
)

// 状态色三支：fill 用于点、底色和低透明度叠加；ink 用于文字和图标；
// line 用于边框、指示器。成功 / 危险由 Apply 按当前配色写入——色相是语义，
// 明度跟着日夜和配色推导。在线绿是固定值，日夜同一支。
var (
	StatusOnline, StatusOnlineInk, StatusOnlineLine ui.Color
	Success, SuccessInk, SuccessLine                ui.Color
	Danger, DangerInk, DangerLine                   ui.Color

	// 离线态的点：无彩色，明度跟着文字元数据走。
	StatusOffline ui.Color
)

func init() { Apply(Palettes[0].ID) }

// ---------------------------------------------------------------- 固定色

// 这几支不随配色变：白色是压在画面上的墨，岛底是恒定的暗面。
var (
	// 封面上的人数胶囊：rgba(8,9,11,.78)
	OverlayInk = ui.RGBA(8, 9, 11, 0.78)
	// OverlayInkStrong 是封面上关注键悬停时更深的一档：rgba(8,9,11,.90)。
	OverlayInkStrong = ui.RGBA(8, 9, 11, 0.90)
	// IslandInk 是灵动岛的底：纯黑，不跟配色走。
	//
	// 参照物是手机的屏幕挖孔——它永远比界面暗，因为它是硬件，不是卡片。
	// 岛一旦跟着配色变浅，浅色主题里它就只是一块白底上的白片，只能靠描边
	// 和半透明的「流光」找存在感；那两种都试过，最后都读成脏边。
	// 黑底一铺，问题消失：字是亮的、环是亮的、影子有地方落。
	IslandInk = ui.RGB(6, 7, 9)
	// IslandTopLight 是墨岛顶部那 1px 受光边。岛不跟配色走，所以这条高光
	// 也不能借 TopLight——浅色主题里那是一道极淡的墨线，压在黑底上看不见。
	IslandTopLight = ui.RGBA(255, 255, 255, 0.06)
	// IslandHover 是岛上整块热区 hover 时的提亮层。岛是恒定黑底，不跟配色走；
	// 值刚好和 IslandTopLight 一样，但语义是「指针经过」而不是「顶部受光」。
	IslandHover = ui.RGBA(255, 255, 255, 0.06)
	// White / Black 是不跟配色走的纯色基座：白底控件、色块棋盘格、滑杆拇指、
	// 视频舞台的黑底都取它们；要半透明就在调用点用 .Alpha() 叠一层。
	White = ui.RGB(255, 255, 255)
	Black = ui.RGB(0, 0, 0)
	// 白色文字描边 / 高光
	White90 = ui.RGBA(255, 255, 255, 0.90)
	White94 = ui.RGBA(255, 255, 255, 0.94)
	White12 = ui.RGBA(255, 255, 255, 0.12)
	// DanmuStrokeInk 是新用户弹幕的默认描边色：#444，压在任意字色后面当一圈灰边。
	DanmuStrokeInk = ui.RGB(68, 68, 68)
	// AuthQrIconInk 是登录二维码占位图标的灰。#7a7e88 压在固定的白底卡片上。
	AuthQrIconInk = ui.RGB(122, 126, 136)
)

// HexColor 把平台下发的 "#rrggbb" 解析成颜色。
//
// 弹幕字色、醒目留言底色、粉丝牌配色、配色面板的预览带都是运行时的字符串，
// 做不成常量令牌；解析这一步住在 theme 里，internal/ui 就只剩「取令牌」。
func HexColor(hex string) ui.Color { return ui.Hex(hex) }

// ---------------------------------------------------------------- 压在画面上的墨

// 播放器和弹幕面板浮在视频/封面上，那里的颜色不能跟配色走：
// 浅色主题里把面板刷白，字就没了。这一组只按"深底浅字"取，
// 和 Palette 无关，所以不参加对比度审计里那些随配色变的组合。
var (
	// 面板底：rgba(17,20,28,.88)，减少透明时的实色退路
	MediaPanel = ui.RGBA(17, 20, 28, 0.88)
	// 玻璃面板的 tint：比实色轻一档，模糊才看得见
	MediaPanelTint = ui.RGBA(17, 20, 28, 0.76)
	// 面板里的一行：0.05 底 + 0.08 描边
	MediaRow     = ui.RGBA(255, 255, 255, 0.05)
	MediaRowLine = ui.RGBA(255, 255, 255, 0.08)
	// 发丝线 / 分隔
	MediaHair = ui.RGBA(255, 255, 255, 0.12)
	// 浮在画面上的阴影：小浮层 / 控制条。
	// 之前 0.35 / 0.40 / 0.70 三个值混着用，还把一个"压暗"的令牌当阴影色。
	MediaShadow       = ui.RGBA(0, 0, 0, 0.35)
	MediaShadowStrong = ui.RGBA(0, 0, 0, 0.70)
	// 滑杆轨道、未选中的滑块
	MediaTrack = ui.RGBA(255, 255, 255, 0.15)
	// 面板上的字：主 / 正文 / 次 / 极弱。
	// 之前这里有 0.86、0.85、0.82、0.68、0.60、0.40、0.35 七个值，
	// 相差不到 3% 的透明度分不出层级，只是各自的随手数。
	MediaText       = ui.RGBA(255, 255, 255, 0.92)
	MediaTextBody   = ui.RGBA(255, 255, 255, 0.85)
	MediaTextDim    = ui.RGBA(255, 255, 255, 0.70)
	MediaTextFaint  = ui.RGBA(255, 255, 255, 0.45)
	MediaTrackFill  = ui.RGBA(255, 255, 255, 0.26)
	MediaHairStrong = ui.RGBA(255, 255, 255, 0.24)
	MediaScrimSoft  = ui.RGBA(0, 0, 0, 0.22)
	// 墨底上的状态色：SuccessInk / DangerInk 是对卡片底推导的，浅色主题
	// 里被压深到接近黑，放到通知的深墨底上就没了对比。通知跨表面，图标
	// 色取深色主题那两支，固定不跟配色走。
	MediaSuccessInk = ui.RGB(0x61, 0xbd, 0x67)
	MediaDangerInk  = ui.RGB(0xdd, 0x52, 0x30)

	// 黑边区的模糊底。数值不是估的，是 2026-11-23 从 B 站播放器 bundle 里
	// 读出来的（room-player.prod.min.js 的 capture-video-bg、H5 的
	// .bgBlur / .background-cover），逐条对应：
	//
	//	filter: blur(35px)         → MediaBackdropBlur
	//	transform: scale(1.2)      → MediaBackdropOverscan
	//	background: rgba(0,0,0,.5) → MediaBackdropScrim
	//
	// 35 是 CSS 像素的绝对值，不随窗口伸缩——B 站也没让它伸缩。
	MediaBackdropBlur     float32 = 35
	MediaBackdropOverscan float32 = 0.2 // 四周各外扩 20%，抵消模糊吸进来的透明边
	MediaBackdropScrim            = ui.RGBA(0, 0, 0, 0.5)
	// MediaBackdropMinBar 是「值得画」的最小黑边：比它窄的多半是 16:9 在
	// 非整像素窗口下的取整余量，为 1–2 DIP 开一层全屏模糊不值。
	MediaBackdropMinBar float32 = 2
	// MediaScrimTile 是观测墙格子底部信息条的压暗层：0.66 才压得住
	// 亮场景的直播画面，同时不把整块画面盖死。
	MediaScrimTile = ui.RGBA(6, 7, 9, 0.66)
	// 控制条 / 小窗控制条的半透明底：rgba(9,10,12,.62)。
	MediaBarBg = ui.RGBA(9, 10, 12, 0.62)
	// 播放器状态层（加载 / 出错 / 未开播）铺满舞台的压暗底：rgba(6,7,9,.82)。
	MediaOverlayScrim = ui.RGBA(6, 7, 9, 0.82)
	// 全屏顶栏返回圆钮的底色，悬停再深一档。
	MediaCapsuleBg      = ui.RGBA(10, 10, 12, 0.62)
	MediaCapsuleBgHover = ui.RGBA(10, 10, 12, 0.72)
	// 数据同步模态的遮罩底：rgba(4,5,7,.72)。
	MediaModalScrim = ui.RGBA(4, 5, 7, 0.72)
	// 状态层白色主按钮上的深色图标与文字：近墨的 #0b0c08。
	MediaOverlayBtnInk = ui.RGB(0x0b, 0x0c, 0x08)
	// 状态层白色主按钮悬停时的浅灰底：#f1f1f1。
	MediaOverlayBtnHover = ui.RGB(0xf1, 0xf1, 0xf1)
	// 弹幕设置开关打开时、强调色底上的深色拇指：#1a1a1a。
	// 全屏窗口按钮里的关闭键悬停红，取 Win11 自己的 #c42b1c。
	MediaCaptionClose = ui.RGB(0xc4, 0x2b, 0x1c)
	// 平台品牌色只用于观测墙的平台胶囊，不随主题变。胶囊上的字统一用
	// IslandInk：四个品牌色配深色字的对比度都比白字高。
	BrandBilibili = ui.RGB(0xFB, 0x72, 0x99)
	BrandDouyu    = ui.RGB(0xFF, 0x6A, 0x00)
	BrandHuya     = ui.RGB(0xFF, 0xB3, 0x00)
	BrandDouyin   = ui.RGB(0xFE, 0x2C, 0x55)
)

// ---------------------------------------------------------------- 半透明叠加

var (
	// 平涂表面顶部那 1px 受光边：切换配色时由 Apply 按明暗换成合适的值。
	TopLight ui.Color

	// 阴影阶梯：两级一层——大而散的环境光 + 小而实的接触影。
	//
	// 单层阴影只能选"浮不起来"或"像贴纸"：0 22 56 -22 这样的散影在白底上
	// 几乎看不见，压重了又没有接触感。Apple 的影子一直是两层。
	// ShadowPanel 是下拉和气泡，ShadowModal 是面板、登录层和灵动岛。
	ShadowPanel   ui.Color
	ShadowModal   ui.Color
	ShadowContact ui.Color
)

// ---------------------------------------------------------------- 尺度

const (
	// 四档圆角：控件 8 / 面板与卡片 12 / 大容器 16 / 胶囊 999
	RadiusXS   float32 = 8
	RadiusMD   float32 = 12
	RadiusXL   float32 = 16
	RadiusFull float32 = 999

	// 4px 基线
	Space1 float32 = 4
	Space2 float32 = 8
	Space3 float32 = 12
	Space4 float32 = 16
	Space5 float32 = 24

	NavbarHeight float32 = 64
	ControlH     float32 = 40
	// 岛在顶栏里垂直居中：(NavbarHeight - IslandH) / 2。这里不给间距常量——
	// 曾经有一个 IslandGap=10，那是照 44 高的返回钮算的，岛只有 38 高，
	// 于是整体偏上 3px，和顶栏右侧的账号头像对不齐。
	IslandW float32 = 100
	IslandH float32 = 38

	// 首页卡片网格
	CardMinWidth float32 = 220
	GridGap      float32 = 16
	CardMetaH    float32 = 60 // 信息条名义高度；真实高度由两行文字撑到 64.33（internal/ui/card.go）

	// ScrollbarW 是 `::-webkit-scrollbar { width: 10px }`。
	// 浏览器里滚动条常驻，占掉内容宽度；MyGo 的滚动条画在内容上不占位，
	// 所以正文区域自己让出这条宽度，列宽才准。
	ScrollbarW float32 = 10
)

// ---------------------------------------------------------------- 字号

const (
	Text2XS float32 = 11
	TextXS  float32 = 12
	TextSM  float32 = 13
	TextMD  float32 = 15
	TextLG  float32 = 18
	TextXL  float32 = 24
	Text2XL float32 = 32
)

// ---------------------------------------------------------------- 主题

// EaseOut 是 `--ease-out`：cubic-bezier(0.22, 1, 0.36, 1)。
//
// 起步快、收尾缓；MyGo 的 spring 尾段拖得长，观感明显更慢，
// 所以所有 CSS 里写了 `--dur-N var(--ease-out)` 的过渡都用它。
func EaseOut(d time.Duration) ui.Animation {
	return ui.Animation{Duration: d, Ease: ui.CubicBezier(0.22, 1, 0.36, 1)}
}

// EaseInOut 是 `--ease-in-out`：cubic-bezier(0.4, 0, 0.2, 1)。
func EaseInOut(d time.Duration) ui.Animation {
	return ui.Animation{Duration: d, Ease: ui.CubicBezier(0.4, 0, 0.2, 1)}
}

// Slide 是「换了一个选项」这一族动效的弹簧：滑块移动、标签着色、内容换向。
//
// 为什么不沿用 EaseOut：曲线在目标变化时只能从当前位置重新起跑，速度归零；
// 连点两下 tab，滑块会先顿住再走。弹簧把当前速度带进新目标，折返是连续的。
//
// 参数取临界阻尼附近（2√460 ≈ 42.9）：停得干脆、不过冲。EaseOut 的注释说
// 弹簧尾段拖得长，那是 Duration/Bounce 那一组的默认尾段；这里的刚度更高、
// 阻尼更贴临界，一格 tab 的距离（60 上下）实测 250ms 内停稳。
func Slide() ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{Stiffness: 460, Damping: 42, Mass: 1}}
}

// SlideFar 是滑块跨过整行时的同一枚弹簧，只是慢一点：
// 从第一个分区跳到第二十个，距离长了，同样的刚度会显得暴冲。
func SlideFar() ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{Stiffness: 260, Damping: 32, Mass: 1}}
}

// Theme 按当前配色返回控件主题：窗口背景、正文色与控件度量。
func Theme() *ui.Theme {
	t := ui.DarkTheme()
	if current != nil && !current.Dark {
		t = ui.LightTheme()
	}
	t.Background = BgPrimary
	t.Text = TextPrimary
	t.TextMuted = TextTertiary
	t.Surface = BgSecondary
	t.SurfaceHover = HoverBg
	t.Border = Border
	t.Accent = Accent
	t.AccentHover = AccentHover
	t.AccentPressed = AccentActive
	t.AccentText = AccentText
	t.Radius = RadiusMD
	t.Spacing = 4 // 4px 基线
	t.FontSize = TextMD
	// `::-webkit-scrollbar`（base.css）：轨道 10 宽、底色透明，
	// 滑块靠 3.5 的透明边框挤成视觉 3px、圆角胶囊，平时是 text-primary 18%，
	// 指针压上去（或拖动）时变成 accent 实色。
	t.ScrollbarStyle = ui.ScrollbarStyle{
		TrackWidth: 10,
		ThumbWidth: 3,
		MinLength:  24,
		Flush:      true,
		Thumb:      TextPrimary.Alpha(0.18),
		Hover:      Accent,
		Active:     Accent,
	}
	return t
}
