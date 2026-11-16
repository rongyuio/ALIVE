package playback

import (
	"fmt"
	"os"
	"testing"
	"time"

	"alive/internal/live"
)

// TestLocalStream 量一条本地推流的解码与上屏帧率。
//
// 平台的 4K 档位可遇不可求，而且会换编码、会下播，拿来当性能基准不可重复。
// 本地用 ffmpeg 把一段 4K60 片源按实时速度推成 MPEG-TS，等于一条码率、分辨率、
// 帧率都固定的直播流，跑多少次都是同一份负载。
//
//	ffmpeg -re -stream_loop -1 -i 4k60.mp4 -c copy \
//	  -f mpegts -listen 1 http://127.0.0.1:8888/live.ts
//
//	$env:ALIVE_LOCAL_URL="http://127.0.0.1:8888/live.ts"
//	$env:ALIVE_LOCAL_SIZE="3840x2160"   # 可选，提前告诉引擎分辨率
//	$env:ALIVE_LOCAL_FPS="60"
//	$env:ALIVE_LOCAL_SECONDS="10"
func TestLocalStream(t *testing.T) {
	url := os.Getenv("ALIVE_LOCAL_URL")
	if url == "" {
		t.Skip("设 ALIVE_LOCAL_URL 跑本地流测试")
	}
	v := live.Variant{URL: url, Label: "local", Codec: "h264"}
	if s := os.Getenv("ALIVE_LOCAL_SIZE"); s != "" {
		var w, h int
		if _, err := fmt.Sscanf(s, "%dx%d", &w, &h); err != nil {
			t.Fatalf("ALIVE_LOCAL_SIZE 要写成 3840x2160：%v", err)
		}
		v.Width, v.Height = w, h
	}
	v.FrameRate = float64(envInt("ALIVE_LOCAL_FPS", 0))
	c := matrixCase{platform: "local", room: "local", title: "local", variant: v}
	// 进程 CPU 时间是判断「这条路还有多少余量」的唯一硬指标：帧率被呈现节拍
	// 压着，跑满 60fps 不代表 CPU 还轻松。
	cpu0, begin := cpuSeconds(), time.Now()
	r := sampleOne(c, envInt("ALIVE_LOCAL_SECONDS", 10))
	elapsed := time.Since(begin)
	cpu1 := cpuSeconds()
	logResult(t, r)
	if r.err != "" {
		t.Fatal(r.err)
	}
	t.Logf("渲染尺寸 %s，进程 CPU 平均 %.2f 核（%.1f 秒 CPU / %.1f 秒墙钟）",
		r.size, (cpu1-cpu0)/elapsed.Seconds(), cpu1-cpu0, elapsed.Seconds())
}
