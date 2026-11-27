package ui

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 真实报障的端到端复现：**收起某一间之后，回首页点它自己的封面**。
//
// 这一刻的正确行为是：只有卡片那一路在动（飞行块从卡片长成舞台），岛不动。
// 错的版本里岛也带着 surfaceMatch 身份，于是舞台从**岛**的矩形起飞，飞行块
// 和岛同时动——用户看到「点封面，岛也跟着动画」。
//
// 这里跑的是真的 App.Shell（不是拼一个最小视图），所以量到的就是用户看到的。
func TestCollapsedRoomCardClickDoesNotFlyTheIsland(t *testing.T) {
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题", Live: true}
	key := watchKey(room)

	a := newApp()
	a.Platform = room.Platform
	a.Streamers = []Streamer{room}
	a.focus = &roomSession{key: key, room: room, loading: true}
	a.Router = ui.NewRouter(routeHome)

	tt := ui.NewTester(a.Shell, 1280, 800)
	tt.Frame()
	// 窗口早已稳定：把观测点推到 400ms 窗口之外。
	//
	// 这里用 `tt.Advance` 就够了——resizedAt 和 matchable() 用的是同一个帧钟
	// （见 surface_motion.go 的 noteWindowSize）。以前 matchable 按挂钟判，
	// Tester 推不动它，只能写 `time.Now().Add(-time.Hour)` 手工摆平；那正是
	// 「构建路径读挂钟」这条债的证据。
	tt.Advance(600 * time.Millisecond)
	tt.Frame()

	// 前提：首页上这一间有卡片，于是画面归属是 pictureCard。
	if got := a.pictureNow(key); got != pictureCard {
		t.Fatalf("前提不成立：画面归属是 %v，不是卡片（卡片矩形没记下来？）", got)
	}
	// 岛不该持有身份——这一条是修复的核心。
	if got := a.islandMatchKey(true); got != "" {
		t.Fatalf("卡片在场时岛仍持有身份 %q：点封面会让舞台从岛起飞", got)
	}

	islandBefore, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("找不到岛")
	}
	t.Logf("点之前：岛 %+v", islandBefore)

	// 点卡片封面进播放页。
	a.openRoom(room)
	tt.Frame()

	islandAfter, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("点之后找不到岛")
	}
	t.Logf("点之后第一帧：岛 %+v", islandAfter)

	// 岛不该因为这次点击改变尺寸。留 1px 容差给取整。
	if d := islandAfter.W - islandBefore.W; d > 1 || d < -1 {
		t.Fatalf("点封面之后岛宽从 %.0f 变成 %.0f：岛的动画被一起触发了",
			islandBefore.W, islandAfter.W)
	}

	// 真正的判据：岛那一块**像素**在点击前后必须一致。
	//
	// transitions.md 的 Fly 写得很清楚：「The other, flying too, goes with it,
	// fading out under it」——舞台带着同一个身份出现时，岛就是那个 other，
	// 它会跟着飞、并淡出。用户说的「岛的动画也触发了」就是这个：屏幕正中
	// 那颗岛一边朝舞台飞一边淡掉，同时卡片那一路也在飞。
	//
	// 量矩形量不出来：飞的是 MyGo 画的副本，元素的布局矩形不动。所以直接
	// 比那一块像素。
	before := islandPixels(tt.Image(), islandBefore)
	tt.Frame()
	tt.Frame()
	after := islandPixels(tt.Image(), islandBefore)
	if before == nil || after == nil {
		t.Fatal("取不到岛那一块像素")
	}
	if !samePixels(before, after) {
		t.Fatalf("点封面之后岛那一块像素变了：岛跟着飞了（%d 字节 vs %d 字节）",
			len(before), len(after))
	}

	// 飞行块该在飞（卡片那一路还在）。
	if a.surfaceFlight == nil {
		t.Error("卡片 → 播放页的飞行块没排上")
	} else {
		t.Logf("飞行块：from=%v pixels=%v", a.surfaceFlight.from, a.surfaceFlight.pixels)
	}
}

// islandPixels 抠出岛那块矩形里的像素。
func islandPixels(img *image.RGBA, r ui.Rect) []byte {
	if img == nil {
		return nil
	}
	x, y := int(r.X), int(r.Y)
	w, h := int(r.W), int(r.H)
	if x < 0 || y < 0 || x+w > img.Bounds().Dx() || y+h > img.Bounds().Dy() {
		return nil
	}
	out := make([]byte, 0, w*h*4)
	for row := 0; row < h; row++ {
		off := (y+row)*img.Stride + x*4
		out = append(out, img.Pix[off:off+w*4]...)
	}
	return out
}

func samePixels(a, b []byte) bool { return bytes.Equal(a, b) }
