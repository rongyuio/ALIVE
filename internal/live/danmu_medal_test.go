package live

import (
	"strings"
	"testing"

	blivedmmsg "github.com/Akegarasu/blivedm-go/message"
)

// 斗鱼 chatmsg：level 是用户等级，bl/bnn 是粉丝牌等级/名称，两者不能混。
func TestDouyuChatMedal(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantLevel int
		wantMedal Medal
	}{
		{
			name:      "有粉丝牌",
			body:      "type@=chatmsg/nn@=小明/level@=13/bl@=21/bnn@=骚尖勾/txt@=你好/",
			wantLevel: 13,
			wantMedal: Medal{Name: "骚尖勾", Level: 21},
		},
		{
			name:      "没有粉丝牌",
			body:      "type@=chatmsg/nn@=小明/level@=1/bl@=0/bnn@=/txt@=你好/",
			wantLevel: 1,
			wantMedal: Medal{},
		},
	}
	for _, tc := range cases {
		var got Danmu
		s := &douyuDanmaku{roomID: "1", onMessage: func(d Danmu) { got = d }}
		packet := append(make([]byte, 12), []byte(tc.body)...)
		packet = append(packet, 0)
		s.handlePacket(nil, packet)

		if got.Content != "你好" {
			t.Fatalf("%s：正文 = %q", tc.name, got.Content)
		}
		if got.Level != tc.wantLevel {
			t.Errorf("%s：用户等级 = %d，期望 %d", tc.name, got.Level, tc.wantLevel)
		}
		if got.Medal != tc.wantMedal {
			t.Errorf("%s：粉丝牌 = %+v，期望 %+v", tc.name, got.Medal, tc.wantMedal)
		}
	}
}

// protobuf 的小工具：length-delimited 字段。
func pbBytesField(num int, payload []byte) []byte {
	out := []byte{byte(num<<3 | douyinPBWireBytes), byte(len(payload))}
	return append(out, payload...)
}

// 抖音粉丝团在 User.f21 的重复勋章里：fansclub 图标 + f8.f3 等级。
// 用户荣誉等级的勋章长得一样，不能认错。
func TestDouyinFansClub(t *testing.T) {
	fansclub := append(
		pbBytesField(1, []byte("https://p3-webcast.douyinpic.com/img/webcast/fansclub_level_v6_12.png")),
		pbBytesField(8, []byte{0x18, 12})..., // f3 varint = 12
	)
	grade := append(
		pbBytesField(1, []byte("https://p3-webcast.douyinpic.com/img/webcast/new_user_grade_level_v1_15.png")),
		pbBytesField(8, []byte{0x18, 15})...,
	)
	user := []douyinPBField{
		{Num: 3, Wire: douyinPBWireBytes, Bytes: []byte("小明")},
		{Num: 21, Wire: douyinPBWireBytes, Bytes: grade},
		{Num: 21, Wire: douyinPBWireBytes, Bytes: fansclub},
	}
	if got := douyinFansClub(user); got.Name != "粉丝团" || got.Level != 12 {
		t.Fatalf("粉丝团 = %+v，期望 粉丝团/12", got)
	}

	// 只有荣誉等级、没有粉丝团时不能编一个出来。
	if got := douyinFansClub(user[:1]); got != (Medal{}) {
		t.Fatalf("没有粉丝团时解析出 %+v", got)
	}
}

// B 站 info[2][16][0] 是用户等级，info[3] 是粉丝牌。以前把勋章等级
// 当成用户等级填进了 Level，面板上的「Lv.24」其实是粉丝牌等级。
func TestBilibiliChatLevelAndMedal(t *testing.T) {
	// 等级取荣耀等级（info[16][0]），不是 info[4][0]，也不是主站 UL。
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",0,"{}","{}",{}],"你好",[123,"小明"],[24,"德云色","主播",545068,0],[0,0,9868950,">50000",0],0,0,0,0,0,0,0,0,0,0,0,[12]]}`)

	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	if got.Nickname != "小明" || got.Content != "你好" {
		t.Fatalf("昵称/正文 = %q/%q", got.Nickname, got.Content)
	}
	if got.Level != 12 {
		t.Errorf("用户等级 = %d，期望 12（荣耀等级，不是粉丝牌也不是主站 UL）", got.Level)
	}
	if got.Medal != (Medal{Name: "德云色", Level: 24}) {
		t.Errorf("粉丝牌 = %+v，期望 德云色/24", got.Medal)
	}
}

// 主播收藏集表情：和纯图表情共用 info[0][13]，emoticon_unique 是
// upower_[花礼Harei收藏集表情包_prpr] 这种带前缀的全名，正文占位符就是
// 去掉前缀的那一段。只当成纯图会把占位符留在正文里。
func TestBilibiliGarbEmote(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",1,{"bulge_display":1,"emoticon_unique":"upower_[花礼Harei收藏集表情包_何意味]","height":20,"is_dynamic":0,"url":"https://i0.hdslb.com/bfs/garb/x.png","width":20},"{}",{}],"[花礼Harei收藏集表情包_何意味]",[1,"小明"],[0,""]]}`)

	var d blivedmmsg.Danmaku
	d.Parse(body)
	emotes := biliEmotes(body, d)
	if len(emotes) != 1 {
		t.Fatalf("收藏集表情解析出 %d 条：%+v", len(emotes), emotes)
	}
	if emotes[0].Name != "[花礼Harei收藏集表情包_何意味]" || emotes[0].URL != "https://i0.hdslb.com/bfs/garb/x.png" {
		t.Fatalf("收藏集表情 = %+v", emotes[0])
	}

	// 整条消息就是这张贴纸：正文清空，和房间表情走同一条纯图分支。
	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)
	if got.Content != "" || len(got.Emotes) != 1 {
		t.Fatalf("收藏集表情弹幕 = %q / %+v，应清空正文只留贴纸", got.Content, got.Emotes)
	}

	// 普通纯图表情没有占位符名，Name 必须为空，交给纯图分支渲染。
	plain := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",1,{"emoticon_unique":"emoji_224","height":20,"url":"https://i0.hdslb.com/bfs/live/y.png","width":20},"{}",{}],"",[1,"小明"],[0,""]]}`)
	var pd blivedmmsg.Danmaku
	pd.Parse(plain)
	pe := biliEmotes(plain, pd)
	if len(pe) != 1 || pe[0].Name != "" {
		t.Fatalf("纯图表情 = %+v，Name 应该为空", pe)
	}
}

// 平台表情表：斗鱼存文件名（拼固定前缀），抖音存完整签名 URL。
func TestPlatformEmote(t *testing.T) {
	if e, ok := PlatformEmote(Douyu, "[财神]"); !ok || !strings.HasPrefix(e.URL, douyuEmoteBase) || !strings.HasSuffix(e.URL, ".png") {
		t.Fatalf("斗鱼表情 = %+v,%v", e, ok)
	}
	if e, ok := PlatformEmote(Douyin, "[捂脸]"); !ok || !strings.HasPrefix(e.URL, "https://p3-pc-sign.douyinpic.com/") {
		t.Fatalf("抖音表情 = %+v,%v", e, ok)
	}
	if _, ok := PlatformEmote(Douyu, "[不存在的表情]"); ok {
		t.Fatal("表里没有的表情不该查出来")
	}
	if _, ok := PlatformEmote(Bilibili, "[偷笑]"); ok {
		t.Fatal("B 站表情走协议，不该命中基础表")
	}
}

// 醒目留言的粉丝牌在原始 body 的 data.medal_info，不在 blivedm 的
// SuperChat.UserInfo 里。真实样本（2026-11-17，B 站二游区）：
// {"data":{"medal_info":{"medal_name":"哎妹妹","medal_level":23}}}
func TestBilibiliSuperChatMedal(t *testing.T) {
	body := []byte(`{"cmd":"SUPER_CHAT_MESSAGE","data":{"medal_info":{"medal_name":"哎妹妹","medal_level":23},"message":"小保底没歪","price":2,"user_info":{"uname":"小明","user_level":20}}}`)

	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	if got.Kind != DanmuSuperChat {
		t.Fatalf("不是 SC：%+v", got)
	}
	if got.Medal != (Medal{Name: "哎妹妹", Level: 23}) {
		t.Fatalf("SC 粉丝牌 = %+v，期望 哎妹妹/23", got.Medal)
	}
}

// 房间表情（主播自建表情包）是贴纸弹幕：dm_type=1，正文就是表情自己的
// 名字（「哇哈哈」「赞！」），不带方括号，图片在 info[0][13]，
// emoticon_unique 是 room_<房号>_<id>。名字匹配不到 [占位符]，如果照原样
// 当文字画，面板上只剩「哇哈哈」——网页端显示的是那张图。
//
// 真实样本（2026-11-17，B 站 6154037）：
// {"info":[[0,1,25,...,1,{"emoticon_unique":"room_6154037_16291",
//
//	"url":"http://i0.hdslb.com/bfs/live/xxx.png","width":162,"height":162},...],
//	"哇哈哈",[1,"小明"],[0,""]]}
func TestBilibiliRoomEmote(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",1,{"bulge_display":1,"emoticon_unique":"room_6154037_16291","height":162,"in_player_area":1,"is_dynamic":0,"url":"http://i0.hdslb.com/bfs/live/room.png","width":162},"{}",{}],"哇哈哈",[1,"小明"],[0,""]]}`)

	var got Danmu
	s := &biliDanmaku{roomID: "6154037", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	if len(got.Emotes) != 1 || got.Emotes[0].URL != "https://i0.hdslb.com/bfs/live/room.png" {
		t.Fatalf("房间表情 = %+v", got.Emotes)
	}
	// 正文必须清空，交给纯图分支渲染；否则表情名字会被当文字画出来。
	if got.Content != "" {
		t.Fatalf("正文 = %q，房间表情不该留下名字文字", got.Content)
	}
	if got.Emotes[0].Name != "" {
		t.Fatalf("房间表情的 Name = %q，应留空走纯图", got.Emotes[0].Name)
	}
}

// 普通文字弹幕（dm_type=0）不受房间表情那套影响。
func TestBilibiliTextUnaffected(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",0,"{}","{}",{}],"哇哈哈",[1,"小明"],[0,""]]}`)

	var got Danmu
	s := &biliDanmaku{roomID: "6154037", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	if got.Content != "哇哈哈" || len(got.Emotes) != 0 {
		t.Fatalf("普通文字弹幕被改动了：content=%q emotes=%+v", got.Content, got.Emotes)
	}
}
