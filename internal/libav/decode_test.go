package libav

import (
	"context"
	"os"
	"testing"
	"time"

	"alive/internal/live"
)

// 验证 libav 能真的解出视频帧和音频 PCM，并检查两者的时间戳是否同轴。
//
// 这是换掉子进程方案前必须过的一关：如果 PTS 正确、音画同轴，后面的时钟和
// 同步就是构造出来的，不需要任何对齐参数。
func TestDecodeLive(t *testing.T) {
	room := os.Getenv("ALIVE_LIVE_ROOM")
	if room == "" {
		t.Skip("设 ALIVE_LIVE_ROOM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	prov, _ := live.ProviderFor(live.Platform(os.Getenv("ALIVE_LIVE_PLATFORM")))
	st, err := prov.(live.Streamer).Stream(ctx, room, "")
	if err != nil || len(st.Variants) == 0 {
		t.Fatalf("取流: %v", err)
	}
	v := st.Variants[0]
	for _, c := range st.Variants {
		if c.Width > v.Width {
			v = c
		}
	}

	start := time.Now()
	d, err := OpenDemuxer(v.URL, v.Headers)
	if err != nil {
		t.Fatalf("OpenDemuxer: %v", err)
	}
	defer d.Close()
	t.Logf("打开 %v  %+v", time.Since(start), d.Info())

	vd, err := d.OpenVideoDecoder()
	if err != nil {
		t.Fatalf("视频解码器: %v", err)
	}
	defer vd.Close()
	ad, err := d.OpenAudioDecoder()
	if err != nil {
		t.Fatalf("音频解码器: %v", err)
	}
	defer ad.Close()
	w, h := vd.Size()
	t.Logf("视频 %dx%d timebase=%g", w, h, vd.Timebase())

	var firstVideo, firstAudio int64 = -1, -1
	vFrames, aFrames := 0, 0
	var vBytes, aBytes int64
	deadline := time.Now().Add(8 * time.Second)

	for time.Now().Before(deadline) && (vFrames < 60 || aFrames < 60) {
		si, err := d.ReadPacket()
		if err != nil {
			t.Logf("读包结束: %v", err)
			break
		}
		switch si {
		case d.VideoIndex():
			if err := vd.SendPacket(d); err == nil {
				for {
					f, err := vd.Receive()
					if err != nil {
						break
					}
					if firstVideo < 0 {
						firstVideo = f.PTS
						t.Logf("首个视频帧 pts=%d  %dx%d  luma=%d chroma=%d",
							f.PTS, w, h, len(f.Luma), len(f.Chroma))
					}
					vFrames++
					vBytes += int64(len(f.Luma) + len(f.Chroma))
				}
			}
		case d.AudioIndex():
			if err := ad.SendPacket(d); err == nil {
				for {
					f, err := ad.Receive()
					if err != nil {
						break
					}
					if firstAudio < 0 {
						firstAudio = f.PTS
						t.Logf("首个音频帧 pts=%d  pcm=%d 字节 (%.1f ms)",
							f.PTS, len(f.PCM), float64(len(f.PCM))/192.0)
					}
					aFrames++
					aBytes += int64(len(f.PCM))
				}
			}
		}
		d.UnrefPacket()
	}

	t.Logf("视频 %d 帧 %d KB，音频 %d 帧 %d KB", vFrames, vBytes/1024, aFrames, aBytes/1024)
	if firstVideo >= 0 && firstAudio >= 0 {
		// 两路流的 timebase 不同，必须各用各的换算成秒再比。
		vSec := float64(firstVideo) * vd.Timebase()
		aSec := float64(firstAudio) * ad.Timebase()
		t.Logf("首个视频 %.3fs，首个音频 %.3fs，差 %.1f ms",
			vSec, aSec, (aSec-vSec)*1000)
	}
	if vFrames == 0 {
		t.Fatal("一帧视频都没解出来")
	}
	if aFrames == 0 {
		t.Fatal("一帧音频都没解出来")
	}
}
