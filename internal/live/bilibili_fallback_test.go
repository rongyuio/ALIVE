package live

import "testing"

// 目标档解析不出地址时必须往下降档，而不是直接报「这一档没有可播放的地址」。
//
// 匿名会话下服务端对 qn=10000 仍回 current_qn=250，biliTierURLs 会把这档
// 全部过滤掉；不往下降的话，未登录用户点进任何有 1080P 的房间都起不来。
func TestBiliResolveDownFallsBack(t *testing.T) {
	tiers := []biliTier{{QN: 10000}, {QN: 400}, {QN: 250}}
	target := &tiers[0]

	var tried []int64
	got, u, err := biliResolveDown(tiers, target, "", func(qn int64) ([]biliPlayURL, error) {
		tried = append(tried, qn)
		if qn <= 250 {
			return []biliPlayURL{{URL: "https://cdn/250.flv", QN: 250}}, nil
		}
		return nil, nil
	})
	if err != nil {
		t.Fatalf("降档失败：%v", err)
	}
	if got == nil || got.QN != 250 {
		t.Fatalf("选中档位 = %+v，期望 250", got)
	}
	if u.URL != "https://cdn/250.flv" {
		t.Fatalf("地址 = %q", u.URL)
	}
	want := []int64{10000, 400, 250}
	if len(tried) != len(want) {
		t.Fatalf("尝试的档位 = %v，期望 %v", tried, want)
	}
	for i := range want {
		if tried[i] != want[i] {
			t.Fatalf("尝试的档位 = %v，期望 %v", tried, want)
		}
	}
}

// 只往下降：目标档以下的都不可用时，不能反过来去试更高档。
func TestBiliResolveDownNeverUpgrades(t *testing.T) {
	tiers := []biliTier{{QN: 10000}, {QN: 400}, {QN: 250}}
	target := &tiers[2] // 用户选的是 250

	var tried []int64
	_, _, err := biliResolveDown(tiers, target, "", func(qn int64) ([]biliPlayURL, error) {
		tried = append(tried, qn)
		return nil, nil
	})
	if err == nil {
		t.Fatal("所有可用档位都没有地址时应当报错")
	}
	if len(tried) != 1 || tried[0] != 250 {
		t.Fatalf("尝试了 %v，只该试 250", tried)
	}
}
