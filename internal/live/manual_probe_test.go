package live

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestManualBilibiliStream 真的去打一次 B 站接口：列表 → 取流 → 弹幕。
//
// 需要网络，默认跳过；用 ALIVE_PROBE=1 打开。
// 这是一次性探针，不是回归测试——平台的返回会变，断言只放在「有没有拿到东西」上。
func TestManualBilibiliStream(t *testing.T) {
	if os.Getenv("ALIVE_PROBE") == "" {
		t.Skip("设置 ALIVE_PROBE=1 才跑真实接口探针")
	}

	prov, ok := ProviderFor(Bilibili)
	if !ok {
		t.Fatal("没有 B 站 provider")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. 从热门分区拿几个正在播的房间
	page, err := prov.Rooms(ctx, "2_86", 1, 12)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	var target Room
	for _, r := range page.Rooms {
		if r.Live {
			target = r
			break
		}
	}
	if target.RoomID == "" {
		t.Fatalf("拿到的 %d 个房间里没有正在播的", len(page.Rooms))
	}
	t.Logf("房间: %s / %s (roomID=%s)", target.Nickname, target.Title, target.RoomID)

	// 2. 取流
	streamer, ok := prov.(Streamer)
	if !ok {
		t.Fatal("B 站没实现 Streamer")
	}
	info, err := streamer.Stream(ctx, target.RoomID, "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	t.Logf("直播中=%v 档位=%d 线路=%d", info.Live, len(info.Variants), len(info.Lines))
	for _, v := range info.Variants {
		t.Logf("  %-9s %-8s %s", v.ID, v.Label, head(v.URL, 90))
	}
	if info.Live && len(info.Variants) == 0 {
		t.Fatal("在播但一个可播地址都没有")
	}

	// 3. 弹幕：连 6 秒，看能收到几条
	danmakuer, ok := prov.(Danmakuer)
	if !ok {
		t.Fatal("B 站没实现 Danmakuer")
	}
	got := make(chan Danmu, 32)
	stream, err := danmakuer.Danmaku(ctx, target.RoomID, func(d Danmu) {
		select {
		case got <- d:
		default:
		}
	})
	if err != nil {
		t.Fatalf("弹幕连接失败: %v", err)
	}
	defer stream.Close()

	deadline := time.After(6 * time.Second)
	count := 0
	for count < 8 {
		select {
		case d := <-got:
			count++
			t.Logf("  弹幕[%s] Lv.%d %s: %s", d.Kind, d.Level, d.Nickname, d.Content)
		case <-deadline:
			if count == 0 {
				t.Fatal("6 秒内一条弹幕都没收到")
			}
			t.Logf("6 秒内收到 %d 条", count)
			return
		case <-ctx.Done():
			t.Fatal("超时")
		}
	}
	t.Logf("共收到 %d 条", count)
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s…(%d 字节)", s[:n], len(s))
}
