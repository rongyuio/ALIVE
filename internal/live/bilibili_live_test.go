package live

import (
	"context"
	"os"
	"testing"
	"time"
)

// 真实上游的联通性检查，默认跳过：
//
//	LIVE_INTEGRATION=1 go test ./internal/live -run TestBilibiliLive
func TestBilibiliLive(t *testing.T) {
	if os.Getenv("LIVE_INTEGRATION") == "" {
		t.Skip("设置 LIVE_INTEGRATION=1 才打真实接口")
	}
	p, ok := ProviderFor(Bilibili)
	if !ok {
		t.Fatal("B 站 provider 未注册")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	groups, err := p.Categories(ctx)
	if err != nil {
		t.Fatalf("分区树失败：%v", err)
	}
	t.Logf("分区组 %d 个，第一组 %q 有 %d 个二级分区", len(groups), groups[0].Name, len(groups[0].Categories))

	// 网游 / 英雄联盟 = 2_86
	page, err := p.Rooms(ctx, "2_86", 1, 40)
	if err != nil {
		t.Fatalf("直播列表失败：%v", err)
	}
	t.Logf("英雄联盟第 1 页 %d 条，hasMore=%v", len(page.Rooms), page.HasMore)
	if len(page.Rooms) > 0 {
		r := page.Rooms[0]
		t.Logf("首条：%s / %s / 封面=%s", r.Nickname, r.Title, r.Cover)
	}

	rooms, err := p.Search(ctx, "英雄联盟")
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	t.Logf("搜索「英雄联盟」%d 条", len(rooms))
}

// TestBilibiliRoomInfo 验证「房间号 → 房间详情」这条链路。
//
//	LIVE_INTEGRATION=1 go test ./internal/live -run TestBilibiliRoomInfo
func TestBilibiliRoomInfo(t *testing.T) {
	if os.Getenv("LIVE_INTEGRATION") == "" {
		t.Skip("设置 LIVE_INTEGRATION=1 才打真实接口")
	}
	p, ok := ProviderFor(Bilibili)
	if !ok {
		t.Fatal("B 站 provider 未注册")
	}
	infoer, ok := p.(RoomInfoer)
	if !ok {
		t.Fatal("B 站没有实现 RoomInfoer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 先从分区列表拿一个真实房间号，不写死
	page, err := p.Rooms(ctx, "2_86", 1, 5)
	if err != nil {
		t.Fatalf("直播列表失败：%v", err)
	}
	if len(page.Rooms) == 0 {
		t.Skip("列表为空，拿不到房间号")
	}
	id := page.Rooms[0].RoomID

	room, err := infoer.RoomInfo(ctx, id)
	if err != nil {
		t.Fatalf("房间 %s 信息失败：%v", id, err)
	}
	t.Logf("房间 %s：%s / %s / 分区=%s / 人气=%d / 在播=%v / 封面=%s",
		room.RoomID, room.Nickname, room.Title, room.Area, room.Viewers, room.Live, room.Cover)
	if room.Nickname == "" || room.Title == "" {
		t.Errorf("昵称或标题为空：%+v", room)
	}

	// 不存在的房间要报业务错误，而不是被当成风控
	if _, err := infoer.RoomInfo(ctx, "999999999"); err != nil {
		if IsBlocked(err) {
			t.Errorf("不存在的房间被判成风控：%v", err)
		}
		t.Logf("不存在的房间：%v", err)
	} else {
		t.Log("不存在的房间没有报错（上游返回了空房间对象）")
	}
}
