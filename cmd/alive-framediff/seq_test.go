package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// 这一段测的是「两代容器读出来是同一批像素」：录制那边每帧压一段 zstd
// （record_e2e.go），比对这边必须解回和 bgra-raw 一模一样的东西，否则
// 「压前压后结论一样」就是句空话。
//
// 格式约定两边各自实现一遍（这里写、record_e2e.go 也写），对不上就是格式漂了。

// writeSeqZstd 按新格式写一段最小录制：manifest(codec=zstd) + frames.zst +
// 五列 frames.idx。压缩参数与 record_e2e.go 一致（每帧一段独立的 zstd 帧）。
func writeSeqZstd(t *testing.T, dir string, w, h int, frames [][]byte, timesUs []int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := fmt.Sprintf(`{"tool":"alive-record","version":2,"format":"bgra-zstd","codec":"zstd",
		"frames_file":"frames.zst","width":%d,"height":%d,"frame_bytes":%d}`, w, h, w*h*4)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enc.Close() }()
	f, err := os.Create(filepath.Join(dir, "frames.zst"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := os.Create(filepath.Join(dir, "frames.idx"))
	if err != nil {
		t.Fatal(err)
	}
	var off int64
	var comp []byte
	for i, fr := range frames {
		comp = enc.EncodeAll(fr, comp[:0])
		n, err := f.Write(comp)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fmt.Fprintf(idx, "%d\t%d\tgpu\t%d\t%d\n", i, timesUs[i], off, n)
		off += int64(n)
	}
	_, _ = fmt.Fprintf(idx, "# frames=%d why=test\n", len(frames))
	_ = f.Close()
	_ = idx.Close()
}

// 压缩容器读回来的像素必须和写进去的逐字节相同——这是整个无损压缩的地基。
func TestOpenSequenceReadsCompressedFormat(t *testing.T) {
	const w, h = 16, 12
	dir := t.TempDir()
	f0 := pattern(w, h)
	f1 := translate(f0, w, h, 0, 2)
	f2 := translate(f0, w, h, 3, -1)
	writeSeqZstd(t, dir, w, h, [][]byte{f0, f1, f2}, []int64{0, 16667, 33334})

	s, err := OpenSequence(dir)
	if err != nil {
		t.Fatalf("打不开压缩过的录制：%v", err)
	}
	defer func() { _ = s.Close() }()
	if s.Codec != codecZstd {
		t.Errorf("codec 读成了 %q，期望 %q", s.Codec, codecZstd)
	}
	if s.Width != w || s.Height != h || s.Count() != 3 {
		t.Fatalf("读出来 %dx%d %d 帧，期望 %dx%d 3 帧", s.Width, s.Height, s.Count(), w, h)
	}
	for i, want := range [][]byte{f0, f1, f2} {
		got, err := s.Frame(i)
		if err != nil {
			t.Fatalf("第 %d 帧: %v", i, err)
		}
		if len(got) != len(want) {
			t.Fatalf("第 %d 帧 %d 字节，期望 %d", i, len(got), len(want))
		}
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("第 %d 帧第 %d 个字节是 %d，期望 %d", i, k, got[k], want[k])
			}
		}
	}
}

// 同一批像素，一个存成 bgra-raw、一个存成 zstd：比对结果必须完全一样，
// 包括量出来的位移和平均差。
func TestCompressedAndRawGiveSameMetrics(t *testing.T) {
	const w, h = 48, 36
	rawDir, zstdDir := t.TempDir(), t.TempDir()
	f0 := pattern(w, h)
	frames := [][]byte{f0, translate(f0, w, h, 0, 1), translate(f0, w, h, 5, -3), translate(f0, w, h, 0, 6)}
	times := []int64{0, 16667, 33334, 50000}
	writeSeq(t, rawDir, w, h, frames, times)
	writeSeqZstd(t, zstdDir, w, h, frames, times)

	raw, err := OpenSequence(rawDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	zst, err := OpenSequence(zstdDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zst.Close() }()

	if raw.Codec != codecRaw || zst.Codec != codecZstd {
		t.Fatalf("两段的 codec 是 %q / %q", raw.Codec, zst.Codec)
	}
	ref, err := raw.Frame(0)
	if err != nil {
		t.Fatal(err)
	}
	refPix := append([]byte(nil), ref...)
	for _, pr := range []pair{{1, 1}, {2, 2}, {3, 3}} {
		prRaw, err := raw.Frame(pr.ia)
		if err != nil {
			t.Fatal(err)
		}
		prZst, err := zst.Frame(pr.ib)
		if err != nil {
			t.Fatal(err)
		}
		a := compare(refPix, prRaw, w, h, rect{}, 8, 8)
		b := compare(refPix, prZst, w, h, rect{}, 8, 8)
		if a != b {
			t.Errorf("第 %d 帧：raw 给 %+v，zstd 给 %+v —— 解出来的像素不一样", pr.ia, a, b)
		}
	}
	// 位移也得一样：这一帧是 +6 px。
	last, err := zst.Frame(3)
	if err != nil {
		t.Fatal(err)
	}
	if m := compare(refPix, last, w, h, rect{}, 8, 8); m.DY != 6 {
		t.Errorf("压缩容器里 +6 px 量成了 %+d", m.DY)
	}
}

// 进程被杀在压缩帧中间：索引里最后一条的 offset+size 超过了文件尾巴，
// 只认前面完整的帧，不要拿半帧去比。
func TestCompressedDropsTruncatedTail(t *testing.T) {
	const w, h = 8, 6
	dir := t.TempDir()
	f := pattern(w, h)
	writeSeqZstd(t, dir, w, h, [][]byte{f, f, f}, []int64{0, 16667, 33334})

	// 把这个序列的索引重写成「第三条的 size 撑到文件外面」：文件没动，
	// 等于第三条只落了一半。
	s, err := OpenSequence(dir)
	if err != nil {
		t.Fatal(err)
	}
	per := s.Meta[0].Size
	_ = s.Close()
	var comp []byte
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		t.Fatal(err)
	}
	comp = enc.EncodeAll(f, nil)
	_ = enc.Close()
	if len(comp) != per {
		t.Fatalf("同一帧压出来 %d 字节，索引里写着 %d", len(comp), per)
	}
	idx := fmt.Sprintf("0\t0\tgpu\t0\t%d\n1\t16667\tgpu\t%d\t%d\n2\t33334\tgpu\t%d\t%d\n# frames=3 why=test\n",
		per, per, per, 2*per, 4*per)
	if err := os.WriteFile(filepath.Join(dir, "frames.idx"), []byte(idx), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err = OpenSequence(dir)
	if err != nil {
		t.Fatalf("尾部半帧应该被丢掉而不是让整段读不出来：%v", err)
	}
	defer func() { _ = s.Close() }()
	if s.Count() != 2 {
		t.Errorf("完整帧 %d 条，期望 2 条（第三条 offset+size 超过文件尾巴）", s.Count())
	}
}

// codec=zstd 但索引只有三列（老格式的写法）时不能默默给个空帧：整段读不了，
// 而且要说清楚缺的是 offset/size，不是「没有完整的一帧」。
func TestCompressedWithoutPositionIsAnError(t *testing.T) {
	const w, h = 8, 6
	dir := t.TempDir()
	f := pattern(w, h)
	writeSeqZstd(t, dir, w, h, [][]byte{f}, []int64{0})
	idxPath := filepath.Join(dir, "frames.idx")
	if err := os.WriteFile(idxPath, []byte("0\t0\tgpu\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSequence(dir); err == nil {
		t.Fatal("索引里没有 offset/size 却打开成功了")
	} else if !strings.Contains(err.Error(), "offset/size") {
		t.Errorf("报错没点出缺的是 offset/size：%v", err)
	}
}

// 老录制（只有 format=bgra-raw，没有 codec 字段）必须继续读得动；不认识的
// 格式要直接报出来，不要猜。
func TestManifestCodec(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format string
		codec  string
		want   string
		bad    bool
	}{
		{"老录制只有 format", "bgra-raw", "", codecRaw, false},
		{"新录制两个都有", "bgra-zstd", "zstd", codecZstd, false},
		{"只有 codec", "", "bgra-raw", codecRaw, false},
		{"不认识的码子", "bgra-lz4", "lz4", "", true},
		{"自相矛盾", "bgra-zstd", "bgra-raw", "", true},
	} {
		got, err := manifestCodec("t", manifest{Format: tc.format, Codec: tc.codec})
		if tc.bad {
			if err == nil {
				t.Errorf("%s: 期望报错，得到 %q", tc.name, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: 认出 %q，期望 %q", tc.name, got, tc.want)
		}
	}
}
