package live

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// 抖音单个房间的实时信息。
//
// 真实链路是两步（见 DTV/internal/platforms/douyin/room_info.go）：
//  1. 打开房间 H5 页面 https://live.douyin.com/{web_rid}，从页面内嵌的
//     JSON 里读出**内部 roomId**（19 位数字）；
//  2. 用内部 roomId 调 webcast 的 reflow/info，拿到房间对象。
//
// 为什么不能一步到位：reflow 只认内部 roomId，直接传 web_rid 会返回
// status_code=10011（Request params error）。webcast/room/web/enter 又已经
// 实测返回空 body，所以两步是当前唯一可用的路径。

const (
	douyinURLRoomPage = "https://live.douyin.com/"
	douyinURLReflow   = "https://webcast.amemv.com/webcast/room/reflow/info/"

	// reflow 的固定参数：缺一个都会得到 10011。
	douyinReflowAppID       = "1128"
	douyinReflowVersionCode = "99.99.99"
	douyinReflowInvalidRoom = 10011 // 10011 = 房间参数错误（房间不存在/已失效）

	// web_rid → 内部 roomId（顺带分区名）不随开播状态变化，可以缓存久一点：
	// 省掉每次查询都下载一遍 1.3MB 的房间页。实时状态（reflow）不缓存。
	douyinRoomIDTTL = 6 * time.Hour

	// 正常房间页实测约 1.3MB；验证页/被风控时的空壳响应远小于这个量级。
	douyinRoomPageMin = 4096
)

// 页面内嵌 JSON 是转义过的（形如 \"roomId\":\"7707...\"），也兼容未转义的写法。
var douyinRoomIDPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\\"roomId\\":\\"(\d{10,})\\"`),
	regexp.MustCompile(`"roomId":"(\d{10,})"`),
	regexp.MustCompile(`\\"room_id\\":\\"(\d{10,})\\"`),
	regexp.MustCompile(`"room_id":"(\d{10,})"`),
}

// 房间自己的分区在 partition_road_map 里：游戏类房间的 sub_partition 才是
// 二级分区（partition 是父级，如「射击游戏」），非游戏类没有 sub_partition。
var (
	douyinRoadMapSubPattern = regexp.MustCompile(`\\"sub_partition\\":\{\\"partition\\":\{\\"id_str\\":\\"(\d+)\\"`)
	douyinRoadMapPattern    = regexp.MustCompile(`\\"partition_road_map\\":\{\\"partition\\":\{\\"id_str\\":\\"(\d+)\\"`)
)

// 页面导航数据里有全套分区节点，用 id 反查分区名。
// 不能用静态分区表反查：那张表只有游戏类，舞蹈/娱乐等房间不在其中。
var douyinPartitionNodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\\"partition\\":\{\\"id_str\\":\\"(\d+)\\",\\"type\\":\d+,\\"title\\":\\"([^"\\]*)\\"`),
	regexp.MustCompile(`"partition":\{"id_str":"(\d+)","type":\d+,"title":"([^"\\]*)"`),
}

// douyinRoomRef 是房间页能拿到、且不随开播状态变化的两个值。
type douyinRoomRef struct {
	InternalID string
	Area       string
}

// RoomInfo 查单个房间的实时状态（不缓存）。
//
// 错误分类：风控返回 ErrBlocked 并让 limiter 冷却；房间号非法、房间不存在、
// reflow 业务报错一律返回 UpstreamError，上层据此区分「请求过于频繁」和
// 「没有这个房间」。
//
// 房间存在但未开播不算错误：reflow 对下播房间仍然返回完整房间对象
// （status=4，实测 web_rid 8888 / 10086），Room.Live 会置为 false。
// 只有拿不到房间对象时才是 UpstreamError。
func (p *douyinProvider) RoomInfo(ctx context.Context, roomID string) (Room, error) {
	webRID := douyinNormalizeRoomID(roomID)
	if webRID == "" {
		return Room{}, &UpstreamError{
			Platform: Douyin,
			Op:       "房间信息",
			Message:  fmt.Sprintf("房间号 %q 无效", strings.TrimSpace(roomID)),
		}
	}

	ref, err := p.ids.load(webRID, douyinRoomIDTTL, func() (douyinRoomRef, error) {
		return p.douyinResolveRoom(ctx, webRID)
	})
	if err != nil {
		return Room{}, err
	}

	room, err := p.douyinFetchRoom(ctx, ref.InternalID)
	if err != nil {
		return Room{}, err
	}
	return douyinMapRoom(webRID, ref.Area, room), nil
}

// douyinResolveRoom 打开房间页，取出内部 roomId 与分区名。
func (p *douyinProvider) douyinResolveRoom(ctx context.Context, webRID string) (douyinRoomRef, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return douyinRoomRef{}, err
	}
	defer release()

	body, err := p.api.get(ctx, "房间页", douyinURLRoomPage+webRID, header{
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	})
	if err != nil {
		return douyinRoomRef{}, p.wrap("房间页", err)
	}
	// 风控/验证页同样是 HTTP 200，但体积远小于正常页面。按「房间不存在」
	// 处理会让 UI 静默显示无结果，所以这里当风控，进入冷却。
	if len(body) < douyinRoomPageMin {
		return douyinRoomRef{}, p.wrap("房间页", fmt.Errorf("%w: 只返回 %d 字节，疑似风控页", ErrBlocked, len(body)))
	}
	ref := douyinParseRoomPage(string(body))
	if ref.InternalID != "" {
		return ref, nil
	}
	// 房间号不存在时页面仍然 200（走的是占位内容，roomId 为空串）。
	return douyinRoomRef{}, &UpstreamError{
		Platform: Douyin,
		Op:       "房间信息",
		Message:  fmt.Sprintf("房间 %s 不存在", webRID),
	}
}

// douyinFetchRoom 用内部 roomId 取房间对象。
func (p *douyinProvider) douyinFetchRoom(ctx context.Context, internalID string) (douyinReflowRoom, error) {
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return douyinReflowRoom{}, err
	}
	defer release()

	// 参数顺序固定：签名是对这份原始串做哈希的。
	params := [][2]string{
		{"type_id", "0"},
		{"live_id", "1"},
		{"version_code", douyinReflowVersionCode},
		{"app_id", douyinReflowAppID},
		{"room_id", internalID},
		{"msToken", douyinMsToken()},
	}

	var resp douyinReflowResponse
	target := douyinSignedURL(douyinURLReflow, params, p.api.ua)
	if err := p.api.getJSON(ctx, "房间信息", target, &resp, nil); err != nil {
		return douyinReflowRoom{}, p.wrap("房间信息", err)
	}
	return douyinParseReflow(resp)
}

// douyinParseReflow 解析响应并做业务错误分类（拆出来是为了离线测试）。
func douyinParseReflow(resp douyinReflowResponse) (douyinReflowRoom, error) {
	if resp.StatusCode == douyinReflowInvalidRoom {
		return douyinReflowRoom{}, &UpstreamError{
			Platform: Douyin,
			Op:       "房间信息",
			Message:  "房间不存在或已失效",
		}
	}
	if resp.StatusCode != 0 {
		return douyinReflowRoom{}, &UpstreamError{
			Platform: Douyin,
			Op:       "房间信息",
			Message:  fmt.Sprintf("status_code=%d %s", resp.StatusCode, resp.StatusMsg),
		}
	}

	var data douyinReflowData
	if len(resp.Data) > 0 {
		if err := json.Unmarshal(resp.Data, &data); err != nil {
			return douyinReflowRoom{}, &UpstreamError{
				Platform: Douyin,
				Op:       "房间信息",
				Message:  "响应解析失败: " + err.Error(),
			}
		}
	}
	if data.Room == nil {
		return douyinReflowRoom{}, &UpstreamError{
			Platform: Douyin,
			Op:       "房间信息",
			Message:  "响应里没有房间数据",
		}
	}
	return *data.Room, nil
}

// douyinNormalizeRoomID 把用户输入统一成 web_rid（纯数字短号）。
//
// 接受：纯数字、https://live.douyin.com/{id}、www.douyin.com/follow/live/{id}，
// 以及带 room_id / web_rid 参数的链接。关键词（非数字）返回空串——
// 抖音没有可匿名使用的关键词搜索，上层据此直接给空结果。
func douyinNormalizeRoomID(input string) string {
	s := strings.TrimSpace(input)
	if s == "" {
		return ""
	}
	isURL := strings.Contains(s, "://") || strings.ContainsAny(s, "/?#")
	if !isURL {
		return douyinDigitsOnly(s)
	}

	// 链接里如果显式带了房间号参数，优先用它。
	if i := strings.Index(s, "?"); i >= 0 {
		query := s[i+1:]
		if j := strings.Index(query, "#"); j >= 0 {
			query = query[:j]
		}
		for _, kv := range strings.Split(query, "&") {
			for _, key := range []string{"room_id=", "roomId=", "web_rid=", "webId="} {
				if v, ok := strings.CutPrefix(kv, key); ok {
					if j := strings.IndexAny(v, "&#"); j >= 0 {
						v = v[:j]
					}
					if id := douyinDigitsOnly(v); id != "" {
						return id
					}
				}
			}
		}
		s = s[:i]
	}
	if i := strings.Index(s, "#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return douyinDigitsOnly(s)
}

// douyinDigitsOnly 只接受全数字：混了字母的输入不是房间号。
func douyinDigitsOnly(s string) string {
	if s == "" {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return ""
		}
	}
	return s
}

// douyinParseRoomPage 从房间页 HTML 里取内部 roomId 与分区名。
func douyinParseRoomPage(html string) douyinRoomRef {
	ref := douyinRoomRef{InternalID: douyinExtractRoomID(html)}
	if id := douyinRoomPartitionID(html); id != "" {
		ref.Area = douyinPartitionName(html, id)
	}
	return ref
}

// douyinRoomPartitionID 取房间所在二级分区的 id。
func douyinRoomPartitionID(html string) string {
	if m := douyinRoadMapSubPattern.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	if m := douyinRoadMapPattern.FindStringSubmatch(html); len(m) == 2 {
		return m[1]
	}
	return ""
}

// douyinPartitionName 用分区 id 在页面导航数据里查名字。
func douyinPartitionName(html, id string) string {
	for _, re := range douyinPartitionNodePatterns {
		for _, m := range re.FindAllStringSubmatch(html, -1) {
			if len(m) == 3 && m[1] == id {
				if name := strings.TrimSpace(m[2]); name != "" {
					return name
				}
			}
		}
	}
	return ""
}

// douyinExtractRoomID 从房间页 HTML 里取内部 roomId。
func douyinExtractRoomID(html string) string {
	for _, re := range douyinRoomIDPatterns {
		if m := re.FindStringSubmatch(html); len(m) == 2 && m[1] != "" {
			return m[1]
		}
	}
	return ""
}

// douyinMapRoom 把 reflow 的房间对象映射成统一卡片。
func douyinMapRoom(webRID, area string, room douyinReflowRoom) Room {
	// 人数顺序与列表一致：user_count 是精确在线人数，
	// stats 里只有「1.2万」「10万+」这种文案，最后才轮到文案。
	viewers := room.UserCount
	if viewers <= 0 {
		viewers = room.Stats.UserCount
	}
	if viewers <= 0 {
		viewers = room.Stats.TotalUser
	}
	if viewers <= 0 {
		viewers = douyinParseCount(firstNonEmpty(room.Stats.UserCountStr, room.Stats.TotalUserStr))
	}

	return Room{
		Platform: Douyin,
		RoomID:   webRID,
		Title:    strings.TrimSpace(room.Title),
		Nickname: strings.TrimSpace(room.Owner.Nickname),
		Cover:    httpsURL(room.Cover.first()),
		Avatar:   httpsURL(firstNonEmpty(room.Owner.AvatarThumb.first(), room.Owner.AvatarMed.first())),
		Viewers:  viewers,
		Area:     area,
		// status 语义：2=直播中，4=已结束/未开播。未知值按未开播处理，
		// 免得下播的直播间在关注列表里继续亮着。
		Live: room.Status == 2,
	}
}

/* ------------------------------------------------------------ 响应结构 */

type douyinReflowResponse struct {
	StatusCode int             `json:"status_code"`
	StatusMsg  string          `json:"status_msg"`
	Data       json.RawMessage `json:"data"`
}

type douyinReflowData struct {
	Room *douyinReflowRoom `json:"room"`
}

// douyinReflowRoom 只取卡片需要的字段。reflow 还带几十 KB 的取流数据，
// 数据层用不上，不解进结构体。
type douyinReflowRoom struct {
	Status    int         `json:"status"`
	Title     string      `json:"title"`
	Cover     douyinURLs  `json:"cover"`
	Owner     douyinOwner `json:"owner"`
	Stats     douyinStats `json:"stats"`
	UserCount int64       `json:"user_count"`
}

var _ RoomInfoer = (*douyinProvider)(nil)
