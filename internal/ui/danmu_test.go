package ui

import (
	"testing"
	"time"

	"alive/internal/live"
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

// TestDanmuImageSpansTracks 钉住「图片弹幕和文字弹幕不重叠」这条。
//
// 缺陷的成因是几何的：轨道高度曾经是常量 30px（按默认字号 20 算死），而纯图
// 弹幕的高度是字号 × danmuStickerScale = 20 × 2.8 = 56px。一张 56px 的图排在
// 30px 的轨道里，上下各溢出 13px，正好压住相邻轨道上那行字。
//
// 所以断言不看「分到了第几轨」，而是看**纵向区间不相交**：这才是用户看到的
// 那件事。图片占几条轨道由 danmuLaneSpan 算，分配器必须把连续几条一起占掉。
func TestDanmuImageSpansTracks(t *testing.T) {
	const font = danmuFontSize // 20
	lane := danmuLaneH(font)
	img := danmuLine{Emotes: []live.Emote{{URL: "u", Width: 1, Height: 1}}}
	text := danmuLine{Text: "这是一条文字弹幕"}

	imgSpan := danmuLaneSpan(img, font)
	textSpan := danmuLaneSpan(text, font)
	if textSpan != 1 {
		t.Errorf("文字弹幕 span = %d，应当只占 1 条轨道", textSpan)
	}
	// 图片高 56、轨道 30，至少要占 2 条才装得下。
	if imgSpan < 2 {
		t.Fatalf("纯图弹幕 span = %d，56px 的图排在 %vpx 的轨道里会压到相邻轨道", imgSpan, lane)
	}
	if got := imgSpan * int(lane); float32(got) < danmuHeightEstimate(img, font) {
		t.Errorf("图片占 %d 条轨道 = %dpx，装不下 %vpx", imgSpan, got, danmuHeightEstimate(img, font))
	}

	// 轨道高度必须跟着字号走，否则调大字号之后文字自己也会溢出。
	for _, f := range []float32{danmuFontSize, 32, 48} {
		if h := danmuLaneH(f); h < f {
			t.Errorf("字号 %v 的轨道高度 %v 比一行字还矮", f, h)
		}
	}
}

// TestDanmuImageTakesConsecutiveTracks 钉住「占多条轨道时整段一起占」：
// 图片占了 track 和 track+1，下一条弹幕就不能再被分到 track+1。
func TestDanmuImageTakesConsecutiveTracks(t *testing.T) {
	const font = danmuFontSize
	a := &App{DanmuFont: font, DanmuDur: danmuDuration}
	a.danmuTracks = 8
	a.danmuStageW = 1200

	img := danmuLine{Emotes: []live.Emote{{URL: "u", Width: 100, Height: 100}}}
	text := danmuLine{Text: "字"}
	span := danmuLaneSpan(img, font)
	now := time.Now()

	first := a.takeDanmuTrack(now, danmuWidthEstimate(img, font), span)
	// 同一瞬间再来一条文字弹幕：它不能落在 first 占用的任何一条轨道上。
	second := a.takeDanmuTrack(now, danmuWidthEstimate(text, font), 1)
	if second >= first && second < first+span {
		t.Errorf("图片占了轨道 %d..%d，文字却被分到其中的 %d——图会压住它",
			first, first+span-1, second)
	}
}

// TestDanmuImageDoesNotCoverText 是「图片弹幕和文字弹幕重叠」的回归判据。
//
// 判据必须是二维的：**x 与 y 同时相交才算视觉重叠**。同一条轨道上前后两条
// 弹幕纵向本来就相交，靠 x 错开——那正是追尾模型在做的事；只比纵向会把
// 正常情形全部误报成缺陷（我第一版测试就是这么错的）。
//
// 真正的缺陷是几何的：纯图弹幕高 字号×2.8 = 56px，而轨道只有 30px。它排在
// 一条轨道里，上下各溢出 13px 压进相邻轨道，而相邻轨道上的文字跟它 x 是
// 错开的也躲不掉——因为图片**纵向**盖过来了。
//
// 修法是让图片按高度占多条轨道（danmuLaneSpan）。这条测试按真实时序推进，
// 逐对检查飞行中的弹幕矩形是否二维相交。
func TestDanmuImageDoesNotCoverText(t *testing.T) {
	const font = danmuFontSize
	const S = 1425.0
	const viewH = 893.0
	lane := danmuLaneH(font)
	D := danmuDuration.Seconds()

	img := danmuLine{Emotes: []live.Emote{{URL: "u", Width: 64, Height: 64}}}
	txt := danmuLine{Text: "一条文字弹幕在这里"}

	// 纯图弹幕必须按高度占多条轨道，否则它就会纵向溢出。
	if span := danmuLaneSpan(img, font); span < 2 {
		t.Fatalf("纯图弹幕 span = %d，高 %vpx 排在 %vpx 的轨道里会溢出到相邻轨道",
			span, danmuHeightEstimate(img, font), lane)
	}

	type flying struct {
		born time.Time
		y, h float64
		w    float64
	}

	for _, every := range []int{1, 3, 0} { // 全是图 / 每三条一图 / 全是字
		a := &App{DanmuFont: font, DanmuDur: danmuDuration}
		a.danmuStageW = S
		a.danmuTracks = int(viewH * danmuAreaFrac / lane)

		var up []flying
		base := time.Now()
		const interval = 200 * time.Millisecond
		total := int(20 * time.Second / interval)

		for i := 0; i < total; i++ {
			now := base.Add(time.Duration(i) * interval)
			keep := up[:0]
			for _, b := range up {
				if now.Sub(b.born) < danmuDuration {
					keep = append(keep, b)
				}
			}
			up = keep
			if len(up) >= danmuFlyMax {
				continue
			}

			d := txt
			if every > 0 && i%every == 0 {
				d = img
			}
			w := float64(danmuWidthEstimate(d, font))
			tr := a.takeDanmuTrack(now, float32(w), danmuLaneSpan(d, font))
			h := float64(danmuHeightEstimate(d, font))
			y := float64(tr)*float64(lane) + 16

			for _, b := range up {
				if !(y < b.y+b.h && b.y < y+h) {
					continue // 纵向不相交，不可能重叠
				}
				dtA := now.Sub(b.born).Seconds()
				xa := S - (dtA/D)*(S+b.w)
				xb := S
				if xa < xb+w && xb < xa+b.w {
					t.Fatalf("every=%d 第 %d 条：纵向 y[%.0f,%.0f] 与 y[%.0f,%.0f] 相交，横向也相交——图压住了字",
						every, i, y, y+h, b.y, b.y+b.h)
				}
			}
			up = append(up, flying{now, y, h, w})
		}
	}
}
