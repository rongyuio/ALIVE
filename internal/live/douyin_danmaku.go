package live

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/dop251/goja"
)

// 抖音弹幕：一条 WebSocket 连接 + protobuf 帧，payload 是 gzip 压缩的 Response。
//
// 与 B 站不同，抖音的 WS 要在 query 上带 signature，签名算法在 web 端的
// sign.js 里（485KB 混淆代码），所以这里用 goja 跑一份原样拷贝。链路是三步：
//  1. 打开房间页，收下 ttwid 等 cookie，并从页面内嵌 JSON 里读出内部 roomId；
//  2. 用固定的 web 端参数拼 WS query，把其中 12 个键按固定顺序拼串做 MD5；
//  3. 把 MD5 交给 sign.js 的 get_sign() 得到 signature。
//
// 参数顺序、字符串内容一个都不能改：改一个字节签名就对不上，抖音不会报错，
// 而是直接关闭连接或干脆不推消息。所以这一段是照参考实现逐行搬的，不做"优化"。
const (
	douyinDanmuHeartbeat = 5 * time.Second
	// 超过这么久没收到任何帧就认为连接已死。
	douyinDanmuReadTO   = 90 * time.Second
	douyinDanmuDialTO   = 15 * time.Second
	douyinDanmuMaxFrame = 4 << 20
	// 断线退避：1s 起，最多 30s；连接稳住 10s 后下一次重新从 1s 开始。
	douyinDanmuMaxBack = 30 * time.Second
	douyinDanmuStable  = 10 * time.Second
	douyinDanmuOrigin  = "https://live.douyin.com"
)

// douyinWSHosts 是抖音弹幕 WS 的候选域名，按顺序逐个尝试。
var douyinWSHosts = []string{
	"webcast3-ws-web-lq.douyin.com",
	"webcast5-ws-web-lf.douyin.com",
	"webcast5-ws-web-hl.douyin.com",
	"webcast3-ws-web-hl.douyin.com",
	"webcast3-ws-web-lf.douyin.com",
}

//go:embed assets/douyin/sign.js
var douyinSignJS string

var (
	// sign.js 的编译结果与运行时进程内共享：编译一次要几百毫秒，
	// 而 goja 的 Runtime 不是并发安全的，所以用一把锁串行化。
	douyinSignMu sync.Mutex
	douyinSignVM *goja.Runtime

	// 房间页请求要带 cookie jar：ttwid 是弹幕握手需要的，而 apiClient
	// 共用的是没有 jar 的 http.Client，所以这里单独一份。
	douyinPageOnce sync.Once
	douyinPageHTTP *http.Client
)

// douyinWSCreds 是建立一条弹幕连接所需的全部凭据。
type douyinWSCreds struct {
	webRID       string
	internalID   string // 19 位内部 roomId，WS query 要这个
	userUniqueID string // 从 cookie 里取，取不到用时间戳兜底
	cookie       string
}

// douyinDanmaku 是一次房间监听会话。
type douyinDanmaku struct {
	provider  *douyinProvider
	webRID    string
	creds     douyinWSCreds
	onMessage func(Danmu)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	mu   sync.Mutex
	conn *websocket.Conn
	seq  int64
}

// Danmaku 开始监听一个房间的弹幕。
//
// 凭据（内部房间号 + cookie）在返回前就抓好：房间号不合法、房间不存在这类
// 错误要让调用方立刻看到，而不是先拿到一个"成功"的流再在后台无限重试。
func (p *douyinProvider) Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error) {
	webRID := douyinNormalizeRoomID(roomID)
	if webRID == "" {
		return nil, &UpstreamError{
			Platform: Douyin, Op: "弹幕",
			Message: fmt.Sprintf("房间号 %q 无效", strings.TrimSpace(roomID)),
		}
	}
	if onMessage == nil {
		onMessage = func(Danmu) {}
	}

	creds, err := p.douyinWSCredentials(ctx, webRID)
	if err != nil {
		return nil, err
	}

	s := &douyinDanmaku{
		provider:  p,
		webRID:    webRID,
		creds:     creds,
		onMessage: onMessage,
		done:      make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
	return s, nil
}

// douyinPageClient 返回带 cookie jar 的页面请求客户端（进程内复用）。
func douyinPageClient() *http.Client {
	douyinPageOnce.Do(func() {
		jar, err := cookiejar.New(nil)
		if err != nil {
			// 只有 nil 存储源会失败，实际不会发生；退化成无 jar 的客户端。
			douyinPageHTTP = &http.Client{Timeout: httpTimeout}
			return
		}
		douyinPageHTTP = &http.Client{Timeout: httpTimeout, Jar: jar}
	})
	return douyinPageHTTP
}

// douyinWSCredentials 打开房间页，取出内部 roomId 与 cookie。
//
// 页面正常体量在 1MB 以上，风控/验证页同样是 HTTP 200 但体积远小于
// douyinRoomPageMin；按"房间不存在"处理会让上层静默无结果，所以判成风控。
func (p *douyinProvider) douyinWSCredentials(ctx context.Context, webRID string) (douyinWSCreds, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return douyinWSCreds{}, err
	}
	defer release()

	pageURL := douyinURLRoomPage + webRID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return douyinWSCreds{}, p.wrap("弹幕房间页", fmt.Errorf("构造请求失败: %w", err))
	}
	p.api.apply(req, header{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})

	resp, err := douyinPageClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return douyinWSCreds{}, ctx.Err()
		}
		return douyinWSCreds{}, p.wrap("弹幕房间页", fmt.Errorf("请求失败: %w", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, httpMaxBody))
	if err != nil {
		return douyinWSCreds{}, p.wrap("弹幕房间页", fmt.Errorf("读取响应失败: %w", err))
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusPreconditionFailed, http.StatusTooManyRequests:
		return douyinWSCreds{}, p.wrap("弹幕房间页", fmt.Errorf("%w: HTTP %d", ErrBlocked, resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return douyinWSCreds{}, &UpstreamError{
			Platform: Douyin, Op: "弹幕房间页",
			Status: resp.StatusCode, Message: snippet(body),
		}
	}
	if len(body) < douyinRoomPageMin {
		return douyinWSCreds{}, p.wrap("弹幕房间页",
			fmt.Errorf("%w: 只返回 %d 字节，疑似风控页", ErrBlocked, len(body)))
	}

	internalID := douyinExtractRoomID(string(body))
	if internalID == "" {
		return douyinWSCreds{}, &UpstreamError{
			Platform: Douyin, Op: "弹幕",
			Message: fmt.Sprintf("房间 %s 不存在", webRID),
		}
	}

	cookie := douyinPageCookie(pageURL)
	userUniqueID := douyinCookieValue(cookie, "s_v_web_id")
	if userUniqueID == "" {
		userUniqueID = douyinCookieValue(cookie, "ttwid")
	}
	if userUniqueID == "" {
		// 服务端只要求这个字段存在且稳定，取不到 cookie 时用时间戳兜底。
		userUniqueID = strconv.FormatInt(time.Now().UnixMilli(), 10)
	}

	return douyinWSCreds{
		webRID:       webRID,
		internalID:   internalID,
		userUniqueID: userUniqueID,
		cookie:       cookie,
	}, nil
}

// douyinPageCookie 把 jar 里该地址的 cookie 拼成 "k=v; k2=v2"。
//
// 弹幕 WS 的 cookie 必须显式放进握手头：WS 不走 HTTP 的 cookie jar。
func douyinPageCookie(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	cookies := douyinPageClient().Jar.Cookies(u)
	parts := make([]string, 0, len(cookies))
	for _, c := range cookies {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// douyinCookieValue 从 "k=v; k2=v2" 里取指定 key 的值。
func douyinCookieValue(cookie, key string) string {
	for _, kv := range strings.Split(cookie, ";") {
		kv = strings.TrimSpace(kv)
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

/* ------------------------------------------------------------- 会话生命周期 */

// Close 停止会话。重复调用安全。
func (s *douyinDanmaku) Close() {
	s.once.Do(func() {
		s.cancel()

		s.mu.Lock()
		conn := s.conn
		s.conn = nil
		s.mu.Unlock()
		if conn != nil {
			_ = conn.CloseNow()
		}

		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	})
}

// run 是会话主循环：断线后按退避重连，直到 Close。
func (s *douyinDanmaku) run() {
	defer close(s.done)

	backoff := time.Second
	for {
		started := time.Now()
		err := s.connectOnce()
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("抖音弹幕断开", "room", s.webRID, "error", err, "retry", backoff)
		}
		if time.Since(started) >= douyinDanmuStable {
			backoff = time.Second
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff < douyinDanmuMaxBack {
			backoff *= 2
			if backoff > douyinDanmuMaxBack {
				backoff = douyinDanmuMaxBack
			}
		}
	}
}

// connectOnce 拼 query、签名，逐个域名试连，连上后读到断开。
//
// 签名只依赖 query（与域名无关），所以整个循环只算一次。
func (s *douyinDanmaku) connectOnce() error {
	query := douyinWSQuery("", "", s.creds.userUniqueID, s.creds.internalID)
	sig, err := douyinWSSignature(query)
	if err != nil {
		return err
	}

	var lastErr error
	for _, host := range douyinWSHosts {
		if s.ctx.Err() != nil {
			return nil
		}
		target := "wss://" + host + "/webcast/im/push/v2/?" + query + "&signature=" + sig

		dialCtx, cancel := context.WithTimeout(s.ctx, douyinDanmuDialTO)
		conn, _, err := websocket.Dial(dialCtx, target, &websocket.DialOptions{
			HTTPHeader: douyinWSHeaders(s.creds.cookie),
		})
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("连接 %s 失败: %w", host, err)
			slog.Debug("抖音弹幕握手失败", "host", host, "room", s.webRID, "error", err)
			continue
		}
		conn.SetReadLimit(douyinDanmuMaxFrame)

		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()

		runErr := s.readLoop(conn)

		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		_ = conn.CloseNow()

		// 连上了又断开：换域名也没用，交给上层退避重连。
		return runErr
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的抖音弹幕域名")
	}
	return lastErr
}

// douyinWSHeaders 构造 WS 握手头。cookie 为空时不带 Cookie 字段。
func douyinWSHeaders(cookie string) http.Header {
	h := http.Header{
		"User-Agent": {desktopUA},
		"Referer":    {douyinReferer},
		"Origin":     {douyinDanmuOrigin},
	}
	if cookie != "" {
		h.Set("Cookie", cookie)
	}
	return h
}

// readLoop 跑心跳并逐帧读取。
func (s *douyinDanmaku) readLoop(conn *websocket.Conn) error {
	hbCtx, stopHeartbeat := context.WithCancel(s.ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(douyinDanmuHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				frame := douyinPushFrame{PayloadType: "hb"}.encode()
				if err := conn.Write(hbCtx, websocket.MessageBinary, frame); err != nil {
					return
				}
			}
		}
	}()

	for {
		if s.ctx.Err() != nil {
			return nil
		}
		readCtx, cancel := context.WithTimeout(s.ctx, douyinDanmuReadTO)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("读取帧: %w", err)
		}
		s.handleFrame(conn, data)
	}
}

// handleFrame 解析一帧：解压 → 解析 → ACK → 逐条推弹幕。
func (s *douyinDanmaku) handleFrame(conn *websocket.Conn, data []byte) {
	frame, err := douyinDecodePushFrame(data)
	if err != nil {
		slog.Debug("抖音弹幕帧解析失败", "room", s.webRID, "error", err)
		return
	}
	if frame.PayloadType != "msg" || len(frame.Payload) == 0 {
		return
	}

	// payload 是 gzip 压缩的 Response（query 里 compress=gzip）。
	zr, err := gzip.NewReader(bytes.NewReader(frame.Payload))
	if err != nil {
		slog.Debug("抖音弹幕 gzip 打开失败", "room", s.webRID, "error", err)
		return
	}
	plain, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		slog.Debug("抖音弹幕 gzip 解压失败", "room", s.webRID, "error", err)
		return
	}

	resp, err := douyinDecodeResponse(plain)
	if err != nil {
		slog.Debug("抖音弹幕 Response 解析失败", "room", s.webRID, "error", err)
		return
	}

	// 需要 ACK 时回一帧，payload 就是 internalExt 的原始 protobuf 字节。
	// 不回 ACK 的话服务端会认为消息没消费，很快停止推送。
	if resp.NeedAck && resp.InternalExt != "" {
		ack := douyinPushFrame{
			LogID:       frame.LogID,
			PayloadType: "ack",
			Payload:     []byte(resp.InternalExt),
		}
		if err := conn.Write(s.ctx, websocket.MessageBinary, ack.encode()); err != nil {
			slog.Debug("抖音弹幕发送 ACK 失败", "room", s.webRID, "error", err)
		}
	}

	for _, msg := range resp.Messages {
		switch msg.Method {
		case "WebcastEmojiChatMessage":
			s.handleEmojiChat(msg.Payload)
			continue
		case "WebcastChatMessage":
		default:
			continue
		}
		chat, err := douyinDecodeChatMessage(msg.Payload)
		if err != nil {
			slog.Debug("抖音聊天消息解析失败", "room", s.webRID, "error", err)
			continue
		}
		content := strings.TrimSpace(chat.Content)
		if content == "" && len(chat.Emotes) == 0 {
			continue
		}
		nickname := strings.TrimSpace(chat.Nickname)
		if nickname == "" {
			nickname = "匿名"
		}
		at := time.Now()
		s.emit(Danmu{
			ID:       s.nextID(at),
			Nickname: nickname,
			Content:  content,
			Level:    int(chat.UserLevel),
			Medal:    chat.Medal,
			// 抖音聊天消息不带颜色，留给 UI 按昵称哈希取色。
			Color:  "",
			Kind:   DanmuChat,
			At:     at,
			Emotes: chat.Emotes,
		})
	}
}

// emit 归一化后回调。
func (s *douyinDanmaku) emit(msg Danmu) {
	if msg.At.IsZero() {
		msg.At = time.Now()
	}
	if msg.ID == "" {
		msg.ID = s.nextID(msg.At)
	}
	if msg.Kind == "" {
		msg.Kind = DanmuChat
	}
	s.onMessage(msg)
}

// nextID 生成稳定递增的消息 id（同一毫秒可能有多条）。
func (s *douyinDanmaku) nextID(at time.Time) string {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return fmt.Sprintf("douyin:%s:%d:%d", s.webRID, at.UnixMilli(), seq)
}

/* ---------------------------------------------------------------- 签名 */

// douyinWSQuery 组装 WS 的 query（顺序与参考实现完全一致）。
func douyinWSQuery(cursor, internalExt, userUniqueID, roomID string) string {
	return "app_name=douyin_web&version_code=180800" +
		"&webcast_sdk_version=1.0.14-beta.0&update_version_code=1.0.14-beta.0" +
		"&compress=gzip&device_platform=web&cookie_enabled=true" +
		"&screen_width=1536&screen_height=864&browser_language=zh-CN" +
		"&browser_platform=Win32&browser_name=Mozilla" +
		"&browser_version=5.0%20(Windows%20NT%2010.0;%20Win64;%20x64)%20AppleWebKit/537.36%20(KHTML,%20like%20Gecko)%20Chrome/126.0.0.0%20Safari/537.36" +
		"&browser_online=true&tz_name=Asia/Shanghai" +
		"&cursor=" + cursor +
		"&internal_ext=" + internalExt +
		"&host=https://live.douyin.com&aid=6383&live_id=1&did_rule=3&endpoint=live_pc" +
		"&support_wrds=1&user_unique_id=" + userUniqueID +
		"&im_path=/webcast/im/fetch/&identity=audience&need_persist_msg_count=15" +
		"&insert_task_id=&live_reason=&room_id=" + roomID + "&heartbeatDuration=0"
}

// douyinWSSignature 计算 WS 连接需要的 signature。
//
// 步骤（对齐参考实现 signature.rs）：
//  1. 从 query 里取固定 12 个参数，按固定顺序拼成 "k=v,k=v,..."
//  2. 对整个串做 MD5
//  3. 交给 sign.js 的 get_sign(md5) 得到最终签名
func douyinWSSignature(query string) (string, error) {
	// 顺序必须与参考实现一致，改顺序签名就废。
	keys := []string{
		"live_id", "aid", "version_code", "webcast_sdk_version", "room_id",
		"sub_room_id", "sub_channel_id", "did_rule", "user_unique_id",
		"device_platform", "device_type", "ac", "identity",
	}

	values := douyinQueryValues(query)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+values[k])
	}

	sum := md5.Sum([]byte(strings.Join(parts, ",")))
	return douyinSign(hex.EncodeToString(sum[:]))
}

// douyinSign 调 sign.js 的 get_sign()。
//
// 运行时只建一次：sign.js 是 485KB 混淆代码，编译加执行要几百毫秒，
// 而 get_sign 是纯函数，同一个运行时反复调用即可。
func douyinSign(md5Hex string) (string, error) {
	douyinSignMu.Lock()
	defer douyinSignMu.Unlock()

	if douyinSignVM == nil {
		prog, err := goja.Compile("douyin-sign.js", douyinSignJS, false)
		if err != nil {
			return "", fmt.Errorf("编译抖音签名脚本失败: %w", err)
		}
		vm := douyinNewSignVM()
		if _, err := douyinRunGuarded(vm, func() (goja.Value, error) {
			return vm.RunProgram(prog)
		}); err != nil {
			return "", fmt.Errorf("执行抖音签名脚本失败: %w", err)
		}
		douyinSignVM = vm
	}

	// md5Hex 是十六进制串，直接内联进表达式不会破坏语法。
	expr := "get_sign('" + md5Hex + "')"
	v, err := douyinRunGuarded(douyinSignVM, func() (goja.Value, error) {
		return douyinSignVM.RunString(expr)
	})
	if err != nil {
		return "", fmt.Errorf("计算抖音弹幕签名失败: %w", err)
	}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return "", errors.New("抖音签名脚本返回空值")
	}
	sig := strings.TrimSpace(v.String())
	if sig == "" {
		return "", errors.New("抖音签名脚本返回空串")
	}
	return sig, nil
}

// douyinNewSignVM 建一个带最小浏览器 shim 的 goja 运行时。
//
// sign.js 顶层会自己定义 document / window / navigator，但会先探测 window
// 上的属性；缺了这些全局对象部分分支会直接抛 ReferenceError。
func douyinNewSignVM() *goja.Runtime {
	vm := goja.New()
	global := vm.GlobalObject()
	_ = vm.Set("window", global)
	_ = vm.Set("self", global)
	_ = vm.Set("globalThis", global)
	_ = vm.Set("console", map[string]any{
		"log":   func(...any) {},
		"warn":  func(...any) {},
		"error": func(...any) {},
		"info":  func(...any) {},
	})
	return vm
}

// douyinRunGuarded 在超时保护下执行一段 goja 调用。
//
// 混淆脚本里只要死循环就能把整个进程卡住；超时后清掉 interrupt 标记，
// 让运行时还能继续服务后续房间。
func douyinRunGuarded(vm *goja.Runtime, fn func() (goja.Value, error)) (goja.Value, error) {
	timer := time.AfterFunc(5*time.Second, func() {
		vm.Interrupt("douyin sign timeout")
	})
	defer timer.Stop()

	v, err := fn()
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			vm.ClearInterrupt()
			return nil, errors.New("签名脚本执行超时")
		}
		return nil, err
	}
	return v, nil
}

// douyinQueryValues 把 query 拆成 key→value（取值前先做 URL 解码）。
func douyinQueryValues(query string) map[string]string {
	out := map[string]string{}
	if i := strings.Index(query, "#"); i >= 0 {
		query = query[:i]
	}
	for _, kv := range strings.Split(query, "&") {
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if dec, err := url.QueryUnescape(v); err == nil {
			v = dec
		}
		out[k] = v
	}
	return out
}

/* ------------------------------------------------------------- protobuf */

// 抖音弹幕协议用到的 protobuf 子集，手写 wire-format 解析。
//
// 参考实现的 douyin.proto 有 700 多行，真正要解析的只有 PushFrame /
// Response / Message / ChatMessage / User 里几个字段；手写比引入 protoc
// 和生成代码轻，也避免生成物进仓库。
//
// 字段号（已对照 douyin.proto）：
//
//	PushFrame  : logId=2 varint, payloadType=7 string, payload=8 bytes
//	Response   : messages=1 repeated Message, internalExt=5 string, needAck=9 bool
//	Message    : method=1 string, payload=2 bytes
//	ChatMessage: user=2 message, content=3 string
//	User       : nickName=3 string, payGrade=23 message
//	PayGrade   : level=3 varint
const (
	douyinPBWireVarint  = 0
	douyinPBWireFixed64 = 1
	douyinPBWireBytes   = 2
	douyinPBWireFixed32 = 5
)

// douyinPBField 是一个解析出来的 protobuf 字段。
type douyinPBField struct {
	Num    int
	Wire   int
	Varint uint64
	Bytes  []byte
}

// douyinPBReadFields 顺序扫描一段 protobuf 数据。
func douyinPBReadFields(data []byte) ([]douyinPBField, error) {
	var out []douyinPBField
	i := 0
	for i < len(data) {
		key, n := binary.Uvarint(data[i:])
		if n <= 0 {
			return nil, fmt.Errorf("protobuf: key 解析失败（偏移 %d）", i)
		}
		i += n

		num := int(key >> 3)
		wire := int(key & 0x7)
		if num <= 0 {
			return nil, fmt.Errorf("protobuf: 字段号非法 %d", num)
		}

		field := douyinPBField{Num: num, Wire: wire}
		switch wire {
		case douyinPBWireVarint:
			v, m := binary.Uvarint(data[i:])
			if m <= 0 {
				return nil, fmt.Errorf("protobuf: varint 解析失败（字段 %d）", num)
			}
			i += m
			field.Varint = v
		case douyinPBWireBytes:
			l, m := binary.Uvarint(data[i:])
			if m <= 0 {
				return nil, fmt.Errorf("protobuf: 长度解析失败（字段 %d）", num)
			}
			i += m
			// 先做无符号比较：l ≥ 2^63 时 int(l) 会变成负数，end 落在 i 之前，
			// 下面的 end > len(data) 就拦不住，data[i:end] 直接越界 panic。
			if l > uint64(len(data)-i) {
				return nil, fmt.Errorf("protobuf: 字段 %d 越界（需要 %d，剩余 %d）", num, l, len(data)-i)
			}
			end := i + int(l)
			field.Bytes = data[i:end]
			i = end
		case douyinPBWireFixed64:
			if len(data)-i < 8 {
				return nil, fmt.Errorf("protobuf: 字段 %d fixed64 越界（剩余 %d）", num, len(data)-i)
			}
			i += 8
		case douyinPBWireFixed32:
			if len(data)-i < 4 {
				return nil, fmt.Errorf("protobuf: 字段 %d fixed32 越界（剩余 %d）", num, len(data)-i)
			}
			i += 4
		default:
			return nil, fmt.Errorf("protobuf: 不支持的 wire type %d（字段 %d）", wire, num)
		}
		out = append(out, field)
	}
	return out, nil
}

// douyinPBBytes 返回第一个指定字段号的 bytes 值。
func douyinPBBytes(fields []douyinPBField, num int) []byte {
	for _, f := range fields {
		if f.Num == num && f.Wire == douyinPBWireBytes {
			return f.Bytes
		}
	}
	return nil
}

// douyinPBString 返回第一个指定字段号的字符串值。
func douyinPBString(fields []douyinPBField, num int) string {
	return string(douyinPBBytes(fields, num))
}

// douyinPBVarint 返回第一个指定字段号的整数值。
func douyinPBVarint(fields []douyinPBField, num int) uint64 {
	for _, f := range fields {
		if f.Num == num && f.Wire == douyinPBWireVarint {
			return f.Varint
		}
	}
	return 0
}

// douyinPBBool 返回第一个指定字段号的布尔值。
func douyinPBBool(fields []douyinPBField, num int) bool {
	return douyinPBVarint(fields, num) != 0
}

// douyinPBAppendVarint 追加一个 varint 编码。
func douyinPBAppendVarint(buf []byte, v uint64) []byte {
	for v >= 0x80 {
		buf = append(buf, byte(v)|0x80)
		v >>= 7
	}
	return append(buf, byte(v))
}

// douyinPBAppendTag 追加一个字段头。
func douyinPBAppendTag(buf []byte, num, wire int) []byte {
	return douyinPBAppendVarint(buf, uint64(num)<<3|uint64(wire))
}

// douyinPBAppendString 追加一个 string 字段（空值跳过）。
func douyinPBAppendString(buf []byte, num int, s string) []byte {
	if s == "" {
		return buf
	}
	buf = douyinPBAppendTag(buf, num, douyinPBWireBytes)
	buf = douyinPBAppendVarint(buf, uint64(len(s)))
	return append(buf, s...)
}

// douyinPBAppendBytes 追加一个 bytes 字段（空值跳过）。
func douyinPBAppendBytes(buf []byte, num int, b []byte) []byte {
	if len(b) == 0 {
		return buf
	}
	buf = douyinPBAppendTag(buf, num, douyinPBWireBytes)
	buf = douyinPBAppendVarint(buf, uint64(len(b)))
	return append(buf, b...)
}

// douyinPBAppendUint 追加一个 varint 字段（0 跳过，与 proto3 默认值语义一致）。
func douyinPBAppendUint(buf []byte, num int, v uint64) []byte {
	if v == 0 {
		return buf
	}
	buf = douyinPBAppendTag(buf, num, douyinPBWireVarint)
	return douyinPBAppendVarint(buf, v)
}

// douyinPushFrame 是 WS 上的一帧（心跳、ACK、消息都用它）。
type douyinPushFrame struct {
	LogID       uint64
	PayloadType string
	Payload     []byte
}

// encode 把 douyinPushFrame 编码成 protobuf 字节。
func (f douyinPushFrame) encode() []byte {
	var buf []byte
	buf = douyinPBAppendUint(buf, 2, f.LogID)
	buf = douyinPBAppendString(buf, 7, f.PayloadType)
	buf = douyinPBAppendBytes(buf, 8, f.Payload)
	return buf
}

// douyinDecodePushFrame 解析一帧。
func douyinDecodePushFrame(data []byte) (douyinPushFrame, error) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return douyinPushFrame{}, err
	}
	return douyinPushFrame{
		LogID:       douyinPBVarint(fields, 2),
		PayloadType: douyinPBString(fields, 7),
		Payload:     douyinPBBytes(fields, 8),
	}, nil
}

// douyinResponseMsg 是 Response 里的一条业务消息。
type douyinResponseMsg struct {
	Method  string
	Payload []byte
}

// douyinResponse 是解压后的 Response。
type douyinResponse struct {
	Messages    []douyinResponseMsg
	InternalExt string
	NeedAck     bool
}

// douyinDecodeResponse 解析 Response。
func douyinDecodeResponse(data []byte) (douyinResponse, error) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return douyinResponse{}, err
	}

	out := douyinResponse{
		InternalExt: douyinPBString(fields, 5),
		NeedAck:     douyinPBBool(fields, 9),
	}
	for _, f := range fields {
		if f.Num != 1 || f.Wire != douyinPBWireBytes {
			continue
		}
		sub, err := douyinPBReadFields(f.Bytes)
		if err != nil {
			continue // 单条消息坏了不影响其它消息
		}
		out.Messages = append(out.Messages, douyinResponseMsg{
			Method:  douyinPBString(sub, 1),
			Payload: douyinPBBytes(sub, 2),
		})
	}
	return out, nil
}

// handleEmojiChat 处理纯图片弹幕（WebcastEmojiChatMessage）。
//
// 抖音把「只发一个表情、不打字」的弹幕单独走这个类型，正文在
// emoji_content（Text）的 pieces 里，是一张图而不是字符。不接的话这类
// 弹幕会整条消失——它在直播间里并不少见。
func (s *douyinDanmaku) handleEmojiChat(payload []byte) {
	emoji, err := douyinDecodeEmojiChat(payload)
	if err != nil || emoji.URL == "" {
		return
	}
	nickname := strings.TrimSpace(emoji.Nickname)
	if nickname == "" {
		nickname = "匿名"
	}
	at := time.Now()
	s.emit(Danmu{
		ID:       s.nextID(at),
		Nickname: nickname,
		// 正文留兜底文案（形如 [开心]）：有它的时候按内联表情渲染，
		// 没有就是「空正文 + 一张图」，UI 两条路都认。
		Content: emoji.Fallback,
		Level:   int(emoji.UserLevel),
		Medal:   emoji.Medal,
		Kind:    DanmuChat,
		At:      at,
		Emotes: []Emote{{
			Name:    emoji.Fallback,
			URL:     emoji.URL,
			Width:   emoji.Width,
			Height:  emoji.Height,
			Dynamic: emoji.Dynamic,
		}},
	})
}

// douyinEmojiChat 是 WebcastEmojiChatMessage 里关心的字段。
type douyinEmojiChat struct {
	Fallback  string
	URL       string
	Width     int
	Height    int
	Dynamic   bool
	Nickname  string
	UserLevel int64
	Medal     Medal
}

// douyinDecodeEmojiChat 解析 WebcastEmojiChatMessage 的 payload。
//
// 字段号取自抖音网页端自己的 schema（live-schema-im 里的 EmojiChatMessage）：
//
//	1 common / 2 user / 3 emoji_id / 4 emoji_content(Text) / 5 default_content
//	Text        : 4 pieces（重复）
//	TextPiece   : 25 image_value
//	TextPieceImage : 1 image
//	Image       : 1 url_list（重复）, 3 height, 4 width, 9 is_animated
func douyinDecodeEmojiChat(data []byte) (douyinEmojiChat, error) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return douyinEmojiChat{}, err
	}
	out := douyinEmojiChat{Fallback: douyinPBString(fields, 5)}
	out.Nickname, out.UserLevel, out.Medal = douyinUserInfo(fields)

	text, err := douyinPBReadFields(douyinPBBytes(fields, 4))
	if err != nil {
		return out, nil
	}
	for _, piece := range douyinPBAll(text, 4) {
		pieceFields, err := douyinPBReadFields(piece.Bytes)
		if err != nil {
			continue
		}
		imageValue, err := douyinPBReadFields(douyinPBBytes(pieceFields, 25))
		if err != nil {
			continue
		}
		image, err := douyinPBReadFields(douyinPBBytes(imageValue, 1))
		if err != nil {
			continue
		}
		urls := douyinPBAll(image, 1)
		if len(urls) == 0 {
			continue
		}
		out.URL = string(urls[0].Bytes)
		out.Height = int(douyinPBVarint(image, 3))
		out.Width = int(douyinPBVarint(image, 4))
		out.Dynamic = douyinPBVarint(image, 9) != 0
		return out, nil
	}
	return out, nil
}

// douyinRichParts 解析 RichText（rtf_content_v2）里的 pieces。
//
// 抖音网页端渲染聊天时优先读它，逐 piece 处理：string_value 是文字、
// user_value 是 @ 出来的昵称、image_value 是图。我们跟同一套顺序走，
// 图那一段写成 InlineEmote，图片按出现顺序返回。
//
// 字段号（live-schema-im，2026-11-17 核对）：
//
//	Text    : 4 pieces
//	TextPiece: 11 string_value / 21 user_value / 25 image_value
//	TextPieceUser : 1 user, 5 left_additional_content, 6 right_additional_content
//	TextPieceImage: 1 image
//	Image   : 1 url_list, 3 height, 4 width, 9 is_animated
func douyinRichParts(data []byte) (string, []Emote) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return "", nil
	}
	var b strings.Builder
	var emotes []Emote
	for _, piece := range douyinPBAll(fields, 4) {
		pieceFields, err := douyinPBReadFields(piece.Bytes)
		if err != nil {
			continue
		}
		b.WriteString(douyinPBString(pieceFields, 11))
		if user := douyinPBBytes(pieceFields, 21); len(user) > 0 {
			b.WriteString(douyinRichMention(user))
		}
		if image := douyinPBBytes(pieceFields, 25); len(image) > 0 {
			if e, ok := douyinRichImage(image); ok {
				b.WriteRune(InlineEmote)
				emotes = append(emotes, e)
			}
		}
	}
	return b.String(), emotes
}

// douyinRichMention 还原一个 @ 片段：左右修饰 + 昵称。
func douyinRichMention(data []byte) string {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(douyinPBString(fields, 5))
	if user, err := douyinPBReadFields(douyinPBBytes(fields, 1)); err == nil {
		b.WriteString(douyinPBString(user, 3))
	}
	b.WriteString(douyinPBString(fields, 6))
	return b.String()
}

// douyinRichImage 从 TextPieceImage 里取一张图。
func douyinRichImage(data []byte) (Emote, bool) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return Emote{}, false
	}
	image, err := douyinPBReadFields(douyinPBBytes(fields, 1))
	if err != nil {
		return Emote{}, false
	}
	urls := douyinPBAll(image, 1)
	if len(urls) == 0 {
		return Emote{}, false
	}
	return Emote{
		URL:     string(urls[0].Bytes),
		Width:   int(douyinPBVarint(image, 4)),
		Height:  int(douyinPBVarint(image, 3)),
		Dynamic: douyinPBVarint(image, 9) != 0,
	}, true
}

// douyinUserInfo 从一条消息的顶层 user（f2）里取昵称、等级和粉丝团。
func douyinUserInfo(fields []douyinPBField) (nickname string, level int64, medal Medal) {
	userBytes := douyinPBBytes(fields, 2)
	if len(userBytes) == 0 {
		return "", 0, Medal{}
	}
	userFields, err := douyinPBReadFields(userBytes)
	if err != nil {
		return "", 0, Medal{}
	}
	nickname = douyinPBString(userFields, 3)
	if pg := douyinPBBytes(userFields, 23); len(pg) > 0 {
		if pgFields, err := douyinPBReadFields(pg); err == nil {
			level = int64(douyinPBVarint(pgFields, 6))
			if level == 0 {
				level = int64(douyinPBVarint(pgFields, 3))
			}
		}
	}
	return nickname, level, douyinFansClub(userFields)
}

// douyinPBAll 返回某个字段号的全部出现（重复字段）。
func douyinPBAll(fields []douyinPBField, num int) []douyinPBField {
	var out []douyinPBField
	for _, f := range fields {
		if f.Num == num && f.Wire == douyinPBWireBytes {
			out = append(out, f)
		}
	}
	return out
}

// douyinChatMessage 是 WebcastChatMessage 里关心的字段。
type douyinChatMessage struct {
	Content string
	// Emotes 是富文本里按位置出现的图片（rtf_content_v2 的 image_value）。
	// Content 里对应位置是 live.InlineEmote。
	Emotes    []Emote
	Nickname  string
	UserLevel int64
	// Medal 是粉丝团勋章。抖音的粉丝团在消息里只有徽章和等级，
	// 没有名称——界面上就叫「粉丝团」。
	Medal Medal
}

// douyinDecodeChatMessage 解析 WebcastChatMessage 的 payload。
func douyinDecodeChatMessage(data []byte) (douyinChatMessage, error) {
	fields, err := douyinPBReadFields(data)
	if err != nil {
		return douyinChatMessage{}, err
	}

	out := douyinChatMessage{Content: douyinPBString(fields, 3)}

	// user = 2，昵称在 User.nickName=3，等级在 User.payGrade=23。
	// 真实样本（2026-11-17）里荣誉等级在 f6，和网页端
	// new_user_grade_level_vN 图标一致；老样本落在 f3，两个都认。
	out.Nickname, out.UserLevel, out.Medal = douyinUserInfo(fields)

	// 带图的消息正文要靠富文本拼：content 字段里根本没有图的位置。
	// 只在富文本里真有图时才改用它，其余照旧用 content，改动面最小。
	if text, emotes := douyinRichParts(douyinPBBytes(fields, 41)); len(emotes) > 0 {
		out.Content = text
		out.Emotes = emotes
	}
	return out, nil
}

// douyinFansClub 从 User 的重复勋章字段（f21）里找粉丝团。
//
// 勋章有两种：用户荣誉等级（new_user_grade_level_vN.png）和粉丝团
// （fansclub_level_vN.png）。两者都是 f21，靠图片名或描述区分。
// 等级在勋章明细 f8.f3；没有粉丝团时返回零值。
func douyinFansClub(userFields []douyinPBField) Medal {
	for _, f := range userFields {
		if f.Num != 21 || f.Wire != douyinPBWireBytes {
			continue
		}
		medalFields, err := douyinPBReadFields(f.Bytes)
		if err != nil {
			continue
		}
		icon := douyinPBString(medalFields, 1)
		detail := douyinPBBytes(medalFields, 8)
		if !strings.Contains(icon, "fansclub") {
			continue
		}
		if len(detail) == 0 {
			continue
		}
		detailFields, err := douyinPBReadFields(detail)
		if err != nil {
			continue
		}
		level := int(douyinPBVarint(detailFields, 3))
		if level <= 0 {
			continue
		}
		return Medal{Name: "粉丝团", Level: level}
	}
	return Medal{}
}

// 编译期确认抖音实现了弹幕接口。
var _ Danmakuer = (*douyinProvider)(nil)
