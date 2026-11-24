//go:build windows

package playback

import (
	"context"
	"os"
	"testing"
	"time"
)

// 零拷贝帧也必须能被 CPU 消费者读成像素。
//
// 这条断言文档里写过（doc/architecture/playback.md 的「alpha 陷阱」：截图与
// CPU 渲染器走 Picture.RGBA，CPU 转换必须有内容且 alpha 全 255），但一直只
// 覆盖软件渲染。零拷贝路径把 RGBA 留成空切片，于是截图验收对整面墙失效，
// 而 make check 全绿——这正是「绿色只证明没违反你写下的断言」。
//
// 用本地合成源（mpv 的 lavfi），不联网、不依赖平台地址。
func TestGPUFramesConvertForCPU(t *testing.T) {
	if os.Getenv("ALIVE_MPV_LIB") == "" {
		t.Skip("没有 ALIVE_MPV_LIB：跳过真实引擎用例")
	}
	e, err := Open(context.Background(), "av://lavfi:testsrc=size=160x90:rate=30", Options{
		Silent:        true,
		SkipAudioGate: true,
		NoBackBuffer:  true,
		Width:         160,
		Height:        90,
		FrameRate:     30,
		GPU:           true,
	})
	if err != nil {
		t.Fatalf("打不开本地合成源：%v", err)
	}
	defer func() { _ = e.Close() }()
	if !e.GPU() {
		// 没有硬件 device 时插件退回软件渲染，这条路径由 TestFramesResumeAfterIdle 一类覆盖。
		t.Skip("这台机器没走零拷贝：没有可用的硬件 D3D11 device")
	}
	e.SetPresentSize(160, 90)

	deadline := time.Now().Add(10 * time.Second)
	var (
		pix  []byte
		w, h int
	)
	for time.Now().Before(deadline) {
		p, changed, ok := e.Frame(time.Now())
		if !ok {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		w, h = p.W, p.H
		pix = p.RGBA(nil)
		if changed && len(pix) == 4*w*h {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(pix) != 4*w*h || w <= 0 || h <= 0 {
		t.Fatalf("零拷贝帧没有 CPU 像素：%d 字节，期望 %d×%d×4", len(pix), w, h)
	}

	var nonBlack, alphaBad int
	for i := 0; i < len(pix); i += 4 {
		if pix[i] != 0 || pix[i+1] != 0 || pix[i+2] != 0 {
			nonBlack++
		}
		if pix[i+3] != 255 {
			alphaBad++
		}
	}
	if nonBlack == 0 {
		t.Fatal("CPU 转换结果是全黑：截图里这一格会是空的")
	}
	if alphaBad != 0 {
		t.Fatalf("%d 个像素 alpha 不是 255：CPU 光栅化会按预乘 alpha 画成透明", alphaBad)
	}
}
