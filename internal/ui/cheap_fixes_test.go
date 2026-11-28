package ui

import (
	"encoding/json"
	"testing"
)

// selectCategory 必须校验下标。
//
// selectGroup 有这条，这里原来没有：越界会把 CategoryIndex 写成一个不存在的
// 值，之后 currentCategory() 兜底返回 cats[0]，可二级的滑块按 box.at(越界)
// 拿到 (0,0)——宽 0、透明，整行没有选中态。
func TestSelectCategoryRejectsOutOfRange(t *testing.T) {
	a := newApp()
	a.Groups = []CategoryGroup{{
		Name:        "网游",
		Categories:  []string{"英雄联盟", "DOTA2"},
		CategoryIDs: []string{"1", "2"},
	}}
	a.GroupIndex, a.CategoryIndex = 0, 0

	a.selectCategory(9)
	if a.CategoryIndex != 0 {
		t.Fatalf("越界下标被写进去了：CategoryIndex = %d", a.CategoryIndex)
	}
	a.selectCategory(-1)
	if a.CategoryIndex != 0 {
		t.Fatalf("负下标被写进去了：CategoryIndex = %d", a.CategoryIndex)
	}
	// 合法下标照常生效。
	a.selectCategory(1)
	if a.CategoryIndex != 1 {
		t.Fatalf("合法下标没生效：%d", a.CategoryIndex)
	}
}

// 快照里塞的必须是拷贝，不是活 map / 活切片。
//
// 以前直接把 a.roomChoices / a.PlatformOrder 放进去，靠「同一函数里同步
// marshal」才没炸——谁把 marshal 挪进 goroutine 就是数据竞争。
func TestStateSnapshotCopiesLiveCollections(t *testing.T) {
	a := newApp()
	a.PlatformOrder = []string{"bilibili", "douyu"}
	a.roomChoices = map[string]roomChoice{"bilibili|1": {}}

	a.markDirty()
	data, _, ok := a.stateSnapshot()
	if !ok {
		t.Fatal("拍不出快照")
	}

	// 拍完之后改活值：快照里必须是拍那一刻的内容。
	a.PlatformOrder[0] = "huya"
	a.roomChoices["bilibili|2"] = roomChoice{}

	var got struct {
		PlatformOrder []string              `json:"platformOrder"`
		RoomChoices   map[string]roomChoice `json:"roomChoices"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("快照解不开：%v", err)
	}
	if len(got.PlatformOrder) != 2 || got.PlatformOrder[0] != "bilibili" {
		t.Errorf("PlatformOrder 是活切片：快照里变成了 %v", got.PlatformOrder)
	}
	if _, extra := got.RoomChoices["bilibili|2"]; extra {
		t.Error("RoomChoices 是活 map：拍完之后加进去的键出现在了快照里")
	}
}
