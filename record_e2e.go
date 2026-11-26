//go:build e2e

// 真窗口逐帧录制：e2e 构建里按 ALIVE_RECORD=<dir> 把窗口**画出来的每一帧**
// 落成可直接比对的序列（每帧一段 zstd + manifest + 逐帧索引），供
// cmd/alive-framediff 两段序列逐帧比对。
//
//	ALIVE_RECORD=.tmp/rec/a ALIVE_RECORD_SECONDS=3 build/e2e/alive-e2e.exe
//
// 帧从哪儿来：`ui.FrameSink`（third_party/mygo/ui/record.go），D3D11 渲染器
// 把 swapchain 后缓冲在 Present 之前回读给出来——真 GPU 光栅化的结果，含真
// 字形/图片/mpv 纹理与 GPU 合成。离屏的 `ui.Tester` 帧探针和
// `mygo-automation screenshot`（windowHost.capture 用 CPU raster 重画最后一帧）
// 都拿不到这一层，见 doc/reference/frame-recording.md。
//
// 为什么压缩：raw BGRA 是 2138×1339×4 = 11.45 MB/帧，60fps 就是 690 MB/s。
// 队列只有 8 格，写盘跟不上就背压到渲染主线程，帧率跟着掉。每帧独立压成一段
// zstd 帧之后，界面帧只剩 ~0.08 MB（实测 128–148×），贴满视频的播放页 6.1 MB
// （1.8×）——写盘量降下来，队列才有余量。无损，逐帧独立，framediff 仍能随机
// 访问第 i 帧（offset/size 在 frames.idx 里）。
//
// 只在 e2e 构建里存在：正式二进制里既没有这个文件，也没有 ui.FrameSink 的
// 赋值者（sink 为 nil 时每帧只多一次指针判空）。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/klauspost/compress/zstd"
)

const (
	// dataFile 是压缩后帧数据的文件名。老格式（manifest 里 format=bgra-raw）
	// 用的是 frames.bgra，读端两种都认。
	dataFile = "frames.zst"
	// codecName 写进 manifest 的 codec 字段。
	codecName = "zstd"
)

// recordJob 是一帧待落盘的画面。buf 是紧凑排布的 BGRA（每行 width*4 字节）；
// comp 是压完的那一段 zstd 帧，由压缩 goroutine 填。
type recordJob struct {
	index int
	at    time.Duration
	buf   []byte
	comp  []byte
}

// frameRecorder 把 ui.FrameSink 交来的帧写进 dir：
//
//	manifest.json  尺寸 / 像素格式 / codec / 起点（第一帧才写，尺寸那时才知道）
//	frames.zst     每帧一段独立的 zstd 帧，逐帧顺序紧排（偏移见 frames.idx）
//	frames.idx     每帧一行：序号、相对第一帧的时刻（微秒）、来源、偏移、压缩后字节数
//	record.log     录制过程的纪要（这是 windowsgui 进程，没有控制台）
//
// 分工：frame 在渲染主线程上只做一次 memcpy 再投递（和加压缩之前逐字相同，
// 一个字节都没多）；压缩在 workers 条 goroutine 里；只有 writeLoop 那一条碰盘。
// 编码/落盘慢不能拖住渲染，否则录出来的不是用户看到的帧率。队列满了就阻塞
// 主线程——宁可掉帧率，也不悄悄丢帧："每一帧"是这条工具的全部意义。
//
// 为什么不是「writeLoop 一条 goroutine 里压」：单条压一帧界面要 2.8 ms（60fps
// 的预算 16.7 ms，绰绰有余），压一帧贴满视频的播放页要 37.9 ms——只够 26 fps。
// 播放页恰恰是常被录的地方，所以压这一步按 CPU 核数分成 workers 条（本机
// 8C/16T → 4 条，实测把 37.9 ms/帧摊成 ~9.5 ms/帧）。排序仍由 writeLoop 一个人
// 负责：压缩结果乱序回来，按序号重排后再顺序写。实测数据见
// doc/reference/frame-recording.md「压缩：每帧一段 zstd」。
type frameRecorder struct {
	dir   string
	limit time.Duration

	// 下面这些只有主线程（frame）和收尾时的 stop 碰。
	begun   bool
	stopped bool
	t0      time.Time
	w, h    int
	frameB  int
	frames  int

	// encoders 每条压缩 goroutine 一份：EncodeAll 是「一次调用只跑一条
	// goroutine」，共享一个 Encoder 实例只会取到同一个内部编码器、把调用串行化。
	encoders []*zstd.Encoder
	workers  int

	queue   chan recordJob // 渲染主线程 → 压缩
	results chan recordJob // 压缩 → 写盘（乱序）
	free    chan []byte    // 回收的像素缓冲
	spare   chan []byte    // 回收的压缩输出缓冲
	done    chan struct{}
	why     string // 收尾原因，close(queue) 之前写，writeLoop 在队列结束后读
}

var recorder *frameRecorder

func init() {
	dir := os.Getenv("ALIVE_RECORD")
	if dir == "" {
		return
	}
	limit := 5 * time.Second
	if v := os.Getenv("ALIVE_RECORD_SECONDS"); v != "" {
		if s, err := strconv.ParseFloat(v, 64); err == nil && s > 0 {
			limit = time.Duration(s * float64(time.Second))
		}
	}
	n := compressWorkers()
	recorder = &frameRecorder{
		dir:     dir,
		limit:   limit,
		workers: n,
		// 在途帧的上限 = 队列 + 压缩中 + 待重排 + 写。容量只决定能回收多少块
		// 11.45 MB 的缓冲，不预分配：队列满之前不会多占内存。
		queue:   make(chan recordJob, 8),
		results: make(chan recordJob, n),
		free:    make(chan []byte, 8+2*n),
		spare:   make(chan []byte, 8+2*n),
		done:    make(chan struct{}),
	}
	ui.FrameSink = recorder.frame
}

// compressWorkers 定压缩 goroutine 的条数。
//
// 依据是实测（2026-11-26，Ryzen 7 9700X，2138×1339）：zstd SpeedFastest 压
// 界面帧 2.8 ms（4094 MB/s）、压贴满视频的播放页 37.9 ms（302 MB/s，1.8×）。
// 60fps 的预算是 16.7 ms/帧，所以播放页至少要 3 条才跟得上；取 4 条留四成
// 余量，同时把 8 个物理核里的 4 个留在渲染/系统那边。
func compressWorkers() int {
	n := runtime.NumCPU() / 4
	if n < 2 {
		n = 2
	}
	if n > 4 {
		n = 4
	}
	return n
}

// frame 在渲染主线程上被调用。只做复制与投递，不碰磁盘、不碰压缩。
func (r *frameRecorder) frame(p ui.PresentedFrame) {
	if p.Width <= 0 || p.Height <= 0 || len(p.Pix) == 0 {
		return
	}
	if r.stopped {
		return
	}
	if !r.begun {
		if err := r.begin(p); err != nil {
			r.stopped = true
			_, _ = os.Stderr.WriteString("alive-record: " + err.Error() + "\n")
			return
		}
	}
	if r.limit > 0 && time.Since(r.t0) > r.limit {
		r.stop("limit")
		return
	}
	index, at := r.frames, time.Since(r.t0)
	r.frames++

	// 行距可能比 width*4 大（回读的 staging 纹理按对齐排），落盘要紧凑。
	var buf []byte
	select {
	case buf = <-r.free:
	default:
	}
	if cap(buf) < r.frameB {
		buf = make([]byte, r.frameB)
	}
	buf = buf[:r.frameB]
	row := p.Width * 4
	for y := 0; y < p.Height; y++ {
		copy(buf[y*row:(y+1)*row], p.Pix[y*p.Stride:y*p.Stride+row])
	}
	r.queue <- recordJob{index: index, at: at, buf: buf}
}

// begin 建目录、写 manifest（第一帧才知道尺寸）、起压缩器与写盘 goroutine。
func (r *frameRecorder) begin(p ui.PresentedFrame) error {
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return err
	}
	encs := make([]*zstd.Encoder, r.workers)
	for i := range encs {
		enc, err := zstd.NewWriter(nil,
			zstd.WithEncoderLevel(zstd.SpeedFastest),
			zstd.WithEncoderConcurrency(1),
		)
		if err != nil {
			for _, e := range encs[:i] {
				_ = e.Close()
			}
			return err
		}
		encs[i] = enc
	}
	f, err := os.Create(filepath.Join(r.dir, dataFile))
	if err != nil {
		r.closeEncoders(encs)
		return err
	}
	idx, err := os.Create(filepath.Join(r.dir, "frames.idx"))
	if err != nil {
		_ = f.Close()
		r.closeEncoders(encs)
		return err
	}
	lg, err := os.Create(filepath.Join(r.dir, "record.log"))
	if err != nil {
		_ = f.Close()
		_ = idx.Close()
		r.closeEncoders(encs)
		return err
	}
	// 第一帧里有多少像素不透明：ALIVE 的窗口是不透明的（d3d11.New），全 255；
	// 有半透明说明窗口走的是 DirectComposition（premultiplied），记下来，
	// 免得把「抓到的是不是用户看到的那一层」变成一句口头结论。
	// 顺带它也是压缩比的解释：alpha 恒 255 的那一列不占熵。
	nonOpaque := 0
	for i := 3; i < len(p.Pix); i += 4 {
		if p.Pix[i] != 0xFF {
			nonOpaque++
		}
	}
	man := map[string]any{
		"tool":                     "alive-record",
		"version":                  2,
		"format":                   "bgra-zstd",
		"codec":                    codecName,
		"codec_note":               "每帧一段独立的 zstd 帧（SpeedFastest），不是连续流；offset/size 见 frames.idx",
		"pixel":                    "BGRA",
		"alpha":                    "premultiplied",
		"origin":                   "top-left",
		"width":                    p.Width,
		"height":                   p.Height,
		"stride":                   p.Width * 4,
		"frame_bytes":              p.Width * p.Height * 4,
		"frames_file":              dataFile,
		"index_note":               "每帧一行：序号、相对第一帧的微秒、来源、帧在 frames_file 里的字节偏移、压缩后字节数",
		"seq_at_start":             p.Seq,
		"first_frame_nonopaque_px": nonOpaque,
		"started":                  time.Now().Format(time.RFC3339Nano),
		"limit_ms":                 int64(r.limit / time.Millisecond),
	}
	b, _ := json.MarshalIndent(man, "", "  ")
	if err := os.WriteFile(filepath.Join(r.dir, "manifest.json"), append(b, '\n'), 0o644); err != nil {
		_ = f.Close()
		_ = idx.Close()
		_ = lg.Close()
		r.closeEncoders(encs)
		return err
	}
	r.begun, r.t0 = true, time.Now()
	r.w, r.h, r.frameB = p.Width, p.Height, p.Width*p.Height*4
	r.encoders = encs
	_, _ = fmt.Fprintf(lg, "record: %dx%d BGRA -> %s (zstd fastest, %d workers), seq at start %d, nonopaque %d px, limit %v\n",
		p.Width, p.Height, dataFile, r.workers, p.Seq, nonOpaque, r.limit)
	go r.writeLoop(f, idx, lg)
	return nil
}

func (r *frameRecorder) closeEncoders(encs []*zstd.Encoder) {
	for _, e := range encs {
		_ = e.Close()
	}
}

// compressLoop 压它自己那一份编码器，压完投给写盘。队列关了才退出。
func (r *frameRecorder) compressLoop(enc *zstd.Encoder, wg *sync.WaitGroup) {
	defer wg.Done()
	for job := range r.queue {
		var dst []byte
		select {
		case dst = <-r.spare:
		default:
		}
		job.comp = enc.EncodeAll(job.buf, dst[:0])
		r.results <- job
	}
}

// writeLoop 是唯一碰盘的地方：收乱序回来的压缩结果，按序号重排，顺序写帧、
// 追加索引、每 60 帧记一行纪要。
func (r *frameRecorder) writeLoop(f, idx, lg *os.File) {
	defer close(r.done)

	var wg sync.WaitGroup
	for _, enc := range r.encoders {
		wg.Add(1)
		go r.compressLoop(enc, &wg)
	}
	// 压缩全退出了才关 results，写盘循环才知道什么时候算完。
	go func() {
		wg.Wait()
		r.closeEncoders(r.encoders)
		close(r.results)
	}()

	var (
		off     int64 // 已经写进文件的字节数，也就是下一帧的 offset
		next    int   // 下一个该写的序号
		written int
		lastAt  time.Duration
		broken  bool
	)
	pending := make(map[int]recordJob, r.workers*2)

	emit := func(job recordJob) {
		if !broken {
			n, err := f.Write(job.comp)
			switch {
			case err != nil || n != len(job.comp):
				_, _ = fmt.Fprintf(lg, "%s\twrite frame %d: %v (wrote %d/%d)\n",
					time.Now().Format("15:04:05.000"), job.index, err, n, len(job.comp))
				// 从这里往后每一帧的 offset 都对不上了，索性不再追加：
				// 已经落盘的那一截仍是一段完整的、可用的录制。
				broken = true
			default:
				_, _ = fmt.Fprintf(idx, "%d\t%d\tgpu\t%d\t%d\n", job.index, job.at.Microseconds(), off, len(job.comp))
				off += int64(len(job.comp))
				written++
				lastAt = job.at
			}
		}
		select {
		case r.free <- job.buf:
		default:
		}
		select {
		case r.spare <- job.comp:
		default:
		}
		if job.index%60 == 0 && job.at > 0 {
			_, _ = fmt.Fprintf(lg, "%s\tframe %d at %v, %.1f fps\n", time.Now().Format("15:04:05.000"),
				job.index, job.at.Truncate(time.Millisecond), float64(job.index)/job.at.Seconds())
		}
	}

	for job := range r.results {
		pending[job.index] = job
		for {
			j, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			emit(j)
			next++
		}
	}
	// 压缩 goroutine 每条都处理完队列才退，所以正常情况这里一条不剩。
	if len(pending) > 0 {
		_, _ = fmt.Fprintf(lg, "record: %d 帧压完没写（缺序号 %d 之前的帧）\n", len(pending), next)
		broken = true
	}
	if broken {
		_ = f.Truncate(off) // 文件切到最后一条落盘成功的帧，和索引逐条对得上
	}
	if written > 0 {
		_, _ = fmt.Fprintf(lg, "record: end (%s), %d frames, last at %v, %.1f MB on disk\n",
			r.why, written, lastAt.Truncate(time.Millisecond), float64(off)/(1<<20))
	} else {
		_, _ = fmt.Fprintf(lg, "record: end (%s), no frames\n", r.why)
	}
	_, _ = fmt.Fprintf(idx, "# frames=%d why=%s\n", written, r.why)
	_ = f.Sync()
	_ = idx.Sync()
	_ = lg.Sync()
}

// stop 收尾：等队列写完（writeLoop 会落总数），不改任何已经落盘的帧。
func (r *frameRecorder) stop(why string) {
	if r.stopped {
		return
	}
	r.stopped, r.why = true, why
	close(r.queue)
	<-r.done
}
