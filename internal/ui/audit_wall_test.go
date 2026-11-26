package ui

import (
	"testing"
	"time"
)

// M15 回归：墙上非 active 格的滚轮方向必须与播放页一致——上滚增大。
//
// 旧实现 watchWheelStep 已经返回增量（上滚为负），调用点又写了
// `volume - step`，等于取负两次：上滚变小、下滚变大。
func TestWallTileWheelScrollsUpToLouder(t *testing.T) {
	rooms := watchTestRooms(1)
	app, tt := newWatchMock(t, rooms)
	s := fakeSession(t, app, rooms[0])
	tt.Frame()

	// hover 到格子上，底栏才画出来。
	r, ok := tt.Find(watchTileLabel(rooms[0]))
	if !ok {
		t.Fatal("找不到格子")
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()

	hot, ok := tt.Find("音量 " + rooms[0].Name)
	if !ok {
		t.Fatalf("hover 之后没有音量热区，labels=%v", tt.Texts())
	}
	before := s.volume
	// dy < 0 是上滚。
	tt.Scroll(hot.X+hot.W/2, hot.Y+hot.H/2, 0, -100)
	tt.Frame()
	if s.volume <= before {
		t.Fatalf("上滚之后音量没有增大：%.2f → %.2f（方向反了）", before, s.volume)
	}
	afterUp := s.volume
	tt.Scroll(hot.X+hot.W/2, hot.Y+hot.H/2, 0, 100)
	tt.Frame()
	if s.volume >= afterUp {
		t.Fatalf("下滚之后音量没有减小：%.2f → %.2f", afterUp, s.volume)
	}
}

// M14 回归：给「刚接上的新引擎」推呈现尺寸必须无条件推一次。
//
// 旧代码只对焦点格调 applyPresentForce；墙格重连后新引擎的尺寸还是默认值，
// 而 s.present 记着上一枚引擎推过的意图 → applyPresentTo 的 `want==present`
// 短路 → 那一格按视频原始尺寸（1080p）渲染。
func TestPresentNewEnginePushesEvenWhenUnchanged(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	s := fakeSession(t, app, rooms[0])
	spy := &trackSpy{}
	useTrack(t, app, spy)
	// 把 present 预置成「已经推过」的样子，模拟 dropEngine 之后的状态。
	s.present = app.presentSizeForSession(s)
	before := spy.count()

	app.presentNewEngine(s)

	if spy.count() != before+1 {
		t.Fatalf("新引擎没有收到呈现尺寸：推了 %d 次（期望 +1）", spy.count()-before)
	}
	if last, ok := spy.last(); !ok || last != app.presentSizeForSession(s) {
		t.Fatalf("推的尺寸不对：%+v 期望 %+v", last, app.presentSizeForSession(s))
	}

	// 反证：常规的 applyPresentTo 在同样的前提下必须短路（这就是旧墙格
	// 重连拿不到尺寸的原因）。
	n := spy.count()
	app.applyPresentTo(s)
	if spy.count() != n {
		t.Fatalf("applyPresentTo 应当短路，却又推了一次（%d → %d）", n, spy.count())
	}
}

// L29 回归：焦点格的 stalled 必须每帧重算。
//
// 旧实现把 `s.stalled = wallStalled(...)` 写在 `s == a.focus { continue }`
// 之后，焦点格永远停在历史值——「缓冲中…」该消失的不消失、该报的不报。
func TestWatchTickRecomputesStalledForFocus(t *testing.T) {
	rooms := watchTestRooms(1)
	app, _ := newWatchMock(t, rooms)
	s := fakeSession(t, app, rooms[0])
	// 假设它上一帧是「缓冲中」（历史值 true），此后画面已恢复。
	s.stalled = true
	s.stillSince = time.Now()
	app.setFocus(s)

	app.watchTick(time.Now())

	if s.stalled {
		t.Fatal("焦点格的 stalled 没有重算，停在历史值：胶囊会一直显示「缓冲中…」")
	}
}
