package ui

import "testing"

// 焦点夹具。
//
// 测试里要造"正在看这一间"的初值时，走生产同一条路：sessionFor 建会话、
// setFocus 设焦点。不要直接改字段——焦点只有一个写者，测试也守同一条规则，
// 否则测的是夹具不是代码。

// focusForTest 把焦点设到这一间，不取流、不起引擎。
func focusForTest(a *App, room Streamer) *roomSession {
	s := a.sessionFor(watchKey(room), room)
	a.setFocus(s)
	return s
}

// focusSessionForTest 把一个已经造好的会话装进表里并设为焦点。
func focusSessionForTest(a *App, s *roomSession) *roomSession {
	a.watch.init()
	if s.key != "" {
		a.watch.sessions[s.key] = s
	}
	a.setFocus(s)
	return s
}

// focusWithPlayer 给焦点装一个纯 Go 播放器，模拟"这一间正在出声"。
func focusWithPlayer(t *testing.T, a *App, room Streamer) *roomSession {
	t.Helper()
	s := focusForTest(a, room)
	p := gifPlayer(t)
	p.SetVolume(float64(s.volume))
	s.player = p
	s.track = &trackSpy{}
	s.firstFrame = true
	return s
}
