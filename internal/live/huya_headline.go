package live

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

// 虎牙「上头条」留言板。
//
// 这是虎牙的付费置顶文字：观众花虎牙币买一条留言，在直播间留言板里
// 挂一段时间（日截图里的金话筒 / 喇叭 / 为你亮牌 / 纸短情长 / 告白城堡）。
// 网页端先 Wup getHeadLineMessageBoard 拉当前列表，之后靠 taf-signal 的
// 2001314 推送增量。这里只做轮询：留言的存活时间以分钟计，20 秒拉一次
// 足够，也省掉一套 signal 订阅。
//
// 归一化后仍然是 DanmuSuperChat：金额、昵称、正文、背景色，和其他平台一致。
const (
	huyaHeadlinePoll = 20 * time.Second
	huyaHeadlineMax  = 256
)

// huyaHeadline 是一条留言板消息（Wup 结构 GameEventMessageBoardInfo）。
type huyaHeadline struct {
	ID         int64
	Nickname   string
	Content    string
	Price      int    // 元
	Background string // #RRGGBB，来自该等级的展示样式；空表示没给
	Countdown  int    // 剩余秒数，<=0 表示已经过时
}

// pollHeadlines 周期性拉留言板并上屏。
func (s *huyaDanmaku) pollHeadlines() {
	defer close(s.pollDone)
	ticker := time.NewTicker(huyaHeadlinePoll)
	defer ticker.Stop()
	for {
		s.fetchHeadlines()
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *huyaDanmaku) fetchHeadlines() {
	// 走 provider 的限流，和列表、档案那些请求共用一档，不额外开一条通道。
	release, err := s.provider.lim.acquire(s.ctx)
	if err != nil {
		return
	}
	headlines, err := huyaFetchHeadlines(s.ctx, s.provider.api.http, s.channelID)
	release()
	if err != nil {
		return
	}
	for _, h := range headlines {
		// 拉取是一次网络往返，这段时间里会话可能已经 Close：每上屏一条之前
		// 都要再看一次 ctx，Close 之后不允许再有任何回调。
		if s.ctx.Err() != nil {
			return
		}
		if !s.firstSightHeadline(h.ID) {
			continue
		}
		s.emit(huyaHeadlineDanmu(h))
	}
}

// huyaHeadlineDanmu 把一条留言折成 SC。金额、昵称、正文、底色四个字段，
// 和 B 站、斗鱼的 SC 走同一条界面路径。
func huyaHeadlineDanmu(h huyaHeadline) Danmu {
	return Danmu{
		ID:         fmt.Sprintf("huya:headline:%d", h.ID),
		Nickname:   h.Nickname,
		Content:    h.Content,
		Price:      h.Price,
		Background: h.Background,
		Kind:       DanmuSuperChat,
		At:         time.Now(),
	}
}

// firstSightHeadline 报告这条留言是不是第一次上屏。
func (s *huyaDanmaku) firstSightHeadline(id int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seenHead[id]; ok {
		return false
	}
	s.seenHead[id] = struct{}{}
	s.headOrder = append(s.headOrder, id)
	if len(s.headOrder) > huyaHeadlineMax {
		delete(s.seenHead, s.headOrder[0])
		s.headOrder = s.headOrder[1:]
	}
	return true
}

// huyaHeadlineReq 构造 getHeadLineMessageBoard 请求。
//
// GetGameEventMessageBoardReq：lPid(tag0) / sOffset(tag1) / tId(tag2) /
// iMessageBoardScope(tag3) / iPageSize(tag4)。lPid 是频道号（网页端的
// TT_PROFILE_INFO.lp，和订阅弹幕用的是同一个 id），不是主播 uid。
//
// 实测两个坑：整个 Req 还要再套一层 struct(tag0)——网页端 taf 的 send()
// 就是这么封的，直接平铺会得到 `read 'struct' type mismatch, tag: 0`；
// 而 tId 里的 UserId 在这台服务端是平铺的（不像表情包接口要再套一层），
// 字段顺序照网页 taf 定义。
func huyaHeadlineReq(channelID int64) []byte {
	w := newHuyaTarsWriter()
	w.writeStructBegin(0) // taf send() 的外层
	w.writeInt64(0, channelID)
	w.writeString(1, "")
	w.writeStructBegin(2) // tId: UserId
	w.writeInt64(0, 0)    // lUid
	w.writeString(1, "")  // sGuid
	w.writeString(2, "")  // sToken
	w.writeString(3, "webh5")
	w.writeString(4, "") // sCookie
	w.writeInt32(5, 0)   // iTokenType
	w.writeString(6, "") // sDeviceInfo
	w.writeString(7, "") // sQIMEI
	w.writeStructEnd()
	w.writeInt32(3, 0)  // iMessageBoardScope：0 房间
	w.writeInt32(4, 10) // iPageSize
	w.writeStructEnd()
	return w.bytes()
}

// huyaFetchHeadlines 拉一次当前的留言板。
func huyaFetchHeadlines(ctx context.Context, client *http.Client, channelID int64) ([]huyaHeadline, error) {
	raw, err := huyaWupCall(ctx, client, "wupui", "getHeadLineMessageBoard", huyaHeadlineReq(channelID))
	if err != nil {
		return nil, err
	}
	return huyaParseHeadlines(raw), nil
}

// huyaParseHeadlines 解 GetGameEventMessageBoardRsp。
//
//	Rsp        : tag1 面板 / tag4 map<int32, MessageBoardShowStyle>
//	面板       : tag1 Vector<GameEventMessageBoardInfo>
//	留言       : tag0 用户 / tag1 正文 / tag5 剩余秒 / tag9 id / tag11 等级 / tag12 实付
//	展示样式   : tag3 深色底 / tag13 浅色底（message board 用浅色这一套）
func huyaParseHeadlines(raw []byte) []huyaHeadline {
	r := newHuyaTarsReader(raw)
	// 服务端把 Rsp 又包了一层 StructBegin/End，和表情包响应一样。
	if len(raw) > 0 && raw[0]&0x0f == huyaTarsStructBegin {
		if _, err := r.takeHead(); err != nil {
			return nil
		}
	}

	panel, err := r.find(1)
	if err != nil || panel.typ != huyaTarsStructBegin {
		return nil
	}
	list, err := r.find(1)
	if err != nil || list.typ != huyaTarsList {
		return nil
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 50 {
		return nil
	}

	type item struct {
		h     huyaHeadline
		level int
	}
	items := make([]item, 0, count)
	for i := 0; i < count; i++ {
		head, err := r.takeHead()
		if err != nil || head.typ != huyaTarsStructBegin {
			break
		}
		var it item
		var costPay int
		for {
			f, err := r.takeHead()
			if err != nil || f.typ == huyaTarsStructEnd {
				break
			}
			switch f.tag {
			case 0: // MessageUser
				if f.typ != huyaTarsStructBegin {
					_ = r.skipField(f.typ)
					continue
				}
				for {
					uf, err := r.takeHead()
					if err != nil || uf.typ == huyaTarsStructEnd {
						break
					}
					switch uf.tag {
					case 1:
						it.h.Nickname = r.stringFromHead(uf, "")
					default:
						_ = r.skipField(uf.typ)
					}
				}
			case 1:
				it.h.Content = r.stringFromHead(f, "")
			case 5:
				it.h.Countdown = int(r.intFromHead(f, 0))
			case 9:
				it.h.ID = r.int64FromHead(f, 0)
			case 11:
				it.level = int(r.intFromHead(f, 0))
			case 12:
				costPay = int(r.intFromHead(f, 0))
			default:
				_ = r.skipField(f.typ)
			}
		}
		it.h.Price = costPay / 100
		// 正文是网页端转义过的（&lt; &amp; &#39;），网页自己用 HUYA_UTIL.unescape
		// 还原；这里同样要还原，否则面板里会直接显示实体。
		it.h.Nickname = html.UnescapeString(strings.TrimSpace(it.h.Nickname))
		it.h.Content = html.UnescapeString(strings.TrimSpace(it.h.Content))
		if it.h.ID == 0 || it.h.Price <= 0 || it.h.Content == "" || it.h.Countdown <= 0 {
			continue
		}
		items = append(items, it)
	}

	styles := huyaHeadlineStyles(r)
	out := make([]huyaHeadline, 0, len(items))
	for _, it := range items {
		if c := styles[it.level]; c != 0 {
			it.h.Background = huyaARGBColor(c)
		}
		out = append(out, it.h)
	}
	return out
}

// huyaHeadlineStyles 读 map<int32, MessageBoardShowStyle> 的浅色底色。
//
// 面板上每档留言用一套配色，浅色字段是 iLightNormalBgColor(tag13)；
// 深色字段 tag3 只当兜底。
func huyaHeadlineStyles(r *huyaTarsReader) map[int]int32 {
	out := map[int]int32{}
	head, err := r.find(4)
	if err != nil || head.typ != huyaTarsMap {
		return out
	}
	count := int(r.readInt32(0, 0))
	if count <= 0 || count > 30 {
		return out
	}
	for i := 0; i < count; i++ {
		kh, err := r.takeHead()
		if err != nil {
			return out
		}
		key := int(r.intFromHead(kh, 0))
		vh, err := r.takeHead()
		if err != nil || vh.typ != huyaTarsStructBegin {
			return out
		}
		var light, dark int32
		for {
			f, err := r.takeHead()
			if err != nil || f.typ == huyaTarsStructEnd {
				break
			}
			switch f.tag {
			case 3:
				dark = r.intFromHead(f, 0)
			case 13:
				light = r.intFromHead(f, 0)
			default:
				_ = r.skipField(f.typ)
			}
		}
		if light == 0 {
			light = dark
		}
		out[key] = light
	}
	return out
}

// huyaARGBColor 把平台的颜色整数（0xAARRGGBB）转成 #RRGGBB。
func huyaARGBColor(v int32) string {
	if v == 0 {
		return ""
	}
	u := uint32(v)
	return fmt.Sprintf("#%02x%02x%02x", (u>>16)&0xff, (u>>8)&0xff, u&0xff)
}

// stringFromHead 读一个已经取过头的字符串字段。
func (r *huyaTarsReader) stringFromHead(head huyaTarsHead, def string) string {
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

// int64FromHead 读一个已经取过头的整数（保留 64 位，留言 id 会超过 int32）。
func (r *huyaTarsReader) int64FromHead(head huyaTarsHead, def int64) int64 {
	switch head.typ {
	case huyaTarsZero:
		return 0
	case huyaTarsInt8, huyaTarsInt16, huyaTarsInt32:
		return int64(r.intFromHead(head, 0))
	case huyaTarsInt64:
		if r.pos+8 > len(r.buf) {
			return def
		}
		var v uint64
		for i := 0; i < 8; i++ {
			v = v<<8 | uint64(r.buf[r.pos+i])
		}
		r.pos += 8
		return int64(v)
	}
	return def
}
