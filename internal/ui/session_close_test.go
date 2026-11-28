package ui

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
)

// 焦点那一间被销毁时，媒体层那一轮必须一起作废。
//
// 只把 `a.focus` 置 nil 是不够的：`m.ctx` 还活着，后台取流的 goroutine 不会
// 被取消；它回来时 `applyMedia` 的代次检查（`sess.expired() || seq 不等`）
// 也认为「这仍是当前一轮」——于是一份属于已删房间的结果照常写进 a.media
// 的字段：地址、音量、呈现尺寸可能被一个不存在的房间改掉。
//
// 触发：在墙上把正在出声的那一格删掉；或者关焦点时那一路还在取流。
func TestCloseFocusedSessionCancelsMediaRound(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "bilibili", RoomID: "42", Name: "甲"}
	s := &roomSession{key: watchKey(room), room: room, log: wallLog(watchKey(room), "")}
	a.watch.init()
	a.watch.sessions[s.key] = s
	a.focus = s

	sess := a.beginSession("t")
	if a.media.ctx == nil {
		t.Fatal("前提不成立：beginSession 之后应该有一轮")
	}
	seqBefore := a.media.seq

	a.closeSession(s, "removed")

	if a.media.ctx != nil {
		t.Error("会话已销毁，media.ctx 仍然非 nil：后台取流不会被取消")
	}
	if a.media.seq == seqBefore {
		t.Errorf("seq 没有推进（%d）：过期结果仍会被当成当前一轮", seqBefore)
	}
	// 过期结果必须被挡住。
	ran := false
	origRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	defer func() { runOnMain = origRun }()
	a.applyMedia(sess, func() { ran = true }, nil)
	if ran {
		t.Error("会话已删除，这一轮的取流结果仍然通过了代次检查")
	}
}

// 关掉的不是焦点时，不要动媒体层那一轮——别人的画面还在跑。
func TestCloseNonFocusedSessionKeepsMediaRound(t *testing.T) {
	a := newApp()
	focus := Streamer{Platform: "bilibili", RoomID: "1", Name: "焦点"}
	other := Streamer{Platform: "bilibili", RoomID: "2", Name: "别的"}
	fs := &roomSession{key: watchKey(focus), room: focus, log: wallLog(watchKey(focus), "")}
	os2 := &roomSession{key: watchKey(other), room: other, log: wallLog(watchKey(other), "")}
	a.watch.init()
	a.watch.sessions[fs.key] = fs
	a.watch.sessions[os2.key] = os2
	a.focus = fs

	a.beginSession("t")
	ctxBefore, seqBefore := a.media.ctx, a.media.seq

	a.closeSession(os2, "removed")

	if a.media.ctx != ctxBefore || a.media.seq != seqBefore {
		t.Error("关掉一个非焦点会话，把焦点那一轮的取流也撤了")
	}
	if a.focus != fs {
		t.Error("焦点被别的会话的销毁带走了")
	}
}

// 时间轴还在走的时候不该判成缓冲——用帧钟推，别用 sleep。
var _ = time.Second
