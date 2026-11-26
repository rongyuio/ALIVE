package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"alive/internal/live"
	"alive/internal/media"
)

// 真实数据接入：分区树与直播列表都通过 internal/live 拉取。
// 所有网络请求在后台 goroutine 里做，结果用 win.Update 回主线程；
// 请求带序号，切换平台/分区后过期的响应会被丢弃。

// ensureLiveData 在启动时把所有平台的分区树一起拉起来。
//
// 分区树本身很小（斗鱼十几 KB，虎牙和抖音还是随包静态表，根本不发请求），
// 各平台的 limiter 互相独立，并发拉不会互相拖慢。先拉全的好处是：首次切
// 平台是瞬时的，不会先闪一下「正在加载分区…」再等网络。
// StartPrefetch 在窗口就绪时立刻开拉各平台的分区树和首屏列表，
// 不等首页第一帧渲染。
//
// 首帧之前省下的那几百毫秒很值：切平台要用的首屏数据和封面都是从这里
// 开始下载的，晚一步，用户第一次切过去就正好撞在还没回来的路上。
func (a *App) StartPrefetch() { a.ensureLiveData() }

func (a *App) ensureLiveData() {
	if a.liveLoaded == nil {
		a.liveLoaded = map[string]bool{}
	}
	for _, id := range a.PlatformOrder {
		if a.liveLoaded[id] {
			continue
		}
		a.liveLoaded[id] = true
		if id == a.Platform {
			a.loadCategories(id) // 当前平台走完整流程，会更新界面
			continue
		}
		a.prefetchCategories(id) // 其他平台只填缓存，不动界面
	}
}

// prefetchCategories 预热一个非当前平台的分区树。
//
// 只写 GroupsByPlatform，不碰 Groups / ListLoading 这些界面状态——
// 用户没切过去之前，界面上不该有任何东西因为它而变化。
func (a *App) prefetchCategories(platform string) {
	provider, ok := providerFor(live.Platform(platform))
	if !ok {
		return
	}
	win := a.Win
	if win == nil {
		return // 视图测试没有窗口，也不该发真实请求
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		groups, err := provider.Categories(ctx)
		if err != nil {
			return
		}
		converted := convertGroups(groups)
		win.Update(func() {
			if existing, ok := a.GroupsByPlatform[platform]; ok && len(existing) > 0 {
				return // 期间已经有人填过了
			}
			a.GroupsByPlatform[platform] = converted
			// 分区树到手了，顺手把这个平台的首屏列表也拉下来。
			// 列表接口和分区接口往往不是同一个域名（斗鱼是 www / m），
			// 切过去时才发请求要多付一次 TLS 握手——实测斗鱼首次 1.3 秒，
			// 之后只要 60 毫秒。预取之后切平台是直接命中。
			a.prefetchFirstPage(platform)
		})
	}()
}

// feedURLs 是一个平台首屏会用到的所有图片地址（封面 + 头像）。
//
// 切平台时用它决定哪些纹理要保住：这些图如果已经预取好了，
// 用户切过去就是瞬时的，不该被"清理旧平台"顺手清掉。
//
// 还有一组图不在任何列表里，却一直显示在屏幕上：当前播放 / 收听的那位
// 主播。灵动岛在后台收听时挂着它的头像，播放页挂着它的封面——这两张
// 不保住，切平台那一帧就会退回文字占位，图解码回来又跳一下。
func (a *App) feedURLs(platform string) map[string]bool {
	keep := map[string]bool{}
	if cur := a.current(); cur.AvatarURL != "" || cur.CoverURL != "" {
		if cur.AvatarURL != "" {
			keep[cur.AvatarURL] = true
		}
		if cur.CoverURL != "" {
			keep[cur.CoverURL] = true
		}
	}

	// 顶栏的登录头像也是常驻图：切走再切回来，它不该重新加载。
	// 账号按平台隔离，四个平台各一张，都留着。
	for _, id := range a.PlatformOrder {
		if auth := authFor(id); auth != nil {
			if u := auth.Auth().Avatar; u != "" {
				keep[u] = true
			}
		}
	}

	groups := a.GroupsByPlatform[platform]
	if len(groups) == 0 || len(groups[0].CategoryIDs) == 0 {
		return keep
	}
	entry, ok := a.feedCache[platform+"|"+groups[0].CategoryIDs[0]+"|-1"]
	if !ok {
		return keep
	}
	for _, r := range entry.rooms {
		if r.CoverURL != "" {
			keep[r.CoverURL] = true
		}
		if r.AvatarURL != "" {
			keep[r.AvatarURL] = true
		}
	}
	return keep
}

// prefetchFirstPage 预取一个非当前平台的首屏列表（它的第一个分区）。
//
// 只是把数据放进 feedCache：界面状态一律不碰，用户没切过去之前
// 不该看到任何东西因为它变化。
func (a *App) prefetchFirstPage(platform string) {
	if platform == a.Platform || a.Win == nil {
		return
	}
	groups := a.GroupsByPlatform[platform]
	if len(groups) == 0 || len(groups[0].CategoryIDs) == 0 {
		return
	}
	areaID := groups[0].CategoryIDs[0]
	// 和切过去之后 feedKey 算出来的完全一致：第一个一级分区的第一个二级分区
	key := platform + "|" + areaID + "|-1"
	if _, ok := a.feedCache[key]; ok {
		return
	}
	provider, ok := providerFor(live.Platform(platform))
	if !ok {
		return
	}
	win := a.Win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		chunk, err := fetchFeedChunk(ctx, provider, areaID, 0, listPageSize)
		if err != nil {
			return
		}
		rooms := convertRooms(chunk.Rooms)
		win.Update(func() {
			if _, ok := a.feedCache[key]; ok {
				return // 期间用户已经自己拉过了
			}
			a.feedCache[key] = feedEntry{
				rooms: rooms, cursor: chunk.Cursor, hasMore: chunk.HasMore, at: time.Now(),
			}
			// 连封面一起预取：切过去时数据在 feedCache、图在纹理缓存，
			// 用户看到的是一次瞬时切换，而不是骨架闪一下。
			// 只取前面这些——一屏放不下再多，后面滚动时再按需加载。
			n := min(len(rooms), prefetchCovers)
			for i := 0; i < n; i++ {
				media.Default.Prefetch(a.gridCardW, rooms[i].CoverURL)
				media.Default.PrefetchAvatars(32, rooms[i].AvatarURL)
			}
		})
	}()
}

// onPlatformChanged 在切换平台后恢复该平台的分区树：
// 有缓存直接用，没有就重新拉。
func (a *App) onPlatformChanged() {
	if !a.LiveMode {
		return
	}
	if groups, ok := a.GroupsByPlatform[a.Platform]; ok && len(groups) > 0 {
		a.applyLiveGroups(a.Platform, nil, groups)
		return
	}
	a.Groups = nil
	a.Rooms = nil
	a.loadCategories(a.Platform)
}

func (a *App) loadCategories(platform string) {
	// 当前平台的分区树现在由这次调用负责：重试路径会先把标记清掉，
	// 这里立刻填回来，否则下一帧 ensureLiveData 又会发一次同样的请求。
	// 加载失败时标记保持 true，自动预取不反复冲击接口，重试仍是用户显式触发的。
	if platform == a.Platform && a.liveLoaded != nil {
		a.liveLoaded[platform] = true
	}
	provider, ok := providerFor(live.Platform(platform))
	if !ok {
		// 该平台还没接入：保持空态，不显示假数据。
		if platform == a.Platform {
			a.Groups = nil
			a.Rooms = nil
			a.ListLoading = false
			a.ErrText = ""
		}
		return
	}
	a.ListLoading = true
	a.ErrText = ""
	// 分区树用自己的代次 seq，不复用列表的 liveSeq：两个请求的生命周期互不
	// 相干，共用一个序号时切平台会把分区树的在途响应一起作废（A→B→A 快切
	// 一次，B 的分区树就永远填不进 GroupsByPlatform，下次切过去又重拉）。
	a.catSeq++
	seq := a.catSeq
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		groups, err := provider.Categories(ctx)
		if win == nil {
			return
		}
		// 走 runOnMain（= 窗口的 Update）：同一件事，测试里能换成当场执行的
		// 桩，序号守卫才算得清（见 TestLoadCategoriesKeepsOtherPlatformTree）。
		runOnMain(win, func() {
			if seq != a.catSeq {
				return
			}
			a.ListLoading = false
			if err != nil {
				a.ErrText = "分区加载失败：" + err.Error()
				return
			}
			converted := convertGroups(groups)
			a.GroupsByPlatform[platform] = converted
			if platform == a.Platform {
				a.applyLiveGroups(platform, groups, converted)
			}
		})
	}()
}

// applyLiveGroups 把某个平台的分区树设为当前显示的分区。
func (a *App) applyLiveGroups(platform string, _ []live.CategoryGroup, groups []CategoryGroup) {
	if platform != a.Platform {
		return
	}
	a.Groups = groups
	a.GroupIndex = 0
	a.CategoryIndex = 0
	a.SubCatIndex = -1
	a.MoreOpen = false
	// 不在这里重置入场动画。
	//
	// 四个平台的分区树和首屏列表启动时就预取好了（ensureLiveData），切平台
	// 一般直接命中 feedCache：房间里已经有人、封面纹理也保住了。这时候再让
	// 整屏卡片从 opacity 0 淡入一次，用户看到的就是「切平台闪一下」。
	// 该有动画的是真的从网络取回来那一次，由 loadRooms 在写入新列表时重置。
	a.loadRooms()
}

// loadRooms 拉当前平台 + 当前二级分区的第一页直播列表。
func (a *App) loadRooms() {
	if !a.LiveMode {
		return
	}
	provider, ok := providerFor(live.Platform(a.Platform))
	if !ok {
		a.Rooms = nil
		a.ListLoading = false
		return
	}
	areaID := a.currentCategoryID()
	if areaID == "" {
		a.Rooms = nil
		a.ListLoading = false
		return
	}

	platform := a.Platform
	key := a.feedKey()

	// 每次 loadRooms 都作废更早的在途请求，包括下面命中缓存直接返回的那次。
	//
	// 不推进序号的话，先前那个没有缓存的分区 A 的响应回来时 seq 仍然相等：
	// a.Rooms 会被换成 A 的列表（A 失败时还会在新分区上写 A 的 ErrText），
	// 之后 loadMoreRooms 按当前 key 把第二页拼到 A 的房间后面，污染缓存。
	a.liveSeq++
	seq := a.liveSeq

	// 缓存命中时先渲染缓存：切回看过的分区是瞬时的，不闪骨架、不等网络。
	// 60s 内直接返回；过期则保留缓存内容，后台静默刷新。
	cached, hit := a.feedCache[key]
	if hit {
		a.Rooms = cached.rooms
		a.ListHasMore = cached.hasMore
		a.homeCursor = cached.cursor
		a.ListLoading = false
		a.ErrText = ""
		if time.Since(cached.at) < feedTTL {
			return
		}
	} else {
		// 没缓存就清空，宁可先看到骨架也不能让上一份内容继续挂着。
		// 那次「保留旧列表避免闪白」的改动是错的：切平台时挂着的是另一个
		// 平台的房间，切分区时是上一个分区的，用户会以为切失败了。
		a.Rooms = nil
		a.homeCursor = 0
		a.ListLoading = true
		a.ListHasMore = false
		a.ErrText = ""
	}
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		chunk, err := fetchFeedChunk(ctx, provider, areaID, 0, listPageSize)
		if win == nil {
			return
		}
		win.Update(func() {
			// 除了序号和平台，还要比列表 key：同一个序号下 feedKey 也可能已经
			// 变了（比如只改了三级分区、还没触发新的加载），旧列表不能写回来。
			if seq != a.liveSeq || platform != a.Platform || key != a.feedKey() {
				return
			}
			a.ListLoading = false
			if err != nil {
				a.noteTopUpFailure()
				if !hit {
					a.ErrText = "直播间加载失败：" + err.Error()
				}
				return
			}
			a.applyFirstChunk(key, chunk)
			// 新数据直接换上去，不播任何入场动画：卡片在最终位置、满不
			// 透明地出现，滚动位置也不跳。这里曾经只在「从骨架换成卡片」
			// 时播一次整屏淡入（260ms、带 10 DIP 上浮），而封面还在加载，
			// 那一下就是用户说的「切换后画面一直在抖」。
		})
	}()
}

// applyFirstChunk 把首屏的一段写进列表。
//
// 部分结果照常换上去：Chunk 契约是「已经取到的部分照常交出来，Cursor 停在
// 真实消费位置」，下一次从 Cursor 续，不跳也不丢（见 live.Chunk 与
// doc/architecture/platform.md 第八节）。
func (a *App) applyFirstChunk(key string, chunk live.Chunk) {
	rooms := convertRooms(chunk.Rooms)
	a.Rooms = rooms
	a.ListHasMore = chunk.HasMore
	a.homeCursor = chunk.Cursor
	// 首屏这一段是刷新：缓存直接换成这一份，不跟旧缓存比新旧。过期刷新
	// 本来就要把列表收回平台当前的第一段。
	a.feedCache[key] = feedEntry{
		rooms: rooms, cursor: chunk.Cursor, hasMore: chunk.HasMore, at: time.Now(),
	}
	a.noteChunkOutcome(chunk)
}

// fetchFeedChunk 从 cursor 往后取一段。
//
// 能按 cursor 续取的平台（抖音）直接用；按页取数的平台把 cursor 换算成
// 页码，Cursor 仍然按 page*size 前进。界面只认 Chunk，不认页。
func fetchFeedChunk(ctx context.Context, provider live.Provider, areaID string, cursor, size int) (live.Chunk, error) {
	if cp, ok := provider.(live.CursorProvider); ok {
		return cp.RoomsFrom(ctx, areaID, cursor, size)
	}
	page := cursor/size + 1
	pageData, err := provider.Rooms(ctx, areaID, page, size)
	if err != nil {
		return live.Chunk{}, err
	}
	return live.Chunk{Rooms: pageData.Rooms, Cursor: page * size, HasMore: pageData.HasMore}, nil
}

// loadMoreRooms 续取下一段：滚动到距底部 320px 以内时由触底哨兵触发。
//
// 三道闸：正在拉首屏、已经在拉下一页、上游说没有更多，都不重复发请求——
// 触底事件会连着来好几次。
func (a *App) loadMoreRooms() {
	if !a.LiveMode || a.ListLoading || a.ListLoadingMore || !a.ListHasMore {
		return
	}
	// 视图测试没有窗口，也不该发真实请求（现在网格会自己触发补页，
	// 这条路径不再只由用户滚动进入）。
	if a.Win == nil {
		return
	}
	provider, ok := providerFor(live.Platform(a.Platform))
	if !ok {
		return
	}
	areaID := a.currentCategoryID()
	if areaID == "" {
		return
	}

	platform := a.Platform
	key := a.feedKey()
	entryCursor := a.homeCursor
	a.ListLoadingMore = true
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		chunk, err := fetchFeedChunk(ctx, provider, areaID, entryCursor, listPageSize)
		if win == nil {
			return
		}
		win.Update(func() {
			// 先复位：即使这一段属于已经切走的分区，也不能让「正在加载更多」
			// 的标记挂在新分区上，否则新列表再也续不了。
			a.ListLoadingMore = false
			// 期间切了平台或分区：这一段属于旧列表，直接丢
			if platform != a.Platform || a.feedKey() != key {
				return
			}
			if err != nil {
				// 保留 hasMore：下次滚动到底还能再试一次，不把列表卡死。
				// 自动补页要退避，否则失败回包触发的重建会每帧再发一次。
				a.noteTopUpFailure()
				return
			}
			// 结果由 mergeMoreChunk 记：旧代次（返回 false）的那一段不算数。
			a.mergeMoreChunk(key, entryCursor, chunk)
		})
	}()
}

// mergeMoreChunk 把新取到的一段合进首页列表。
//
// cursor 是发起请求时首页列表自己的位置（homeCursor）。期间首屏刷新（过期
// 缓存）或别的写者推进了列表，这一段就属于旧代次：直接丢，否则会 append 到
// 一份已经重置的列表上，把中间的房间永久跳过。
func (a *App) mergeMoreChunk(key string, cursor int, chunk live.Chunk) bool {
	if a.homeCursor != cursor {
		return false
	}
	// 上游翻页偶尔会返回重复房间，按房间号去重
	seen := make(map[string]bool, len(a.Rooms))
	for _, s := range a.Rooms {
		seen[s.RoomID] = true
	}
	added := make([]Streamer, 0, len(chunk.Rooms))
	for _, s := range convertRooms(chunk.Rooms) {
		if seen[s.RoomID] {
			continue
		}
		seen[s.RoomID] = true
		added = append(added, s)
	}

	// 一段全是已经有的房间：可能只是上游深页重复了上一段（抖音
	// 匿名列表实测有），也可能真的续到头了。第一次把 cursor 推进到
	// 这一段之后的位置再试，连续两次才判死——否则一次抖动就把列表提前关掉。
	if len(added) == 0 {
		misses := a.feedCache[key].misses + 1
		hasMore := chunk.HasMore && misses < 2
		a.ListHasMore = hasMore
		a.homeCursor = chunk.Cursor
		a.writeFeedCache(key, feedEntry{
			rooms: a.Rooms, cursor: chunk.Cursor, hasMore: hasMore,
			at: time.Now(), misses: misses,
		})
		a.noteChunkOutcome(chunk)
		return true
	}

	// 不设上限：一直追加到上游没有更多为止。
	// 数据本身很小（一条一两百字节），真正贵的是封面图，
	// 而列表是虚拟化的——不可见的卡片根本不会请求图片。
	rooms := append(a.Rooms, added...)
	hasMore := chunk.HasMore
	a.Rooms = rooms
	a.ListHasMore = hasMore
	a.homeCursor = chunk.Cursor
	a.writeFeedCache(key, feedEntry{
		rooms: rooms, cursor: chunk.Cursor, hasMore: hasMore, at: time.Now(),
	})
	a.noteChunkOutcome(chunk)
	return true
}

// writeFeedCache 把一段写进首页/订阅页共用的列表缓存。
//
// 只往前推：两个列表各有自己的 cursor，订阅页可能已经把缓存续到更远。
// 落后的一边续上来时只刷新时间戳，不能把缓存回退成更短的列表——否则另一
// 边下次进来会看到一份缩短的列表。
func (a *App) writeFeedCache(key string, entry feedEntry) {
	if cached, ok := a.feedCache[key]; ok && entry.cursor < cached.cursor {
		cached.at = time.Now()
		a.feedCache[key] = cached
		return
	}
	a.feedCache[key] = entry
}

// 自动补页失败后的退避：3s、6s、12s、24s，封顶 30s。
const (
	topUpBackoffMin = 3 * time.Second
	topUpBackoffMax = 30 * time.Second
)

// noteTopUpFailure 记一次「加载更多」失败。
//
// 补页写在渲染路径里，失败回包会触发重建；没有退避就会每帧再发一次请求。
// 只影响自动补页，用户滚动触发的 EndReached 不受它约束。
func (a *App) noteTopUpFailure() {
	a.topUpFailures++
	d := topUpBackoffMin << min(a.topUpFailures-1, 4)
	if d > topUpBackoffMax {
		d = topUpBackoffMax
	}
	a.topUpAfter = time.Now().Add(d)
}

func (a *App) noteTopUpSuccess() {
	a.topUpFailures = 0
	a.topUpAfter = time.Time{}
}

// noteChunkOutcome 按 Chunk 的契约记一次取数结果。
//
// Partial 表示这一段没取满、中途有失败：Rooms 照常使用，下一次从 Cursor 续，
// 但自动补页要退避一下——失败回包会触发重建，没有退避就是每帧再发一次请求。
func (a *App) noteChunkOutcome(chunk live.Chunk) {
	if chunk.Partial {
		a.noteTopUpFailure()
		return
	}
	a.noteTopUpSuccess()
}

func (a *App) topUpAllowed() bool {
	return a.topUpAfter.IsZero() || !time.Now().Before(a.topUpAfter)
}

// reloadRooms 在切换分区后按需重新拉列表。
func (a *App) reloadRooms() {
	if a.LiveMode {
		a.loadRooms()
	}
}

const (
	// listPageSize 是一次续取的条数，不是界面的「页」。列表是无限流，
	// 这个值只决定每次向平台要多少条。
	listPageSize = 40
	// prefetchCovers 是预取首屏时连带拉多少张封面。
	//
	// 取一整页：List 除了可见的那几行，还会多建几行（list.md 的
	// "the rows in view, and a few beyond"），窗口宽的时候六行就是三十多张。
	// 只备两三行的话，切过去第一帧会有几张卡空着封面，等下载回来才补上。
	prefetchCovers = listPageSize
	// feedTTL 是分区列表的缓存时间：60s 内切回同一个分区直接渲染缓存，
	// 不重新请求。
	feedTTL = 60 * time.Second
)

// feedEntry 是某个分区已经加载到的位置。
type feedEntry struct {
	rooms []Streamer
	// cursor 是已经消费到的上游位置（平台自己的 offset 单位）。列表是
	// 无限流，page 只是取数单位；cursor 才是续取的真值。
	cursor  int
	hasMore bool
	at      time.Time
	// misses 是连续「一段全是已有房间」的次数。上游深页偶尔会重复上一段
	// （抖音匿名列表实测有），一次就判死会把还能续的列表提前停掉。
	misses int
}

// feedKey 唯一标识一个列表：平台 + 二级分区 id + 三级分区。
//
// 用分区 id 而不是索引：首页和订阅页要能命中同一份缓存，
// 而订阅项只知道「哪个平台的哪个分区」，不知道它在分区树里的位置。
func (a *App) feedKey() string {
	return fmt.Sprintf("%s|%s|%d", a.Platform, a.currentCategoryID(), a.SubCatIndex)
}

func convertGroups(groups []live.CategoryGroup) []CategoryGroup {
	out := make([]CategoryGroup, 0, len(groups))
	for _, g := range groups {
		names := make([]string, 0, len(g.Categories))
		ids := make([]string, 0, len(g.Categories))
		for _, c := range g.Categories {
			names = append(names, c.Name)
			ids = append(ids, c.ID)
		}
		out = append(out, CategoryGroup{
			ID:          g.ID,
			Name:        g.Name,
			Categories:  names,
			CategoryIDs: ids,
		})
	}
	return out
}

func convertRooms(rooms []live.Room) []Streamer {
	out := make([]Streamer, 0, len(rooms))
	for i, r := range rooms {
		out = append(out, Streamer{
			Platform:    string(r.Platform),
			RoomID:      r.RoomID,
			Name:        r.Nickname,
			Title:       r.Title,
			Category:    r.Area,
			Viewers:     int(r.Viewers),
			Live:        r.Live,
			Hue:         float32(i * 37 % 360),
			CoverURL:    r.Cover,
			AvatarURL:   r.Avatar,
			BackdropURL: r.Background,
		})
	}
	return out
}

// currentCategoryIDs 是当前一级分区下各二级分区的接口 id。
func (a *App) currentCategoryIDs() []string { return a.currentGroup().CategoryIDs }

// ---------------------------------------------------------------- 顶栏搜索

const (
	// searchDebounce 是搜索框的 220ms 防抖：
	// 连续输入只在停下来之后发一次请求。
	searchDebounce = 220 * time.Millisecond
	// searchTimeout 比列表请求短：搜索是前台交互，等不起 20 秒。
	searchTimeout = 12 * time.Second
)

// syncSearch 在查询词或平台变化时启动一次防抖搜索。
//
// view 每帧重建，所以用「已经发起的查询词」判断，只有真的变了才调度一次；
// 连续输入时更早的响应靠代次丢弃。
func (a *App) syncSearch() {
	q := strings.TrimSpace(a.SearchQuery)
	platform := a.viewPlatform()
	if q == a.searchQuery && platform == a.searchPlatform {
		return
	}
	a.searchQuery, a.searchPlatform = q, platform
	a.SearchHit = 0
	seq := a.searchSeq.Add(1)
	// 查询词或平台已经变了：上一份结果不属于这一次查询。这段窗口至少
	// 220ms 防抖加一次网络往返，留着旧结果时列表画的是上一个词的房间，
	// 按 Enter 打开的也是上一个词的首条命中。
	a.SearchResults = nil

	if q == "" {
		a.SearchResults, a.SearchLoading, a.SearchErr = nil, false, ""
		return
	}
	// 视图测试没有窗口：不发真实请求，也不留后台 goroutine。
	if a.Win == nil {
		a.SearchLoading = false
		return
	}
	provider, ok := providerFor(live.Platform(platform))
	if !ok {
		a.SearchResults, a.SearchLoading = nil, false
		a.SearchErr = "该平台暂未接入搜索"
		return
	}
	a.SearchLoading, a.SearchErr = true, ""
	win := a.Win

	go func() {
		time.Sleep(searchDebounce)
		if a.searchSeq.Load() != seq {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), searchTimeout)
		defer cancel()
		rooms, err := provider.Search(ctx, q)
		if win == nil {
			return
		}
		win.Update(func() {
			if a.searchSeq.Load() != seq {
				return
			}
			a.SearchLoading = false
			if err != nil {
				a.SearchResults = nil
				a.SearchErr = searchErrorText(err)
				return
			}
			a.SearchResults = convertRooms(rooms)
		})
	}()
}

// searchErrorText 是给用户看的失败文案：风控单独说，
// 不要把 HTTP 状态码和上游报文直接甩到浮层上。
func searchErrorText(err error) string {
	if live.IsBlocked(err) {
		return "请求过于频繁，请稍后再试"
	}
	return "搜索失败：" + err.Error()
}

// ---------------------------------------------------------------- 订阅页

// subFeedKey 是订阅项对应列表的缓存 key，格式与 feedKey 一致
// （订阅永远落在二级分区维度，所以第三段是 -1）。
func subFeedKey(sub Subscription) string { return sub.PlatformID + "|" + sub.CategoryID + "|-1" }

// subResolveRetry 是「缺分区 id」的订阅回填失败后隔多久再试。
//
// 回填由 ensureSubFeed 在渲染路径上触发，每帧都会跑；没有冷却就是每帧一条
// Categories 请求。上游挂了时每帧撞墙也没有意义——30s 一次，直到成功。
const subResolveRetry = 30 * time.Second

// subResolveKey 是回填去重的身份串：平台 + 一级分组 + 二级分区名。
//
// 不能用下标：订阅列表会增删重排，同一条订阅换了个位置仍是同一条。
func subResolveKey(s Subscription) string { return s.PlatformID + "|" + s.Group + "|" + s.Name }

// ensureSubFeed 在当前选中的订阅变化时拉第一页。
//
// 订阅页的平台和分区可以跟首页完全不同，所以它有一套自己的列表状态，
// 但缓存跟首页共用 feedCache——同一个分区在哪边看都是同一份数据。
func (a *App) ensureSubFeed() {
	if !a.LiveMode || len(a.Subs) == 0 {
		return
	}
	chips := a.subChips()
	if a.SubIndex < 0 || a.SubIndex >= len(chips) {
		return
	}
	// 一枚 chip 可能挂着好几家平台，房间列表只显示当前选中的那一家的
	sub := a.activeSub(chips[a.SubIndex])
	if sub.CategoryID == "" {
		// 旧版本存下来的订阅没有分区 id：先按名字回填。
		//
		// 这个函数在渲染路径上每帧都会跑，而 resolveSubID 自己没有在途去重：
		// 不在这里记账，一条缺分区 id 的旧订阅就是每帧一次 Categories 请求。
		// 发起时把「下次可再试」记成 now+subResolveRetry，在途和刚失败都算
		// 在这段冷却里；失败就一直按这个节奏退避，不是每帧重试。
		key := subResolveKey(sub)
		if a.subResolve == nil {
			a.subResolve = map[string]time.Time{}
		}
		if next, tried := a.subResolve[key]; tried && time.Now().Before(next) {
			return
		}
		i := a.subIndex(sub)
		if i < 0 {
			return
		}
		a.subResolve[key] = time.Now().Add(subResolveRetry)
		a.resolveSubID(i)
		return
	}
	key := subFeedKey(sub)
	if key == a.subKey {
		return
	}
	a.subKey = key
	a.subSeq++
	a.subCursor = 0
	a.SubLoadingMore = false

	if _, ok := providerFor(live.Platform(sub.PlatformID)); !ok {
		a.SubRooms, a.SubHasMore, a.SubLoading = nil, false, false
		a.SubErr = "该平台暂未接入"
		return
	}

	if cached, hit := a.feedCache[key]; hit {
		a.SubRooms, a.SubHasMore, a.SubLoading, a.SubErr = cached.rooms, cached.hasMore, false, ""
		a.subCursor = cached.cursor
		if time.Since(cached.at) < feedTTL {
			return
		}
	} else {
		a.SubRooms, a.SubHasMore, a.SubLoading, a.SubErr = nil, false, true, ""
		a.subCursor = 0
	}
	a.fetchSubChunk(sub.PlatformID, sub.CategoryID, key, a.subSeq, 0)
}

// fetchSubChunk 从 cursor 往后取订阅列表的一段；cursor 0 是首屏重置。
func (a *App) fetchSubChunk(platform, areaID, key string, seq, cursor int) {
	win := a.Win
	if win == nil {
		return // 视图测试没有窗口，也不该发真实请求
	}
	provider, ok := providerFor(live.Platform(platform))
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		chunk, err := fetchFeedChunk(ctx, provider, areaID, cursor, listPageSize)
		win.Update(func() {
			if seq != a.subSeq {
				return
			}
			a.SubLoading, a.SubLoadingMore = false, false
			// 首屏刷新会把列表重置到 cursor 0；在途的旧 cursor 属于旧代次，
			// 不能再接到新列表后面（会把中间一段跳过）。
			if !a.subCursorAppendAllowed(cursor) {
				return
			}
			if err != nil {
				a.noteTopUpFailure()
				if cursor == 0 {
					a.SubErr = "直播间加载失败：" + err.Error()
				}
				return
			}
			a.applySubChunk(key, cursor, chunk)
		})
	}()
}

// applySubChunk 把订阅列表的一段合进去。
//
// cursor 是发起请求时的位置：0 是首屏重置，其余按房间号去重后接在后面。
// 部分结果照常使用（Chunk 契约），共享缓存只往前写：订阅列表从 cursor 0
// 重新拉时，不能把首页已经续到的更长列表覆盖成短的那一份。
func (a *App) applySubChunk(key string, cursor int, chunk live.Chunk) {
	a.SubErr = ""
	rooms := convertRooms(chunk.Rooms)
	if cursor == 0 {
		a.SubRooms = rooms
	} else {
		// 上游续取会返回重复房间，按房间号去重
		seen := make(map[string]bool, len(a.SubRooms))
		for _, s := range a.SubRooms {
			seen[s.RoomID] = true
		}
		for _, s := range rooms {
			if seen[s.RoomID] {
				continue
			}
			seen[s.RoomID] = true
			a.SubRooms = append(a.SubRooms, s)
		}
	}
	a.SubHasMore = chunk.HasMore
	a.subCursor = chunk.Cursor
	a.writeFeedCache(key, feedEntry{
		rooms: a.SubRooms, cursor: chunk.Cursor, hasMore: chunk.HasMore,
		// misses 是首页「这一段是不是全是重复房间」的连续计数（两次才判死）。
		// 订阅页也写同一份缓存，不带上的话会把首页的计数清零，一条上游深页
		// 重复的段之后就再判不出来，列表只能靠别的信号收尾。
		misses: a.feedCache[key].misses,
		at:     time.Now(),
	})
	a.noteChunkOutcome(chunk)
}

// retrySubFeed 重发当前订阅项的第一页。
//
// ensureSubFeed 用 subKey 记住「这一项已经发起过」，首屏失败后不会自己
// 再发第二次；所以重试不能只清 SubErr，必须亲手把第一页再发一遍并推进
// 代次，让在途的旧响应作废。成功后 fetchSubChunk 会清掉 SubErr。
func (a *App) retrySubFeed() {
	a.SubErr = ""
	// 视图测试没有窗口：只清错误，不发真实请求，也不留后台 goroutine。
	if a.Win == nil || !a.LiveMode {
		return
	}
	chips := a.subChips()
	if a.SubIndex < 0 || a.SubIndex >= len(chips) {
		return
	}
	sub := a.activeSub(chips[a.SubIndex])
	if sub.CategoryID == "" {
		return
	}
	if _, ok := providerFor(live.Platform(sub.PlatformID)); !ok {
		a.SubErr = "该平台暂未接入"
		return
	}
	key := subFeedKey(sub)
	a.subKey = key
	a.subSeq++
	a.subCursor = 0
	a.SubLoading, a.SubLoadingMore = true, false
	a.fetchSubChunk(sub.PlatformID, sub.CategoryID, key, a.subSeq, 0)
}

// loadMoreSubRooms 是订阅页的触底预取。
func (a *App) loadMoreSubRooms() {
	if !a.LiveMode || a.SubLoading || a.SubLoadingMore || !a.SubHasMore {
		return
	}
	// 视图测试没有窗口：不能先置位 SubLoadingMore 再让 fetchSubChunk 直接
	// 返回，那会把「正在加载更多」永久挂住。
	if a.Win == nil {
		return
	}
	chips := a.subChips()
	if a.SubIndex < 0 || a.SubIndex >= len(chips) {
		return
	}
	sub := a.activeSub(chips[a.SubIndex])
	if sub.CategoryID == "" {
		return
	}
	key := subFeedKey(sub)
	a.SubLoadingMore = true
	a.fetchSubChunk(sub.PlatformID, sub.CategoryID, key, a.subSeq, a.subFetchCursor())
}

// subFetchCursor 是订阅列表自己的续取位置。
//
// 不能读 feedCache 的 cursor：那个 cursor 首页和订阅页共用，首页续过之后
// 订阅页会从中间某处开始，把它没取到的一段整段跳过。
func (a *App) subFetchCursor() int { return a.subCursor }

// subCursorAppendAllowed 报告这一段能不能接在当前订阅列表后面。
//
// 首屏刷新（cursor 0）随时可以重置；其余只有正好接在 subCursor 后面才算数。
// 在途的旧代次响应晚到时，列表已经被重置，直接丢。
func (a *App) subCursorAppendAllowed(cursor int) bool {
	return cursor == 0 || a.subCursor == cursor
}

// resolveSubID 给没有分区 id 的旧订阅回填 id：
// 拿该平台的二级分区表按「一级分组名 + 二级分区名」反查，命中就写回去。
func (a *App) resolveSubID(i int) {
	if i < 0 || i >= len(a.Subs) || a.Win == nil {
		return
	}
	sub := a.Subs[i]
	provider, ok := providerFor(live.Platform(sub.PlatformID))
	if !ok {
		return
	}
	win := a.Win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		groups, err := provider.Categories(ctx)
		if err != nil {
			return
		}
		id := ""
		for _, g := range groups {
			if g.Name != sub.Group {
				continue
			}
			for _, c := range g.Categories {
				if c.Name == sub.Name {
					id = c.ID
					break
				}
			}
		}
		if id == "" {
			return
		}
		win.Update(func() {
			// 期间用户可能已经删掉或重排了订阅，按内容再确认一次
			if i >= len(a.Subs) {
				return
			}
			if a.Subs[i].PlatformID != sub.PlatformID || a.Subs[i].Name != sub.Name {
				return
			}
			if a.Subs[i].CategoryID == "" {
				a.Subs[i].CategoryID = id
				a.markDirty()
			}
		})
	}()
}

// currentCategoryID 是当前选中的二级分区 id（真实数据模式下用于请求列表）。
func (a *App) currentCategoryID() string {
	ids := a.currentCategoryIDs()
	if a.CategoryIndex < 0 || a.CategoryIndex >= len(ids) {
		return ""
	}
	return ids[a.CategoryIndex]
}
