package playback

import (
	"context"
	"sync"
	"time"

	"github.com/egoist/mygo/audio"
	mygoMedia "github.com/egoist/mygo/media"
	mygoScene "github.com/egoist/mygo/scene"
)

// pcmRing 是解码线程和音频输出之间的环形缓冲，同时实现 audio.Decoder。
//
// 它有两个身份：
//
//   - 写端：音频解码 goroutine 往里写 float32 样本。写满就阻塞，这是有意为之，
//     它是整条管线唯一的节流点。
//   - 读端：MyGo 的 audio.Player 按 WASAPI 的实际消费节奏读。
//
// 之所以要有这一层：libav 一次解出的 PCM 多寡不均，而声卡是匀速消费的。
// 中间隔一个环，解码快慢就不会影响出声的连续性。
type pcmRing struct {
	mu   sync.Mutex
	cond *sync.Cond

	buf  []float32
	r, w int
	n    int

	// closed 之后 write 立刻返回，Read 返回 nil 表示到底。
	closed bool
	// 已写出的总帧数，用于外部换算播放进度。
	written int64

	// 诊断：环被读空的次数与最长一次空等，用来判断断音是饿死还是别的原因。
	starved   uint64
	starveMax time.Duration
}

func newPCMRing(frames int) *pcmRing {
	p := &pcmRing{buf: make([]float32, frames*audioChannels)}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// write 把交错立体声样本写进环。环满就阻塞，直到有空间或 ctx 结束。
func (p *pcmRing) write(samples []float32, ctx context.Context) {
	// 环满时不再有新样本进来，用 ctx 的 Done 唤醒等待。
	stop := context.AfterFunc(ctx, func() {
		p.mu.Lock()
		p.closed = true
		p.cond.Broadcast()
		p.mu.Unlock()
	})
	defer stop()

	p.mu.Lock()
	defer p.mu.Unlock()
	for len(samples) > 0 {
		if p.closed {
			return
		}
		space := len(p.buf) - p.n
		if space <= 0 {
			p.cond.Wait()
			continue
		}
		// 一次最多写到环尾，避免绕回。
		end := min(len(p.buf), p.w+min(space, len(samples)))
		c := copy(p.buf[p.w:end], samples)
		p.w = (p.w + c) % len(p.buf)
		p.n += c
		p.written += int64(c / audioChannels)
		samples = samples[c:]
		p.cond.Broadcast()
	}
}

// Read 实现 audio.Decoder：填满 buf，没有数据就等。
//
// 返回值是**交错样本数**，不是帧数。MyGo 的 stream 拿到 n 之后自己做
// `n -= n % channels` 再切，这正是 audio.Decoder 的约定。
func (p *pcmRing) Read(buf []float32) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.n == 0 && !p.closed {
		// 环空了：记一次饿死，以及这次饿了多久。
		start := time.Now()
		for p.n == 0 && !p.closed {
			p.cond.Wait()
		}
		if d := time.Since(start); d > time.Millisecond {
			p.starved++
			if d > p.starveMax {
				p.starveMax = d
			}
		}
	}
	if p.n == 0 && p.closed {
		return 0, nil
	}
	got := 0
	for got < len(buf) && p.n > 0 {
		end := min(len(p.buf), p.r+min(p.n, len(buf)-got))
		c := copy(buf[got:], p.buf[p.r:end])
		p.r = (p.r + c) % len(p.buf)
		p.n -= c
		got += c
	}
	p.cond.Broadcast()
	return got, nil
}

// writtenFrames 返回累计写入的帧数，用于判断预缓冲是否攒够。
func (p *pcmRing) writtenFrames() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.written
}

// SampleRate 实现 audio.Decoder。
func (p *pcmRing) SampleRate() int { return audioSampleRate }

// Channels 实现 audio.Decoder。
func (p *pcmRing) Channels() int { return audioChannels }

// Len 实现 audio.Decoder：直播没有总长度。
func (p *pcmRing) Len() int64 { return -1 }

// SetPosition 实现 audio.Decoder：直播不支持跳转。
func (p *pcmRing) SetPosition(int64) error { return nil }

// close 让读写两端都退出。
func (p *pcmRing) close() {
	p.mu.Lock()
	p.closed = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

var _ audio.Decoder = (*pcmRing)(nil)

// --- media.Engine ---

// SetWake 收下窗口的唤醒函数。
func (e *Engine) SetWake(wake func()) {
	e.mu.Lock()
	e.wake = wake
	e.mu.Unlock()
}

// Frame 返回 t 这一刻该显示的画面。
//
// 只挑「时间戳已经到点」的最新一帧。所有比它更旧的 ready 帧都不会再上屏了，
// 顺手释放掉——留着只会占槽，让解码线程丢掉更新的帧。
func (e *Engine) Frame(t time.Time) (mygoMedia.Picture, bool, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return mygoMedia.Picture{}, false, false
	}
	content := e.contentTimeLocked()

	best := -1
	for i := range e.slots {
		s := &e.slots[i]
		if s.state != slotReady || s.pts > content {
			continue
		}
		if best < 0 || s.pts > e.slots[best].pts {
			best = i
		}
	}
	if best < 0 {
		for i := range e.slots {
			if e.slots[i].state == slotShowing {
				return e.pictureLocked(i), false, true
			}
		}
		return mygoMedia.Picture{}, false, false
	}

	// 过期帧（比 best 更旧）永远不会上屏了，直接还回去。
	for i := range e.slots {
		s := &e.slots[i]
		if s.state == slotReady && s.pts < e.slots[best].pts {
			s.state = slotEmpty
			e.dropped++
			e.releaseLocked(i)
		}
	}
	for i := range e.slots {
		if e.slots[i].state == slotShowing && i != best {
			e.slots[i].state = slotEmpty
			e.releaseLocked(i)
		}
	}
	e.slots[best].state = slotShowing
	if e.state == mygoMedia.Loading {
		e.state = mygoMedia.Ready
	}
	return e.pictureLocked(best), true, true
}

// releaseLocked 把槽还回 free。必须在持锁时调用。
//
// free 容量正好等于槽数，每个索引同时只会在 free 里出现一次，所以这里不会阻塞。
func (e *Engine) releaseLocked(i int) {
	select {
	case e.free <- i:
	default:
	}
}

// contentTimeLocked 是当前的内容时间。
//
// 有音轨时音频就是主时钟：音频首帧的 PTS 加上播放器已经播出的时长。音频和
// 视频来自同一个 demuxer，PTS 同轴，所以这个值可以直接和视频帧的 PTS 比。
//
// 没有音轨时才退回「用视频自己走」：取最早一帧的 PTS，按墙钟推进。
func (e *Engine) contentTimeLocked() time.Duration {
	if e.player != nil && e.hasBase {
		return e.audioBase + e.player.Position()
	}
	if e.hasStart {
		// 音频还没接上（关键帧之前的样本正在被丢弃），先把时钟钉在关键帧上：
		// 第一帧立刻上屏，不必等音频追平。
		return e.startPTS
	}
	if !e.hasVideoBase {
		return 0
	}
	at := time.Now()
	if e.paused {
		at = e.pausedAt
	}
	elapsed := at.Sub(e.videoBaseAt)
	if elapsed < 0 {
		elapsed = 0
	}
	return e.videoBase + elapsed
}

// pictureLocked 把槽包成 MyGo 的画面：NV12 平面，GPU 就地采样。
func (e *Engine) pictureLocked(i int) mygoMedia.Picture {
	s := &e.slots[i]
	w, h := e.width, e.height
	surface := &mygoScene.Surface{
		Format: mygoScene.SurfaceNV12,
		Matrix: mygoScene.BT709,
		Planes: [2]mygoScene.Plane{
			{Pix: s.luma, Stride: w},
			{Pix: s.chroma, Stride: w},
		},
	}
	return mygoMedia.Picture{
		Surface: surface,
		W:       w,
		H:       h,
		RGBA:    func(dst []byte) []byte { return surface.RGBA(dst, w, h) },
	}
}

// Interval 是呈现端的取帧节拍。
func (e *Engine) Interval() time.Duration { return 16 * time.Millisecond }

// State 报告加载与播放状态。
func (e *Engine) State() (mygoMedia.State, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return mygoMedia.Failed, e.err
	}
	if e.closed {
		return mygoMedia.Ended, nil
	}
	return e.state, nil
}

// Size 是画面尺寸。
func (e *Engine) Size() (int, int) { return e.width, e.height }

// Duration 对直播没有意义。
func (e *Engine) Duration() time.Duration { return 0 }

// Position 是已经播过的时长。
func (e *Engine) Position() time.Duration {
	if e.player == nil {
		return 0
	}
	return e.player.Position()
}

// Play 恢复播放。预缓冲没攒够时只记下意图，攒够后由 pushPCM 起播。
func (e *Engine) Play() {
	e.mu.Lock()
	e.paused = false
	e.wantPlay = true
	p := e.player
	play := e.audioReady
	e.mu.Unlock()
	if p != nil && play {
		p.Play()
	}
}

// Pause 停住播放。
func (e *Engine) Pause() {
	e.mu.Lock()
	e.paused = true
	e.wantPlay = false
	p := e.player
	e.mu.Unlock()
	if p != nil {
		p.Pause()
	}
}

// Seek 对直播不支持。
func (e *Engine) Seek(time.Duration) {}

// SetLoop 对直播没有意义。
func (e *Engine) SetLoop(bool) {}

// SetRate 对直播没有意义。
func (e *Engine) SetRate(float64) {}

// SetVolume 设置音量。
func (e *Engine) SetVolume(v float64) {
	e.mu.Lock()
	e.volume = v
	muted, p := e.muted, e.player
	e.mu.Unlock()
	if p != nil {
		p.SetVolume(effectiveGain(v, muted))
	}
}

// SetMuted 静音或取消静音。
func (e *Engine) SetMuted(m bool) {
	e.mu.Lock()
	e.muted = m
	vol, p := e.volume, e.player
	e.mu.Unlock()
	if p != nil {
		p.SetVolume(effectiveGain(vol, m))
	}
}

// Close 停掉整条管线。
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	ring, p := e.ring, e.player
	e.wake = nil
	e.mu.Unlock()

	e.cancel()
	if p != nil {
		p.Stop()
	}
	if ring != nil {
		ring.close()
	}
	return nil
}

// Stats 返回已解码帧数、丢帧数与音频是否在用。
func (e *Engine) Stats() (frames, dropped uint64, audioOn bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.frames, e.dropped, e.player != nil
}

// Starved 返回 PCM 环被读空的次数与最长一次空等。
func (e *Engine) Starved() (count uint64, worst time.Duration) {
	e.mu.Lock()
	ring := e.ring
	e.mu.Unlock()
	if ring == nil {
		return 0, 0
	}
	ring.mu.Lock()
	defer ring.mu.Unlock()
	return ring.starved, ring.starveMax
}

// DemuxStall 返回解封装读包阻塞的次数与最长一次，供诊断。
func (e *Engine) DemuxStall() (count uint64, worst time.Duration) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.demuxWaits, e.demuxMax
}

// Done 在解码线程退出后关闭。
func (e *Engine) Done() <-chan struct{} { return e.done }

func effectiveGain(volume float64, muted bool) float64 {
	if muted {
		return 0
	}
	if volume < 0 {
		return 0
	}
	if volume > 1 {
		return 1
	}
	return volume
}
