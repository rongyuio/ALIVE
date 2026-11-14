package live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 虎牙适配层：分区来自随包静态表，列表与搜索走两个公开的匿名接口。
// 协议细节参考 DTV baseline 的 platforms/huya（那边已在线跑过），这里只保留
// 数据层要用的部分，并按本包的 cache / limiter / 错误分类重写。
const (
	huyaAreaTTL   = 30 * time.Minute
	huyaListTTL   = 15 * time.Second
	huyaSearchTTL = 60 * time.Second

	// 上游完全忽略 iPageSize，永远按 120 条/页返回，iTotalPage 也是按 120 算的。
	// 所以这里的“页”只是逻辑页，请求时要换算成上游页再切片。
	huyaUpstreamPageSize = 120

	// 与 UI 的 listPageSize 一致；调用方给 0 或超过上游单页容量时回落到它。
	huyaDefaultPageSize = 40

	huyaSearchRows = 20

	// 虎牙没有明确的验证码页，风控通常表现为 403 / 换验证页 / 空响应，
	// 冷却 60s 与 B 站保持同一节奏。
	huyaBlockCooldown = 60 * time.Second

	huyaLiveListURL = "https://live.huya.com/liveHttpUI/getLiveList"
	huyaSearchURL   = "https://search.cdn.huya.com/"
)

type huyaProvider struct {
	api    *apiClient
	areas  *cache[[]CategoryGroup]
	rooms  *cache[Page]
	search *cache[[]Room]
	lim    *limiter
}

func newHuya() *huyaProvider {
	return &huyaProvider{
		// 虎牙接口要求 Referer 来自站内页面，缺了容易拿到空响应。
		api:    newAPIClient(Huya, "https://www.huya.com/"),
		areas:  newCache[[]CategoryGroup](),
		rooms:  newCache[Page](),
		search: newCache[[]Room](),
		lim:    newLimiter(2, 150*time.Millisecond),
	}
}

func (p *huyaProvider) Platform() Platform { return Huya }

// Categories 返回分区树。数据是随包静态表，没有网络请求，也就没有限流。
func (p *huyaProvider) Categories(ctx context.Context) ([]CategoryGroup, error) {
	return p.areas.load("all", huyaAreaTTL, func() ([]CategoryGroup, error) {
		return parseHuyaCategories()
	})
}

func (p *huyaProvider) Rooms(ctx context.Context, areaID string, page, size int) (Page, error) {
	gid, err := normalizeHuyaGID(areaID)
	if err != nil {
		return Page{}, err
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > huyaUpstreamPageSize {
		size = huyaDefaultPageSize
	}

	// 逻辑页换算成上游页；同一上游页内的不同逻辑页要各自切片，
	// 所以缓存键带上原始 page/size 而不是上游页码。
	offset := (page - 1) * size
	upstreamPage := offset/huyaUpstreamPageSize + 1

	key := fmt.Sprintf("%s|%d|%d", gid, page, size)
	return p.rooms.load(key, huyaListTTL, func() (Page, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return Page{}, err
		}
		defer release()

		q := url.Values{}
		q.Set("iGid", gid)
		q.Set("iPageNo", strconv.Itoa(upstreamPage))
		// 传了也不生效，显式带上只为对齐上游预期；真正决定页宽的是常量。
		q.Set("iPageSize", strconv.Itoa(huyaUpstreamPageSize))

		body, err := p.api.get(ctx, "直播列表", huyaLiveListURL+"?"+q.Encode(), nil)
		if err != nil {
			return Page{}, p.wrap("直播列表", err)
		}
		result, err := parseHuyaLiveList(body, page, size)
		if err != nil {
			return Page{}, p.wrap("直播列表", err)
		}
		return result, nil
	})
}

func (p *huyaProvider) Search(ctx context.Context, keyword string) ([]Room, error) {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	return p.search.load(kw, huyaSearchTTL, func() ([]Room, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return nil, err
		}
		defer release()

		q := url.Values{}
		q.Set("m", "Search")
		q.Set("do", "getSearchContent")
		q.Set("q", kw)
		q.Set("uid", "0")
		q.Set("v", "1")
		q.Set("typ", "-5") // -5 = 只看主播
		q.Set("livestate", "0")
		q.Set("rows", strconv.Itoa(huyaSearchRows))
		q.Set("start", "0")

		body, err := p.api.get(ctx, "搜索", huyaSearchURL+"?"+q.Encode(), header{
			"Referer": "https://www.huya.com/search/",
			"Origin":  "https://www.huya.com",
			"Accept":  "*/*",
		})
		if err != nil {
			return nil, p.wrap("搜索", err)
		}
		rooms, err := parseHuyaSearch(body)
		if err != nil {
			return nil, p.wrap("搜索", err)
		}
		return rooms, nil
	})
}

// wrap 把错误分成风控和上游业务错误两类。风控还要让 limiter 冷却，
// 否则下一次刷新的并发请求会继续撞在同一个风控上。
func (p *huyaProvider) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBlocked) {
		p.lim.block(huyaBlockCooldown)
		return fmt.Errorf("%w: 虎牙%s %v", ErrBlocked, op, err)
	}
	// 取消与超时原样上抛，上层要区分“用户切走了”和“上游出错”。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return err
	}
	return &UpstreamError{Platform: Huya, Op: op, Message: err.Error()}
}

/* ------------------------------------------------------------- 列表解析 */

// huyaLiveList 只声明用到的字段；iTotal 可能上百万，用 float 解析。
type huyaLiveList struct {
	PageNo    int              `json:"iPageNo"`
	PageSize  int              `json:"iPageSize"`
	Total     float64          `json:"iTotal"`
	TotalPage float64          `json:"iTotalPage"`
	Rooms     []map[string]any `json:"vList"`
}

// parseHuyaLiveList 把一个上游页映射成逻辑页。
//
// 上游固定 120 条/页且不认 iPageSize，因此调用方要的 page/size 得在这里
// 换算成「上游第几页 + 页内偏移」再切片；hasMore 优先看 iTotalPage，
// 缺失时退化成“本页满员”。
func parseHuyaLiveList(body []byte, page, size int) (Page, error) {
	var resp huyaLiveList
	if err := json.Unmarshal(body, &resp); err != nil {
		// 正常响应一定是 JSON；解不出来基本是被换成了验证页或空页。
		return Page{}, fmt.Errorf("%w: 直播列表响应不是 JSON: %v", ErrBlocked, err)
	}

	offset := (page - 1) * size
	start := offset % huyaUpstreamPageSize
	end := start + size
	if end > len(resp.Rooms) {
		end = len(resp.Rooms)
	}
	var items []map[string]any
	if start < end {
		items = resp.Rooms[start:end]
	}

	rooms := make([]Room, 0, len(items))
	for _, raw := range items {
		if room, ok := mapHuyaLiveRoom(raw); ok {
			rooms = append(rooms, room)
		}
	}

	totalPages := int(resp.TotalPage)
	hasMore := end < len(resp.Rooms)
	if !hasMore {
		switch {
		case totalPages > 0:
			hasMore = offset/huyaUpstreamPageSize+1 < totalPages
		case resp.Total > 0:
			hasMore = float64(offset+len(rooms)) < resp.Total
		default:
			hasMore = len(resp.Rooms) >= huyaUpstreamPageSize
		}
	}
	return Page{Rooms: rooms, HasMore: hasMore}, nil
}

func mapHuyaLiveRoom(raw map[string]any) (Room, bool) {
	roomID := huyaJSONString(raw["lProfileRoom"])
	if roomID == "" {
		return Room{}, false
	}
	nickname := huyaJSONString(raw["sNick"])
	if nickname == "" {
		nickname = "房间 " + roomID
	}
	title := huyaJSONString(raw["sIntroduction"])
	if title == "" {
		title = huyaJSONString(raw["sRoomName"])
	}
	return Room{
		Platform: Huya,
		RoomID:   roomID,
		Title:    strings.TrimSpace(title),
		Nickname: strings.TrimSpace(nickname),
		Cover:    httpsURL(huyaJSONString(raw["sScreenshot"])),
		Avatar:   httpsURL(huyaJSONString(raw["sAvatar180"])),
		Viewers:  int64(huyaJSONFloat(raw["lUserCount"])),
		// 列表接口直接带分区名，省一次请求
		Area: firstNonEmpty(huyaJSONString(raw["sGameFullName"]), huyaJSONString(raw["sGameHostName"])),
		Live: true, // 该接口只返回在播房间
	}, true
}

/* ------------------------------------------------------------- 搜索解析 */

// huyaSearch 的 response 是「结果集 id → 结果」的 map，键有 1/3/122/1024…
// 主播结果在 "1"（结构体），其它键有的直接是数组，所以只能逐个容错解析。
type huyaSearch struct {
	Header struct {
		Status int `json:"status"`
	} `json:"responseHeader"`
	Response map[string]json.RawMessage `json:"response"`
}

type huyaSearchSet struct {
	NumFound int              `json:"numFound"`
	Start    int              `json:"start"`
	Docs     []map[string]any `json:"docs"`
}

func parseHuyaSearch(body []byte) ([]Room, error) {
	var resp huyaSearch
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: 搜索响应不是 JSON: %v", ErrBlocked, err)
	}
	if resp.Header.Status != 0 {
		return nil, &UpstreamError{
			Platform: Huya,
			Op:       "搜索",
			Message:  fmt.Sprintf("responseHeader.status=%d", resp.Header.Status),
		}
	}

	sets := make([]json.RawMessage, 0, len(resp.Response))
	if raw, ok := resp.Response["1"]; ok {
		sets = append(sets, raw)
	} else {
		// 上游万一换了主播结果集的键名，就把所有结果集扫一遍。
		for _, raw := range resp.Response {
			sets = append(sets, raw)
		}
	}

	rooms := make([]Room, 0, huyaSearchRows)
	seen := make(map[string]struct{}, huyaSearchRows)
	for _, rawSet := range sets {
		var set huyaSearchSet
		// 数组形式的结果集解析会失败，跳过即可
		if err := json.Unmarshal(rawSet, &set); err != nil {
			continue
		}
		for _, doc := range set.Docs {
			room, ok := mapHuyaSearchRoom(doc)
			if !ok {
				continue
			}
			if _, dup := seen[room.RoomID]; dup {
				continue
			}
			seen[room.RoomID] = struct{}{}
			rooms = append(rooms, room)
		}
	}
	return rooms, nil
}

func mapHuyaSearchRoom(doc map[string]any) (Room, bool) {
	roomID := huyaJSONString(doc["room_id"])
	if roomID == "" {
		return Room{}, false
	}
	nickname := huyaJSONString(doc["game_nick"])
	if nickname == "" {
		nickname = "房间 " + roomID
	}
	return Room{
		Platform: Huya,
		RoomID:   roomID,
		Title:    strings.TrimSpace(huyaJSONString(doc["live_intro"])),
		Nickname: strings.TrimSpace(nickname),
		// 搜索结果不带直播截图，卡片会回落到占位图，这里不硬凑封面。
		Avatar: httpsURL(huyaJSONString(doc["game_avatarUrl180"])),
		Area:   firstNonEmpty(huyaJSONString(doc["game_name"]), huyaJSONString(doc["rec_game_name"])),
		Live:   huyaJSONBool(doc["gameLiveOn"]),
	}, true
}

/* ------------------------------------------------------------- 分区 id */

// normalizeHuyaGID 把分区 id 归一成接口的 iGid。
//
// 订阅里可能存着网页地址（https://www.huya.com/g/lol），而 iGid 只认数字；
// 静态分区表里有 slug→数字的映射，正好用来兜底。
func normalizeHuyaGID(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("live: 虎牙分区 id 为空")
	}
	if i := strings.LastIndex(text, "/g/"); i >= 0 {
		text = text[i+3:]
	}
	if i := strings.IndexAny(text, "?#"); i >= 0 {
		text = text[:i]
	}
	text = strings.TrimPrefix(strings.Trim(text, "/"), "g_")
	// 走到这里还带 / 或 : 说明给的是别的页面地址，取不到分区段。
	if text == "" || strings.ContainsAny(text, "/:") {
		return "", fmt.Errorf("live: 虎牙分区 id %q 无法识别", raw)
	}
	if huyaIsDigits(text) {
		return text, nil
	}
	if id, ok := huyaSlugGID(text); ok {
		return id, nil
	}
	// 不是数字也不在分区表里：交给上游判断，错误会以 UpstreamError 上抛。
	return text, nil
}

func huyaIsDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

/* ------------------------------------------------------------- JSON 取值 */

// 虎牙同一字段有时是数字、有时是字符串；取严格类型会整页解析失败，
// 所以列表和搜索的房间都用 map[string]any + 柔性取值。

func huyaJSONString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case float64:
		if value == float64(int64(value)) {
			return strconv.FormatInt(int64(value), 10)
		}
		return strconv.FormatFloat(value, 'f', -1, 64)
	case json.Number:
		return value.String()
	case bool:
		if value {
			return "1"
		}
		return "0"
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", value)
	}
}

// huyaJSONFloat 读人气值：常见 2.52e+06 这类科学计数法，也可能被包成字符串。
func huyaJSONFloat(v any) float64 {
	switch value := v.(type) {
	case float64:
		return value
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0
		}
		return f
	case json.Number:
		f, err := value.Float64()
		if err != nil {
			return 0
		}
		return f
	case bool:
		if value {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func huyaJSONBool(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case float64:
		return value != 0
	case string:
		text := strings.ToLower(strings.TrimSpace(value))
		return text == "1" || text == "true"
	default:
		return false
	}
}
