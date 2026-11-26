package live

import "testing"

// TestHuyaStreamVariantsSingleBitrate 盯住「单码率房间不出两个同 Rate 的档位」。
//
// 虎牙的清晰度只是同一地址加不同的 &ratio=码率。只有一个码率时，「高清 N K」
// 和「标清 N K」算出来是同一个 Rate，菜单里就是两个一模一样的项，选哪个都
// 是同一路流。修后只出一个高清项。
func TestHuyaStreamVariantsSingleBitrate(t *testing.T) {
	profile := &huyaStreamProfile{}
	profile.LiveData.BitRateInfo = `[{"iBitRate":4000}]`

	variants := huyaStreamVariants(profile)
	if len(variants) != 2 {
		t.Fatalf("单码率房间应只出 原画 + 一档高清，实际 %d 档: %+v", len(variants), variants)
	}
	if variants[0].ID != "source" || variants[0].Rate != 0 {
		t.Errorf("第一档 = %+v，期望原画（不带 ratio）", variants[0])
	}
	if variants[1].ID != "high" || variants[1].Rate != 4000 {
		t.Errorf("第二档 = %+v，期望 high/4000", variants[1])
	}
	seen := map[int32]bool{}
	for _, v := range variants {
		if v.Rate == 0 {
			continue
		}
		if seen[v.Rate] {
			t.Fatalf("出现了两个 Rate 相同的档位: %+v", variants)
		}
		seen[v.Rate] = true
	}
}

// 多码率房间保持原行为：原画 + 最高码率的高清 + 最低码率的标清。
func TestHuyaStreamVariantsMultiBitrate(t *testing.T) {
	profile := &huyaStreamProfile{}
	profile.LiveData.BitRateInfo = `[{"iBitRate":2000},{"iBitRate":4000},{"iBitRate":8000}]`

	variants := huyaStreamVariants(profile)
	if len(variants) != 3 {
		t.Fatalf("多码率房间应有 3 档，实际 %d 档: %+v", len(variants), variants)
	}
	if variants[1].Rate != 8000 || variants[2].Rate != 2000 {
		t.Errorf("档位 = %+v，期望高清 8000K / 标清 2000K", variants)
	}
}

// 码率缺失时用兜底值，仍出三档。
func TestHuyaStreamVariantsFallback(t *testing.T) {
	variants := huyaStreamVariants(&huyaStreamProfile{})
	if len(variants) != 3 {
		t.Fatalf("码率缺失应有 3 档兜底，实际 %d 档: %+v", len(variants), variants)
	}
	if variants[1].Rate != huyaBitrateFallbackHD || variants[2].Rate != huyaBitrateFallbackSD {
		t.Errorf("兜底码率 = %+v，期望 %d/%d",
			variants, huyaBitrateFallbackHD, huyaBitrateFallbackSD)
	}
}
