package live

import (
	"encoding/json"
	"testing"
)

// 抖音同档 FLV / HLS 的选择与合并。
//
// 地址一律用 example.com 假域：测试只关心协议选择和档位合并，
// 不需要真实取流，也不该把上游地址或凭证带进仓库。

// douyinTestTier 是 stream_data 里一档的测试载荷。
// Params 非空时原样写进 sdk_params，用来模拟解析失败的脏数据。
type douyinTestTier struct {
	FLV    string
	HLS    string
	Codec  string
	Params string
}

// douyinTestRoom 拼一份房间对象，字段名与上游一致。
func douyinTestRoom(t *testing.T, tiers map[string]douyinTestTier, flvPull, hlsPull map[string]string) *douyinStreamRoom {
	t.Helper()

	streamURL := map[string]any{
		"flv_pull_url":     flvPull,
		"hls_pull_url_map": hlsPull,
	}
	if tiers != nil {
		data := make(map[string]any, len(tiers))
		for key, tier := range tiers {
			main := map[string]any{"flv": tier.FLV, "hls": tier.HLS}
			switch {
			case tier.Params != "":
				main["sdk_params"] = tier.Params
			case tier.Codec != "":
				params, err := json.Marshal(map[string]string{"VCodec": tier.Codec, "resolution": "1920x1080"})
				if err != nil {
					t.Fatalf("编码 sdk_params 失败: %v", err)
				}
				main["sdk_params"] = string(params)
			}
			data[key] = map[string]any{"main": main}
		}
		raw, err := json.Marshal(map[string]any{"data": data})
		if err != nil {
			t.Fatalf("编码 stream_data 失败: %v", err)
		}
		streamURL["live_core_sdk_data"] = map[string]any{
			"pull_data": map[string]any{"stream_data": string(raw)},
		}
	}

	raw, err := json.Marshal(map[string]any{
		"title":      "测试直播间",
		"status":     2,
		"stream_url": streamURL,
	})
	if err != nil {
		t.Fatalf("编码房间数据失败: %v", err)
	}

	var room douyinStreamRoom
	if err := json.Unmarshal(raw, &room); err != nil {
		t.Fatalf("解析房间数据失败: %v", err)
	}
	return &room
}

// douyinTestQuality 按档位 key 取回结果，顺便校验档位存在。
func douyinTestQuality(t *testing.T, ladder []douyinQuality, key string) douyinQuality {
	t.Helper()
	for _, q := range ladder {
		if q.Key == key {
			return q
		}
	}
	t.Fatalf("档位 %q 不在结果里: %+v", key, ladder)
	return douyinQuality{}
}

// TestDouyinVariantCodecNormalization 锁定归一化的实际语义：
// 只有明确的 H.264 / H.265 / AV1 别名会被认出来，其余一律空串。
func TestDouyinVariantCodecNormalization(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"h264", "avc"},
		{"H264", "avc"},
		{" 264 ", "avc"},
		{"avc", "avc"},
		{"AVC1", "avc"},
		{"h265", "hevc"},
		{"HEVC", "hevc"},
		{"265", "hevc"},
		{"hvc1", "hevc"},
		{"av1", "av1"},
		{"AV01", "av1"},
		{"", ""},
		{"vp9", ""},
		{"unknown", ""},
	}
	for _, c := range cases {
		if got := douyinVariantCodec(c.raw); got != c.want {
			t.Errorf("douyinVariantCodec(%q) = %q，期望 %q", c.raw, got, c.want)
		}
	}
}

// TestDouyinTierPrefersFLVForExplicitAVC 明确 H.264 的同档要优先 FLV。
//
// 网页端就是拿 HTTP-FLV 连续流喂 MSE，收到第一个关键帧就出画；HLS 要先解包再按分片
// 吐数据。同时 Key / Label / ID / Codec 这些元数据不能被选择过程改掉。
func TestDouyinTierPrefersFLVForExplicitAVC(t *testing.T) {
	const flv = "https://pull.example.com/stream-1_hd.flv"
	const hls = "https://pull.example.com/stream-1_hd/index.m3u8"
	for _, raw := range []string{"h264", "264", "avc", "avc1", " H264 "} {
		q, ok := douyinTierQuality("hd", "超清", "high", &douyinTierStream{FLV: flv, HLS: hls, Codec: raw})
		if !ok {
			t.Fatalf("VCodec %q 的档位被判为不可播", raw)
		}
		if q.URL != flv || q.Format != "flv" {
			t.Errorf("VCodec %q 选到 %q/%q，明确 H.264 应优先 FLV", raw, q.URL, q.Format)
		}
		if q.Codec != "avc" || q.Key != "hd" || q.Label != "超清" || q.ID != "high" {
			t.Errorf("VCodec %q 的元数据被改动: %+v", raw, q)
		}
	}
}

// TestDouyinTierKeepsHEVCPreference HEVC 同样优先 FLV，编码标注要保留。
func TestDouyinTierKeepsHEVCPreference(t *testing.T) {
	const flv = "https://pull.example.com/stream-1_uhd.flv"
	const hls = "https://pull.example.com/stream-1_uhd/index.m3u8"
	for _, raw := range []string{"h265", "265", "hevc", "hvc1"} {
		q, ok := douyinTierQuality("uhd", "蓝光", "bluray", &douyinTierStream{FLV: flv, HLS: hls, Codec: raw})
		if !ok {
			t.Fatalf("VCodec %q 的档位被判为不可播", raw)
		}
		if q.URL != flv || q.Format != "flv" {
			t.Errorf("VCodec %q 选到 %q/%q，HEVC 也应优先 FLV", raw, q.URL, q.Format)
		}
		if q.Codec != "hevc" || q.Label != "蓝光 · H.265" {
			t.Errorf("VCodec %q 的编码标注 = %q/%q", raw, q.Codec, q.Label)
		}
	}
}

// TestDouyinTierUnknownCodecStaysOnFLV 未知编码保持老行为：有 FLV 就给 FLV。
// AV1 保留原协议优先级，继续交给兼容转码处理。
func TestDouyinTierUnknownCodecStaysOnFLV(t *testing.T) {
	const flv = "https://pull.example.com/stream-1_sd.flv"
	const hls = "https://pull.example.com/stream-1_sd/index.m3u8"
	cases := []struct{ raw, wantCodec string }{
		{"", ""},
		{"vp9", ""},
		{"unknown", ""},
		{"av1", "av1"},
	}
	for _, c := range cases {
		q, ok := douyinTierQuality("sd", "高清", "standard", &douyinTierStream{FLV: flv, HLS: hls, Codec: c.raw})
		if !ok {
			t.Fatalf("VCodec %q 的档位被判为不可播", c.raw)
		}
		if q.URL != flv || q.Format != "flv" {
			t.Errorf("VCodec %q 选到 %q/%q，未知编码不应换协议", c.raw, q.URL, q.Format)
		}
		if q.Codec != c.wantCodec {
			t.Errorf("VCodec %q 的编码 = %q，期望 %q", c.raw, q.Codec, c.wantCodec)
		}
	}

	// 只有 HLS 时仍然要能播：这不是「优先」，是没有别的可选。
	q, ok := douyinTierQuality("sd", "高清", "standard", &douyinTierStream{HLS: hls})
	if !ok || q.URL != hls || q.Format != "hls" {
		t.Errorf("只有 HLS 的未知编码档位 = %+v/%v，期望回退到 HLS", q, ok)
	}
}

// TestDouyinTierAVCWithoutHLSFallsBackToFLV 没有 HLS 时 H.264 也不能丢档。
func TestDouyinTierAVCWithoutHLSFallsBackToFLV(t *testing.T) {
	const flv = "https://pull.example.com/stream-1_hd.flv"
	q, ok := douyinTierQuality("hd", "超清", "high", &douyinTierStream{FLV: flv, Codec: "h264"})
	if !ok {
		t.Fatal("只有 FLV 的 H.264 档位被判为不可播")
	}
	if q.URL != flv || q.Format != "flv" || q.Codec != "avc" {
		t.Errorf("回退结果 = %+v，期望 FLV 且编码保持 avc", q)
	}
}

// TestDouyinParseStreamDataBrokenSDKParamsStaysUnknown sdk_params 解析失败时
// 编码必须留空，不能因为「看着像 h264」就把这一档提到 HLS。
func TestDouyinParseStreamDataBrokenSDKParamsStaysUnknown(t *testing.T) {
	room := douyinTestRoom(t, map[string]douyinTestTier{
		"hd": {
			FLV:    "https://pull.example.com/stream-1_hd.flv",
			HLS:    "https://pull.example.com/stream-1_hd/index.m3u8",
			Params: "{\"VCodec\": h264}",
		},
	}, nil, nil)

	streams := room.StreamURL.parseStreamData()
	if streams["hd"] == nil {
		t.Fatal("stream_data 没有解析出 hd 档")
	}
	if streams["hd"].Codec != "" {
		t.Errorf("sdk_params 非法时编码 = %q，期望留空", streams["hd"].Codec)
	}

	q := douyinTestQuality(t, douyinPickQualities(room), "hd")
	if q.Format != "flv" || q.Codec != "" {
		t.Errorf("编码未知的档位 = %q/%q，期望 FLV 且编码留空", q.URL, q.Format)
	}
}

// 独立 HLS 地址表不能继承 stream_data 中同名档位的编码。
func TestDouyinPickQualitiesDoesNotBorrowCodecForIndependentHLS(t *testing.T) {
	room := douyinTestRoom(t, map[string]douyinTestTier{
		"origin": {FLV: "https://pull.example.com/stream-1_or4.flv", Codec: "h265"},
		"hd":     {FLV: "https://pull.example.com/stream-1_hd.flv", Codec: "h264"},
		"sd": {
			FLV:   "https://pull.example.com/stream-1_sd.flv",
			HLS:   "https://pull.example.com/stream-1_sd/index.m3u8",
			Codec: "h264",
		},
	}, map[string]string{
		"FULL_HD1": "https://pull.example.com/stream-1_uhd.flv",
		"HD1":      "https://pull.example.com/stream-1_hd.flv",
	}, map[string]string{
		"FULL_HD1": "https://pull.example.com/stream-1_uhd/index.m3u8",
		"HD1":      "https://pull.example.com/stream-1_hd/index.m3u8",
	})

	ladder := douyinPickQualities(room)
	want := []string{"origin", "uhd", "hd", "sd"}
	if len(ladder) != len(want) {
		t.Fatalf("档位数量 = %d，期望 %d: %+v", len(ladder), len(want), ladder)
	}
	for i, key := range want {
		if ladder[i].Key != key {
			t.Fatalf("第 %d 档 = %q，期望 %q（顺序由高到低）", i, ladder[i].Key, key)
		}
	}

	origin := douyinTestQuality(t, ladder, "origin")
	if origin.Format != "flv" || origin.Codec != "hevc" || origin.Label != "原画 · H.265" {
		t.Errorf("原画 = %+v，HEVC 没有同档 HLS 时应保留 FLV 并标注编码", origin)
	}

	// 独立地址表没有编码声明，保持原 FLV 选择。
	uhd := douyinTestQuality(t, ladder, "uhd")
	if uhd.Format != "flv" || uhd.Codec != "" {
		t.Errorf("蓝光 = %q/%q，未知编码不该被合并出来的 HLS 提升", uhd.URL, uhd.Format)
	}

	hd := douyinTestQuality(t, ladder, "hd")
	if hd.Format != "flv" || hd.URL != "https://pull.example.com/stream-1_hd.flv" {
		t.Errorf("超清 = %q/%q，独立 HLS 不能借用 FLV 的 H.264 声明", hd.URL, hd.Format)
	}
	if hd.Codec != "avc" || hd.ID != "high" || hd.Label != "超清" {
		t.Errorf("超清的元数据 = %+v", hd)
	}

	sd := douyinTestQuality(t, ladder, "sd")
	if sd.Format != "flv" || sd.Codec != "avc" {
		t.Errorf("高清 = %+v，H.264 档优先 FLV（与网页端一致）", sd)
	}
}

// TestDouyinPickQualitiesWithoutStreamDataKeepsFLV 老响应没有 stream_data 时，
// 档位从 flv_pull_url 的 key 推断、编码未知，仍选 FLV。
func TestDouyinPickQualitiesWithoutStreamDataKeepsFLV(t *testing.T) {
	room := douyinTestRoom(t, nil, map[string]string{
		"HD1": "https://pull.example.com/stream-1_hd.flv",
		"SD1": "https://pull.example.com/stream-1_ld.flv",
	}, map[string]string{
		"HD1": "https://pull.example.com/stream-1_hd/index.m3u8",
		"SD1": "https://pull.example.com/stream-1_ld/index.m3u8",
	})

	ladder := douyinPickQualities(room)
	if len(ladder) != 2 {
		t.Fatalf("档位数量 = %d，期望 2: %+v", len(ladder), ladder)
	}
	for _, q := range ladder {
		if q.Format != "flv" || q.Codec != "" {
			t.Errorf("档位 %q = %q/%q，编码未知时应保持 FLV", q.Key, q.URL, q.Format)
		}
	}
}

// TestDouyinOrderForPlaybackKeepsAVCOriginDefault 原画是 H.264 时不再挪位，
// 默认档就是原画并走 FLV；HEVC 原画仍然让位给第二位。
func TestDouyinOrderForPlaybackKeepsAVCOriginDefault(t *testing.T) {
	avcRoom := douyinTestRoom(t, map[string]douyinTestTier{
		"origin": {
			FLV:   "https://pull.example.com/stream-1_or4.flv",
			HLS:   "https://pull.example.com/stream-1_or4/index.m3u8",
			Codec: "h264",
		},
		"hd": {FLV: "https://pull.example.com/stream-1_hd.flv", Codec: "h264"},
	}, nil, nil)
	avcLadder := douyinOrderForPlayback(avcRoom.StreamURL.parseStreamData(), douyinPickQualities(avcRoom))
	if len(avcLadder) == 0 || avcLadder[0].Key != "origin" {
		t.Fatalf("H.264 原画的默认档 = %+v，期望仍是原画", avcLadder)
	}
	if avcLadder[0].Format != "flv" || avcLadder[0].Codec != "avc" || avcLadder[0].Label != "原画" {
		t.Errorf("H.264 原画 = %+v，期望走 FLV 且标签不变", avcLadder[0])
	}

	hevcRoom := douyinTestRoom(t, map[string]douyinTestTier{
		"origin": {FLV: "https://pull.example.com/stream-1_or4.flv", Codec: "h265"},
		"hd":     {FLV: "https://pull.example.com/stream-1_hd.flv", Codec: "h264"},
	}, nil, nil)
	hevcLadder := douyinOrderForPlayback(hevcRoom.StreamURL.parseStreamData(), douyinPickQualities(hevcRoom))
	if len(hevcLadder) < 2 || hevcLadder[0].Key != "hd" || hevcLadder[1].Key != "origin" {
		t.Fatalf("H.265 原画的顺序 = %+v，期望默认档退让给 H.264 的 hd", hevcLadder)
	}
	if hevcLadder[1].Label != "原画 · H.265" || hevcLadder[1].Format != "flv" {
		t.Errorf("被挪开的原画 = %+v", hevcLadder[1])
	}
}
