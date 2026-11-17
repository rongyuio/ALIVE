package live

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
	bilibiliSearch "github.com/rongyuio/bilibili/search"
)

// B 站适配层：协议、Cookie、WBI、错误解码全部交给 github.com/rongyuio/bilibili，
// 这里只做参数归一化、缓存、限流和字段映射。
const (
	biliPageSize   = 40
	biliAreaTTL    = 30 * time.Minute
	biliListTTL    = 15 * time.Second
	biliSearchWait = 400 * time.Millisecond
	biliCoverWidth = 480

	// biliAvatarSize 是头像请求的边长。头像最大画到 40 DIP（关注列表），
	// 高 DPI 下 2 倍也只有 80 像素；120 留余量，同时避开上游默认的 480 宽。
	biliAvatarSize = 120
)

type bilibiliProvider struct {
	api   *biblib.Client
	lc    *bilibiliLive.Client
	sc    *bilibiliSearch.Client
	areas *cache[[]CategoryGroup]
	rooms *cache[Page]
	// uids 缓存「房间号 → 主播 uid」。刷新关注列表时每个房间都要先 get_info
	// 换 uid，而 uid 基本不会变：缓存住能把整轮刷新的请求数砍掉一半。
	uids *cache[biliRoomRef]
	lim  *limiter
	// auth 是扫码登录；它和取流共用上面这个 api 会话。
	auth *qrAuth
}

func newBilibili() *bilibiliProvider {
	api := biblib.New()
	p := &bilibiliProvider{
		api: api,
		lc:  bilibiliLive.New(api),
		// 搜索必须用独立会话：直播接口会下发 buvid3 等 Cookie，
		// 同一会话里再搜会稳定返回空 result。
		sc:    bilibiliSearch.New(biblib.New()),
		areas: newCache[[]CategoryGroup](),
		rooms: newCache[Page](),
		uids:  newCache[biliRoomRef](),
		// 关注刷新并发：前台 6 个 worker，后端不限流。
		// 平台限流器开到 6 并发，刷新时 6 个 worker 不会互相卡住。
		// 60ms 只用来避免同一瞬间把 6 个请求一起打出去；真被风控时
		// block() 仍会把整个平台冷下来。
		lim: newLimiter(refreshConcurrency, 60*time.Millisecond),
	}
	p.auth = newBiliAuth(p)
	return p
}

func (p *bilibiliProvider) Platform() Platform { return Bilibili }

func (p *bilibiliProvider) Categories(ctx context.Context) ([]CategoryGroup, error) {
	return p.areas.load("all", biliAreaTTL, func() ([]CategoryGroup, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()

		areas, err := p.lc.GetLiveAreaList(ctx)
		if err != nil {
			return nil, p.wrap("分区树", err)
		}

		groups := make([]CategoryGroup, 0, len(areas))
		for _, g := range areas {
			if len(g.List) == 0 {
				continue
			}
			parentID := strconv.Itoa(g.ID)
			cats := make([]Category, 0, len(g.List))
			for _, sub := range g.List {
				childID := strings.TrimSpace(sub.ID)
				if childID == "" {
					continue
				}
				pid := strings.TrimSpace(sub.ParentID)
				if pid == "" {
					pid = parentID
				}
				cats = append(cats, Category{
					ID:   pid + "_" + childID,
					Name: strings.TrimSpace(sub.Name),
				})
			}
			if len(cats) == 0 {
				continue
			}
			groups = append(groups, CategoryGroup{
				ID:         parentID,
				Name:       strings.TrimSpace(g.Name),
				Categories: cats,
			})
		}
		return groups, nil
	})
}

func (p *bilibiliProvider) Rooms(ctx context.Context, areaID string, page, size int) (Page, error) {
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 50 {
		size = biliPageSize
	}
	parent, child, err := splitAreaID(areaID)
	if err != nil {
		return Page{}, err
	}

	key := fmt.Sprintf("%d|%d|%d|%d", parent, child, page, size)
	return p.rooms.load(key, biliListTTL, func() (Page, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return Page{}, err
		}
		defer release()

		list, err := p.lc.GetLiveAreaRoomList(ctx, bilibiliLive.GetLiveAreaRoomListParam{
			ParentAreaID: parent,
			AreaID:       child,
			Page:         page,
			PageSize:     size,
		})
		if err != nil {
			return Page{}, p.wrap("直播列表", err)
		}

		rooms := make([]Room, 0, len(list))
		for _, r := range list {
			if r.RoomID <= 0 {
				continue
			}
			rooms = append(rooms, Room{
				Platform: Bilibili,
				RoomID:   strconv.FormatInt(r.RoomID, 10),
				Title:    strings.TrimSpace(r.Title),
				Nickname: strings.TrimSpace(r.Uname),
				Cover:    biliImage(firstNonEmpty(r.Cover, r.UserCover, r.SystemCover)),
				Avatar:   biliAvatar(r.Face),
				Viewers:  int64(r.Online),
				Area:     firstNonEmpty(r.AreaV2Name, r.AreaName, r.ParentName),
				Live:     true, // 该接口只返回在播房间
			})
		}
		// 上游没有总数也没有 has_more：满页即认为还有下一页。
		return Page{Rooms: rooms, HasMore: len(list) >= size}, nil
	})
}

func (p *bilibiliProvider) Search(ctx context.Context, keyword string) ([]Room, error) {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()

	param := bilibiliSearch.CategorySearchParam{
		SearchType: "live_room",
		Keyword:    kw,
		Page:       1,
	}
	result, err := p.sc.CategorySearch(ctx, param)
	if err != nil {
		return nil, p.wrap("搜索", err)
	}

	// 上游偶发返回 code=0、numResults>0 但 result 为空（限流/瞬态）。
	// 直接透给 UI 会显示「无结果」，等一小会儿重试一次。
	if len(result.Result) == 0 && result.NumResults > 0 {
		select {
		case <-time.After(biliSearchWait):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if retry, err := p.sc.CategorySearch(ctx, param); err == nil {
			result = retry
		}
	}
	if len(result.Result) == 0 {
		return []Room{}, nil
	}

	// live_room 的 result 正常是数组，个别情况会回落成 {live_room: [...]}。
	var items []*bilibiliSearch.CategorySearchLiveRoomItem
	if err := json.Unmarshal(result.Result, &items); err != nil {
		var wrapped bilibiliSearch.CategorySearchLiveResult
		if err := json.Unmarshal(result.Result, &wrapped); err != nil {
			return nil, p.wrap("搜索解析", err)
		}
		items = wrapped.LiveRoom
	}

	rooms := make([]Room, 0, len(items))
	for _, item := range items {
		if item == nil || item.RoomID <= 0 {
			continue
		}
		rooms = append(rooms, Room{
			Platform: Bilibili,
			RoomID:   strconv.Itoa(item.RoomID),
			Title:    stripTags(item.Title),
			Nickname: stripTags(item.Uname),
			Cover:    biliImage(firstNonEmpty(item.Cover, item.UserCover)),
			Avatar:   biliAvatar(item.Uface),
			Viewers:  int64(item.Online),
			Area:     item.CateName,
			Live:     item.LiveStatus == 1,
		})
	}
	return rooms, nil
}

// wrap 把库的错误分成「风控」和「上游业务错误」两类。
func (p *bilibiliProvider) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(msg, "412") || strings.Contains(msg, "403") ||
		strings.Contains(lower, "captcha") || strings.Contains(msg, "风控") {
		p.lim.block(60 * time.Second)
		return ErrBlocked
	}
	return &UpstreamError{Platform: Bilibili, Op: op, Message: msg}
}

// splitAreaID 把 "父_子" 拆成两个 id；只给子分区时父分区传 0（库表示不限）。
func splitAreaID(id string) (parent, child int64, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return 0, 0, fmt.Errorf("live: 分区 id 为空")
	}
	if i := strings.IndexByte(id, '_'); i > 0 {
		parent, err = strconv.ParseInt(id[:i], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("live: 分区 id %q 格式错误", id)
		}
		child, err = strconv.ParseInt(id[i+1:], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("live: 分区 id %q 格式错误", id)
		}
		return parent, child, nil
	}
	child, err = strconv.ParseInt(id, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("live: 分区 id %q 格式错误", id)
	}
	return 0, child, nil
}

var tagPattern = regexp.MustCompile(`<[^>]+>`)

func stripTags(s string) string {
	return strings.TrimSpace(tagPattern.ReplaceAllString(s, ""))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func httpsURL(raw string) string {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "//"):
		return "https:" + raw
	case strings.HasPrefix(raw, "http://"):
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

// biliImage 给封面加上宽度参数：列表卡片用 480 宽足够，
// 比原图少一个数量级的流量。已经有 @ 参数的地址不再追加。
func biliImage(raw string) string {
	u := httpsURL(raw)
	if u == "" || strings.Contains(u, "@") {
		return u
	}
	return fmt.Sprintf("%s@%dw.webp", u, biliCoverWidth)
}

// biliAvatar 把头像地址改写成一枚静态小图。
//
// 上游的 face 自带 `@480w.webp`。源图是 GIF 的头像（B 站允许动图头像）会因为
// 这个参数被 CDN 转成动画 WebP，而 Go 的 webp 解码器只认单帧 VP8/VP8L，
// 整张图直接解不出来——界面上就是永远停在字母占位。换成 `.jpg` 取的是首帧
// 静态图，顺带把 480 宽降到头像真正需要的尺寸。
func biliAvatar(raw string) string {
	u := httpsURL(raw)
	if u == "" {
		return ""
	}
	if i := strings.IndexByte(u, '@'); i >= 0 {
		u = u[:i]
	}
	return fmt.Sprintf("%s@%dw_%dh.jpg", u, biliAvatarSize, biliAvatarSize)
}
