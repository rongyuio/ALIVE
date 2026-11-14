package ui

import (
	"testing"

	"alive/internal/live"
)
// 刷新结束的提示文案要和 baseline 的 handleRefresh 一致：
// 全成功报数、部分失败报两边、全军覆没报失败。色调也要对上——它决定图标。
func TestRefreshSummary(t *testing.T) {
	cases := []struct {
		updated, failed int
		want            string
		wantTone        string
	}{
		{3, 0, "已刷新 3 位主播", toastSuccess},
		{2, 1, "已刷新 2 位，1 位暂时无法获取状态", toastInfo},
		{0, 3, "刷新失败，请稍后重试", toastError},
	}
	for _, c := range cases {
		msg, tone := refreshSummary(c.updated, c.failed)
		if msg != c.want || tone != c.wantTone {
			t.Errorf("updated=%d failed=%d：得到 (%q, %q)，期望 (%q, %q)",
				c.updated, c.failed, msg, tone, c.want, c.wantTone)
		}
	}
}

// 关注面板的排序要和 baseline FollowsList 一致：
// 在播优先 → 同状态内按顶栏平台顺序 → 组内保持原关注顺序。
func TestSortFollowStreamers(t *testing.T) {
	order := []string{"douyu", "huya", "douyin", "bilibili"}
	makeList := func() []Streamer {
		return []Streamer{
			{Platform: "douyu", RoomID: "1"},
			{Platform: "bilibili", RoomID: "2", Live: true},
			{Platform: "huya", RoomID: "3", Live: true},
			{Platform: "bilibili", RoomID: "4"},
			{Platform: "douyu", RoomID: "5", Live: true},
		}
	}
	ids := func(list []Streamer) []string {
		out := make([]string, len(list))
		for i, s := range list {
			out[i] = s.RoomID
		}
		return out
	}

	// 在播：douyu(5) → huya(3) → bilibili(2)；离线：douyu(1) → bilibili(4)
	list := makeList()
	sortFollowStreamers(list, order)
	if got, want := ids(list), []string{"5", "3", "2", "1", "4"}; !equalStrings(got, want) {
		t.Fatalf("按默认平台顺序排序：得到 %v，期望 %v", got, want)
	}

	// 拖一次顶栏 tab，面板里的分组要立刻跟着变
	reordered := []string{"bilibili", "douyu", "huya", "douyin"}
	list = makeList()
	sortFollowStreamers(list, reordered)
	if got, want := ids(list), []string{"2", "5", "3", "4", "1"}; !equalStrings(got, want) {
		t.Fatalf("按调整后的平台顺序排序：得到 %v，期望 %v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 刷新走 fallback（取流接口）时的字段边界：取流不给人气和封面，这两个
// 字段必须保留刷新前的值；昵称 / 标题 / 头像 / 直播状态照写。
func TestApplyStreamInfo(t *testing.T) {
	f := FollowRecord{
		Name: "旧名", Title: "旧标题", AvatarURL: "old-avatar",
		CoverURL: "old-cover", Viewers: 1234, Live: true,
	}
	applyStreamInfo(&f, live.Stream{
		Nickname: "新名", Title: "新标题", Avatar: "new-avatar", Live: false,
	})

	if f.Name != "新名" || f.Title != "新标题" || f.AvatarURL != "new-avatar" {
		t.Errorf("取流给的字段没写回：%+v", f)
	}
	if f.Live {
		t.Errorf("取流说没在播，Live 应为 false")
	}
	// baseline 的 fallback 分支同样不写 viewers，封面来源也不在取流接口里
	if f.CoverURL != "old-cover" || f.Viewers != 1234 {
		t.Errorf("取流不该覆盖封面 (%q) 与人气 (%d)", f.CoverURL, f.Viewers)
	}
}

// 房间信息里上游留空的字段要保留原值，人气为 0 时同样不覆盖——
// 0 在关注列表里既可能是真实 0 也可能是上游没给，按 baseline 的 `||` 语义留着。
func TestApplyRoomInfo(t *testing.T) {
	f := FollowRecord{
		Name: "旧名", Title: "旧标题", Category: "旧分区",
		Viewers: 99, Live: true,
	}
	applyRoomInfo(&f, live.Room{Nickname: "新名", Live: false})

	if f.Name != "新名" {
		t.Errorf("昵称没写回：%q", f.Name)
	}
	if f.Title != "旧标题" || f.Category != "旧分区" || f.Viewers != 99 {
		t.Errorf("上游留空的字段不该被清掉：%+v", f)
	}
	if f.Live {
		t.Errorf("房间信息说没在播，Live 应为 false")
	}
}
