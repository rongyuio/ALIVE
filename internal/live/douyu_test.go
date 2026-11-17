package live

import (
	"context"
	"errors"
	"strconv"
	"testing"
)

// 这些样本取自真实响应（字段做了裁剪，值保持原样）。
// 解析函数只吃字节，所以测试完全不碰网络。

const douyuCategorySample = `{
  "code": 0,
  "data": {
    "cate1Info": [
      {"cate1Id": 1, "cate1Name": "游戏竞技"},
      {"cate1Id": 2, "cate1Name": "娱乐天地"},
      {"cate1Id": 3, "cate1Name": "空分组"},
      {"cate1Id": "", "cate1Name": "没有 id"}
    ],
    "cate2Info": [
      {"cate1Id": 1, "cate2Id": 1, "cate2Name": "英雄联盟"},
      {"cate1Id": 1, "cate2Id": 1, "cate2Name": "英雄联盟（重复下发）"},
      {"cate1Id": 1, "cate2Id": 86, "cate2Name": "王者荣耀"},
      {"cate1Id": 2, "cate2Id": 311, "cate2Name": "颜值"},
      {"cate1Id": 2, "cate2Id": 208, "cate2Name": "一起看"},
      {"cate1Id": 2, "cate2Id": 999, "cate2Name": "一起看"},
      {"cate1Id": 3, "cate2Id": "", "cate2Name": "空 id"}
    ]
  }
}`

func TestParseDouyuCategories(t *testing.T) {
	groups, err := parseDouyuCategories([]byte(douyuCategorySample))
	if err != nil {
		t.Fatalf("解析分区失败：%v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("分区组 = %d，期望 2（空分组和无效 id 应被丢掉）", len(groups))
	}

	game := groups[0]
	if game.ID != "1" || game.Name != "游戏竞技" {
		t.Errorf("第一组 = %+v，期望 1/游戏竞技", game)
	}
	if len(game.Categories) != 2 {
		t.Fatalf("游戏竞技二级分区 = %d，期望 2（重复 id 只留一条）", len(game.Categories))
	}
	if game.Categories[0] != (Category{ID: "1", Name: "英雄联盟"}) {
		t.Errorf("首个二级分区 = %+v", game.Categories[0])
	}

	ent := groups[1]
	if ent.ID != "2" {
		t.Fatalf("第二组 id = %q，期望 2", ent.ID)
	}
	// 颜值 + 一个「一起看」；同名不同 id 的第二个应被丢掉。
	if len(ent.Categories) != 2 {
		t.Fatalf("娱乐天地二级分区 = %d，期望 2，实际 %+v", len(ent.Categories), ent.Categories)
	}
	if ent.Categories[1].Name != "一起看" || ent.Categories[1].ID != "208" {
		t.Errorf("一起看 = %+v，期望保留先出现的 208", ent.Categories[1])
	}
}

func TestBuildDouyuCategoryGroupsInjectsTogetherWatch(t *testing.T) {
	groups := buildDouyuCategoryGroups(
		[]douyuCate1{{ID: float64(2), Name: "娱乐天地"}},
		[]douyuCate2{{ParentID: "2", ID: "311", Name: "颜值"}},
	)
	if len(groups) != 1 {
		t.Fatalf("分区组 = %d，期望 1", len(groups))
	}
	cats := groups[0].Categories
	if len(cats) != 2 || cats[1] != (Category{ID: "208", Name: "一起看"}) {
		t.Fatalf("缺少「一起看」补位：%+v", cats)
	}
}

func TestParseDouyuCategoriesErrors(t *testing.T) {
	if _, err := parseDouyuCategories([]byte("<html>验证码</html>")); !errors.Is(err, ErrBlocked) {
		t.Errorf("非 JSON 应归为风控，实际 %v", err)
	}
	if _, err := parseDouyuCategories([]byte(`{"code":1001,"msg":"系统异常"}`)); err == nil {
		t.Error("业务错误码应返回错误")
	} else {
		var upstream *UpstreamError
		if !errors.As(err, &upstream) {
			t.Errorf("期望 UpstreamError，实际 %T：%v", err, err)
		}
	}
}

const douyuDirectorySample = `{
  "code": 0,
  "msg": "success",
  "data": {
    "pgcnt": 2,
    "rl": [
      {
        "rid": 6512,
        "rn": "玩传奇就看6512！！",
        "nn": "雨神丶",
        "av": "avatar_v3/201908/22af160f57c94db9b0f2396f2607da49",
        "ol": 3436345,
        "cid2": 1,
        "c2name": "英雄联盟",
        "rs1": "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0121.avif/dy2",
        "rs16": "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0121.avif/dy1"
      },
      {
        "rid": "288016",
        "rn": "峡谷之巅",
        "nn": "小主播",
        "av": "https://apic.douyucdn.cn/upload/avatar_v3/202608/abc_middle.jpg",
        "ol": "12.3万",
        "cid2": 1,
        "c2name": "英雄联盟",
        "rs1": "//rpic.douyucdn.cn/live-cover/roomCover/a.jpg/dy2",
        "rs16": "//rpic.douyucdn.cn/live-cover/roomCover/a.jpg/dy1"
      },
      {"rid": 0, "rn": "没有房间号", "nn": "ghost"}
    ]
  }
}`

func TestParseDouyuDirectory(t *testing.T) {
	page, err := parseDouyuDirectory([]byte(douyuDirectorySample))
	if err != nil {
		t.Fatalf("解析列表失败：%v", err)
	}
	if page.TotalPages != 2 {
		t.Errorf("总页数 = %d，期望 2", page.TotalPages)
	}
	if len(page.Rooms) != 2 {
		t.Fatalf("房间数 = %d，期望 2（rid=0 应被丢掉）", len(page.Rooms))
	}

	first := page.Rooms[0]
	if first.Platform != Douyu || first.RoomID != "6512" {
		t.Errorf("首条 id = %s/%s", first.Platform, first.RoomID)
	}
	if first.Title != "玩传奇就看6512！！" || first.Nickname != "雨神丶" {
		t.Errorf("首条标题/昵称 = %q / %q", first.Title, first.Nickname)
	}
	if first.Cover != "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0121.avif/dy1" {
		t.Errorf("封面 = %q", first.Cover)
	}
	wantAvatar := "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_big.jpg"
	if first.Avatar != wantAvatar {
		t.Errorf("头像 = %q，期望 %q", first.Avatar, wantAvatar)
	}
	if first.Viewers != 3436345 {
		t.Errorf("人气 = %d", first.Viewers)
	}
	if first.Area != "英雄联盟" || !first.Live {
		t.Errorf("分区/直播状态 = %q/%v", first.Area, first.Live)
	}

	second := page.Rooms[1]
	if second.RoomID != "288016" {
		t.Errorf("字符串 rid 未转成房间号：%q", second.RoomID)
	}
	if second.Viewers != 123000 {
		t.Errorf("「12.3万」解析 = %d，期望 123000", second.Viewers)
	}
	if second.Cover != "https://rpic.douyucdn.cn/live-cover/roomCover/a.jpg/dy1" {
		t.Errorf("协议相对封面未补全：%q", second.Cover)
	}
	if second.Avatar != "https://apic.douyucdn.cn/upload/avatar_v3/202608/abc_middle.jpg" {
		t.Errorf("已带扩展名的头像被改写：%q", second.Avatar)
	}
}

func TestParseDouyuDirectoryErrors(t *testing.T) {
	if _, err := parseDouyuDirectory([]byte("not json")); !errors.Is(err, ErrBlocked) {
		t.Errorf("非 JSON 应归为风控，实际 %v", err)
	}
	if _, err := parseDouyuDirectory([]byte(`{"code":1,"msg":"系统异常，请重试"}`)); err == nil {
		t.Error("业务错误码应返回错误")
	}
}

const douyuSearchSample = `{
  "error": 0,
  "msg": "",
  "data": {
    "pageSize": 20,
    "relateUser": [
      {
        "type": 1,
        "anchorInfo": {
          "rid": 6512,
          "nickName": "雨神丶",
          "roomName": "玩传奇就看6512！！",
          "avatar": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_middle.jpg",
          "roomSrc": "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0121.avif/dy1",
          "cateName": "英雄联盟",
          "isLive": 1,
          "isLoop": 0,
          "videoLoop": 0,
          "bkUrl": "https://www.douyu.com/6512"
        }
      },
      {
        "type": 1,
        "anchorInfo": {
          "rid": null,
          "nickname": "夜猫",
          "roomName": null,
          "description": "旧标题",
          "avatar": "avatar_v3/202001/abcdef",
          "cateName": "主机游戏",
          "isLive": 2,
          "isLoop": 1,
          "videoLoop": 0,
          "bkUrl": "https://www.douyu.com/9999/"
        }
      },
      {"type": 2, "anchorInfo": {"rid": 123456, "nickName": "分类结果"}},
      {"type": 1, "anchorInfo": {"rid": 777, "nickName": ""}}
    ]
  }
}`

func TestParseDouyuSearch(t *testing.T) {
	rooms, err := parseDouyuSearch([]byte(douyuSearchSample))
	if err != nil {
		t.Fatalf("解析搜索失败：%v", err)
	}
	if len(rooms) != 2 {
		t.Fatalf("搜索结果 = %d，期望 2（非主播项与缺昵称项应被丢掉）", len(rooms))
	}

	live := rooms[0]
	if live.RoomID != "6512" || live.Nickname != "雨神丶" || !live.Live {
		t.Errorf("直播中的结果 = %+v", live)
	}
	if live.Area != "英雄联盟" || live.Cover == "" {
		t.Errorf("分区/封面缺失：%+v", live)
	}

	offline := rooms[1]
	if offline.RoomID != "9999" {
		t.Errorf("bkUrl 兜底房间号 = %q，期望 9999", offline.RoomID)
	}
	if offline.Title != "旧标题" {
		t.Errorf("标题兜底 = %q，期望 description", offline.Title)
	}
	if offline.Live {
		t.Error("isLive=2 应视为未开播")
	}
	if want := "https://apic.douyucdn.cn/upload/avatar_v3/202001/abcdef_big.jpg"; offline.Avatar != want {
		t.Errorf("头像 = %q，期望 %q", offline.Avatar, want)
	}
}

func TestParseDouyuSearchRelateUserListFallback(t *testing.T) {
	body := `{"error":0,"data":{"relateUserList":[
      {"type":1,"anchorInfo":{"rid":42,"nickName":"只在旧字段里的人","isLive":1}}
    ]}}`
	rooms, err := parseDouyuSearch([]byte(body))
	if err != nil {
		t.Fatalf("解析搜索失败：%v", err)
	}
	if len(rooms) != 1 || rooms[0].RoomID != "42" {
		t.Fatalf("relateUserList 兜底失败：%+v", rooms)
	}
}

func TestDouyuPageOffset(t *testing.T) {
	cases := []struct {
		page, size      int
		upstream, start int
	}{
		{1, 40, 1, 0},
		{2, 40, 1, 40},
		{3, 40, 1, 80},
		{4, 40, 2, 0},
		{2, 120, 2, 0},
		{11, 40, 4, 40},
	}
	for _, c := range cases {
		upstream, start := douyuPageOffset(c.page, c.size)
		if upstream != c.upstream || start != c.start {
			t.Errorf("page=%d size=%d → (%d,%d)，期望 (%d,%d)",
				c.page, c.size, upstream, start, c.upstream, c.start)
		}
	}
}

func TestDouyuWindow(t *testing.T) {
	rows := make([]Room, 120)
	for i := range rows {
		rows[i].RoomID = strconv.Itoa(i)
	}

	got, more := douyuWindow(rows, 0, 40)
	if len(got) != 40 || got[0].RoomID != "0" || !more {
		t.Errorf("start=0: len=%d more=%v", len(got), more)
	}

	got, more = douyuWindow(rows, 80, 40)
	if len(got) != 40 || got[0].RoomID != "80" || more {
		t.Errorf("start=80: len=%d more=%v", len(got), more)
	}

	got, more = douyuWindow(rows, 110, 40)
	if len(got) != 10 || got[0].RoomID != "110" || more {
		t.Errorf("start=110: len=%d more=%v", len(got), more)
	}

	if got, more := douyuWindow(rows, 120, 40); got != nil || more {
		t.Errorf("越界应返回空：len=%d more=%v", len(got), more)
	}
}

// ---------------------------------------------------------------- 房间实时信息

// betard 的真实响应（2026-11-14 取自直播间 6512，只保留卡片用到的字段）。
const douyuRoomSample = `{
  "room_gg": {"show": "", "status": 0},
  "video_links": [{"id": "4645"}],
  "room": {
    "room_id": 6512,
    "room_name": "玩传奇就看6512！！",
    "nickname": "雨神丶",
    "owner_name": "雨神丶",
    "avatar_mid": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_middle.jpg",
    "owner_avatar": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_big.jpg",
    "avatar": {
      "big": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_big.jpg",
      "middle": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_middle.jpg",
      "small": "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_small.jpg"
    },
    "room_src": "asrpic/261114/6512_src_0131.avif/dy4",
    "rs1": "/asrpic/261114/6512_src_0131.avif/dy1",
    "room_pic": "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0131.avif/dy4",
    "coverSrc": "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0131.avif/dy4",
    "show_status": 1,
    "videoLoop": 0,
    "isLoop": null,
    "second_lvl_name": "英雄联盟",
    "cate2_id": "1",
    "online": null,
    "room_biz_all": {"hot": "3430358", "videoLoop": 0}
  }
}`

// 房间不存在/已关闭时斗鱼返回的是 HTTP 200 的 HTML 提示页，不是 JSON。
const douyuRoomErrorPage = `<!DOCTYPE html PUBLIC "-//W3C//DTD XHTML 1.0 Transitional//EN" "http://www.w3.org/TR/xhtml1/DTD/xhtml1-transitional.dtd">
<html xmlns="http://www.w3.org/1999/xhtml">
<head>
<meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
<title>提示信息 -斗鱼</title>
</head>
<body>
<div class="error">
<div class="pic"></div>
<span><p>该房间目前没有开放</p></span>
<a href="/" class="sub">返回首页</a>
</div>
</body>
</html>`

func TestParseDouyuRoom(t *testing.T) {
	room, err := parseDouyuRoom([]byte(douyuRoomSample), "6512")
	if err != nil {
		t.Fatalf("解析房间信息失败：%v", err)
	}
	if room.Platform != Douyu || room.RoomID != "6512" {
		t.Errorf("平台/房间号 = %s/%s", room.Platform, room.RoomID)
	}
	if room.Title != "玩传奇就看6512！！" || room.Nickname != "雨神丶" {
		t.Errorf("标题/昵称 = %q / %q", room.Title, room.Nickname)
	}
	if want := "https://rpic.douyucdn.cn/asrpic/261114/6512_src_0131.avif/dy1"; room.Cover != want {
		t.Errorf("封面 = %q，期望 %q", room.Cover, want)
	}
	wantAvatar := "https://apic.douyucdn.cn/upload/avatar_v3/201908/22af160f57c94db9b0f2396f2607da49_middle.jpg"
	if room.Avatar != wantAvatar {
		t.Errorf("头像 = %q，期望 %q", room.Avatar, wantAvatar)
	}
	if room.Viewers != 3430358 {
		t.Errorf("人气 = %d，期望 3430358", room.Viewers)
	}
	if room.Area != "英雄联盟" || !room.Live {
		t.Errorf("分区/直播状态 = %q/%v", room.Area, room.Live)
	}
}

func TestParseDouyuRoomOfflineAndLoop(t *testing.T) {
	// show_status=2 是未开播/已结束。
	offline := `{"room":{"room_id":288016,"room_name":"【重播】","nickname":"赛事","show_status":2,
      "room_biz_all":{"hot":"0"}}}`
	room, err := parseDouyuRoom([]byte(offline), "288016")
	if err != nil {
		t.Fatalf("解析未开播房间失败：%v", err)
	}
	if room.Live {
		t.Error("show_status=2 应视为未开播")
	}
	if room.Area != "" || room.Cover != "" {
		t.Errorf("缺失字段不应拼出内容：area=%q cover=%q", room.Area, room.Cover)
	}

	// 轮播：show_status=1 但 videoLoop=1，同样不算直播中（实测房间 9999 就是这种）。
	loop := `{"room":{"room_id":9999,"room_name":"每天","nickname":"yyfyyf","show_status":1,
      "videoLoop":1,"isLoop":null,"room_biz_all":{"hot":"4456949"}}}`
	room, err = parseDouyuRoom([]byte(loop), "9999")
	if err != nil {
		t.Fatalf("解析轮播房间失败：%v", err)
	}
	if room.Live {
		t.Error("videoLoop=1 应视为轮播，不算直播中")
	}

	// isLoop=1 是另一种轮播标记。
	isLoop := `{"room":{"room_id":1,"show_status":1,"isLoop":1}}`
	room, err = parseDouyuRoom([]byte(isLoop), "1")
	if err != nil {
		t.Fatalf("解析 isLoop 房间失败：%v", err)
	}
	if room.Live {
		t.Error("isLoop=1 应视为轮播，不算直播中")
	}
}

func TestParseDouyuRoomFallbacks(t *testing.T) {
	// room_id 缺失时用调用方给的房间号；avatar 是字符串形态；
	// 封面只给相对路径；人气退回 online；昵称退回 owner_name。
	body := `{"room":{"room_name":"","owner_name":"小助手",
      "avatar":"avatar_v3/202001/abcdef","room_src":"asrpic/x.avif/dy4",
      "show_status":"1","online":"1.2万","second_lvl_name":"","cate2_name":"主机游戏"}}`
	room, err := parseDouyuRoom([]byte(body), "606118")
	if err != nil {
		t.Fatalf("解析兜底房间失败：%v", err)
	}
	if room.RoomID != "606118" {
		t.Errorf("房间号兜底 = %q，期望 606118", room.RoomID)
	}
	if room.Title != "暂无标题" || room.Nickname != "小助手" {
		t.Errorf("标题/昵称兜底 = %q / %q", room.Title, room.Nickname)
	}
	if want := "https://apic.douyucdn.cn/upload/avatar_v3/202001/abcdef_big.jpg"; room.Avatar != want {
		t.Errorf("字符串 avatar 兜底 = %q，期望 %q", room.Avatar, want)
	}
	if want := "https://rpic.douyucdn.cn/asrpic/x.avif/dy4"; room.Cover != want {
		t.Errorf("相对封面未补全：%q", room.Cover)
	}
	if room.Viewers != 12000 || room.Area != "主机游戏" || !room.Live {
		t.Errorf("人气/分区/状态 = %d/%q/%v", room.Viewers, room.Area, room.Live)
	}
}

func TestParseDouyuRoomErrors(t *testing.T) {
	// 房间不存在/已关闭：业务错误，不能归到风控。
	_, err := parseDouyuRoom([]byte(douyuRoomErrorPage), "999999999")
	if err == nil {
		t.Fatal("房间提示页应返回错误")
	}
	if IsBlocked(err) {
		t.Errorf("房间不存在不应算风控：%v", err)
	}
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("期望 UpstreamError，实际 %T：%v", err, err)
	}
	if upstream.Message != "该房间目前没有开放" {
		t.Errorf("提示页文案 = %q", upstream.Message)
	}

	// 没有房间提示页标记的非 JSON 仍是验证页/空响应，按风控处理。
	if _, err := parseDouyuRoom([]byte(`<html><body>请完成验证</body></html>`), "1"); !errors.Is(err, ErrBlocked) {
		t.Errorf("验证页应归为风控，实际 %v", err)
	}

	// 业务错误码。
	_, err = parseDouyuRoom([]byte(`{"error":1,"msg":"系统异常"}`), "1")
	if err == nil {
		t.Error("业务错误码应返回错误")
	} else if IsBlocked(err) {
		t.Errorf("业务错误码不应算风控：%v", err)
	}

	// 解析成功但没有 room 对象。
	if _, err := parseDouyuRoom([]byte(`{"room_gg":{"status":0}}`), "1"); err == nil {
		t.Error("缺少 room 对象应返回错误")
	} else if IsBlocked(err) {
		t.Errorf("缺少 room 对象不应算风控：%v", err)
	}
}

func TestDouyuRoomInfoRejectsBadRoomID(t *testing.T) {
	p := newDouyu()
	for _, id := range []string{"", "  ", "abc", "https://www.douyu.com/6512", "65 12"} {
		if _, err := p.RoomInfo(context.Background(), id); err == nil {
			t.Errorf("房间号 %q 应被拒绝", id)
		}
	}
}
