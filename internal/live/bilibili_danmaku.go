package live

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	blivedmmsg "github.com/Akegarasu/blivedm-go/message"
	blivedmpacket "github.com/Akegarasu/blivedm-go/packet"
	"github.com/andybalholm/brotli"
	"github.com/coder/websocket"
	bilibiliLive "github.com/rongyuio/bilibili/live"

	"alive/internal/diag"
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
	// 单帧解压后的上限与压缩嵌套层数：B 站只发一层，超出的按坏帧处理。
	// 没有上限的话，一条压缩炸弹就能把内存吃光。
	biliDanmuMaxBody  = 8 << 20
	biliDanmuMaxDepth = 2
	biliDanmuDialTO   = 12 * time.Second
	// 断线退避：1s 起，最多 30s；连接稳住 10s 后下一次重新从 1s 开始。
	biliDanmuMaxBack = 30 * time.Second
	biliDanmuStable  = 10 * time.Second
	// protover=3 表示接受 brotli 压缩帧，与官方前端一致。
	biliDanmuProto  = 3
	biliDanmuOrigin = "https://live.bilibili.com"
	biliDanmuRefer  = "https://live.bilibili.com/"
	// 协议头固定 16 字节：packLen(4) + headerLen(2) + ver(2) + op(4) + seq(4)。
	biliDanmuHeaderLen = 16
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
			diag.L().Debug().Str("room", s.roomID).Dict(diag.ErrField, diag.Err(err)).Dur("retry", backoff).Msg("B 站弹幕断开")
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

// biliLoginUID 返回当前会话的登录 uid，未登录时返回 "0"。
//
// 弹幕 auth 包里的 uid 必须和 getDanmuInfo 签发 token 时用的是同一个。
// 登录态下 token 把登录 uid 编了进去，再拿匿名 uid 去认证，服务端会直接
// 断开连接——表现是连上就 EOF，一条弹幕都收不到，上层只看到「断开重连」
// 在循环。实测 2026-11-16：同一个 token，uid=0 立刻 EOF，
// uid=<登录 uid> 正常收到认证回复。
//
// 返回字符串而不是数字：uid 已经是 16 位量级，拼进 JSON 不该经过浮点。
func (p *bilibiliProvider) biliLoginUID() string {
	for _, c := range p.api.GetCookies() {
		if c.Name == "DedeUserID" && c.Value != "" {
			return c.Value
		}
	}
	return "0"
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
	// uid 必须和签发 token 用的一致：登录态下是登录 uid，匿名才是 0。
	// 写死 0 会让登录用户的连接被服务端立刻断开（见 biliLoginUID）。
	auth := fmt.Sprintf(`{"uid":%s,"roomid":%d,"protover":%d,"platform":"web","type":2,"key":%q}`,
		s.provider.biliLoginUID(), s.realID, biliDanmuProto, token)
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
		typ, data, err := conn.Read(readCtx)
		cancel()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("读取帧: %w", err)
		}
		// B 站弹幕只有二进制帧。文本帧不是协议的一部分，按二进制切长度
		// 只会切出错位的数据，直接丢弃这一帧。
		payload, err := biliFramePayload(typ, data)
		if err != nil {
			diag.L().Debug().Str("room", s.roomID).Dict(diag.ErrField, diag.Err(err)).Msg("B 站弹幕忽略非二进制帧")
			continue
		}
		if err := s.handleFrame(payload); err != nil {
			return fmt.Errorf("解析帧: %w", err)
		}
	}
}

// biliFramePayload 只放行二进制帧。
func biliFramePayload(typ websocket.MessageType, data []byte) ([]byte, error) {
	if typ != websocket.MessageBinary {
		return nil, fmt.Errorf("帧类型 %d 不是二进制", typ)
	}
	return data, nil
}

// biliSplitFrames 按协议头把一条 WS 消息切成完整的帧。
//
// 不直接用 blivedmpacket.Slice：那个函数无边界检查——声明长度大于剩余
// 字节会越界切片，packLen 为 0 会让 cursor 原地踏步、无限循环。这里先
// 校验头再切，坏帧返回错误交给上层重连。
func biliSplitFrames(data []byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("弹幕帧为空")
	}
	var frames [][]byte
	for cursor := 0; cursor < len(data); {
		rest := len(data) - cursor
		if rest < biliDanmuHeaderLen {
			return nil, fmt.Errorf("弹幕帧头不足：剩余 %d 字节", rest)
		}
		packLen := int(binary.BigEndian.Uint32(data[cursor : cursor+4]))
		headerLen := int(binary.BigEndian.Uint16(data[cursor+4 : cursor+6]))
		if packLen < biliDanmuHeaderLen || packLen > rest {
			return nil, fmt.Errorf("弹幕帧长度非法：声明 %d，剩余 %d", packLen, rest)
		}
		if headerLen < biliDanmuHeaderLen || headerLen > packLen {
			return nil, fmt.Errorf("弹幕帧头长度非法：%d（帧长 %d）", headerLen, packLen)
		}
		frames = append(frames, data[cursor:cursor+packLen])
		cursor += packLen
	}
	return frames, nil
}

// biliUnpackFrame 把一帧拆成业务包，解压由本包自己做。
//
// 不能把解压后的字节交回 blivedmpacket.Slice：那个函数对内层帧不做任何
// 边界检查，packLen==0 时 cursor 原地踏步、永不返回——recover 接不住死循环。
// 解压结果先过 biliSplitFrames，再逐帧递归（B 站只会压一层，深度有上限）。
func biliUnpackFrame(frame []byte, depth int) ([]blivedmpacket.Packet, error) {
	if depth > biliDanmuMaxDepth {
		return nil, fmt.Errorf("弹幕压缩嵌套过深")
	}
	p := blivedmpacket.DecodePacket(frame)
	switch p.ProtocolVersion {
	case blivedmpacket.Plain, blivedmpacket.Popularity:
		return []blivedmpacket.Packet{p}, nil
	case blivedmpacket.Zlib:
		zr, err := zlib.NewReader(bytes.NewReader(p.Body))
		if err != nil {
			return nil, fmt.Errorf("zlib 弹幕帧: %w", err)
		}
		defer zr.Close()
		body, err := readBiliBody(zr)
		if err != nil {
			return nil, fmt.Errorf("zlib 弹幕帧: %w", err)
		}
		return biliUnpackAll(body, depth+1)
	case blivedmpacket.Brotli:
		body, err := readBiliBody(brotli.NewReader(bytes.NewReader(p.Body)))
		if err != nil {
			return nil, fmt.Errorf("brotli 弹幕帧: %w", err)
		}
		return biliUnpackAll(body, depth+1)
	default:
		return nil, fmt.Errorf("未知弹幕协议版本 %d", p.ProtocolVersion)
	}
}

// biliUnpackAll 校验并展开一段（可能是解压后的）帧序列。
func biliUnpackAll(body []byte, depth int) ([]blivedmpacket.Packet, error) {
	frames, err := biliSplitFrames(body)
	if err != nil {
		return nil, err
	}
	out := make([]blivedmpacket.Packet, 0, len(frames))
	for _, f := range frames {
		packets, err := biliUnpackFrame(f, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, packets...)
	}
	return out, nil
}

// readBiliBody 读出解压内容，并卡住上限。
func readBiliBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, biliDanmuMaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > biliDanmuMaxBody {
		return nil, fmt.Errorf("解压后超过 %d 字节", biliDanmuMaxBody)
	}
	return body, nil
}

// handleFrame 解析一条 WS 消息（可能含多个包，压缩包会被逐层展开）。
func (s *biliDanmaku) handleFrame(data []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("弹幕帧解析崩溃: %v", r)
		}
	}()

	packets, err := biliUnpackAll(data, 0)
	if err != nil {
		return err
	}
	for _, inner := range packets {
		if inner.Operation == uint32(blivedmpacket.Notification) {
			s.handleCommand(inner.Body)
		}
	}
	return nil
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
		medal := Medal{}
		if d.Sender != nil {
			if name := strings.TrimSpace(d.Sender.Uname); name != "" {
				nickname = name
			}
			if m := d.Sender.Medal; m != nil {
				name := strings.TrimSpace(m.Name)
				if name != "" && m.Level > 0 {
					// 底色取 v2 配色（自带 alpha），网页端就是拿它画牌子；
					// 老字段 medal.color 是旧版暗色，只在 v2 缺失时兜底。
					start, end := biliMedalV2Colors(body)
					if start == "" {
						start = biliMedalTint(biliMedalColor(m.Color))
						end = start
					}
					medal = Medal{Name: name, Level: m.Level, Color: start, ColorEnd: end}
				}
			}
		}
		// 直播间里说的「等级」是荣耀等级（消费等级），不是主站账号的 UL。
		// UL 是社区活跃度，和直播间消费是两回事，界面上挂它会读成假的。
		level := biliWealthLevel(body)
		content := strings.TrimSpace(d.Content)
		emotes := biliEmotes(body, d)
		// 表情弹幕（dm_type=1）整条就是一张贴纸：房间表情的正文是表情
		// 自己的名字（「哇哈哈」「赞！」，不带方括号），收藏集表情的正文
		// 是 [占位符]。两种都不该当文字画——正文清空，交给纯图分支按
		// 贴纸尺寸渲染。名字匹配不到 [占位符] 时留着会被原样画出来。
		if d.Type == 1 && len(emotes) == 1 {
			content = ""
		}
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
			Medal:    medal,
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
		// SC 的粉丝牌不在 blivedm 的 SuperChat 结构里，在原始 body 的
		// data.medal_info（medal_name / medal_level）。实测样本：
		// {"data":{"medal_info":{"medal_name":"哎妹妹","medal_level":23}}}
		medal := Medal{}
		var raw struct {
			Data struct {
				MedalInfo struct {
					Name  string `json:"medal_name"`
					Level int    `json:"medal_level"`
				} `json:"medal_info"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &raw) == nil {
			name := strings.TrimSpace(raw.Data.MedalInfo.Name)
			if name != "" && raw.Data.MedalInfo.Level > 0 {
				medal = Medal{Name: name, Level: raw.Data.MedalInfo.Level}
			}
		}
		at := time.Now()
		s.emit(Danmu{
			ID:         s.nextID(at),
			Nickname:   firstNonEmpty(strings.TrimSpace(sc.UserInfo.Uname), "匿名"),
			Content:    text,
			Level:      sc.UserInfo.UserLevel,
			Medal:      medal,
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

// biliMedalColor 把勋章颜色整数转成 #RRGGBB；0 或越界表示没给色。
//
// 这是老字段 info[3][4]（medal.color）的编码：和弹幕颜色同一个整数 RGB，
// 但勋章没有「白色=默认」的语义，0 直接当没有。网页端已经不用它了，
// 只在 v2 配色缺失时兜底，所以上面还要补 alpha。
func biliMedalColor(v int) string {
	if v <= 0 || v > 0xFFFFFF {
		return ""
	}
	return fmt.Sprintf("#%06x", v)
}

// biliMedalTint 把 6 位色补齐成带 alpha 的 8 位色，和 v2 配色同一种写法。
func biliMedalTint(hex string) string {
	switch len(hex) {
	case 7:
		return hex + "99" // 网页端牌子的底色就是 #RRGGBB99
	case 9:
		return hex
	}
	return ""
}

// biliMedalV2Colors 从 DANMU_MSG 原始 JSON 取粉丝牌的 v2 底色。
//
//	info[0][15].user.medal:
//	  v2_medal_color_start / v2_medal_color_end  #RRGGBBAA，网页端直接铺成
//	  linear-gradient(45deg, start, end)
//	  v2_medal_color_text                       牌子文字色（四个平台统一白字，
//	                                            这里不取）
//
// 同一档位老字段给的是 #06154c 这种暗色，画出来整屏发灰——2026-11-18 在
// 德云色直播间对照网页 DOM 才定位到：网页端读的是 v2 这两个值。
func biliMedalV2Colors(body []byte) (start, end string) {
	var envelope struct {
		Info []json.RawMessage `json:"info"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Info) == 0 {
		return "", ""
	}
	var first []json.RawMessage
	if json.Unmarshal(envelope.Info[0], &first) != nil || len(first) <= 15 {
		return "", ""
	}
	var holder struct {
		User struct {
			Medal struct {
				Start string `json:"v2_medal_color_start"`
				End   string `json:"v2_medal_color_end"`
			} `json:"medal"`
		} `json:"user"`
	}
	if json.Unmarshal(first[15], &holder) != nil {
		return "", ""
	}
	start = biliMedalTint(strings.TrimSpace(holder.User.Medal.Start))
	end = biliMedalTint(strings.TrimSpace(holder.User.Medal.End))
	if !isHexColor(start) {
		start = ""
	}
	if !isHexColor(end) {
		end = ""
	}
	if start == "" {
		end = ""
	}
	return start, end
}

// isHexColor 检查 #RRGGBBAA 是否合法。ui.Hex 遇到坏值会 panic，
// 平台字段不能直接喂进去。
func isHexColor(s string) bool {
	if len(s) != 9 || s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
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

// biliWealthLevel 读荣耀等级（直播间消费等级）。
//
// 字段是 info[16][0]。这条是拿网页端对出来的：把网页聊天区每条的荣耀等级
// 勋章图（wealth-medal 的 png，图里就是数字）和我们抓到的原始报文按 uid
// 对齐，57 个重叠用户里 info[16][0] 与勋章图零冲突（一张图只对应一个值，
// 值为 0 的正好没有勋章），而 info[4][0] 有 9 张图对应多个值。
//
// 之前用的 info[4][0] 是错的：它和勋章图对不上，不是荣耀等级。
// info[4] 里的数字另有含义，不要拿它当等级显示。
//
// 备选是 extra.user.wealth.level——实测多数消息里没有 user 对象，只当兜底。
func biliWealthLevel(body []byte) int {
	var envelope struct {
		Info []json.RawMessage `json:"info"`
	}
	if json.Unmarshal(body, &envelope) != nil || len(envelope.Info) == 0 {
		return 0
	}
	if len(envelope.Info) > 16 {
		var sixteenth []json.RawMessage
		if json.Unmarshal(envelope.Info[16], &sixteenth) == nil && len(sixteenth) > 0 {
			var level int
			if json.Unmarshal(sixteenth[0], &level) == nil && level > 0 {
				return level
			}
		}
	}
	var first []json.RawMessage
	if json.Unmarshal(envelope.Info[0], &first) != nil || len(first) <= 15 {
		return 0
	}
	var holder struct {
		Extra string `json:"extra"`
	}
	if json.Unmarshal(first[15], &holder) != nil || holder.Extra == "" {
		return 0
	}
	var extra struct {
		User struct {
			Wealth *struct {
				Level int `json:"level"`
			} `json:"wealth"`
		} `json:"user"`
	}
	if json.Unmarshal([]byte(holder.Extra), &extra) != nil || extra.User.Wealth == nil {
		return 0
	}
	return extra.User.Wealth.Level
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
		// 收藏集/装扮表情走的是同一个槽：emoticon_unique 是
		// upower_[花礼Harei收藏集表情包_prpr] 这种带前缀的全名，
		// 正文里的占位符就是去掉前缀的那一段。普通纯图表情这里是
		// 空串或 emoji_224，留空名，交给纯图分支渲染。
		name := strings.TrimPrefix(e.EmoticonUnique, "upower_")
		if !strings.HasPrefix(name, "[") || !strings.HasSuffix(name, "]") || len(name) < 3 {
			name = ""
		}
		push(name, e.Url, e.Width, e.Height, e.IsDynamic)
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
//
// 不能只看长度：弹幕面板按 len==7 就会把它交给 ui.Hex，而 ui.Hex 解析失败
// 直接 panic，打崩渲染路径。SC 的 background_color 是平台下发的普通字符串，
// 必须在这里按 isHexColor 同样的规则逐位校验。
func biliHexColor(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) != 7 || raw[0] != '#' {
		return ""
	}
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return ""
		}
	}
	return raw
}

// 编译期确认 B 站实现了弹幕接口。
var _ Danmakuer = (*bilibiliProvider)(nil)
