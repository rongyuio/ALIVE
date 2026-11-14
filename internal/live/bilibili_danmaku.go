package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	blivedmmsg "github.com/Akegarasu/blivedm-go/message"
	blivedmpacket "github.com/Akegarasu/blivedm-go/packet"
	"github.com/coder/websocket"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// B 站弹幕：一条 WebSocket 连接 + 二进制协议。
//
// 帧格式、zlib/brotli 解包、DANMU_MSG 的两种编码（包括 dm_v2 的 protobuf
// 分支）全部交给 blivedm-go 的 packet / message 包，这里只负责会话生命周期、
// 断线重连和字段归一化。
const (
	biliDanmuHeartbeat = 30 * time.Second
	// 超过这么久没收到任何帧就认为连接已死。
	biliDanmuReadTO   = 90 * time.Second
	biliDanmuMaxFrame = 8 << 20
	biliDanmuDialTO   = 12 * time.Second
	// 断线退避：1s 起，最多 30s；连接稳住 10s 后下一次重新从 1s 开始。
	biliDanmuMaxBack = 30 * time.Second
	biliDanmuStable  = 10 * time.Second
	// protover=3 表示接受 brotli 压缩帧，与官方前端一致。
	biliDanmuProto  = 3
	biliDanmuOrigin = "https://live.bilibili.com"
	biliDanmuRefer  = "https://live.bilibili.com/"
)

// biliMaxHosts 最多尝试几个弹幕节点：host_list 通常有 4–6 个，
// 全试一遍会让「连接失败」拖到十几秒。
const biliMaxHosts = 4

// biliDanmaku 是一次房间监听会话。
type biliDanmaku struct {
	provider  *bilibiliProvider
	roomID    string // 上游传入的房间号（可能是短号）
	realID    int64  // 真实 room_id，弹幕接口要这个
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
// 先把真实房间号解出来再返回：房间号不合法这类错误要让调用方立刻看到，
// 而不是先拿到一个「成功」的流、随后在后台无限重试。
func (p *bilibiliProvider) Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error) {
	roomID = strings.TrimSpace(roomID)
	id, err := strconv.ParseInt(roomID, 10, 64)
	if err != nil || id <= 0 {
		return nil, &UpstreamError{
			Platform: Bilibili, Op: "弹幕",
			Message: fmt.Sprintf("房间号 %q 不合法", roomID),
		}
	}
	if onMessage == nil {
		onMessage = func(Danmu) {}
	}

	realID, err := p.biliRealRoomID(ctx, id)
	if err != nil {
		return nil, err
	}

	s := &biliDanmaku{
		provider:  p,
		roomID:    roomID,
		realID:    realID,
		onMessage: onMessage,
		done:      make(chan struct{}),
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
	return s, nil
}

// biliRealRoomID 用 get_info 把短号换成真实 room_id。
func (p *bilibiliProvider) biliRealRoomID(ctx context.Context, id int64) (int64, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return 0, err
	}
	info, err := p.lc.GetLiveRoomInfo(ctx, bilibiliLive.GetLiveRoomInfoParam{RoomID: int(id)})
	release()
	if err != nil {
		return 0, p.wrap("弹幕房间号", err)
	}
	if info.RoomID > 0 {
		return int64(info.RoomID), nil
	}
	return id, nil
}

// Close 停止会话。重复调用安全。
func (s *biliDanmaku) Close() {
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
func (s *biliDanmaku) run() {
	defer close(s.done)

	backoff := time.Second
	for {
		started := time.Now()
		err := s.connectOnce()
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("B 站弹幕断开", "room", s.roomID, "error", err, "retry", backoff)
		}
		if time.Since(started) >= biliDanmuStable {
			backoff = time.Second
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff < biliDanmuMaxBack {
			backoff *= 2
			if backoff > biliDanmuMaxBack {
				backoff = biliDanmuMaxBack
			}
		}
	}
}

// connectOnce 取一次 token 与节点列表，连上并读到断开。
func (s *biliDanmaku) connectOnce() error {
	token, hosts, err := s.provider.biliDanmuInfo(s.ctx, s.realID)
	if err != nil {
		return err
	}
	if token == "" || len(hosts) == 0 {
		return errors.New("没有可用的弹幕服务器")
	}
	if len(hosts) > biliMaxHosts {
		hosts = hosts[:biliMaxHosts]
	}

	var lastErr error
	for _, host := range hosts {
		if s.ctx.Err() != nil {
			return nil
		}
		url := "wss://" + host + "/sub"

		dialCtx, cancel := context.WithTimeout(s.ctx, biliDanmuDialTO)
		conn, _, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
			HTTPHeader: http.Header{
				"User-Agent": {desktopUA},
				"Origin":     {biliDanmuOrigin},
				"Referer":    {biliDanmuRefer},
			},
		})
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		conn.SetReadLimit(biliDanmuMaxFrame)

		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()

		runErr := s.readLoop(conn, token)

		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		_ = conn.CloseNow()

		if runErr == nil {
			return nil
		}
		lastErr = runErr
	}
	if lastErr == nil {
		lastErr = errors.New("所有弹幕服务器均连接失败")
	}
	return lastErr
}

// biliDanmuInfo 取弹幕服务器的 token 与域名列表。
func (p *bilibiliProvider) biliDanmuInfo(ctx context.Context, realID int64) (string, []string, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return "", nil, err
	}
	info, err := p.lc.GetDanmuInfo(ctx, bilibiliLive.GetDanmuInfoParam{
		ID: realID, Type: 0, WebLocation: "444.8",
	})
	release()
	if err != nil {
		return "", nil, p.wrap("弹幕服务器", err)
	}

	// 同一个 host 会在不同端口出现多次，去重保序。
	seen := make(map[string]bool, len(info.HostList))
	hosts := make([]string, 0, len(info.HostList))
	for _, h := range info.HostList {
		host := strings.TrimSpace(h.Host)
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return info.Token, hosts, nil
}

// readLoop 发 auth、跑心跳、读帧。
func (s *biliDanmaku) readLoop(conn *websocket.Conn, token string) error {
	// uid 是 JSON 数字位，可能超过 int64 精确范围；匿名直接用 0。
	auth := fmt.Sprintf(`{"uid":0,"roomid":%d,"protover":%d,"platform":"web","type":2,"key":%q}`,
		s.realID, biliDanmuProto, token)
	authPkt := blivedmpacket.NewPlainPacket(blivedmpacket.RoomEnter, []byte(auth))
	if err := conn.Write(s.ctx, websocket.MessageBinary, authPkt.Build()); err != nil {
		return fmt.Errorf("发送 auth: %w", err)
	}

	hbCtx, hbCancel := context.WithCancel(s.ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(biliDanmuHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				// blivedm 的心跳包就是固定明文 "{}"。
				if err := conn.Write(hbCtx, websocket.MessageBinary, blivedmpacket.NewHeartBeatPacket()); err != nil {
					return
				}
			}
		}
	}()

	for {
		if s.ctx.Err() != nil {
			return nil
		}
		readCtx, cancel := context.WithTimeout(s.ctx, biliDanmuReadTO)
		_, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("读取帧: %w", err)
		}
		s.handleFrame(data)
	}
}

// handleFrame 解析一帧（可能含多个包，压缩包会被库递归解开）。
func (s *biliDanmaku) handleFrame(data []byte) {
	for _, p := range blivedmpacket.Slice(data) {
		for _, inner := range p.Parse() {
			if inner.Operation == uint32(blivedmpacket.Notification) {
				s.handleCommand(inner.Body)
			}
		}
	}
}

// handleCommand 处理一条业务消息（JSON 形如 {"cmd":"DANMU_MSG",...}）。
func (s *biliDanmaku) handleCommand(body []byte) {
	var env struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Cmd == "" {
		return
	}
	// 新的 cmd 会带参数，例如 "DANMU_MSG:4:0:2:2:2:0"。
	cmd := env.Cmd
	if i := strings.IndexByte(cmd, ':'); i >= 0 {
		cmd = cmd[:i]
	}

	switch cmd {
	case "DANMU_MSG":
		var d blivedmmsg.Danmaku
		d.Parse(body)

		nickname := "匿名"
		level := 0
		if d.Sender != nil {
			if name := strings.TrimSpace(d.Sender.Uname); name != "" {
				nickname = name
			}
			if d.Sender.Medal != nil {
				level = d.Sender.Medal.Level
			}
		}
		content := strings.TrimSpace(d.Content)
		emotes := biliEmotes(body, d)
		// 纯图片弹幕的正文是空串，但表情本身就是要显示的内容。
		if content == "" && len(emotes) == 0 {
			return
		}
		at := time.Now()
		if d.Timestamp > 0 {
			at = time.UnixMilli(d.Timestamp)
		}
		s.emit(Danmu{
			ID:       s.nextID(at),
			Nickname: nickname,
			Content:  content,
			Level:    level,
			Color:    biliColor(d),
			Kind:     DanmuChat,
			At:       at,
			Emotes:   emotes,
		})

	case "SUPER_CHAT_MESSAGE":
		// 醒目留言：用户花钱让一句话停在直播间顶部。
		// 它是「一个人在说话」，只是付了费，所以算弹幕行为，保留。
		// 礼物刷屏、进场提醒那类不是——那些不进这里。
		var sc blivedmmsg.SuperChat
		sc.Parse(body)
		text := strings.TrimSpace(sc.Message)
		if text == "" {
			return
		}
		at := time.Now()
		s.emit(Danmu{
			ID:         s.nextID(at),
			Nickname:   firstNonEmpty(strings.TrimSpace(sc.UserInfo.Uname), "匿名"),
			Content:    text,
			Level:      sc.UserInfo.UserLevel,
			Kind:       DanmuSuperChat,
			At:         at,
			Price:      sc.Price,
			Background: biliHexColor(sc.BackgroundColor),
		})
	}
}

// emit 归一化后回调。ID、时间、kind 缺省值都在这里补齐。
func (s *biliDanmaku) emit(msg Danmu) {
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
func (s *biliDanmaku) nextID(at time.Time) string {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return fmt.Sprintf("bilibili:%d:%d:%d", s.realID, at.UnixMilli(), seq)
}

// biliColor 把平台给的颜色整数转成 #RRGGBB；0（白色）不返回，交给 UI 按昵称取色。
func biliColor(d blivedmmsg.Danmaku) string {
	if d.Extra == nil {
		return ""
	}
	v := int64(d.Extra.Color)
	if v <= 0 || v > 0xFFFFFF {
		return ""
	}
	return fmt.Sprintf("#%06x", v)
}

// biliEmotes 读取弹幕携带的表情图。
//
// blivedm-go 只解析了 extra.emoticon（整条图弹幕）这一种；
// info[0][15].extra.emots 那种内联表情它注释掉了，这里先只取前者——
// 内联表情的后缀补丁等实测到样本再加，避免按猜测写解析。
// biliEmoteJSON 是 B 站弹幕里表情的统一形状，两种来源共用：
//   - info[0][13]：整条弹幕就是一张表情（正文为空，blivedm 已解析）；
//   - info[0][15].extra.emots：正文里的 [dog] 这类内联占位符，键即占位符。
type biliEmoteJSON struct {
	URL     string `json:"url"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Dynamic int    `json:"is_dynamic"`
}

// biliEmotes 取一条弹幕携带的全部表情图。
//
// blivedm-go 只解了 info[0][13]（整条图弹幕），把 extra.emots 那个字段
// 注释掉了。内联表情在正文里以 [dog] 这种占位符出现，不解析的话
// 面板里就只剩一串方括号。
func biliEmotes(body []byte, d blivedmmsg.Danmaku) []Emote {
	out := make([]Emote, 0, 2)
	seen := make(map[string]bool, 2)

	push := func(name, rawURL string, width, height, dynamic int) {
		url := biliHTTPS(rawURL)
		if url == "" {
			return
		}
		key := name + "\x00" + url
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Emote{
			Name:    name,
			URL:     url,
			Width:   width,
			Height:  height,
			Dynamic: dynamic != 0,
		})
	}

	if e := d.Emoticon; e != nil {
		push("", e.Url, e.Width, e.Height, e.IsDynamic)
	}
	for _, item := range biliInlineEmotes(body) {
		push(item.name, item.emote.URL, item.emote.Width, item.emote.Height, item.emote.Dynamic)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// biliInlineEmote 是一条内联表情：占位符 + 图片信息。
type biliInlineEmote struct {
	name  string
	emote biliEmoteJSON
}

// biliInlineEmotes 读取 info[0][15].extra.emots。
//
// 结构是「字符串里再套一层 JSON」，所以逐层按 raw 解，任一层不符预期
// 就返回空——表情解析失败不该影响正文。
func biliInlineEmotes(body []byte) []biliInlineEmote {
	var envelope struct {
		Info []json.RawMessage `json:"info"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Info) == 0 {
		return nil
	}
	var first []json.RawMessage
	if json.Unmarshal(envelope.Info[0], &first) != nil || len(first) < 16 {
		return nil
	}
	var extra struct {
		Extra string `json:"extra"`
	}
	if json.Unmarshal(first[15], &extra) != nil || extra.Extra == "" {
		return nil
	}
	var payload struct {
		Emots map[string]biliEmoteJSON `json:"emots"`
	}
	if json.Unmarshal([]byte(extra.Extra), &payload) != nil || len(payload.Emots) == 0 {
		return nil
	}

	out := make([]biliInlineEmote, 0, len(payload.Emots))
	for name, emote := range payload.Emots {
		if name == "" {
			continue
		}
		out = append(out, biliInlineEmote{name: name, emote: emote})
	}
	return out
}

// biliHTTPS 把图片地址统一成 https。B 站偶尔下发 http 的 CDN 域名，
// 混着用会让图片被拦。
func biliHTTPS(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.Replace(raw, "http://", "https://", 1)
}

// biliHexColor 只接受 #RRGGBB 形式的颜色，其余一律返回空串。
func biliHexColor(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) != 7 || !strings.HasPrefix(raw, "#") {
		return ""
	}
	return raw
}

// 编译期确认 B 站实现了弹幕接口。
var _ Danmakuer = (*bilibiliProvider)(nil)
