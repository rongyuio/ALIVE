package live

import "testing"

// 荣耀等级在 info[16][0]，不是 info[4][0]。
//
// 这条用例的字段选择是拿网页端对出来的：把网页聊天区每条消息的荣耀等级
// 勋章图按 uid 和我们抓到的报文对齐，57 个重叠用户里 info[16][0] 与勋章图
// 零冲突，info[4][0] 有 9 张图对应多个值。之前用 info[4][0]，界面上每个人
// 的等级都和网页对不上。
func TestBilibiliWealthLevelUsesInfo16(t *testing.T) {
	body := []byte(`{"cmd":"DANMU_MSG","info":[[0,1,25,16777215,1794920027891,1794915903,0,"c53b1b63",0,0,0,"",0,"{}","{}",{"extra":"{}"}],"[dog]",[514469537,"某人",0,0,0,10000,1,""],[28,"某牌","某主播",1914112608,398668,"",0], [21,0,5805790,">50000",3],["",""],0,0,null,{"ct":"4B74FCF8","ts":1794920027},0,0,null,null,0,832,[34],null]}`)

	if got := biliWealthLevel(body); got != 34 {
		t.Fatalf("荣耀等级 = %d，期望 34（info[16][0]）；info[4][0] 是 21，用错字段就会得到 21", got)
	}
}

// 没有 info[16] 时退回 extra.user.wealth.level，再没有就是 0。
func TestBilibiliWealthLevelFallback(t *testing.T) {
	noLevel := []byte(`{"info":[[0,1,25,0,0,0,0,"",0,0,0,"",0,"{}","{}",{"extra":"{}"}],"x",[1,"n",0,0,0,1,1,""],[0,"","",0,0,"",0],[99,0,0,"",0]]}`)
	if got := biliWealthLevel(noLevel); got != 0 {
		t.Errorf("没有等级字段时 = %d，期望 0（不能退回 info[4][0]）", got)
	}

	withWealth := []byte(`{"info":[[0,1,25,0,0,0,0,"",0,0,0,"",0,"{}","{}",{"extra":"{\"user\":{\"wealth\":{\"level\":17}}}"}],"x",[1,"n",0,0,0,1,1,""],[0,"","",0,0,"",0],[99,0,0,"",0]]}`)
	if got := biliWealthLevel(withWealth); got != 17 {
		t.Errorf("兜底 = %d，期望 17", got)
	}
}
