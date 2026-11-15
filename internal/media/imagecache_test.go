package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// 动图头像必须能换到静态变体，否则那张图永远解不出来。
//
// 线上实例：某主播头像是 GIF，上游给的是
// https://i0.hdslb.com/bfs/face/xxx.gif@480w.webp，CDN 会转成动画 WebP
// （VP8X + ANIM），x/image/webp 只解单帧，image.Decode 直接报错，
// 界面上就永远停在字母占位。换成 .jpg 拿首帧才能画出来。
func TestStaticVariant(t *testing.T) {
	cases := []struct {
		name   string
		url    string
		target int
		want   string
	}{
		{
			name:   "GIF 头像换成静态 JPEG",
			url:    "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@480w.webp",
			target: 64,
			want:   "https://i0.hdslb.com/bfs/face/d442b405ea18f1fff815084a73fe3a3abb5c1424.gif@64w.jpg",
		},
		{
			name:   "WebP 源同样能换",
			url:    "https://i1.hdslb.com/bfs/face/fbc753c928a6d224b14fdd753ee4122cd9fd0238.webp@480w.webp",
			target: 120,
			want:   "https://i1.hdslb.com/bfs/face/fbc753c928a6d224b14fdd753ee4122cd9fd0238.webp@120w.jpg",
		},
		{
			name:   "原图场景用兜底宽度",
			url:    "https://i0.hdslb.com/bfs/face/x.gif@480w.webp",
			target: 0,
			want:   "https://i0.hdslb.com/bfs/face/x.gif@480w.jpg",
		},
		{
			name:   "没有 @ 参数就不动",
			url:    "https://i0.hdslb.com/bfs/face/x.jpg",
			target: 64,
			want:   "",
		},
		{
			name:   "别的 CDN 不认这套语法",
			url:    "https://apic.douyucdn.cn/upload/avanew/face/x.jpg@480w.webp",
			target: 64,
			want:   "",
		},
	}
	for _, c := range cases {
		if got := staticVariant(c.url, c.target); got != c.want {
			t.Errorf("%s: staticVariant = %q，想要 %q", c.name, got, c.want)
		}
	}
}

func testBitmap(t *testing.T) *ui.Bitmap {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	bmp, err := ui.DecodeBitmap(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return bmp
}

func testCache() *Cache {
	return &Cache{
		textures: map[string]*texture{},
		raw:      map[string]*rawImage{},
		loading:  map[string]bool{},
		failed:   map[string]time.Time{},
		last:     map[string]*ui.Bitmap{},
		scale:    1,
	}
}

// 窗口拖动时目标宽度每过一档就是新 key。新图解码期间必须继续用上一张纹理，
// 不能返回 nil——返回 nil 会让整片封面退回占位色，看起来就是持续闪烁。
func TestBitmapKeepsPreviousTextureWhileResizing(t *testing.T) {
	c := testCache()
	url := "https://example.invalid/cover.jpg"
	bmp := testBitmap(t)
	c.textures["cover|200"] = &texture{bmp: bmp, url: url, used: time.Now()}
	c.last[url] = bmp
	// 另一个尺寸正在解码：这一帧不能再排队，也不该交出 nil。
	c.loading[url] = true

	if got := c.Bitmap(url, 240); got != bmp {
		t.Fatalf("换尺寸加载期间返回 %v，期望沿用上一张纹理", got)
	}
}

// 失败冷却期内同样沿用上一张：重试之前不该把已经显示过的图清成占位色。
func TestBitmapKeepsPreviousTextureWhileFailing(t *testing.T) {
	c := testCache()
	url := "https://example.invalid/cover.jpg"
	bmp := testBitmap(t)
	c.last[url] = bmp
	c.failed[url] = time.Now()

	if got := c.Bitmap(url, 240); got != bmp {
		t.Fatal("失败冷却期内没有沿用上一张纹理")
	}
}

// 释放纹理时 last 也要一起清，否则被淘汰的纹理还挂在缓存里，回收失效。
func TestReleaseTexturesClearsLast(t *testing.T) {
	c := testCache()
	url := "https://example.invalid/cover.jpg"
	bmp := testBitmap(t)
	c.textures["cover|200"] = &texture{bmp: bmp, url: url, used: time.Now()}
	c.last[url] = bmp

	c.ReleaseTextures()
	if len(c.last) != 0 || len(c.textures) != 0 {
		t.Fatalf("释放后仍保留 last=%d textures=%d", len(c.last), len(c.textures))
	}
}
