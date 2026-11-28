package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/icons/lucide"
)

// lucide 的线性图标（24×24 网格、2px 描边、圆头圆角），
// 路径与 lucide-react 是同一套，所以粗细和比例能直接对上。
//
// MyGo 的 SVG 不支持文字、内嵌图片、pattern、marker、filter，
// 但这条线性图标只有 path / circle / rect，全部在支持范围内。
func iconSVG(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

// iconSVGThin 和 iconSVG 一样，只是描边 1.8：窗口按钮那三个图标是手写的
// 1.8 描边 SVG，不是 lucide 的 2。
func iconSVGThin(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

// ---------------------------------------------------------------- 墨迹归一

// iconInkTarget / iconStroke 是播放页 chrome 那一排的统一度量：任何字形
// 归一到这个墨迹高度、这个描边粗细，整排就只剩一个字号。
//
// 19.5 是被最宽的字形反推出来的：小窗是 22×18 的扁字形，等比放大到 20 高
// 时宽正好 24.4，撑出 24 网格被裁；19.5 时宽 23.7，留在网格里。等比缩放
// 必须保比例，所以上限由最宽的那个决定，不是随便取的一个整数。
const (
	iconInkTarget float32 = 19.5
	// iconStroke 是归一化之后的描边粗细。
	//
	// 字形被缩到 19.5 高，笔画却没有跟着缩——下面 fitSVG 把缩放反除了
	// （stroke/k 配 scale(k)），所以笔画恒等于这个数。于是 19.5 高的图形上
	// 画 2px，相对比例 10.3%；lucide 在 24 网格上画 2px 是 8.3%——同样标 2，
	// 这里看上去粗两成多。24px 那排播放控制条最明显。
	//
	// 1.7 / 19.5 = 8.7%，落在 lucide 那档上。
	iconStroke float32 = 1.7
)

// fitIcon 把一个 lucide 字形归一到 iconInkTarget 高、iconStroke 粗。
//
// 做法是围绕网格中心 (12,12) 等比缩放，并把描边宽度反除——缩放会把笔画
// 一起放大，不反除的话被放大 1.08 倍的喇叭会粗到 2.17。等比缩放保持宽高比，
// 所以小窗本来就宽，归一后仍然宽：那是形状，不是误差。
//
// inkH 是这个字形原始的墨迹高，来自 lucide 的几何（24 网格）。换图标时
// 重量一次——量的是 path 的包围盒，不是字号。归一化后的墨迹高应当和同排
// 其它图标一致，否则一排按钮的字形会一大一小。
func fitIcon(inner string, inkH float32) *ui.SVG {
	return fitSVG(`fill="none"`, iconStroke, inner, inkH)
}

// fitFilledIcon 是实心版本的 fitIcon：播放 / 暂停是填满的字形，只写描边
// 会细成两根线。
func fitFilledIcon(inner string, inkH float32) *ui.SVG {
	return fitSVG(`fill="currentColor"`, 1, inner, inkH)
}

func fitSVG(fill string, stroke float32, inner string, inkH float32) *ui.SVG {
	k := iconInkTarget / inkH
	return ui.MustParseSVG([]byte(fmt.Sprintf(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" %s `+
			`stroke="currentColor" stroke-width="%.4f" stroke-linecap="round" stroke-linejoin="round">`+
			`<g transform="translate(12 12) scale(%.5f) translate(-12 -12)">%s</g></svg>`,
		fill, stroke/k, k, inner)))
}

// starPath 是 lucide Star 的原始路径（node_modules/lucide-react 同名文件）。
const starPath = `M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z`

// starSVG 构造空心或实心的星标：关注按钮未关注时空心、已关注时实心。
func starSVG(fill string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="` + fill + `" ` +
			`stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="` + starPath + `"/></svg>`))
}

var (
	// 关注按钮的星标：空心 = 未关注，实心 = 已关注
	iconStar       = starSVG("none")
	iconStarFilled = starSVG("currentColor")

	iconSearch = iconSVG(`<circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/>`)
	// MonitorSmartphone：顶栏「数据同步」按钮用的图标
	iconMonitorSmartphone = lucide.MonitorSmartphone()
	iconSync              = fitIcon(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`, 20)
	iconPalette           = iconSVG(
		`<circle cx="13.5" cy="6.5" r="0.5"/><circle cx="17.5" cy="10.5" r="0.5"/>` +
			`<circle cx="8.5" cy="7.5" r="0.5"/><circle cx="6.5" cy="12.5" r="0.5"/>` +
			`<path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"/>`)

	// 配色模式：日 / 夜 / 跟随系统（显示器）。只用一个圆形按钮，
	// 状态由图标表达，点击循环切换。
	iconSun = iconSVG(
		`<circle cx="12" cy="12" r="4"/><path d="M12 2v2"/><path d="M12 20v2"/>` +
			`<path d="m4.93 4.93 1.41 1.41"/><path d="m17.66 17.66 1.41 1.41"/>` +
			`<path d="M2 12h2"/><path d="M20 12h2"/>` +
			`<path d="m6.34 17.66-1.41 1.41"/><path d="m19.07 4.93-1.41 1.41"/>`)
	iconMoon = iconSVG(
		`<path d="M20.985 12.486a9 9 0 1 1-9.473-9.472c.405-.022.617.46.402.803a6 6 0 0 0 8.268 8.268c.344-.215.825-.004.803.401"/>`)
	// 跟随系统：描边圆 + 右半实心，表示随系统日夜切换。
	// 内层 fill 覆盖根上的 fill="none" 在真实渲染里是生效的（用像素亮度比测过），
	// 所以右半直接画成实心路径，不用粗描边去凑。
	iconSystemTheme = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<circle cx="12" cy="12" r="9"/>` +
			`<path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none"/>` +
			`</svg>`))

	// Bookmark：lucide 新版把旧版那四个尖角改成了圆角，几何从
	// m19 21-7-4-7 4V5… 变成 M17 3a2 2 0 0 1 2 2v15…。跟上游走。
	iconBookmark = lucide.Bookmark()
	iconPlus     = lucide.Plus()
	iconMinus    = lucide.Minus()
	// 面板把手的收起 / 展开键是 14px / 2.8 描边，比常规 2px 粗一档。
	// 面板展开时用 ChevronRight（指向面板里），收起时用 ChevronLeft。
	iconChevronRightBold = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="m9 18 6-6-6-6"/></svg>`))
	iconChevronLeftBold = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="m15 18-6-6 6-6"/></svg>`))
	iconArrowDown = lucide.ArrowDown()
	// MonitorOff：未开播空态的那个圆底图标
	iconMonitorOff = lucide.MonitorOff()
	iconChevron    = lucide.ChevronDown()
	iconCheck      = lucide.Check()
	iconUser       = lucide.User()
	iconLogOut     = lucide.LogOut()
	// iconX 是 2px 描边的关闭叉号（输入框的清除键、订阅 chip 的删除键都用它）
	iconX = lucide.X()
	// 数据同步：导出 / 导入按钮，以及导入结果的成功提示
	iconUpload   = lucide.Upload()
	iconDownload = lucide.Download()
	iconOkCircle = iconSVG(`<circle cx="12" cy="12" r="10"/><path d="m8.5 12 2.5 2.5 4.5-5"/>`)
	// Activity：诊断面板的页签与「性能剖析」按钮。
	// 新版把旧版那条 M22 12h-4l-3 9L9 3l-3 9H2 的尖角折线改成了圆角折线。
	iconActivity = lucide.Activity()
	// Copy / FolderOpen：诊断产物落盘后的「复制路径」「打开所在文件夹」
	iconCopy       = lucide.Copy()
	iconFolderOpen = lucide.FolderOpen()
	// Stop：剖析进行中的按钮，按下去叫停
	iconStop = fitFilledIcon(`<rect width="11" height="11" x="6.5" y="6.5" rx="2"/>`, 14)
	// AlertCircle：toast 的 info 色调
	iconAlert = iconSVG(`<circle cx="12" cy="12" r="10"/><path d="M12 7v6"/><path d="M12 16.5v.5"/>`)

	// 播放页 chrome
	// 这两条是 lucide v1.72 的路径：play 是带圆角的填充三角形（不是 polygon），
	// pause 的两根竖条是 5 宽、18 高、rx 1，比早先版本的 4×16 更粗。
	iconPlay  = fitFilledIcon(`<path d="M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z"/>`, 19)
	iconPause = fitFilledIcon(`<rect x="14" y="3" width="5" height="18" rx="1"/><rect x="5" y="3" width="5" height="18" rx="1"/>`, 19)
	// Maximize2：全屏 / 退出全屏
	iconFullscreen = fitIcon(`<path d="M15 3h6v6"/><path d="m21 3-7 7"/><path d="m3 21 7-7"/><path d="M9 21H3v-6"/>`, 20)
	// Maximize：窗口全屏（铺满应用窗口，不动系统窗口）
	iconWinFull = fitIcon(`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/>`, 20)
	// Minimize：窗口全屏的退出态。和 Maximize 是同一对四角图标（外扩 / 收拢）；
	// 之前这里借用了 Minimize2，跟系统全屏的退出态撞成同一个形状。
	iconWinShrink = fitIcon(`<path d="M8 3v3a2 2 0 0 1-2 2H3"/><path d="M21 8h-3a2 2 0 0 1-2-2V3"/><path d="M3 16h3a2 2 0 0 1 2 2v3"/><path d="M16 21v-3a2 2 0 0 1 2-2h3"/>`, 20)
	// Minimize2：系统全屏的退出态，和 Maximize2 是同一对斜箭头
	iconMinimize2 = fitIcon(`<path d="m14 10 7-7"/><path d="M20 10h-6V4"/><path d="m3 21 7-7"/><path d="M4 14h6v6"/>`, 20)
	// PictureInPicture2：小窗播放
	iconPip = fitIcon(`<path d="M21 9V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v10c0 1.1.9 2 2 2h4"/><rect width="10" height="7" x="12" y="13" rx="2"/>`, 18)
	// 弹幕控件：电视机身去掉天线，屏幕里换成三条左对齐、逐条变短的横线，
	// 右下角压一枚徽标（勾 / 禁止符 / 六边形 / 漏斗）。
	//
	// 徽标完全落在机身墨迹范围内：机身 18×18（描边 2，墨迹 20×20），徽标圆心
	// 在 (17.2,17.2)、外径 9.6，正好收在 22 那道边里。之前徽标吊在机身外面，
	// 合成墨迹比同排图标高 1-2px，上下边缘对不齐——一排看过去就是「大小不一」。
	//
	// 机身以 12 居中、墨迹 20×20，和 lucide 那几个（同样是 20×20）上下边对齐。
	// 缺口用 mask 挖成圆的，圆心压在机身右下角上。
	// 归一化用的是 is 那一套：mask 放进缩放组里，缺口跟着机身一起缩，
	// 徽标（在 24 网格上画、不缩放）仍然压在洞口上。
	iconDanmuTV = fitIcon(
		`<mask id="danmu-tv-cut"><rect width="24" height="24" fill="#fff"/>`+
			`<circle cx="17.2" cy="17.2" r="5" fill="#000"/></mask>`+
			`<g mask="url(#danmu-tv-cut)"><rect x="3" y="3" width="18" height="18" rx="3"/>`+
			`<path d="M5.4 8h6.4"/><path d="M5.4 11h4.8"/><path d="M5.4 14h3.2"/></g>`, 20)
	// 开启：勾。颜色由 danmuTVIcon 单独给主题色，不跟机身走。
	iconDanmuBadgeCheck = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2.6" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="m13.8 16.8 2.5 2.5 4.1-4.9"/></svg>`))
	// 关闭：禁止符。圆环和另外三枚徽标同一个外径。
	iconDanmuBadgeOff = iconSVG(
		`<circle cx="17.2" cy="17.2" r="3.6"/>` +
			`<path d="M14.7 19.7 19.7 14.7"/>`)
	// 设置：六边形描边 + 中心实心点（照例图，不是齿轮）。
	iconDanmuBadgeSettings = iconSVG(
		`<path d="M21.1 17.2 19.15 20.6 15.25 20.6 13.3 17.2 15.25 13.8 19.15 13.8 Z"/>` +
			`<circle cx="17.2" cy="17.2" r="1.6" fill="currentColor" stroke="none"/>`)
	// 屏蔽：实心漏斗。徽标只有 9px，描边漏斗的三条斜线在这个尺寸下会糊，
	// 所以整块填满，只留一个短柄。
	iconDanmuBadgeFilter = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<path fill="currentColor" stroke="none" d="M12.5 12.7 H21.9 L18.6 17.2 V21.5 H16.4 V17.2 Z"/>` +
			`</svg>`))
	iconPlayerBack = iconSVG(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	iconQRCode     = iconSVG(`<rect width="5" height="5" x="3" y="3" rx="1"/><rect width="5" height="5" x="16" y="3" rx="1"/><rect width="5" height="5" x="3" y="16" rx="1"/><path d="M21 16h-3a2 2 0 0 0-2 2v3"/><path d="M21 21v.01"/><path d="M12 7v3a2 2 0 0 1-2 2H7"/><path d="M3 12h.01"/><path d="M12 3h.01"/><path d="M12 16v.01"/><path d="M16 12h1"/><path d="M21 12v.01"/><path d="M12 21v-1"/>`)
	// VolumeX / Volume1 / Volume2：v1.72 的喇叭是带圆角的一体路径，
	// 早先版本那种「梯形 + 短线」在 19px 下转角是硬的。
	iconVolumeMute = fitIcon(`<path d="M11 4.702a.7.7 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.7.7 0 0 0 11 19.298z"/><path d="m16.5 14.5 5-5"/><path d="m16.5 9.5 5 5"/>`, 18)
	iconVolumeLow  = fitIcon(`<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><path d="M16 9a5 5 0 0 1 0 6"/>`, 18)
	iconVolumeHigh = fitIcon(`<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><path d="M16 9a5 5 0 0 1 0 6"/><path d="M19.364 18.364a9 9 0 0 0 0-12.728"/>`, 18)

	iconMinimize = iconSVGThin(`<path d="M6 12h12"/>`)
	iconMaximize = iconSVGThin(`<rect x="4.5" y="4.5" width="15" height="15" rx="2.5"/>`)
	iconClose    = iconSVGThin(`<path d="M6 6l12 12M18 6L6 18"/>`)
)
