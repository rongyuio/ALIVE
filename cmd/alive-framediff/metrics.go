package main

import "math"

// rect 是像素坐标下的比对区域。
type rect struct{ X, Y, W, H int }

func (r rect) clamp(w, h int) rect {
	if r.W <= 0 || r.H <= 0 {
		return rect{0, 0, w, h}
	}
	x0, y0 := max(0, r.X), max(0, r.Y)
	x1, y1 := min(w, r.X+r.W), min(h, r.Y+r.H)
	if x1 <= x0 || y1 <= y0 {
		return rect{0, 0, w, h}
	}
	return rect{x0, y0, x1 - x0, y1 - y0}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// luma 取一个 BGRA 像素的亮度（Rec.601 的定点近似），给「位移」一个稳定的参照。
func luma(px []byte) int { return (29*int(px[0]) + 150*int(px[1]) + 77*int(px[2])) >> 8 }

// profiles 返回两条一维投影：
//
//	col[x] = Σ_y |L(x,y) - L(x+1,y)|   竖边的强度，按列累加
//	row[y] = Σ_x |L(x,y) - L(x,y+1)|   横边的强度，按行累加
//
// 界面里横平竖直的边远多于斜边，整块内容平移时这两条投影的形状不变、只是
// 错位：拿它找整帧平移比在二维上暴力搜便宜得多（O(W·maxShift) 而不是
// O(W·H·maxShift²)），也更稳。
func profiles(pix []byte, stride int, rg rect) (col, row []float64) {
	col = make([]float64, max(0, rg.W-1))
	row = make([]float64, max(0, rg.H-1))
	prevRow := make([]int, rg.W)
	for y := 0; y < rg.H; y++ {
		base := (rg.Y + y) * stride
		prevL := 0
		for x := 0; x < rg.W; x++ {
			cur := luma(pix[base+(rg.X+x)*4:])
			if x > 0 {
				col[x-1] += float64(abs(cur - prevL))
			}
			if y > 0 {
				row[y-1] += float64(abs(cur - prevRow[x]))
			}
			prevRow[x], prevL = cur, cur
		}
	}
	return col, row
}

// bestOffset 找让 a[i] 对上 b[i+d] 的 d：在 |d| <= maxShift 内取按重叠长度
// 归一后 L1 距离最小的那个，同时返回 d=0 和最优 d 的代价。
func bestOffset(a, b []float64, maxShift int) (best int, bestCost, zeroCost float64) {
	if len(a) < 2 || len(b) < 2 {
		return 0, 0, 0
	}
	best, bestCost = 0, math.Inf(1)
	lo := max(-maxShift, -(len(a) - 1))
	hi := min(maxShift, len(b)-1)
	for d := lo; d <= hi; d++ {
		i0 := max(0, -d)
		i1 := min(len(a), len(b)-d)
		if i1 <= i0 {
			continue
		}
		var sum float64
		for i := i0; i < i1; i++ {
			sum += math.Abs(a[i] - b[i+d])
		}
		c := sum / float64(i1-i0)
		if d == 0 {
			zeroCost = c
		}
		// 同代价时偏向小的 |d|：重复纹理的界面不至于被无谓地推走。
		if c < bestCost-1e-9 || (math.Abs(c-bestCost) <= 1e-9 && abs(d) < abs(best)) {
			best, bestCost = d, c
		}
	}
	return best, bestCost, zeroCost
}

// Metrics 是一次逐帧比对的全部数字。
type Metrics struct {
	// DX, DY 是把 frame 平移多少像素才和 reference 最像（整帧最佳对齐
	// 偏移）。内容整体动过，这个数就是位移量。
	DX, DY int
	// MeanAbs 是未对齐时区域内每像素平均的通道绝对差（0..255）——用户眼睛
	// 看到的就是它。
	MeanAbs float64
	// AlignedMeanAbs 是按 DX,DY 对齐后的同一个量：它小而 MeanAbs 大，说明
	// 「内容没变，位置变了」。
	AlignedMeanAbs float64
	// Changed 是最大通道差超过 pixTol 的像素数，Pixels 是比对的像素数。
	Changed, Pixels int
}

// ChangedPct 是变化像素占区域的百分比。
func (m Metrics) ChangedPct() float64 {
	if m.Pixels == 0 {
		return 0
	}
	return 100 * float64(m.Changed) / float64(m.Pixels)
}

// compare 比对两帧（紧凑 BGRA，每行 width*4 字节）。
func compare(reference, frame []byte, w, h int, rg rect, maxShift, pixTol int) Metrics {
	stride := w * 4
	rg = rg.clamp(w, h)
	m := Metrics{Pixels: rg.W * rg.H}

	ca, ra := profiles(reference, stride, rg)
	cb, rb := profiles(frame, stride, rg)
	m.DX, _, _ = bestOffset(ca, cb, maxShift)
	m.DY, _, _ = bestOffset(ra, rb, maxShift)

	// 逐像素：平均绝对差、超阈值像素数（未对齐）。
	var sum float64
	for y := rg.Y; y < rg.Y+rg.H; y++ {
		base := y * stride
		for x := rg.X; x < rg.X+rg.W; x++ {
			o := base + x*4
			d := absDiff(reference[o:], frame[o:])
			sum += float64(d)
			if d > pixTol {
				m.Changed++
			}
		}
	}
	m.MeanAbs = sum / float64(m.Pixels)

	// 对齐后重采样一次：每 4 像素取一个，只用来回答「是不是只是挪了」。
	// 约定：DX,DY 是被比那帧相对参照的位移，所以 frame(x,y) 的内容在
	// reference 的 (x-DX, y-DY)。
	const s = 4
	var asum float64
	var an int
	for y := rg.Y; y < rg.Y+rg.H; y += s {
		for x := rg.X; x < rg.X+rg.W; x += s {
			sx, sy := x-m.DX, y-m.DY
			if sx < 0 || sy < 0 || sx >= w || sy >= h {
				continue
			}
			asum += float64(absDiff(reference[sy*stride+sx*4:], frame[y*stride+x*4:]))
			an++
		}
	}
	if an > 0 {
		m.AlignedMeanAbs = asum / float64(an)
	}
	return m
}

// absDiff 返回两个 BGRA 像素三个颜色通道里最大的绝对差（alpha 不算：窗口
// 不透明，alpha 恒 255）。
func absDiff(a, b []byte) int {
	d := 0
	for i := 0; i < 3; i++ {
		if v := abs(int(a[i]) - int(b[i])); v > d {
			d = v
		}
	}
	return d
}
