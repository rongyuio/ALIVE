package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"alive/internal/diag"
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
	// NeedCode 表示平台要求填短信验证码：界面这时显示输入框而不是二维码。
	// 目前只有抖音的扫码登录会走到这里。
	NeedCode bool
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

// cookieRenewer 是能续期登录 cookie 的驱动。可选实现：不支持续期的平台
// 不实现它，qrAuth 就不会碰会话文件。
//
// renew 成功时返回新的 cookie 串与可选的刷新凭据（B 站的 refresh_token），
// 调用方负责落盘；失败原样返回，绝不覆盖存下来的会话——续不上就下次再试，
// 把好会话删掉才是灾难。
type cookieRenewer interface {
	renew(ctx context.Context, sess authSession) (cookies, refreshToken string, err error)
}

// cookieForceRenewer 是能跳过「还不到期」闸门、立刻续期的驱动。
//
// 平台可能比本地 exp 更早判定票据失效（服务端作废），到期时间看不出来；
// 取流被平台以「鉴权失败」拒绝时，需要一条强制续期的路径。
type cookieForceRenewer interface {
	forceRenew(ctx context.Context, sess authSession) (cookies, refreshToken string, err error)
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
	// renewer 是平台的 cookie 续期实现；nil 表示该平台不支持。
	renewer cookieRenewer
	// renewCancel 停掉上一轮续期的定时器，换号 / 退出时调用。
	renewCancel context.CancelFunc
}

func newQRAuth(p Platform, drv qrDriver) *qrAuth {
	a := &qrAuth{platform: p, drv: drv, state: AuthState{Platform: p}}
	if r, ok := drv.(cookieRenewer); ok {
		a.renewer = r
	}
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
	a.stopRenew()
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
			a.scheduleRenew()
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
	if r, ok := a.drv.(interface{ setRefreshToken(string) }); ok {
		r.setRefreshToken(sess.RefreshToken)
	}
	a.mu.Lock()
	a.state.LoggedIn = true
	a.state.Name = sess.Name
	a.state.Avatar = sess.Avatar
	// 代次在启动校验的那一刻就定下来：goroutine 什么时候被调度不由这里
	// 决定，等 verify 自己读 seq 时用户可能已经退出登录了。
	seq := a.seq
	a.mu.Unlock()
	go a.verifyAt(seq)
}

// verifyAt 在后台确认存下来的会话还有效。seq 是发起这次校验时的代次；
// 请求在飞的时候用户可能已经退出 / 换了号，那之后返回的结果整份作废。
func (a *qrAuth) verifyAt(seq int) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	user, err := a.drv.verifyQR(ctx)
	if err != nil {
		// 只有平台明确说「这个会话不认了」才删登录态。verifyQR 是真实网络
		// 请求，断网、超时、5xx 都会返回普通 error；把它们当失效处理，
		// 等于离线启动一次就静默删掉用户还能用的登录，和 renewOnce
		// 「续不上就下次再试，绝不删会话」的约定相反。验证不上就先保留，
		// 状态标成「稍后重试」。
		if !errors.Is(err, errSessionInvalid) {
			diag.L().Info().Str("platform", string(a.platform)).Dict(diag.ErrField, diag.Err(err)).Msg("登录态校验失败，保留本地会话")
			a.update(seq, func(s *AuthState) {
				s.Status = "登录态校验失败，稍后重试"
			})
			return
		}
		// 失效判定也只对发起它的那一轮有效：用户已经退出 / 换了号时，
		// 这里再清一次会把新一轮刚建立的状态一起带走。
		if !a.isCurrent(seq) {
			return
		}
		diag.L().Info().Str("platform", string(a.platform)).Dict(diag.ErrField, diag.Err(err)).Msg("登录态已失效")
		a.drv.logoutQR()
		removeAuthSession(a.platform)
		a.stopRenew()
		a.mu.Lock()
		if a.seq == seq {
			a.state = AuthState{Platform: a.platform}
		}
		a.mu.Unlock()
		a.notify()
		return
	}
	if !a.commitVerify(seq, user) {
		return
	}
	a.scheduleRenew()
}

// commitVerify 把校验成功的结果写回状态；代次变了就整份丢弃。
//
// 落盘和状态更新放在同一把锁里：Logout 要么发生在这之前（seq++，这里直接
// 退出，不会再写回已被删掉的会话），要么发生在这之后（它会把刚写的文件
// 再删掉）。两步分在锁外做，中间那条缝就是「退出后又亮回来」。
func (a *qrAuth) commitVerify(seq int, user authUser) bool {
	a.mu.Lock()
	if a.seq != seq {
		a.mu.Unlock()
		return false
	}
	a.saveSession(user)
	a.state.LoggedIn = true
	a.state.Name = user.Name
	a.state.Avatar = user.Avatar
	a.mu.Unlock()
	a.notify()
	return true
}

// cookieRenewCheckInterval 是两次续期检查之间的间隔。
//
// 具体要不要续由平台的 renew 自己判断（本地读 JWT / SESSDATA 的到期时刻，
// 零网络）。这里只是定时唤醒：一天一次够用，斗鱼一次续期推 7 天、B 站
// 推 180 天，留足失败重试的时间。
const cookieRenewCheckInterval = 24 * time.Hour

// scheduleRenew 启动定时续期。只在已登录且有续期实现时做事。
func (a *qrAuth) scheduleRenew() {
	if a.renewer == nil || !a.Auth().LoggedIn {
		return
	}
	a.stopRenew()
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.renewCancel = cancel
	a.mu.Unlock()
	go a.runRenew(ctx)
}

func (a *qrAuth) stopRenew() {
	a.mu.Lock()
	cancel := a.renewCancel
	a.renewCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *qrAuth) runRenew(ctx context.Context) {
	// 启动时先检查一次：verify 刚确认会话有效，但 B 站的 SESSDATA 可能
	// 本来就已经快到期（用户很久没开客户端）。之后按天检查。
	for {
		if !a.Auth().LoggedIn {
			return
		}
		a.renewOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(cookieRenewCheckInterval):
		}
	}
}

func (a *qrAuth) renewOnce(ctx context.Context) {
	sess, ok := loadAuthSession(a.platform)
	if !ok || sess.Cookies == "" {
		return
	}
	// 捕获代次：续期请求在飞的时候用户可能退出登录（Logout 会 seq++ 并删
	// 会话文件）。回来时不再核对，就会把服务端仍有效的票据重新写回磁盘，
	// 下次启动 restore 又把账号点亮——和 verify 那条是同一个形状。
	seq := a.currentSeq()
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cookies, token, changed, err := applyRenew(c, a.renewer, sess)
	if err != nil {
		var skip errNoRenewNeeded
		if errors.As(err, &skip) {
			return
		}
		// 续期失败只记日志：续不上就下次再试，绝不删存下来的会话。
		diag.L().Info().Str("platform", string(a.platform)).Dict(diag.ErrField, diag.Err(err)).Msg("cookie 续期失败")
		return
	}
	if !changed {
		return
	}
	if !a.commitRenew(seq, cookies, token, sess) {
		diag.L().Info().Str("platform", string(a.platform)).Msg("续期期间登录态已变化，丢弃结果")
		return
	}
	diag.L().Info().Str("platform", string(a.platform)).Msg("cookie 已续期")
}

// commitRenew 在登录代次未变时把续期结果写回驱动并落盘。
//
// 检查、写驱动、落盘放在同一把锁里：Logout 也要拿这把锁推进代次，所以
// 要么续期先完成、随后 Logout 把会话删掉；要么 Logout 先完成、这里整份
// 丢弃。两种顺序都不会留下「界面已登出、磁盘上还躺着有效会话」的裂缝。
func (a *qrAuth) commitRenew(seq int, cookies, token string, sess authSession) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seq != seq {
		return false
	}
	a.drv.loadCookies(cookies)
	sess.Cookies = cookies
	if token != "" {
		sess.RefreshToken = token
		if r, ok := a.drv.(interface{ setRefreshToken(string) }); ok {
			r.setRefreshToken(token)
		}
	}
	a.writeSession(sess)
	return true
}

// RenewNow 立刻续期一次登录票据，跳过到期闸门。
//
// 取流被平台以「鉴权失败」拒绝时用它：本地读到的 exp 可能还远，但服务端
// 已经不认这张票据了。成功时新 cookie 写回驱动并落盘，界面状态不变。
func (a *qrAuth) RenewNow(ctx context.Context) error {
	fr, ok := a.renewer.(cookieForceRenewer)
	if !ok {
		return errNoRenewNeeded{"该平台不支持强制续期"}
	}
	if !a.Auth().LoggedIn {
		return errNoRenewNeeded{"当前是游客"}
	}
	seq := a.currentSeq()

	// 有会话文件就沿用（带 UID / 昵称 / refresh token），cookie 以驱动
	// 当前持有的为准——它可能比文件新。
	sess := authSession{Cookies: a.drv.cookieString()}
	if stored, ok := loadAuthSession(a.platform); ok {
		sess = stored
		sess.Cookies = a.drv.cookieString()
	}
	if sess.Cookies == "" {
		return errSessionInvalid
	}

	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cookies, token, err := fr.forceRenew(c, sess)
	if err != nil {
		return err
	}
	if cookies == "" || cookies == sess.Cookies {
		return errNoRenewNeeded{"续期没有返回新票据"}
	}
	if !a.commitRenew(seq, cookies, token, sess) {
		return errNoRenewNeeded{"续期期间登录态已变化"}
	}
	diag.L().Info().Str("platform", string(a.platform)).Msg("cookie 已强制续期")
	return nil
}

// writeSession 用现成的 authSession 覆盖会话文件（temp + rename）。
func (a *qrAuth) writeSession(sess authSession) { writeAuthSession(a.platform, sess) }

// writeAuthSession 是各登录实现共用的会话落盘。
func writeAuthSession(p Platform, sess authSession) {
	path := authSessionPath(p)
	if path == "" {
		return
	}
	data, err := json.Marshal(sess)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		diag.L().Warn().Str("platform", string(p)).Dict(diag.ErrField, diag.Err(err)).Msg("保存登录态失败")
		return
	}
	_ = os.Rename(tmp, path)
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
	// RefreshToken 是 B 站 cookie 刷新的持久化凭据；其他平台留空。
	// 它不是 cookie、绝不能进日志或界面，只跟着会话文件落盘。
	RefreshToken string `json:"refresh_token,omitempty"`
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
	// refresh token 存在驱动侧（扫码时写入），不放在 user 里：
	// 它不是展示信息，只有落盘这一个去处。
	token := ""
	if r, ok := a.drv.(interface{ refreshToken() string }); ok {
		token = r.refreshToken()
	}
	data, err := json.Marshal(authSession{
		Cookies:      a.drv.cookieString(),
		UID:          user.UID,
		Name:         user.Name,
		Avatar:       user.Avatar,
		RefreshToken: token,
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
		diag.L().Warn().Str("platform", string(a.platform)).Dict(diag.ErrField, diag.Err(err)).Msg("保存登录态失败")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		diag.L().Warn().Str("platform", string(a.platform)).Dict(diag.ErrField, diag.Err(err)).Msg("登录态落盘失败")
		return
	}
	diag.L().Info().Str("platform", string(a.platform)).Str("path", path).Msg("登录态已落盘")
}

func removeAuthSession(p Platform) {
	path := authSessionPath(p)
	if path == "" {
		return
	}
	if err := os.Remove(path); err == nil {
		diag.L().Info().Str("platform", string(p)).Str("path", path).Msg("登录态已删除")
	}
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

// applyRenew 是一次续期的纯逻辑：调驱动、判断有无变化、拼新会话。
//
// 单独抽出来是为了测试「失败 / 没变化时不返回新会话」这个不变量——
// renewOnce 直接读写 %APPDATA%，测试里碰不得真实会话文件。
func applyRenew(ctx context.Context, r cookieRenewer, sess authSession) (cookies, token string, changed bool, err error) {
	cookies, token, err = r.renew(ctx, sess)
	if err != nil {
		return "", "", false, err
	}
	if cookies == "" || cookies == sess.Cookies {
		return "", "", false, nil
	}
	return cookies, token, true, nil
}
