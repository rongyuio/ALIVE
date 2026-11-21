package live

import "testing"

// 斗鱼的响应里没有格式参数可控，服务端给什么就是什么。这里能做的是：把
// 响应里所有候选都收起来，优先选 FLV，并且不因为第一个字段非空就直接返回。
//
// 旧实现 `if douyuIsPlayable(live) { return live }` 会在 rtmp_live 非空时
// 立刻返回，后面 player_1 / stream_url / url 永远不看——哪天斗鱼改下发
// m3u8，就会静默踩上 B 站那个「HLS 每片新建连接」的坑。
func TestDouyuParsePlayURLPrefersFLV(t *testing.T) {
	data := &douyuPlayData{
		RtmpLive: "stream_abc.m3u8?wsAuth=1",
		RtmpURL:  "https://cdn.example/live",
	}
	// rtmp_live 是 m3u8，rtmp_url + live 拼出来也是 m3u8；player_1 给 FLV。
	data.Player1 = "https://cdn.example/live/stream_abc.flv?wsAuth=1"

	got, err := douyuParsePlayURL(data)
	if err != nil {
		t.Fatalf("取地址失败：%v", err)
	}
	if !douyuIsFLVURL(got) {
		t.Fatalf("选了 %q，期望 FLV", got)
	}
}

// 只有 m3u8 时仍然要能播——斗鱼不给 FLV 时不能没有地址。
func TestDouyuParsePlayURLFallsBackToHLS(t *testing.T) {
	const hls = "https://cdn.example/live/stream_abc.m3u8?wsAuth=1"
	data := &douyuPlayData{Player1: hls}
	got, err := douyuParsePlayURL(data)
	if err != nil {
		t.Fatalf("取地址失败：%v", err)
	}
	if got != hls {
		t.Fatalf("得到 %q，期望原样返回", got)
	}
}

// 一个字段都没有时要报错，不能返回空地址让播放层去撞。
func TestDouyuParsePlayURLEmpty(t *testing.T) {
	if _, err := douyuParsePlayURL(&douyuPlayData{}); err == nil {
		t.Fatal("空响应应该报错")
	}
}

// FLV 判断不能看 query。
func TestDouyuIsFLVURLIgnoresQuery(t *testing.T) {
	if !douyuIsFLVURL("https://cdn/live/x.flv?wsAuth=.m3u8") {
		t.Fatal("query 里的字样影响了判断")
	}
	if douyuIsFLVURL("https://cdn/live/x.m3u8?name=.flv") {
		t.Fatal("m3u8 被误判成 FLV")
	}
}
