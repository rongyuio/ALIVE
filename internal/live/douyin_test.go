package live

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

/* ------------------------------------------------------------ 签名向量 */

// SM3 标准测试向量（GB/T 32905-2016 / 公开测试集）。
func TestDouyinSM3Vectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			"abc",
			"66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0",
		},
		{
			"abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd",
			"debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732",
		},
	}
	for _, c := range cases {
		got := douyinSM3Sum([]byte(c.in))
		if hex := encodeHex(got); hex != c.want {
			t.Errorf("SM3(%q) = %s, want %s", c.in, hex, c.want)
		}
	}
}

// RC4 经典测试向量（key=Key, plaintext=Plaintext）。
func TestDouyinRC4Vector(t *testing.T) {
	got := douyinRC4([]byte("Plaintext"), []byte("Key"))
	if want := "bbf316e8d940af0ad3"; encodeHex(got) != want {
		t.Fatalf("RC4 = %s, want %s", encodeHex(got), want)
	}
}

// 编码表 s0 是标准 base64：用它和标准库对拍，验证 24 位分组的位运算。
// 只比整组：这个变体在不足 3 字节时用表首字符补齐，而不是标准 base64 的 '='。
func TestDouyinEncodeMatchesStdBase64(t *testing.T) {
	inputs := [][]byte{
		{},
		{'a', 'b', 'c'},
		[]byte("hello,douyin"),
		{0x00, 0xff, 0x10, 0x7f, 0x80, 0x01},
	}
	for _, in := range inputs {
		want := base64.StdEncoding.EncodeToString(in)
		if got := douyinEncode(in, "s0"); got != want {
			t.Errorf("encode(%v) = %q, want %q", in, got, want)
		}
	}
}

// a_bogus 固定向量。
//
// 来源：由本实现生成；同一实现运行中的签名在 2026-11-14 对真实
// partition/detail/room/v2 接口返回了完整列表（未返回空数据），
// 说明签名被上游接受。时间戳固定后输出确定，这里锁定它防回归。
func TestDouyinABogusVector(t *testing.T) {
	query := "aid=6383&app_name=douyin_web&live_id=1&device_platform=web&language=zh-CN" +
		"&enter_from=web_homepage_hot&cookie_enabled=true&screen_width=1920&screen_height=1080" +
		"&browser_language=zh-CN&browser_platform=Win32&browser_name=Chrome" +
		"&browser_version=140.0.0.0&count=15&offset=0&partition=1010032&partition_type=1" +
		"&req_from=2&msToken=FIXED_TOKEN_FOR_TEST"
	want := "E7mhBmg6mEVNgf6X5lQLfY3q6IF3Y4OT0HViMD2flVvM1L39HMYD9exobRhvwKWjiG/BIeYjy4hbO3xprQAjM36UHWwEUdQ2mgWkKl5Q5I0j53iruyRDntmF4vj3SFlm5XNAEOk0y75rKb70Woqe-vIlO62-zo0/9VfD="

	got := douyinABogus(query, desktopUA, time.UnixMilli(1767225600000))
	if got != want {
		t.Fatalf("a_bogus = %q, want %q", got, want)
	}
	// 时间戳变了输出必须变，否则说明时间字段没进签名。
	if again := douyinABogus(query, desktopUA, time.UnixMilli(1767225600001)); again == got {
		t.Fatal("a_bogus 未随时间戳变化")
	}
}

/* ------------------------------------------------------------ 解析 */

// 列表响应解析：样本结构取自 2026-11-14 的真实 partition 响应
// （字段名、层级、user_count 的位置都照抄，数值改成虚构）。
const douyinPartitionSample = `{
  "status_code": 0,
  "data": {
    "count": 15,
    "offset": 15,
    "data": [
      {
        "web_rid": "100001",
        "room": {
          "id_str": "7000000000000000001",
          "status": 2,
          "title": "测试直播间",
          "user_count": 12345,
          "cover": {"url_list": ["//p3.douyinpic.com/cover.jpeg"]},
          "owner": {
            "nickname": "示例主播",
            "avatar_thumb": {"url_list": ["https://p3.douyinpic.com/avatar.jpeg"]}
          },
          "stats": {"total_user_str": "2.5万", "user_count_str": "1.2万"}
        }
      },
      {
        "web_rid": "100002",
        "room": {
          "id_str": "7000000000000000002",
          "status": 0,
          "title": "没有精确人数",
          "cover": {"url_list": ["https://p3.douyinpic.com/cover2.jpeg"]},
          "owner": {"nickname": "第二位", "avatar_thumb": {"url_list": []}},
          "stats": {"total_user_str": "3.4亿", "user_count_str": ""}
        }
      },
      {
        "web_rid": "",
        "room": {"id_str": "", "title": "无效记录"}
      }
    ]
  }
}`

func TestDouyinPartitionParse(t *testing.T) {
	var resp douyinPartitionResponse
	if err := json.Unmarshal([]byte(douyinPartitionSample), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if resp.StatusCode != 0 {
		t.Fatalf("status_code = %d", resp.StatusCode)
	}

	var wrapper douyinPartitionData
	if err := json.Unmarshal(resp.Data, &wrapper); err != nil {
		t.Fatalf("解析 data 失败: %v", err)
	}
	if len(wrapper.Data) != 3 {
		t.Fatalf("条目数 = %d, want 3", len(wrapper.Data))
	}
	// 真实响应里没有 has_more 字段，缺失时必须当作 false。
	if bool(wrapper.HasMore) {
		t.Fatal("has_more 缺失时应为 false")
	}

	var rooms []Room
	for _, entry := range wrapper.Data {
		if room, ok := douyinMapEntry(entry, "和平精英"); ok {
			rooms = append(rooms, room)
		}
	}
	if len(rooms) != 2 {
		t.Fatalf("有效房间数 = %d, want 2", len(rooms))
	}

	r := rooms[0]
	if r.Platform != Douyin || r.RoomID != "100001" {
		t.Errorf("room[0] 标识错误: %+v", r)
	}
	if r.Nickname != "示例主播" || r.Title != "测试直播间" || r.Area != "和平精英" {
		t.Errorf("room[0] 文案错误: %+v", r)
	}
	// 精确人数优先于 stats 里的「1.2万」文案
	if r.Viewers != 12345 {
		t.Errorf("room[0] viewers = %d, want 12345", r.Viewers)
	}
	if r.Cover != "https://p3.douyinpic.com/cover.jpeg" {
		t.Errorf("room[0] 封面补全错误: %q", r.Cover)
	}
	if !r.Live {
		t.Error("room[0] status=2 应为在播")
	}

	// 没有 user_count 时回落到文案：「3.4亿」
	r = rooms[1]
	if r.Viewers != 340000000 {
		t.Errorf("room[1] viewers = %d, want 340000000", r.Viewers)
	}
	if r.Avatar != "" {
		t.Errorf("room[1] 头像应为空: %q", r.Avatar)
	}
	if !r.Live {
		t.Error("room[1] status 缺失时按在播处理")
	}
}

// 房间信息（reflow）响应样本：结构照抄 2026-11-14 的真实响应
// （id_str / user_count / owner / stats 的位置与真实一致，数值改成虚构）。
// 真实响应里 room.user_count 是精确在线人数，stats 只有「10万+」这种文案，
// room_view_stats.display_value 是累计「看过」人数，不能当在线人数用。
const douyinReflowSample = `{
  "status_code": 0,
  "data": {
    "room": {
      "id": 7707702178277755702,
      "id_str": "7707702178277755702",
      "status": 2,
      "title": "示例直播间",
      "user_count": 10146,
      "cover": {"url_list": ["//p3-webcast-sign.douyinpic.com/cover.jpeg"]},
      "owner": {
        "nickname": "示例主播",
        "avatar_thumb": {"url_list": ["https://p26.douyinpic.com/avatar.jpeg"]},
        "avatar_medium": {"url_list": ["https://p11.douyinpic.com/avatar720.jpeg"]}
      },
      "stats": {"total_user_str": "10万+", "user_count_str": "10146", "total_user": 111035}
    }
  },
  "extra": {"now": 1794591397851}
}`

func TestDouyinReflowParse(t *testing.T) {
	var resp douyinReflowResponse
	if err := json.Unmarshal([]byte(douyinReflowSample), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	room, err := douyinParseReflow(resp)
	if err != nil {
		t.Fatalf("解析房间失败: %v", err)
	}

	got := douyinMapRoom("95062189730", "和平精英", room)
	if got.Platform != Douyin || got.RoomID != "95062189730" {
		t.Errorf("房间标识错误: %+v", got)
	}
	if got.Title != "示例直播间" || got.Nickname != "示例主播" || got.Area != "和平精英" {
		t.Errorf("文案错误: %+v", got)
	}
	// 精确人数优先于 stats 里的累计人数
	if got.Viewers != 10146 {
		t.Errorf("viewers = %d, want 10146", got.Viewers)
	}
	if got.Cover != "https://p3-webcast-sign.douyinpic.com/cover.jpeg" {
		t.Errorf("封面补全错误: %q", got.Cover)
	}
	if got.Avatar != "https://p26.douyinpic.com/avatar.jpeg" {
		t.Errorf("头像错误: %q", got.Avatar)
	}
	if !got.Live {
		t.Error("status=2 应为在播")
	}
}

// 下播房间 reflow 仍然返回完整房间对象（status=4），必须映射成
// Live=false 而不是报错：关注列表要靠它更新昵称/头像/标题。
const douyinReflowOfflineSample = `{
  "status_code": 0,
  "data": {
    "room": {
      "id_str": "7026727478496348968",
      "status": 4,
      "title": "已结束的直播",
      "user_count": 0,
      "owner": {"nickname": "下播主播", "avatar_thumb": {"url_list": []}},
      "stats": {"user_count_str": "0", "total_user_str": "1333", "total_user": 1333}
    }
  }
}`

func TestDouyinReflowOffline(t *testing.T) {
	var resp douyinReflowResponse
	if err := json.Unmarshal([]byte(douyinReflowOfflineSample), &resp); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	room, err := douyinParseReflow(resp)
	if err != nil {
		t.Fatalf("下播房间不应报错: %v", err)
	}
	got := douyinMapRoom("8888", "", room)
	if got.Live {
		t.Error("status=4 应为未开播")
	}
	if got.Nickname != "下播主播" || got.Title != "已结束的直播" {
		t.Errorf("下播房间也应带出文案: %+v", got)
	}
	// user_count 为 0 时回落到 stats 的总看过人数
	if got.Viewers != 1333 {
		t.Errorf("viewers = %d, want 1333", got.Viewers)
	}
	if got.Cover != "" || got.Avatar != "" {
		t.Errorf("没有封面/头像时应为空: %+v", got)
	}
}

// 业务错误必须是 UpstreamError：上层据此区分「没有这个房间」和风控。
func TestDouyinReflowErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		// 实测：把 web_rid 当 room_id 传就是这个结果
		{"房间参数错误", `{"status_code":10011,"data":{"message":"Request params error"}}`},
		{"其他业务错误", `{"status_code":4001,"status_msg":"房间已关闭","data":{}}`},
		{"没有房间对象", `{"status_code":0,"data":{}}`},
		{"data 解析失败", `{"status_code":0,"data":"oops"}`},
	}
	for _, c := range cases {
		var resp douyinReflowResponse
		if err := json.Unmarshal([]byte(c.body), &resp); err != nil {
			t.Fatalf("%s: 外壳解析失败: %v", c.name, err)
		}
		if _, err := douyinParseReflow(resp); err == nil {
			t.Errorf("%s: 应返回错误", c.name)
			continue
		} else if IsBlocked(err) {
			t.Errorf("%s: 业务错误不应归为风控", c.name)
		}
	}
}

// 房间页里的内部 roomId：真实页面是转义 JSON，也要兼容未转义写法。
func TestDouyinExtractRoomID(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"renderData":"{\"roomId\":\"7707702178277755702\"}"}`, "7707702178277755702"},
		{`{"roomId":"7026727478496348968"}`, "7026727478496348968"},
		{`{"room_id":"7707653244083800883"}`, "7707653244083800883"},
		// 不存在的房间：页面有 "roomId":"" 的占位内容，不能当成房间
		{`{"roomInfo":{"roomId":"","web_rid":"123456"}}`, ""},
		{`<html></html>`, ""},
	}
	for _, c := range cases {
		if got := douyinExtractRoomID(c.in); got != c.want {
			t.Errorf("extract(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// 房间页解析：内部 roomId 与二级分区名一起取出来。
//
// 真实页面是转义的 SSR JSON，导航数据里有一堆分区节点，
// 必须按 partition_road_map 里的 id 反查，不能取页面上第一个分区（那是「舞蹈」）。
func TestDouyinParseRoomPage(t *testing.T) {
	html := `{"roomStore":{"roomInfo":{"roomId":"7707702178277755702","web_rid":"218448400833"}},` +
		`\"partition_road_map\":{\"partition\":{\"id_str\":\"1\",\"type\":1,\"title\":\"射击游戏\"},` +
		`\"sub_partition\":{\"partition\":{\"id_str\":\"1010032\",\"type\":1,\"title\":\"\"}}},` +
		`\"nav\":[{\"partition\":{\"id_str\":\"105\",\"type\":4,\"title\":\"舞蹈\"}},` +
		`{\"partition\":{\"id_str\":\"1010032\",\"type\":1,\"title\":\"和平精英\"}}]}`

	ref := douyinParseRoomPage(html)
	if ref.InternalID != "7707702178277755702" {
		t.Errorf("内部 roomId = %q", ref.InternalID)
	}
	if ref.Area != "和平精英" {
		t.Errorf("分区名 = %q, want 和平精英", ref.Area)
	}

	// 非游戏类房间没有 sub_partition，用 partition 本身；名字仍在导航数据里。
	html = `\"partition_road_map\":{\"partition\":{\"id_str\":\"105\",\"type\":4,\"title\":\"舞蹈\"}}` +
		`\"nav\":[{\"partition\":{\"id_str\":\"105\",\"type\":4,\"title\":\"舞蹈\"}}]`
	ref = douyinParseRoomPage(`{"roomId":"7026727478496348968"}` + html)
	if ref.InternalID != "7026727478496348968" || ref.Area != "舞蹈" {
		t.Errorf("非游戏房间解析错误: %+v", ref)
	}

	// 没有分区信息时只要求 roomId，分区留空不报错。
	ref = douyinParseRoomPage(`{"roomId":"7026727478496348968"}`)
	if ref.InternalID != "7026727478496348968" || ref.Area != "" {
		t.Errorf("无分区页面解析错误: %+v", ref)
	}
}

// 搜索输入只有「房间号 / 房间链接」有效；关键词不是房间号。
func TestDouyinNormalizeRoomID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"95062189730", "95062189730"},
		{" 95062189730 ", "95062189730"},
		{"https://live.douyin.com/933572413882", "933572413882"},
		{"https://live.douyin.com/933572413882?enter_from=web", "933572413882"},
		{"http://www.douyin.com/follow/live/123456/", "123456"},
		{"https://live.douyin.com/123?room_id=777&from=x", "777"},
		{"英雄联盟", ""},
		{"123abc", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := douyinNormalizeRoomID(c.in); got != c.want {
			t.Errorf("normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// has_more 在部分版本是 true/false，部分版本是 1/0。
func TestDouyinFlexBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{`"1"`, true},
		{"null", false},
	}
	for _, c := range cases {
		var b douyinFlexBool
		if err := json.Unmarshal([]byte(c.in), &b); err != nil {
			t.Fatalf("unmarshal %s: %v", c.in, err)
		}
		if bool(b) != c.want {
			t.Errorf("%s → %v, want %v", c.in, bool(b), c.want)
		}
	}
}

/* ------------------------------------------------------------ 工具函数 */

func TestDouyinSplitAreaID(t *testing.T) {
	cases := []struct {
		in        string
		ptype     string
		partition string
		wantErr   bool
	}{
		{"1_1010032", "1", "1010032", false},
		{"1_1_1_1010032", "1", "1010032", false},
		{"/category/1_1010032", "1", "1010032", false},
		{" 1_1010032 ", "1", "1010032", false},
		{"1010032", "", "", true},
		{"", "", "", true},
	}
	for _, c := range cases {
		ptype, partition, err := douyinSplitAreaID(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%q err=%v, wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if !c.wantErr && (ptype != c.ptype || partition != c.partition) {
			t.Errorf("%q → (%q,%q), want (%q,%q)", c.in, ptype, partition, c.ptype, c.partition)
		}
	}
}

func TestDouyinParseCount(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"3286", 3286},
		{"1.2万", 12000},
		{"3.4亿", 340000000},
		{"10万+", 100000},
		{"2.5w", 25000},
		{"1.2W", 12000},
		{"1.2万人", 12000},
		{"", 0},
		{"-1", -1},
		{"1.5万", 15000},
		{"未知", 0},
	}
	for _, c := range cases {
		if got := douyinParseCount(c.in); got != c.want {
			t.Errorf("parseCount(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestDouyinMsToken(t *testing.T) {
	token := douyinMsToken()
	if len(token) != douyinTokenLen {
		t.Fatalf("长度 = %d, want %d", len(token), douyinTokenLen)
	}
	for i := 0; i < len(token); i++ {
		c := token[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			t.Fatalf("第 %d 位字符 %q 不在字母数字集合内", i, c)
		}
	}
	if token == douyinMsToken() {
		t.Fatal("两次生成的 msToken 不应该相同")
	}
}

// 内嵌分区表必须能解出分组与分类，且 id 是「类型_分区」格式。
func TestDouyinCategoriesAsset(t *testing.T) {
	p := newDouyin()
	groups, names, err := p.loadCategories()
	if err != nil {
		t.Fatalf("解析分区表失败: %v", err)
	}
	if len(groups) == 0 {
		t.Fatal("分区表为空")
	}
	if groups[0].Name == "" || len(groups[0].Categories) == 0 {
		t.Fatalf("首组为空: %+v", groups[0])
	}
	for _, c := range groups[0].Categories {
		if !strings.Contains(c.ID, "_") {
			t.Fatalf("分区 id %q 不是 类型_分区 格式", c.ID)
		}
		if c.Name == "" {
			t.Fatalf("分区 %q 名称为空", c.ID)
		}
	}
	if names["1_1010032"] == "" {
		t.Fatal("常见分区 1_1010032 没有解出名称")
	}
	// 静态表不会随请求变化：再次解析必须得到同一份结果。
	if again, _, _ := p.loadCategories(); len(again) != len(groups) {
		t.Fatal("分区表被重复解析成了不同结果")
	}
}

// 签名 URL 必须把 a_bogus 放在最后，且签名的 query 就是发出的 query。
func TestDouyinSignedURL(t *testing.T) {
	params := [][2]string{{"keyword", "英雄 联盟"}, {"count", "20"}}
	target := douyinSignedURL("https://example.com/api/", params, "UA")
	if !strings.HasPrefix(target, "https://example.com/api/?") {
		t.Fatalf("URL 前缀错误: %q", target)
	}
	if !strings.Contains(target, "keyword=%E8%8B%B1%E9%9B%84+%E8%81%94%E7%9B%9F") {
		t.Fatalf("query 未被正确转义: %q", target)
	}
	if !strings.Contains(target, "&a_bogus=") {
		t.Fatalf("缺少 a_bogus: %q", target)
	}
}

// encodeHex 避免为测试引入 encoding/hex 之外的依赖。
func encodeHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, v := range b {
		out = append(out, digits[v>>4], digits[v&0xf])
	}
	return string(out)
}
