package ui

import (
	"encoding/json"
	"testing"
)

// 导出再导入必须是自洽的：同一份数据过一遍文件，能原样回到新 App 上。
func TestConfigRoundTrip(t *testing.T) {
	src := NewMockApp()
	// NewMockApp 会 restore 本机 state.json，测试要先清空才不依赖用户数据
	src.Follows = []FollowRecord{
		{Platform: "bilibili", RoomID: "1001", Name: "老王", Title: "直播中", Live: true, Viewers: 3200},
		{Platform: "douyu", RoomID: "77", Name: "老李"},
	}
	src.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1_1"},
	}
	src.Quality = "原画"
	src.Volume = 0.42

	data, err := json.Marshal(src.buildConfigPayload())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}

	parsed, err := parseConfigFile(data)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}

	dst := NewMockApp()
	dst.Follows = nil
	dst.Subs = nil
	sum := dst.applyConfig(parsed)
	if sum.Follows != 2 || sum.Subs != 1 {
		t.Fatalf("导入 %+v，期望 2 位关注 1 个订阅", sum)
	}
	if len(dst.Follows) != 2 || dst.Follows[0].RoomID != "1001" {
		t.Fatalf("关注没有正确落地：%+v", dst.Follows)
	}
	if dst.Quality != "原画" || dst.Volume != 0.42 {
		t.Errorf("播放设置没有覆盖：quality=%q volume=%v", dst.Quality, dst.Volume)
	}

	// 再导一次：已经有的不该重复加
	again := dst.applyConfig(parsed)
	if again.Follows != 0 || again.Subs != 0 {
		t.Errorf("增量合并不幂等：%+v", again)
	}
	if len(dst.Follows) != 2 {
		t.Errorf("重复导入后关注变成 %d 条", len(dst.Follows))
	}
}

// DTV 导出的配置文件也要能读：字段名和平台大小写都不一样。
func TestParseDTVConfig(t *testing.T) {
	entries := map[string]string{
		"followedStreamers": `[{"id":"555","platform":"BILIBILI","nickname":"老王",` +
			`"roomTitle":"老王的直播间","avatarUrl":"https://a/b.png",` +
			`"liveStatus":"LIVE"}]`,
		"dtv_custom_categories_v1": `[{"platform":"DOUYU","cate1Name":"网游",` +
			`"cate2Id":"1_1","cate2Name":"英雄联盟"}]`,
		"dtv_danmu_preferences_v1": `{"enabled":true,"settings":{"color":"#ff0000",` +
			`"strokeColor":"#000000","fontSize":"22px","duration":8000,` +
			`"area":0.5,"opacity":0.8,"colorful":true}}`,
		"theme_preference":           "daylight",
		"dtv_player_volume_v1":       "0.6",
		"BILIBILI_preferred_quality": "高清",
	}
	raw, err := json.Marshal(map[string]any{
		"kind":    dtvConfigKind,
		"version": 1,
		"entries": entries,
	})
	if err != nil {
		t.Fatal(err)
	}

	d, err := parseConfigFile(raw)
	if err != nil {
		t.Fatalf("解析 dtv-config 失败：%v", err)
	}

	if len(d.Follows) != 1 {
		t.Fatalf("关注解析出 %d 条", len(d.Follows))
	}
	f := d.Follows[0]
	if f.Platform != "bilibili" || f.RoomID != "555" || f.Name != "老王" ||
		f.Title != "老王的直播间" || f.AvatarURL != "https://a/b.png" || !f.Live {
		t.Errorf("关注字段没对上：%+v", f)
	}

	if len(d.Subs) != 1 {
		t.Fatalf("订阅解析出 %d 条", len(d.Subs))
	}
	s := d.Subs[0]
	if s.PlatformID != "douyu" || s.Group != "网游" || s.Name != "英雄联盟" ||
		s.CategoryID != "1_1" || s.Platform != "斗鱼" {
		t.Errorf("订阅字段没对上：%+v", s)
	}

	if d.Palette != "daylight" {
		t.Errorf("主题 %q", d.Palette)
	}
	if d.Volume == nil || *d.Volume != 0.6 {
		t.Errorf("音量 %v", d.Volume)
	}
	if d.Quality != "高清" {
		t.Errorf("清晰度 %q", d.Quality)
	}
	if d.Danmu == nil {
		t.Fatal("弹幕设置没解析出来")
	}
	if d.Danmu.Font != 22 || d.Danmu.DurMs != 8000 || d.Danmu.Color[0] != 255 {
		t.Errorf("弹幕字段没对上：%+v", *d.Danmu)
	}
	if d.DanmuOn == nil || !*d.DanmuOn {
		t.Error("弹幕开关没带过来")
	}
}

// 认不出来的文件要明确报错，不能静默合并成空结果。
func TestParseConfigRejectsUnknownKind(t *testing.T) {
	if _, err := parseConfigFile([]byte(`{"kind":"something-else"}`)); err == nil {
		t.Error("陌生 kind 没有报错")
	}
	if _, err := parseConfigFile([]byte(`not json`)); err == nil {
		t.Error("非 JSON 没有报错")
	}
}
