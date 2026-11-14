package live

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Mock 数据层。
//
// 只在 ALIVE_MOCK 有值时启用（见 registry.go）：它把四个平台全部换掉，
// 于是 UI 和交互在没有网络、没有真实消息的情况下也能完整跑一遍。
//
// 做成「假 provider」而不是在 UI 里塞假数据，是为了走同一条链路：
// 弹幕要经过 Danmaku → applyDanmu → 面板 / 飞屏，被覆盖的才是真实代码。
// 之前的 fake 池写在视图层，结果是真实弹幕到了以后两套并存，进直播间
// 头几条永远是假的。
//
//	ALIVE_MOCK=danmu    在播、有弹幕（画面停在封面上，不起 ffmpeg）
//	ALIVE_MOCK=offline  未开播（测空态覆盖层）
const (
	MockDanmu   = "danmu"
	MockOffline = "offline"
)

// mockEmoteURL 当表情弹幕的图片源。
//
// 用 B 站的默认头像：常年在线、不会因为签名过期失效。mock 只关心
// 「图片能不能画出来」，不需要真表情。
const mockEmoteURL = "https://i0.hdslb.com/bfs/face/member/noface.jpg"

// mockEmoteGIF 是 24×24 的 gif，用来验 image/gif 解码器有没有注册。
// 表情里确实有动图，漏注册的话界面上只会剩下一个空位。
const mockEmoteGIF = "https://i0.hdslb.com/bfs/static/jinkela/long/images/loading.gif"

// mockInterval 是剧本的节奏：900ms 一条，和真实弹幕的观感接近。
const mockInterval = 900 * time.Millisecond

var mockNicks = []string{
	"夜行的猫", "柠檬汽水", "盐焗小星球", "路过的风", "半糖去冰",
	"打野的鱼", "橘子海", "晚风信箱", "雾都孤儿", "不吃香菜",
}

type mockProvider struct {
	platform Platform
	mode     string
}

func newMockProvider(p Platform, mode string) *mockProvider {
	return &mockProvider{platform: p, mode: mode}
}

func (m *mockProvider) Platform() Platform { return m.platform }

func (m *mockProvider) Categories(context.Context) ([]CategoryGroup, error) {
	return []CategoryGroup{
		{ID: "1", Name: "热门", Categories: []Category{{ID: "1_1", Name: "全部"}, {ID: "1_2", Name: "网游"}}},
		{ID: "2", Name: "手游", Categories: []Category{{ID: "2_1", Name: "王者荣耀"}, {ID: "2_2", Name: "原神"}}},
	}, nil
}

func (m *mockProvider) Rooms(_ context.Context, _ string, page, size int) (Page, error) {
	// 只造一页：mock 不是为了测分页，看弹幕 UI 用不着无限列表。
	if page > 1 || m.mode == MockOffline {
		return Page{}, nil
	}
	total := 8
	rooms := make([]Room, 0, total)
	for i := 1; i <= total; i++ {
		rooms = append(rooms, Room{
			Platform: m.platform,
			RoomID:   fmt.Sprintf("mock-%d", i),
			Title:    fmt.Sprintf("Mock 直播间 %d —— 用来验界面，不连真实接口", i),
			Nickname: mockNicks[(i-1)%len(mockNicks)],
			Viewers:  int64(1200 * i),
			Area:     "全部",
			Live:     true,
		})
	}
	_ = size
	return Page{Rooms: rooms}, nil
}

func (m *mockProvider) Search(context.Context, string) ([]Room, error) {
	return nil, ErrUnsupported
}

func (m *mockProvider) RoomInfo(_ context.Context, roomID string) (Room, error) {
	return Room{
		Platform: m.platform,
		RoomID:   roomID,
		Title:    "Mock 直播间 —— 用来验界面，不连真实接口",
		Nickname: "夜行的猫",
		Viewers:  12345,
		Area:     "全部",
		Live:     m.mode != MockOffline,
	}, nil
}

// Stream 刻意返回「在播但没有任何档位」：
//
// startStream 看到 len(Variants) == 0 就停下，不会起 ffmpeg、不会连
// 真实 CDN——画面留在封面上，弹幕和所有控件照常可用。这正是测 UI
// 需要的状态：有页面、无网络。
func (m *mockProvider) Stream(_ context.Context, roomID, line string) (Stream, error) {
	return Stream{
		RoomID:   roomID,
		Platform: m.platform,
		Title:    "Mock 直播间 —— 用来验界面，不连真实接口",
		Nickname: "夜行的猫",
		Live:     m.mode != MockOffline,
		// 给两条真实形状的线路：mock 也要能验线路下拉，但它不该叫
		// 「自动」——真实平台那里是 CDN 节点列表。
		Lines: []Line{{ID: "main", Label: "主线"}, {ID: "backup1", Label: "备线1"}},
	}, nil
}

// Danmaku 按固定剧本循环发送，覆盖面板和飞屏要处理的每一种消息。
func (m *mockProvider) Danmaku(ctx context.Context, roomID string, onMessage func(Danmu)) (DanmakuStream, error) {
	if m.mode == MockOffline {
		// 未开播的房间不会有弹幕：给一条永远静默的流，
		// 免得 UI 上出现「在播却没有消息」的错位感。
		return &mockStream{done: make(chan struct{})}, nil
	}
	if onMessage == nil {
		onMessage = func(Danmu) {}
	}
	s := &mockStream{done: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(ctx)
	go s.run(onMessage)
	return s, nil
}

type mockStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (s *mockStream) Close() {
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
		close(s.done)
	})
}

func (s *mockStream) run(onMessage func(Danmu)) {
	ticker := time.NewTicker(mockInterval)
	defer ticker.Stop()

	script := mockScript()
	for i := 0; ; i++ {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			msg := script[i%len(script)]
			msg.ID = fmt.Sprintf("mock:%d", i)
			msg.At = time.Now()
			if msg.Kind == "" {
				msg.Kind = DanmuChat
			}
			onMessage(msg)
		}
	}
}

// longChatText 是一条「真实长度」的长弹幕：三行左右，用来测面板换行、
// 行高和飞屏滚动距离。太短的样本看不出折行对不对。
const longChatText = "这条弹幕故意写得非常长，用来确认面板里的换行、行高和飞屏的滚动距离" +
	"是不是都对得上真实数据里的长句子，顺便看看长昵称会不会把时间或等级挤出容器外面去，" +
	"再看看两三行之后行间距有没有突然变大或者压到一起。"

// longSC 是长醒目留言。真实的 SC 经常是整段话，面板那一块要能撑开好几行，
// 而且金额徽章和昵称不能因此被挤变形。
const longSC = "从三年前开始看你直播，那时候还在小房间，一晚上就几十个人说话。" +
	"现在人多了，弹幕刷得看不清，但每次开播还是会来。这波操作是真的强，" +
	"解说也讲得清楚，比看比赛回放有意思多了。钱不多，就是个心意，主播别省着，" +
	"该买的设备就买，该休息就休息，别老是通宵。"

// hugeSC 是极端长的 SC：用来确认面板不会因为单条消息太高就整块布局崩掉。
const hugeSC = "写点长的。第一，谢谢主播每天准点开播，这个习惯其实挺难得的；" +
	"第二，上次那个连招我照着练了一周，现在能稳定打出来了，收益很大；" +
	"第三，解说里提到的那几个细节，比如什么时候该换线、什么时候该开团，" +
	"我复盘的时候反复看了好几遍，比看攻略管用；第四，弹幕里总有人问设备，" +
	"其实真不用买太贵的，先把基础的操作和意识练起来更重要；第五，希望主播" +
	"别太拼，播到半夜三四点对身体不好，观众也希望你能长长久久地播下去；" +
	"第六，这条是专门用来测超长文本的，看看面板会不会把这条撑得比屏幕还高，" +
	"以及滚到底部的时候会不会卡住。"

// mockScript 是弹幕剧本，按顺序循环。
//
// 每一条都对着 UI 的一个分支，改面板样式时照着这张表逐条看一眼就够了。
// 分组不是随便排的——同一组里的条目互为对照，比如「纯数字」之后立刻是
// 「纯英文」，只为看出中英混排时行高有没有跳。
//
// 覆盖范围：
//
//	文字形态   极短 / 短句 / 纯英文 / 纯数字 / 带 URL / 含 emoji / 特殊字符 /
//	           首尾空格 / 超长中文 / 超长无空格英文串
//	级别       0（无徽章）/ 1 / 7 / 33 / 60
//	昵称       单字 / 带 emoji / 带空格 / 超长 / 重复出现（验证哈希色稳定）
//	平台颜色   红 / 青 / 橙
//	表情       单个内联 / 多个内联混排 / 纯图 / 纯图 gif / 纯图缺宽高
//	醒目留言   ¥1 / ¥30 / ¥100 / ¥500 长文 / ¥1000 超长文（无等级 + 超长昵称）
//
// 唯一没覆盖的是「非正方形表情」——手头两个稳定的图源都是方图，
// 硬凑宽高比会在界面上显示成被拉扁的图，反而误导。
func mockScript() []Danmu {
	square := Emote{URL: mockEmoteURL, Width: 64, Height: 64}
	// 24×24 的 gif：顺带验证 image/gif 解码器有没有注册
	animated := Emote{URL: mockEmoteGIF, Width: 24, Height: 24}
	// 宽高缺失：老表情不带尺寸，界面要按 1:1 兜底而不是除零
	sizeless := Emote{URL: mockEmoteURL}

	return []Danmu{
		// ---------------------------------------------------------- 文字形态
		{Content: "6", Nickname: "夜行的猫", Level: 14},
		{Content: "哈哈哈哈哈", Nickname: "柠檬汽水", Level: 21},
		{Content: "2333333", Nickname: "盐焗小星球", Level: 7},
		{Content: "GG", Nickname: "晚风信箱", Level: 2},
		{Content: "这波我给满分😭👌", Nickname: "橘子海", Level: 27},
		{Content: "看这个 https://live.bilibili.com/545068 笑死", Nickname: "月亮邮差", Level: 18},
		{Content: "@小明 #测试 $%^&*()_+-=[]{}|;':\",./<>?", Nickname: "雾都孤儿", Level: 12},
		{Content: "   前后各带了三个空格   ", Nickname: "路过的风", Level: 9},
		{Content: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			Nickname: "会飞的猪", Level: 33},
		{Content: longChatText, Nickname: "月亮邮差", Level: 46},

		// ------------------------------------------------ 平台原生颜色 / 等级
		{Content: "彩色弹幕 · 红", Nickname: "雾都孤儿", Level: 33, Color: "#FF2E2E"},
		{Content: "彩色弹幕 · 青", Nickname: "小满", Level: 1, Color: "#00CCFF"},
		{Content: "彩色弹幕 · 橙", Nickname: "阿澈", Level: 60, Color: "#FF6600"},

		// 没有等级：Lv 徽章整块不出现，昵称要顶到最左边
		{Content: "第一次来，请问这是谁的直播间", Nickname: "新观众"},

		// ------------------------------------------------ 昵称边界
		{Content: "一个字的名字", Nickname: "火", Level: 55},
		{Content: "名字里带空格", Nickname: "夜行 的猫", Level: 14},
		{Content: "名字带符号", Nickname: "☞一路向前☞", Level: 24},
		{Content: "名字特别长会怎样", Nickname: "这是一个刻意写得很长很长的观众昵称用来测截断", Level: 38},

		// ------------------------------------------------ 表情
		// 单个内联：正文里的占位符要和 Emotes[].Name 对上
		{Content: "笑死[dog]", Nickname: "路过的风", Level: 30,
			Emotes: []Emote{{Name: "[dog]", URL: square.URL, Width: square.Width, Height: square.Height}}},
		// 多个内联混排：前 / 中 / 后都要按顺序
		{Content: "前[dog]中[cat]后", Nickname: "半糖去冰", Level: 8,
			Emotes: []Emote{
				{Name: "[dog]", URL: square.URL, Width: square.Width, Height: square.Height},
				{Name: "[cat]", URL: square.URL, Width: square.Width, Height: square.Height},
			}},
		// 纯图：正文为空，整条就是一张图
		{Nickname: "打野的鱼", Level: 45, Emotes: []Emote{square}},
		// 纯图 gif
		{Nickname: "不吃香菜", Level: 6, Emotes: []Emote{animated}},
		// 纯图但平台没给尺寸
		{Nickname: "山与海", Level: 19, Emotes: []Emote{sizeless}},

		// -------------------------------------------------------- 醒目留言
		{Content: "1", Nickname: "阿澈", Level: 3,
			Kind: DanmuSuperChat, Price: 1, Background: "#DFE6F7"},
		{Content: "这波必须支持一下", Nickname: "打野的鱼", Level: 45,
			Kind: DanmuSuperChat, Price: 30, Background: "#E8B0D8"},
		{Content: "主播加油！", Nickname: "橘子海", Level: 52,
			Kind: DanmuSuperChat, Price: 100, Background: "#EDC08A"},
		// 长文 SC：面板那一块要能撑开几行而不挤坏金额徽章
		{Content: longSC, Nickname: "月亮邮差", Level: 46,
			Kind: DanmuSuperChat, Price: 500, Background: "#7BB8FF"},
		// 超长文 + 无等级 + 超长昵称：三个边界同时压上来
		{Content: hugeSC, Nickname: "这是给超长醒目留言配的超长观众昵称", Level: 0,
			Kind: DanmuSuperChat, Price: 1000, Background: "#C77BFF"},
	}
}

// 编译期确认 mock 覆盖了 UI 用到的全部接口。
var (
	_ Provider   = (*mockProvider)(nil)
	_ RoomInfoer = (*mockProvider)(nil)
	_ Streamer   = (*mockProvider)(nil)
	_ Danmakuer  = (*mockProvider)(nil)
)
