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

// TestDanmuTracksDoNotOverlap 用真的分配器排一串混合弹幕，断言**轨道够用时
// 两条弹幕的纵向区间绝不相交**——也就是屏幕上不会出现图片压着文字。
//
// 这管的是主路径。轨道真的用尽时分配器会走「宁叠不丢」的兜底分支（见
// takeDanmuTrack 末尾），那种重叠是既定取舍，不在这里断言。
//
// 修复前这条必红：纯图弹幕和文字弹幕都只占 1 条轨道，而图高 56px、轨道 30px，
// 排在相邻两轨上的图和字必然相交。
func TestDanmuTracksDoNotOverlap(t *testing.T) {
	const font = danmuFontSize
	const tracks = 40 // 给足：10 张图 × 2 轨 + 10 条字 × 1 轨 = 30 < 40
	a := &App{DanmuFont: font, DanmuDur: danmuDuration}
	a.danmuTracks = tracks
	a.danmuStageW = 1200

	lane := danmuLaneH(font)
	base := time.Now()
	type placed struct {
		top, bottom float32
		img         bool
	}
	placedAll := make([]placed, 0, 20)

	for i := 0; i < 20; i++ {
		var d danmuLine
		if i%2 == 0 {
			d = danmuLine{Emotes: []live.Emote{{URL: "u", Width: 100, Height: 100}}}
		} else {
			d = danmuLine{Text: "文字"}
		}
		now := base.Add(time.Duration(i) * time.Millisecond)
		span := danmuLaneSpan(d, font)
		track := a.takeDanmuTrack(now, danmuWidthEstimate(d, font), span)
		if track+span > tracks {
			t.Fatalf("第 %d 条分到轨道 %d + span %d，超出 %d 条轨道", i, track, span, tracks)
		}
		top := float32(track)*lane + 16
		h := danmuHeightEstimate(d, font)
		placedAll = append(placedAll, placed{top, top + h, d.imageOnly()})
	}

	for i := range placedAll {
		for j := i + 1; j < len(placedAll); j++ {
			x, y := placedAll[i], placedAll[j]
			if x.top < y.bottom && y.top < x.bottom {
				t.Errorf("第 %d 条 [%v, %v] 与第 %d 条 [%v, %v] 纵向重叠%s",
					i, x.top, x.bottom, j, y.top, y.bottom,
					overlapNote(x.img, y.img))
			}
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

func overlapNote(a, b bool) string {
	switch {
	case a && b:
		return "（两张图）"
	case a || b:
		return "（图压着文字）"
	default:
		return "（两条文字）"
	}
}
