package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

// 抖音列表一页必须凑满调用方要的条数。
//
// 期望值的出处不是实现：首页固定按 listPageSize=40 取一页；上游 count 上限
// 2026-11-24 实测为 20（30/40/100 都只回 20）。所以 40 条必须是两次请求
// （offset 0、20）拼出来的。曾经写死 15，首屏只有 15 张卡，在常见窗口里
// 填不满一屏，列表没有可滚动范围，触底加载永远不会触发。
func TestDouyinRoomsFillRequestedPage(t *testing.T) {
	type call struct{ offset, count int }
	var mu sync.Mutex
	var calls []call

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		mu.Lock()
		calls = append(calls, call{offset, count})
		mu.Unlock()

		// 每个 offset 有 60 间房可用，模拟一个永远不会取空的真实分区。
		entries := make([]map[string]any, 0, count)
		for i := 0; i < count && offset+i < 60; i++ {
			id := strconv.Itoa(offset + i)
			entries = append(entries, map[string]any{
				"web_rid": id,
				"room": map[string]any{
					"id_str": id, "web_rid": id, "title": "房" + id, "status": 2,
					"owner": map[string]any{"nickname": "主" + id},
				},
			})
		}
		body, _ := json.Marshal(map[string]any{
			"status_code": 0,
			"data": map[string]any{
				"data": entries, "count": len(entries),
				"offset": offset + len(entries), "has_more": false,
			},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newDouyin()
	p.api.http = &http.Client{Transport: douyinTestRewrite{t: t, srv: srv}}

	ctx := context.Background()
	page, err := p.Rooms(ctx, "1_1010032", 1, 40)
	if err != nil {
		t.Fatalf("第一页失败: %v", err)
	}
	if len(page.Rooms) != 40 {
		t.Fatalf("第一页 %d 条，调用方要 40 条", len(page.Rooms))
	}
	for i, room := range page.Rooms {
		if want := strconv.Itoa(i); room.RoomID != want {
			t.Fatalf("第 %d 条房间号 %q，want %q", i, room.RoomID, want)
		}
	}
	if !page.HasMore {
		t.Error("第一页取满了，HasMore 应为 true")
	}
	mu.Lock()
	got := append([]call(nil), calls...)
	mu.Unlock()
	want := []call{{0, 20}, {20, 20}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("上游请求 %+v，want %+v（40 条 = 20×2）", got, want)
	}

	// 第二页接在第一页后面，不能和第一页重叠。
	page2, err := p.Rooms(ctx, "1_1010032", 2, 40)
	if err != nil {
		t.Fatalf("第二页失败: %v", err)
	}
	if len(page2.Rooms) != 20 {
		t.Fatalf("第二页 %d 条，want 20（上游共 60 间）", len(page2.Rooms))
	}
	if page2.Rooms[0].RoomID != "40" {
		t.Fatalf("第二页从 %q 开始，want 40", page2.Rooms[0].RoomID)
	}
	if page2.HasMore {
		t.Error("上游只剩 20 条、第二块没取满，HasMore 应为 false")
	}
	mu.Lock()
	defer mu.Unlock()
	want = []call{{40, 20}, {60, 20}}
	got = calls[2:]
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("第二页上游请求 %+v，want %+v", got, want)
	}
}

// 上游某一块没取满但 has_more=true：不能判死，也不能让下一页跳过缺口。
//
// 2026-11-24 实测 has_more 恒 false；这里用假服务器把它置 true，锁住
// 「短块不等于列表结束」这条防御分支。第一块只回 5 条，后面按请求条数
// 回满；修复前 page1 只有 5 条，page2 直接从 offset 40 起，5..39 永远
// 拿不到。
func TestDouyinRoomsContinueAfterShortChunk(t *testing.T) {
	type call struct{ offset, count int }
	var mu sync.Mutex
	var calls []call

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		mu.Lock()
		calls = append(calls, call{offset, count})
		mu.Unlock()

		n := count
		if offset == 0 && count == 20 {
			n = 5 // 模拟上游短块
		}
		entries := make([]map[string]any, 0, n)
		for i := 0; i < n && offset+i < 60; i++ {
			id := strconv.Itoa(offset + i)
			entries = append(entries, map[string]any{
				"web_rid": id,
				"room": map[string]any{
					"id_str": id, "web_rid": id, "title": "房" + id, "status": 2,
					"owner": map[string]any{"nickname": "主" + id},
				},
			})
		}
		body, _ := json.Marshal(map[string]any{
			"status_code": 0,
			"data": map[string]any{
				"data": entries, "count": len(entries),
				"offset": offset + len(entries), "has_more": offset+len(entries) < 60,
			},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newDouyin()
	p.api.http = &http.Client{Transport: douyinTestRewrite{t: t, srv: srv}}
	ctx := context.Background()

	page, err := p.Rooms(ctx, "1_1010032", 1, 40)
	if err != nil {
		t.Fatalf("第一页失败: %v", err)
	}
	if len(page.Rooms) != 40 {
		t.Fatalf("第一页 %d 条，want 40（短块之后要继续取）", len(page.Rooms))
	}
	for i, room := range page.Rooms {
		if want := strconv.Itoa(i); room.RoomID != want {
			t.Fatalf("第 %d 条房间号 %q，want %q", i, room.RoomID, want)
		}
	}
	if !page.HasMore {
		t.Error("第一页取满了，HasMore 应为 true")
	}
	mu.Lock()
	got := append([]call(nil), calls...)
	mu.Unlock()
	want := []call{{0, 20}, {5, 20}, {25, 15}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("上游请求 %+v，want %+v（短块之后从 offset 5 续取）", got, want)
	}

	// 第二页接在 40 后面，不能因为第一块的短块跳房间。
	page2, err := p.Rooms(ctx, "1_1010032", 2, 40)
	if err != nil {
		t.Fatalf("第二页失败: %v", err)
	}
	if len(page2.Rooms) != 20 {
		t.Fatalf("第二页 %d 条，want 20（上游共 60 间）", len(page2.Rooms))
	}
	if page2.Rooms[0].RoomID != "40" {
		t.Fatalf("第二页从 %q 开始，want 40", page2.Rooms[0].RoomID)
	}
	if page2.HasMore {
		t.Error("上游只剩 20 条、第二块没取满，HasMore 应为 false")
	}
}

// 第二段请求抖动一次：整页不该跟着判死，第一段已经拿到的房间也不能白丢。
func TestDouyinRetriesSecondChunkOnce(t *testing.T) {
	type call struct{ offset, count int }
	var mu sync.Mutex
	var calls []call
	failed := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		mu.Lock()
		calls = append(calls, call{offset, count})
		fail := offset == 20 && !failed
		if fail {
			failed = true
		}
		mu.Unlock()
		if fail {
			body, _ := json.Marshal(map[string]any{"status_code": 1, "status_msg": "transient"})
			_, _ = w.Write(body)
			return
		}

		entries := make([]map[string]any, 0, count)
		for i := 0; i < count && offset+i < 60; i++ {
			id := strconv.Itoa(offset + i)
			entries = append(entries, map[string]any{
				"web_rid": id,
				"room": map[string]any{
					"id_str": id, "web_rid": id, "title": "房" + id, "status": 2,
					"owner": map[string]any{"nickname": "主" + id},
				},
			})
		}
		body, _ := json.Marshal(map[string]any{
			"status_code": 0,
			"data": map[string]any{
				"data": entries, "count": len(entries),
				"offset": offset + len(entries), "has_more": offset+len(entries) < 60,
			},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newDouyin()
	p.api.http = &http.Client{Transport: douyinTestRewrite{t: t, srv: srv}}
	ctx := context.Background()

	page, err := p.Rooms(ctx, "1_1010032", 1, 40)
	if err != nil {
		t.Fatalf("第二段抖动一次就整页失败: %v", err)
	}
	if len(page.Rooms) != 40 {
		t.Fatalf("第一页 %d 条，want 40", len(page.Rooms))
	}
	mu.Lock()
	got := append([]call(nil), calls...)
	mu.Unlock()
	want := []call{{0, 20}, {20, 20}, {20, 20}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("上游请求 %+v，want %+v（第二段重试一次）", got, want)
	}
}

// 中途某一块反复失败：已经取到的部分照常交出来，Cursor 停在真实消费位置；
// 下一次从 Cursor 续，既不跳也不丢。部分结果不进缓存，下一次要真的重试。
func TestDouyinPartialChunkResumes(t *testing.T) {
	var mu sync.Mutex
	failAt20 := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		mu.Lock()
		fail := offset == 20 && failAt20
		mu.Unlock()
		if fail {
			body, _ := json.Marshal(map[string]any{"status_code": 1, "status_msg": "transient"})
			_, _ = w.Write(body)
			return
		}
		entries := make([]map[string]any, 0, count)
		for i := 0; i < count && offset+i < 60; i++ {
			id := strconv.Itoa(offset + i)
			entries = append(entries, map[string]any{
				"web_rid": id,
				"room": map[string]any{
					"id_str": id, "web_rid": id, "title": "房" + id, "status": 2,
					"owner": map[string]any{"nickname": "主" + id},
				},
			})
		}
		body, _ := json.Marshal(map[string]any{
			"status_code": 0,
			"data": map[string]any{
				"data": entries, "count": len(entries),
				"offset": offset + len(entries), "has_more": offset+len(entries) < 60,
			},
		})
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := newDouyin()
	p.api.http = &http.Client{Transport: douyinTestRewrite{t: t, srv: srv}}
	ctx := context.Background()

	chunk, err := p.RoomsFrom(ctx, "1_1010032", 0, 40)
	if err != nil {
		t.Fatalf("第二块反复失败不该让整段失败: %v", err)
	}
	if len(chunk.Rooms) != 20 || chunk.Cursor != 20 || !chunk.HasMore || !chunk.Partial {
		t.Fatalf("部分结果 %d 条 cursor=%d hasMore=%v partial=%v，want 20/20/true/true",
			len(chunk.Rooms), chunk.Cursor, chunk.HasMore, chunk.Partial)
	}

	// 故障恢复，从 Cursor 续：下一段必须从 20 开始，不能跳。
	mu.Lock()
	failAt20 = false
	mu.Unlock()
	next, err := p.RoomsFrom(ctx, "1_1010032", chunk.Cursor, 40)
	if err != nil {
		t.Fatalf("续取失败: %v", err)
	}
	if len(next.Rooms) != 40 || next.Rooms[0].RoomID != "20" {
		t.Fatalf("续取 %d 条、从 %q 开始，want 40 条从 20 开始", len(next.Rooms), next.Rooms[0].RoomID)
	}

	// 部分结果没有被缓存住：同一个 cursor 再问一次要重新取到完整一段。
	again, err := p.RoomsFrom(ctx, "1_1010032", 0, 40)
	if err != nil {
		t.Fatalf("部分结果被缓存住了: %v", err)
	}
	if len(again.Rooms) != 40 || again.Partial {
		t.Fatalf("重试仍是 %d 条 partial=%v，want 40 条完整", len(again.Rooms), again.Partial)
	}
}
