package ui

import (
	"image"
	"io"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// oneFrameDecoder 只交一帧就 EOF：造一个真实的 *ui.VideoPlayer 用于测试，
// 不需要网络也不需要真窗口。
type oneFrameDecoder struct{ n int }

func (d *oneFrameDecoder) Next() (ui.VideoFrame, error) {
	if d.n > 0 {
		return ui.VideoFrame{}, io.EOF
	}
	d.n++
	return ui.VideoFrame{
		Image:    image.NewRGBA(image.Rect(0, 0, 16, 9)),
		Duration: 100 * time.Millisecond,
	}, nil
}

func (d *oneFrameDecoder) Rewind() error { d.n = 0; return nil }

func testPlayer(t *testing.T) *ui.VideoPlayer {
	t.Helper()
	p, err := ui.NewVideoPlayer(&oneFrameDecoder{})
	if err != nil {
		t.Fatalf("造播放器失败：%v", err)
	}
	t.Cleanup(p.Close)
	return p
}

// 热切换的在途闸门（refreshing）必须在轮次作废时放下。
//
// 闸门一端在 scheduleSwap 里立起来，另一端只在对得上号的回包里放下
// （runHotSwap 的 fail 先比 seq 再清 refreshing）。这一轮已经作废时，
// 那个回包走「过期」分支直接 return——闸门就永远立着。此后 scheduleSwap
// 每次都在第一行退出：地址续期和断流重连一起失效，画面停在原地，只有
// 用户手点重试才解得开。
//
// 触发很普通：热切换在飞的时候焦点被换掉（墙上点另一格、列表开别间、
// Esc 关焦点都走 setFocus → cancelSession）。
func TestCancelSessionLowersSwapGate(t *testing.T) {
	m := &playerMedia{}

	// 模拟 scheduleSwap 立闸门。
	m.refreshing = true
	m.lastSwapAt = time.Now()

	before := m.seq
	m.cancelSession()

	if m.refreshing {
		t.Fatal("cancelSession 之后闸门还立着：这一路的地址续期和断流重连永久失效")
	}
	if m.seq <= before {
		t.Fatalf("seq 没有推进：%d → %d（在途回包会认不出自己已过期）", before, m.seq)
	}
	if m.ctx != nil {
		t.Error("ctx 没有清")
	}
}

// 从墙上接管一路时，上一轮的换地址进度不能带过来。
//
// adoptLiveSession 不经过 stopPlayback，残留的 swapFailures 会让新这一路
// 第一次热切换失败就凑满阈值走「整轮重开」——用户看到 loading 覆盖层 +
// 断音，而不是无缝续期。
func TestAdoptLiveSessionResetsSwapProgress(t *testing.T) {
	a := newApp()
	a.media.swapFailures = swapMaxFailures - 1
	a.media.lastSwapAt = time.Now()

	s := &roomSession{key: "p|1", room: Streamer{Platform: "p", RoomID: "1", Name: "房"}}
	s.attachPlayer(testPlayer(t))
	a.adoptLiveSession(s)

	if a.media.swapFailures != 0 {
		t.Errorf("接管之后 swapFailures 是 %d，应为 0：新这一路第一次失败就会整轮重开",
			a.media.swapFailures)
	}
	if !a.media.lastSwapAt.IsZero() {
		t.Errorf("接管之后 lastSwapAt 还没清：新这一路会被旧的退避窗口挡住")
	}
}
