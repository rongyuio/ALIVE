package live

import "testing"

// 构造一份最小可解的 getHeadLineMessageBoard 响应：
// 一条 30 元、还剩 42 秒的留言，等级 2 的展示样式底色是 ARGB 0xFF385FAC。
func huyaHeadlineSample() []byte {
	w := newHuyaTarsWriter()
	w.putHead(0, huyaTarsStructBegin) // 服务端外套的一层结构

	w.putHead(1, huyaTarsStructBegin) // tMessageBoardPanel
	w.putHead(1, huyaTarsList)        // vGameEventMessageBoardInfo
	w.writeInt32(0, 1)                // 一条
	w.putHead(0, huyaTarsStructBegin)
	w.putHead(0, huyaTarsStructBegin) // tMessageUser
	w.writeString(1, "小明")
	w.putHead(0, huyaTarsStructEnd)
	w.writeString(1, "这波必须支持一下")
	w.writeInt32(5, 42)          // iCountDown
	w.writeInt64(9, 12345678901) // lMessageId
	w.writeInt32(11, 2)          // iLevel
	w.writeInt32(12, 3000)       // iCostPay：分币，/100 = 30 元
	w.putHead(0, huyaTarsStructEnd)
	w.putHead(0, huyaTarsStructEnd) // 面板结束

	w.putHead(4, huyaTarsMap) // mMessageBoardShowStyle
	w.writeInt32(0, 1)
	w.writeInt32(0, 2) // key = 等级 2
	w.putHead(0, huyaTarsStructBegin)
	w.writeInt32(13, argbTest(0xFF385FAC)) // iLightNormalBgColor
	w.putHead(0, huyaTarsStructEnd)

	w.putHead(0, huyaTarsStructEnd) // 外套结束
	return w.bytes()
}

func argbTest(v uint32) int32 { return int32(v) }

func TestHuyaParseHeadlines(t *testing.T) {
	got := huyaParseHeadlines(huyaHeadlineSample())
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条，期望 1 条", len(got))
	}
	h := got[0]
	if h.Nickname != "小明" || h.Content != "这波必须支持一下" {
		t.Errorf("昵称/正文 = %q/%q", h.Nickname, h.Content)
	}
	if h.Price != 30 {
		t.Errorf("金额 = %d，期望 30", h.Price)
	}
	if h.Background != "#385fac" {
		t.Errorf("背景色 = %q，期望 #385fac", h.Background)
	}
	if h.ID != 12345678901 {
		t.Errorf("id = %d，64 位被截断了", h.ID)
	}
}

// 倒计时结束的留言不再上屏。
func TestHuyaHeadlineSkipsExpired(t *testing.T) {
	w := newHuyaTarsWriter()
	w.putHead(0, huyaTarsStructBegin)
	w.putHead(1, huyaTarsStructBegin)
	w.putHead(1, huyaTarsList)
	w.writeInt32(0, 1)
	w.putHead(0, huyaTarsStructBegin)
	w.putHead(0, huyaTarsStructBegin)
	w.writeString(1, "小明")
	w.putHead(0, huyaTarsStructEnd)
	w.writeString(1, "过时的留言")
	w.writeInt32(5, 0) // iCountDown
	w.writeInt64(9, 1) // id
	w.writeInt32(12, 1000)
	w.putHead(0, huyaTarsStructEnd)
	w.putHead(0, huyaTarsStructEnd)
	w.putHead(0, huyaTarsStructEnd)
	if got := huyaParseHeadlines(w.bytes()); len(got) != 0 {
		t.Fatalf("过时留言仍被解析出 %d 条", len(got))
	}
}

// 轮询会反复拿到同一条还在倒计时的留言，只上屏一次。
func TestHuyaHeadlineFirstSight(t *testing.T) {
	s := &huyaDanmaku{seenHead: map[int64]struct{}{}}
	if !s.firstSightHeadline(7) {
		t.Fatal("第一次应当是新的")
	}
	if s.firstSightHeadline(7) {
		t.Fatal("同一条留言被当成新的，会重复上屏")
	}
	if !s.firstSightHeadline(8) {
		t.Fatal("另一条留言应当是新的")
	}
}

// 去重表有上限，老 id 淘汰后不会无限长。
func TestHuyaHeadlineFirstSightCaps(t *testing.T) {
	s := &huyaDanmaku{seenHead: map[int64]struct{}{}}
	for i := int64(1); i <= huyaHeadlineMax+10; i++ {
		s.firstSightHeadline(i)
	}
	if len(s.seenHead) != huyaHeadlineMax {
		t.Errorf("去重表 %d 条，期望上限 %d", len(s.seenHead), huyaHeadlineMax)
	}
	if s.firstSightHeadline(1) != true {
		t.Error("最早的 id 应已被淘汰")
	}
}

func TestHuyaARGBColor(t *testing.T) {
	if got := huyaARGBColor(argbTest(0xFF385FAC)); got != "#385fac" {
		t.Errorf("颜色 = %q", got)
	}
	if got := huyaARGBColor(0); got != "" {
		t.Errorf("0 应返回空串，得到 %q", got)
	}
}

// 请求体必须是「外层 struct(tag0) 包着 Req」，lPid 放 Req 的 tag0。
// 这两点都踩过坑：平铺直接被服务端拒（read 'struct' type mismatch, tag: 0）。
func TestHuyaHeadlineReqShape(t *testing.T) {
	const channel = 2367547387
	raw := huyaHeadlineReq(channel)

	r := newHuyaTarsReader(raw)
	outer, err := r.takeHead()
	if err != nil || outer.tag != 0 || outer.typ != huyaTarsStructBegin {
		t.Fatalf("外层头 = %+v err=%v，期望 struct(0)", outer, err)
	}
	if got := r.readInt64Tag(0, 0); got != channel {
		t.Errorf("lPid = %d，期望 %d", got, channel)
	}
	if got := raw[len(raw)-1]; got != byte(huyaTarsStructEnd) {
		t.Errorf("尾部 = %#x，期望外层 struct 结束", got)
	}
}
