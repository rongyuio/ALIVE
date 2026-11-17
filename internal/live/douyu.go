package live

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 斗鱼数据层：分区树、目录列表、主播搜索。
//
// 三个接口分属两套服务：
//   - m.douyu.com/api/cate/list 是移动端分区接口，扁平分发 cate1/cate2；
//   - www.douyu.com/gapi/rkc/directory 是网页目录接口，按二级分区分页；
//   - www.douyu.com/japi/search/api/searchUser 是网页搜索，结果按 type 混排。
//
// 旧的 m.douyu.com/hgapi/live/cate/newRecList 已固定返回
// {"error":1,"msg":"系统异常，请重试"}，列表只能走目录接口。
const (
	douyuCateURL = "https://m.douyu.com/api/cate/list"
	// 路径段是 "2_{cate2Id}"；limit 参数服务端根本不看，见 douyuUpstreamPageSize。
	douyuDirectoryURL = "https://www.douyu.com/gapi/rkc/directory/2_%s/%d"
	douyuSearchURL    = "https://www.douyu.com/japi/search/api/searchUser"

	// 分区接口挂在 m 站，用移动端 UA 更贴近它的正常调用方。
	douyuMobileUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 13_2_3 like Mac OS X) AppleWebKit/605.1.15 " +
		"(KHTML, like Gecko) Version/13.0.3 Mobile/15E148 Safari/604.1"

	douyuCoverPrefix  = "https://rpic.douyucdn.cn/"
	douyuAvatarPrefix = "https://apic.douyucdn.cn/upload/"

	// 目录接口无视 limit，实测固定一次返回 120 条（最后一页可能更少）。
	// 逻辑分页先按这个粒度换算成上游页号，再在批内切片。
	douyuUpstreamPageSize = 120
	douyuPageSize         = 40
	douyuSearchPageSize   = 20

	douyuAreaTTL   = 30 * time.Minute
	douyuListTTL   = 15 * time.Second
	douyuSearchTTL = 60 * time.Second

	// 斗鱼验证码一般几十秒后自动放行，冷却一分钟足够。
	douyuBlockCool = 60 * time.Second

	// 随机 did 生成失败时退回网页端的默认游客标识。
	douyuFallbackDID = "10000000000000000000000000001501"
)

// douyuProvider 持有斗鱼的 HTTP 出口、游客标识、缓存与限流器。
type douyuProvider struct {
	api      *apiClient
	did      string
	areas    *cache[[]CategoryGroup]
	pages    *cache[douyuDirectoryPage]
	searches *cache[[]Room]
	lim      *limiter
	// signer 缓存取流用的加密描述符（websec/getEncryption），
	// 5 分钟内可复用，避免每个清晰度都重新握手。
	signer *douyuSigner
	// authCookie 是扫码登录拿到的会话；空串表示游客。
	// 它和 did 必须配套使用，见 douyuCookieHeader。
	authMu     sync.RWMutex
	authCookie string
	// auth 是扫码登录；它和取流共用上面这个 api 会话。
	auth *qrAuth
}

func newDouyu() *douyuProvider {
	p := &douyuProvider{
		api: newAPIClient(Douyu, "https://www.douyu.com/"),
		// 斗鱼按 did 做游客限流：固定一个进程内随机 did，
		// 比每次请求现编一个更像正常客户端，也避免同一会话被当成多个游客。
		did:      douyuDeviceID(),
		areas:    newCache[[]CategoryGroup](),
		pages:    newCache[douyuDirectoryPage](),
		searches: newCache[[]Room](),
		// 关注刷新并发 6：6 个 worker 不会互相卡住。
		// 60ms 间隔只防同一瞬间齐发，风控冷却机制照常生效。
		lim:    newLimiter(refreshConcurrency, 60*time.Millisecond),
		signer: &douyuSigner{},
	}
	p.auth = newDouyuAuth(p)
	return p
}

func (p *douyuProvider) Platform() Platform { return Douyu }

func (p *douyuProvider) Categories(ctx context.Context) ([]CategoryGroup, error) {
	return p.areas.load("all", douyuAreaTTL, func() ([]CategoryGroup, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()

		body, err := p.api.get(ctx, "分区树", douyuCateURL, header{
			"User-Agent": douyuMobileUA,
			"Referer":    "https://m.douyu.com/",
		})
		if err != nil {
			return nil, p.wrap("分区树", err)
		}
		groups, err := parseDouyuCategories(body)
		if err != nil {
			return nil, p.wrap("分区树", err)
		}
		return groups, nil
	})
}

// Rooms 返回某个二级分区下的一页直播间。
//
// 上游目录接口按自己的 120 条一批分页，page/size 是这层暴露给 UI 的逻辑分页：
// 逻辑页先换算到上游页，必要时跨批取一次，再切片。这样 size=40 时不会因为
// 上游一批 120 条而让第 2 页跳过前 80 条。
func (p *douyuProvider) Rooms(ctx context.Context, areaID string, page, size int) (Page, error) {
	areaID = strings.TrimSpace(areaID)
	if areaID == "" {
		return Page{}, fmt.Errorf("live: 斗鱼分区 id 为空")
	}
	if !douyuDigits(areaID) {
		return Page{}, fmt.Errorf("live: 斗鱼分区 id %q 格式错误", areaID)
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = douyuPageSize
	}
	// 超过上游一批就没法用一次切片表达，直接按一批封顶。
	if size > douyuUpstreamPageSize {
		size = douyuUpstreamPageSize
	}

	upstreamPage, start := douyuPageOffset(page, size)

	first, err := p.directory(ctx, areaID, upstreamPage)
	if err != nil {
		return Page{}, err
	}

	// 拷一份再拼：first.Rooms 是缓存里的切片，不能往它的底层数组里追加。
	rows := append([]Room(nil), first.Rooms...)
	lastPage := upstreamPage

	// 逻辑页跨过上游批边界时多取一批；size ≤ 120，最多只会跨一批。
	if start+size > len(rows) && upstreamPage < first.TotalPages {
		next, err := p.directory(ctx, areaID, upstreamPage+1)
		if err != nil {
			return Page{}, err
		}
		rows = append(rows, next.Rooms...)
		lastPage = upstreamPage + 1
	}

	sliced, moreInBatch := douyuWindow(rows, start, size)
	// 本批后面还有、或者上游还有下一页，都算还有更多。
	hasMore := moreInBatch || lastPage < first.TotalPages
	return Page{Rooms: sliced, HasMore: hasMore}, nil
}

func (p *douyuProvider) Search(ctx context.Context, keyword string) ([]Room, error) {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	return p.searches.load(kw, douyuSearchTTL, func() ([]Room, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()

		target := fmt.Sprintf("%s?kw=%s&page=1&pageSize=%d&filterType=0",
			douyuSearchURL, url.QueryEscape(kw), douyuSearchPageSize)
		// 搜索接口缺 did cookie 时更容易触发验证码，带上固定的游客标识。
		body, err := p.api.get(ctx, "搜索", target, header{
			"Referer": "https://www.douyu.com/search/",
			"Cookie":  "dy_did=" + p.did + "; acf_did=" + p.did,
		})
		if err != nil {
			return nil, p.wrap("搜索", err)
		}
		rooms, err := parseDouyuSearch(body)
		if err != nil {
			return nil, p.wrap("搜索", err)
		}
		return rooms, nil
	})
}

// directory 拉上游某一批目录数据，按「分区 + 上游页号」缓存。
func (p *douyuProvider) directory(ctx context.Context, areaID string, page int) (douyuDirectoryPage, error) {
	key := fmt.Sprintf("%s|%d", areaID, page)
	return p.pages.load(key, douyuListTTL, func() (douyuDirectoryPage, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return douyuDirectoryPage{}, err
		}
		defer release()

		target := fmt.Sprintf(douyuDirectoryURL, areaID, page)
		body, err := p.api.get(ctx, "直播列表", target, header{"Referer": "https://www.douyu.com/"})
		if err != nil {
			return douyuDirectoryPage{}, p.wrap("直播列表", err)
		}
		parsed, err := parseDouyuDirectory(body)
		if err != nil {
			return douyuDirectoryPage{}, p.wrap("直播列表", err)
		}
		return parsed, nil
	})
}

// wrap 区分风控与上游业务错误；风控时让整个平台冷却一段时间。
func (p *douyuProvider) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBlocked) {
		p.lim.block(douyuBlockCool)
		// 去掉内层已经带上的 sentinel 文案，避免「风控: 风控」重复。
		msg := strings.TrimPrefix(err.Error(), ErrBlocked.Error()+": ")
		return fmt.Errorf("%w: %s: %s", ErrBlocked, op, msg)
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return err
	}
	return &UpstreamError{Platform: Douyu, Op: op, Message: err.Error()}
}

// ---------------------------------------------------------------- 分区树

type douyuCateListResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Cate1 []douyuCate1 `json:"cate1Info"`
		Cate2 []douyuCate2 `json:"cate2Info"`
	} `json:"data"`
}

// douyuCate1 / douyuCate2 的 id 用 any：同一接口有时给数字、有时给字符串。
type douyuCate1 struct {
	ID   any    `json:"cate1Id"`
	Name string `json:"cate1Name"`
}

type douyuCate2 struct {
	ParentID any    `json:"cate1Id"`
	ID       any    `json:"cate2Id"`
	Name     string `json:"cate2Name"`
}

func parseDouyuCategories(body []byte) ([]CategoryGroup, error) {
	var payload douyuCateListResponse
	if err := decodeDouyuJSON("分区树", body, &payload); err != nil {
		return nil, err
	}
	if payload.Code != 0 {
		return nil, &UpstreamError{
			Platform: Douyu,
			Op:       "分区树",
			Message:  fmt.Sprintf("code=%d msg=%s", payload.Code, payload.Msg),
		}
	}

	groups := buildDouyuCategoryGroups(payload.Data.Cate1, payload.Data.Cate2)
	if len(groups) == 0 {
		return nil, &UpstreamError{Platform: Douyu, Op: "分区树", Message: "接口返回空分区"}
	}
	return groups, nil
}

// buildDouyuCategoryGroups 把扁平的 cate1/cate2 组装成 UI 要的分组树。
//
// 真实数据有两处坑：
//  1. 同一个 cate2Id 偶尔重复下发，只保留第一次；
//  2. 「娱乐天地」(cate1Id=2) 的「一起看」时有时无，且可能以不同 id 出现两次；
//     同名只留一个，缺失时补 208，否则网页端入口在卡片栏里会消失。
func buildDouyuCategoryGroups(cate1List []douyuCate1, cate2List []douyuCate2) []CategoryGroup {
	groups := make([]CategoryGroup, 0, len(cate1List))
	for _, c1 := range cate1List {
		cate1ID := douyuText(c1.ID)
		if cate1ID == "" {
			continue
		}

		group := CategoryGroup{
			ID:         cate1ID,
			Name:       strings.TrimSpace(c1.Name),
			Categories: make([]Category, 0, 32),
		}
		seen := make(map[string]struct{}, len(cate2List))
		hasTogetherWatch := false
		for _, c2 := range cate2List {
			if douyuText(c2.ParentID) != cate1ID {
				continue
			}
			cate2ID := douyuText(c2.ID)
			if cate2ID == "" {
				continue
			}
			if _, dup := seen[cate2ID]; dup {
				continue
			}
			seen[cate2ID] = struct{}{}

			name := strings.TrimSpace(c2.Name)
			if name == "一起看" {
				if hasTogetherWatch {
					continue
				}
				hasTogetherWatch = true
			}
			group.Categories = append(group.Categories, Category{ID: cate2ID, Name: name})
		}

		if cate1ID == "2" && !hasTogetherWatch {
			group.Categories = append(group.Categories, Category{ID: "208", Name: "一起看"})
		}
		if len(group.Categories) == 0 {
			continue
		}
		groups = append(groups, group)
	}
	return groups
}

// ---------------------------------------------------------------- 直播列表

// douyuDirectoryPage 是上游一批目录数据：房间 + 总页数。
type douyuDirectoryPage struct {
	TotalPages int
	Rooms      []Room
}

type douyuDirectoryResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Pgcnt int                  `json:"pgcnt"`
		Rl    []douyuDirectoryItem `json:"rl"`
	} `json:"data"`
}

// douyuDirectoryItem 对应 data.rl 里的一条。ol 用 any：人气可能给数字、
// 字符串或「12.3万」这类展示文案。
type douyuDirectoryItem struct {
	RoomID   any    `json:"rid"`
	Title    string `json:"rn"`
	Nickname string `json:"nn"`
	Avatar   string `json:"av"`
	Cover    string `json:"rs16"`
	CoverAlt string `json:"rs1"`
	Viewers  any    `json:"ol"`
	Area     string `json:"c2name"`
	// type 只有三级分区列表才带，1 表示直播中；二级目录接口不返回。
	Type any `json:"type"`
}

func parseDouyuDirectory(body []byte) (douyuDirectoryPage, error) {
	var payload douyuDirectoryResponse
	if err := decodeDouyuJSON("直播列表", body, &payload); err != nil {
		return douyuDirectoryPage{}, err
	}
	if payload.Code != 0 {
		return douyuDirectoryPage{}, &UpstreamError{
			Platform: Douyu,
			Op:       "直播列表",
			Message:  fmt.Sprintf("code=%d msg=%s", payload.Code, payload.Msg),
		}
	}

	rooms := make([]Room, 0, len(payload.Data.Rl))
	for _, item := range payload.Data.Rl {
		if room, ok := item.room(); ok {
			rooms = append(rooms, room)
		}
	}
	// 最后一页空是正常情况，不报错：上层要的是「到底了」，不是「加载失败」。
	return douyuDirectoryPage{TotalPages: payload.Data.Pgcnt, Rooms: rooms}, nil
}

func (item douyuDirectoryItem) room() (Room, bool) {
	roomID := douyuText(item.RoomID)
	if roomID == "" || roomID == "0" {
		return Room{}, false
	}

	room := Room{
		Platform: Douyu,
		RoomID:   roomID,
		Title:    strings.TrimSpace(item.Title),
		Nickname: strings.TrimSpace(item.Nickname),
		Cover:    douyuCover(item.Cover, item.CoverAlt),
		Avatar:   douyuAvatar(item.Avatar),
		Viewers:  douyuInteger(item.Viewers),
		Area:     strings.TrimSpace(item.Area),
		// 二级目录只列在播房间；三级列表会带 type，另行判断。
		Live: true,
	}
	if item.Type != nil {
		room.Live = douyuInteger(item.Type) == 1
	}
	if room.Nickname == "" {
		room.Nickname = "房间 " + roomID
	}
	return room, true
}

// douyuPageOffset 把逻辑页换算成「上游第几批 + 批内起点」。
func douyuPageOffset(page, size int) (upstreamPage, start int) {
	offset := (page - 1) * size
	return offset/douyuUpstreamPageSize + 1, offset % douyuUpstreamPageSize
}

// douyuWindow 从一批房间里切出 [start, start+size)；
// 第二个返回值表示切完之后本批是否还有剩余房间。
func douyuWindow(rows []Room, start, size int) ([]Room, bool) {
	if start < 0 || size <= 0 || start >= len(rows) {
		return nil, false
	}
	end := start + size
	if end > len(rows) {
		end = len(rows)
	}
	return rows[start:end], end < len(rows)
}

// ---------------------------------------------------------------- 搜索

type douyuSearchResponse struct {
	Error int    `json:"error"`
	Msg   string `json:"msg"`
	Data  struct {
		RelateUser []douyuSearchItem `json:"relateUser"`
		// 个别版本把这个字段叫 relateUserList。
		RelateUserList []douyuSearchItem `json:"relateUserList"`
	} `json:"data"`
}

type douyuSearchItem struct {
	Type       any               `json:"type"`
	AnchorInfo douyuSearchAnchor `json:"anchorInfo"`
}

// douyuSearchAnchor 只取卡片需要的字段；同一字段存在大小写两种命名。
type douyuSearchAnchor struct {
	RoomID      any    `json:"rid"`
	NickName    string `json:"nickName"`
	Nickname    string `json:"nickname"`
	RoomName    string `json:"roomName"`
	Description string `json:"description"`
	Avatar      string `json:"avatar"`
	RoomSrc     string `json:"roomSrc"`
	Category    string `json:"cateName"`
	Live        any    `json:"isLive"`
	IsLoop      any    `json:"isLoop"`
	VideoLoop   any    `json:"videoLoop"`
	BkURL       string `json:"bkUrl"`
}

func parseDouyuSearch(body []byte) ([]Room, error) {
	var payload douyuSearchResponse
	if err := decodeDouyuJSON("搜索", body, &payload); err != nil {
		return nil, err
	}
	if payload.Error != 0 {
		return nil, &UpstreamError{
			Platform: Douyu,
			Op:       "搜索",
			Message:  fmt.Sprintf("error=%d msg=%s", payload.Error, payload.Msg),
		}
	}

	items := payload.Data.RelateUser
	if len(items) == 0 {
		items = payload.Data.RelateUserList
	}

	rooms := make([]Room, 0, len(items))
	for _, item := range items {
		// type=1 才是主播；其余是分类、视频等混排项。
		if item.Type != nil && douyuInteger(item.Type) != 1 {
			continue
		}
		anchor := item.AnchorInfo

		roomID := douyuText(anchor.RoomID)
		if roomID == "" {
			// 兜底：bkUrl 形如 https://www.douyu.com/606118。
			roomID = douyuLastPathSegment(anchor.BkURL)
		}
		nickname := douyuFirst(anchor.NickName, anchor.Nickname)
		if roomID == "" || nickname == "" {
			continue
		}

		rooms = append(rooms, Room{
			Platform: Douyu,
			RoomID:   roomID,
			Title:    douyuFirst(anchor.RoomName, anchor.Description, "暂无标题"),
			Nickname: nickname,
			Cover:    douyuCover(anchor.RoomSrc, ""),
			Avatar:   douyuAvatar(anchor.Avatar),
			Area:     strings.TrimSpace(anchor.Category),
			// 搜索接口 isLive：1=直播中，2=未开播（实测即如此，别写反）；
			// 轮播（isLoop/videoLoop=1）不算直播中。
			Live: douyuInteger(anchor.Live) == 1 &&
				douyuInteger(anchor.IsLoop) != 1 &&
				douyuInteger(anchor.VideoLoop) != 1,
		})
	}
	return rooms, nil
}

// ---------------------------------------------------------------- 通用工具

// decodeDouyuJSON 解 JSON；解不出来通常是验证页或空页，按风控处理。
func decodeDouyuJSON(op string, body []byte, out any) error {
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %s 响应不是 JSON: %v", ErrBlocked, op, err)
	}
	return nil
}

// douyuText 把 JSON 里的 string/number 统一成字符串：
// 斗鱼同一字段在不同接口里类型不一致（rid 有时数字有时字符串）。
func douyuText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case json.Number:
		return x.String()
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", x))
	}
}

// douyuInteger 宽松解析整数，兼容 "12.3万" 这类人气文案。
func douyuInteger(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case json.Number:
		if n, err := x.Int64(); err == nil {
			return n
		}
		return 0
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return 0
		}
		if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return n
		}
		return douyuChineseCount(trimmed)
	default:
		return 0
	}
}

// douyuChineseCount 解析 "12.3万" / "2.3亿"。
func douyuChineseCount(s string) int64 {
	multiplier := 1.0
	switch {
	case strings.Contains(s, "亿"):
		multiplier = 1e8
		s = strings.ReplaceAll(s, "亿", "")
	case strings.Contains(s, "万"):
		multiplier = 1e4
		s = strings.ReplaceAll(s, "万", "")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f * multiplier))
}

// douyuCover 优先用 rs16、缺失时退回 rs1，并补齐 CDN 前缀。
func douyuCover(primary, fallback string) string {
	raw := strings.TrimSpace(primary)
	if raw == "" {
		raw = strings.TrimSpace(fallback)
	}
	return douyuURL(douyuCoverPrefix, raw)
}

// douyuAvatar 拼出头像地址。目录接口的 av 形如 "avatar_v3/202004/<hash>"，
// 没有扩展名；直接拼前缀会 404，正确文件名是 `<hash>_big.jpg`。
func douyuAvatar(raw string) string {
	full := douyuURL(douyuAvatarPrefix, raw)
	if full == "" {
		return ""
	}
	parsed, err := url.Parse(full)
	if err != nil || !strings.Contains(strings.ToLower(parsed.Hostname()), "douyucdn.cn") {
		return full
	}
	if !strings.Contains(parsed.Path, "/upload/") {
		return full
	}
	name := parsed.Path[strings.LastIndex(parsed.Path, "/")+1:]
	if name == "" || strings.Contains(name, ".") {
		return full
	}
	// acf_avatar 里这种形状实测带一个尾随下划线（...ec_）。直接 += "_big.jpg"
	// 会得到 __big.jpg 并 404；先去掉尾下划线再拼。
	name = strings.TrimSuffix(name, "_")
	if name == "" {
		return full
	}
	parsed.Path = parsed.Path[:strings.LastIndex(parsed.Path, "/")+1] + name + "_big.jpg"
	return parsed.String()
}

// douyuURL 给相对路径补 CDN 前缀，并把 http 升到 https。
func douyuURL(prefix, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		return "https:" + raw
	}
	if strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return prefix + strings.TrimPrefix(raw, "/")
}

// douyuFirst 返回第一个非空值（去掉首尾空白后）。
func douyuFirst(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// douyuLastPathSegment 取 URL 的最后一段，用于从 bkUrl 兜底房间号。
func douyuLastPathSegment(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	path := strings.Trim(parsed.Path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	return path
}

func douyuDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// douyuDeviceID 生成 32 位十六进制的游客 did。
func douyuDeviceID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return douyuFallbackDID
	}
	return hex.EncodeToString(buf)
}
