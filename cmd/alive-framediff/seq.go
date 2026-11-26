package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// 两代落盘格式的 codec 名。老的录制没有 codec 字段，按 bgra-raw 认——这是
// alive-framediff 必须继续读得动已经录好的那几段的原因。
const (
	codecRaw  = "bgra-raw"
	codecZstd = "zstd"
)

// FrameMeta 是一帧的元数据，来自序列目录里的 frames.idx：序号、相对第一帧
// 的时刻（微秒）、这一帧是怎么来的（"gpu" = D3D11 渲染器画完、Present
// 之前从后缓冲回读的），以及帧数据在文件里的位置。
//
// bgra-raw 的录制没有位置信息（第 i 帧在 i*FrameBytes），那两列是 0。
type FrameMeta struct {
	Index int
	At    int64 // 微秒
	Path  string
	Off   int64 // 帧在数据文件里的起始偏移
	Size  int   // 落盘字节数（压缩后）
}

// Sequence 是一段录制：manifest.json + 帧数据文件 + frames.idx。
//
// 帧是紧凑的 BGRA（每行 Width*4 字节）。两种容器都是随机的：
// bgra-raw 第 i 帧直接按 i*FrameBytes 读；zstd 每帧一段独立的 zstd 帧，
// 按索引里的 offset/size 读出来解一段。解码后的像素和 raw 一模一样，
// 所以比对逻辑一行都不用知道压缩这回事。
type Sequence struct {
	Dir        string
	Width      int
	Height     int
	FrameBytes int
	Codec      string
	Meta       []FrameMeta
	file       *os.File
	dec        *zstd.Decoder
	buf        []byte // 解码/读出的一帧
	comp       []byte // 压缩后那一帧的原始字节（只有 zstd 用）
}

// manifest 是 alive-record 写在序列目录里的东西。
type manifest struct {
	Tool       string `json:"tool"`
	Version    int    `json:"version"`
	Format     string `json:"format"`
	Codec      string `json:"codec"`
	Pixel      string `json:"pixel"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	FrameBytes int    `json:"frame_bytes"`
	FramesFile string `json:"frames_file"`
	Started    string `json:"started"`
}

// OpenSequence 读一个录制目录。
func OpenSequence(dir string) (*Sequence, error) {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("%s: 读不到 manifest.json（不是一个录制目录？）: %w", dir, err)
	}
	var m manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: manifest.json 坏了: %w", dir, err)
	}
	codec, err := manifestCodec(dir, m)
	if err != nil {
		return nil, err
	}
	if m.Width <= 0 || m.Height <= 0 {
		return nil, fmt.Errorf("%s: manifest 里的尺寸是 %dx%d", dir, m.Width, m.Height)
	}
	fb := m.Width * m.Height * 4
	if m.FrameBytes != 0 && m.FrameBytes != fb {
		return nil, fmt.Errorf("%s: frame_bytes=%d 和 %dx%d 对不上", dir, m.FrameBytes, m.Width, m.Height)
	}

	meta, err := readIndex(filepath.Join(dir, "frames.idx"))
	if err != nil {
		return nil, err
	}
	dataFile := m.FramesFile
	if dataFile == "" {
		dataFile = "frames.bgra"
	}
	f, err := os.Open(filepath.Join(dir, dataFile))
	if err != nil {
		return nil, fmt.Errorf("%s: 读不到 %s: %w", dir, dataFile, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if codec == codecRaw {
		// 进程被杀在半帧上时，索引里可能多一条：只认磁盘上完整的帧。
		if have := int(st.Size() / int64(fb)); have < len(meta) {
			meta = meta[:have]
		}
	} else {
		// 压缩帧的长度是逐帧自己记的，所以「完整」要按 offset+size 判；
		// 先看索引里到底有没有这两列——没有的话不是「帧不完整」，是格式不对，
		// 报明白比让后面逐帧取的时候撞一个越界偏移好。
		for i := range meta {
			if meta[i].Size <= 0 {
				_ = f.Close()
				return nil, fmt.Errorf("%s: frames.idx 第 %d 条没有 offset/size 两列（codec=%s 必须有）",
					dir, i, codec)
			}
		}
		// 半帧只会出现在尾部，从后往前削。
		for len(meta) > 0 {
			last := meta[len(meta)-1]
			if last.Off+int64(last.Size) <= st.Size() {
				break
			}
			meta = meta[:len(meta)-1]
		}
	}
	if len(meta) == 0 {
		_ = f.Close()
		if codec == codecRaw {
			return nil, fmt.Errorf("%s: 没有完整的一帧（%d 字节）", dir, st.Size())
		}
		return nil, fmt.Errorf("%s: 没有完整的一帧（%d 字节，%s）", dir, st.Size(), codec)
	}
	s := &Sequence{Dir: dir, Width: m.Width, Height: m.Height, FrameBytes: fb, Codec: codec, Meta: meta, file: f}
	if codec == codecZstd {
		s.dec, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("%s: 建 zstd 解码器: %w", dir, err)
		}
	}
	return s, nil
}

// manifestCodec 定这段录制用的是什么编码，并且要求 manifest 自洽：
// 老录制只有 format=bgra-raw；新录制 format=bgra-zstd + codec=zstd。
func manifestCodec(dir string, m manifest) (string, error) {
	codec := m.Codec
	if codec == "" {
		codec = m.Format // 老格式里 format 就是 codec
	}
	switch codec {
	case codecRaw:
		if m.Format != "" && m.Format != "bgra-raw" {
			return "", fmt.Errorf("%s: format=%q 但 codec 是 bgra-raw", dir, m.Format)
		}
		return codecRaw, nil
	case codecZstd:
		return codecZstd, nil
	}
	return "", fmt.Errorf("%s: 不认识的格式（format=%q codec=%q，只认 bgra-raw 与 zstd）", dir, m.Format, m.Codec)
}

// readIndex 读 frames.idx。`#` 开头的是收尾行，跳过。
//
// 三列是老录制（序号、微秒、来源）；四列以上多出的是帧在数据文件里的位置。
func readIndex(path string) ([]FrameMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("读不到 frames.idx: %w", err)
	}
	defer func() { _ = f.Close() }()
	var out []FrameMeta
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s: 坏行 %q", path, line)
		}
		idx, err1 := strconv.Atoi(fields[0])
		at, err2 := strconv.ParseInt(fields[1], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("%s: 坏行 %q", path, line)
		}
		if idx != len(out) {
			return nil, fmt.Errorf("%s: 帧号不连续：第 %d 条写着 %d", path, len(out), idx)
		}
		p := "gpu"
		if len(fields) > 2 {
			p = fields[2]
		}
		var off int64
		var size int
		if len(fields) > 4 {
			off, err1 = strconv.ParseInt(fields[3], 10, 64)
			size, err2 = strconv.Atoi(fields[4])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("%s: 坏行 %q", path, line)
			}
		} else if len(fields) == 4 {
			return nil, fmt.Errorf("%s: 坏行 %q（位置要两列：offset size）", path, line)
		}
		out = append(out, FrameMeta{Index: idx, At: at, Path: p, Off: off, Size: size})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// Count 是可比的帧数。
func (s *Sequence) Count() int { return len(s.Meta) }

// Frame 把第 i 帧读进 s.buf 并返回它（BGRA，每行 Width*4 字节）。
// 返回的切片下一次调用就失效。
func (s *Sequence) Frame(i int) ([]byte, error) {
	if i < 0 || i >= len(s.Meta) {
		return nil, fmt.Errorf("帧号 %d 越界（0..%d）", i, len(s.Meta)-1)
	}
	n := s.FrameBytes
	if cap(s.buf) < n {
		s.buf = make([]byte, n)
	}
	buf := s.buf[:n]
	if s.Codec == codecRaw {
		if _, err := s.file.ReadAt(buf, int64(i)*int64(n)); err != nil {
			return nil, fmt.Errorf("%s 第 %d 帧: %w", s.Dir, i, err)
		}
		return buf, nil
	}
	meta := s.Meta[i]
	if meta.Size <= 0 {
		return nil, fmt.Errorf("%s 第 %d 帧: 索引里没有位置（offset=%d size=%d，manifest 说是 %s）",
			s.Dir, i, meta.Off, meta.Size, s.Codec)
	}
	if cap(s.comp) < meta.Size {
		s.comp = make([]byte, meta.Size)
	}
	comp := s.comp[:meta.Size]
	if _, err := s.file.ReadAt(comp, meta.Off); err != nil {
		return nil, fmt.Errorf("%s 第 %d 帧: %w", s.Dir, i, err)
	}
	// 每帧一段独立的 zstd 帧，解出来必须是整整一帧：长度不对就是错位的索引
	// 或坏数据，不能拿去比。
	dec, err := s.dec.DecodeAll(comp, buf[:0])
	if err != nil {
		return nil, fmt.Errorf("%s 第 %d 帧（offset=%d size=%d）: 解压: %w", s.Dir, i, meta.Off, meta.Size, err)
	}
	if len(dec) != n {
		return nil, fmt.Errorf("%s 第 %d 帧: 解出来 %d 字节，这一帧应该有 %d", s.Dir, i, len(dec), n)
	}
	s.buf = dec
	return dec, nil
}

// Close 关掉序列。
func (s *Sequence) Close() error {
	if s.dec != nil {
		s.dec.Close()
	}
	return s.file.Close()
}
