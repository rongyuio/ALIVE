package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"alive/internal/diag"
)

// 虎牙弹幕：一条 WebSocket 连接 + TARS(JCE) 二进制消息。
//
// 网关、心跳字节、注册包结构与参考实现保持一致；这里只负责会话生命周期、
// 断线退避和字段归一化。
//
// 订阅 UID 用的是「频道 ID」（liveData.channel / baseSteamInfoList[0].lChannelId），
// 不是主播 UID。这一点参考实现踩过坑：用 profileInfo.yyid 只能收到控制包，
// 聊天一条都收不到。
const (
	huyaDanmakuURL = "wss://cdnws.api.huya.com"

	huyaDanmuHeartbeat = 20 * time.Second
	// 超过这么久没收到任何帧就认为连接已死。
	huyaDanmuReadTO   = 90 * time.Second
	huyaDanmuDialTO   = 15 * time.Second
	huyaDanmuMaxFrame = 1 << 20

	// 断线退避：1s 起，最多 30s；连接稳住 10s 后下一次重新从 1s 开始。
	huyaDanmuMaxBack = 30 * time.Second
	huyaDanmuStable  = 10 * time.Second

	// wscmd 里的指令号：16 = 订阅主题。
	huyaDanmuSubscribeCmd = 16
	// 业务帧的外层 type 固定 7，其余是控制帧。
	huyaDanmuOuterType = 7
	// 内层 nested：1400 是聊天。
	huyaDanmuChat = 1400

	huyaDanmuDefaultColor = 0xFFFFFF
)

// huyaDanmuHeartbeatBytes 是虎牙网页端心跳包的原始字节（从它实际发出的数据里核对得到）。
//
// 包体是 TARS 编码的 onlineui.OnUserHeartBeat 请求，自己拼极易出错，原样保留。
var huyaDanmuHeartbeatBytes = []byte{
	0x00, 0x03, 0x1d, 0x00, 0x00, 0x69, 0x00, 0x00, 0x00, 0x69, 0x10, 0x03,
	0x2c, 0x3c, 0x4c, 0x56, 0x08, 0x6f, 0x6e, 0x6c, 0x69, 0x6e, 0x65, 0x75,
	0x69, 0x66, 0x0f, 0x4f, 0x6e, 0x55, 0x73, 0x65, 0x72, 0x48, 0x65, 0x61,
	0x72, 0x74, 0x42, 0x65, 0x61, 0x74, 0x7d, 0x00, 0x00, 0x3c, 0x08, 0x00,
	0x01, 0x06, 0x04, 0x74, 0x52, 0x65, 0x71, 0x1d, 0x00, 0x00, 0x2f, 0x0a,
	0x0a, 0x0c, 0x16, 0x00, 0x26, 0x00, 0x36, 0x07, 0x61, 0x64, 0x72, 0x5f,
	0x77, 0x61, 0x70, 0x46, 0x00, 0x0b, 0x12, 0x03, 0xae, 0xf0, 0x0f, 0x22,
	0x03, 0xae, 0xf0, 0x0f, 0x3c, 0x42, 0x6d, 0x52, 0x02, 0x60, 0x5c, 0x60,
	0x01, 0x7c, 0x82, 0x00, 0x0b, 0xb0, 0x1f, 0x9c, 0xac, 0x0b, 0x8c, 0x98,
	0x0c, 0xa8, 0x0c,
}

// huyaDanmaku 是一次房间监听会话。
type huyaDanmaku struct {
	provider  *huyaProvider
	roomID    string // 归一后的房间号，只用于日志与消息 id
	channelID int64  // 订阅用的频道 id，也是「上头条」留言板要的 lPid
	onMessage func(Danmu)

	// 上头条留言按 id 去重：轮询会反复拿到还在倒计时的同一条。
	seenHead  map[int64]struct{}
	headOrder []int64

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
// 订阅 UID 要先查房间档案，所以把这一步放在返回之前：房间号不合法、房间
// 不存在这类错误要让调用方立刻看到，而不是先拿到一个「成功」的流随后在
// 后台无限重试。
func (p *huyaProvider) Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error) {
	id, err := normalizeHuyaRoomID(roomID)
	if err != nil {
		return nil, err
	}
	p.ensureEmotes()
	if onMessage == nil {
		onMessage = func(Danmu) {}
	}

	channelID, err := p.huyaRoomIDs(ctx, id)
	if err != nil {
		return nil, err
	}

	s := &huyaDanmaku{
		provider:  p,
		roomID:    id,
		channelID: channelID,
		onMessage: onMessage,
		seenHead:  map[int64]struct{}{},
		done:      make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
	if channelID > 0 {
		go s.pollHeadlines()
	}
	return s, nil
}

// huyaRoomIDs 查一次房间档案，取出订阅用的频道 id。
//
// 一次连接只查一次：重连时频道不会变，每帧都去查只会让风控更快盯上。
//
// 这个 id 同时是「上头条」留言板的 lPid：网页端传的是 TT_PROFILE_INFO.lp，
// 也就是频道号，不是主播 uid（yyid）——传 yyid 服务端照样回 200，只是板子永远是空的。
func (p *huyaProvider) huyaRoomIDs(ctx context.Context, roomID string) (channelID int64, err error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return 0, err
	}
	target := fmt.Sprintf(huyaRoomProfileURL, url.QueryEscape(roomID))
	body, err := p.api.get(ctx, "弹幕频道", target, nil)
	release()
	if err != nil {
		return 0, p.wrap("弹幕频道", err)
	}

	var env huyaProfileEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		// 正常响应一定是 JSON；解不出来基本是被换成了验证页或空页
		return 0, p.wrap("弹幕频道", fmt.Errorf("%w: 房间档案不是 JSON: %v", ErrBlocked, err))
	}
	if env.Status != 200 {
		switch env.Status {
		case 403, 412, 429:
			return 0, p.wrap("弹幕频道", fmt.Errorf("%w: 房间档案 status=%d", ErrBlocked, env.Status))
		}
		msg := strings.TrimSpace(env.Message)
		if msg == "" {
			msg = fmt.Sprintf("status=%d", env.Status)
		}
		return 0, p.wrap("弹幕频道", &UpstreamError{Platform: Huya, Op: "弹幕频道", Message: msg})
	}

	// 档案字段同样会在数字 / 字符串 / 科学计数法之间漂移，走柔性取值。
	var root map[string]any
	_ = json.Unmarshal(env.Data, &root)
	liveData, _ := root["liveData"].(map[string]any)
	profile, _ := root["profileInfo"].(map[string]any)

	id := int64(huyaJSONFloat(liveData["channel"]))
	if stream, ok := root["stream"].(map[string]any); ok && id == 0 {
		if list, ok := stream["baseSteamInfoList"].([]any); ok && len(list) > 0 {
			if first, ok := list[0].(map[string]any); ok {
				id = int64(huyaJSONFloat(first["lChannelId"]))
			}
		}
	}
	if id == 0 {
		id = int64(huyaJSONFloat(profile["yyid"]))
	}
	if id == 0 {
		// 兜底与参考实现一致：没有频道字段时按房间号订阅，能收到控制帧。
		id, _ = strconv.ParseInt(roomID, 10, 64)
	}
	if id == 0 {
		return 0, &UpstreamError{
			Platform: Huya, Op: "弹幕频道",
			Message: fmt.Sprintf("房间 %s 没有可用的频道 id", roomID),
		}
	}
	return id, nil
}

// Close 停止会话。重复调用安全。
func (s *huyaDanmaku) Close() {
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
func (s *huyaDanmaku) run() {
	defer close(s.done)

	backoff := time.Second
	for {
		started := time.Now()
		err := s.connectOnce()
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			diag.L().Debug().Str("room", s.roomID).Dict(diag.ErrField, diag.Err(err)).Dur("retry", backoff).Msg("虎牙弹幕断开")
		}
		if time.Since(started) >= huyaDanmuStable {
			backoff = time.Second
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff < huyaDanmuMaxBack {
			backoff *= 2
			if backoff > huyaDanmuMaxBack {
				backoff = huyaDanmuMaxBack
			}
		}
	}
}

// connectOnce 建一次连接，发订阅包，然后读到断开。
func (s *huyaDanmaku) connectOnce() error {
	payload := s.huyaRegPayload()

	dialCtx, cancel := context.WithTimeout(s.ctx, huyaDanmuDialTO)
	conn, _, err := websocket.Dial(dialCtx, huyaDanmakuURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"User-Agent": {desktopUA},
			"Origin":     {"https://www.huya.com"},
			"Referer":    {"https://www.huya.com/"},
		},
	})
	cancel()
	if err != nil {
		return fmt.Errorf("连接弹幕网关: %w", err)
	}
	conn.SetReadLimit(huyaDanmuMaxFrame)

	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()

	runErr := s.readLoop(conn, payload)

	s.mu.Lock()
	if s.conn == conn {
		s.conn = nil
	}
	s.mu.Unlock()
	_ = conn.CloseNow()
	return runErr
}

// huyaRegPayload 组装注册包。
//
// 结构（参考实现的 get_ws_info_tars）：
//
//	oos   = [list<string>(tag0) 订阅主题, string(tag1) 空串]
//	wscmd = [int32(tag0)=16, bytes(tag1)=oos]
func (s *huyaDanmaku) huyaRegPayload() []byte {
	uid := strconv.FormatInt(s.channelID, 10)
	oos := newHuyaTarsWriter()
	oos.writeStringList(0, []string{"live:" + uid, "chat:" + uid})
	oos.writeString(1, "")

	wscmd := newHuyaTarsWriter()
	wscmd.writeInt32(0, huyaDanmuSubscribeCmd)
	wscmd.writeBytes(1, oos.bytes())
	return wscmd.bytes()
}

// readLoop 发订阅包、跑心跳、读帧。
func (s *huyaDanmaku) readLoop(conn *websocket.Conn, payload []byte) error {
	if err := conn.Write(s.ctx, websocket.MessageBinary, payload); err != nil {
		return fmt.Errorf("发送订阅包: %w", err)
	}

	hbCtx, hbCancel := context.WithCancel(s.ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(huyaDanmuHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := conn.Write(hbCtx, websocket.MessageBinary, huyaDanmuHeartbeatBytes); err != nil {
					return
				}
			}
		}
	}()

	for {
		if s.ctx.Err() != nil {
			return nil
		}
		readCtx, cancel := context.WithTimeout(s.ctx, huyaDanmuReadTO)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("读取帧: %w", err)
		}
		// 坏帧只丢这一帧：WS 本身有消息边界，丢一条不影响后续帧，
		// 重连反而会因为服务端持续下发同一类帧而空转。
		if err := s.handleMessage(data); err != nil {
			diag.L().Debug().Str("room", s.roomID).Dict(diag.ErrField, diag.Err(err)).Msg("虎牙弹幕帧解析失败")
		}
	}
}

// handleMessage 解析一帧并分发给对应的处理函数。
//
// 消息结构（参考实现的 decode_msg_tars）：
//
//	外层: int32(tag0)=7, bytes(tag1)
//	  内层: int32(tag1)=nested 消息类型, bytes(tag2)
//	    载荷: 各类型自己的 TARS 结构
//
// 返回错误表示这一帧的字节已经损坏（长度为负、越界等）；控制帧不是错误，
// 原样忽略并返回 nil。
func (s *huyaDanmaku) handleMessage(data []byte) error {
	outer := newHuyaTarsReader(data)
	// 先找外层 tag0：控制帧没有它，属于正常情况；跳过字段失败才是坏帧。
	head, err := outer.find(0)
	if err != nil {
		if errors.Is(err, errHuyaTarsTagNotFound) {
			return nil
		}
		return fmt.Errorf("tars: 外层帧损坏: %w", err)
	}
	if typ := outer.intFromHead(head, -1); typ != huyaDanmuOuterType {
		return nil
	}
	inner, err := outer.readBytesTag(1)
	if err != nil {
		return fmt.Errorf("tars: 业务帧载荷: %w", err)
	}

	r := newHuyaTarsReader(inner)
	nested := r.readInt32Tag(1, -1)
	payload, err := r.readBytesTag(2)
	if err != nil {
		return fmt.Errorf("tars: 业务帧内容: %w", err)
	}

	switch nested {
	case huyaDanmuChat:
		s.handleChat(payload)
		// 其余类型（实测在线的有 6501 / 6110 / 6211 / 6213 / 6480 / 100000 / 24102901）
		// 与参考实现一样忽略：这些帧没有可对照的字段布局，按猜测解只会往面板里塞错内容。
		// 6213 是贵族名单（只有 uid、头像、爵位字节，没有昵称），本身也拼不出「谁进场了」。
	}
	return nil
}

// handleChat 解析聊天弹幕。
//
// 载荷：struct(tag0)=HuyaUser, string(tag3)=内容, struct(tag6)=颜色。
func (s *huyaDanmaku) handleChat(payload []byte) {
	r := newHuyaTarsReader(payload)

	nickname := ""
	if err := r.readStructBegin(0); err == nil {
		// HuyaUser：tag0=uid、tag1=imid、tag2=昵称、tag3=性别。
		//
		// tag3 必须读掉（即使不用），否则结构体里的 tag3 会把后面正文的
		// tag3（聊天内容）抢走，正文永远解析为空。
		_ = r.readInt64Tag(0, -1)
		_ = r.readInt64Tag(1, -1)
		nickname = r.readStringTag(2, "")
		_ = r.readInt32Tag(3, -1)
	}
	text := r.readStringTag(3, "")

	color := int32(huyaDanmuDefaultColor)
	if err := r.readStructBegin(6); err == nil {
		color = r.readInt32Tag(0, huyaDanmuDefaultColor)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if strings.TrimSpace(nickname) == "" {
		nickname = "匿名"
	}

	at := time.Now()
	s.emit(Danmu{
		ID:       s.nextID(at),
		Nickname: nickname,
		Content:  text,
		// 消费等级（网页端弹幕最左边那颗徽章）也在装饰字段里，
		// 和粉丝牌是两条不同的 DecorationInfo。
		Level: huyaLevelFromPayload(payload),
		Medal: huyaMedalFromPayload(payload),
		Color: huyaColorHex(color),
		Kind:  DanmuChat,
		At:    at,
	})
}

// huyaLevelFromPayload 从装饰字段里取消费等级（用户等级）。
//
// 实测 vData 结构：tag0=uid（int32/int64）、tag1=等级（int8）、tag2=0、
// tag3=1。粉丝牌那段 vData 没有 tag1，直接是 tag3=牌子名，所以两者
// 用「有没有 tag1 数字」区分得开。
func huyaLevelFromPayload(payload []byte) int {
	r := newHuyaTarsReader(payload)
	head, err := r.find(8)
	if err != nil || head.typ != huyaTarsList {
		return 0
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 20 {
		return 0
	}
	for i := 0; i < count; i++ {
		eh, err := r.takeHead()
		if err != nil || eh.typ != huyaTarsStructBegin {
			return 0
		}
		var data []byte
		for {
			h, err := r.takeHead()
			if err != nil || h.typ == huyaTarsStructEnd {
				break
			}
			if h.typ == huyaTarsSimpleList && h.tag == 2 {
				if _, err := r.takeHead(); err != nil {
					return 0
				}
				n := int(r.readInt32(0, 0))
				if n < 0 || r.pos+n > len(r.buf) {
					return 0
				}
				data = r.buf[r.pos : r.pos+n]
				r.pos += n
				continue
			}
			if err := r.skipField(h.typ); err != nil {
				return 0
			}
		}
		if lv, ok := huyaParseLevelData(data); ok {
			return lv
		}
	}
	return 0
}

// huyaParseLevelData 解一段消费等级 vData。
func huyaParseLevelData(data []byte) (int, bool) {
	if len(data) < 4 {
		return 0, false
	}
	r := newHuyaTarsReader(data)
	h, err := r.takeHead()
	if err != nil || h.tag != 0 {
		return 0, false
	}
	switch h.typ {
	case huyaTarsInt64:
		if r.pos+8 > len(r.buf) {
			return 0, false
		}
		r.pos += 8
	case huyaTarsInt32:
		if r.pos+4 > len(r.buf) {
			return 0, false
		}
		r.pos += 4
	case huyaTarsZero:
		// uid 为 0，继续
	default:
		return 0, false
	}
	h2, err := r.takeHead()
	if err != nil || h2.tag != 1 {
		return 0, false
	}
	level := r.intFromHead(h2, 0)
	if level <= 0 || level > 100 {
		return 0, false
	}
	return int(level), true
}

// huyaMedalFromPayload 从装饰字段（tag8 vDecorationPrefix）里取粉丝牌。
//
// 粉丝牌不是独立字段，而是混在一串 DecorationInfo 里：每一项带一个
// appId 和一段厂商自定义的二进制 vData。实测粉丝牌那一段是嵌套 Tars——
// tag1=uid、tag3=牌子名称、tag4=等级；气泡、贵族之类的 vData 长得不一样，
// 靠「名称非空、不是 URL，等级在合理范围」把它们挡掉。
func huyaMedalFromPayload(payload []byte) Medal {
	r := newHuyaTarsReader(payload)
	head, err := r.find(8)
	if err != nil || head.typ != huyaTarsList {
		return Medal{}
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 20 {
		return Medal{}
	}
	for i := 0; i < count; i++ {
		eh, err := r.takeHead()
		if err != nil || eh.typ != huyaTarsStructBegin {
			return Medal{}
		}
		var data []byte
		for {
			h, err := r.takeHead()
			if err != nil || h.typ == huyaTarsStructEnd {
				break
			}
			if h.typ == huyaTarsSimpleList && h.tag == 2 {
				if _, err := r.takeHead(); err != nil {
					return Medal{}
				}
				n := int(r.readInt32(0, 0))
				if n < 0 || r.pos+n > len(r.buf) {
					return Medal{}
				}
				data = r.buf[r.pos : r.pos+n]
				r.pos += n
				continue
			}
			if err := r.skipField(h.typ); err != nil {
				return Medal{}
			}
		}
		if m, ok := huyaParseMedalData(data); ok {
			return m
		}
	}
	return Medal{}
}

// huyaParseMedalData 解一段粉丝牌 vData。
func huyaParseMedalData(data []byte) (Medal, bool) {
	if len(data) < 6 {
		return Medal{}, false
	}
	r := newHuyaTarsReader(data)
	name := r.readStringTag(3, "")
	level := int(r.readInt32Tag(4, 0))
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "http") || level <= 0 || level > 100 {
		return Medal{}, false
	}
	return Medal{Name: name, Level: level}, true
}

// ensureEmotes 后台拉一次表情包。
//
// 弹幕里的文字表情只有占位符，图片在 Wup 接口里；连上弹幕就顺手拉一次，
// 拉到之前弹幕照常显示，只是 [大哭] 还是文字。
func (p *huyaProvider) ensureEmotes() {
	if _, ok := runtimeEmotes.Load(Huya); ok {
		return
	}
	now := time.Now().Unix()
	last := p.emoteTried.Load()
	if now-last < 60 || !p.emoteTried.CompareAndSwap(last, now) {
		return
	}
	client := p.api.http
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		list := huyaFetchExpressionEmotes(ctx, client, 0)
		if len(list) == 0 {
			return
		}
		table := make(emoteTable, len(list))
		for _, e := range list {
			table[e.Name] = e.URL
		}
		setRuntimeEmotes(Huya, table)
	}()
}

// emit 归一化后回调。时间、id、kind 缺省值都在这里补齐。
func (s *huyaDanmaku) emit(msg Danmu) {
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
func (s *huyaDanmaku) nextID(at time.Time) string {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return fmt.Sprintf("huya:%s:%d:%d", s.roomID, at.UnixMilli(), seq)
}

// huyaColorHex 把平台给的十进制颜色转成 #RRGGBB。
//
// 上游对「默认白色」也回 0 或 16777215，两种都归到 #FFFFFF：
// 虎牙弹幕就是白色，交回 UI 按昵称取色会跟基准对不上。
func huyaColorHex(color int32) string {
	if color <= 0 {
		return "#FFFFFF"
	}
	if color > 0xFFFFFF {
		color &= 0xFFFFFF
	}
	return fmt.Sprintf("#%06X", color)
}

// ---------------------------------------------------------------- TARS 编解码
//
// 取流（WUP 请求/响应）和弹幕（订阅包、解帧）共用同一套 TARS(JCE) 子集，
// 放在这个文件里只因为弹幕先用到它；两个文件同包，不需要再开第三个文件。
//
// 实现严格对齐参考实现核对过的 tars-stream crate：
//
//	类型编号: 0 int8, 1 int16, 2 int32, 3 int64, 4 float, 5 double,
//	          6 string1(1字节长度), 7 string4(4字节长度), 8 map, 9 list,
//	          10 struct 开始, 11 struct 结束, 12 zero, 13 字节数组
//	头部:     tag<15 → (tag<<4)|type 一个字节；否则 (0xF0|type) + tag 字节
//	整数压缩: 能塞进更小的类型就用更小的（0 直接写 EnZero，不带数据）
//	字节数组: head(EnSimplelist) + head(0, EnInt8) + int32(0, 长度) + 数据
//
// 注意：这套编号与常见文档里的 JCE 编号（10=BYTE_ARRAY）不同，
// 以实际 crate 为准，否则服务器不认。

const (
	huyaTarsInt8 = iota
	huyaTarsInt16
	huyaTarsInt32
	huyaTarsInt64
	huyaTarsFloat
	huyaTarsDouble
	huyaTarsString1
	huyaTarsString4
	huyaTarsMap
	huyaTarsList
	huyaTarsStructBegin
	huyaTarsStructEnd
	huyaTarsZero
	huyaTarsSimpleList
)

type huyaTarsWriter struct {
	buf []byte
}

func newHuyaTarsWriter() *huyaTarsWriter { return &huyaTarsWriter{} }

func (w *huyaTarsWriter) bytes() []byte { return w.buf }

func (w *huyaTarsWriter) putHead(tag uint8, typ uint8) {
	if tag < 15 {
		w.buf = append(w.buf, byte(tag<<4)|typ)
		return
	}
	w.buf = append(w.buf, 0xF0|typ, tag)
}

func (w *huyaTarsWriter) writeInt8(tag uint8, v int8) {
	if v == 0 {
		w.putHead(tag, huyaTarsZero)
		return
	}
	w.putHead(tag, huyaTarsInt8)
	w.buf = append(w.buf, byte(v))
}

func (w *huyaTarsWriter) writeInt16(tag uint8, v int16) {
	if v >= -128 && v <= 127 {
		w.writeInt8(tag, int8(v))
		return
	}
	w.putHead(tag, huyaTarsInt16)
	w.buf = append(w.buf, byte(v>>8), byte(v))
}

func (w *huyaTarsWriter) writeInt32(tag uint8, v int32) {
	if v >= -32768 && v <= 32767 {
		w.writeInt16(tag, int16(v))
		return
	}
	w.putHead(tag, huyaTarsInt32)
	w.buf = append(w.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *huyaTarsWriter) writeInt64(tag uint8, v int64) {
	if v >= -2147483648 && v <= 2147483647 {
		w.writeInt32(tag, int32(v))
		return
	}
	w.putHead(tag, huyaTarsInt64)
	w.buf = append(w.buf,
		byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *huyaTarsWriter) writeString(tag uint8, s string) {
	if len(s) <= 255 {
		w.putHead(tag, huyaTarsString1)
		w.buf = append(w.buf, byte(len(s)))
		w.buf = append(w.buf, s...)
		return
	}
	w.putHead(tag, huyaTarsString4)
	w.buf = append(w.buf, byte(len(s)>>24), byte(len(s)>>16), byte(len(s)>>8), byte(len(s)))
	w.buf = append(w.buf, s...)
}

func (w *huyaTarsWriter) writeBytes(tag uint8, data []byte) {
	w.putHead(tag, huyaTarsSimpleList)
	w.putHead(0, huyaTarsInt8)
	w.writeInt32(0, int32(len(data)))
	w.buf = append(w.buf, data...)
}

func (w *huyaTarsWriter) writeStringList(tag uint8, list []string) {
	w.putHead(tag, huyaTarsList)
	w.writeInt32(0, int32(len(list)))
	for _, item := range list {
		w.writeString(0, item)
	}
}

func (w *huyaTarsWriter) writeEmptyMap(tag uint8) {
	w.putHead(tag, huyaTarsMap)
	w.writeInt32(0, 0)
}

func (w *huyaTarsWriter) writeBytesMap(tag uint8, key string, value []byte) {
	w.putHead(tag, huyaTarsMap)
	w.writeInt32(0, 1)
	w.writeString(0, key)
	w.writeBytes(1, value)
}

func (w *huyaTarsWriter) writeStructBegin(tag uint8) { w.putHead(tag, huyaTarsStructBegin) }
func (w *huyaTarsWriter) writeStructEnd()            { w.putHead(0, huyaTarsStructEnd) }

type huyaTarsReader struct {
	buf []byte
	pos int
}

func newHuyaTarsReader(buf []byte) *huyaTarsReader { return &huyaTarsReader{buf: buf} }

type huyaTarsHead struct {
	tag uint8
	typ uint8
}

func (r *huyaTarsReader) takeHead() (huyaTarsHead, error) {
	// pos 可能被非法长度推成负数；只查上界的话 r.buf[r.pos] 直接越界 panic。
	if r.pos < 0 || r.pos >= len(r.buf) {
		return huyaTarsHead{}, fmt.Errorf("tars: 数据不足")
	}
	b := r.buf[r.pos]
	r.pos++
	tag := (b & 0xF0) >> 4
	typ := b & 0x0F
	if tag == 15 {
		if r.pos >= len(r.buf) {
			return huyaTarsHead{}, fmt.Errorf("tars: 扩展 tag 数据不足")
		}
		tag = r.buf[r.pos]
		r.pos++
	}
	return huyaTarsHead{tag: tag, typ: typ}, nil
}

// errHuyaTarsTagNotFound 区分「字段不存在」和「数据损坏」：前者是可选字段
// 缺失，调用方按默认值处理即可；后者必须当成坏帧报出去。
var errHuyaTarsTagNotFound = errors.New("tars: 找不到 tag")

// find 从当前位置向后找 tag 匹配的字段，途中跳过其它字段。
// 找不到时回退到起点（对齐 crate 的 TarsTagNotFoundErr 行为）；
// 跳过字段失败说明数据已损坏，如实返回错误，不伪装成「字段不存在」。
func (r *huyaTarsReader) find(tag uint8) (huyaTarsHead, error) {
	start := r.pos
	for r.pos < len(r.buf) {
		head, err := r.takeHead()
		if err != nil {
			r.pos = start
			return huyaTarsHead{}, fmt.Errorf("tars: 找 tag=%d 时头部损坏: %w", tag, err)
		}
		if head.tag == tag && head.typ != huyaTarsStructEnd {
			return head, nil
		}
		if err := r.skipField(head.typ); err != nil {
			r.pos = start
			return huyaTarsHead{}, fmt.Errorf("tars: 找 tag=%d 时跳过 tag=%d 失败: %w", tag, head.tag, err)
		}
	}
	r.pos = start
	return huyaTarsHead{}, fmt.Errorf("%w=%d", errHuyaTarsTagNotFound, tag)
}

func (r *huyaTarsReader) skipField(typ uint8) error {
	advance := func(n int) error {
		// 负数长度会把 pos 推到缓冲区之前：find 的 r.pos < len 仍然成立，
		// 下一轮 takeHead 就越界 panic。长度来自不可信报文，必须挡。
		if n < 0 || r.pos+n > len(r.buf) {
			return fmt.Errorf("tars: 跳过失败")
		}
		r.pos += n
		return nil
	}

	switch typ {
	case huyaTarsInt8:
		return advance(1)
	case huyaTarsInt16:
		return advance(2)
	case huyaTarsInt32, huyaTarsFloat:
		return advance(4)
	case huyaTarsInt64, huyaTarsDouble:
		return advance(8)
	case huyaTarsZero, huyaTarsStructEnd:
		return nil
	case huyaTarsString1:
		if r.pos >= len(r.buf) {
			return fmt.Errorf("tars: 数据不足")
		}
		size := int(r.buf[r.pos])
		r.pos++
		return advance(size)
	case huyaTarsString4:
		if r.pos+4 > len(r.buf) {
			return fmt.Errorf("tars: 数据不足")
		}
		size := int(r.buf[r.pos])<<24 | int(r.buf[r.pos+1])<<16 |
			int(r.buf[r.pos+2])<<8 | int(r.buf[r.pos+3])
		r.pos += 4
		return advance(size)
	case huyaTarsSimpleList:
		// 元素类型头读完忽略，长度是紧随其后的整数。
		// 不能复用「上一次整数读取」的结果：readInt32 遇到 tag 不匹配会回退
		// 且不更新那个值，复用会把旧值（可能是负数）当长度，把 pos 推飞。
		if _, err := r.takeHead(); err != nil {
			return err
		}
		size := r.readInt32(0, -1)
		if size < 0 {
			return fmt.Errorf("tars: 字节数组长度异常 %d", size)
		}
		return advance(int(size))
	case huyaTarsMap:
		count := int(r.readInt32(0, 0))
		for i := 0; i < count*2; i++ {
			head, err := r.takeHead()
			if err != nil {
				return err
			}
			if err := r.skipField(head.typ); err != nil {
				return err
			}
		}
		return nil
	case huyaTarsList:
		count := int(r.readInt32(0, 0))
		for i := 0; i < count; i++ {
			head, err := r.takeHead()
			if err != nil {
				return err
			}
			if err := r.skipField(head.typ); err != nil {
				return err
			}
		}
		return nil
	case huyaTarsStructBegin:
		for r.pos < len(r.buf) {
			head, err := r.takeHead()
			if err != nil {
				return err
			}
			if head.typ == huyaTarsStructEnd {
				return nil
			}
			if err := r.skipField(head.typ); err != nil {
				return err
			}
		}
		return fmt.Errorf("tars: 结构体未结束")
	}
	return fmt.Errorf("tars: 未知类型 %d", typ)
}

// readInt32Tag 按 tag 搜索读取 int32。
func (r *huyaTarsReader) readInt32Tag(tag uint8, def int32) int32 {
	head, err := r.find(tag)
	if err != nil {
		return def
	}
	return r.intFromHead(head, def)
}

// readInt32 顺序读取（不做 tag 搜索），用于 map/list 的计数。
func (r *huyaTarsReader) readInt32(tag uint8, def int32) int32 {
	head, err := r.takeHead()
	if err != nil {
		return def
	}
	if head.tag != tag {
		// 计数位置出现了别的 tag：回退并当作缺失
		r.pos--
		return def
	}
	return r.intFromHead(head, def)
}

// intFromHead 按类型解出整数值（含各种压缩形式）。
func (r *huyaTarsReader) intFromHead(head huyaTarsHead, def int32) int32 {
	var v int64
	switch head.typ {
	case huyaTarsZero:
		return 0
	case huyaTarsInt8:
		if r.pos+1 > len(r.buf) {
			return def
		}
		v = int64(int8(r.buf[r.pos]))
		r.pos++
	case huyaTarsInt16:
		if r.pos+2 > len(r.buf) {
			return def
		}
		v = int64(int16(uint16(r.buf[r.pos])<<8 | uint16(r.buf[r.pos+1])))
		r.pos += 2
	case huyaTarsInt32:
		if r.pos+4 > len(r.buf) {
			return def
		}
		v = int64(int32(uint32(r.buf[r.pos])<<24 | uint32(r.buf[r.pos+1])<<16 |
			uint32(r.buf[r.pos+2])<<8 | uint32(r.buf[r.pos+3])))
		r.pos += 4
	case huyaTarsInt64:
		if r.pos+8 > len(r.buf) {
			return def
		}
		v = int64(uint64(r.buf[r.pos])<<56 | uint64(r.buf[r.pos+1])<<48 |
			uint64(r.buf[r.pos+2])<<40 | uint64(r.buf[r.pos+3])<<32 |
			uint64(r.buf[r.pos+4])<<24 | uint64(r.buf[r.pos+5])<<16 |
			uint64(r.buf[r.pos+6])<<8 | uint64(r.buf[r.pos+7]))
		r.pos += 8
	default:
		return def
	}
	return int32(v)
}

func (r *huyaTarsReader) readInt64Tag(tag uint8, def int64) int64 {
	head, err := r.find(tag)
	if err != nil {
		return def
	}
	// 走 int64FromHead：虎牙的 uid、留言 id 都超过 int32，按 int32 读会溢出成负数。
	return r.int64FromHead(head, def)
}

func (r *huyaTarsReader) readStringTag(tag uint8, def string) string {
	head, err := r.find(tag)
	if err != nil {
		return def
	}
	switch head.typ {
	case huyaTarsZero:
		return def
	case huyaTarsString1:
		if r.pos+1 > len(r.buf) {
			return def
		}
		size := int(r.buf[r.pos])
		r.pos++
		if r.pos+size > len(r.buf) {
			return def
		}
		s := string(r.buf[r.pos : r.pos+size])
		r.pos += size
		return s
	case huyaTarsString4:
		if r.pos+4 > len(r.buf) {
			return def
		}
		size := int(r.buf[r.pos])<<24 | int(r.buf[r.pos+1])<<16 |
			int(r.buf[r.pos+2])<<8 | int(r.buf[r.pos+3])
		r.pos += 4
		if r.pos+size > len(r.buf) {
			return def
		}
		s := string(r.buf[r.pos : r.pos+size])
		r.pos += size
		return s
	}
	return def
}

func (r *huyaTarsReader) readBytesTag(tag uint8) ([]byte, error) {
	head, err := r.find(tag)
	if err != nil {
		return nil, err
	}
	if head.typ != huyaTarsSimpleList {
		return nil, fmt.Errorf("tars: tag=%d 不是字节数组（type=%d）", tag, head.typ)
	}
	// 元素类型头（crate 里固定 EnInt8），读完忽略
	if _, err := r.takeHead(); err != nil {
		return nil, err
	}
	size := int(r.readInt32(0, 0))
	if size < 0 || r.pos+size > len(r.buf) {
		return nil, fmt.Errorf("tars: 字节数组长度异常 %d", size)
	}
	out := r.buf[r.pos : r.pos+size]
	r.pos += size
	return out, nil
}

// readStructBegin 定位结构体起点（tag 匹配且类型为 struct 开始）。
func (r *huyaTarsReader) readStructBegin(tag uint8) error {
	head, err := r.find(tag)
	if err != nil {
		return err
	}
	if head.typ != huyaTarsStructBegin {
		return fmt.Errorf("tars: tag=%d 不是结构体（type=%d）", tag, head.typ)
	}
	return nil
}

// 编译期确认虎牙实现了弹幕接口。
var _ Danmakuer = (*huyaProvider)(nil)
