package ui

import (
	"log/slog"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/theme"
)

// 浏览器登录：虎牙和抖音在系统 webview 里登录。
//
// 主区域整个换成登录页，页面原样铺开：注入的脚本只负责把平台自己的
// 登录入口点开，登录框由平台弹出。扫码、点「获取验证码」、填码、点确定
// 全是用户在真页面上做——程序不判断走到哪一步、不代点按钮。
//
// 为什么要这样：抖音的验证流程分支多（要不要点获取验证码、点完是否
// 自动发送、要不要再点一次），代点一改版就整条失效，而且失败了程序
// 察觉不到。交给真页面，只有平台自己改版才需要动，动的也只是「点哪个
// 入口」这一处。
//
// 程序只做一件事：cookie 里出现会话字段就收工。
// 会话用固定名字的持久会话：HTTP / JS 缓存留着，第二次打开一两秒就出来；
// 会话 cookie 也留着，平台自己会续期。登录成功后读走 cookie 就销毁窗口，
// 但 profile 不动。

// loginSessionName 是登录 webview 用的会话名。
//
// 用持久会话而不是临时会话，是为了留下 HTTP / JS 缓存：登录页第一次要
// 十几秒，之后走缓存一两秒就出来。同一个 profile 两个平台共用——同一
// 时间只有一个在登录，缓存也能复用。会话 cookie 一起留着，平台的页面
// 自己会把它续上。
const loginSessionName = "alive-login"

// ensureLoginWebView 保证当前平台有一个登录 webview。
//
// 主区域每帧重建，靠 loginKey 判断「已经建过」。
func (a *App) ensureLoginWebView(wl *live.WebLogin) *mygo.WebView {
	key := string(wl.Platform())
	if a.loginPage != nil && a.loginKey == key {
		return a.loginPage
	}
	a.destroyLoginWebView()
	if a.Win == nil {
		return nil
	}

	// 持久会话：抖音那套 React 应用每次冷启动要十几秒，有 HTTP 和 JS
	// 缓存之后第二次就快得多。登录成功会把 cookie 清掉，缓存留着。
	sess := mygo.NewSession(loginSessionName)
	page, err := a.Win.NewWebView(mygo.WebViewOptions{
		URL: wl.LoginURL(),
		Page: mygo.PageOptions{
			// 第三方页面：不能调 Go 的方法，也不需要。
			Untrusted: true,
			Session:   sess,
			// 隔离脚本只进主框架：抖音的登录框在页面里，虎牙的在 iframe 里，
			// 由脚本自己分派。
			PreloadScript: wl.IsolateScript(),
		},
	})
	if err != nil {
		slog.Warn("创建登录窗口失败", "platform", key, "error", err)
		return nil
	}
	// 登录页上可能有直播列表、预览视频，静音兜底：登录过程不该出声。
	page.Page().SetAudioMuted(true)
	a.loginPage, a.loginSess, a.loginKey = page, sess, key
	a.loginReady = false
	a.startLoginPoll(wl, sess, page)
	return page
}

// startLoginPoll 只等一件事：cookie 里出现会话字段。
//
// cookie store 可以在主线程外读；改视图状态要切回主线程。
// stop 在销毁窗口时关闭，避免面板关掉后还在跑。
func (a *App) startLoginPoll(wl *live.WebLogin, sess *mygo.Session, page *mygo.WebView) {
	stop := make(chan struct{})
	a.loginStop = stop

	domain := wl.CookieDomain()
	target := a.Win

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}

			// 页面就绪了才让界面显示它。
			if !a.loginReady && wl.ReadyScript() != "" {
				v, err := page.Page().Eval(wl.ReadyScript())
				if err == nil {
					if s, ok := v.(string); ok && s == "yes" {
						a.loginReady = true
						if target != nil {
							target.Update(func() { a.markDirty() })
						}
					}
				}
			}

			raw, err := sess.Cookies.List(mygo.CookieFilter{Domain: domain})
			if err != nil || len(raw) == 0 {
				continue
			}
			list := make([]live.BrowserCookie, 0, len(raw))
			for _, c := range raw {
				list = append(list, live.BrowserCookie{Name: c.Name, Value: c.Value, Domain: c.Domain})
			}
			// Finish 在没登录时返回错误，继续等下一轮。
			if err := wl.Finish(list); err != nil {
				continue
			}
			if target != nil {
				target.Update(func() {
					a.destroyLoginWebView()
					a.markDirty()
				})
			}
			return
		}
	}()
}

// destroyLoginWebView 关掉登录窗口。主线程调用。
func (a *App) destroyLoginWebView() {
	if a.loginStop != nil {
		close(a.loginStop)
		a.loginStop = nil
	}
	if a.loginPage != nil {
		a.loginPage.Destroy()
		a.loginPage = nil
	}
	a.loginSess = nil
	a.loginKey = ""
	a.loginReady = false
}

// renewLoginSession 用同一个 profile 后台开一次页面，让平台自己续期。
//
// 会话失效不一定是真的过期：抖音和虎牙的页面加载时会带着现有 cookie 去
// 换新的，很多时候刷一下就回来了。webview 不放进视图，用户看不到。
// 续期成功就把新会话写回去；失败由调用方决定要不要清会话。
func (a *App) renewLoginSession(wl *live.WebLogin) {
	win := a.Win
	if win == nil {
		return
	}
	sess := mygo.NewSession(loginSessionName)
	page, err := win.NewWebView(mygo.WebViewOptions{
		URL: wl.LoginURL(),
		Page: mygo.PageOptions{
			Untrusted: true,
			Session:   sess,
		},
	})
	if err != nil {
		slog.Warn("自动续期窗口创建失败", "platform", wl.Platform(), "error", err)
		return
	}
	page.Page().SetAudioMuted(true)

	domain := wl.CookieDomain()
	go func() {
		// 给页面足够时间跑完它自己的续期请求（登录态刷新通常在页面加载
		// 后几秒内完成）。这段时间窗口是隐藏的，用户无感。
		time.Sleep(20 * time.Second)
		raw, err := sess.Cookies.List(mygo.CookieFilter{Domain: domain})
		var list []live.BrowserCookie
		if err == nil {
			list = make([]live.BrowserCookie, 0, len(raw))
			for _, c := range raw {
				list = append(list, live.BrowserCookie{Name: c.Name, Value: c.Value, Domain: c.Domain})
			}
		}
		ok := wl.Finish(list) == nil
		if !ok {
			slog.Info("自动续期没成功", "platform", wl.Platform())
		}
		win.Update(func() {
			page.Destroy()
			if !ok {
				// 续不上就按失效处理，让用户重新登录。
				wl.Logout()
			}
			a.markDirty()
		})
	}()
}

// loginLoading 是登录页加载中的占位。
//
// 一圈主题色的弧在转，配一行字。用 Loop 做循环：它自己会持续请求帧，
// 不用手动 tick。
func (a *App) loginLoading(c *ui.Context) {
	ui.Column(c).Gap(14).AlignItems(ui.Center).Children(func() {
		box := ui.Box(c).Size(26, 26)
		// 0.9 秒一圈，和关注页的刷新环同一个节奏。
		turn := box.Loop("login-spin", 900*time.Millisecond, ui.Linear)
		box.Draw(func(p *ui.Painter, r ui.Rect) {
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			rad := r.W/2 - 1.5
			// 26% 的弧段：比整圈好看，也能看出在转。
			arc := arcPath(cx, cy, rad, turn, turn+0.26)
			p.StrokePath(&arc, 2.5, theme.AccentInk)
		})
		ui.Text(c, "正在加载登录页…").FontSize(theme.TextSM).
			TextColor(theme.TextTertiary)
	})
}
