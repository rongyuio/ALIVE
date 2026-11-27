package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestFollowRowIDIsStableIdentity 行的身份由平台 + 房间 + 昵称组成：
// 同样的三者必须给出同一个 id，任一不同就不同。
func TestFollowRowIDIsStableIdentity(t *testing.T) {
	base := Streamer{Platform: "bilibili", RoomID: "12345", Name: "阿"}
	cases := []struct {
		name string
		s    Streamer
		diff bool
	}{
		{"同一身份", Streamer{Platform: "bilibili", RoomID: "12345", Name: "阿"}, false},
		{"换了平台", Streamer{Platform: "douyu", RoomID: "12345", Name: "阿"}, true},
		{"换了房间", Streamer{Platform: "bilibili", RoomID: "54321", Name: "阿"}, true},
		{"换了昵称", Streamer{Platform: "bilibili", RoomID: "12345", Name: "阿二"}, true},
	}
	id := followRowID(base)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := followRowID(c.s)
			if (got != id) != c.diff {
				t.Errorf("followRowID(%+v) = %q，和基准 %q 的差异应为 %v", c.s, got, id, c.diff)
			}
		})
	}
}

// TestFollowRowKey 序号换 key：0 号原样，之后依次加 #1、#2。
func TestFollowRowKey(t *testing.T) {
	const id = "bilibili|12345|阿"
	cases := []struct {
		n    int
		want string
	}{
		{0, id},
		{1, id + "#1"},
		{2, id + "#2"},
		{17, id + "#17"},
	}
	for _, c := range cases {
		if got := followRowKey(id, c.n); got != c.want {
			t.Errorf("followRowKey(%q, %d) = %q, want %q", id, c.n, got, c.want)
		}
	}
}

// TestFollowRowKeysDoNotCollide 同一份列表里"同名同房间的重复项"必须拿到
// 各自的 key，而不同身份之间不能撞。这就是 followsGrid 建 key 的那段循环。
//
// 撞了会怎样：GridView 把两行当成同一行，Tester 直接 panic，生产里底色动画
// 和右键菜单会落到另一行上。
func TestFollowRowKeysDoNotCollide(t *testing.T) {
	list := []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "阿"},
		{Platform: "bilibili", RoomID: "1", Name: "阿"}, // 重复项（Mock / 旧记录）
		{Platform: "bilibili", RoomID: "1", Name: "阿"}, // 再来一条
		{Platform: "bilibili", RoomID: "2", Name: "阿"}, // 同平台同昵称、不同房间
		{Platform: "douyu", RoomID: "1", Name: "阿"},    // 同房间号、不同平台
		{Platform: "bilibili", RoomID: "1", Name: "阿二"},
	}
	seen := make(map[string]int, len(list))
	keys := make([]string, len(list))
	owners := make(map[string]int, len(list))
	for i, s := range list {
		id := followRowID(s)
		keys[i] = followRowKey(id, seen[id])
		seen[id]++
		if prev, dup := owners[keys[i]]; dup {
			t.Fatalf("第 %d 行和第 %d 行拿到同一个 key %q", i, prev, keys[i])
		}
		owners[keys[i]] = i
	}
	// 0 号就是原 key，重复项才带编号。
	if keys[0] != followRowID(list[0]) {
		t.Errorf("第一行的 key = %q，应为原身份 %q", keys[0], followRowID(list[0]))
	}
	if keys[1] != followRowID(list[1])+"#1" {
		t.Errorf("重复项的 key = %q，应带 #1 后缀", keys[1])
	}
}

// TestFollowsGridRendersDuplicateRows 是上面那条规则的视图侧证据：
// 列表里有两条完全一样的关注记录时，关注页必须把两行都画出来，
// 而不是在 GridView 里撞 Key。
func TestFollowsGridRendersDuplicateRows(t *testing.T) {
	list := []Streamer{
		{Platform: "bilibili", RoomID: "1", Name: "同一个主播"},
		{Platform: "bilibili", RoomID: "1", Name: "同一个主播"},
		{Platform: "bilibili", RoomID: "2", Name: "另一个主播"},
	}
	a := &App{}
	view := func(c *ui.Context) {
		ui.Column(c).Grow(1).Children(func() {
			a.followsGrid(c, list)
		})
	}
	tt := ui.NewTester(view, 900, 400)
	if !tt.HasText("同一个主播") {
		t.Errorf("没画出来重复的那一行，可见文本：%q", tt.Texts())
	}
	if !tt.HasText("另一个主播") {
		t.Errorf("没画出来另一行，可见文本：%q", tt.Texts())
	}
}
