// Package theme 把 DTV baseline 的设计令牌（src/styles/theme.css）原样搬到 Go。
//
// 视觉语言一句话：平涂表面 + 色阶分层，只给真正需要边界的浮层加 1px 描边；
// 石墨黑底 + 暖白字 + 信号青柠（chartreuse），青柠只出现在主操作、激活态和 LIVE。
package theme

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// ---------------------------------------------------------------- 表面色阶

var (
	BgPrimary   = ui.Hex("#0a0b0d") // 窗口地面
	BgSecondary = ui.Hex("#14161b") // 卡片 / 顶栏 / 浮层底
	BgTertiary  = ui.Hex("#1c1f25") // 封面占位、更深一档
	BgElevated  = ui.Hex("#191c22") // 弹层
	SidebarBg   = ui.Hex("#0c0e11")

	FillQuaternary = ui.Hex("#16181d")
	FillTertiary   = ui.Hex("#1e2128")
	FillSecondary  = ui.Hex("#272b33")
	HoverBg        = ui.Hex("#1e2128")
)

// ---------------------------------------------------------------- 文字

var (
	TextPrimary   = ui.Hex("#f4f4ef")
	TextSecondary = ui.Hex("#a7aab2")
	TextTertiary  = ui.Hex("#8b909a")
	TextMeta      = ui.Hex("#737884")
)

// ---------------------------------------------------------------- 强调色（信号青柠）

var (
	Accent       = ui.Hex("#d8ff4f")
	AccentHover  = ui.Hex("#e6ff7c")
	AccentActive = ui.Hex("#c2ed2f")
	AccentText   = ui.Hex("#0b0c08") // 青柠上的字
	AccentInk    = ui.Hex("#d8ff4f") // 青柠当文字用
	AccentSoft   = ui.RGBA(216, 255, 79, 0.12)
	AccentSofter = ui.RGBA(216, 255, 79, 0.07)
	AccentGlow   = ui.RGBA(216, 255, 79, 0.20)
)

// ---------------------------------------------------------------- 描边与分隔

var (
	Border       = ui.Hex("#24272e")
	BorderStrong = ui.Hex("#343842")
	Separator    = ui.Hex("#1f2229")
)

// ---------------------------------------------------------------- 状态色

var (
	StatusLive    = ui.Hex("#ff4757")
	StatusOffline = ui.Hex("#5c6169")
	// 关注面板头像右下角的在线点：所有配色都是同一个绿
	StatusOnline = ui.Hex("#22c55e")
	Success      = ui.Hex("#7bdc8a")
	Warning      = ui.Hex("#f5b544")
	Danger       = ui.Hex("#ff5c6c")
)

// ---------------------------------------------------------------- 半透明叠加（来自 CSS 的 rgba 值）

var (
	// 平涂表面顶部那 1px 受光边：inset 0 1px 0 rgba(255,255,255,.04)。
	// 切换配色时由 Apply 按配色的明暗换成合适的值。
	TopLight = ui.RGBA(255, 255, 255, 0.04)

	// 阴影令牌：baseline 的 --shadow-popover / --shadow-lg。
	// 深色主题近乎纯黑；浅色主题是低透明度的蓝黑（daylight / paper 的值）。
	// 由 Apply 按配色明暗切换——浅色主题套用深色阴影会压出一圈脏灰。
	ShadowPopover = ui.RGBA(0, 0, 0, 0.80)
	ShadowLg      = ui.RGBA(0, 0, 0, 0.85)
	// 封面上的人数胶囊：rgba(8,9,11,.78)
	OverlayInk       = ui.RGBA(8, 9, 11, 0.78)
	OverlayInkStrong = ui.RGBA(8, 9, 11, 0.90)
	// 播放器控制条：rgba(10,11,13,.92)
	PlayerGlass = ui.RGBA(10, 11, 13, 0.92)
	// 白色文字描边 / 高光
	White90 = ui.RGBA(255, 255, 255, 0.90)
	White94 = ui.RGBA(255, 255, 255, 0.94)
	White12 = ui.RGBA(255, 255, 255, 0.12)
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
	CardMetaH    float32 = 60 // 封面下方的信息条高度

	// ScrollbarW 是 baseline 的 `::-webkit-scrollbar { width: 10px }`。
	// 浏览器里滚动条常驻，占掉内容宽度；MyGo 的滚动条画在内容上不占位，
	// 所以正文区域自己让出这条宽度，列宽才和 baseline 算出来的一致。
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

// EaseOut 是 baseline 的 `--ease-out`：cubic-bezier(0.22, 1, 0.36, 1)。
//
// 起步快、收尾缓；MyGo 的 spring 尾段拖得长，观感明显更慢，
// 所以所有 CSS 里写了 `--dur-N var(--ease-out)` 的过渡都用它。
func EaseOut(d time.Duration) ui.Animation {
	return ui.Animation{Duration: d, Ease: ui.CubicBezier(0.22, 1, 0.36, 1)}
}

// EaseInOut 是 baseline 的 `--ease-in-out`：cubic-bezier(0.4, 0, 0.2, 1)。
func EaseInOut(d time.Duration) ui.Animation {
	return ui.Animation{Duration: d, Ease: ui.CubicBezier(0.4, 0, 0.2, 1)}
}

// Theme 按当前配色返回控件主题：窗口背景、正文色与控件度量都对齐 baseline。
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
	t.Spacing = 4 // baseline 的 4px 基线
	t.FontSize = TextMD
	// baseline 的 `::-webkit-scrollbar`（base.css）：轨道 10 宽、底色透明，
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
