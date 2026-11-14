package live

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// 一次性探针：逐档请求，打印每档返回的 current_qn 与 expected_qn。
func TestProbeBiliStream(t *testing.T) {
	room := os.Getenv("ALIVE_PROBE_ROOM")
	if room == "" {
		t.Skip("设置 ALIVE_PROBE_ROOM 才跑")
	}
	id, err := strconv.ParseInt(room, 10, 64)
	if err != nil {
		t.Fatalf("房间号: %v", err)
	}
	p := newBilibili()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	for _, qn := range []int64{15000, 10000, 400, 250, 150} {
		res, err := p.biliRequest(ctx, id, qn)
		if err != nil {
			t.Logf("请求 qn=%-6d 失败: %v", qn, err)
			continue
		}
		if res.PlayURLInfo == nil || res.PlayURLInfo.PlayURL == nil {
			t.Logf("请求 qn=%-6d 无 playurl", qn)
			continue
		}
		type pair struct{ cur, exp int64 }
		seen := map[pair]bool{}
		for _, st := range res.PlayURLInfo.PlayURL.Stream {
			for _, f := range st.Format {
				for _, c := range f.Codec {
					if len(c.URLInfo) == 0 {
						continue
					}
					full := biliJoinURL(c.URLInfo[0].Host, c.BaseURL, c.URLInfo[0].Extra)
					k := pair{int64(c.CurrentQn), biliExpectedQN(full)}
					seen[k] = true
				}
			}
		}
		keys := make([]pair, 0, len(seen))
		for k := range seen {
			keys = append(keys, k)
		}
		t.Logf("请求 qn=%-6d → (current,expected) %v", qn, keys)
	}
}
