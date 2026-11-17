package live

import "testing"

// B 站勋章信息里带底色（info[3][4] 的整数 RGB）。新版消息有 v2 配色时用
// v2（见 bilibili_medal_test.go）；这里是没有 v2 的老消息，退回整数色并
// 补上网页端的 60% 透明度。
func TestBilibiliMedalColor(t *testing.T) {
	// 0x2d0855 = 2951253
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,0,0,0,"",0,0,0,"",0,"{}","{}",{}],"你好",[123,"小明"],[24,"德云色","主播",545068,2951253]]}`)

	var got Danmu
	s := &biliDanmaku{roomID: "545068", onMessage: func(d Danmu) { got = d }}
	s.handleCommand(body)

	want := Medal{Name: "德云色", Level: 24, Color: "#2d085599", ColorEnd: "#2d085599"}
	if got.Medal != want {
		t.Fatalf("粉丝牌 = %+v，期望 %+v", got.Medal, want)
	}
}
