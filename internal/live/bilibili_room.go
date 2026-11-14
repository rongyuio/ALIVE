package live

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// biliRoomRef 是刷新链路要记住的稳定映射：房间号 → 主播 uid。
//
// get_info 是唯一能拿到 uid 的匿名接口，而 uid 属于主播、不会随开播状态变化。
// 缓存住它，刷新关注列表时每个房间就少一次请求——46 个关注原本 92 次请求，
// 变成 46 次。
type biliRoomRef struct {
	UID int64
	// Title 来自 get_info，详情接口缺字段时用它兜底
	Title string
}

// biliUIDTTL 取得很长：uid 不会变，只有房间被转手这种极端情况才会过期。
const biliUIDTTL = 6 * time.Hour

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

	ref, err := p.uids.load(strconv.Itoa(id), biliUIDTTL, func() (biliRoomRef, error) {
		release, err := p.lim.acquire(ctx)
		if err != nil {
			return biliRoomRef{}, err
		}
		info, err := p.lc.GetLiveRoomInfo(ctx, bilibiliLive.GetLiveRoomInfoParam{RoomID: id})
		release()
		if err != nil {
			return biliRoomRef{}, p.wrap("房间信息", err)
		}
		if info.UID <= 0 {
			return biliRoomRef{}, &UpstreamError{
				Platform: Bilibili, Op: "房间信息", Message: "上游没有返回主播 uid",
			}
		}
		return biliRoomRef{UID: int64(info.UID), Title: info.Title}, nil
	})
	if err != nil {
		return Room{}, err
	}

	release, err := p.lim.acquire(ctx)
	if err != nil {
		return Room{}, err
	}
	status, err := p.lc.GetRoomStatusInfoByUIDs(ctx,
		bilibiliLive.GetRoomStatusInfoByUIDsParam{UIDs: []int64{ref.UID}})
	release()
	if err != nil {
		return Room{}, p.wrap("房间详情", err)
	}
	item, ok := status[strconv.FormatInt(ref.UID, 10)]
	if !ok {
		return Room{}, &UpstreamError{
			Platform: Bilibili, Op: "房间详情",
			Message: fmt.Sprintf("上游没有返回 uid=%d 的房间信息", ref.UID),
		}
	}

	realID := strconv.FormatInt(item.RoomID, 10)
	if item.RoomID <= 0 {
		realID = strconv.Itoa(id)
	}
	return Room{
		Platform: Bilibili,
		RoomID:   realID,
		Title:    firstNonEmpty(item.Title, ref.Title),
		Nickname: item.Uname,
		Cover:    biliImage(firstNonEmpty(item.CoverFromUser, item.Keyframe)),
		Avatar:   biliAvatar(item.Face),
		Viewers:  item.Online,
		Area:     firstNonEmpty(item.AreaV2Name, item.AreaName),
		// 只有 live_status==1 才算直播中；2 是轮播，界面按未开播处理
		Live: item.LiveStatus == 1,
	}, nil
}

// 编译期确认 B 站满足 RoomInfoer。
var _ RoomInfoer = (*bilibiliProvider)(nil)
