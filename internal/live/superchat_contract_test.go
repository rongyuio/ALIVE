package live

import "testing"

// SC 的归一化契约：不管平台叫什么，进 Danmu 时只有四个字段有语义——
// Price（元）、Nickname、Content、Background（平台色，可空）。
// 这条测试看住两个有 SC 能力的 provider 都按这个形状产出。
func TestSuperChatContract(t *testing.T) {
	t.Run("B 站", func(t *testing.T) {
		body := []byte(`{"cmd":"SUPER_CHAT_MESSAGE","data":{"message":"小保底没歪","price":30,"background_color":"#DFE6F7","user_info":{"uname":"小明","user_level":20}}}`)

		var got Danmu
		s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
		s.handleCommand(body)

		assertSuperChat(t, got)
		if got.Background != "#DFE6F7" {
			t.Errorf("背景色 = %q，期望平台下发的 #DFE6F7", got.Background)
		}
	})

	t.Run("斗鱼", func(t *testing.T) {
		fields := douyuParseSTT(douyuVoiceTrltSample)
		got := douyuVoiceBarrages(fields["list"])
		if len(got) != 1 {
			t.Fatalf("解析出 %d 条", len(got))
		}
		assertSuperChat(t, got[0])
		if got[0].Background != "#385fac" {
			t.Errorf("背景色 = %q，期望 danmucr 缺省的 #385fac", got[0].Background)
		}
	})

	t.Run("虎牙", func(t *testing.T) {
		list := huyaParseHeadlines(huyaHeadlineSample())
		if len(list) != 1 {
			t.Fatalf("解析出 %d 条", len(list))
		}
		got := huyaHeadlineDanmu(list[0])
		assertSuperChat(t, got)
		if got.Background != "#385fac" {
			t.Errorf("背景色 = %q，期望该档位的 iLightNormalBgColor", got.Background)
		}
	})
}

func assertSuperChat(t *testing.T, d Danmu) {
	t.Helper()
	if d.Kind != DanmuSuperChat {
		t.Fatalf("种类 = %q，期望 %q", d.Kind, DanmuSuperChat)
	}
	if d.Price <= 0 {
		t.Errorf("金额 = %d，SC 必须带金额", d.Price)
	}
	if d.Nickname == "" {
		t.Error("昵称为空，SC 必须带昵称")
	}
	if d.Content == "" {
		t.Error("正文为空，SC 必须带正文")
	}
}
