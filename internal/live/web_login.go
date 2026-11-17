package live

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// 浏览器登录：平台没有能在纯 HTTP 里复现的扫码协议时，把登录页交给
// 系统 webview（Windows 上是 WebView2），登录成功后从 cookie store 里
// 读回会话。
//
// 为什么必须走浏览器内核：
//   - 虎牙 UDB 的扫码确认要求 sdid 是 hydevice 从 canvas、audio、字体等
//     指纹算出的设备标识（`csid_` + guid 只是它的兜底），拿随机串顶替会
//     在手机端点确认时被拒。
//   - 抖音 passport 的 check_qrconnect 要 secsdk 的签名头和 sign/qs 参数，
//     纯 Go 复刻既做不稳也撑不久。
//
// 数据层只负责三件事：给出登录页地址、从 cookie 里认出登录态、把会话
// 落盘和恢复。webview 的创建与销毁在 internal/ui，数据层不依赖任何
// 界面包。

// BrowserCookie 是 webview cookie store 里的一条 cookie。
//
// Domain 只用于过滤，不参与落盘：会话文件里存的是 `name=value` 串。
type BrowserCookie struct {
	Name   string
	Value  string
	Domain string
}

// BrowserUser 是浏览器登录成功后展示用的账号信息。
type BrowserUser struct {
	UID    string
	Name   string
	Avatar string
}

// WebLoginSpec 是一个平台的浏览器登录描述。
//
// 登录界面只显示「登录框」本身，不是整个网站：页面加载后注入一段 CSS，
// 把除登录框以外的部分全部隐藏，webview 的尺寸就设成登录框的大小。
// 扫码、点「获取验证码」、填码、点确定全由用户在真页面上完成——程序
// 不猜任何一步的状态，只做一件事：cookie 里出现会话字段就收工。
//
// 为什么不做成「程序代点」：抖音的验证流程分支多（要不要点获取验证码、
// 点完是否自动发送、要不要再点一次），页面一改版就整条流程失效，而且
// 失败了程序察觉不到。让用户在真页面上点，只有页面本身变了才需要改，
// 改的也只是选择器。
type WebLoginSpec struct {
	// URL 是登录页。
	URL string
	// Domain 是从 cookie store 里取 cookie 时过滤的域，例如 "huya.com"。
	Domain string
	// Isolate 是注入到主框架的脚本：把平台自己的登录入口点开。
	Isolate string
	// ReadyScript 判断登录框是不是已经渲染好了。返回字符串 "yes" 表示
	// 可以显示页面了。界面在它返回 yes 之前盖一层 loading 遮罩，
	// 免得用户看到白屏或者半渲染的中间态。
	ReadyScript string
	// Detect 从 cookie 里认出登录态；没登录返回 false。
	Detect func(map[string]string) (BrowserUser, bool)
}

// WebLogin 是一个平台的浏览器登录状态机，满足 Authenticator。
//
// 它和 qrAuth 的区别只在于「怎么拿到会话」：qrAuth 自己申请二维码并
// 轮询平台接口，WebLogin 把这件事交给 webview，界面读到 cookie 之后
// 调 Finish。
type WebLogin struct {
	platform Platform
	spec     WebLoginSpec
	// verify 用存下来的 cookie 向平台确认会话还有效，并拿回最新的展示信息。
	// 为 nil 时只做本地判断（cookie 里有登录字段）。
	verify func(ctx context.Context, cookies string) (authUser, error)
	// cookieOrder 是落盘时的字段顺序，只影响可读性。
	cookieOrder []string

	mu     sync.Mutex
	state  AuthState
	on     func()
	cancel context.CancelFunc
	// onInvalid 是「校验判定会话失效」时叫醒界面的钩子：界面可以起一次
	// 后台 webview，让平台自己把会话续上。返回 true 表示交给它处理，
	// 这时不删会话文件。
	onInvalid func() bool
	// onSession 把当前会话交给数据层。
	//
	// provider 的 authCookie 是请求层唯一的登录态来源，所以写入、删除、
	// 启动恢复都要走它——漏一处就是「界面显示已登录、请求仍按游客走」，
	// 抖音和虎牙的 setAuthCookie 一度就是这样成了没人调用的死代码。
	onSession func(cookies string)
}

func newWebLogin(p Platform, spec WebLoginSpec, verify func(context.Context, string) (authUser, error), order []string) *WebLogin {
	w := &WebLogin{platform: p, spec: spec, verify: verify, cookieOrder: order}
	w.state = AuthState{Platform: p}
	return w
}

func (w *WebLogin) Platform() Platform { return w.platform }

// Auth 返回当前登录态快照。
func (w *WebLogin) Auth() AuthState {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// SetInvalidHandler 注册「会话失效」的处理钩子。
//
// 浏览器登录的平台，会话失效不一定真的要重扫：用同一个 profile 打开一次
// 平台页面，它可能自己把 cookie 续上。界面在这里起后台 webview 试一次，
// 返回 true 表示接手处理，数据层就不删会话文件。
func (w *WebLogin) SetInvalidHandler(fn func() bool) {
	w.mu.Lock()
	w.onInvalid = fn
	w.mu.Unlock()
}

// SetAuthListener 注册状态变化回调。
func (w *WebLogin) SetAuthListener(fn func()) {
	w.mu.Lock()
	w.on = fn
	w.mu.Unlock()
}

// SetSessionHandler 挂上会话回填。
//
// 必须在 Restore 之前调用：启动时恢复出来的会话只在 Restore 里交出去一次。
func (w *WebLogin) SetSessionHandler(fn func(string)) {
	w.mu.Lock()
	w.onSession = fn
	w.mu.Unlock()
}

// applySession 把会话交给数据层。空串表示退出登录。
func (w *WebLogin) applySession(cookies string) {
	w.mu.Lock()
	fn := w.onSession
	w.mu.Unlock()
	if fn != nil {
		fn(cookies)
	}
}

// LoginURL 是界面要加载的登录页。
func (w *WebLogin) LoginURL() string { return w.spec.URL }

// IsolateScript 是注入到页面里的取入口脚本。
func (w *WebLogin) IsolateScript() string { return w.spec.Isolate }

// ReadyScript 判断页面是否已经能看。
func (w *WebLogin) ReadyScript() string { return w.spec.ReadyScript }

// CookieDomain 是界面读 cookie 时过滤的域。
func (w *WebLogin) CookieDomain() string { return w.spec.Domain }

// BeginLogin 把状态复位到「等待窗口里完成登录」。
func (w *WebLogin) BeginLogin() {
	w.mu.Lock()
	if w.state.LoggedIn {
		w.mu.Unlock()
		return
	}
	w.state = AuthState{Platform: w.platform, Status: "请在窗口中完成登录"}
	w.mu.Unlock()
	w.notify()
}

// RefreshQRCode 与 BeginLogin 同义：浏览器登录没有单独的换码动作。
func (w *WebLogin) RefreshQRCode() { w.BeginLogin() }

// Logout 清掉本地会话。
func (w *WebLogin) Logout() {
	w.mu.Lock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	w.state = AuthState{Platform: w.platform}
	w.mu.Unlock()
	removeAuthSession(w.platform)
	w.applySession("")
	w.notify()
}

// Restore 在启动时恢复上次的会话。
//
// 和扫码登录一样：先点亮界面，再后台校验。校验失败就退回未登录，
// 免得界面挂着「已登录」而请求全是匿名。
func (w *WebLogin) Restore() {
	sess, ok := loadAuthSession(w.platform)
	if !ok || sess.Cookies == "" {
		return
	}
	w.mu.Lock()
	w.state = AuthState{Platform: w.platform, LoggedIn: true, Name: sess.Name, Avatar: sess.Avatar}
	w.mu.Unlock()
	w.applySession(sess.Cookies)
	go w.verifyLoop(sess)
}

// 会话多久校验一次。
//
// 虎牙没有续期接口（checkLogin 只回登录态，不下发新 cookie，
// udb_biztoken 本身就是长期票据），抖音也不做启动校验，所以这里做的
// 是「定期确认还能用」：失效就退回未登录，而不是让界面挂着假账号。
const webLoginCheckInterval = 24 * time.Hour

// verifyLoop 先校验一次，之后每天一次。
func (w *WebLogin) verifyLoop(sess authSession) {
	if w.verify == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	w.cancel = cancel
	w.mu.Unlock()
	defer cancel()

	for {
		w.verifySession(ctx, sess)
		select {
		case <-ctx.Done():
			return
		case <-time.After(webLoginCheckInterval):
		}
		// 会话可能在这期间被换过（退出再登录），以文件里的为准。
		cur, ok := loadAuthSession(w.platform)
		if !ok || cur.Cookies == "" {
			return
		}
		sess = cur
	}
}

func (w *WebLogin) verifySession(parent context.Context, sess authSession) {
	if w.verify == nil {
		return
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	user, err := w.verify(ctx, sess.Cookies)
	if err != nil {
		// 只有平台明确说「这个会话不认」才删。超时、DNS、风控这些
		// 一次性的失败不能当失效——删掉的文件无法恢复，用户得重新扫码。
		if !errors.Is(err, errSessionInvalid) {
			slog.Info("登录态校验失败，保留会话", "platform", w.platform, "error", err)
			return
		}
		slog.Info("登录态已失效", "platform", w.platform, "error", err)
		// 先让界面用 webview 试一次自动续期；它接不了手才删会话。
		w.mu.Lock()
		hook := w.onInvalid
		w.mu.Unlock()
		if hook != nil && hook() {
			return
		}
		removeAuthSession(w.platform)
		w.applySession("")
		w.mu.Lock()
		w.state = AuthState{Platform: w.platform}
		w.mu.Unlock()
		w.notify()
		return
	}
	sess.Name, sess.Avatar, sess.UID = user.Name, user.Avatar, user.UID
	w.writeSession(sess)
	w.mu.Lock()
	w.state.LoggedIn = true
	w.state.Name = user.Name
	w.state.Avatar = user.Avatar
	w.mu.Unlock()
	w.notify()
}

// Finish 接收界面从 cookie store 里读到的裸 cookie 列表。
//
// cookies 是全部 cookie（界面按 CookieDomain 过滤），这里再按平台规则
// 挑出登录字段、拼成会话串并落盘。返回错误时说明还没登录成功。
func (w *WebLogin) Finish(cookies []BrowserCookie) error {
	fields := make(map[string]string, len(cookies))
	for _, c := range cookies {
		if c.Name == "" || c.Value == "" {
			continue
		}
		fields[c.Name] = c.Value
	}
	user, ok := w.spec.Detect(fields)
	if !ok {
		return errSessionInvalid
	}
	raw := buildCookieString(fields, w.cookieOrder)
	sess := authSession{Cookies: raw, UID: user.UID, Name: user.Name, Avatar: user.Avatar}
	w.writeSession(sess)
	w.applySession(sess.Cookies)

	w.mu.Lock()
	w.state = AuthState{
		Platform: w.platform,
		LoggedIn: true,
		Name:     user.Name,
		Avatar:   user.Avatar,
	}
	w.mu.Unlock()
	w.notify()
	// 刚拿到的是 cookie 里的账号名。昵称和头像要靠接口补，异步做，
	// 不让登录流程等它。
	if w.verify != nil {
		go w.verifySession(context.Background(), sess)
	}
	return nil
}

// writeSession 覆盖会话文件（temp + rename）。
func (w *WebLogin) writeSession(sess authSession) { writeAuthSession(w.platform, sess) }

func (w *WebLogin) notify() {
	w.mu.Lock()
	fn := w.on
	w.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// BrowserAuthFor 返回某个平台的浏览器登录入口；不支持的平台返回 false。
func BrowserAuthFor(p Platform) (*WebLogin, bool) {
	prov, ok := ProviderFor(p)
	if !ok {
		return nil, false
	}
	w, ok := prov.(interface{ webLogin() *WebLogin })
	if !ok {
		return nil, false
	}
	login := w.webLogin()
	return login, login != nil
}
