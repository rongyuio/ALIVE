package live

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 斗鱼弹幕：裸 TCP + STT 私有协议。
//
// 这里**不走 WebSocket**：danmuproxy.douyu.com 的
// 8501–8506 端口已经不再提供 TLS/WebSocket 服务（Go 和 Node 客户端都在
// 握手阶段被拒），而 8601–8603 上的明文 TCP 仍然可用，报文格式是同一套
// STT 包。差别只在传输层——TCP 要自己按长度字段切包，而 WebSocket 时代
// 一个 message 就是一个包。
var douyuDanmakuEndpoints = []string{
	"danmuproxy.douyu.com:8601",
	"danmuproxy.douyu.com:8602",
	"danmuproxy.douyu.com:8603",
}

const (
	// 心跳 45s；服务端也会发 pingreq 要求立刻回。
	douyuHeartbeat   = 45 * time.Second
	douyuReadTimeout = 90 * time.Second
	douyuDialTimeout = 10 * time.Second
	douyuMaxBackoff  = 30 * time.Second
	// 单包上限：actqzs_* 这类活动包可以到 2KB+，留足余量但不能无限。
	douyuMaxPacket = 1 << 16
)

// douyuDanmaku 是一次房间监听会话。
type douyuDanmaku struct {
	roomID    string
	onMessage func(Danmu)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once

	mu   sync.Mutex
	conn net.Conn
	seq  int64
	// seenVR 是已上屏的高能弹幕 id（vrId）。
	// 服务端除了逐条推，还会在有新条目时重发整个 list，不去重会重复刷屏。
	seenVR  map[string]struct{}
	vrOrder []string
}

// Danmaku 开始监听一个斗鱼房间的弹幕。
//
// 斗鱼的弹幕接口直接吃房间号，不需要先换算真实房间号，因此这里不阻塞：
// 拿到一个可用的会话对象就返回，连不上由后台按退避重试。
func (p *douyuProvider) Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return nil, &UpstreamError{Platform: Douyu, Op: "弹幕", Message: "房间号为空"}
	}
	if _, err := strconv.Atoi(roomID); err != nil {
		return nil, &UpstreamError{
			Platform: Douyu, Op: "弹幕",
			Message: fmt.Sprintf("房间号 %q 不是合法的数字", roomID),
		}
	}
	if onMessage == nil {
		onMessage = func(Danmu) {}
	}

	s := &douyuDanmaku{
		roomID: roomID, onMessage: onMessage, done: make(chan struct{}),
		seenVR: map[string]struct{}{},
	}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run()
	return s, nil
}

// Close 停止会话。重复调用安全。
func (s *douyuDanmaku) Close() {
	s.once.Do(func() {
		// 先取消上下文再主动断开：阻塞中的 Read 会立刻返回，不用等 deadline。
		s.cancel()
		s.closeConn()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	})
}

func (s *douyuDanmaku) setConn(conn net.Conn) {
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
}

func (s *douyuDanmaku) closeConn() {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// run 是重连主循环：断开后指数退避，直到 Close。
func (s *douyuDanmaku) run() {
	defer close(s.done)

	backoff := time.Second
	for {
		started := time.Now()
		err := s.connectAndRead()
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("斗鱼弹幕断开", "room", s.roomID, "error", err, "retry", backoff)
		}
		// 稳过 10s 再断说明只是抖动，下一次从 1s 重新开始
		if time.Since(started) >= 10*time.Second {
			backoff = time.Second
		}

		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}

		if backoff < douyuMaxBackoff {
			backoff *= 2
			if backoff > douyuMaxBackoff {
				backoff = douyuMaxBackoff
			}
		}
	}
}

// connectAndRead 建立一次连接并读到断开为止。
func (s *douyuDanmaku) connectAndRead() error {
	conn, err := douyuDial(s.ctx)
	if err != nil {
		return err
	}
	s.setConn(conn)
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
		_ = conn.Close()
	}()

	// 登录 → 加房间，顺序不能反
	if err := douyuWriteSTT(conn, fmt.Sprintf("type@=loginreq/roomid@=%s/", s.roomID)); err != nil {
		return fmt.Errorf("发送登录包: %w", err)
	}
	if err := douyuWriteSTT(conn, fmt.Sprintf("type@=joingroup/rid@=%s/gid@=1/", s.roomID)); err != nil {
		return fmt.Errorf("发送加房间包: %w", err)
	}

	hbCtx, hbCancel := context.WithCancel(s.ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(douyuHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := douyuWriteSTT(conn, "type@=mrkl/"); err != nil {
					return
				}
			}
		}
	}()

	for {
		// deadline 既让 Close 及时生效，也能识别「服务端静默」的半死连接
		_ = conn.SetReadDeadline(time.Now().Add(douyuReadTimeout))

		packet, err := douyuReadPacket(conn)
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		s.handlePacket(conn, packet)
	}
}

// douyuDial 依次尝试候选服务器，返回第一个连上的。
func douyuDial(ctx context.Context) (net.Conn, error) {
	var lastErr error
	for _, endpoint := range douyuDanmakuEndpoints {
		dialer := &net.Dialer{Timeout: douyuDialTimeout}
		dialCtx, cancel := context.WithTimeout(ctx, douyuDialTimeout)
		conn, err := dialer.DialContext(dialCtx, "tcp", endpoint)
		cancel()
		if err == nil {
			return conn, nil
		}
		lastErr = fmt.Errorf("%s: %w", endpoint, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的斗鱼弹幕服务器")
	}
	return nil, fmt.Errorf("连接斗鱼弹幕服务器失败（%w）", lastErr)
}

// handlePacket 解析一个 STT 包。
//
// 取包范围是 packet[12:len-1]：跳过 12 字节头、去掉尾部 \0。
func (s *douyuDanmaku) handlePacket(conn net.Conn, packet []byte) {
	if len(packet) < 13 {
		return
	}
	fields := douyuParseSTT(string(packet[12 : len(packet)-1]))

	switch fields["type"] {
	case "chatmsg":
		content := fields["txt"]
		if strings.TrimSpace(content) == "" {
			return
		}
		at := time.Now()
		// 粉丝牌：bl 是牌等级、bnn 是牌名；没有牌时 bl 为 0、bnn 为空。
		medal := Medal{}
		if lv := douyuAtoi(fields["bl"]); lv > 0 {
			medal = Medal{Name: strings.TrimSpace(fields["bnn"]), Level: lv}
		}
		s.emit(Danmu{
			Nickname: douyuFirst(fields["nn"], "未知用户"),
			Content:  content,
			Level:    douyuAtoi(fields["level"]),
			Medal:    medal,
			Color:    douyuColor(fields["col"]),
			Kind:     DanmuChat,
			At:       at,
		})

	case "voice_trlt":
		// 高能弹幕：付费语音弹幕。房间里每来一条就推一帧，list 里通常
		// 只有这一条；有新条目时会把整个列表重发一遍，所以要按 vrId 去重。
		for _, d := range douyuVoiceBarrages(fields["list"]) {
			if !s.firstSight(d.ID) {
				continue
			}
			s.emit(d)
		}

	case "pingreq":
		// 服务端主动要心跳，必须回，否则会被认为掉线
		_ = douyuWriteSTT(conn, "type@=mrkl/")
	}
}

// emit 补默认值后回调。
func (s *douyuDanmaku) emit(msg Danmu) {
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

func (s *douyuDanmaku) nextID(at time.Time) string {
	s.mu.Lock()
	s.seq++
	seq := s.seq
	s.mu.Unlock()
	return fmt.Sprintf("douyu:%s:%d:%d", s.roomID, at.UnixMilli(), seq)
}

// firstSight 报告这个高能弹幕 id 是不是第一次见到，并把它记下来。
//
// 只保留最近 maxSeenVoice 条：斗鱼会把整个 list 重发，「最近见过」就够，
// 留全量没有意义，长会话里还会一直涨。
func (s *douyuDanmaku) firstSight(id string) bool {
	if id == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seenVR[id]; ok {
		return false
	}
	s.seenVR[id] = struct{}{}
	s.vrOrder = append(s.vrOrder, id)
	if len(s.vrOrder) > maxSeenVoice {
		delete(s.seenVR, s.vrOrder[0])
		s.vrOrder = s.vrOrder[1:]
	}
	return true
}

// maxSeenVoice 是去重表保留的条数。
const maxSeenVoice = 256

// douyuUnescape 还原 STT 的值转义（@S 是 /，@A 是 @）。
func douyuUnescape(value string) string {
	value = strings.ReplaceAll(value, "@S", "/")
	return strings.ReplaceAll(value, "@A", "@")
}

// douyuVoiceBarrages 解析 voice_trlt 的 list 字段。
//
// list 本身是一个「被转义过两层的 STT 串」，暗号和外层一样：
//
//	外层     vrId@AA=2122…@ASun@AA=…
//	解一层   vrId@A=2122…@S un@A=…
//	再解一层 vrId@=2122…/un@=…
//
// douyuParseSTT 已经解掉了外层，所以这里只在 @S 上切项、按 @A= 取值，
// 值再解两次——URL 里的 / 比正文多一层转义（`https:@AS@ASapic…`），
// 少解一次拿到的就是半截地址。
//
// 金额是「分 × 10」的整数：面板接口 minVoicePrice=1000 对应 10 元，
// 所以要除以 100 才是元。realPrice 是实付，price 是档位，优先用实付。
func douyuVoiceBarrages(list string) []Danmu {
	if list == "" {
		return nil
	}
	var out []Danmu
	var cur *Danmu
	var price, realPrice int
	var color int
	var acptime, etime int64

	flush := func() {
		if cur != nil {
			amount := realPrice
			if amount <= 0 {
				amount = price
			}
			cur.Price = amount / 100
			cur.Background = douyuEnergyColor(color)
			switch {
			case acptime > 0:
				cur.At = time.Unix(acptime, 0)
			case etime > 0:
				cur.At = time.Unix(etime, 0)
			}
			if cur.Nickname == "" {
				cur.Nickname = "未知用户"
			}
			if cur.Content != "" {
				out = append(out, *cur)
			}
		}
		cur, price, realPrice, color, acptime, etime = nil, 0, 0, 0, 0, 0
	}

	for _, item := range strings.Split(list, "@S") {
		key, value, ok := strings.Cut(item, "@A=")
		if !ok {
			continue
		}
		value = douyuUnescape(douyuUnescape(value))
		if key == "vrId" {
			flush()
			cur = &Danmu{Kind: DanmuSuperChat, ID: "douyu:vr:" + value}
			continue
		}
		if cur == nil {
			continue
		}
		switch key {
		case "un":
			cur.Nickname = value
		case "content":
			cur.Content = value
		case "price":
			price = douyuAtoi(value)
		case "realPrice":
			realPrice = douyuAtoi(value)
		case "danmucr":
			color = douyuAtoi(value)
		case "acptime":
			acptime = int64(douyuAtoi(value))
		case "etime":
			etime = int64(douyuAtoi(value))
		}
	}
	flush()
	return out
}

// douyuEnergyColor 把高能弹幕的颜色索引转成平台的头部色。
//
// 网页端 da={1:is-blue, 2:is-red, 3:is-yellow}，卡片边框和 40px 头部就用
// 这三个色（见 BarrageGroup 的 HighEnergyV2Barrage 样式）；缺省按蓝色。
func douyuEnergyColor(idx int) string {
	switch idx {
	case 2:
		return "#d45954"
	case 3:
		return "#f9af1b"
	default:
		return "#385fac"
	}
}

// ---------------------------------------------------------------- STT 协议

// douyuEncodeSTT 把一条指令编码成协议包。
//
// 包结构：
//
//	u32 LE 长度(=len(msg)+9) | u32 LE 同一长度 | u16 LE 689 | 0x00 0x00 | msg | 0x00
//
// 长度字段不含它自己那 4 字节，所以总字节数 = declared + 4。
func douyuEncodeSTT(msg string) []byte {
	body := []byte(msg)
	declared := uint32(len(body) + 9)

	out := make([]byte, 0, len(body)+13)
	out = binary.LittleEndian.AppendUint32(out, declared)
	out = binary.LittleEndian.AppendUint32(out, declared)
	out = binary.LittleEndian.AppendUint16(out, 689)
	out = append(out, 0, 0)
	out = append(out, body...)
	out = append(out, 0)
	return out
}

func douyuWriteSTT(conn net.Conn, msg string) error {
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := conn.Write(douyuEncodeSTT(msg))
	return err
}

// douyuReadPacket 从 TCP 流里读一个完整包。
//
// 服务端会把多个包连续写在同一连接上，必须按长度字段切 ——
// 这正是 WebSocket 时代不用操心的那部分。
func douyuReadPacket(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	declared := binary.LittleEndian.Uint32(header)
	if declared < 8 || declared > douyuMaxPacket {
		return nil, fmt.Errorf("弹幕包长度异常: %d", declared)
	}

	packet := make([]byte, 4+declared)
	copy(packet, header)
	if _, err := io.ReadFull(r, packet[4:]); err != nil {
		return nil, err
	}
	return packet, nil
}

// douyuParseSTT 解析 "k1@=v1/k2@=v2/" 形式的消息体。
//
// 值里的 @S 代表 '/'、@A 代表 '@'（斗鱼自己的转义）。
func douyuParseSTT(content string) map[string]string {
	out := make(map[string]string, 16)
	for _, item := range strings.Split(content, "/") {
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, "@=")
		if !ok {
			continue
		}
		value = strings.ReplaceAll(value, "@S", "/")
		value = strings.ReplaceAll(value, "@A", "@")
		out[key] = value
	}
	return out
}

// douyuColColors 是 col 字段的调色板。
//
// col **不是十进制 RGB，而是调色板索引**（官方《弹幕服务器接入协议》原文：
// 「col 颜色:默认值 0(表示默认颜色弹幕)」）。表取自斗鱼官方 web 播放器
// bundle 里的 color 映射，七个取值一一对应。
//
// 按 RGB 解会得到 col=4 → #000004 这种近黑色，彩色弹幕全黑。
var douyuColColors = map[int]string{
	1: "#FF2E2E", // 红
	2: "#00CCFF", // 青
	3: "#66FF00", // 草绿
	4: "#FF6600", // 橙
	5: "#CC00FF", // 紫
	6: "#F6447F", // 玫红
	7: "#FF84F6", // 粉
}

// douyuColor 把 col 转成 #RRGGBB；空/0/未知一律返回空串，
// 让界面按昵称哈希取色 —— 绝不能返回黑色。
func douyuColor(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return ""
	}
	if c, ok := douyuColColors[n]; ok {
		return c
	}
	// 调色板只有 1~7；大数值按十进制 RGB 兜底（万一上游改回 RGB 语义）
	if n >= 0x10000 && n <= 0xFFFFFF {
		return fmt.Sprintf("#%06X", n)
	}
	return ""
}

func douyuAtoi(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0
	}
	return n
}

// 编译期确认斗鱼实现了弹幕接口。
var _ Danmakuer = (*douyuProvider)(nil)
