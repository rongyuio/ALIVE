package video

import (
	"strings"
	"testing"
)

// 回归：B 站的 fMP4 每个分片都重带 init 段，mpv 每几秒报一次
// "Found duplicated MOOV Atom"。实测 60 秒收到 50 条，全是它——真正的
// 故障行（跳分片、缺参考帧）会被淹掉。只留第一条。
func TestDuplicatedMoovLoggedOnce(t *testing.T) {
	p := &Player{}
	const warn = "mov,mp4,m4a,3gp,3g2,mj2: Found duplicated MOOV Atom. Skipped it"
	for range 50 {
		p.apply(event{kind: 2, name: "warn", text: warn})
	}
	if got := len(p.state.Log); got != 1 {
		t.Fatalf("duplicated MOOV 记了 %d 条，期望只留 1 条", got)
	}
	// 别的警告不能被这条规则顺手吞掉。
	p.apply(event{kind: 2, name: "error", text: "hls: skipping 3 segments ahead"})
	if got := len(p.state.Log); got != 2 {
		t.Fatalf("其它日志被吞了，共 %d 条", got)
	}
	if !strings.Contains(p.state.Log[1].Text, "skipping") {
		t.Fatalf("第二条不是 hls 警告：%q", p.state.Log[1].Text)
	}
}

// mpv 日志必须按 Seq 单调递增，上层才能靠它增量转发、不重复落盘。
func TestMPVLogSeqIncrements(t *testing.T) {
	p := &Player{}
	p.apply(event{kind: 2, name: "warn", text: "a"})
	p.apply(event{kind: 2, name: "error", text: "b"})
	if len(p.state.Log) != 2 {
		t.Fatalf("收到 %d 条，期望 2 条", len(p.state.Log))
	}
	if p.state.Log[1].Seq <= p.state.Log[0].Seq {
		t.Fatalf("Seq 没有递增：%d → %d", p.state.Log[0].Seq, p.state.Log[1].Seq)
	}
}

// info 级别不进来：hls / 解复用在正常播放时也在刷，收下来只会淹没故障行。
func TestInfoLevelNotCollected(t *testing.T) {
	p := &Player{}
	p.apply(event{kind: 2, name: "info", text: "something routine"})
	if len(p.state.Log) != 0 {
		t.Fatalf("info 级别被收了：%v", p.state.Log)
	}
}
