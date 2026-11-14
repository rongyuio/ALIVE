package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// 虎牙单房间实时信息。
//
// 链路参考 DTV baseline 的 platforms/huya/stream_url.go：它取流前先拉同一个
// profileRoom 档案，档案里已经带了卡片要的全部字段，所以这里不再走
// WUP/TARS 那条只有取流才需要的 RPC。
//
// 档案接口回的是「房间快照」而不是列表，不存在分页；房间不存在时 HTTP 仍是
// 200，业务状态放在 status 里（422），因此错误分类必须看 body 而不是状态码。
const huyaRoomProfileURL = "https://mp.huya.com/cache.php?m=Live&do=profileRoom&roomid=%s"

// 编译期确认虎牙实现了房间信息能力；上层用类型断言判断，漏了不会报错只会静默跳过。
var _ RoomInfoer = (*huyaProvider)(nil)

// huyaProfileEnvelope 是档案响应的外层。
//
// data 单独留成 RawMessage：房间不存在时它是 []，解成对象会失败，
// 得先看 status 才能区分「房间不存在」和「上游换了格式」。
type huyaProfileEnvelope struct {
	Status  int             `json:"status"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// RoomInfo 查询单个房间的实时状态。
//
// 不缓存：调用方要的就是「此刻在不在播」，几秒的缓存会让刷新按钮看起来失灵。
// 上游节奏由 limiter 控制，关注列表的并发刷新会在那一层排队。
func (p *huyaProvider) RoomInfo(ctx context.Context, roomID string) (Room, error) {
	id, err := normalizeHuyaRoomID(roomID)
	if err != nil {
		return Room{}, err
	}

	release, err := p.lim.acquire(ctx)
	if err != nil {
		return Room{}, err
	}
	defer release()

	target := fmt.Sprintf(huyaRoomProfileURL, url.QueryEscape(id))
	body, err := p.api.get(ctx, "房间信息", target, nil)
	if err != nil {
		// 403/412/429 在这一层已经是 ErrBlocked，wrap 会顺带让 limiter 冷却
		return Room{}, p.wrap("房间信息", err)
	}

	room, err := parseHuyaRoomProfile(body, id)
	if err != nil {
		return Room{}, p.wrap("房间信息", err)
	}
	return room, nil
}

// normalizeHuyaRoomID 归一房间号，接受纯数字和站内房间地址两种写法。
//
// 关注列表里存的可能是 https://www.huya.com/518518 这类地址；上游只认数字，
// 所以先把地址尾巴截出来。非数字的 vanity 名（如 /lpl）上游会返回 422，
// 这里不猜，交给上游判定。
func normalizeHuyaRoomID(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("live: 虎牙房间号为空")
	}
	if i := strings.LastIndex(text, "/"); i >= 0 && strings.Contains(text[:i], "huya.com") {
		text = text[i+1:]
	}
	if i := strings.IndexAny(text, "?#"); i >= 0 {
		text = text[:i]
	}
	text = strings.Trim(text, "/")
	if text == "" {
		return "", fmt.Errorf("live: 虎牙房间号 %q 无法识别", raw)
	}
	return text, nil
}

// parseHuyaRoomProfile 把档案响应映射成卡片数据。
//
// 字段用 map + 柔性取值，和直播列表保持一致：虎牙同一字段会在数字、
// 字符串、科学计数法之间漂移，用严格类型会整条失败。
func parseHuyaRoomProfile(body []byte, roomID string) (Room, error) {
	var resp huyaProfileEnvelope
	if err := json.Unmarshal(body, &resp); err != nil {
		// 正常响应一定是 JSON；解不出来基本是被换成了验证页或空页
		return Room{}, fmt.Errorf("%w: 房间信息响应不是 JSON: %v", ErrBlocked, err)
	}

	if resp.Status != 200 {
		// 业务状态码也可能镜像风控状态，按同一套规则归类
		switch resp.Status {
		case 403, 412, 429:
			return Room{}, fmt.Errorf("%w: 房间信息 status=%d", ErrBlocked, resp.Status)
		}
		msg := strings.TrimSpace(resp.Message)
		if msg == "" {
			msg = fmt.Sprintf("status=%d", resp.Status)
		}
		// 422 且 message 是「该主播不存在！」，房间号错时走这里
		return Room{}, &UpstreamError{Platform: Huya, Op: "房间信息", Message: msg}
	}

	var root map[string]any
	if err := json.Unmarshal(resp.Data, &root); err != nil {
		// status=200 但 data 不是对象：房间没有档案，按「不存在」上抛
		return Room{}, &UpstreamError{
			Platform: Huya,
			Op:       "房间信息",
			Message:  fmt.Sprintf("房间 %s 不存在或没有数据", roomID),
		}
	}
	liveData, _ := root["liveData"].(map[string]any)
	owner, _ := root["profileInfo"].(map[string]any)
	// status=200 但两个对象都没有：上游偶尔对已注销房间回空壳，
	// 不能把它当成一个「未开播的空白房间」写回关注列表。
	if liveData == nil && owner == nil {
		return Room{}, &UpstreamError{
			Platform: Huya,
			Op:       "房间信息",
			Message:  fmt.Sprintf("房间 %s 不存在或没有数据", roomID),
		}
	}

	id := firstNonEmpty(
		huyaJSONString(liveData["profileRoom"]),
		huyaJSONString(owner["profileRoom"]),
		roomID,
	)

	// liveStatus 是本次直播状态，realLiveStatus 是实时校准后的值；
	// 两个都在时才以它们为准，都缺失才退回 stream 是否存在。
	liveStatus := huyaJSONString(root["liveStatus"])
	realStatus := huyaJSONString(root["realLiveStatus"])
	live := strings.EqualFold(realStatus, "on") || strings.EqualFold(liveStatus, "on")
	if !live && liveStatus == "" && realStatus == "" {
		if stream, ok := root["stream"].(map[string]any); ok {
			if list, ok := stream["baseSteamInfoList"].([]any); ok && len(list) > 0 {
				live = true
			}
		}
	}

	// 未开播的房间没有 userCount（totalCount 是累计观众/关注量，不是当前人气），
	// 所以人气只在在播时取值，避免把累计量当成在线人数显示。
	var viewers int64
	if live {
		viewers = int64(huyaJSONFloat(liveData["userCount"]))
		if viewers <= 0 {
			viewers = int64(huyaJSONFloat(liveData["totalCount"]))
		}
	}

	nickname := firstNonEmpty(
		huyaJSONString(owner["nick"]),
		huyaJSONString(liveData["nick"]),
	)
	if nickname == "" {
		nickname = "房间 " + id
	}

	// gameFullName 是完整分区名；gameHostName 正常是 lol 这类 slug，
	// 但老房间会给成 gid 数字，那时对卡片没有意义，直接丢掉。
	area := huyaJSONString(liveData["gameFullName"])
	if area == "" {
		if host := huyaJSONString(liveData["gameHostName"]); host != "" && !huyaIsDigits(host) {
			area = host
		}
	}

	return Room{
		Platform: Huya,
		RoomID:   id,
		Title: firstNonEmpty(
			huyaJSONString(liveData["introduction"]),
			huyaJSONString(liveData["roomName"]),
		),
		Nickname: strings.TrimSpace(nickname),
		Cover:    httpsURL(huyaJSONString(liveData["screenshot"])),
		Avatar: httpsURL(firstNonEmpty(
			huyaJSONString(owner["avatar180"]),
			huyaJSONString(liveData["avatar180"]),
		)),
		Viewers: viewers,
		Area:    strings.TrimSpace(area),
		Live:    live,
	}, nil
}
