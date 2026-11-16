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
	provider, ok := live.ProviderFor(live.Platform(platform))
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
	provider, ok := live.ProviderFor(live.Platform(platform))
	if !ok {
		return
	}
	win := a.Win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		page, err := provider.Rooms(ctx, areaID, 1, listPageSize)
		if err != nil {
			return
		}
		rooms := convertRooms(page.Rooms)
		win.Update(func() {
			if _, ok := a.feedCache[key]; ok {
				return // 期间用户已经自己拉过了
			}
			a.feedCache[key] = feedEntry{
				rooms: rooms, page: 1, hasMore: page.HasMore, at: time.Now(),
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
	provider, ok := live.ProviderFor(live.Platform(platform))
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
	a.liveSeq++
	seq := a.liveSeq
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		groups, err := provider.Categories(ctx)
		if win == nil {
			return
		}
		win.Update(func() {
			if seq != a.liveSeq {
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
	provider, ok := live.ProviderFor(live.Platform(a.Platform))
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

	// 缓存命中时先渲染缓存：切回看过的分区是瞬时的，不闪骨架、不等网络。
	// 60s 内直接返回；过期则保留缓存内容，后台静默刷新。
	cached, hit := a.feedCache[key]
	if hit {
		a.Rooms = cached.rooms
		a.ListHasMore = cached.hasMore
		a.ListLoading = false
		a.ErrText = ""
		if time.Since(cached.at) < feedTTL {
			return
		}
	} else {
		// 没缓存就清空，宁可先看到骨架也不能让上一份内容继续挂着。
		// 那次「保留旧列表避免闪白」的改动是错的：切平台时挂着的是另一个
		// 平台的房间，切分区时是上一个分区的，用户会以为切失败了。
		// baseline 的 useStreamerFeed 在没缓存时也是直接清空。
		a.Rooms = nil
		a.ListLoading = true
		a.ListHasMore = false
		a.ErrText = ""
	}
	a.liveSeq++
	seq := a.liveSeq
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		page, err := provider.Rooms(ctx, areaID, 1, listPageSize)
		if win == nil {
			return
		}
		win.Update(func() {
			if seq != a.liveSeq || platform != a.Platform {
				return
			}
			a.ListLoading = false
			if err != nil {
				if !hit {
					a.ErrText = "直播间加载失败：" + err.Error()
				}
				return
			}
			rooms := convertRooms(page.Rooms)
			a.Rooms = rooms
			a.ListHasMore = page.HasMore
			a.feedCache[key] = feedEntry{
				rooms: rooms, page: 1, hasMore: page.HasMore, at: time.Now(),
			}
			// 只有「从骨架换成卡片」才播入场动画。
			//
			// 缓存命中（哪怕已经过期）时列表早就在屏幕上了，这时候再整屏淡入
			// 一次就是用户说的「切分区闪一下」——而且只在缓存超过 60 秒时出现，
			// 所以看起来是偶尔复现。内容换成新的不再有动画，滚动位置也不跳。
			if !hit {
				a.resetCards()
			}
		})
	}()
}

// loadMoreRooms 追加下一页：滚动到距底部 320px 以内时由触底哨兵触发。
//
// 三道闸：正在拉首屏、已经在拉下一页、上游说没有更多，都不重复发请求
// （baseline 用 busyRef 防的是同一件事——滚动事件会连着来好几次）。
func (a *App) loadMoreRooms() {
	if !a.LiveMode || a.ListLoading || a.ListLoadingMore || !a.ListHasMore {
		return
	}
	provider, ok := live.ProviderFor(live.Platform(a.Platform))
	if !ok {
		return
	}
	areaID := a.currentCategoryID()
	if areaID == "" {
		return
	}

	platform := a.Platform
	key := a.feedKey()
	entry := a.feedCache[key]
	next := entry.page + 1
	a.ListLoadingMore = true
	win := a.Win

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		page, err := provider.Rooms(ctx, areaID, next, listPageSize)
		if win == nil {
			return
		}
		win.Update(func() {
			// 先复位：即使这一页属于已经切走的分区，也不能让「正在加载更多」
			// 的标记挂在新分区上，否则新列表再也翻不了页。
			a.ListLoadingMore = false
			// 期间切了平台或分区：这一页属于旧列表，直接丢
			if platform != a.Platform || a.feedKey() != key {
				return
			}
			if err != nil {
				// 保留 hasMore：下次滚动到底还能再试一次，不把列表卡死
				return
			}

			// 上游翻页偶尔会返回重复房间，按房间号去重（baseline 同样处理）
			seen := make(map[string]bool, len(a.Rooms))
			for _, s := range a.Rooms {
				seen[s.RoomID] = true
			}
			added := make([]Streamer, 0, len(page.Rooms))
			for _, s := range convertRooms(page.Rooms) {
				if seen[s.RoomID] {
					continue
				}
				seen[s.RoomID] = true
				added = append(added, s)
			}

			// 不设上限：一直追加到上游没有下一页为止。
			// 数据本身很小（一条一两百字节），真正贵的是封面图，
			// 而列表是虚拟化的——不可见的卡片根本不会请求图片。
			rooms := append(a.Rooms, added...)
			hasMore := page.HasMore
			a.Rooms = rooms
			a.ListHasMore = hasMore
			a.feedCache[key] = feedEntry{
				rooms: rooms, page: next, hasMore: hasMore, at: time.Now(),
			}
		})
	}()
}

// reloadRooms 在切换分区后按需重新拉列表。
func (a *App) reloadRooms() {
	if a.LiveMode {
		a.loadRooms()
	}
}

const (
	listPageSize = 40
	// prefetchCovers 是预取首屏时连带拉多少张封面。
	//
	// 取一整页：List 除了可见的那几行，还会多建几行（list.md 的
	// "the rows in view, and a few beyond"），窗口宽的时候六行就是三十多张。
	// 只备两三行的话，切过去第一帧会有几张卡空着封面，等下载回来才补上。
	prefetchCovers = listPageSize
	// feedTTL 是分区列表的缓存时间：60s 内切回同一个分区直接渲染缓存，
	// 不重新请求（baseline 的 feedCache 是同一个策略）。
	feedTTL = 60 * time.Second
	// loadMoreMargin 是触底预取的距离：距底部还有这么多 DIP 就开始拉下一页，
	// 滚到底时数据往往已经回来了（baseline 的 PREFETCH_THRESHOLD）。
	loadMoreMargin float32 = 320
)

// feedEntry 是某个分区已经加载到的位置。
type feedEntry struct {
	rooms   []Streamer
	page    int
	hasMore bool
	at      time.Time
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
			Platform:  string(r.Platform),
			RoomID:    r.RoomID,
			Name:      r.Nickname,
			Title:     r.Title,
			Category:  r.Area,
			Viewers:   int(r.Viewers),
			Live:      r.Live,
			Hue:       float32(i * 37 % 360),
			CoverURL:  r.Cover,
			AvatarURL: r.Avatar,
		})
	}
	return out
}

// currentCategoryIDs 是当前一级分区下各二级分区的接口 id。
func (a *App) currentCategoryIDs() []string { return a.currentGroup().CategoryIDs }

// ---------------------------------------------------------------- 顶栏搜索

const (
	// searchDebounce 对齐 baseline 搜索框的 220ms 防抖：
	// 连续输入只在停下来之后发一次请求。
	searchDebounce = 220 * time.Millisecond
	// searchTimeout 比列表请求短：搜索是前台交互，等不起 20 秒。
	searchTimeout = 12 * time.Second
)

// syncSearch 在查询词或平台变化时启动一次防抖搜索。
//
// view 每帧重建，所以用「已经发起的查询词」判断，只有真的变了才调度一次；
// 连续输入时更早的响应靠代次丢弃（baseline 用 cancelled 标记做同一件事）。
func (a *App) syncSearch() {
	q := strings.TrimSpace(a.SearchQuery)
	if q == a.searchQuery && a.Platform == a.searchPlatform {
		return
	}
	a.searchQuery, a.searchPlatform = q, a.Platform
	a.SearchHit = 0
	seq := a.searchSeq.Add(1)

	if q == "" {
		a.SearchResults, a.SearchLoading, a.SearchErr = nil, false, ""
		return
	}
	// 视图测试没有窗口：不发真实请求，也不留后台 goroutine。
	if a.Win == nil {
		a.SearchLoading = false
		return
	}
	provider, ok := live.ProviderFor(live.Platform(a.Platform))
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

// ensureSubFeed 在当前选中的订阅变化时拉第一页。
//
// 订阅页的平台和分区可以跟首页完全不同，所以它有一套自己的列表状态，
// 但缓存跟首页共用 feedCache——同一个分区在哪边看都是同一份数据。
func (a *App) ensureSubFeed() {
	if !a.LiveMode || len(a.Subs) == 0 {
		return
	}
	if a.SubIndex < 0 || a.SubIndex >= len(a.Subs) {
		return
	}
	sub := a.Subs[a.SubIndex]
	if sub.CategoryID == "" {
		// 旧版本存下来的订阅没有分区 id：先按名字回填
		a.resolveSubID(a.SubIndex)
		return
	}
	key := subFeedKey(sub)
	if key == a.subKey {
		return
	}
	a.subKey = key
	a.subSeq++
	a.SubLoadingMore = false

	if _, ok := live.ProviderFor(live.Platform(sub.PlatformID)); !ok {
		a.SubRooms, a.SubHasMore, a.SubLoading = nil, false, false
		a.SubErr = "该平台暂未接入"
		return
	}

	if cached, hit := a.feedCache[key]; hit {
		a.SubRooms, a.SubHasMore, a.SubLoading, a.SubErr = cached.rooms, cached.hasMore, false, ""
		if time.Since(cached.at) < feedTTL {
			return
		}
	} else {
		a.SubRooms, a.SubHasMore, a.SubLoading, a.SubErr = nil, false, true, ""
	}
	a.fetchSubPage(sub.PlatformID, sub.CategoryID, key, a.subSeq, 1)
}

// fetchSubPage 拉订阅页的第 page 页；page > 1 时追加。
func (a *App) fetchSubPage(platform, areaID, key string, seq, page int) {
	provider, ok := live.ProviderFor(live.Platform(platform))
	if !ok {
		return
	}
	win := a.Win
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		pageData, err := provider.Rooms(ctx, areaID, page, listPageSize)
		if win == nil {
			return
		}
		win.Update(func() {
			if seq != a.subSeq {
				return
			}
			a.SubLoading, a.SubLoadingMore = false, false
			if err != nil {
				if page == 1 {
					a.SubErr = "直播间加载失败：" + err.Error()
				}
				return
			}
			rooms := convertRooms(pageData.Rooms)
			if page == 1 {
				a.SubRooms = rooms
			} else {
				// 上游翻页会返回重复房间，按房间号去重
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
			hasMore := pageData.HasMore
			a.SubHasMore = hasMore
			a.feedCache[key] = feedEntry{
				rooms: a.SubRooms, page: page, hasMore: hasMore, at: time.Now(),
			}
		})
	}()
}

// loadMoreSubRooms 是订阅页的触底预取。
func (a *App) loadMoreSubRooms() {
	if !a.LiveMode || a.SubLoading || a.SubLoadingMore || !a.SubHasMore {
		return
	}
	if a.SubIndex < 0 || a.SubIndex >= len(a.Subs) {
		return
	}
	sub := a.Subs[a.SubIndex]
	if sub.CategoryID == "" {
		return
	}
	key := subFeedKey(sub)
	next := a.feedCache[key].page + 1
	a.SubLoadingMore = true
	a.fetchSubPage(sub.PlatformID, sub.CategoryID, key, a.subSeq, next)
}

// resolveSubID 给没有分区 id 的旧订阅回填 id：
// 拿该平台的二级分区表按「一级分组名 + 二级分区名」反查，命中就写回去。
func (a *App) resolveSubID(i int) {
	if i < 0 || i >= len(a.Subs) || a.Win == nil {
		return
	}
	sub := a.Subs[i]
	provider, ok := live.ProviderFor(live.Platform(sub.PlatformID))
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
