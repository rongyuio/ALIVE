package ui

import (
	"testing"
	"time"
)

// 焦点格的「缓冲中…」不能永久挂着。
//
// s.stalled 读的是 s.stillSince，而那颗字段原来只由 watchDeadReason 推进——
// 它跑在非焦点路径上。于是焦点格一旦出图 1.5 秒，s.stalled 永久为真，
// 墙格上一直挂着「缓冲中…」，即使画面在正常走。
//
// 场景：在墙上单击一格（焦点留在墙上，那一格继续画），1.5 秒后那一格
// 就永久顶着一行「缓冲中…」。
func TestFocusedTileStallClearsWhenPictureMoves(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, playing: true}
	s.attachPlayer(testPlayer(t))
	a.watch.init()
	a.watch.sessions[s.key] = s
	a.focus = s // 这一格是焦点

	now := time.Now()
	var lastPos time.Duration
	var since time.Time
	observeWallStall(s.player, &lastPos, &since, now)
	if since.IsZero() {
		t.Fatal("首次观测没有建起点")
	}
	// 时间轴在动（位置从 0 推到 2s，模拟画面在走）：不算缓冲。
	later := now.Add(5 * time.Second)
	observeWallStallAt(2*time.Second, &lastPos, &since, later)
	if wallStalled(true, true, since, later) {
		t.Errorf("画面在走却判成缓冲中（lastPos=%v since=%v）", lastPos, since)
	}

	// 时间轴不动了：再过 5 秒应该判成缓冲中。
	settled := later.Add(5 * time.Second)
	if !wallStalled(true, true, since, settled) {
		t.Error("时间轴停住 5 秒却没判成缓冲中")
	}
}

// 焦点格 tick 之后，stalled 必须反映当前画面状态，不能是历史值。
func TestFocusTileStalledUpdatesAcrossTicks(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, playing: true}
	s.attachPlayer(testPlayer(t))
	a.watch.init()
	a.watch.sessions[s.key] = s
	a.focus = s

	base := time.Now()
	// 第一拍：建观测点，不是缓冲。
	a.watchTick(base)
	if s.stalled {
		t.Fatal("刚出图就判成缓冲中")
	}
	// 很久以后同一帧：时间轴没动（Tester 的播放器是静态图），应判成缓冲。
	a.watchTick(base.Add(5 * time.Second))
	if !s.stalled {
		t.Fatal("时间轴 5 秒没动，焦点格却没判成缓冲中")
	}
	// 但焦点会话本身不该被墙层动过（重连归媒体层）。
	if s.err != "" {
		t.Errorf("墙层对焦点会话动手了：err=%q", s.err)
	}
}

// observeWallStallAt 是 observeWallStall 的"时间轴已推进到 pos"版本：
// 真播放器的 Position 由解码器驱动，测试里直接给值。
func observeWallStallAt(pos time.Duration, lastPos *time.Duration, stillSince *time.Time, now time.Time) {
	if pos != *lastPos {
		*lastPos, *stillSince = pos, now
	}
}
