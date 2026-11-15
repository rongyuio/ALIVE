package ui

import "github.com/egoist/mygo/ui"

// lucide 的线性图标（24×24 网格、2px 描边、圆头圆角），
// 与 baseline 用的 lucide-react 是同一套路径，所以粗细和比例能直接对上。
//
// MyGo 的 SVG 不支持文字、内嵌图片、pattern、marker、filter，
// 但这条线性图标只有 path / circle / rect，全部在支持范围内。
func iconSVG(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

// iconFilled 是实心图标：外层就是 fill="currentColor"，没有描边。
//
// 播放/暂停在 baseline 里是 `<Pause fill="currentColor" />`——矩形被填满；
// 只写描边会细成两根线。MyGo 的 SVG 解析不认内层元素上的 fill 覆盖外层，
// 所以只能整张图改成实心。
func iconFilled(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="currentColor" ` +
			`stroke="currentColor" stroke-width="1" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

// iconSVGThin 和 iconSVG 一样，只是描边 1.8 —— 窗口按钮用的是这一档，
// baseline 里那三个图标是手写的 1.8 描边 SVG，不是 lucide 的 2。
func iconSVGThin(inner string) *ui.SVG {
	return ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` +
			inner + `</svg>`))
}

// starPath 是 lucide Star 的原始路径（node_modules/lucide-react 同名文件）。
const starPath = `M11.525 2.295a.53.53 0 0 1 .95 0l2.31 4.679a2.123 2.123 0 0 0 1.595 1.16l5.166.756a.53.53 0 0 1 .294.904l-3.736 3.638a2.123 2.123 0 0 0-.611 1.878l.882 5.14a.53.53 0 0 1-.771.56l-4.618-2.428a2.122 2.122 0 0 0-1.973 0L6.396 21.01a.53.53 0 0 1-.77-.56l.881-5.139a2.122 2.122 0 0 0-.611-1.879L2.16 9.795a.53.53 0 0 1 .294-.906l5.165-.755a2.122 2.122 0 0 0 1.597-1.16z`

// starSVG 构造空心或实心的星标：baseline 的关注按钮未关注时空心、已关注时实心。
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
	// MonitorSmartphone：baseline 顶栏「数据同步」按钮用的图标
	iconMonitorSmartphone = iconSVG(
		`<path d="M18 8V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v7a2 2 0 0 0 2 2h8"/>` +
			`<path d="M10 19v-3.96 3.15"/><path d="M7 19h5"/>` +
			`<rect width="6" height="10" x="16" y="12" rx="2"/>`)
	iconSync    = iconSVG(`<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>`)
	iconPalette = iconSVG(
		`<circle cx="13.5" cy="6.5" r="0.5"/><circle cx="17.5" cy="10.5" r="0.5"/>` +
			`<circle cx="8.5" cy="7.5" r="0.5"/><circle cx="6.5" cy="12.5" r="0.5"/>` +
			`<path d="M12 2C6.5 2 2 6.5 2 12s4.5 10 10 10c.926 0 1.648-.746 1.648-1.688 0-.437-.18-.835-.437-1.125-.29-.289-.438-.652-.438-1.125a1.64 1.64 0 0 1 1.668-1.668h1.996c3.051 0 5.555-2.503 5.555-5.554C21.965 6.012 17.461 2 12 2z"/>`)

	iconBookmark     = iconSVG(`<path d="m19 21-7-4-7 4V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v16z"/>`)
	iconPlus         = iconSVG(`<path d="M5 12h14"/><path d="M12 5v14"/>`)
	iconChevronRight = iconSVG(`<path d="m9 18 6-6-6-6"/>`)
	// 面板把手的收起 / 展开键是 14px / 2.8 描边，比常规 2px 粗一档。
	// baseline 展开时用 ChevronRight（指向面板里），收起时用 ChevronLeft。
	iconChevronRightBold = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="m9 18 6-6-6-6"/></svg>`))
	iconChevronLeftBold = ui.MustParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="2.8" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="m15 18-6-6 6-6"/></svg>`))
	iconArrowDown = iconSVG(`<path d="M12 5v14"/><path d="m19 12-7 7-7-7"/>`)
	// MonitorOff：未开播空态的那个圆底图标
	iconMonitorOff = iconSVG(
		`<path d="M12 17v4"/><path d="M17 17H4a2 2 0 0 1-2-2V5a2 2 0 0 1 1.184-1.826"/>` +
			`<path d="m2 2 20 20"/><path d="M8 21h8"/>` +
			`<path d="M8.656 3H20a2 2 0 0 1 2 2v10a2 2 0 0 1-.293 1.042"/>`)
	iconChevron = iconSVG(`<path d="m6 9 6 6 6-6"/>`)
	iconCheck   = iconSVG(`<path d="M20 6 9 17l-5-5"/>`)
	iconUser    = iconSVG(`<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`)
	iconLogOut  = iconSVG(`<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5"/><path d="M21 12H9"/>`)
	// iconX 是 2px 描边的关闭叉号（baseline 输入框的清除键、订阅 chip 的删除键都用它）
	iconX = iconSVG(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	// 数据同步：导出 / 导入按钮，以及导入结果的成功提示
	iconUpload   = iconSVG(`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M17 8l-5-5-5 5"/><path d="M12 3v12"/>`)
	iconDownload = iconSVG(`<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="M7 10l5 5 5-5"/><path d="M12 15V3"/>`)
	iconOkCircle = iconSVG(`<circle cx="12" cy="12" r="10"/><path d="m8.5 12 2.5 2.5 4.5-5"/>`)
	// AlertCircle：toast 的 info 色调（baseline ToastProvider 的 TONE_ICON.info）
	iconAlert = iconSVG(`<circle cx="12" cy="12" r="10"/><path d="M12 7v6"/><path d="M12 16.5v.5"/>`)
	// TriangleAlert：toast 的 error 色调（baseline ToastProvider 的 TONE_ICON.error）
	iconTriangleAlert = iconSVG(`<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3"/><path d="M12 9v4"/><path d="M12 17h.01"/>`)

	// 播放页 chrome
	iconBack = iconSVG(`<path d="m12 19-7-7 7-7"/><path d="M19 12H5"/>`)
	// 这两条是 lucide v1.72 的路径：play 是带圆角的填充三角形（不是 polygon），
	// pause 的两根竖条是 5 宽、18 高、rx 1，比早先版本的 4×16 更粗。
	iconPlay   = iconFilled(`<path d="M5 5a2 2 0 0 1 3.008-1.728l11.997 6.998a2 2 0 0 1 .003 3.458l-12 7A2 2 0 0 1 5 19z"/>`)
	iconPause  = iconFilled(`<rect x="14" y="3" width="5" height="18" rx="1"/><rect x="5" y="3" width="5" height="18" rx="1"/>`)
	iconVolume = iconSVG(`<path d="M11 5 6 9H2v6h4l5 4V5z"/><path d="M15.5 8.5a5 5 0 0 1 0 7"/>`)
	// Maximize2：全屏 / 退出全屏
	iconFullscreen = iconSVG(`<path d="M15 3h6v6"/><path d="m21 3-7 7"/><path d="m3 21 7-7"/><path d="M9 21H3v-6"/>`)
	// Maximize：窗口全屏（铺满应用窗口，不动系统窗口）
	iconWinFull = iconSVG(`<path d="M8 3H5a2 2 0 0 0-2 2v3"/><path d="M21 8V5a2 2 0 0 0-2-2h-3"/><path d="M3 16v3a2 2 0 0 0 2 2h3"/><path d="M16 21h3a2 2 0 0 0 2-2v-3"/>`)
	// Minimize：窗口全屏的退出态。和 Maximize 是同一对四角图标（外扩 / 收拢）；
	// 之前这里借用了 Minimize2，跟系统全屏的退出态撞成同一个形状。
	iconWinShrink = iconSVG(`<path d="M8 3v3a2 2 0 0 1-2 2H3"/><path d="M21 8h-3a2 2 0 0 1-2-2V3"/><path d="M3 16h3a2 2 0 0 1 2 2v3"/><path d="M16 21v-3a2 2 0 0 1 2-2h3"/>`)
	// Minimize2：系统全屏的退出态，和 Maximize2 是同一对斜箭头
	iconMinimize2 = iconSVG(`<path d="m14 10 7-7"/><path d="M20 10h-6V4"/><path d="m3 21 7-7"/><path d="M4 14h6v6"/>`)
	// PictureInPicture2：小窗播放
	iconPip      = iconSVG(`<path d="M21 9V6a2 2 0 0 0-2-2H4a2 2 0 0 0-2 2v10c0 1.1.9 2 2 2h4"/><rect width="10" height="7" x="12" y="13" rx="2"/>`)
	iconDanmuOff = iconSVG(`<path d="M19 19H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.7.7 0 0 1 2 21.286V5a2 2 0 0 1 1.184-1.826"/><path d="m2 2 20 20"/><path d="M8.656 3H20a2 2 0 0 1 2 2v11.344"/>`)
	// baseline 的开启态是 <MessageSquare fill="currentColor" fillOpacity={0.18}>；
	// MyGo 的 SVG 解析不认 fill-opacity，所以这里只有描边。
	iconDanmu      = iconSVG(`<path d="M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z"/>`)
	iconSettings2  = iconSVG(`<path d="M20 7h-9"/><path d="M14 17H5"/><circle cx="17" cy="17" r="3"/><circle cx="7" cy="7" r="3"/>`)
	iconPlayerBack = iconSVG(`<path d="M18 6 6 18"/><path d="m6 6 12 12"/>`)
	iconQRCode     = iconSVG(`<rect width="5" height="5" x="3" y="3" rx="1"/><rect width="5" height="5" x="16" y="3" rx="1"/><rect width="5" height="5" x="3" y="16" rx="1"/><path d="M21 16h-3a2 2 0 0 0-2 2v3"/><path d="M21 21v.01"/><path d="M12 7v3a2 2 0 0 1-2 2H7"/><path d="M3 12h.01"/><path d="M12 3h.01"/><path d="M12 16v.01"/><path d="M16 12h1"/><path d="M21 12v.01"/><path d="M12 21v-1"/>`)
	iconFilter     = iconSVG(`<path d="M10 20a1 1 0 0 0 .553.895l2 1A1 1 0 0 0 14 21v-7a2 2 0 0 1 .517-1.341L21.74 4.67A1 1 0 0 0 21 3H3a1 1 0 0 0-.742 1.67l7.225 7.989A2 2 0 0 1 10 14z"/>`)
	// VolumeX / Volume1 / Volume2：v1.72 的喇叭是带圆角的一体路径，
	// 早先版本那种「梯形 + 短线」在 19px 下转角是硬的。
	iconVolumeMute = iconSVG(`<path d="M11 4.702a.7.7 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.7.7 0 0 0 11 19.298z"/><path d="m16.5 14.5 5-5"/><path d="m16.5 9.5 5 5"/>`)
	iconVolumeLow  = iconSVG(`<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><path d="M16 9a5 5 0 0 1 0 6"/>`)
	iconVolumeHigh = iconSVG(`<path d="M11 4.702a.705.705 0 0 0-1.203-.498L6.413 7.587A1.4 1.4 0 0 1 5.416 8H3a1 1 0 0 0-1 1v6a1 1 0 0 0 1 1h2.416a1.4 1.4 0 0 1 .997.413l3.383 3.384A.705.705 0 0 0 11 19.298z"/><path d="M16 9a5 5 0 0 1 0 6"/><path d="M19.364 18.364a9 9 0 0 0 0-12.728"/>`)
	iconSettings   = iconSVG(`<path d="M4 21v-7"/><path d="M4 10V3"/><path d="M12 21v-9"/><path d="M12 8V3"/><path d="M20 21v-5"/><path d="M20 12V3"/><path d="M1 14h6"/><path d="M9 8h6"/><path d="M17 16h6"/>`)

	iconMinimize = iconSVGThin(`<path d="M6 12h12"/>`)
	iconMaximize = iconSVGThin(`<rect x="4.5" y="4.5" width="15" height="15" rx="2.5"/>`)
	iconClose    = iconSVGThin(`<path d="M6 6l12 12M18 6L6 18"/>`)
)
