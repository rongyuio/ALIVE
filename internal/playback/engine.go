// Package playback 用 libav 在进程内解码直播流，做成 MyGo 的媒体引擎。
//
// 为什么不再用 ffmpeg 子进程：
//
// 子进程方案把音视频分成两路（视频走 stdout 管道、音频走本地 TCP）。FFmpeg 写
// 两个输出是串行的，视频读得慢会反压整个进程，把音频一起饿死；PTS 还得从
// stderr 的 showinfo 文本里逐行解析。这些坑在进程内调用 libav 时全部消失：
// av_read_frame 给出的包自带正确时间戳，音视频共享同一个 demuxer，落在同一条
// 绝对时间轴上，同步是构造出来的，不需要任何对齐参数。
//
// 结构是三个 goroutine：
//
//	解封装（demux）  读包，按流分给两个包队列
//	音频解码         收包、解码、重采样成 48kHz 立体声 f32，写进 PCM 环
//	视频解码         收包、解码、转 NV12，写进帧槽
//
// 音频是主时钟。PCM 环被 MyGo 的 audio.Player 按 WASAPI 的实际出声节奏消费，
// 所以「音频播到哪儿」本身就是最准的时钟，视频跟着它挑帧。
//
// 反压只加在音频上：PCM 环满了，音频解码 goroutine 停下，解封装随之停下。
// 视频那条路永远不阻塞，槽满了丢最旧的帧。这是直播的正确语义：宁可丢一帧
// 画面，也不能让声音断。
package playback

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/egoist/mygo/audio"
	mygoMedia "github.com/egoist/mygo/media"

	"alive/internal/libav"
)

// ErrUnsupported 表示这条流无法自带解码。
var ErrUnsupported = errors.New("playback: 该流无法自带解码")

const (
	audioSampleRate = 48000
	audioChannels   = 2
	// pcmRingFrames 是 PCM 环的容量，4 秒。
	//
	// 这是整个播放器的缓冲深度，唯一的取值依据是实测的断供数据，不是感觉：
	//
	//	1 秒 → PCM 环饿死 30 次，最长空等 959ms，声音听得出来在断
	//	2 秒 → 饿死 27 次，最长空等 888ms，仍然断
	//
	// 原因是 HLS 分片是突发到达的：av_read_frame 实测会卡到 1.63 秒。环形缓冲
	// 在两次分片之间会掉到「容量减一个分片时长」，官方流的分片是 2 秒，所以
	// 容量必须明显大于 2 + 1.6 秒才够。
	//
	// 4 秒的代价是直播延迟多几秒——这是 HLS 本身的性质，浏览器同样如此。
	pcmRingFrames = 4 * audioSampleRate
	// videoSlots 是解码后视频帧的槽数。
	//
	// 每槽一整帧 NV12：1080p 3MB、1440p 5.5MB。十二槽是 37~66MB，够吸收一次
	// 分片突发，又不会把延迟堆高。槽满了丢最旧的帧，不阻塞解码。
	videoSlots = 12
	// packetQueue 是解码前压缩包的队列深度。压缩包很小，给足不影响延迟。
	packetQueue = 256
	// prebufferFrames 是出声前要攒够的音频时长，2 秒。
	//
	// 这段差额是断音的解药：播放从直播边缘开始，供给等于实时，缓冲攒不起来；
	// 先攒 2 秒再出声，此后遇到 2 秒以内的网络停顿都不会断。
	prebufferFrames = 2 * audioSampleRate
)

// slotState 是视频帧槽的状态。
type slotState uint8

const (
	slotEmpty slotState = iota
	slotWriting
	slotReady
	slotShowing
)

type slot struct {
	luma, chroma []byte
	pts          time.Duration
	state        slotState
}

// Engine 是一路直播的解码引擎，实现 media.Engine。
type Engine struct {
	ctx    context.Context
	cancel context.CancelFunc

	width, height int

	mu    sync.Mutex
	slots [videoSlots]slot
	wake  func()
	state mygoMedia.State
	err   error

	// 音频主时钟：起点 + 播放器已播出的时长。
	//
	// startPTS 是第一个能解码的视频帧（也就是关键帧）的时间戳，不是第一个音频帧的。
	// 直播流接上之后音频立刻流动，视频却要等下一个 IDR；实测斗鱼两者差三秒多。
	// 拿音频起点当时钟零点，画面就必须先干等这三秒——这正是首帧慢的真正原因。
	// 正确做法和 ffplay 一样：音频跳过关键帧之前的部分，两路都从关键帧起步。
	startPTS  time.Duration
	hasStart  bool
	audioBase time.Duration
	hasBase   bool

	// free 是槽位的信号量：每次取一个空槽，用完还回来。
	//
	// 视频解码靠它节流——槽取完了就阻塞等待，等呈现端把帧放回来。这正是
	// 「解码跟着呈现走」，也是和音频时钟对齐的关键：不节流的话解码会全速
	// 跑到前面几秒，槽里堆的全是未来的帧，反而把当前该显示的丢掉。
	free chan int

	// 没有音轨时退回视频墙钟：最早一帧的 PTS 与它到达的时刻。
	videoBase    time.Duration
	videoBaseAt  time.Time
	hasVideoBase bool
	pausedAt     time.Time

	volume float64
	muted  bool
	paused bool
	// wantPlay 是「用户希望在播」，由 Play/Pause 设置。
	// audioReady 是「预缓冲攒够了」。两个都真才真的出声。
	wantPlay   bool
	audioReady bool

	ring   *pcmRing
	player *audio.Player

	frames  uint64
	dropped uint64

	// 诊断：解封装每次读包阻塞了多久。这是判断断音到底卡在网络还是 CPU 的唯一依据。
	demuxWaits uint64
	demuxMax   time.Duration

	closed bool
	done   chan struct{}
}

// Options 是一次自带解码的选项。
type Options struct {
	Headers   []string
	Width     int
	Height    int
	FrameRate float64
}

// Open 打开一路直播并启动解码。返回时管道已经跑起来，首帧要等 Frame 给出。
func Open(ctx context.Context, upstream string, opts Options) (*Engine, error) {
	if upstream == "" {
		return nil, fmt.Errorf("%w: 上游地址为空", ErrUnsupported)
	}

	// 分片由 libav 自己按需取，和网页端完全一样：一条下载流、一次一片。
	//
	// 这里曾经套过一层本地分片代理（后台预取 6 片再供 libav 读），想用它吸收
	// 网络抖动。实测是反的：预取和 libav 正在读的那一片抢带宽，解封装一次卡到
	// 2 秒，12 秒里音频断 4~17 次。关掉之后同样的房间 0 断供。
	//
	// 抖动的正确吸收位置是播放端的环形缓冲（见 pcmRingFrames / prebufferFrames），
	// 不是输入端的二次搬运。
	dm, err := libav.OpenDemuxer(upstream, opts.Headers)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	info := dm.Info()
	if !info.HasVideo {
		dm.Close()
		return nil, fmt.Errorf("%w: 流里没有视频", ErrUnsupported)
	}

	runCtx, cancel := context.WithCancel(ctx)
	e := &Engine{
		ctx:    runCtx,
		cancel: cancel,

		width:  info.Width,
		height: info.Height,
		volume: 1,
		state:  mygoMedia.Loading,
		done:   make(chan struct{}),
	}
	if e.width <= 0 || e.height <= 0 {
		e.width, e.height = opts.Width, opts.Height
	}
	if e.width <= 0 || e.height <= 0 {
		dm.Close()
		cancel()
		return nil, fmt.Errorf("%w: 拿不到视频尺寸", ErrUnsupported)
	}
	e.free = make(chan int, videoSlots)
	for i := range e.slots {
		e.slots[i].luma = make([]byte, e.width*e.height)
		e.slots[i].chroma = make([]byte, e.width*e.height/2)
		e.free <- i
	}

	videoDec, err := dm.OpenVideoDecoder()
	if err != nil {
		dm.Close()
		cancel()
		return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	var audioDec *libav.Decoder
	if info.HasAudio {
		if ad, err := dm.OpenAudioDecoder(); err == nil {
			audioDec = ad
		}
	}
	if audioDec != nil {
		e.ring = newPCMRing(pcmRingFrames)
		e.player = audio.NewPlayer(e.ring)
		// 这里不立刻出声：等 PCM 环攒够再播，见 pushPCM。
		e.wantPlay = true
	}

	go e.run(dm, videoDec, audioDec)
	return e, nil
}

// run 是整条解码管线的总调度。
func (e *Engine) run(dm *libav.Demuxer, videoDec, audioDec *libav.Decoder) {
	defer close(e.done)
	defer func() {
		videoDec.Close()
		if audioDec != nil {
			audioDec.Close()
		}
		dm.Close()
	}()

	videoPkts := make(chan *libav.Packet, packetQueue)
	audioPkts := make(chan *libav.Packet, packetQueue)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		e.videoLoop(videoDec, videoPkts)
	}()
	if audioDec != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.audioLoop(audioDec, audioPkts)
		}()
	}
	defer func() {
		close(videoPkts)
		if audioDec != nil {
			close(audioPkts)
		}
		wg.Wait()
	}()

	for {
		if e.ctx.Err() != nil {
			return
		}
		readStart := time.Now()
		si, err := dm.ReadPacket()
		if d := time.Since(readStart); d > 5*time.Millisecond {
			e.mu.Lock()
			e.demuxWaits++
			if d > e.demuxMax {
				e.demuxMax = d
			}
			e.mu.Unlock()
		}
		if err != nil {
			if e.ctx.Err() == nil {
				e.finish(fmt.Errorf("读流结束: %w", err))
			}
			return
		}
		p := dm.ClonePacket()
		dm.UnrefPacket()
		if p == nil {
			continue
		}

		switch si {
		case dm.VideoIndex():
			// 视频包队列满时丢最旧的一包，绝不阻塞在这里。
			//
			// 解封装只有一个线程，音频包和视频包都从它出来。如果在这里等视频队列
			// 腾位置，音频包就一起停——音频环一耗尽，用户听到的就是断音。丢一包
			// 视频最多是一段花屏，断一次声音是立刻就听得出来的，孰轻孰重很清楚。
			select {
			case videoPkts <- p:
			default:
				select {
				case old := <-videoPkts:
					old.Close()
				default:
				}
				select {
				case videoPkts <- p:
				default:
					p.Close()
				}
			}
		case dm.AudioIndex():
			if audioDec == nil {
				p.Close()
				continue
			}
			// 音频这条反压是对的：PCM 环满了说明解码已经跑到实时前面，停一停。
			select {
			case audioPkts <- p:
			case <-e.ctx.Done():
				p.Close()
				return
			}
		default:
			p.Close()
		}
	}
}

// videoLoop 解视频，写进帧槽。永远不阻塞在呈现上。
func (e *Engine) videoLoop(dec *libav.Decoder, pkts <-chan *libav.Packet) {
	for {
		select {
		case <-e.ctx.Done():
			return
		case p, ok := <-pkts:
			if !ok {
				return
			}
			if err := dec.Send(p); err != nil {
				p.Close()
				continue
			}
			p.Close()
			for {
				f, err := dec.Receive()
				if err != nil {
					break
				}
				e.pushFrame(f, dec.Timebase())
			}
		}
	}
}

// audioLoop 解音频，写进 PCM 环。环满就在这里停住，这是整条管线唯一的节流点。
func (e *Engine) audioLoop(dec *libav.Decoder, pkts <-chan *libav.Packet) {
	for {
		select {
		case <-e.ctx.Done():
			return
		case p, ok := <-pkts:
			if !ok {
				return
			}
			// 先把解码器里攒着的帧收干净，再送新包。
			//
			// avcodec_send_packet 在内部缓冲满时返回 EAGAIN。原来这里直接丢包，
			// 等于从音频时间轴上挖掉一块——听起来就是断一下，而且切得莫名其妙。
			e.drainAudio(dec)
			if err := dec.Send(p); err != nil {
				p.Close()
				continue
			}
			p.Close()
			e.drainAudio(dec)
		}
	}
}

// drainAudio 把解码器里所有能取的音频帧写进 PCM 环。
func (e *Engine) drainAudio(dec *libav.Decoder) {
	for {
		f, err := dec.Receive()
		if err != nil {
			return
		}
		e.pushPCM(f, dec.Timebase())
	}
}

// pushFrame 把一帧 NV12 放进槽里。
//
// 没空槽就在这里等——这是视频解码的节流点，让解码跟着呈现节奏走。
// 只有等太久（呈现端完全停住）才降级丢最旧的帧，避免解码永久卡死。
func (e *Engine) pushFrame(f *libav.Frame, tb float64) {
	pts := time.Duration(float64(f.PTS) * tb * float64(time.Second))

	var idx int
	select {
	case idx = <-e.free:
	case <-e.ctx.Done():
		return
	case <-time.After(3 * time.Second):
		// 呈现端三秒没来取帧：说明窗口不在画面上（最小化/遮挡）。
		// 这时候丢最新的这帧，别让解码和音频一起卡住。
		e.mu.Lock()
		e.dropped++
		e.mu.Unlock()
		return
	}

	s := &e.slots[idx]
	copy(s.luma, f.Luma)
	copy(s.chroma, f.Chroma)

	e.mu.Lock()
	s.pts = pts
	s.state = slotReady
	if !e.hasStart {
		// 第一个能解码出来的帧就是关键帧，它是整条播放的起点。
		e.startPTS = pts
		e.hasStart = true
	}
	if !e.hasVideoBase {
		// 只给没有音轨的流用：第一帧的 PTS 与它到达的墙钟。
		e.videoBase = pts
		e.videoBaseAt = time.Now()
		e.hasVideoBase = true
	}
	e.frames++
	wake := e.wake
	e.mu.Unlock()
	if wake != nil {
		wake()
	}
}

// pushPCM 把 PCM 写进环里。环满就阻塞，这是有意的节流。
func (e *Engine) pushPCM(f *libav.Frame, tb float64) {
	pcm := f.PCMFloat32()
	if len(pcm) == 0 {
		return
	}
	pts := time.Duration(float64(f.PTS) * tb * float64(time.Second))

	e.mu.Lock()
	if !e.hasBase {
		// 关键帧之前的音频直接扔掉：那一段没有对应画面，留着只会让画面干等。
		if !e.hasStart || pts < e.startPTS {
			e.mu.Unlock()
			return
		}
		e.audioBase = pts
		e.hasBase = true
	}
	ring, ready := e.ring, e.audioReady
	e.mu.Unlock()

	if ring == nil {
		return
	}
	ring.write(pcm, e.ctx)

	if ready {
		return
	}
	// 预缓冲：攒够一段再出声。
	//
	// 不预缓冲的话，播放从直播边缘开始，供给就等于实时，缓冲永远攒不起来；
	// 一次网络停顿就是一个补不回来的缺口，用户听到的就是断音。先攒后播，
	// 把那一段差额一次性赚出来。
	if ring.writtenFrames() < prebufferFrames {
		return
	}
	e.mu.Lock()
	e.audioReady = true
	play := e.wantPlay && !e.paused
	p := e.player
	e.mu.Unlock()
	if p != nil && play {
		p.Play()
	}
}

// finish 记录让这一路停下来的错误。
func (e *Engine) finish(err error) {
	e.mu.Lock()
	if e.err == nil {
		e.err = err
	}
	wake := e.wake
	e.mu.Unlock()
	if wake != nil {
		wake()
	}
}
