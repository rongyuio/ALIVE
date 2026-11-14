package live

import (
	"context"
	"crypto/rand"
	_ "embed"
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

// 抖音适配层。
//
// 抖音没有分区列表接口，分区表是 web 端写死的静态数据（内嵌
// assets/douyin/categories.json）；列表走 webcast 的 web 接口，
// 且必须带 a_bogus 签名（见 douyin_abogus.go）。签名、Cookie 与请求头
// 任何一处不一致，上游都会返回空响应而不是报错，所以风控在这里一律
// 归类成 ErrBlocked 并让 limiter 冷却。
//
// 搜索没有可选用的关键词接口（匿名一律 2483「请先登录」），语义改成
// 「按房间号查房间」，见 Search 与 douyin_room.go。
const (
	douyinPageSize      = 15
	douyinTokenLen      = 107
	douyinBrowserVer    = "140.0.0.0"
	douyinAreaTTL       = 30 * time.Minute
	douyinListTTL       = 15 * time.Second
	douyinBlockCooldown = 3 * time.Minute

	douyinURLPartition = "https://live.douyin.com/webcast/web/partition/detail/room/v2/"
	douyinReferer      = "https://live.douyin.com/"
	douyinAid          = "6383"
)

//go:embed assets/douyin/categories.json
var douyinCategoriesJSON []byte

type douyinProvider struct {
	api   *apiClient
	areas *cache[[]CategoryGroup]
	rooms *cache[Page]
	// ids 缓存「web_rid → 内部 roomId + 分区名」，见 douyin_room.go。
	ids *cache[douyinRoomRef]
	lim *limiter

	// 分区表是静态数据，解析一次常驻；catNames 用于给卡片补分类胶囊。
	catOnce   sync.Once
	catGroups []CategoryGroup
	catNames  map[string]string
	catErr    error
}

func newDouyin() *douyinProvider {
	return &douyinProvider{
		api:   newAPIClient(Douyin, douyinReferer),
		areas: newCache[[]CategoryGroup](),
		rooms: newCache[Page](),
		ids:   newCache[douyinRoomRef](),
		// 抖音风控最严：单并发、请求间隔 400ms。
		lim: newLimiter(1, 400*time.Millisecond),
	}
}

func (p *douyinProvider) Platform() Platform { return Douyin }

/* ---------------------------------------------------------------- 分区 */

func (p *douyinProvider) Categories(ctx context.Context) ([]CategoryGroup, error) {
	return p.areas.load("all", douyinAreaTTL, func() ([]CategoryGroup, error) {
		groups, _, err := p.loadCategories()
		return groups, err
	})
}

// loadCategories 解析内嵌分区表并建立 id→名称索引。
func (p *douyinProvider) loadCategories() ([]CategoryGroup, map[string]string, error) {
	p.catOnce.Do(func() {
		var groups []CategoryGroup
		if err := json.Unmarshal(douyinCategoriesJSON, &groups); err != nil {
			p.catErr = fmt.Errorf("解析抖音分区表失败: %w", err)
			return
		}
		names := make(map[string]string, len(groups)*8)
		for _, g := range groups {
			for _, c := range g.Categories {
				if c.ID != "" {
					names[c.ID] = c.Name
				}
			}
		}
		p.catGroups, p.catNames = groups, names
	})
	return p.catGroups, p.catNames, p.catErr
}

/* ---------------------------------------------------------------- 列表 */

func (p *douyinProvider) Rooms(ctx context.Context, areaID string, page, size int) (Page, error) {
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > douyinPageSize {
		size = douyinPageSize
	}
	partitionType, partition, err := douyinSplitAreaID(areaID)
	if err != nil {
		return Page{}, err
	}

	key := fmt.Sprintf("%s|%s|%d|%d", partitionType, partition, page, size)
	return p.rooms.load(key, douyinListTTL, func() (Page, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return Page{}, err
		}
		defer release()

		params := douyinLiveParams()
		params = append(params,
			[2]string{"count", strconv.Itoa(size)},
			[2]string{"offset", strconv.Itoa((page - 1) * size)},
			[2]string{"partition", partition},
			[2]string{"partition_type", partitionType},
			[2]string{"req_from", "2"},
			[2]string{"msToken", douyinMsToken()},
		)

		var resp douyinPartitionResponse
		target := douyinSignedURL(douyinURLPartition, params, p.api.ua)
		if err := p.api.getJSON(ctx, "直播列表", target, &resp, nil); err != nil {
			return Page{}, p.wrap("直播列表", err)
		}
		if resp.StatusCode != 0 {
			return Page{}, p.wrap("直播列表", fmt.Errorf("status_code=%d %s", resp.StatusCode, resp.StatusMsg))
		}

		var wrapper douyinPartitionData
		if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
			return Page{}, p.wrap("直播列表解析", err)
		}

		_, names, _ := p.loadCategories()
		area := names[partitionType+"_"+partition]
		rooms := make([]Room, 0, len(wrapper.Data))
		for _, entry := range wrapper.Data {
			if room, ok := douyinMapEntry(entry, area); ok {
				rooms = append(rooms, room)
			}
		}
		// 上游不给总页数；has_more 缺失时用「本页是否满页」兜底。
		hasMore := bool(wrapper.HasMore) || len(wrapper.Data) >= size
		return Page{Rooms: rooms, HasMore: hasMore}, nil
	})
}

/* ---------------------------------------------------------------- 搜索 */

// Search 把关键词当房间号查房间信息。
//
// 抖音匿名的关键词搜索接口一律返回 2483「请先登录」，baseline 的抖音
// 「搜索」实际就是「按直播间号查房间」（DTV baseline 的 src/api/douyin.ts）。
// 因此这里只有两种结果：查到了给一条，查不到（业务错误）给空列表——
// 错误不往上抛，否则 UI 会把「没这个房间」显示成「搜索失败」。
// 风控是例外：必须让上层提示「请求过于频繁」，不能假装没结果。
func (p *douyinProvider) Search(ctx context.Context, keyword string) ([]Room, error) {
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return nil, nil
	}
	room, err := p.RoomInfo(ctx, kw)
	if err != nil {
		if errors.Is(err, ErrBlocked) {
			return nil, err
		}
		return nil, nil
	}
	return []Room{room}, nil
}

/* ------------------------------------------------------------ 请求参数 */

// douyinLiveParams 是 live.douyin.com 列表接口的固定 web 端标识。
// 顺序固定：签名是对这个顺序的原始串做哈希的。
func douyinLiveParams() [][2]string {
	return [][2]string{
		{"aid", douyinAid},
		{"app_name", "douyin_web"},
		{"live_id", "1"},
		{"device_platform", "web"},
		{"language", "zh-CN"},
		{"enter_from", "web_homepage_hot"},
		{"cookie_enabled", "true"},
		{"screen_width", "1920"},
		{"screen_height", "1080"},
		{"browser_language", "zh-CN"},
		{"browser_platform", "Win32"},
		{"browser_name", "Chrome"},
		{"browser_version", douyinBrowserVer},
	}
}

// douyinSignedURL 拼 query、签名，再拼成最终请求地址。
//
// 签名的串与发出去的串必须逐字节一致，所以两边共用同一个 query。
func douyinSignedURL(endpoint string, params [][2]string, ua string) string {
	parts := make([]string, 0, len(params))
	for _, kv := range params {
		parts = append(parts, url.QueryEscape(kv[0])+"="+url.QueryEscape(kv[1]))
	}
	query := strings.Join(parts, "&")
	sign := douyinABogus(query, ua, time.Now())
	return endpoint + "?" + query + "&a_bogus=" + url.QueryEscape(sign)
}

// douyinMsToken 生成 msToken。服务端只要求它存在，不校验内容。
func douyinMsToken() string {
	buf := make([]byte, douyinTokenLen)
	out := make([]byte, douyinTokenLen)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时退化成确定性串，仍然能跑通。
		for i := range out {
			out[i] = douyinTokenByte(byte(i))
		}
		return string(out)
	}
	for i, b := range buf {
		out[i] = douyinTokenByte(b)
	}
	return string(out)
}

// douyinTokenByte 把任意字节映射到 [a-z A-Z 0-9]。
func douyinTokenByte(b byte) byte {
	switch v := int(b % 62); {
	case v < 26:
		return byte('a' + v)
	case v < 52:
		return byte('A' + v - 26)
	default:
		return byte('0' + v - 52)
	}
}

/* ------------------------------------------------------------ 响应结构 */

type douyinPartitionResponse struct {
	StatusCode int             `json:"status_code"`
	StatusMsg  string          `json:"status_msg"`
	Data       json.RawMessage `json:"data"`
}

type douyinPartitionData struct {
	Data    []douyinRoomEntry `json:"data"`
	Count   int               `json:"count"`
	Offset  int               `json:"offset"`
	HasMore douyinFlexBool    `json:"has_more"`
}

// douyinRoomEntry 是列表里的房间记录：外层 short 号 + 内层房间对象。
type douyinRoomEntry struct {
	WebRID string     `json:"web_rid"`
	Room   douyinRoom `json:"room"`
}

type douyinRoom struct {
	IDStr  string      `json:"id_str"`
	WebRID string      `json:"web_rid"`
	Title  string      `json:"title"`
	Cover  douyinURLs  `json:"cover"`
	Owner  douyinOwner `json:"owner"`
	Stats  douyinStats `json:"stats"`
	Status int         `json:"status"`
	// user_count 是房间上的精确在线人数（stats 里只有「1万+」这类文案）。
	UserCount int64 `json:"user_count"`
}

type douyinOwner struct {
	Nickname    string     `json:"nickname"`
	AvatarThumb douyinURLs `json:"avatar_thumb"`
	AvatarMed   douyinURLs `json:"avatar_medium"`
}

type douyinStats struct {
	TotalUserStr string `json:"total_user_str"`
	UserCountStr string `json:"user_count_str"`
	UserCount    int64  `json:"user_count"`
	TotalUser    int64  `json:"total_user"`
}

// douyinURLs 是抖音统一的 {url_list:[...]} 结构。
type douyinURLs struct {
	URLList []string `json:"url_list"`
}

func (u douyinURLs) first() string {
	for _, s := range u.URLList {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// douyinFlexBool 兼容 bool 与 0/1 两种写法：不同接口对 has_more 的编码不一样，
// 用严格 bool 会让整包解析失败。
type douyinFlexBool bool

func (b *douyinFlexBool) UnmarshalJSON(data []byte) error {
	switch strings.Trim(strings.TrimSpace(string(data)), `"`) {
	case "1", "true":
		*b = true
	default:
		*b = false
	}
	return nil
}

/* -------------------------------------------------------------- 映射 */

// douyinMapEntry 把列表记录映射成统一直播卡片。
func douyinMapEntry(entry douyinRoomEntry, area string) (Room, bool) {
	r := entry.Room
	roomID := firstNonEmpty(entry.WebRID, r.WebRID, r.IDStr)
	if roomID == "" {
		return Room{}, false
	}

	viewers := r.UserCount
	if viewers <= 0 {
		viewers = r.Stats.UserCount
	}
	if viewers <= 0 {
		viewers = r.Stats.TotalUser
	}
	if viewers <= 0 {
		viewers = douyinParseCount(firstNonEmpty(r.Stats.UserCountStr, r.Stats.TotalUserStr))
	}

	// status=2 是在播；字段缺失（0）时按列表语义当作在播。
	live := true
	if r.Status != 0 {
		live = r.Status == 2
	}

	return Room{
		Platform: Douyin,
		RoomID:   roomID,
		Title:    strings.TrimSpace(r.Title),
		Nickname: strings.TrimSpace(r.Owner.Nickname),
		Cover:    httpsURL(r.Cover.first()),
		Avatar:   httpsURL(firstNonEmpty(r.Owner.AvatarThumb.first(), r.Owner.AvatarMed.first())),
		Viewers:  viewers,
		Area:     area,
		Live:     live,
	}, true
}

// douyinSplitAreaID 把 "1_1010032" 拆成 (partition_type, partition)。
//
// 也接受完整 href（/category/1_1_1_1010032），与 web 端同样的规则：
// 取最后两段。
func douyinSplitAreaID(id string) (partitionType, partition string, err error) {
	s := strings.TrimSpace(id)
	if s == "" {
		return "", "", fmt.Errorf("live: 抖音分区 id 为空")
	}
	if i := strings.LastIndex(s, "/category/"); i >= 0 {
		s = s[i+len("/category/"):]
	}
	s = strings.Trim(s, "/")

	parts := strings.Split(s, "_")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("live: 抖音分区 id %q 格式应为 类型_分区", id)
	}
	partitionType = strings.TrimSpace(parts[len(parts)-2])
	partition = strings.TrimSpace(parts[len(parts)-1])
	if partitionType == "" || partition == "" {
		return "", "", fmt.Errorf("live: 抖音分区 id %q 不完整", id)
	}
	return partitionType, partition, nil
}

// douyinParseCount 把「1.2万」「3.4亿」「10万+」这类人数文案换算成数字。
func douyinParseCount(text string) int64 {
	s := strings.TrimSpace(text)
	if s == "" {
		return 0
	}
	s = strings.TrimSuffix(s, "+")
	s = strings.TrimSuffix(s, "人")
	s = strings.TrimSuffix(s, "次")
	s = strings.TrimSpace(s)

	multiplier := 1.0
	switch {
	case strings.HasSuffix(s, "万"):
		multiplier = 10000
		s = strings.TrimSuffix(s, "万")
	case strings.HasSuffix(s, "亿"):
		multiplier = 100000000
		s = strings.TrimSuffix(s, "亿")
	case strings.HasSuffix(s, "w"), strings.HasSuffix(s, "W"):
		multiplier = 10000
		s = s[:len(s)-1]
	}

	s = strings.TrimSpace(s)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(v * multiplier))
}

/* -------------------------------------------------------------- 错误 */

// wrap 把抖音的失败分成两类：风控（空响应/403/412）进入冷却，
// 其余是上游业务错误。
func (p *douyinProvider) wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBlocked) {
		p.lim.block(douyinBlockCooldown)
		return ErrBlocked
	}
	var upstream *UpstreamError
	if errors.As(err, &upstream) {
		return err
	}
	return &UpstreamError{Platform: Douyin, Op: op, Message: err.Error()}
}

var _ Provider = (*douyinProvider)(nil)
