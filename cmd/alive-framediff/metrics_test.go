package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// 仪器的自检：合成一张有横竖结构（但没有重复纹理）的图，按已知向量平移，
// 比对工具必须把那个向量原样量回来。
//
// 这和 internal/ui 的 TestProbeCatchesShiftAndFlash 是同一种东西：红了说明
// 工具本身失灵，后面所有「没有位移」的绿色都不作数。
func TestCompareMeasuresKnownShift(t *testing.T) {
	const w, h = 160, 120
	base := pattern(w, h)
	for _, tc := range []struct{ dx, dy int }{
		{0, 0}, {0, 18}, {0, -18}, {7, -3}, {-12, 5}, {23, 0}, {-24, 24},
	} {
		shifted := translate(base, w, h, tc.dx, tc.dy)
		m := compare(base, shifted, w, h, rect{}, 24, 8)
		if m.DX != tc.dx || m.DY != tc.dy {
			t.Errorf("位移 (%+d,%+d) 量成了 (%+d,%+d)", tc.dx, tc.dy, m.DX, m.DY)
		}
		if tc.dx == 0 && tc.dy == 0 {
			if m.MeanAbs != 0 || m.Changed != 0 {
				t.Errorf("同一张图：平均差 %.3f、变化像素 %d，应该是 0 和 0", m.MeanAbs, m.Changed)
			}
			continue
		}
		// 平移过的图，未对齐的平均差应该很大——不然「阈值外的位移」就漏了。
		if m.MeanAbs < 5 {
			t.Errorf("平移 (%+d,%+d) 后未对齐平均差只有 %.3f，太小：比对没看见内容挪了", tc.dx, tc.dy, m.MeanAbs)
		}
		// 对齐之后应该几乎一样（只有被平移出画面的边条不同）。
		if m.AlignedMeanAbs > 1 {
			t.Errorf("平移 (%+d,%+d) 对齐后平均差 %.3f，应该接近 0", tc.dx, tc.dy, m.AlignedMeanAbs)
		}
	}
}

// 一格像素的位移就是用户能看见的最小「跳」，容差 1 px 时它必须命中。
func TestCompareCatchesOnePixelShift(t *testing.T) {
	const w, h = 80, 60
	base := pattern(w, h)
	m := compare(base, translate(base, w, h, 0, 1), w, h, rect{}, 8, 8)
	if abs(m.DY) < 1 {
		t.Fatalf("1 px 的位移没量出来：(%+d,%+d)", m.DX, m.DY)
	}
	// 一像素的位移也要被逐像素差看见（否则只剩位移这一条判据）。
	if m.Changed == 0 {
		t.Error("1 px 位移后没有一个像素超过阈值")
	}
}

// 位置没变、只是某块颜色变了：位移必须是 0，像素差要非 0。
// 这就是「闪」和「跳」的分界，也是这张表两条判据各自的意义。
func TestCompareSeparatesFlashFromShift(t *testing.T) {
	const w, h = 80, 60
	base := pattern(w, h)
	altered := append([]byte(nil), base...)
	// 中间一块整块改色：位置不动。
	for y := 20; y < 40; y++ {
		for x := 10; x < 50; x++ {
			o := y*w*4 + x*4
			altered[o], altered[o+1], altered[o+2] = 255, 0, 255
		}
	}
	m := compare(base, altered, w, h, rect{}, 8, 8)
	if m.DX != 0 || m.DY != 0 {
		t.Errorf("只改颜色不该量出位移，得到 (%+d,%+d)", m.DX, m.DY)
	}
	if m.Changed == 0 {
		t.Error("整块改色没有被逐像素差抓到")
	}
	if want := 800; m.Changed != want {
		t.Errorf("改色像素数 %d，期望 %d", m.Changed, want)
	}
}

// 单级量化误差（每通道 +3）不算「变了」：-pix 8 的意义就在这里，否则任何一个
// 8bit 舍入差都会把整帧记成闪。
func TestCompareIgnoresSubThresholdNoise(t *testing.T) {
	const w, h = 40, 30
	base := pattern(w, h)
	noisy := append([]byte(nil), base...)
	for i := 0; i < len(noisy); i += 4 {
		for c := 0; c < 3; c++ {
			if noisy[i+c] <= 250 {
				noisy[i+c] += 3
			}
		}
	}
	m := compare(base, noisy, w, h, rect{}, 8, 8)
	if m.Changed != 0 {
		t.Errorf("每通道 +3 的差被当成 %d 个变化像素，-pix 8 没起作用", m.Changed)
	}
	if m.MeanAbs < 1 || m.MeanAbs > 3.5 {
		t.Errorf("平均差 %.3f，期望 ~3（这是真实存在的差，只是够不上像素判据）", m.MeanAbs)
	}
}

// pattern 造一张既有竖边又有横边的图（值不重复，避免纹理重复把偏移带偏）。
func pattern(w, h int) []byte {
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := (x*37 + y*101 + (x*y*7)%53) % 256
			o := y*w*4 + x*4
			pix[o+0] = byte(v)
			pix[o+1] = byte((v * 3) % 251)
			pix[o+2] = byte((v + 97) % 241)
			pix[o+3] = 255
		}
	}
	return pix
}

// translate 把 base 的内容挪 (dx,dy)：新图 (x,y) = 原图 (x-dx, y-dy)，外面填 0。
func translate(base []byte, w, h, dx, dy int) []byte {
	out := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		sy := y - dy
		if sy < 0 || sy >= h {
			continue
		}
		for x := 0; x < w; x++ {
			sx := x - dx
			if sx < 0 || sx >= w {
				continue
			}
			copy(out[y*w*4+x*4:y*w*4+x*4+4], base[sy*w*4+sx*4:sy*w*4+sx*4+4])
		}
	}
	return out
}

// writeSeq 按 manifest / frames.bgra / frames.idx 的格式写一段最小录制。
// 这是与 record_e2e.go 共享的格式约定，两边各自实现一遍，对不上就是格式漂了。
func writeSeq(t *testing.T, dir string, w, h int, frames [][]byte, timesUs []int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := fmt.Sprintf(`{"tool":"alive-record","version":1,"format":"bgra-raw","pixel":"BGRA",
		"width":%d,"height":%d,"frame_bytes":%d}`, w, h, w*h*4)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "frames.bgra"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.Create(filepath.Join(dir, "frames.idx"))
	if err != nil {
		t.Fatal(err)
	}
	for i, fr := range frames {
		if _, err := f.Write(fr); err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(idx, "%d\t%d\tgpu\n", i, timesUs[i])
	}
	_ = f.Close()
	_ = idx.Close()
}

func TestOpenSequenceReadsRecordedFormat(t *testing.T) {
	const w, h = 4, 3
	dir := t.TempDir()
	f0 := pattern(w, h)
	f1 := translate(f0, w, h, 0, 1)
	writeSeq(t, dir, w, h, [][]byte{f0, f1}, []int64{0, 16667})

	s, err := OpenSequence(dir)
	if err != nil {
		t.Fatalf("打不开自己写的录制：%v", err)
	}
	defer func() { _ = s.Close() }()
	if s.Width != w || s.Height != h || s.Count() != 2 {
		t.Fatalf("读出来 %dx%d %d 帧，期望 %dx%d 2 帧", s.Width, s.Height, s.Count(), w, h)
	}
	got, err := s.Frame(1)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f1 {
		if got[i] != f1[i] {
			t.Fatalf("第 1 帧第 %d 个字节是 %d，期望 %d", i, got[i], f1[i])
		}
	}
	if m := compare(f0, got, w, h, rect{}, 2, 8); m.DY != 1 {
		t.Errorf("落盘再读回来之后位移量成了 %+d，期望 +1", m.DY)
	}
}

// 两次录制各自从自己的第一帧起算时刻，所以按时间戳配对是可用的。
func TestAlignPairsByTime(t *testing.T) {
	const w, h = 4, 3
	dirA, dirB := t.TempDir(), t.TempDir()
	f := pattern(w, h)
	// A：0/16/33ms；B：0/17/34ms（采样抖动）+ 多一帧 50ms
	writeSeq(t, dirA, w, h, [][]byte{f, f, f}, []int64{0, 16000, 33000})
	writeSeq(t, dirB, w, h, [][]byte{f, f, f, f}, []int64{0, 17000, 34000, 50000})
	a, err := OpenSequence(dirA)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := OpenSequence(dirB)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()

	o := options{align: "time", timeTol: 8}
	pairs, err := alignPairs(o, a, b)
	if err != nil {
		t.Fatal(err)
	}
	want := []pair{{0, 0}, {1, 1}, {2, 2}}
	if len(pairs) != len(want) {
		t.Fatalf("配上 %d 对，期望 %d 对：%v", len(pairs), len(want), pairs)
	}
	for i := range want {
		if pairs[i] != want[i] {
			t.Errorf("第 %d 对是 %v，期望 %v", i, pairs[i], want[i])
		}
	}

	// 容许时刻差小于两段之间的抖动（1ms）时，只剩两边精确对齐的第 0 帧能配上，
	// 后面两帧宁可丢掉也不「勉强配一个」。
	o.timeTol = 0
	pairs, err = alignPairs(o, a, b)
	if err != nil {
		t.Fatalf("time-tol=0 时第 0 帧应该还能配上：%v", err)
	}
	if len(pairs) != 1 || pairs[0] != (pair{0, 0}) {
		t.Errorf("time-tol=0 配上 %v，期望只有 {0,0}", pairs)
	}

	// index 模式就是同号帧，多出来的帧不算。
	o = options{align: "index"}
	pairs, err = alignPairs(o, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 3 || pairs[2] != (pair{2, 2}) {
		t.Errorf("index 配对是 %v，期望 3 对且最后一对是 {2,2}", pairs)
	}
}

func TestNearest(t *testing.T) {
	ts := []int64{0, 16000, 33000}
	for _, tc := range []struct {
		at   int64
		tol  int64
		want int
	}{
		{0, 1000, 0}, {15000, 8000, 1}, {15000, 500, -1}, {32000, 8000, 2}, {999999, 8000, -1},
	} {
		if got := nearest(ts, tc.at, tc.tol); got != tc.want {
			t.Errorf("nearest(%d, ±%d) = %d，期望 %d", tc.at, tc.tol, got, tc.want)
		}
	}
}
