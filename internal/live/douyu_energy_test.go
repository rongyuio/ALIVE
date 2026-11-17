package live

import (
	"strconv"
	"testing"
	"time"
)

// douyuVoiceTrltSample 是 2026-11-17 从斗鱼真实房间抓到的 voice_trlt 原文
// （已去掉 12 字节包头和结尾  ，和 handlePacket 交给 douyuParseSTT 的部分一致）。
//
// 抓法：连 danmuproxy 的 8601，loginreq + joingroup 之后 dump 每包原文。
// 这条来自房间 10351160，发送者 苹果汽水儿，金额档 1000。
const douyuVoiceTrltSample = `danmuShowSwitch@=1/mtype@=2/totalNum@=0/type@=voice_trlt/list@=vrId@AA=2122660338531831808@ASvocUrl@AA=@ASuat@AA=https:@AAS@AASapic.douyucdn.cn@AASupload@AASavatar_v3@AAS202501@AASf94d202e3bcd4cb6a11974d5a7fcf957_big.jpg@ASstime@AA=30@ASetimeV2@AA=1794916710@AScontent@AA=小融融天天开心@ASvocNickUrl@AA=@ASuid@AA=677998469@ASprice@AA=1000@ASetime@AA=1794916710@ASacptime@AA=1794916680@ASun@AA=苹果汽水儿@ASmf@AA=0@ASisVoc@AA=0@ASrealPrice@AA=1000@AS@S/isShow@=1/`

// 高能弹幕是 voice_trlt，不是 chatmsg：正文、昵称、金额都在 list 里，
// 而且 list 被转义了两层。
func TestDouyuVoiceBarrage(t *testing.T) {
	fields := douyuParseSTT(douyuVoiceTrltSample)
	if got := fields["type"]; got != "voice_trlt" {
		t.Fatalf("样本的 type 是 %q，不是 voice_trlt", got)
	}

	got := douyuVoiceBarrages(fields["list"])
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条，期望 1 条", len(got))
	}
	d := got[0]

	if d.Nickname != "苹果汽水儿" {
		t.Errorf("昵称 = %q", d.Nickname)
	}
	if d.Content != "小融融天天开心" {
		t.Errorf("正文 = %q", d.Content)
	}
	// price 与 realPrice 都是 1000，除以 100 才是元。
	if d.Price != 10 {
		t.Errorf("金额 = %d 元，期望 10", d.Price)
	}
	if d.Kind != DanmuSuperChat {
		t.Errorf("种类 = %q，期望 %q", d.Kind, DanmuSuperChat)
	}
	if d.ID != "douyu:vr:2122660338531831808" {
		t.Errorf("id = %q", d.ID)
	}
	if want := time.Unix(1794916680, 0); !d.At.Equal(want) {
		t.Errorf("时间 = %v，期望 %v（acptime）", d.At, want)
	}
	// 样本里没有 danmucr，按平台缺省走蓝色头部。
	if d.Background != "#385fac" {
		t.Errorf("背景色 = %q，期望 #385fac", d.Background)
	}
}

// 高能弹幕的头部颜色由 danmucr 决定：1 蓝、2 红、3 黄（网页端
// HighEnergyV2Barrage 的 is-blue / is-red / is-yellow）。
func TestDouyuVoiceBarrageColor(t *testing.T) {
	cases := map[int]string{0: "#385fac", 1: "#385fac", 2: "#d45954", 3: "#f9af1b"}
	for idx, want := range cases {
		list := "vrId@A=1@Sun@A=小明@Scontent@A=测试@SrealPrice@A=1000@Sdanmucr@A=" + strconv.Itoa(idx)
		got := douyuVoiceBarrages(list)
		if len(got) != 1 {
			t.Fatalf("danmucr=%d 解析出 %d 条", idx, len(got))
		}
		if got[0].Background != want {
			t.Errorf("danmucr=%d 背景色 = %q，期望 %q", idx, got[0].Background, want)
		}
	}
}

// 空 list 和垃圾数据都不能 panic，也不能产出弹幕。
func TestDouyuVoiceBarrageIgnoresGarbage(t *testing.T) {
	for _, list := range []string{"", "//", "vrId@A=", "content@A=没有昵称"} {
		if got := douyuVoiceBarrages(list); len(got) != 0 {
			t.Errorf("list=%q 解析出 %d 条，期望 0 条", list, len(got))
		}
	}
}

// 服务端会把整个 list 重发，同一个 vrId 只上屏一次。
func TestDouyuVoiceBarrageDedupesByVRID(t *testing.T) {
	s := &douyuDanmaku{seenVR: map[string]struct{}{}}
	if !s.firstSight("douyu:vr:1") {
		t.Fatal("第一次见到就判成重复")
	}
	if s.firstSight("douyu:vr:1") {
		t.Fatal("第二次见到仍判成新的，会重复上屏")
	}
	if !s.firstSight("douyu:vr:2") {
		t.Fatal("另一条被判成重复")
	}
}

// 到了 list 这一层，正文里的 / 还是 @S、@ 还是 @A，必须再解一次；
// 不解的话正文会原样带着 @S 上屏。URL 字段比正文多一层（服务端先自己
// 转义过），所以这里对每个值解两次——对正文是幂等的，对 URL 才够。
func TestDouyuVoiceBarrageUnescapesContent(t *testing.T) {
	// 值里的 / 在 list 这层是 @AS、@ 是 @AA（先值转义一次，再整条记录转义一次），
	// 和分隔符 @S / 键值符 @A= 区分得开。
	list := "vrId@A=1@Sun@A=爱@AA@AA的@AS人@Scontent@A=1@AS2@AA@AA3@SrealPrice@A=1000"
	got := douyuVoiceBarrages(list)
	if len(got) != 1 {
		t.Fatalf("解析出 %d 条，期望 1 条", len(got))
	}
	if got[0].Nickname != "爱@@的/人" {
		t.Errorf("昵称 = %q，期望 %q", got[0].Nickname, "爱@@的/人")
	}
	if got[0].Content != "1/2@@3" {
		t.Errorf("正文 = %q，期望 %q", got[0].Content, "1/2@@3")
	}
}
