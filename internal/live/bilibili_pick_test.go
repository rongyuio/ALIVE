package live

import "testing"

// 选流必须优先 FLV。
//
// libmpv 的嵌入式用法强制自定义 io_open，mpv 因此主动关掉 hls 的连接复用
// （它自己的日志：「hls: Disabling http_persistent due to custom io_open」），
// 每个 1 秒分片都要新建一条 TCP+TLS 连接，实测单片 380ms，分片取数追不上
// 实时。FLV 是单条长连接，没有这个问题。
//
// 实测（B 站 263605，1080P，2026-11-21，各 60 秒）：
//
//	HLS(fMP4)  缓冲 4–23 秒，avsync 漂到 8 秒，首帧 7.7 秒
//	FLV        缓冲 0 秒，avsync 0，首帧 1.4 秒
//
// 旧实现给 HLS 加 100 分、FLV 不加分，FLV 永远选不上。
func TestBiliPickPlayablePrefersFLV(t *testing.T) {
	hls := biliPlayURL{
		URL:    "https://d1--cn-gotcha204b.bilivideo.com/live-bvc/1/x/index.m3u8?sign=a",
		Format: "hls", Codec: "avc",
	}
	flv := biliPlayURL{
		URL:    "https://d1--cn-gotcha07b.bilivideo.com/live-bvc/2/x_2500.flv?sign=b",
		Format: "flv", Codec: "avc",
	}
	got, ok := biliPickPlayable([]biliPlayURL{hls, flv})
	if !ok {
		t.Fatal("没有选出任何候选")
	}
	if !biliIsFLV(got) {
		t.Fatalf("选了 %q，期望 FLV", got.URL)
	}
}

// 只有 HLS 时仍然要能选出来——有些房间不给 FLV，不能因此没有地址。
func TestBiliPickPlayableFallsBackToHLS(t *testing.T) {
	hls := biliPlayURL{
		URL:    "https://d1--cn-gotcha204b.bilivideo.com/live-bvc/1/x/index.m3u8?sign=a",
		Format: "hls", Codec: "avc",
	}
	got, ok := biliPickPlayable([]biliPlayURL{hls})
	if !ok || !biliIsHLS(got) {
		t.Fatalf("只有 HLS 时没选出来：%+v ok=%v", got, ok)
	}
}

// 流名后缀要能认出档位。逐档接口的 current_qn 不可靠：请求 10000 时服务端
// 回的 current_qn 仍是 250，只有 base_url 里的后缀和实际内容绑定。
func TestBiliStreamSuffixQN(t *testing.T) {
	cases := []struct {
		url  string
		want int64
	}{
		{"https://cdn/live-bvc/1/live_x_prohevc.flv?sign=a", qnOrigin},
		{"https://cdn/live-bvc/1/live_x_bluray.flv?sign=a", qnBluray},
		{"https://cdn/live-bvc/1/live_x_4000.flv?sign=a", qnBluray},
		{"https://cdn/live-bvc/1/live_x_minihevc.flv?sign=a", qnUltra},
		{"https://cdn/live-bvc/1/live_x_2500.flv?sign=a", qnUltra},
		{"https://cdn/live-bvc/1/live_x_uhd/index.m3u8?sign=a", qn4K},
		{"https://cdn/live-bvc/1/live_x_dolby.flv?sign=a", qnDolby},
		// 认不出来：返回 0，调用方保持原有判断。
		{"https://cdn/live-bvc/1/whatever.flv?sign=a", 0},
	}
	for _, c := range cases {
		if got := biliStreamSuffixQN(c.url); got != c.want {
			t.Errorf("biliStreamSuffixQN(%q) = %d，期望 %d", c.url, got, c.want)
		}
	}
}

// query 里出现 _2500 之类的字样不能误判：只看路径部分。
func TestBiliStreamSuffixQNIgnoresQuery(t *testing.T) {
	u := "https://cdn/live-bvc/1/live_x_prohevc.flv?sign=_2500&x=_bluray"
	if got := biliStreamSuffixQN(u); got != qnOrigin {
		t.Fatalf("query 干扰了判断：得到 %d，期望 %d", got, qnOrigin)
	}
}
