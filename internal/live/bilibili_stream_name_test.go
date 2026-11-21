package live

import "testing"

// B 站 getRoomPlayInfo 的 base_url 现在有两种形态：旧的 .../<流名>_<后缀>.flv，
// 和新的 .../<流名>_<后缀>/index.m3u8。后者如果按「取最后一段」解析，流名会
// 变成 index——master 播放列表就永远查不到，只能退回逐档请求。
func TestBiliSplitStreamName(t *testing.T) {
	cases := []struct {
		name, base, extra, wantName, wantSuffix string
	}{
		{
			name:       "新接口 AVC",
			base:       "/live-bvc/971000/live_4g5l48_Sb5Tlt3_5r2c0_2500/index.m3u8?",
			extra:      "expires=1&pt=web&qn=250&suffix=2500&codec=0",
			wantName:   "live_4g5l48_Sb5Tlt3_5r2c0",
			wantSuffix: "2500",
		},
		{
			name:       "新接口 HEVC",
			base:       "/live-bvc/172849/live_4g5l48_Sb5Tlt3_5r2c0_minihevc/index.m3u8?expires=1",
			extra:      "suffix=minihevc&codec=1",
			wantName:   "live_4g5l48_Sb5Tlt3_5r2c0",
			wantSuffix: "minihevc",
		},
		{
			name:       "旧接口 FLV",
			base:       "/live-bvc/1/live_50329118_9516950_2500.flv?expires=1",
			extra:      "suffix=2500",
			wantName:   "live_50329118_9516950",
			wantSuffix: "2500",
		},
		{
			name:       "没有后缀的 index",
			base:       "https://cn-hnld-ct-01-18.bilivideo.com/live-bvc/510454/live_demo/index.m3u8?sign=a",
			extra:      "",
			wantName:   "live_demo",
			wantSuffix: "",
		},
	}
	for _, tc := range cases {
		name, suffix := biliSplitStreamName(tc.base, tc.extra)
		if name != tc.wantName || suffix != tc.wantSuffix {
			t.Errorf("%s：得到 (%q, %q)，期望 (%q, %q)",
				tc.name, name, suffix, tc.wantName, tc.wantSuffix)
		}
	}
}
