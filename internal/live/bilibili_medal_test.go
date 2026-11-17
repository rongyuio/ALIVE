package live

import "testing"

// biliDanmuWithMedal 是一条带头像框的 DANMU_MSG（2026-11-18 从德云色直播间
// 抓的真实结构，昵称/uid 换成占位值）。
//
// v2 配色是网页端真正在用的那一套：medal.color 那组老字段同时存在，
// 但它是旧版暗色（这里是 #06154c），照它画牌子会整屏发灰。
const biliDanmuWithMedal = `{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1794941211504,1611297118,0,"2a2b96cb",0,0,0,"",0,"{}","{}",{"extra":"{}","mode":0,"show_player_type":0,"user":{"medal":{"color":398668,"color_border":398668,"color_end":6850801,"color_start":398668,"is_light":1,"level":28,"name":"德云色","ruid":8739477,"v2_medal_color_border":"#3FB4F699","v2_medal_color_end":"#3FB4F699","v2_medal_color_level":"#3FB4F6E6","v2_medal_color_start":"#3FB4F699","v2_medal_color_text":"#FFFFFF"}}},{"activity_identity":"","activity_source":0,"not_show":0},0],"2分钟，玩不了？",[37498316,"观众甲",0,0,0,10000,1,""],[28,"德云色","主播",545068,398668,"",0,398668,398668,6850801,0,1,8739477],[8,0,9868950,">50000",0],["",""],0,0,null,{"ct":"6EA39BA4","ts":1794941211},0,0,null,null,0,390,[23],null]}`

// biliDanmuLegacyMedal 只有老字段、没有 v2 配色（早期协议/其它消息源）。
const biliDanmuLegacyMedal = `{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1794941211504,1611297118,0,"2a2b96cb",0,0,0,"",0,"{}","{}",{"extra":"{}","mode":0,"show_player_type":0,"user":{"medal":{"color":398668,"is_light":1,"level":28,"name":"德云色"}}},0],"旧协议",[37498316,"观众乙",0,0,0,10000,1,""],[28,"德云色","主播",545068,398668,"",0,398668,398668,6850801,0,1,8739477],[8,0,9868950,">50000",0],["",""],0,0,null,{"ct":"6EA39BA4","ts":1794941211},0,0,null,null,0,390,[23],null]}`

// 粉丝牌底色取 v2 配色，自带 alpha，和网页端的牌子一致。
func TestBiliMedalColorV2(t *testing.T) {
	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand([]byte(biliDanmuWithMedal))

	if got.Medal.Name != "德云色" || got.Medal.Level != 28 {
		t.Fatalf("勋章 = %q Lv.%d", got.Medal.Name, got.Medal.Level)
	}
	if got.Medal.Color != "#3FB4F699" {
		t.Errorf("勋章底色 = %q，期望 v2 的 #3FB4F699", got.Medal.Color)
	}
	if got.Medal.ColorEnd != "#3FB4F699" {
		t.Errorf("渐变终点 = %q，期望 #3FB4F699", got.Medal.ColorEnd)
	}
}

// 没有 v2 配色时退回老字段，并把 6 位色补成网页端的 60%。
func TestBiliMedalColorLegacyFallback(t *testing.T) {
	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand([]byte(biliDanmuLegacyMedal))

	if got.Medal.Color != "#06154c99" {
		t.Errorf("勋章底色 = %q，期望老字段补 alpha 的 #06154c99", got.Medal.Color)
	}
}

// 平台给了坏值不能让 ui.Hex 拿到（它会 panic），一律当没有配色。
func TestBiliMedalColorRejectsJunk(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",0,"{}","{}",{"user":{"medal":{"level":28,"name":"德云色","v2_medal_color_start":"red"}}},0],"x",[1,"观众丙",0,0,0,10000,1,""],[28,"德云色","主播",545068,0,"",0,0,0,0,0,1,0],[8,0,0,"",0],["",""]],0,0]}'`)
	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	if got.Medal.Color != "" || got.Medal.ColorEnd != "" {
		t.Errorf("坏色值应被丢弃，实际 %q / %q", got.Medal.Color, got.Medal.ColorEnd)
	}
}
