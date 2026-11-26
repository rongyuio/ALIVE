package main

import (
	"fmt"
	"sort"
)

// dwell 模式：把一段序列切成「亮度连续」的段，报每段挂了多少帧、多久。
//
// 为什么需要它：切平台的「闪」在 2026-11-26 被证实**不能用 delta 峰值判**。
// 切平台本来就是硬切（0010 规定内容层不做动画），一帧之内从 A 换到 B，
// 相邻帧平均差必然到 46-66，看着像剧烈跳变。而用户抱怨的「闪」是
// **一片空白挂了多久**——那是时间量，不是差分量。
//
// 现有判据（与参照帧比）回答不了这个：跨平台切换时参照帧本身就不是同一
// 幅画面。所以要单开一个模式，量「停留」。
//
// 判据：每段 > 6 帧（约 100ms）才算问题。1-3 帧是设计内的骨架。
func dwellReport(o options, a *Sequence) int {
	n := a.Count()
	if n == 0 {
		fmt.Printf("序列里没有帧\n")
		return 2
	}
	if o.dwellLo <= 0 || o.dwellHi <= 0 {
		fmt.Printf("-dwell-lo / -dwell-hi 必须是正数（默认 %.0f / %.0f）\n", dwellLoDefault, dwellHiDefault)
		return 2
	}

	type seg struct {
		from, to int     // 帧号
		firstAt  int64   // ms
		lum      float64 // 这段的平均亮度
	}
	var segs []seg
	cur := seg{from: 0, firstAt: a.Meta[0].At / 1000}
	var sum float64
	var cnt int

	flush := func(to int) {
		if cnt == 0 {
			return
		}
		cur.to, cur.lum = to, sum/float64(cnt)
		segs = append(segs, cur)
	}

	var prevLum float64
	for i := 0; i < n; i++ {
		pix, err := a.Frame(i)
		if err != nil {
			return 2
		}
		var s int64
		for p := 0; p < len(pix); p += 4 {
			// BGRA，感知亮度近似（B 1 / G 2 / R 3）
			s += int64(pix[p]) + 2*int64(pix[p+1]) + 3*int64(pix[p+2])
		}
		lum := float64(s) / float64(len(pix)/4) / 6.0

		// 「同一段」= 亮度落在前一帧的容差带里。带子宽 o.dwellBand 就是为了
		// 容忍时钟跳秒那种小变化；跨平台/骨架这种整屏级的变化一定越界。
		if cnt > 0 && (lum < prevLum-o.dwellBand || lum > prevLum+o.dwellBand) {
			flush(i - 1)
			cur = seg{from: i, firstAt: a.Meta[i].At / 1000}
			sum, cnt = 0, 0
		}
		sum += lum
		cnt++
		prevLum = lum
	}
	flush(n - 1)

	// 只报「像骨架/像空白」和「像内容」的两类：中间态没人看。
	type line struct {
		seg
		frames int
		ms     int64
		kind   string
	}
	var lines []line
	for _, s := range segs {
		frames := s.to - s.from + 1
		var ms int64
		if s.to+1 < n {
			ms = (a.Meta[s.to+1].At - a.Meta[s.from].At) / 1000
		} else if s.to > s.from {
			// 末段没有「下一帧」的起点：按段内平均帧间隔外推，而不是报 0。
			ms = (a.Meta[s.to].At - a.Meta[s.from].At) / 1000
			ms += ms / int64(frames-1)
		}
		kind := "内容"
		switch {
		case s.lum >= o.dwellHi:
			kind = "灰白/骨架"
		case s.lum <= o.dwellLo:
			kind = "黑"
		}
		lines = append(lines, line{seg: s, frames: frames, ms: ms, kind: kind})
	}

	fmt.Printf("A = %s  %dx%d  %d 帧\n", a.Dir, a.Width, a.Height, n)
	fmt.Printf("分段：亮度跨出前帧 ±%.1f 就切一段；灰白 ≥%.0f、黑 ≤%.0f\n\n",
		o.dwellBand, o.dwellHi, o.dwellLo)
	fmt.Printf("%-6s %-8s %-9s %-8s %-10s %s\n", "起帧", "起(ms)", "帧数", "时长", "亮度", "判定")

	var bad int
	for _, l := range lines {
		mark := ""
		if l.kind != "内容" && l.frames > o.dwellMaxFrames {
			mark = "  ← 超过 " + fmt.Sprint(o.dwellMaxFrames) + " 帧，用户能看到"
			bad++
		}
		if l.kind != "内容" || l.frames > 30 {
			fmt.Printf("%-6d %-8d %-9d %-8s %-10.1f %s%s\n",
				l.from, l.firstAt, l.frames, fmt.Sprintf("%dms", l.ms), l.lum, l.kind, mark)
		}
	}

	fmt.Printf("\n共 %d 段\n", len(lines))
	if bad == 0 {
		fmt.Printf("结论：没有任何一段白/黑超过 %d 帧（约 %dms）。这类「闪」可以结案。\n",
			o.dwellMaxFrames, o.dwellMaxFrames*16)
		return 0
	}
	fmt.Printf("结论：%d 段白/黑超过 %d 帧——用户能看到。先看那几段的起帧，导出 PNG 核对。\n",
		bad, o.dwellMaxFrames)
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].frames > lines[j].frames })
	return 1
}

const (
	dwellLoDefault      = 40.0
	dwellHiDefault      = 215.0
	dwellBandDefault    = 6.0
	dwellMaxFramesDeflt = 6
)
