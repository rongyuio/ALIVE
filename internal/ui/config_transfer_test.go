package ui

import (
	"encoding/json"
	"slices"
	"testing"
)

// 导出再导入必须是自洽的：同一份数据过一遍文件，能原样回到新 App 上。
func TestConfigRoundTrip(t *testing.T) {
	src := NewMockApp()
	// NewMockApp 会 restore 本机 state.json，测试要先写死想要的字段
	src.Follows = []FollowRecord{
		{Platform: "bilibili", RoomID: "1001", Name: "老王", Title: "直播中", Live: true, Viewers: 3200},
		{Platform: "douyu", RoomID: "77", Name: "老李"},
	}
	src.Subs = []Subscription{
		{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟", CategoryID: "1_1"},
	}
	src.WatchRooms = []Streamer{{Platform: "douyu", RoomID: "77", Name: "老李"}}
	src.watch.init()
	src.watch.volumes["douyu|77"] = 0.25
	focusForTest(src, Streamer{Platform: "bilibili", RoomID: "1001", Name: "老王"})
	src.setRoomQuality("bilibili|1001", "原画")
	src.setRoomLine("douyu|77", "线路二", "line-2")
	src.setVolume(0.42)
	src.ThemeMode = themeModeDark
	src.DanmuFilters = []string{"加微信"}
	src.FollowsSectionOrder = []string{sectionIDSubs, sectionIDStreamers}

	data, err := json.Marshal(src.buildConfigFile())
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
	dst.WatchRooms = nil
	dst.DanmuFilters = nil
	dst.ThemeMode = themeModeSystem
	focusForTest(dst, Streamer{Platform: "bilibili", RoomID: "1001", Name: "老王"})
	sum := dst.applyConfig(parsed)
	if sum.Follows != 2 || sum.Subs != 1 || sum.Watch != 1 || sum.Filters != 1 {
		t.Fatalf("导入 %+v，期望 2 位关注、1 个订阅、1 个观测墙、1 个屏蔽词", sum)
	}
	if len(dst.Follows) != 2 || dst.Follows[0].RoomID != "1001" {
		t.Fatalf("关注没有正确落地：%+v", dst.Follows)
	}
	if got := dst.qualityFor("bilibili|1001"); got != "原画" {
		t.Errorf("清晰度没有覆盖：%q", got)
	}
	if c := dst.roomChoiceOf("douyu|77"); c.Line != "线路二" || c.LineID != "line-2" {
		t.Errorf("线路没有覆盖：%+v", c)
	}
	if len(dst.WatchRooms) != 1 || watchKey(dst.WatchRooms[0]) != "douyu|77" {
		t.Fatalf("观测墙没有落地：%+v", dst.WatchRooms)
	}
	if v := dst.watchVolume("douyu|77"); v != 0.25 {
		t.Errorf("观测墙音量 %v，期望 0.25", v)
	}
	if dst.focusVolume() != 0.42 {
		t.Errorf("全局音量 %v，期望 0.42", dst.focusVolume())
	}
	if dst.ThemeMode != themeModeDark {
		t.Errorf("日夜模式 %q，期望 %q", dst.ThemeMode, themeModeDark)
	}
	if !slices.Contains(dst.DanmuFilters, "加微信") {
		t.Errorf("屏蔽词没有落地：%v", dst.DanmuFilters)
	}
	if got, want := dst.followsSectionOrder(), []string{sectionIDSubs, sectionIDStreamers, sectionIDWatch}; !equalStrings(got, want) {
		t.Errorf("一级分区顺序 %v，期望 %v", got, want)
	}

	// 再导一次：已经有的不该重复加
	again := dst.applyConfig(parsed)
	if again.Follows != 0 || again.Subs != 0 || again.Watch != 0 || again.Filters != 0 {
		t.Errorf("增量合并不幂等：%+v", again)
	}
	if len(dst.Follows) != 2 || len(dst.WatchRooms) != 1 {
		t.Errorf("重复导入后关注 %d 条、观测墙 %d 个", len(dst.Follows), len(dst.WatchRooms))
	}
}

// 导出文件的顶层就是我们的领域划分，不许再出现外部客户端那层
// kind / version / source / data 信封。
func TestConfigFileHasNoEnvelope(t *testing.T) {
	app := NewMockApp()
	data, err := json.Marshal(app.buildConfigFile())
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	for _, banned := range []string{"kind", "version", "data", "entries", "source"} {
		if _, ok := top[banned]; ok {
			t.Errorf("导出文件里还有 %q 这层包装", banned)
		}
	}
	if got := string(top["app"]); got != `"ALIVE"` {
		t.Errorf("app 标识是 %s，期望 \"ALIVE\"", got)
	}
	want := []string{"theme", "follows", "subscriptions", "watchWall", "rooms", "player", "danmu", "platformOrder", "followsSections"}
	for _, key := range want {
		if _, ok := top[key]; !ok {
			t.Errorf("导出文件缺少 %q 段", key)
		}
	}
}

// 导入不许弄丢本机已有的观测墙和别的房间的清晰度：列表只加不删，
// 房间记录按房间合并。
func TestImportMergesListsAndRoomChoices(t *testing.T) {
	app := NewMockApp()
	app.WatchRooms = []Streamer{{Platform: "huya", RoomID: "9", Name: "老张"}}
	app.watch.init()
	app.watch.volumes["huya|9"] = 0.3
	app.setRoomQuality("huya|9", "蓝光")

	app.applyConfig(configFile{
		WatchWall: []watchRecord{
			{Platform: "huya", RoomID: "9", Name: "老张"},
			{Platform: "douyu", RoomID: "77", Name: "老李"},
		},
		Rooms: map[string]roomChoice{
			"bilibili|1001": {Quality: "超清"},
		},
	})

	if len(app.WatchRooms) != 2 || watchKey(app.WatchRooms[0]) != "huya|9" || watchKey(app.WatchRooms[1]) != "douyu|77" {
		t.Fatalf("观测墙合并结果不对：%+v", app.WatchRooms)
	}
	if got := app.qualityFor("huya|9"); got != "蓝光" {
		t.Errorf("本机别的房间被改掉了：%q", got)
	}
	if got := app.qualityFor("bilibili|1001"); got != "超清" {
		t.Errorf("文件里的房间没写进去：%q", got)
	}
}

// 认不出来的文件要明确报错，不能静默合并成空结果。
func TestParseConfigRejectsUnknownFile(t *testing.T) {
	cases := map[string]string{
		"没有 app": `{"follows":[]}`,
		"别家 app": `{"app":"DTV"}`,
		"非 JSON": `not json`,
		"字段类型错":  `{"app":"ALIVE","player":{"volume":"大声"}}`,
		"数字溢出":   `{"app":"ALIVE","player":{"volume":1e40}}`,
	}
	for name, raw := range cases {
		if _, err := parseConfigFile([]byte(raw)); err == nil {
			t.Errorf("%s 没有报错", name)
		}
	}
}
