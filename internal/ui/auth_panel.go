package ui

import (
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/media"
	"alive/internal/theme"
)

const (
	// authPanelW 按二维码定：168 的码 + 左右各 14 的 padding 再多一点余量。
	// 之前写 240，比内容宽出一截，二维码摆进去右边空一大块。
	authPanelW = 212
	// authQRSize 是二维码白底的边长。图本身留出内边距，扫码识别需要
	// 四周有一圈静区，贴边画会扫不出来。
	authQRSize = 168
	// authAvatarW 是顶栏头像的解码宽度：常态外框 38 减 12，窄屏 34 减 12。
	// 两级都落在同一个解码档（targetPixels 把小于 64px 的抬到 64），
	// 所以预取一次就够两种宽度用。
	authAvatarW float32 = 26
)

// authFor 返回某个平台的登录入口（扫码），没有则 nil。
//
// 与 currentAuth 的区别只在于平台由调用方给：切平台时要预取的是
// 「所有平台」的头像，而不只是当前这个。
//
// 抽成变量是为了能在测试里验登录界面：真实实现会去问平台、发网络请求，
// 而「点登录之后屏幕上出现什么」是纯界面的事，不该依赖网络。测试注入一个
// 假的 Authenticator，界面那条路一行都不用改。
var authFor = func(platform string) live.Authenticator {
	auth, _ := live.AuthFor(live.Platform(platform))
	return auth
}

// syncAuthAvatars 把各平台已登录账号的头像提前备进图片缓存。
//
// 顶栏头像只画当前平台那一个，切平台就换人。换过去的那一帧，新平台的
// 头像通常还没进 L1，只能先画灰色人形占位，等下载和解码回来再跳成真
// 头像——这就是「切平台闪一下」里剩下的那部分（封面和主播头像都已预取）。
// 一共四个平台、各一张，全备好，切谁都是瞬时的。
//
// 每帧调用只是查表：只有头像地址变了（刚登录、换号）才真的发请求。
func (a *App) syncAuthAvatars() {
	if a.Win == nil {
		return // 视图测试不发真实请求
	}
	for _, id := range a.PlatformOrder {
		auth := authFor(id)
		if auth == nil {
			continue
		}
		url := auth.Auth().Avatar
		if url == "" || a.authAvatarPrefetched[id] == url {
			continue
		}
		a.authAvatarPrefetched[id] = url
		media.Default.PrefetchAvatars(authAvatarW, url)
	}
}

// closeAuthPanel 收起账号入口。
//
// 入口从顶栏消失的页面（「全部关注」「订阅」）也要调一次——只把按钮不画，
// 状态还留在 AuthOpen，等回到有账号入口的页面时浮层会自己弹回来。
func (a *App) closeAuthPanel() {
	a.AuthOpen = false
}

// authButton 是顶栏右上角的登录入口。
//
// 账号是**按平台**的：当前 tab 是 B 站就显示 B 站的号，切到斗鱼换成斗鱼的。
// 播放页跟着正在看的主播走（viewPlatform），不是进房之前停留的那个 tab。
// 没有登录能力的平台（抖音）整块不出现，而不是摆一个点了没用的图标。
func (a *App) authButton(c *ui.Context) {
	auth := a.currentAuth()
	if auth == nil {
		return
	}
	st := auth.Auth()
	key := string(st.Platform)

	size := chromeH
	dia := size - 12

	btn := ui.Box(c).Size(size, size).Radius(theme.RadiusFull).
		Center().Cursor(ui.CursorPointer)
	if st.LoggedIn {
		btn.Label("账号")
	} else {
		btn.Label("登录")
	}

	hovered := btn.Hovered()
	switch {
	case a.AuthOpen:
		btn.Background(theme.FillSecondary)
	case hovered:
		btn.Background(theme.FillTertiary)
	}

	if btn.Clicked() {
		a.AuthOpen = !a.AuthOpen
		// 第一次打开就顺手要一张码：进去还要再点一下才出码太啰嗦。
		if a.AuthOpen && !st.LoggedIn && len(st.QR) == 0 && st.Err == "" {
			auth.BeginLogin()
		}
	}
	btn.Children(func() {
		if st.LoggedIn && st.Avatar != "" {
			if bmp := media.Default.Avatar(st.Avatar, dia); bmp != nil {
				ui.Image(c, bmp).Size(dia, dia).Radius(dia / 2).Fit(ui.Cover)
				return
			}
		}
		// 未登录，或者头像还在路上：画占位人像。
		ui.Box(c).Size(dia, dia).Radius(dia / 2).
			Background(theme.FillTertiary).Center().Children(func() {
			col := theme.TextSecondary
			if a.AuthOpen || hovered {
				col = theme.TextPrimary
			}
			ui.Icon(c, iconUser).FontSize(dia * 0.6).TextColor(col)
		})
	})

	ui.PopoverBase(c, btn, &a.AuthOpen, func(panel ui.Element) {
		// 用 MinWidth 而不是 Width：内容（二维码 / 长一点的错误文案）
		// 比这个宽时要能自己撑开，写死宽度会把二维码右侧裁掉。
		panel.MinWidth(authPanelW).Padding(14).Radius(theme.RadiusXL).
			Background(theme.BgElevated).Border(1, theme.Border).
			Label("登录浮层").
			// --shadow-lg: 0 38px 72px -30px rgba(0,0,0,.85)
			Shadow(0, 38, 72, -30, theme.ShadowModal).Shadow(0, 6, 16, 0, theme.ShadowContact).
			Margin(10, 0, 0, 0).
			AttachTo(btn, ui.AnchorBottomRight, ui.AnchorTopRight)
		if st.LoggedIn {
			a.authAccountPanel(c, auth, st)
			return
		}
		a.authScanPanel(c, auth, st, key)
	})
}

// authScanPanel 是未登录时的扫码面板。
func (a *App) authScanPanel(c *ui.Context, auth live.Authenticator, st live.AuthState, key string) {

	// 二维码是 PNG，解一次缓存起来；每帧重解码是白烧 CPU。
	// 解码要在主线程做（会建纹理），所以放在这里而不是后台。
	if st.QRSeq != a.authQRSeq[key] {
		a.authQRSeq[key] = st.QRSeq
		a.authQR[key] = nil
		if len(st.QR) > 0 {
			if bmp, err := ui.DecodeBitmap(st.QR); err == nil {
				a.authQR[key] = bmp
			}
		}
	}
	qr := a.authQR[key]

	// 列要显式居中：默认是左对齐，二维码 168 摆进 212 宽的面板里会全挤到
	// 左边，右边空一大块，标题和文案也跟着贴左。
	ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "登录 "+platformLabel(key)).FontSize(theme.TextSM).FontWeight(600).
			TextColor(theme.TextPrimary).TextAlign(ui.Center).MaxLines(1)

		ui.Box(c).Size(authQRSize, authQRSize).Radius(theme.RadiusMD).
			Background(ui.RGB(255, 255, 255)).Center().Children(func() {
			if qr != nil {
				ui.Image(c, qr).Size(authQRSize-20, authQRSize-20).Fit(ui.Contain)
				return
			}
			ui.Icon(c, iconQRCode).FontSize(30).TextColor(ui.RGB(122, 126, 136))
		})

		// 状态文案最多两行：错误信息带接口原文，一行放不下，
		// 但也不能任它把面板撑得老高。
		switch {
		case st.Err != "":
			ui.Text(c, st.Err).FontSize(theme.TextXS).TextColor(theme.DangerInk).
				TextAlign(ui.Center).MaxLines(2)
			a.authRetry(c, auth)
		case st.Status != "":
			ui.Text(c, st.Status).FontSize(theme.TextXS).
				TextColor(theme.TextTertiary).TextAlign(ui.Center).MaxLines(2)
		default:
			ui.Text(c, "正在获取二维码…").FontSize(theme.TextXS).
				TextColor(theme.TextTertiary).TextAlign(ui.Center)
		}
	})
}

// authAccountPanel 是已登录时的账号面板。
func (a *App) authAccountPanel(c *ui.Context, auth live.Authenticator, st live.AuthState) {
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			if bmp := media.Default.Avatar(st.Avatar, 36); bmp != nil {
				ui.Image(c, bmp).Size(36, 36).Radius(18).Fit(ui.Cover)
			} else {
				ui.Box(c).Size(36, 36).Radius(18).Background(theme.FillTertiary).
					Center().Children(func() {
					ui.Icon(c, iconUser).FontSize(20).TextColor(theme.TextSecondary)
				})
			}
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, st.Name).FontSize(theme.TextSM).FontWeight(600).
					TextColor(theme.TextPrimary).SingleLine()
				ui.Text(c, platformLabel(string(st.Platform))+" 账号").
					FontSize(theme.Text2XS).TextColor(theme.TextTertiary).NoWrap()
			})
		})

		logout := ui.Row(c).Height(32).FillWidth().Padding(0, 10, 0, 10).Gap(6).
			Radius(theme.RadiusMD).AlignItems(ui.Center).
			Cursor(ui.CursorPointer).Label("退出登录")
		logout.Background(theme.FillTertiary)
		logout.Background(hoverSurface(logout, "logout-bg", logout.Hovered(), theme.FillSecondary))
		if logout.Clicked() {
			auth.Logout()
			a.AuthOpen = false
			a.markDirty()
		}
		logout.Children(func() {
			ui.Icon(c, iconLogOut).FontSize(14).TextColor(theme.TextSecondary)
			ui.Text(c, "退出登录").FontSize(theme.TextXS).
				TextColor(theme.TextSecondary)
		})
	})
}

// authRetry 是二维码拿不到时的重试按钮。
func (a *App) authRetry(c *ui.Context, auth live.Authenticator) {
	btn := ui.Row(c).Height(30).FillWidth().Radius(theme.RadiusMD).
		Center().Cursor(ui.CursorPointer).Label("重新获取")
	btn.Background(hoverSurface(btn, "retry-bg", btn.Hovered(), theme.FillSecondary))
	if btn.Clicked() {
		auth.RefreshQRCode()
	}
	btn.Children(func() {
		ui.Text(c, "重新获取").FontSize(theme.TextXS).TextColor(theme.TextPrimary)
	})
}
