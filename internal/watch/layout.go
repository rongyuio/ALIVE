// Package watch 是「悲鸣观测」的纯逻辑：房间上限、布局、会话调度。
//
// 这里不碰 MyGo，也不碰网络。布局是纯函数，能在测试里把 1–12 间房、横屏/竖屏、
// 不同窗口比例一次算完；会话部分只描述“该开哪几路”，不负责取流。
package watch

import "math"

// MaxRooms 是观测墙的硬上限。
//
// 12 是产品上限，不是根据本机带宽算出来的：每个用户的带宽不同，产品只保证
// 不超过 12 路。平台侧实测四个平台单平台 9 路都能同时出流（2026-11-21）；
// 10–12 路尚未实测，超出的余量按同样的「不降档、不做带宽准入」处理。
const MaxRooms = 12

// DefaultAspect 是格子的统一画面比例（16:9）。
const DefaultAspect float32 = 16.0 / 9.0

// Item 是参与布局的一格。Aspect 是宽/高；观测墙统一传 16:9。
type Item struct {
	Key    string
	Aspect float32
}

// Rect 是一格画面在容器里的位置与大小，单位与传入的宽高一致。
type Rect struct {
	X, Y, W, H float32
}

// Layout 把 n 格排成「每格等大」的网格，返回与 items 同序的画面矩形。
//
// 每格矩形按画面比例；墙上的卡片就是这块矩形，视频在里面正好铺满。
// items 的 Aspect 用于给行/列组合打分。
//
// 规则：
//   - 枚举行数 1..n，列数 = ceil(n/rows)，格子同宽同高；
//   - 每格的画面按自己的比例 contain 在格子里，不裁剪；
//   - 以「所有格里最小的画面面积」为评分，取最大的排法；同分时空格更少、
//     行数更多者优先；
//   - 行按列左对齐；最后一行不满时，空位留在行尾右侧。
//
// 因此 3 间是 2+1（C 在第二行左列，右下空；1440×800 下每格约 700×394），
// 不是一横排的 458×258；7 间严格按行填是 3+3+1（右下两格空），每格约
// 460×259；9 间是 3×3；12 间是 3 行 4 列。所有格子的画面尺寸相同，不会
// 出现一大几小。
//
// 观测墙固定传 16:9，所以画面正好填满格子；竖屏/4:3 的流在格子里留黑边，
// 不裁剪。gap 是格与格之间的间距；最后一行不满时，空格留在两侧。
func Layout(items []Item, w, h, gap float32) []Rect {
	n := len(items)
	if n == 0 || w <= 0 || h <= 0 {
		return nil
	}
	if gap < 0 {
		gap = 0
	}
	if n > MaxRooms {
		n = MaxRooms
		items = items[:n]
	}

	aspects := make([]float32, n)
	for i, it := range items {
		a := it.Aspect
		if !(a > 0) || math.IsInf(float64(a), 0) || math.IsNaN(float64(a)) {
			a = DefaultAspect
		}
		aspects[i] = a
	}

	best := grid{minArea: -1}
	for rows := 1; rows <= n; rows++ {
		cols := (n + rows - 1) / rows
		cellW := (w - gap*float32(cols-1)) / float32(cols)
		cellH := (h - gap*float32(rows-1)) / float32(rows)
		if cellW <= 0 || cellH <= 0 {
			continue
		}
		minArea := float32(math.MaxFloat32)
		for _, a := range aspects {
			vw, vh := containSize(a, cellW, cellH)
			minArea = min(minArea, vw*vh)
		}
		g := grid{rows: rows, cols: cols, cellW: cellW, cellH: cellH,
			minArea: minArea, empty: rows*cols - n}
		if betterGrid(g, best) {
			best = g
		}
	}

	// 按行从左到右顺序填格；最后一行不满时，空位留在行尾右侧。
	// 这样增删房间只影响行尾，已有格子的列位置不动。
	rects := make([]Rect, n)
	idx := 0
	for r := 0; r < best.rows; r++ {
		count := min(best.cols, n-idx)
		for c := 0; c < count; c++ {
			a := aspects[idx]
			vw, vh := containSize(a, best.cellW, best.cellH)
			x := float32(c)*(best.cellW+gap) + (best.cellW-vw)/2
			y := float32(r)*(best.cellH+gap) + (best.cellH-vh)/2
			rects[idx] = Rect{X: x, Y: y, W: vw, H: vh}
			idx++
		}
	}
	return rects
}

type grid struct {
	rows, cols int
	cellW      float32
	cellH      float32
	minArea    float32
	empty      int
}

// betterGrid 先看最小画面面积，再让空格更少、行数更多。
func betterGrid(a, b grid) bool {
	if b.minArea < 0 {
		return true
	}
	const eps = 0.5
	if a.minArea > b.minArea+eps {
		return true
	}
	if a.minArea < b.minArea-eps {
		return false
	}
	if a.empty != b.empty {
		return a.empty < b.empty
	}
	if a.rows != b.rows {
		return a.rows > b.rows
	}
	return a.cols < b.cols
}

// containSize 返回 aspect 的画面放进 cellW×cellH 后的实际宽高。
func containSize(aspect, cellW, cellH float32) (float32, float32) {
	if cellW/cellH > aspect {
		return cellH * aspect, cellH
	}
	return cellW, cellW / aspect
}
