package live

import (
	"errors"
	"strings"
	"testing"
)

// 下面两个样本取自 2026-11 的真实响应（只裁掉未使用的字段），
// 测试不联网，字段值原样保留，方便对着上游变化核对。
const huyaListSample = `{
  "tCacheInfo": {
    "iSourceType": 2,
    "iUpdateTime": 1794590670,
    "iCurrentTime": 1794590672,
    "iDiffTime": 2
  },
  "vList": [
    {
      "lProfileRoom": 518518,
      "sNick": "Letme严君泽",
      "sIntroduction": "男刀来了",
      "sScreenshot": "http://live-cover.msstatic.com/huyalive/1430972471-1430972471-6145979964421308416-2862068398-10057-A-0-1-imgplus/20261114012354.jpg",
      "sAvatar180": "https://huyaimg.msstatic.com/avatar/1073/fe/1a689ed11d9f778a6a1be6054052a9_180_135.jpg?1790611908",
      "lUserCount": 1827963,
      "sGameFullName": "英雄联盟",
      "sGameHostName": "lol"
    },
    {
      "lProfileRoom": 572329,
      "sNick": "北枫CC",
      "sIntroduction": "大师局elo小代号上钻教学 ～枫神归来",
      "sScreenshot": "http://live-cover.msstatic.com/huyalive/2183525275-2183525275-9378169646114406400-4367174006-10057-A-0-1-imgplus/20261114012359.jpg",
      "sAvatar180": "https://huyaimg.msstatic.com/avatar/1048/b7/ad1b03bbf11572bc8859f7a8fe5b1b_180_135.jpg?1585153940",
      "lUserCount": 1801241,
      "sGameFullName": "英雄联盟",
      "sGameHostName": "lol"
    }
  ],
  "iPageNo": 1,
  "iPageSize": 120,
  "iTotalPage": 4,
  "iTotal": 470
}`

const huyaSearchSample = `{
  "response": {
    "1": {
      "docs": [
        {
          "gameLiveOn": true,
          "game_avatarUrl180": "https://huyaimg.msstatic.com/avatar/1042/6f/3626ef611e990cbbcefab8e1bbc561_180_135.jpg?1792072553",
          "game_name": "英雄联盟",
          "game_nick": "虎牙英雄联盟赛事",
          "live_intro": "【预告】15日3点 GEN vs BLG S16总决赛",
          "room_id": 660000,
          "game_liveLink": "http://www.huya.com/lpl"
        },
        {
          "gameLiveOn": false,
          "game_avatarUrl180": "https://huyaimg.msstatic.com/avatar/1076/84/3eb4c9c4877fc675df448a78bd768f_180_135.jpg?1549028836",
          "game_name": "英雄联盟",
          "game_nick": "Zz1tai姿态",
          "live_intro": "欢迎来到我的直播间",
          "room_id": 333003,
          "game_liveLink": "http://www.huya.com/333003"
        },
        {
          "gameLiveOn": false,
          "game_avatarUrl180": "http://huyaimg.msstatic.com/avatar/1095/83/2aa2f6905fe4382221d08b66d7cdcb_180_135.jpg?1410599038",
          "game_name": "英雄联盟",
          "game_nick": "胖炸",
          "live_intro": "8进6淘汰赛【八店联合赛】",
          "room_id": 441195,
          "game_liveLink": "http://www.huya.com/baozha"
        },
        {
          "gameLiveOn": false,
          "game_avatarUrl180": "https://huyaimg.msstatic.com/avatar/1076/84/3eb4c9c4877fc675df448a78bd768f_180_135.jpg?1549028836",
          "game_name": "英雄联盟",
          "game_nick": "Zz1tai姿态",
          "live_intro": "欢迎来到我的直播间",
          "room_id": 333003,
          "game_liveLink": "http://www.huya.com/333003"
        }
      ],
      "numFound": 1626,
      "start": 0
    },
    "1026": [],
    "1027": []
  },
  "responseHeader": {
    "QTime": 578,
    "status": 0
  }
}`

func TestParseHuyaLiveList(t *testing.T) {
	page, err := parseHuyaLiveList([]byte(huyaListSample), 1, 40)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(page.Rooms) != 2 {
		t.Fatalf("房间数 = %d，想要 2", len(page.Rooms))
	}
	if !page.HasMore {
		t.Error("iTotalPage=4 且当前是第 1 页，HasMore 应为 true")
	}

	r := page.Rooms[0]
	if r.Platform != Huya {
		t.Errorf("Platform = %q", r.Platform)
	}
	if r.RoomID != "518518" {
		t.Errorf("RoomID = %q", r.RoomID)
	}
	if r.Nickname != "Letme严君泽" {
		t.Errorf("Nickname = %q", r.Nickname)
	}
	if r.Title != "男刀来了" {
		t.Errorf("Title = %q", r.Title)
	}
	// 封面上游给的是 http，统一升级成 https，避免被当成不安全资源拦掉。
	if got, want := r.Cover, "https://live-cover.msstatic.com/huyalive/1430972471-1430972471-6145979964421308416-2862068398-10057-A-0-1-imgplus/20261114012354.jpg"; got != want {
		t.Errorf("Cover = %q，想要 %q", got, want)
	}
	if r.Avatar != "https://huyaimg.msstatic.com/avatar/1073/fe/1a689ed11d9f778a6a1be6054052a9_180_135.jpg?1790611908" {
		t.Errorf("Avatar = %q", r.Avatar)
	}
	if r.Viewers != 1827963 {
		t.Errorf("Viewers = %d", r.Viewers)
	}
	if r.Area != "英雄联盟" {
		t.Errorf("Area = %q", r.Area)
	}
	if !r.Live {
		t.Error("列表接口只返回在播房间，Live 应为 true")
	}
}

func TestParseHuyaLiveListSlicesLogicalPage(t *testing.T) {
	// 样本里只有 2 条，但上游的实际页宽是 120；逻辑页要按 size 切片。
	page, err := parseHuyaLiveList([]byte(huyaListSample), 2, 1)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(page.Rooms) != 1 || page.Rooms[0].RoomID != "572329" {
		t.Fatalf("第 2 个逻辑页 = %+v，想要房间 572329", page.Rooms)
	}
	if !page.HasMore {
		t.Error("同页后面还有房间，HasMore 应为 true")
	}

	// 越过真实数据结尾时不能再声称还有下一页。
	last, err := parseHuyaLiveList([]byte(`{"vList":[],"iPageNo":9,"iPageSize":120,"iTotalPage":1,"iTotal":2}`), 9, 40)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if last.HasMore || len(last.Rooms) != 0 {
		t.Errorf("空页 = %+v，想要 0 条且 HasMore=false", last)
	}
}

func TestParseHuyaLiveListFallbackHasMore(t *testing.T) {
	// iTotalPage / iTotal 都缺失（极少数分区）：只能退化成“本页满员”。
	body := `{"vList":[{"lProfileRoom":1}],"iPageNo":1,"iPageSize":120}`
	page, err := parseHuyaLiveList([]byte(body), 1, 40)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if page.HasMore {
		t.Error("只有 1 条且没有分页字段，HasMore 应为 false")
	}
}

func TestParseHuyaLiveListNotJSON(t *testing.T) {
	_, err := parseHuyaLiveList([]byte("<html>验证</html>"), 1, 40)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("错误 = %v，想要 ErrBlocked", err)
	}
}

func TestParseHuyaSearch(t *testing.T) {
	rooms, err := parseHuyaSearch([]byte(huyaSearchSample))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 样本第 4 条与第 2 条是同一个房间，应被去重。
	if len(rooms) != 3 {
		t.Fatalf("结果数 = %d，想要 3（去重后）", len(rooms))
	}
	first := rooms[0]
	if first.RoomID != "660000" || first.Nickname != "虎牙英雄联盟赛事" {
		t.Errorf("首条 = %+v", first)
	}
	if !first.Live {
		t.Error("gameLiveOn=true 时 Live 应为 true")
	}
	if first.Area != "英雄联盟" {
		t.Errorf("Area = %q", first.Area)
	}
	if first.Title != "【预告】15日3点 GEN vs BLG S16总决赛" {
		t.Errorf("Title = %q", first.Title)
	}
	// 搜索结果不带封面，保持为空，让卡片回落到占位图。
	if first.Cover != "" {
		t.Errorf("Cover = %q，想要空", first.Cover)
	}
	if rooms[1].Live {
		t.Error("gameLiveOn=false 时 Live 应为 false")
	}
	// 头像给的是 http，同样要升级成 https。
	if rooms[2].Avatar != "https://huyaimg.msstatic.com/avatar/1095/83/2aa2f6905fe4382221d08b66d7cdcb_180_135.jpg?1410599038" {
		t.Errorf("Avatar = %q", rooms[2].Avatar)
	}
}

func TestParseHuyaSearchStatusError(t *testing.T) {
	body := `{"responseHeader":{"status":1001},"response":{}}`
	_, err := parseHuyaSearch([]byte(body))
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("错误 = %v，想要 UpstreamError", err)
	}
}

func TestParseHuyaCategories(t *testing.T) {
	groups, err := parseHuyaCategories()
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("一级分区 = %d，想要 4", len(groups))
	}
	if groups[0].ID != "ol" || groups[0].Name != "网游竞技" {
		t.Errorf("首组 = %+v", groups[0])
	}
	if len(groups[0].Categories) != 108 {
		t.Errorf("网游竞技二级分区 = %d，想要 108", len(groups[0].Categories))
	}
	if c := groups[0].Categories[0]; c.ID != "1" || c.Name != "英雄联盟" {
		t.Errorf("首个二级分区 = %+v", c)
	}
	// 每个二级分区都必须有接口 id，否则列表请求会打空。
	for _, g := range groups {
		for _, c := range g.Categories {
			if c.ID == "" || c.Name == "" {
				t.Fatalf("分区 %q/%q 缺字段", g.Name, c.Name)
			}
		}
	}
}

func TestNormalizeHuyaGID(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"1", "1", false},
		{" 862 ", "862", false},
		{"https://www.huya.com/g/lol", "1", false},
		{"www.huya.com/g/dota2", "7", false},
		{"lol", "1", false}, // 网页 slug 不是 iGid，要用静态表换回数字
		{"g_862", "862", false},
		{"", "", true},
		{"https://www.huya.com/", "", true},
	}
	for _, c := range cases {
		got, err := normalizeHuyaGID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeHuyaGID(%q) err = %v", c.in, err)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("normalizeHuyaGID(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}

func TestHuyaJSONHelpers(t *testing.T) {
	// 同一字段上游会在数字/字符串/科学计数法之间漂移。
	if got := huyaJSONString(float64(1827963)); got != "1827963" {
		t.Errorf("float 整数转字符串 = %q", got)
	}
	if got := huyaJSONFloat("2.52e+06"); got != 2520000 {
		t.Errorf("科学计数法字符串 = %v", got)
	}
	if got := huyaJSONFloat(1801241.0); got != 1801241 {
		t.Errorf("float = %v", got)
	}
	if !huyaJSONBool("true") || !huyaJSONBool(float64(1)) || huyaJSONBool("0") {
		t.Error("boolean 归一化不正确")
	}
}

// 房间档案样本取自 2026-11 的真实响应（只保留用到的字段）：
// 在播 518518、未开播 333003、不存在的主播。测试不联网。
const huyaRoomLiveSample = `{
  "status": 200,
  "message": "",
  "data": {
    "liveStatus": "ON",
    "realLiveStatus": "ON",
    "profileInfo": {
      "uid": 1430972471,
      "yyid": 1594139863,
      "nick": "Letme严君泽",
      "avatar180": "https://huyaimg.msstatic.com/avatar/1073/fe/1a689ed11d9f778a6a1be6054052a9_180_135.jpg?1790611908",
      "profileRoom": 518518
    },
    "liveData": {
      "nick": "Letme严君泽",
      "avatar180": "https://huyaimg.msstatic.com/avatar/1073/fe/1a689ed11d9f778a6a1be6054052a9_180_135.jpg?1790611908",
      "introduction": "男刀来了",
      "roomName": "",
      "userCount": 1842585,
      "totalCount": 1842585,
      "gameFullName": "英雄联盟",
      "gameHostName": "lol",
      "gid": 1,
      "screenshot": "http://live-cover.msstatic.com/huyalive/1430972471-1430972471-6145979964421308416-2862068398-10057-A-0-1-imgplus/20261114013312.jpg",
      "profileRoom": 518518
    },
    "stream": {
      "baseSteamInfoList": [
        {"sCdnType": "tx", "sStreamName": "1430972471-1430972471-6145979964421308416"}
      ]
    }
  }
}`

// 未开播房间的真实样本：没有 userCount，也不带 stream；
// gameHostName 在这里是 gid 数字而不是 slug。
const huyaRoomOfflineSample = `{
  "status": 200,
  "message": "",
  "data": {
    "liveStatus": "OFF",
    "realLiveStatus": "OFF",
    "profileInfo": {
      "uid": 2367547387,
      "yyid": 2369247112,
      "nick": "Zz1tai姿态",
      "avatar180": "https://huyaimg.msstatic.com/avatar/1076/84/3eb4c9c4877fc675df448a78bd768f_180_135.jpg?1549028836",
      "profileRoom": "333003"
    },
    "liveData": {
      "nick": "Zz1tai姿态",
      "avatar180": "https://huyaimg.msstatic.com/avatar/1076/84/3eb4c9c4877fc675df448a78bd768f_180_135.jpg?1549028836",
      "introduction": "欢迎来到我的直播间",
      "roomName": "",
      "totalCount": 3120234,
      "attendeeCount": 3120234,
      "gameFullName": "英雄联盟",
      "gameHostName": 1,
      "gid": 1,
      "screenshot": "http://live-cover.msstatic.com/huyalive/2367547387-2367547387-10168538598895255552-4735218230-10057-A-0-1-imgplus/20261114004103.jpg",
      "profileRoom": "333003"
    }
  }
}`

const huyaRoomMissingSample = `{"status":422,"message":"该主播不存在！","data":[]}`

func TestParseHuyaRoomProfileLive(t *testing.T) {
	room, err := parseHuyaRoomProfile([]byte(huyaRoomLiveSample), "518518")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if room.Platform != Huya || room.RoomID != "518518" {
		t.Errorf("Platform/RoomID = %q/%q", room.Platform, room.RoomID)
	}
	if room.Nickname != "Letme严君泽" || room.Title != "男刀来了" {
		t.Errorf("Nickname/Title = %q/%q", room.Nickname, room.Title)
	}
	if room.Area != "英雄联盟" {
		t.Errorf("Area = %q，想要 英雄联盟", room.Area)
	}
	if room.Viewers != 1842585 {
		t.Errorf("Viewers = %d，想要 1842585", room.Viewers)
	}
	if !room.Live {
		t.Error("realLiveStatus=ON，Live 应为 true")
	}
	// 封面/头像统一 https：上游偶尔给 http，输出层统一升级
	if want := "https://live-cover.msstatic.com/huyalive/1430972471-1430972471-6145979964421308416-2862068398-10057-A-0-1-imgplus/20261114013312.jpg"; room.Cover != want {
		t.Errorf("Cover = %q，想要 %q", room.Cover, want)
	}
	if !strings.HasPrefix(room.Avatar, "https://huyaimg.msstatic.com/avatar/1073/fe/") {
		t.Errorf("Avatar = %q", room.Avatar)
	}
}

func TestParseHuyaRoomProfileOffline(t *testing.T) {
	room, err := parseHuyaRoomProfile([]byte(huyaRoomOfflineSample), "333003")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if room.Live {
		t.Error("liveStatus=OFF，Live 应为 false")
	}
	// totalCount 是累计观众量，未开播时不能当成人气显示
	if room.Viewers != 0 {
		t.Errorf("Viewers = %d，未开播时应为 0", room.Viewers)
	}
	// gameHostName=1 是 gid，不能当成区名；真实区名来自 gameFullName
	if room.Area != "英雄联盟" {
		t.Errorf("Area = %q，想要 英雄联盟", room.Area)
	}
	if room.RoomID != "333003" || room.Nickname != "Zz1tai姿态" {
		t.Errorf("RoomID/Nickname = %q/%q", room.RoomID, room.Nickname)
	}
}

func TestParseHuyaRoomProfileStreamFallback(t *testing.T) {
	// 状态字段缺失（刚开播的缓存空窗）时用 stream 是否存在兜底
	body := `{"status":200,"data":{
	  "liveData":{"profileRoom":1,"nick":"n","userCount":10},
	  "stream":{"baseSteamInfoList":[{"sCdnType":"tx"}]}
	}}`
	room, err := parseHuyaRoomProfile([]byte(body), "1")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !room.Live || room.Viewers != 10 {
		t.Errorf("room = %+v，想要 Live=true Viewers=10", room)
	}
}

func TestParseHuyaRoomProfileMissing(t *testing.T) {
	_, err := parseHuyaRoomProfile([]byte(huyaRoomMissingSample), "99999999")
	var upstream *UpstreamError
	if !errors.As(err, &upstream) {
		t.Fatalf("错误 = %v，想要 UpstreamError", err)
	}
	if upstream.Message != "该主播不存在！" {
		t.Errorf("Message = %q", upstream.Message)
	}
}

func TestParseHuyaRoomProfileNotJSON(t *testing.T) {
	_, err := parseHuyaRoomProfile([]byte("<html>验证</html>"), "1")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("错误 = %v，想要 ErrBlocked", err)
	}
}

func TestParseHuyaRoomProfileEmptyData(t *testing.T) {
	// status=200 但没有任何对象：不能映射成一个空白房间写回关注列表
	for _, body := range []string{
		`{"status":200,"message":"","data":{}}`,
		`{"status":200,"message":"","data":[]}`,
	} {
		_, err := parseHuyaRoomProfile([]byte(body), "1")
		var upstream *UpstreamError
		if !errors.As(err, &upstream) {
			t.Errorf("body=%s 错误 = %v，想要 UpstreamError", body, err)
		}
	}
}

func TestParseHuyaRoomProfileBlockedStatus(t *testing.T) {
	// 业务状态码镜像风控时也要归到 ErrBlocked，上层才会提示「请求过于频繁」
	_, err := parseHuyaRoomProfile([]byte(`{"status":412,"message":"blocked","data":[]}`), "1")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("错误 = %v，想要 ErrBlocked", err)
	}
}

func TestNormalizeHuyaRoomID(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		{"518518", "518518", false},
		{" 333003 ", "333003", false},
		{"https://www.huya.com/518518", "518518", false},
		{"https://www.huya.com/518518?x=1", "518518", false},
		{"www.huya.com/660000", "660000", false},
		{"", "", true},
		{"https://www.huya.com/", "", true},
	}
	for _, c := range cases {
		got, err := normalizeHuyaRoomID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("normalizeHuyaRoomID(%q) err = %v", c.in, err)
			continue
		}
		if !c.wantErr && got != c.want {
			t.Errorf("normalizeHuyaRoomID(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}
