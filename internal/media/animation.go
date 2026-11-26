package media

import (
	"bytes"
	"image"
	"image/gif"
	"time"

	"github.com/egoist/mygo/ui"
)

// 动态表情：GIF 逐帧解码，按时间取当前帧。
//
// 弹幕里的动态表情（B 站 is_dynamic、虎牙的 iFrameSize>0）尺寸只有十几
// 像素，做完整动画收益有限，但「一直在动的表情变成静止首帧」很显眼。
// 这里只处理 GIF：x/image/webp 只解单帧，动画 WebP 在 CDN 上也不稳定，
// 不值得为它引一套解码器。

// animation 是一张动图的所有帧。
type animation struct {
	url    string
	frames []*ui.Bitmap
	delays []time.Duration
	total  time.Duration
}

// frameAt 返回 now 时刻该显示哪一帧，以及距离下一帧还有多久。
func (a *animation) frameAt(now time.Time) (*ui.Bitmap, time.Duration) {
	if len(a.frames) == 0 {
		return nil, 0
	}
	if a.total <= 0 || len(a.frames) == 1 {
		return a.frames[0], 0
	}
	t := time.Duration(now.UnixNano() % int64(a.total))
	var acc time.Duration
	for i, d := range a.delays {
		acc += d
		if t < acc {
			return a.frames[i], acc - t
		}
	}
	last := len(a.frames) - 1
	return a.frames[last], a.total - t
}

// decodeAnimated 把 GIF 的每一帧缩到目标宽度。
//
// 不是 GIF 或只有一帧时返回 false，调用方走普通单帧链路。
func (c *Cache) decodeAnimated(target int, data []byte) ([]image.Image, []time.Duration, bool) {
	if len(data) < 6 || string(data[:3]) != "GIF" {
		return nil, nil, false
	}
	c.decode <- struct{}{}
	defer func() { <-c.decode }()

	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(g.Image) < 2 {
		return nil, nil, false
	}
	frames := make([]image.Image, 0, len(g.Image))
	delays := make([]time.Duration, 0, len(g.Image))
	for i, img := range g.Image {
		var out image.Image = img
		if target > 0 {
			out = scaleTo(img, target)
		}
		frames = append(frames, out)
		// GIF 的 Delay 单位是 1/100 秒；0 或过小的按浏览器行为兜底，
		// 否则某些表情会出现一帧闪过的频闪。
		d := time.Duration(g.Delay[i]) * 10 * time.Millisecond
		if d < 20*time.Millisecond {
			d = 100 * time.Millisecond
		}
		delays = append(delays, d)
	}
	return frames, delays, true
}

// enqueueAnimation 把解码好的多帧排进上屏队列。
func (c *Cache) enqueueAnimation(key, url string, frames []image.Image, delays []time.Duration) {
	c.pmu.Lock()
	c.pendingAnims = append(c.pendingAnims, pendingAnimation{key: key, url: url, frames: frames, delays: delays})
	if c.scheduled {
		c.pmu.Unlock()
		return
	}
	c.scheduled = true
	c.pmu.Unlock()
	time.AfterFunc(flushDelay, c.flush)
}

// doneAnimation 在主线程把帧建成纹理并登记。
func (c *Cache) doneAnimation(key, url string, frames []*ui.Bitmap, delays []time.Duration) {
	var total time.Duration
	for _, d := range delays {
		total += d
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	// url 必须记下来：ReleaseTexturesExcept / sweep 都按 tex.url 判断该不该
	// 保留，漏了它动图永远 keep 不住——切平台时被清掉，或者跟着首帧纹理一起
	// 被回收，下一帧整段 GIF 重新解码。
	c.anims[key] = &animation{url: url, frames: frames, delays: delays, total: total}
	// 纹理记账覆盖整组帧：sweep / LRU / ReleaseTexturesExcept 都按
	// texture.bytes 从 c.bytes 里减，只记首帧会让动图内存对 64MB 上限
	// 不可见，回收时反而把账减成负数。替换同一 key 时旧账整份退出。
	var size int64
	for _, b := range frames {
		w, h := ui.ImageSize(b)
		size += int64(w) * int64(h) * 4
	}
	if old, ok := c.textures[key]; ok {
		c.bytes -= old.bytes
	}
	// 首帧同时进纹理表：Bitmap/Ready 这类只看纹理的调用照常工作。
	c.textures[key] = &texture{bmp: frames[0], url: url, used: now, bytes: size}
	c.bytes += size
	c.last[url] = frames[0]
	c.loading[url] = false
	delete(c.failed, url)
	c.evictTextureLocked()
}

// EmoteFrame 返回表情当前应显示的帧。
//
// anim 为 true 时 wait 是距离下一帧的时间，调用方据此请求下一次重绘；
// 没有动图时退回普通纹理（首帧），anim 为 false。
func (c *Cache) EmoteFrame(url string, width float32, now time.Time) (bmp *ui.Bitmap, wait time.Duration, anim bool) {
	if url == "" {
		return nil, 0, false
	}
	key := cacheKey(url, c.targetPixels(width))
	c.mu.Lock()
	a, ok := c.anims[key]
	if ok {
		// 动图命中不经过 bitmap()，这里不刷新 used 的话，一直在屏幕上
		// 播的表情也会被 sweep 当成闲置纹理回收，下一帧整段 GIF 重新解码。
		if t := c.textures[key]; t != nil {
			t.used = now
		}
		a2 := a
		c.mu.Unlock()
		bmp, wait = a2.frameAt(now)
		return bmp, wait, wait > 0
	}
	c.mu.Unlock()
	return c.bitmap(url, width, true), 0, false
}
