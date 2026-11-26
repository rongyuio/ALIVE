package live

import "testing"

// TestDouyuPickRateEmpty 盯住空档位表不 panic。
//
// 旧实现把 len(rates) > 0 的判断写在用了 rates[0] 之后：上游没下发
// multirates（air 房间、异常响应）时，空表一进来日志那行的 rates[0] 就
// panic。空表必须先返回兜底档。
func TestDouyuPickRateEmpty(t *testing.T) {
	got := douyuPickRate(nil, "原画")
	if got.Name != "默认" || got.Rate != -1 {
		t.Fatalf("空档位表应返回兜底档 默认/-1，实际 %+v", got)
	}
	if got := douyuPickRate([]douyuRate{}, "原画"); got.Rate != -1 {
		t.Fatalf("空表（非 nil）也应返回兜底档，实际 %+v", got)
	}
}

// 非空表仍按菜单标签匹配，没命中退回第一项。
func TestDouyuPickRateMatchesLabel(t *testing.T) {
	rates := []douyuRate{
		{Name: "原画", Rate: 0},
		{Name: "蓝光4M", Rate: 4},
	}
	if got := douyuPickRate(rates, "蓝光4M"); got.Rate != 4 {
		t.Errorf("按标签选档 = %+v，期望 rate=4", got)
	}
	if got := douyuPickRate(rates, "不存在的档"); got.Rate != 0 {
		t.Errorf("未命中应退回第一项，实际 %+v", got)
	}
	if got := douyuPickRate(rates, ""); got.Rate != 0 {
		t.Errorf("空标签应取默认档，实际 %+v", got)
	}
}
