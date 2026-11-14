package live

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// 斗鱼房间实时信息：关注列表刷新在线状态、按房间号直达都走这里。
//
// 接口是播放页自己用的 www.douyu.com/betard/{roomID}，一次返回整页播放器数据
// （实测 100~130KB）。字段比目录接口全，能直接拿到真实房间号、封面、人气和
// 开播状态，不必再拼页面。
//
// 与列表/搜索不同，房间信息不做缓存：主播刚开播或刚下播时必须立刻反映出来，
// 几秒的缓存就足够让「刷新在线状态」给出错误结论。
const douyuRoomURL = "https://www.douyu.com/betard/%s"

// 编译期确认斗鱼提供了房间实时信息能力。
var _ RoomInfoer = (*douyuProvider)(nil)

// RoomInfo 查询单个房间的实时状态。
//
// 错误分类沿用本包规则：
//   - 403/412/429、验证页、无标记的非 JSON → ErrBlocked（wrap 会让平台冷却）；
//   - 房间不存在/已关闭 → UpstreamError。斗鱼对这两类业务错误返回的是
//     HTTP 200 的 HTML 提示页，不能按风控处理，否则上层会把「房间没了」
//     显示成「请求过于频繁」，还会让整个平台白白冷却一分钟。
func (p *douyuProvider) RoomInfo(ctx context.Context, roomID string) (Room, error) {
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		return Room{}, fmt.Errorf("live: 斗鱼房间号为空")
	}
	// 非数字房间号（别名、URL、拼错的短号）上游会直接 403，
	// 提前拦下：这是参数错误，不该被当成风控。
	if !douyuDigits(roomID) {
		return Room{}, fmt.Errorf("live: 斗鱼房间号 %q 格式错误", roomID)
	}

	// 房间详情比列表重，但归属同一个平台限流器：
	// 关注列表刷新会并发查很多房间，节奏仍要和列表/搜索一致。
	release, err := p.lim.acquire(ctx)
	if err != nil {
		return Room{}, err
	}
	defer release()

	target := fmt.Sprintf(douyuRoomURL, roomID)
	body, err := p.api.get(ctx, "房间信息", target, header{
		// 和网页播放页一致：从该房间自己的地址进来，降低被判定为爬虫的概率。
		"Referer": "https://www.douyu.com/" + roomID,
	})
	if err != nil {
		return Room{}, p.wrap("房间信息", err)
	}

	room, err := parseDouyuRoom(body, roomID)
	if err != nil {
		return Room{}, p.wrap("房间信息", err)
	}
	return room, nil
}

type douyuRoomResponse struct {
	Room *douyuRoomDetail `json:"room"`
	// 个别版本会返回 {"error":1,"msg":"..."} 的业务错误。
	Error any    `json:"error"`
	Msg   string `json:"msg"`
}

// douyuRoomDetail 是 betard 的 room 对象，只取卡片需要的字段。
// 数值字段用 any：同一字段在不同房间会以数字或字符串下发。
type douyuRoomDetail struct {
	RoomID   any    `json:"room_id"`
	RoomName string `json:"room_name"`
	Nickname string `json:"nickname"`
	// owner_name 与 nickname 通常是同一个值，但个别房间只给其中一个。
	OwnerName   string `json:"owner_name"`
	AvatarMid   string `json:"avatar_mid"`
	OwnerAvatar string `json:"owner_avatar"`
	// avatar 是对象，但保留原始字节：字段形状变了也不会让整页解析失败。
	Avatar json.RawMessage `json:"avatar"`

	// 封面有三种来源：rs1 是 16:9 小图（和目录接口卡片同款），
	// room_pic/coverSrc 是 1280×720 大图，room_src 是相对路径。
	RS1      string `json:"rs1"`
	RoomPic  string `json:"room_pic"`
	CoverSrc string `json:"coverSrc"`
	RoomSrc  string `json:"room_src"`

	ShowStatus any `json:"show_status"`
	VideoLoop  any `json:"videoLoop"`
	IsLoop     any `json:"isLoop"`

	SecondLvlName string `json:"second_lvl_name"`
	Cate2Name     string `json:"cate2_name"`
	// online 在大多数房间是 null，人气实际在 room_biz_all.hot。
	Online any `json:"online"`

	RoomBizAll struct {
		Hot any `json:"hot"`
	} `json:"room_biz_all"`
}

func parseDouyuRoom(body []byte, roomID string) (Room, error) {
	if err := douyuRoomPageError(body); err != nil {
		return Room{}, err
	}

	var payload douyuRoomResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		// 到这里还解不出 JSON，说明既不是正常响应也不是房间提示页，
		// 大概率是验证页或被换成了空响应，按风控处理。
		return Room{}, fmt.Errorf("%w: 响应不是 JSON（可能被换成了验证页）: %v", ErrBlocked, err)
	}
	if douyuInteger(payload.Error) != 0 {
		return Room{}, &UpstreamError{
			Platform: Douyu,
			Op:       "房间信息",
			Message:  fmt.Sprintf("error=%d msg=%s", douyuInteger(payload.Error), payload.Msg),
		}
	}
	detail := payload.Room
	if detail == nil {
		return Room{}, &UpstreamError{
			Platform: Douyu,
			Op:       "房间信息",
			Message:  "接口未返回房间数据",
		}
	}

	// 短号会被上游解析成它自己的 id；只有字段缺失时才退回调用方给的房间号。
	id := douyuText(detail.RoomID)
	if id == "" || id == "0" {
		id = roomID
	}

	room := Room{
		Platform: Douyu,
		RoomID:   id,
		Title:    douyuFirst(detail.RoomName, "暂无标题"),
		Nickname: douyuFirst(detail.Nickname, detail.OwnerName, "房间 "+id),
		// 先取 rs1（16:9 小图，和目录卡片一致），缺失再退回大图或相对路径。
		Cover:   douyuCover(douyuFirst(detail.RS1, detail.RoomPic, detail.CoverSrc), detail.RoomSrc),
		Avatar:  douyuAvatar(douyuFirst(detail.AvatarMid, detail.OwnerAvatar, douyuRoomAvatarURL(detail.Avatar))),
		Viewers: douyuInteger(detail.RoomBizAll.Hot),
		Area:    douyuFirst(detail.SecondLvlName, detail.Cate2Name),
		Live:    douyuInteger(detail.ShowStatus) == 1,
	}
	if room.Viewers == 0 {
		room.Viewers = douyuInteger(detail.Online)
	}
	// show_status=1 只是「有画面」；轮播（videoLoop/isLoop=1）不算直播中，
	// 判定口径和目录、搜索保持一致。
	if douyuInteger(detail.VideoLoop) == 1 || douyuInteger(detail.IsLoop) == 1 {
		room.Live = false
	}
	return room, nil
}

// douyuRoomAvatarURL 从 avatar 字段里取一个可用地址。
// 正常是 {"big":...,"middle":...,"small":...}，少数响应直接给字符串。
func douyuRoomAvatarURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj struct {
		Middle string `json:"middle"`
		Big    string `json:"big"`
		Small  string `json:"small"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		if url := douyuFirst(obj.Middle, obj.Big, obj.Small); url != "" {
			return url
		}
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return strings.TrimSpace(str)
	}
	return ""
}

// douyuRoomPageMarkers 是斗鱼「提示信息」页的两个固定标记。
// 两个都匹配才算房间提示页：单个 class 名太通用，验证页也可能撞上。
var douyuRoomPageMarkers = [][]byte{
	[]byte(`<div class="error">`),
	[]byte(`提示信息 -斗鱼`),
}

// douyuRoomPageError 识别房间不存在/已关闭的 HTML 提示页。
//
// 这类页面 HTTP 状态是 200、内容是 HTML，按普通解析只会得到「不是 JSON」，
// 从而被误判成风控。这里把它翻译成业务错误，让上层能区分。
func douyuRoomPageError(body []byte) error {
	for _, marker := range douyuRoomPageMarkers {
		if !bytes.Contains(body, marker) {
			return nil
		}
	}
	return &UpstreamError{
		Platform: Douyu,
		Op:       "房间信息",
		Message:  douyuRoomPageMessage(body),
	}
}

// douyuRoomPageMessage 从提示页里抠出人话，取不到就给一句兜底。
func douyuRoomPageMessage(body []byte) string {
	const openTag, closeTag = "<span><p>", "</p>"
	if i := bytes.Index(body, []byte(openTag)); i >= 0 {
		rest := body[i+len(openTag):]
		if j := bytes.Index(rest, []byte(closeTag)); j >= 0 {
			if msg := strings.TrimSpace(string(rest[:j])); msg != "" {
				return msg
			}
		}
	}
	return "房间不存在或未开放"
}
