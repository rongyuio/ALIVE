package media

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 动图命中主路径是 EmoteFrame 的 anims 分支，不再经过 bitmap()。
//
// 如果这条路径不刷新 used，sweep 会把一张一直在弹幕里播的表情当成闲置
// 纹理回收：下一帧表情消失，然后整段 GIF 重新解码，热门表情会周期性闪。
func TestEmoteFrameRefreshesUsage(t *testing.T) {
	c := testCache()
	c.anims = map[string]*animation{}

	url := "https://example.invalid/hot-emote.gif"
	frames := []*ui.Bitmap{testBitmap(t), testBitmap(t)}
	c.doneAnimation(cacheKey(url, c.targetPixels(20)), url, frames,
		[]time.Duration{50 * time.Millisecond, 50 * time.Millisecond})

	key := cacheKey(url, c.targetPixels(20))
	stale := time.Now().Add(-idleTTL - time.Second)
	c.textures[key].used = stale

	now := time.Now()
	bmp, _, anim := c.EmoteFrame(url, 20, now)
	if !anim || bmp == nil {
		t.Fatal("动图没有命中 anims 分支")
	}
	if got := c.textures[key].used; !got.After(stale) {
		t.Fatalf("EmoteFrame 命中后 used 仍是闲置时间 %v：sweep 会回收正在播放的动图", got)
	}
}

// 动图的每一帧都占 L1 内存，doneAnimation 必须把整份字节记到 c.bytes。
//
// 只写 texture.bytes 不加账，回收时却照减，账会一路减成负数：
// 64MB 上限对动图完全不可见，普通封面纹理的 LRU 淘汰也被无限期推迟。
func TestDoneAnimationAccountsAllFrames(t *testing.T) {
	c := testCache()
	c.anims = map[string]*animation{}

	url := "https://example.invalid/emote.gif"
	frames := []*ui.Bitmap{testBitmap(t), testBitmap(t), testBitmap(t)}
	delays := []time.Duration{50 * time.Millisecond, 50 * time.Millisecond, 50 * time.Millisecond}

	var want int64
	for _, b := range frames {
		w, h := ui.ImageSize(b)
		want += int64(w) * int64(h) * 4
	}

	key := cacheKey(url, c.targetPixels(20))
	c.doneAnimation(key, url, frames, delays)
	if c.bytes != want {
		t.Fatalf("动图上账后 c.bytes = %d，期望 %d（三帧总共的字节）", c.bytes, want)
	}

	// 同一 key 换一版动图：旧的整份扣掉、新的整份加上，不能只减不加。
	c.doneAnimation(key, url, frames, delays)
	if c.bytes != want {
		t.Fatalf("替换动图后 c.bytes = %d，期望仍是 %d", c.bytes, want)
	}
}
