package live

import (
	"context"
	"os"
	"testing"
	"time"
)

// 探针：同一个房间的封面 URL 在两次请求之间会不会变。
// 这决定磁盘缓存值不值得做——URL 变了缓存就永远命中不了。
func TestCoverURLStability(t *testing.T) {
	if os.Getenv("LIVE_INTEGRATION") == "" {
		t.Skip("设置 LIVE_INTEGRATION=1 才打真实接口")
	}
	for _, p := range []Platform{Bilibili, Douyu, Huya, Douyin} {
		prov, ok := ProviderFor(p)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		groups, err := prov.Categories(ctx)
		if err != nil || len(groups) == 0 || len(groups[0].Categories) == 0 {
			t.Logf("%s 拿不到分区: %v", p, err)
			cancel()
			continue
		}
		area := groups[0].Categories[0].ID

		first := map[string]string{}
		page, err := prov.Rooms(ctx, area, 1, 20)
		if err != nil {
			t.Logf("%s 第一次失败: %v", p, err)
			cancel()
			continue
		}
		for _, r := range page.Rooms {
			first[r.RoomID] = r.Cover
		}
		if len(page.Rooms) > 0 {
			t.Logf("%s 封面样例: %s", p, page.Rooms[0].Cover)
		}

		time.Sleep(60 * time.Second)
		page2, err := prov.Rooms(ctx, area, 1, 20)
		cancel()
		if err != nil {
			t.Logf("%s 第二次失败: %v", p, err)
			continue
		}
		same, diff, missing := 0, 0, 0
		var sample string
		for _, r := range page2.Rooms {
			old, ok := first[r.RoomID]
			if !ok {
				missing++
				continue
			}
			if old == r.Cover {
				same++
			} else {
				diff++
				if sample == "" {
					sample = "\n  旧: " + old + "\n  新: " + r.Cover
				}
			}
		}
		t.Logf("%s（分区 %s，60 秒后）：相同 %d / 变化 %d / 本轮新增 %d%s", p, area, same, diff, missing, sample)
	}
}
