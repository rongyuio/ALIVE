package live

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 扫码登录。
//
// 各平台的账号是独立的：B 站的会话拿不到斗鱼的清晰度，切平台就得换账号。
// 所以登录态按平台拆开，每个平台一份状态、一份会话文件、一个 Authenticator。
//
// 平台之间真正不同的只有三件事：怎么出码、怎么问「扫了没」、确认之后怎么
// 落地会话。状态机（轮询节奏、失效处理、界面状态、落盘）是一样的，抽在
// qrAuth 里，平台各写一个 qrDriver。
//
// 网络请求和状态机全在后台 goroutine 上跑，界面每帧只读一份快照。

// AuthState 是登录态的快照，拿来就能画。
type AuthState struct {
	Platform Platform
	LoggedIn bool
	Name     string
	// Avatar 是平台返回的头像地址；空串表示用界面自己的占位头像。
	Avatar string
	// QR 是二维码 PNG（等待扫码时非空）。QRSeq 每换一张码自增，
	// 界面据此决定要不要重新解码成纹理。
	QR     []byte
	QRSeq  int
	Status string
	Err    string
}

// Authenticator 是能进入登录流程的平台。
type Authenticator interface {
	Platform() Platform
	Auth() AuthState
	// BeginLogin 开始一轮扫码；已登录时是空操作。
	BeginLogin()
	// RefreshQRCode 换一张新码（码过期后点刷新）。
	RefreshQRCode()
	Logout()
	// SetAuthListener 注册状态变化回调。回调在后台 goroutine 上跑，
	// 界面侧必须自己切回主线程。
	SetAuthListener(func())
}

// qrPollState 是一次扫码查询的结论。
type qrPollState int

const (
	// qrPending 还没人扫。
	qrPending qrPollState = iota
	// qrScanned 扫了但没在手机上确认。
	qrScanned
	// qrConfirmed 用户确认了，可以收尾。
	qrConfirmed
	// qrExpired 码失效或被取消。
	qrExpired
)

// qrPoll 是一次扫码查询的结果。msg 是平台给的原话，没有时用默认文案。
type qrPoll struct {
	state qrPollState
	msg   string
}

// authUser 是登录成功后的展示信息。
type authUser struct {
	UID    string
	Name   string
	Avatar string
}

// qrDriver 是扫码登录里跟平台协议绑定的那一小块。
//
// 实现必须保证同一时刻只有一轮登录在跑——状态机保证了这一点，
// 驱动可以把「这一轮的 key / 回调地址」直接放在自己身上。
type qrDriver interface {
	// beginQR 申请一张新码，返回二维码图片、轮询用的 key 和有效期。
	beginQR(ctx context.Context) (png []byte, key string, ttl time.Duration, err error)
	// pollQR 查一次扫码状态。
	pollQR(ctx context.Context, key string) (qrPoll, error)
	// finishQR 在用户确认后落地会话，返回展示用的用户信息。
	finishQR(ctx context.Context) (authUser, error)
	// verifyQR 在启动时确认存下来的会话还有效。
	verifyQR(ctx context.Context) (authUser, error)
	// logoutQR 清掉平台客户端上的登录态。
	logoutQR()
	// loadCookies 把上次存的会话灌回平台客户端。
	loadCookies(raw string)
	// cookieString 导出当前会话，用于落盘。
	cookieString() string
}

// 扫码节奏：B 站和斗鱼都是 3 秒一次。
const qrPollInterval = 3 * time.Second

// qrAuth 是各平台共用的扫码状态机。
type qrAuth struct {
	platform Platform
	drv      qrDriver

	mu     sync.Mutex
	state  AuthState
	on     func()
	cancel context.CancelFunc
	// seq 用来丢弃上一轮扫码的迟到回调：换码 / 退出之后旧 goroutine
	// 可能还在跑，它写回来的状态必须无效。
	seq int
}

func newQRAuth(p Platform, drv qrDriver) *qrAuth {
	a := &qrAuth{platform: p, drv: drv, state: AuthState{Platform: p}}
	a.restore()
	return a
}

func (a *qrAuth) Platform() Platform { return a.platform }

// Auth 返回当前登录态快照。
func (a *qrAuth) Auth() AuthState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// SetAuthListener 注册状态变化回调。
func (a *qrAuth) SetAuthListener(fn func()) {
	a.mu.Lock()
	a.on = fn
	a.mu.Unlock()
}

// BeginLogin 申请二维码并开始轮询扫码状态。
func (a *qrAuth) BeginLogin() {
	a.mu.Lock()
	if a.state.LoggedIn {
		a.mu.Unlock()
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.seq++
	seq := a.seq
	a.state = AuthState{Platform: a.platform, Status: "正在获取二维码…"}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	a.mu.Unlock()

	a.notify()
	go a.runLogin(ctx, seq)
}

// RefreshQRCode 取消当前这一轮、换一张新码。
func (a *qrAuth) RefreshQRCode() {
	if a.Auth().LoggedIn {
		return
	}
	a.BeginLogin()
}

// Logout 清掉本地会话和平台 cookie。
func (a *qrAuth) Logout() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.seq++
	a.state = AuthState{Platform: a.platform}
	a.mu.Unlock()

	a.drv.logoutQR()
	removeAuthSession(a.platform)
	a.notify()
}

// runLogin 申请二维码 → 轮询状态 → 确认后收尾。
func (a *qrAuth) runLogin(ctx context.Context, seq int) {
	defer func() {
		a.mu.Lock()
		if a.seq == seq {
			a.cancel = nil
		}
		a.mu.Unlock()
	}()

	png, key, ttl, err := a.drv.beginQR(ctx)
	if err != nil {
		a.fail(seq, "获取二维码失败："+err.Error())
		return
	}
	a.update(seq, func(s *AuthState) {
		s.QR = png
		s.QRSeq++
		s.Status = "打开" + platformDisplayName(a.platform) + "手机客户端扫码"
		s.Err = ""
	})

	// 有效期由平台给（B 站 180 秒、斗鱼 300 秒），到点就停，
	// 别让界面停在一张早就扫不动的码上。
	ctx, cancel := context.WithTimeout(ctx, ttl)
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			if a.isCurrent(seq) {
				a.expire(seq)
			}
			return
		case <-time.After(qrPollInterval):
		}

		poll, err := a.drv.pollQR(ctx, key)
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			a.fail(seq, "扫码状态查询失败："+err.Error())
			return
		}
		switch poll.state {
		case qrPending:
		case qrScanned:
			a.update(seq, func(s *AuthState) {
				s.Status = firstNonEmpty(poll.msg, "已扫码，请在手机上确认")
			})
		case qrExpired:
			a.expire(seq)
			return
		case qrConfirmed:
			user, err := a.drv.finishQR(ctx)
			if err != nil {
				a.fail(seq, "登录成功但读取账号信息失败："+err.Error())
				return
			}
			a.saveSession(user)
			a.update(seq, func(s *AuthState) {
				s.LoggedIn = true
				s.Name = user.Name
				s.Avatar = user.Avatar
				s.QR = nil
				s.Status = ""
				s.Err = ""
			})
			return
		}
	}
}

// restore 在启动时恢复上次的登录态。
//
// 先按存下来的昵称/头像把界面点亮，再后台校验会话还有没有效：
// 失效就立刻退回未登录——头像挂着「已登录」而请求全是匿名，
// 是最误导人的一种状态。
func (a *qrAuth) restore() {
	sess, ok := loadAuthSession(a.platform)
	if !ok || sess.Cookies == "" {
		return
	}
	a.drv.loadCookies(sess.Cookies)
	a.mu.Lock()
	a.state.LoggedIn = true
	a.state.Name = sess.Name
	a.state.Avatar = sess.Avatar
	a.mu.Unlock()
	go a.verify()
}

func (a *qrAuth) verify() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	user, err := a.drv.verifyQR(ctx)
	if err != nil {
		slog.Info("登录态已失效", "platform", a.platform, "error", err)
		a.drv.logoutQR()
		removeAuthSession(a.platform)
		a.mu.Lock()
		a.state = AuthState{Platform: a.platform}
		a.mu.Unlock()
		a.notify()
		return
	}
	a.saveSession(user)
	a.update(a.currentSeq(), func(s *AuthState) {
		s.LoggedIn = true
		s.Name = user.Name
		s.Avatar = user.Avatar
	})
}

func (a *qrAuth) currentSeq() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seq
}

func (a *qrAuth) isCurrent(seq int) bool { return a.currentSeq() == seq }

// update 在 seq 仍有效时改状态并通知界面。
func (a *qrAuth) update(seq int, fn func(*AuthState)) {
	a.mu.Lock()
	if a.seq != seq {
		a.mu.Unlock()
		return
	}
	fn(&a.state)
	a.mu.Unlock()
	a.notify()
}

// expire 处理二维码过期：清掉图，留一句提示和重试入口。
func (a *qrAuth) expire(seq int) {
	a.update(seq, func(s *AuthState) {
		s.QR = nil
		s.Status = "二维码已失效"
		s.Err = "点一下头像重新获取"
	})
}

func (a *qrAuth) fail(seq int, msg string) {
	a.update(seq, func(s *AuthState) {
		s.QR = nil
		s.Status = ""
		s.Err = msg
	})
}

func (a *qrAuth) notify() {
	a.mu.Lock()
	fn := a.on
	a.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// ---------------------------------------------------------------- 会话落盘

// authSession 是登录态的落盘形状。每个平台一个文件，互不覆盖。
//
// cookie 只写 %APPDATA%，不进日志、不进仓库。
type authSession struct {
	Cookies string `json:"cookies"`
	UID     string `json:"uid,omitempty"`
	Name    string `json:"name,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
}

func authSessionPath(p Platform) string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "alive", string(p)+".json")
}

func loadAuthSession(p Platform) (authSession, bool) {
	path := authSessionPath(p)
	if path == "" {
		return authSession{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return authSession{}, false
	}
	var sess authSession
	if json.Unmarshal(data, &sess) != nil {
		return authSession{}, false
	}
	return sess, true
}

func (a *qrAuth) saveSession(user authUser) {
	path := authSessionPath(a.platform)
	if path == "" {
		return
	}
	data, err := json.Marshal(authSession{
		Cookies: a.drv.cookieString(),
		UID:     user.UID,
		Name:    user.Name,
		Avatar:  user.Avatar,
	})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// 先写临时文件再 rename：写到一半退出也不会留下半个 JSON。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		slog.Warn("保存登录态失败", "platform", a.platform, "error", err)
		return
	}
	_ = os.Rename(tmp, path)
}

func removeAuthSession(p Platform) {
	path := authSessionPath(p)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

// ---------------------------------------------------------------- 对外入口

// AuthFor 返回某个平台的登录入口；该平台还不支持登录时返回 false。
func AuthFor(p Platform) (Authenticator, bool) {
	prov, ok := ProviderFor(p)
	if !ok {
		return nil, false
	}
	auth, ok := prov.(Authenticator)
	if !ok {
		return nil, false
	}
	return auth, true
}

// platformDisplayName 是提示文案里的平台名。
func platformDisplayName(p Platform) string {
	switch p {
	case Bilibili:
		return "B站"
	case Douyu:
		return "斗鱼"
	case Huya:
		return "虎牙"
	case Douyin:
		return "抖音"
	}
	return string(p)
}

// errSessionInvalid 是「会话无效」的哨兵错误，驱动用它区分网络错误。
var errSessionInvalid = fmt.Errorf("会话无效")
