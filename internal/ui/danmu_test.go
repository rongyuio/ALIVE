package ui

import (
	"testing"
	"time"
)

// TestDanmuAdvance 钉住弹幕时钟的四种走法：正常前进、暂停、缓冲宽限、卡死冻结。
//
// 弹幕时钟只跟"画面有没有真的前进"走。这条判据错了，用户看到的就是
// "画面停了弹幕还在飘"——所有分支都在这里。
func TestDanmuAdvance(t *testing.T) {
	const (
		tick = danmuTick
		pos  = 5 * time.Second
	)
	cases := []struct {
		name      string
		dt        time.Duration
		hasPlayer bool
		playing   bool
		pos       time.Duration
		lastPos   time.Duration
		still     time.Duration
		wantAdd   time.Duration
		wantStill time.Duration
	}{
		{
			name:      "没有播放器（还在取流）：按墙钟走",
			dt:        tick,
			hasPlayer: false,
			playing:   true,
			wantAdd:   tick,
			wantStill: 0,
		},
		{
			name:      "用户按了暂停：当场冻住，不用等宽限",
			dt:        tick,
			hasPlayer: true,
			playing:   false,
			pos:       pos,
			lastPos:   pos,
			still:     100 * time.Millisecond,
			wantAdd:   0,
			wantStill: 0,
		},
		{
			name:      "位置没变但还在宽限内：当作采样间隔，照常走",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     200 * time.Millisecond,
			wantAdd:   tick,
			wantStill: 200*time.Millisecond + tick,
		},
		{
			name:      "正好走到宽限线：仍然算前进",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     danmuStallGrace - tick,
			wantAdd:   tick,
			wantStill: danmuStallGrace,
		},
		{
			name:      "刚超过宽限：这一拍就停",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     danmuStallGrace,
			wantAdd:   0,
			wantStill: danmuStallGrace + tick,
		},
		{
			name:      "卡死很久：继续累计、继续冻结",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     2 * time.Second,
			wantAdd:   0,
			wantStill: 2*time.Second + tick,
		},
		{
			name:      "位置前跳（seek / 换路）：只更新基准，不补时长",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       9 * time.Second,
			lastPos:   pos,
			still:     200 * time.Millisecond,
			wantAdd:   tick,
			wantStill: 0,
		},
		{
			name:      "位置后退：同样只更新基准",
			dt:        tick,
			hasPlayer: true,
			playing:   true,
			pos:       3 * time.Second,
			lastPos:   pos,
			still:     200 * time.Millisecond,
			wantAdd:   tick,
			wantStill: 0,
		},
		{
			name:      "dt 为零：时钟不动，宽限也不变",
			dt:        0,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     100 * time.Millisecond,
			wantAdd:   0,
			wantStill: 100 * time.Millisecond,
		},
		{
			name:      "dt 为负（时钟回拨）：忽略",
			dt:        -time.Millisecond,
			hasPlayer: true,
			playing:   true,
			pos:       pos,
			lastPos:   pos,
			still:     100 * time.Millisecond,
			wantAdd:   0,
			wantStill: 100 * time.Millisecond,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			add, still := danmuAdvance(c.dt, c.hasPlayer, c.playing, c.pos, c.lastPos, c.still)
			if add != c.wantAdd || still != c.wantStill {
				t.Errorf("danmuAdvance(dt=%v hasPlayer=%v playing=%v pos=%v last=%v still=%v) = (%v, %v), want (%v, %v)",
					c.dt, c.hasPlayer, c.playing, c.pos, c.lastPos, c.still,
					add, still, c.wantAdd, c.wantStill)
			}
		})
	}
}

// TestDanmuAdvanceNeverGoesBackwards 连续推进时弹幕时间只增不减：
// 缓冲期按墙钟走，卡死后停住，位置一动又继续——但从不倒退。
func TestDanmuAdvanceNeverGoesBackwards(t *testing.T) {
	var (
		clock   time.Duration
		still   time.Duration
		lastPos = time.Duration(0)
		pos     = time.Duration(0)
	)
	// 画面停了 1 秒（每拍位置不变），然后位置跳一下继续停。
	for i := 0; i < 60; i++ {
		if i == 30 {
			pos = 10 * time.Second // 位置前跳
		}
		add, next := danmuAdvance(danmuTick, true, true, pos, lastPos, still)
		lastPos, still = pos, next
		if add < 0 {
			t.Fatalf("第 %d 拍推进了负时长 %v", i, add)
		}
		clock += add
	}
	// 前 30 拍：宽限 250ms 内按墙钟走（约 8 拍），之后冻结；
	// 位置一跳之后又走 8 拍左右再冻结。总推进必须落在两段宽限之间。
	if clock < danmuStallGrace || clock > 2*danmuStallGrace {
		t.Errorf("总推进 %v，应落在 %v 到 %v 之间", clock, danmuStallGrace, 2*danmuStallGrace)
	}
}
