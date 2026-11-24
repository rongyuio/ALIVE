package ui

import (
	"context"
	"go/ast"
	"go/token"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 首屏房间铺不满窗口时，列表没有可滚动范围，EndReached 永远不会来：
// 不滚动也要自动补下一页，否则列表永远停在第 1 页。
func TestShortFeedTopsUpWithoutScroll(t *testing.T) {
	a := stillApp()
	a.LiveMode = true
	a.ListHasMore = true
	rooms := stillRooms(15)

	calls := 0
	// loadMoreRooms 的第一件事就是把 ListLoadingMore 置位，这里照样模拟，
	// 否则每帧都会再叫一次，测的就不是去重后的行为了。
	onMore := func() {
		if a.ListLoadingMore {
			return
		}
		a.ListLoadingMore = true
		calls++
	}

	tt := ui.NewTester(func(c *ui.Context) {
		a.grid(c, rooms, &a.HomeList, onMore)
	}, 1391, 1000)
	tt.Frame()
	tt.Frame()
	tt.Frame()

	if calls != 1 {
		t.Fatalf("15 条铺不满窗口时补页 %d 次，want 1 次", calls)
	}
}

// 反过来：内容比窗口高时，布局本身有滚动余量，没滚动就不许自动补页。
func TestTallFeedDoesNotTopUpWithoutScroll(t *testing.T) {
	a := stillApp()
	a.LiveMode = true
	a.ListHasMore = true
	rooms := stillRooms(60)

	calls := 0
	onMore := func() {
		if a.ListLoadingMore {
			return
		}
		a.ListLoadingMore = true
		calls++
	}

	tt := ui.NewTester(func(c *ui.Context) {
		a.grid(c, rooms, &a.HomeList, onMore)
	}, 1391, 1000)
	tt.Frame()
	tt.Frame()

	if calls != 0 {
		t.Fatalf("60 条还比窗口高，却自动补了 %d 次", calls)
	}
}

// 自动补页在加载更多失败后必须退避：失败回包会触发重建，没有退避就是
// 每帧一次上游请求（见 grid 的自动补页）。
func TestAutoTopUpBacksOffAfterFailure(t *testing.T) {
	a := stillApp()
	a.LiveMode = true
	a.ListHasMore = true
	rooms := stillRooms(15)

	calls := 0
	onMore := func() {
		if a.ListLoadingMore {
			return
		}
		a.ListLoadingMore = true
		calls++
		// 模拟失败回包：复位 LoadingMore、保留 hasMore、置退避。
		a.ListLoadingMore = false
		a.noteTopUpFailure()
	}
	tt := ui.NewTester(func(c *ui.Context) {
		a.grid(c, rooms, &a.HomeList, onMore)
	}, 1391, 1000)
	for range 6 {
		tt.Frame()
		tt.Advance(16 * time.Millisecond)
	}
	if calls != 1 {
		t.Fatalf("失败后自动补页 %d 次，want 1（退避没有生效）", calls)
	}
	a.topUpAfter = time.Now().Add(-time.Second)
	tt.Frame()
	if calls != 2 {
		t.Fatalf("冷却到期后没有恢复：%d 次", calls)
	}
}

// 首页续取回包写入前要确认 cursor 没被首屏刷新改过；否则会把新的一段
// 接到一份已经重置的列表上，中间一段永久跳过。
func TestMergeMoreChunkDropsStaleCursor(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.Rooms = []Streamer{{Platform: "douyu", RoomID: "1", Name: "甲"}}
	a.homeCursor = 40
	a.feedCache[key] = feedEntry{cursor: 40, hasMore: true}
	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "99", Title: "新"}},
		Cursor:  160,
		HasMore: true,
	}
	if a.mergeMoreChunk(key, 120, chunk) {
		t.Fatal("cursor 已经回到 40，旧代次的 120 段不该合进来")
	}
	if len(a.Rooms) != 1 {
		t.Fatalf("列表被旧代次改了：%d 条", len(a.Rooms))
	}

	a.feedCache[key] = feedEntry{cursor: 120, hasMore: true}
	a.homeCursor = 120
	if !a.mergeMoreChunk(key, 120, chunk) {
		t.Fatal("cursor 一致时新的一段没有合进来")
	}
	if len(a.Rooms) != 2 {
		t.Fatalf("合并后 %d 条，want 2", len(a.Rooms))
	}
	if got := a.feedCache[key].cursor; got != 160 {
		t.Fatalf("cursor 推进到 %d，want 160", got)
	}
}

// 订阅列表的续取位置只看自己的 cursor，不跟首页共用的 feedCache 走。
func TestSubFeedKeepsItsOwnCursor(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	// 首页已经把共享缓存推到 120；订阅列表自己只到 40。
	a.feedCache[key] = feedEntry{cursor: 120, hasMore: true}
	a.SubRooms = stillRooms(40)
	a.subCursor = 40
	if got := a.subFetchCursor(); got != 40 {
		t.Fatalf("订阅页续取位置是 %d，want 40（不能跟着共享 cursor 跳到 120）", got)
	}
}

// 首屏刷新把订阅列表重置到 cursor 0 之后，在途的旧 cursor 不能再接上去。
func TestSubFeedDropsStaleCursor(t *testing.T) {
	a := stillApp()
	a.subCursor = 40
	if a.subCursorAppendAllowed(120) {
		t.Fatal("cursor 120 接在了只剩 40 的列表上")
	}
	if !a.subCursorAppendAllowed(40) {
		t.Fatal("cursor 40 应该允许接在当前列表后面")
	}
	if !a.subCursorAppendAllowed(0) {
		t.Fatal("cursor 0 是重置，永远允许")
	}
}

// 两个加载更多的失败/追加守卫都必须在调用点上：删掉调用会让自动补页
// 回到每帧重试，或让在途的旧页接进已经重置的列表。
func TestLoadMoreGuards(t *testing.T) {
	needs := map[string]map[string]bool{
		"loadRooms":        {"applyFirstChunk": false},
		"loadMoreRooms":    {"noteTopUpFailure": false, "mergeMoreChunk": false},
		"fetchSubChunk":    {"subCursorAppendAllowed": false, "noteTopUpFailure": false, "applySubChunk": false},
		"loadMoreSubRooms": {"subFetchCursor": false},
	}
	forEachProductionFunc(t, func(fn *ast.FuncDecl, file string, fset *token.FileSet) {
		calls, ok := needs[fn.Name.Name]
		if !ok {
			return
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if _, want := calls[sel.Sel.Name]; want {
				calls[sel.Sel.Name] = true
			}
			return true
		})
	})
	for fn, calls := range needs {
		for call, found := range calls {
			if !found {
				t.Errorf("%s 没有调用 %s：加载更多的失败/追加守卫在调用点上丢了", fn, call)
			}
		}
	}
}

// 部分结果不是失败：live.Chunk 的契约（internal/live/types.go）写明
// 「已经取到的部分照常交出来，Cursor 停在真实消费位置；上层把 Rooms append
// 上去，下一次从 Cursor 续，不跳也不丢」。首屏这一段只取到一半时，
// 房间照样换上去、cursor 照样推进，只让自动补页退避。
func TestPartialFirstChunkKeepsRoomsAndCursor(t *testing.T) {
	a := stillApp()
	key := "douyin|86|-1"
	chunk := live.Chunk{
		Rooms: []live.Room{
			{Platform: live.Douyin, RoomID: "1", Title: "甲"},
			{Platform: live.Douyin, RoomID: "2", Title: "乙"},
		},
		Cursor:  20,
		HasMore: true,
		Partial: true,
	}
	a.applyFirstChunk(key, chunk)

	if len(a.Rooms) != 2 {
		t.Fatalf("部分结果丢了房间：Rooms=%d，want 2（Chunk 契约要求照常交出）", len(a.Rooms))
	}
	if got := a.feedCache[key].cursor; got != 20 {
		t.Fatalf("feedCache cursor=%d，want 20（停在真实消费位置）", got)
	}
	if a.homeCursor != 20 {
		t.Fatalf("homeCursor=%d，want 20（下一次从这里续）", a.homeCursor)
	}
	if !a.ListHasMore {
		t.Fatal("HasMore 丢了：下一次没法从 Cursor 续")
	}
	if a.topUpAllowed() {
		t.Fatal("部分结果没有退避：失败回包触发的重建会每帧重试")
	}
}

// 续取这一段只取到一半：已取到的房间照常 append，cursor 推到 60，
// 下一次从 60 续（出处同 live.Chunk 的契约）。
func TestPartialMoreChunkKeepsRoomsAndCursor(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.Rooms = stillRooms(40)
	a.homeCursor = 40
	a.feedCache[key] = feedEntry{cursor: 40, hasMore: true}

	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "new", Title: "新"}},
		Cursor:  60,
		HasMore: true,
		Partial: true,
	}
	if !a.mergeMoreChunk(key, 40, chunk) {
		t.Fatal("cursor 一致时这一段没有合进来")
	}
	if len(a.Rooms) != 41 {
		t.Fatalf("合并后 Rooms=%d，want 41（部分结果照常 append）", len(a.Rooms))
	}
	if got := a.feedCache[key].cursor; got != 60 {
		t.Fatalf("feedCache cursor=%d，want 60", got)
	}
	if a.homeCursor != 60 {
		t.Fatalf("homeCursor=%d，want 60（下一次从这里续）", a.homeCursor)
	}
	if a.topUpAllowed() {
		t.Fatal("部分结果没有退避：自动补页会每帧重试")
	}
}

// 订阅页同理：部分结果照常接到列表后面，subCursor 停在真实消费位置。
func TestPartialSubChunkKeepsRoomsAndCursor(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.SubRooms = stillRooms(40)
	a.subCursor = 40
	a.feedCache[key] = feedEntry{cursor: 40, hasMore: true}

	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "new", Title: "新"}},
		Cursor:  60,
		HasMore: true,
		Partial: true,
	}
	a.applySubChunk(key, 40, chunk)

	if len(a.SubRooms) != 41 {
		t.Fatalf("合并后 SubRooms=%d，want 41（部分结果照常 append）", len(a.SubRooms))
	}
	if a.subCursor != 60 {
		t.Fatalf("subCursor=%d，want 60（停在真实消费位置）", a.subCursor)
	}
	if a.topUpAllowed() {
		t.Fatal("部分结果没有退避：自动补页会每帧重试")
	}
}

// 首页和订阅页共用一份缓存，但各有自己的 cursor：订阅页把缓存续到 120
// 之后，首页从自己的 40 续上来时不能跳到 120——那会把 41..120 永久跳过。
func TestHomeFeedKeepsItsOwnCursor(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.Rooms = stillRooms(40)
	a.homeCursor = 40
	a.feedCache[key] = feedEntry{rooms: stillRooms(120), cursor: 120, hasMore: true}

	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "new", Title: "新"}},
		Cursor:  60,
		HasMore: true,
	}
	if !a.mergeMoreChunk(key, 40, chunk) {
		t.Fatal("首页没有从自己的 cursor 40 续取")
	}
	if len(a.Rooms) != 41 {
		t.Fatalf("首页 Rooms=%d，want 41", len(a.Rooms))
	}
	if a.homeCursor != 60 {
		t.Fatalf("homeCursor=%d，want 60", a.homeCursor)
	}
	if got := a.feedCache[key].cursor; got != 120 {
		t.Fatalf("共享缓存被回退到 %d，want 120（只往前推）", got)
	}
}

// 一段全是已经有的房间：先推进 cursor 再试一次，连续两次才判死——
// 上游深页偶尔会重复上一段（见 mergeMoreChunk 的注释）。
func TestMergeMoreChunkAdvancesPastDuplicateSegments(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.Rooms = stillRooms(40)
	a.homeCursor = 40
	a.feedCache[key] = feedEntry{rooms: a.Rooms, cursor: 40, hasMore: true}

	dup := []live.Room{
		{Platform: live.Douyu, RoomID: "0"},
		{Platform: live.Douyu, RoomID: "1"},
	}
	if !a.mergeMoreChunk(key, 40, live.Chunk{Rooms: dup, Cursor: 60, HasMore: true}) {
		t.Fatal("全重复的一段没有被接受")
	}
	if !a.ListHasMore {
		t.Fatal("第一次全重复就判死了：应推进 cursor 再试一次")
	}
	if a.homeCursor != 60 {
		t.Fatalf("homeCursor=%d，want 60（推进到这一段之后）", a.homeCursor)
	}
	if got := a.feedCache[key].misses; got != 1 {
		t.Fatalf("misses=%d，want 1", got)
	}

	if !a.mergeMoreChunk(key, 60, live.Chunk{Rooms: dup, Cursor: 80, HasMore: true}) {
		t.Fatal("第二段全重复没有被接受")
	}
	if a.ListHasMore {
		t.Fatal("连续两次全重复仍认为有更多")
	}
}

// 订阅列表从 cursor 0 重新拉是重置：整份换掉，不是接在后面。
func TestSubChunkResetReplacesTheList(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.SubRooms = stillRooms(40)
	a.subCursor = 40
	a.feedCache[key] = feedEntry{rooms: a.SubRooms, cursor: 40, hasMore: true}

	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "fresh", Title: "新首屏"}},
		Cursor:  40,
		HasMore: true,
	}
	a.applySubChunk(key, 0, chunk)
	if len(a.SubRooms) != 1 || a.SubRooms[0].RoomID != "fresh" {
		t.Fatalf("cursor 0 没有重置订阅列表：%d 条", len(a.SubRooms))
	}
	if a.subCursor != 40 {
		t.Fatalf("subCursor=%d，want 40", a.subCursor)
	}
}

// 订阅页落后于共享缓存时，续取只动自己的列表，不把缓存换短。
func TestSubChunkDoesNotRewindTheCache(t *testing.T) {
	a := stillApp()
	key := "douyu|86|-1"
	a.SubRooms = stillRooms(40)
	a.subCursor = 40
	a.feedCache[key] = feedEntry{rooms: stillRooms(120), cursor: 120, hasMore: true}

	chunk := live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyu, RoomID: "new", Title: "新"}},
		Cursor:  60,
		HasMore: true,
	}
	a.applySubChunk(key, 40, chunk)
	if len(a.SubRooms) != 41 {
		t.Fatalf("订阅列表没有接上：%d 条", len(a.SubRooms))
	}
	if got := a.feedCache[key].cursor; got != 120 {
		t.Fatalf("共享缓存被回退到 %d，want 120", got)
	}
	if got := len(a.feedCache[key].rooms); got != 120 {
		t.Fatalf("共享缓存的列表被换成 %d 条，want 120", got)
	}
}

// 命中缓存时首页从缓存的位置续，而不是从 0 重来（否则下一段会把
// 缓存里已经有的房间再取一遍）。
func TestLoadRoomsAdoptsCachedCursor(t *testing.T) {
	a := stillApp()
	a.Win = nil
	key := a.feedKey()
	a.feedCache[key] = feedEntry{
		rooms: stillRooms(40), cursor: 40, hasMore: true, at: time.Now(),
	}

	a.loadRooms()

	if len(a.Rooms) != 40 {
		t.Fatalf("缓存没有渲染：%d 条", len(a.Rooms))
	}
	if a.homeCursor != 40 {
		t.Fatalf("homeCursor=%d，want 40（命中缓存要从缓存的位置续）", a.homeCursor)
	}
}

// cursorStub 是能按 cursor 续取的平台；按页入口不该被调用。
type cursorStub struct {
	cursor, size int
	called       bool
	chunk        live.Chunk
}

func (c *cursorStub) Platform() live.Platform { return live.Douyin }
func (c *cursorStub) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (c *cursorStub) Rooms(context.Context, string, int, int) (live.Page, error) {
	panic("cursor provider 不该走按页入口")
}
func (c *cursorStub) Search(context.Context, string) ([]live.Room, error) { return nil, nil }
func (c *cursorStub) RoomsFrom(_ context.Context, _ string, cursor, size int) (live.Chunk, error) {
	c.called, c.cursor, c.size = true, cursor, size
	return c.chunk, nil
}

// pageStub 是只认页的平台；界面仍然只拿 Chunk。
type pageStub struct {
	page, size int
	called     bool
}

func (p *pageStub) Platform() live.Platform { return live.Bilibili }
func (p *pageStub) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (p *pageStub) Rooms(_ context.Context, _ string, page, size int) (live.Page, error) {
	p.called, p.page, p.size = true, page, size
	return live.Page{
		Rooms:   []live.Room{{Platform: live.Bilibili, RoomID: "1"}},
		HasMore: true,
	}, nil
}
func (p *pageStub) Search(context.Context, string) ([]live.Room, error) { return nil, nil }

// 能按 cursor 续取的平台，界面直接拿 Chunk，不经过「页」。
func TestFetchFeedChunkPrefersCursor(t *testing.T) {
	stub := &cursorStub{chunk: live.Chunk{
		Rooms:   []live.Room{{Platform: live.Douyin, RoomID: "1"}},
		Cursor:  60,
		HasMore: true,
	}}
	chunk, err := fetchFeedChunk(context.Background(), stub, "area", 40, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !stub.called || stub.cursor != 40 || stub.size != 40 {
		t.Fatalf("cursor provider 调用 cursor=%d size=%d，want 40/40", stub.cursor, stub.size)
	}
	if chunk.Cursor != 60 || !chunk.HasMore {
		t.Fatalf("Chunk=%+v，want Cursor 60 HasMore true", chunk)
	}
}

// 只认页的平台在适配层把 cursor 换算成页码；界面拿到的仍是 Chunk。
func TestFetchFeedChunkMapsCursorToPage(t *testing.T) {
	stub := &pageStub{}
	chunk, err := fetchFeedChunk(context.Background(), stub, "area", 80, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !stub.called || stub.page != 3 || stub.size != 40 {
		t.Fatalf("page provider 调用 page=%d size=%d，want 3/40", stub.page, stub.size)
	}
	if chunk.Cursor != 120 {
		t.Fatalf("Cursor=%d，want 120（page*size）", chunk.Cursor)
	}
}
