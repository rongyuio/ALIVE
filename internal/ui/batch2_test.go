package ui

import (
	"os"
	"strings"
	"testing"
)

// 关注页在线点的元素身份必须带房间号。
//
// 脉冲环的 Keyframes 绑在元素身份上：身份重合的两个主播共用一份动画状态
// （一个亮一个不亮、或者环停在缩小的半路）。原来的键是 平台|昵称|分区，
// 同一平台同昵称不同房间的两条会撞在一起。
func TestFollowDotKeyIncludesRoomID(t *testing.T) {
	a := Streamer{Platform: "bilibili", RoomID: "1", Name: "甲", Category: "网游"}
	b := Streamer{Platform: "bilibili", RoomID: "2", Name: "甲", Category: "网游"}

	if followDotKey(a) == followDotKey(b) {
		t.Fatalf("同昵称不同房间的两条拿到了同一个身份 %q", followDotKey(a))
	}
	// 同一间房必须稳定（否则每帧都是新身份，动画每帧重置）。
	first, second := followDotKey(a), followDotKey(a)
	if first != second {
		t.Error("同一条记录两次算出的身份不同")
	}
	// 房间号在键里可读（不是只靠别的字段凑巧区分开）。
	if !strings.Contains(followDotKey(a), "bilibili|1|") {
		t.Errorf("身份里没有房间号：%q", followDotKey(a))
	}
}

// 转圈收尾必须往前走完这一圈，不能倒着退回 0。
//
// 停下的那一刻 phase 落在 (0,360) 之间。目标写 0 就是倒着转掉最多一整圈；
// 写 360 则是继续往前补满，落点还是正位（360° 与 0° 屏幕朝向相同）。
func TestFollowsSpinStopTargetGoesForward(t *testing.T) {
	if followsSpinStopTarget != 360 {
		t.Fatalf("收尾目标改成了 %v：不是 0 也不是 360 就得重新想一遍方向",
			followsSpinStopTarget)
	}
	for _, phase := range []float32{0, 1, 90, 180, 270, 359.9} {
		if followsSpinStopTarget < phase {
			t.Errorf("phase=%.1f 时目标 %v 在它后面：会倒着转",
				phase, followsSpinStopTarget)
		}
		if d := followsSpinStopTarget - phase; d > 360 {
			t.Errorf("phase=%.1f 时要转 %.1f°，超过一整圈", phase, d)
		}
	}
}

// 切分区要推迟到本帧构建之后。
//
// selectFollowsSection 改的是 a.FollowsSection，而这一帧后面的内容区正是按它
// 建的。内联改会让滑块（上面按旧值算的选中位）和内容错开一帧：点一下，滑块
// 在新位置、列表还是旧的。
func TestFollowsSectionTabDefersSelection(t *testing.T) {
	src, err := os.ReadFile("follows_page.go")
	if err != nil {
		t.Fatalf("读不到 follows_page.go：%v", err)
	}
	text := string(src)
	i := strings.Index(text, "func (a *App) followsSectionTab(")
	if i < 0 {
		t.Fatal("找不到 followsSectionTab")
	}
	body := text[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if strings.Contains(body, "if tab.Clicked() {") {
		t.Error("followsSectionTab 还在用内联 Clicked：改 a.FollowsSection 会和本帧内容错开")
	}
	if !strings.Contains(body, "tab.OnClick(func() { a.selectFollowsSection(section) })") {
		t.Error("followsSectionTab 没有把选中推迟到本帧构建之后")
	}
}
