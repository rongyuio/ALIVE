// alive-framediff 比对两段「真窗口逐帧录制」（或一段里的两帧），逐帧给出
//
//	· 整帧最佳对齐偏移 —— 内容是整体动了（「跳了一下」「抖」），动多少正是
//	  这个偏移；
//	· 逐帧像素差 —— 平均通道绝对差、超过阈值的像素数（「闪一下」时位置没变、
//	  像素在变）；
//	· 一张可读的帧表 —— 第几帧开始位移 / 闪烁，参照帧是哪一帧；
//	· 退出码 —— 有任何一帧超过阈值就是 1。
//
// 录制的生成见仓库根目录 record_e2e.go（`ALIVE_RECORD=<dir>`，只在 e2e 构建
// 里存在）；判据与阈值的出处见 doc/reference/frame-recording.md。
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"strings"
)

func main() {
	os.Exit(run())
}

type options struct {
	a, b       string
	ref        int
	from       int
	limit      int
	region     string
	align      string
	timeTol    int
	maxShift   int
	shiftTol   int
	pixTol     int
	meanTol    float64
	changedPct float64
	every      int
	pngPath    string
	pngFrame   int
	pngSeq     string
	// calibrate 只量这台机器的噪音底，不给判据、不返回失败。
	//
	// 为什么需要它：阈值（1 px、meanΔ 0.100、0.010%）是在 9700X + RX 9070 XT
	// 上量出来的，那里的噪音底是 0（同一场景两次独立录制逐字节相同）。换一台
	// 机器/驱动如果不为 0，固定阈值会天天报假 FAIL——所以换环境第一件事是跑
	// 这个，把实测的底和当前阈值摆在一起看。
	calibrate bool
	// dwell 模式：不比对，只报「亮度连续段」——一片白/黑挂了多少帧。
	// 「切平台闪不闪」只能这样判，见 doc/reference/ui-regressions.md。
	dwell          bool
	dwellLo        float64
	dwellHi        float64
	dwellBand      float64
	dwellMaxFrames int
}

func run() int {
	var o options
	flag.StringVar(&o.a, "a", "", "第一段序列的目录（必填）")
	flag.StringVar(&o.b, "b", "", "第二段序列的目录；不给就是「同一段里的每一帧 vs 参照帧」")
	flag.IntVar(&o.ref, "ref", 0, "自比模式的参照帧号（默认 0，即第一帧）")
	flag.IntVar(&o.from, "from", 0, "从第几帧开始比（默认 0）")
	flag.IntVar(&o.limit, "limit", 0, "最多比多少帧（0 = 到底）")
	flag.StringVar(&o.region, "region", "", "只比这块区域，像素坐标 x,y,w,h（默认整帧）")
	flag.StringVar(&o.align, "align", "time", "两段怎么配对：time（按 manifest 里的时间戳，默认）或 index（同号帧）")
	flag.IntVar(&o.timeTol, "time-tol", 8, "time 配对时容许的时刻差，毫秒")
	flag.IntVar(&o.maxShift, "max-shift", 24, "找整帧偏移的搜索半径，像素")
	flag.IntVar(&o.shiftTol, "shift-tol", 1, "位移容差，像素：|dx| 或 |dy| 达到它就算位移")
	flag.IntVar(&o.pixTol, "pix", 8, "一个像素算「变了」的门槛：最大通道差 > 它")
	flag.Float64Var(&o.meanTol, "mean-tol", meanTolDefault, "平均通道绝对差的上限（0..255），超过算闪")
	flag.Float64Var(&o.changedPct, "changed-pct", changedPctDefault, "变化像素占比的上限（%），超过算闪")
	flag.IntVar(&o.every, "every", 1, "帧表每隔几帧打一行（小于 1 按 1）")
	flag.StringVar(&o.pngPath, "png", "", "把某一帧导出成 PNG（用来人眼核对参照帧）")
	flag.IntVar(&o.pngFrame, "png-frame", 0, "-png 用第几帧")
	flag.StringVar(&o.pngSeq, "png-seq", "a", "-png 从哪一段取（a 或 b）")
	flag.BoolVar(&o.calibrate, "calibrate", false,
		"只量这台的噪音底：给两段同一场景的录制，输出差的分布和建议阈值，不给判据")
	flag.BoolVar(&o.dwell, "dwell", false,
		"不比对，只报亮度连续段：一片白/黑持续了多少帧（判「闪」用这个，不用 delta）")
	flag.Float64Var(&o.dwellLo, "dwell-lo", dwellLoDefault, "亮度 ≤ 它算「黑」")
	flag.Float64Var(&o.dwellHi, "dwell-hi", dwellHiDefault, "亮度 ≥ 它算「灰白/骨架」")
	flag.Float64Var(&o.dwellBand, "dwell-band", dwellBandDefault, "亮度跨出前帧这么多就切一段")
	flag.IntVar(&o.dwellMaxFrames, "dwell-max", dwellMaxFramesDeflt,
		"白/黑超过这么多帧才算用户看得到（默认 6 帧 ≈ 100ms）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: alive-framediff -a <目录> [-b <目录>] [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
阈值的出处（详见 doc/reference/frame-recording.md）：
  位移 ≥%d px：录制是设备像素，内容只能按整像素移动，1 px 是位移的最小单位；
    而 doc/reference/ui-regressions.md 的布局容差 0.01 DIP 即使在 4x 缩放下也
    远小于 1 物理像素——1 px 位移比它大两个数量级。
  平均差 >%.3f、占比 >%.4f%%：来自同一场景两段录制的实测噪音（2026-11-26，
    2138x1339，60fps，见文档里的实测表），留了余量，不能为过而调大。
`, o.shiftTol, o.meanTol, o.changedPct)
	}
	flag.Parse()
	if o.a == "" {
		flag.Usage()
		return 2
	}
	if o.every < 1 {
		o.every = 1
	}

	a, err := OpenSequence(o.a)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
		return 2
	}
	defer func() { _ = a.Close() }()

	if o.pngPath != "" {
		if err := exportPNG(o, a); err != nil {
			fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
			return 2
		}
		return 0
	}

	var b *Sequence
	if o.b != "" {
		if b, err = OpenSequence(o.b); err != nil {
			fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
			return 2
		}
		defer func() { _ = b.Close() }()
		if b.Width != a.Width || b.Height != a.Height {
			fmt.Fprintf(os.Stderr, "alive-framediff: 两段尺寸不同：%dx%d 对 %dx%d\n",
				a.Width, a.Height, b.Width, b.Height)
			return 2
		}
	}
	if o.dwell {
		if b != nil {
			fmt.Fprintf(os.Stderr, "alive-framediff: -dwell 只看一段，不要给 -b\n")
			return 2
		}
		return dwellReport(o, a)
	}
	return report(o, a, b)
}

// frameResult 是一帧的判定结果。
type frameResult struct {
	index  int
	at     int64 // 毫秒
	m      Metrics
	shift  bool
	flickr bool
}

func (h frameResult) verdict() string {
	switch {
	case h.shift && h.flickr:
		return "SHIFT+FLICKER"
	case h.shift:
		return "SHIFT"
	case h.flickr:
		return "FLICKER"
	}
	return "ok"
}

// pair 是一对要比的帧：A 的 ia，B 的 ib（自比模式是 -1，参照是 A 的 -ref 帧）。
type pair struct{ ia, ib int }

// align 排出要比的帧对。两段时按 -align 配对：time 用 manifest 里记的时间戳
// （两次录制各自从自己的第一帧起算，所以可以直接对齐），index 用同号帧。
func alignPairs(o options, a, b *Sequence) ([]pair, error) {
	var pairs []pair
	if b == nil {
		hi := a.Count()
		if o.limit > 0 && o.from+o.limit < hi {
			hi = o.from + o.limit
		}
		if o.from < 0 || o.from >= hi {
			return nil, fmt.Errorf("帧号越界（from=%d，A 有 %d 帧）", o.from, a.Count())
		}
		if o.ref < 0 || o.ref >= a.Count() {
			return nil, fmt.Errorf("参照帧越界（ref=%d，A 有 %d 帧）", o.ref, a.Count())
		}
		for i := o.from; i < hi; i++ {
			pairs = append(pairs, pair{ia: i, ib: -1})
		}
		return pairs, nil
	}
	switch o.align {
	case "index":
		hi := min(a.Count(), b.Count())
		if o.limit > 0 && o.from+o.limit < hi {
			hi = o.from + o.limit
		}
		if o.from < 0 || o.from >= hi {
			return nil, fmt.Errorf("帧号越界（from=%d，可比 %d 帧）", o.from, hi)
		}
		for i := o.from; i < hi; i++ {
			pairs = append(pairs, pair{ia: i, ib: i})
		}
	case "time":
		big := make([]int64, b.Count())
		for j := range b.Meta {
			big[j] = b.Meta[j].At
		}
		hi := a.Count()
		if o.limit > 0 && o.from+o.limit < hi {
			hi = o.from + o.limit
		}
		if o.from < 0 || o.from >= hi {
			return nil, fmt.Errorf("帧号越界（from=%d，A 有 %d 帧）", o.from, a.Count())
		}
		tol := int64(o.timeTol) * 1000 // 毫秒 → 微秒
		for i := o.from; i < hi; i++ {
			j := nearest(big, a.Meta[i].At, tol)
			if j < 0 {
				continue // B 没有这一刻，跳过而不是硬比
			}
			pairs = append(pairs, pair{ia: i, ib: j})
		}
	default:
		return nil, fmt.Errorf("-align 只认 time 或 index，收到 %q", o.align)
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("没有可配对的帧（align=%s, time-tol=%dms）", o.align, o.timeTol)
	}
	return pairs, nil
}

// nearest 在升序的 t 里找离 at 最近、且距离不超过 tol 的下标，没有就 -1。
func nearest(t []int64, at, tol int64) int {
	lo, hi := 0, len(t)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if t[mid] < at {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	best := -1
	var bestD int64
	for _, j := range []int{lo - 1, lo} {
		if j < 0 || j >= len(t) {
			continue
		}
		d := t[j] - at
		if d < 0 {
			d = -d
		}
		if d <= tol && (best < 0 || d < bestD) {
			best, bestD = j, d
		}
	}
	return best
}

// report 逐帧比对，打印帧表与结论，返回退出码。
func report(o options, a, b *Sequence) int {
	pairs, err := alignPairs(o, a, b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
		return 2
	}
	rg := parseRegion(o.region).clamp(a.Width, a.Height)

	// 自比模式的参照帧要先拷出来：Sequence.Frame 的缓冲会被下一帧覆盖。
	var refPix []byte
	if b == nil {
		p, err := a.Frame(o.ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
			return 2
		}
		refPix = append([]byte(nil), p...)
	}

	fmt.Printf("A = %s  %dx%d  %d 帧  codec %s\n", a.Dir, a.Width, a.Height, a.Count(), a.Codec)
	if b != nil {
		fmt.Printf("B = %s  %dx%d  %d 帧  codec %s\n", b.Dir, b.Width, b.Height, b.Count(), b.Codec)
		fmt.Printf("配对：%s（A 是参照、B 被比），%d 对，A 第 %d..%d 帧\n",
			alignName(o), len(pairs), pairs[0].ia, pairs[len(pairs)-1].ia)
	} else {
		fmt.Printf("参照：A[%d]（t=%dms）\n", o.ref, a.Meta[o.ref].At/1000)
	}
	fmt.Printf("区域：%d,%d %dx%d px；max-shift %d px\n", rg.X, rg.Y, rg.W, rg.H, o.maxShift)
	fmt.Printf("阈值：位移 ≥%d px；平均差 >%.3f；>%d 通道差的像素占比 >%.4f%%\n",
		o.shiftTol, o.meanTol, o.pixTol, o.changedPct)
	fmt.Printf("位移 = 被比的那帧相对参照挪了多少 px（+x 向右、+y 向下）\n\n")

	if !o.calibrate {
		if b != nil {
			fmt.Printf("%-6s %-6s %-9s %-11s %-9s %-9s %-9s %s\n",
				"帧A", "帧B", "t(ms)", "位移", "平均差", fmt.Sprintf(">%dpx", o.pixTol), "占比%", "判定")
		} else {
			fmt.Printf("%-6s %-9s %-11s %-9s %-9s %-9s %s\n",
				"帧", "t(ms)", "位移", "平均差", fmt.Sprintf(">%dpx", o.pixTol), "占比%", "判定")
		}
	}

	var hits []frameResult
	var firstShift, firstFlicker, worstShift, worstMean *frameResult
	compared := 0
	var calMeans []float64
	var calShifts []int

	for k, pr := range pairs {
		i := pr.ia
		pa, err := a.Frame(i)
		if err != nil {
			fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
			return 2
		}
		ref, frame := refPix, pa
		if b != nil {
			pb, err := b.Frame(pr.ib)
			if err != nil {
				fmt.Fprintf(os.Stderr, "alive-framediff: %v\n", err)
				return 2
			}
			ref, frame = pa, pb
		}
		m := compare(ref, frame, a.Width, a.Height, rg, o.maxShift, o.pixTol)
		compared++
		if o.calibrate {
			calMeans = append(calMeans, m.MeanAbs)
			calShifts = append(calShifts, abs(m.DX)+abs(m.DY))
		}

		h := frameResult{index: i, at: a.Meta[i].At / 1000, m: m}
		h.shift = abs(m.DX) >= o.shiftTol || abs(m.DY) >= o.shiftTol
		h.flickr = m.MeanAbs > o.meanTol || m.ChangedPct() > o.changedPct
		if h.shift || h.flickr {
			hits = append(hits, h)
		}
		if h.shift && firstShift == nil {
			c := h
			firstShift = &c
		}
		if h.flickr && firstFlicker == nil {
			c := h
			firstFlicker = &c
		}
		if worstShift == nil || abs(m.DX)+abs(m.DY) > abs(worstShift.m.DX)+abs(worstShift.m.DY) {
			c := h
			worstShift = &c
		}
		if worstMean == nil || m.MeanAbs > worstMean.m.MeanAbs {
			c := h
			worstMean = &c
		}
		// calibrate 模式不打帧表：它要的是分布，逐帧行只会把结论埋掉。
		if !o.calibrate && (h.shift || h.flickr || k%o.every == 0) {
			if b != nil {
				fmt.Printf("%-6d %-6d %-9d %-11s %-9.3f %-9d %-9.4f %s\n",
					i, pr.ib, h.at, fmt.Sprintf("(%+d,%+d)", m.DX, m.DY), m.MeanAbs, m.Changed, m.ChangedPct(), h.verdict())
			} else {
				fmt.Printf("%-6d %-9d %-11s %-9.3f %-9d %-9.4f %s\n",
					i, h.at, fmt.Sprintf("(%+d,%+d)", m.DX, m.DY), m.MeanAbs, m.Changed, m.ChangedPct(), h.verdict())
			}
		}
	}

	if o.calibrate {
		return calibrateReport(o, calMeans, calShifts, compared)
	}

	fmt.Printf("\n对比了 %d 帧（A 第 %d..%d 帧）\n", compared, pairs[0].ia, pairs[len(pairs)-1].ia)
	if firstShift == nil {
		fmt.Printf("位移：全程 |Δ| < %d px（最大 (%+d,%+d)，第 %d 帧）\n",
			o.shiftTol, worstShift.m.DX, worstShift.m.DY, worstShift.index)
	} else {
		fmt.Printf("位移：最早在第 %d 帧（t=%dms）出现 (%+d,%+d) px，参照 %s\n",
			firstShift.index, firstShift.at, firstShift.m.DX, firstShift.m.DY, refName(o, b))
	}
	if firstFlicker == nil {
		fmt.Printf("像素：全程平均差 ≤ %.3f、占比 ≤ %.4f%%（最大平均差 %.3f，第 %d 帧）\n",
			o.meanTol, o.changedPct, worstMean.m.MeanAbs, worstMean.index)
	} else {
		fmt.Printf("像素：最早在第 %d 帧（t=%dms）闪：平均差 %.3f、>%d 通道差的像素 %d 个（%.4f%%）\n",
			firstFlicker.index, firstFlicker.at, firstFlicker.m.MeanAbs, o.pixTol,
			firstFlicker.m.Changed, firstFlicker.m.ChangedPct())
	}

	if len(hits) == 0 {
		fmt.Printf("结论：PASS —— 没有一帧超过阈值\n")
		return 0
	}
	ns, nf := 0, 0
	for _, h := range hits {
		if h.shift {
			ns++
		}
		if h.flickr {
			nf++
		}
	}
	fmt.Printf("结论：FAIL —— %d 帧超过阈值（位移 %d 帧、像素 %d 帧）：", len(hits), ns, nf)
	for i, h := range hits {
		if i == 12 {
			fmt.Printf(" …（共 %d 帧，逐帧见上表）", len(hits))
			break
		}
		if i > 0 {
			fmt.Printf("、")
		}
		fmt.Printf("%d", h.index)
	}
	fmt.Printf("\n")
	return 1
}

func refName(o options, b *Sequence) string {
	if b != nil {
		if o.align == "time" {
			return "B 里同一时刻的帧"
		}
		return "A 的同号帧"
	}
	return fmt.Sprintf("A[%d]", o.ref)
}

func alignName(o options) string {
	if o.align == "time" {
		return fmt.Sprintf("按时间戳配对，容许 %d ms", o.timeTol)
	}
	return "按帧号同号配对"
}

// parseRegion 解析 "x,y,w,h"；空串是整帧。
func parseRegion(s string) rect {
	if strings.TrimSpace(s) == "" {
		return rect{}
	}
	var r rect
	if _, err := fmt.Sscanf(s, "%d,%d,%d,%d", &r.X, &r.Y, &r.W, &r.H); err != nil {
		fmt.Fprintf(os.Stderr, "alive-framediff: -region 要写成 x,y,w,h：%v\n", err)
		os.Exit(2)
	}
	return r
}

// exportPNG 把一帧写成 PNG，用来人眼核对参照帧 / 被指出的那一帧。
func exportPNG(o options, a *Sequence) error {
	seq := a
	if o.pngSeq == "b" {
		if o.b == "" {
			return fmt.Errorf("-png-seq b 要同时给 -b")
		}
		var err error
		if seq, err = OpenSequence(o.b); err != nil {
			return err
		}
		defer func() { _ = seq.Close() }()
	}
	pix, err := seq.Frame(o.pngFrame)
	if err != nil {
		return err
	}
	img := image.NewNRGBA(image.Rect(0, 0, seq.Width, seq.Height))
	for y := 0; y < seq.Height; y++ {
		src := pix[y*seq.Width*4:]
		dst := img.Pix[y*img.Stride:]
		for x := 0; x < seq.Width; x++ {
			dst[x*4+0] = src[x*4+2] // R
			dst[x*4+1] = src[x*4+1] // G
			dst[x*4+2] = src[x*4+0] // B
			dst[x*4+3] = 0xFF
		}
	}
	f, err := os.Create(o.pngPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		return err
	}
	fmt.Printf("%s  %dx%d  %s 第 %d 帧\n", o.pngPath, seq.Width, seq.Height, seq.Dir, o.pngFrame)
	return nil
}
