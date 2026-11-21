package watch

import (
	"fmt"
	"math"
	"testing"
)

func TestLayoutOneKeepsAspectAndCenters(t *testing.T) {
	rs := layoutN(1, DefaultAspect, 1440, 800, 12)
	if len(rs) != 1 {
		t.Fatalf("got %d rects", len(rs))
	}
	r := rs[0]
	if math.Abs(float64(r.W/r.H-DefaultAspect)) > 0.01 {
		t.Fatalf("aspect %.4f, want %.4f", r.W/r.H, DefaultAspect)
	}
	if r.H != 800 {
		t.Fatalf("single 16:9 in a 1440x800 box should fill the height, got h=%.1f", r.H)
	}
	if math.Abs(float64(r.X-(1440-r.W)/2)) > 0.5 {
		t.Fatalf("not centered: x=%.1f w=%.1f", r.X, r.W)
	}
}

func TestLayoutTwoRoomsAreLarge(t *testing.T) {
	rs := layoutN(2, DefaultAspect, 1440, 800, 12)
	assertRects(t, rs, 2, DefaultAspect, 1440, 800)
	if rs[0].W < 650 || rs[0].H < 360 {
		t.Fatalf("two rooms should be large, got %.1fx%.1f", rs[0].W, rs[0].H)
	}
	if math.Abs(float64(rs[0].Y-rs[1].Y)) > 0.5 {
		t.Fatalf("two rooms should share a row: y=%.1f,%.1f", rs[0].Y, rs[1].Y)
	}
}

func TestLayoutThreeRoomsAreLarge(t *testing.T) {
	rs := layoutN(3, DefaultAspect, 1440, 800, 12)
	assertRects(t, rs, 3, DefaultAspect, 1440, 800)
	if rs[0].W < 650 || rs[0].H < 350 {
		t.Fatalf("three rooms should be large, got %.1fx%.1f", rs[0].W, rs[0].H)
	}
	// 3 间是 2+1：前两格同一行，第三格在下一行居中；三格等大。
	if math.Abs(float64(rs[0].Y-rs[1].Y)) > 0.5 || rs[2].Y <= rs[0].Y {
		t.Fatalf("three rooms should be 2+1: %v", rs)
	}
	for i := 1; i < 3; i++ {
		if math.Abs(float64(rs[i].W-rs[0].W)) > 0.5 || math.Abs(float64(rs[i].H-rs[0].H)) > 0.5 {
			t.Fatalf("three rooms should be equal size: %v", rs)
		}
	}
}

func TestLayoutGridRowsAlign(t *testing.T) {
	// 3 间：[A][B] / [C][ ]，C 在 A 正下方，右下空。
	rs := layoutN(3, DefaultAspect, 1440, 800, 12)
	if math.Abs(float64(rs[2].X-rs[0].X)) > 0.5 {
		t.Fatalf("第三格没有对齐第一列：x %.1f / %.1f", rs[2].X, rs[0].X)
	}
	if rs[2].Y <= rs[0].Y {
		t.Fatalf("第三格没有落到第二行：y %.1f / %.1f", rs[2].Y, rs[0].Y)
	}
	// 7 间：[A][B][C] / [D][E][F] / [G][ ][ ]，G 对齐第一列。
	rs = layoutN(7, DefaultAspect, 1440, 800, 12)
	if math.Abs(float64(rs[6].X-rs[0].X)) > 0.5 {
		t.Fatalf("第七格没有对齐第一列：x %.1f / %.1f", rs[6].X, rs[0].X)
	}
	if rs[6].Y <= rs[3].Y {
		t.Fatalf("第七格没有落到第三行：y %.1f / %.1f", rs[6].Y, rs[3].Y)
	}
}

func TestLayoutEqualSizes(t *testing.T) {
	for _, n := range []int{3, 5, 7, 8} {
		rs := layoutN(n, DefaultAspect, 1440, 800, 12)
		assertRects(t, rs, n, DefaultAspect, 1440, 800)
		for i := 1; i < n; i++ {
			if math.Abs(float64(rs[i].W-rs[0].W)) > 0.5 || math.Abs(float64(rs[i].H-rs[0].H)) > 0.5 {
				t.Fatalf("%d 间第 %d 格 %.1fx%.1f 与第一格 %.1fx%.1f 不等大",
					n, i, rs[i].W, rs[i].H, rs[0].W, rs[0].H)
			}
		}
	}
}

func TestLayoutNineRoomsFillTheWall(t *testing.T) {
	rs := layoutN(9, DefaultAspect, 1440, 800, 12)
	assertRects(t, rs, 9, DefaultAspect, 1440, 800)
	if rs[0].W < 400 || rs[0].H < 220 {
		t.Fatalf("nine rooms too small: %.1fx%.1f", rs[0].W, rs[0].H)
	}
	var area float32
	for _, r := range rs {
		area += r.W * r.H
	}
	if ratio := area / (1440 * 800); ratio < 0.85 {
		t.Fatalf("nine 16:9 rooms should fill most of the wall, got %.2f", ratio)
	}
}

func TestLayoutPortraitKeepsAspect(t *testing.T) {
	const portrait = 9.0 / 16.0
	rs := layoutN(3, portrait, 1440, 800, 12)
	assertRects(t, rs, 3, portrait, 1440, 800)
}

func TestLayoutMixedAspectKeepsEveryAspect(t *testing.T) {
	items := []Item{
		{Key: "a", Aspect: 16.0 / 9.0},
		{Key: "b", Aspect: 9.0 / 16.0},
		{Key: "c", Aspect: 4.0 / 3.0},
		{Key: "d", Aspect: 16.0 / 9.0},
	}
	rs := Layout(items, 1280, 900, 12)
	assertRects(t, rs, len(items), 0, 1280, 900)
	for i, r := range rs {
		if got := r.W / r.H; math.Abs(float64(got-items[i].Aspect)) > 0.01 {
			t.Fatalf("item %d aspect %.4f, want %.4f", i, got, items[i].Aspect)
		}
	}
}

func TestLayoutSmallWindowStillFits(t *testing.T) {
	rs := layoutN(9, DefaultAspect, 640, 360, 8)
	assertRects(t, rs, 9, DefaultAspect, 640, 360)
}

func TestLayoutZeroAndInvalidAspect(t *testing.T) {
	items := []Item{{Key: "a", Aspect: 0}, {Key: "b", Aspect: -1}}
	rs := Layout(items, 1000, 600, 12)
	assertRects(t, rs, 2, DefaultAspect, 1000, 600)
}

func TestLayoutCap(t *testing.T) {
	rs := layoutN(MaxRooms+3, DefaultAspect, 1440, 800, 12)
	if len(rs) != MaxRooms {
		t.Fatalf("layout returned %d rects, want cap %d", len(rs), MaxRooms)
	}
}

func layoutN(n int, aspect, w, h, gap float32) []Rect {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Key: fmt.Sprint(i), Aspect: aspect}
	}
	return Layout(items, w, h, gap)
}

func assertRects(t *testing.T, rs []Rect, n int, aspect, w, h float32) {
	t.Helper()
	if len(rs) != n {
		t.Fatalf("got %d rects, want %d", len(rs), n)
	}
	const eps = 0.6
	for i, r := range rs {
		if r.W <= 0 || r.H <= 0 {
			t.Fatalf("rect %d empty: %+v", i, r)
		}
		if r.X < -eps || r.Y < -eps || r.X+r.W > w+eps || r.Y+r.H > h+eps {
			t.Fatalf("rect %d outside container: %+v", i, r)
		}
		if aspect > 0 {
			if got := r.W / r.H; math.Abs(float64(got-aspect)) > 0.01 {
				t.Fatalf("rect %d aspect %.4f, want %.4f", i, got, aspect)
			}
		}
		for j := range i {
			if overlap(rs[j], r) > eps {
				t.Fatalf("rect %d overlaps %d: %+v %+v", i, j, rs[j], r)
			}
		}
		if i > 0 && aspect > 0 {
			// 行优先：y 不回退；同一行内 x 递增。混合比例下同一行的
			// 画面垂直居中，y 本来就可能不同，跳过顺序检查。
			if r.Y < rs[i-1].Y-eps {
				t.Fatalf("reading order broken at %d: y %.1f -> %.1f", i, rs[i-1].Y, r.Y)
			}
			if math.Abs(float64(r.Y-rs[i-1].Y)) < eps && r.X <= rs[i-1].X {
				t.Fatalf("reading order broken in row at %d: x %.1f -> %.1f", i, rs[i-1].X, r.X)
			}
		}
	}
}

func overlap(a, b Rect) float32 {
	w := min(a.X+a.W, b.X+b.W) - max(a.X, b.X)
	h := min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}
