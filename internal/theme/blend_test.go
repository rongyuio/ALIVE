package theme

import "testing"

// 过渡的两端必须就是起止调色板，中间帧的元数据取目标；颜色在 Oklab 里混。
func TestBlendEndpoints(t *testing.T) {
	from, to := Find("inkwhite-night"), Find("curtain-night")
	if from == nil || to == nil {
		t.Fatal("找不到测试配色")
	}
	at0 := Blend(from, to, 0)
	if at0.BgPrimary != from.BgPrimary || at0.Accent != from.Accent {
		t.Error("t=0 的颜色不是 from")
	}
	if at0.ID != to.ID || at0.Dark != to.Dark {
		t.Error("t=0 的元数据不是目标")
	}
	if got := Blend(from, to, 1); got != to {
		t.Error("t=1 没有返回 to")
	}
	mid := Blend(from, to, 0.5)
	if mid.ID != to.ID || mid.Dark != to.Dark || mid.textMode != to.textMode {
		t.Errorf("中间帧元数据不对：%s/%v/%v", mid.ID, mid.Dark, mid.textMode)
	}
	if mid.BgPrimary != from.BgPrimary.MixOklab(to.BgPrimary, 0.5) {
		t.Error("BgPrimary 不是 Oklab 中点")
	}
	if mid.Accent != from.Accent.MixOklab(to.Accent, 0.5) {
		t.Error("Accent 不是 Oklab 中点")
	}
	if mid.StatusOnline != from.StatusOnline.MixOklab(to.StatusOnline, 0.5) {
		t.Error("StatusOnline 没有跟着混")
	}
	if mid.ShadowPanel != from.ShadowPanel.MixOklab(to.ShadowPanel, 0.5) {
		t.Error("ShadowPanel 没有跟着混")
	}
	if mid.AccentText != from.AccentText.MixOklab(to.AccentText, 0.5) {
		t.Error("AccentText 没有跟着混")
	}
}
