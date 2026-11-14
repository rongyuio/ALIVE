package live

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// B 站房间信息：房间号 → 卡片要的全部字段。
//
// 两跳是必须的：get_info 只给 room_id / live_status / uid，
// 昵称、头像、封面、人气、分区名都在「按 uid 查状态」那个接口里。
// 不能用 room_init：它从 2026-11 起对匿名请求直接 412。
func (p *bilibiliProvider) RoomInfo(ctx context.Context, roomID string) (Room, error) {
	id, err := strconv.Atoi(strings.TrimSpace(roomID))
	if err != nil || id <= 0 {
		return Room{}, &UpstreamError{
			Platform: Bilibili, Op: "房间信息",
			Message: fmt.Sprintf("房间号 %q 不合法", roomID),
		}
	}

	release, err := p.lim.acquire(ctx)
	if err != nil {
		return Room{}, err
	}
	info, err := p.lc.GetLiveRoomInfo(ctx, bilibiliLive.GetLiveRoomInfoParam{RoomID: id})
	release()
	if err != nil {
		return Room{}, p.wrap("房间信息", err)
	}
	if info.UID <= 0 {
		return Room{}, &UpstreamError{
			Platform: Bilibili, Op: "房间信息", Message: "上游没有返回主播 uid",
		}
	}

	release, err = p.lim.acquire(ctx)
	if err != nil {
		return Room{}, err
	}
	uid := int64(info.UID)
	status, err := p.lc.GetRoomStatusInfoByUIDs(ctx,
		bilibiliLive.GetRoomStatusInfoByUIDsParam{UIDs: []int64{uid}})
	release()
	if err != nil {
		return Room{}, p.wrap("房间详情", err)
	}
	item, ok := status[strconv.FormatInt(uid, 10)]
	if !ok {
		return Room{}, &UpstreamError{
			Platform: Bilibili, Op: "房间详情",
			Message: fmt.Sprintf("上游没有返回 uid=%d 的房间信息", uid),
		}
	}

	realID := strconv.FormatInt(item.RoomID, 10)
	if item.RoomID <= 0 {
		realID = strconv.Itoa(id)
	}
	return Room{
		Platform: Bilibili,
		RoomID:   realID,
		Title:    firstNonEmpty(item.Title, info.Title),
		Nickname: item.Uname,
		Cover:    biliImage(firstNonEmpty(item.CoverFromUser, item.Keyframe)),
		Avatar:   biliImage(item.Face),
		Viewers:  item.Online,
		Area:     firstNonEmpty(item.AreaV2Name, item.AreaName),
		// 只有 live_status==1 才算直播中；2 是轮播，界面按未开播处理
		Live: item.LiveStatus == 1,
	}, nil
}

// 编译期确认 B 站满足 RoomInfoer。
var _ RoomInfoer = (*bilibiliProvider)(nil)
