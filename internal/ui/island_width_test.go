package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 岛展开那几百毫秒里，内容必须跟着岛沿一起长，不能提前按目标宽度摆好。
//
// 按目标宽度排的后果：X 键落在岛外，被 body 的 ClipInset 整颗裁掉
// （G2 实测 t=20ms 时 X 右沿露出 -160px），收缩时右侧空出一大截。
func TestIslandContentFollowsAnimatedWidth(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "1", Name: "甲主播", Title: "一个挺长的直播间标题"}

	tt := ui.NewTester(a.Island, 1280, 800)
	// 先建几帧时钟态：那时没有焦点，岛是最小宽度。
	for i := 0; i < 3; i++ {
		tt.Frame()
		tt.Advance(100 * time.Millisecond)
	}
	clock, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到岛")
	}
	t.Logf("时钟态岛宽 %.1f", clock.W)

	// 现在给它焦点：岛要从时钟宽度长到主播宽度。
	a.focus = &roomSession{key: watchKey(room), room: room, loading: true}

	var (
		worst    float32
		seen     bool
		minWidth float32
	)
	for i := 0; i < 24; i++ {
		tt.Frame()
		island, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("第 %d 步找不到岛", i)
		}
		if !seen || island.W < minWidth {
			minWidth = island.W
		}
		// 找右侧那颗 X（三种文案随表面变）
		var x ui.Rect
		var found bool
		for _, label := range []string{"退出直播间", "关闭直播间", "取消观测发声"} {
			if r, ok := tt.Find(label); ok {
				x, found = r, true
				break
			}
		}
		if found {
			// 岛右沿减去 X 右沿：负数说明 X 露到岛外面了。
			slack := (island.X + island.W) - (x.X + x.W)
			t.Logf("第 %2d 帧 岛宽 %6.1f 右沿 %6.1f；X 右沿 %6.1f；露出 %6.1f",
				i, island.W, island.X+island.W, x.X+x.W, slack)
			if !seen || slack < worst {
				worst = slack
			}
			seen = true
		}
		tt.Advance(25 * time.Millisecond)
	}
	if !seen {
		t.Fatal("整个展开过程中一次都没找到 X 键")
	}
	if minWidth >= clock.W {
		t.Logf("提示：展开过程中岛宽没有从时钟宽度起步（最小 %.1f，时钟态 %.1f）", minWidth, clock.W)
	}
	if worst < -1 {
		t.Errorf("展开过程中 X 键露出岛外 %.1f px：内容按目标宽度排，被裁掉了", -worst)
	}
}
