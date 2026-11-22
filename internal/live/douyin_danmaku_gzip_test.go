package live

import (
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// douyinGzipFrame 把一段 Response 字节压成 WS 帧。
func douyinGzipFrame(t *testing.T, resp []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(resp); err != nil {
		t.Fatalf("压缩测试帧失败：%v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("结束压缩失败：%v", err)
	}
	return douyinPushFrame{PayloadType: "msg", Payload: buf.Bytes()}.encode()
}

// douyinChatResponse 构造一个合法的 Response，里面有一条正文为 content 的弹幕。
func douyinChatResponse(content string) []byte {
	chat := douyinPBAppendString(nil, 3, content)
	msg := douyinPBAppendString(nil, 1, "WebcastChatMessage")
	msg = douyinPBAppendBytes(msg, 2, chat)
	return douyinPBAppendBytes(nil, 1, msg)
}

// TestDouyinDecompressRejectsBomb 看住 gzip 解压上限。
//
// SetReadLimit 只约束压缩后的 4MB；deflate 最大压缩比上千倍，一帧就能让
// io.ReadAll 申请数 GB，Go 进程被系统 OOM 杀掉（用户侧就是闪退）。同包
// 其它所有响应体都套了 LimitReader，只有这里没有。
//
// 断言选在「弹幕有没有投递」上：解压后超过上限的帧必须整帧丢弃；旧实现
// 会把 8MB+ 的正文解出来并正常投递。
func TestDouyinDecompressRejectsBomb(t *testing.T) {
	// 解压后比响应体上限更大：正常弹幕远小于这个量级。
	bomb := douyinGzipFrame(t, douyinChatResponse(strings.Repeat("A", httpMaxBody+1)))
	if len(bomb) >= douyinDanmuMaxFrame {
		t.Fatalf("测试帧压缩后 %d 字节，已经超过 WS 读限制，前提不成立", len(bomb))
	}

	called := 0
	s := &douyinDanmaku{webRID: "1", onMessage: func(Danmu) { called++ }}
	s.handleFrame(nil, bomb)
	if called != 0 {
		t.Fatalf("解压后超过上限的帧仍投递了 %d 条弹幕", called)
	}
}

// TestDouyinDecompressAcceptsNormalFrame 反向约束：正常弹幕不能被上限误伤。
func TestDouyinDecompressAcceptsNormalFrame(t *testing.T) {
	frame := douyinGzipFrame(t, douyinChatResponse("早上好"))

	var got []Danmu
	s := &douyinDanmaku{webRID: "1", onMessage: func(d Danmu) { got = append(got, d) }}
	s.handleFrame(nil, frame)

	if len(got) != 1 {
		t.Fatalf("正常帧投递了 %d 条弹幕，期望 1", len(got))
	}
	if got[0].Content != "早上好" {
		t.Fatalf("正文 = %q", got[0].Content)
	}
}
