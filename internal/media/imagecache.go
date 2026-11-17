// Package media 负责网络图片：下载、两层缓存、后台解码与缩放、主线程建纹理。
//
// MyGo 只给出 ui.DecodeBitmap / ui.NewBitmap，没有 HTTP 加载器、没有缓存、
// 也没有渐进式占位——这块必须自己补。
//
// # 为什么分两层
//
// 解码后的纹理很贵（一张 360px 宽的封面约 291KB，而 MyGo 的 Bitmap 实际会
// 占「纹理 + CPU 像素」两份），原始字节很便宜（同一张图约 30KB）。所以：
//
//	L1 纹理 —— 服务「现在这屏」，随上下文释放，丢了大不了重新解码
//	L2 字节 —— 服务「刚才看过、可能还要看」，按 TTL 老化
//	磁盘     —— 只服务头像这类长期不变的图，重启之后还在
//
// 切分区 / 切平台时清掉 L1，回滚时从 L2 重新解码，不用走网络。
// 这是两层缓存存在的唯一理由：把「省内存」和「回滚快」拆成两件独立的事。
//
// 磁盘层（见 imagecache_disk.go）只给 Avatar 用，封面不进：直播间的封面
// 常常就是实时画面，分钟级就换了，缓存下来命中的是过期图；头像几个月不换
// 一次，重启后重下一遍纯属浪费。
//
// # 其它约束
//
//   - 渲染线程只做渲染：解码和缩放是 CPU 密集的活，全部放在后台 goroutine，
//     主线程只做 ui.NewBitmap 的纹理创建；
//   - view 每帧重建，取图只能是一次 map 查找，不能阻塞；
//   - 同一个 URL 可能在一帧里被几十张卡片同时请求，必须去重；
//   - 缓存按目标显示尺寸解码，不能把原图整张塞进去；
//   - 一屏有多少张卡片取决于窗口大小和系统缩放，不能假定，所以容量按字节算，
//     淘汰时保护「刚刚还在用」的条目。
package media

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/image/draw"

	// 注册解码器。image.Decode 只认「已经 import 过」的格式，标准库的
	// jpeg/png/gif 必须显式引入才会注册。
	//
	// 这里踩过：原先只注册了 WebP（因为 B 站封面是 @480w.webp），
	// 于是所有 .jpg 的头像和表情全部解不出来——斗鱼、虎牙、B 站的头像
	// CDN 给的都是 jpg，表现就是头像永远只有渐变占位。
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	// x/image/webp 只解单帧 VP8/VP8L。动画 WebP（VP8X + ANIM）会直接报错，
	// 这条限制决定了下面 staticVariant 的存在：动图头像不能按 .webp 取。
	_ "golang.org/x/image/webp"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	// maxTextureBytes 是 L1（纹理）上限，按解码后的像素算（宽 × 高 × 4）。
	//
	// 不按张数：一张封面多大取决于它被解码成多大，张数说明不了占用。
	// 按两倍估：MyGo 的 Bitmap.ReleasePixels 在我们这种用法下省不掉 CPU
	// 像素（见下面 releaseNote），所以 64MB 实际常驻约 128MB。
	maxTextureBytes = 64 << 20

	// maxRawBytes 是 L2（原始字节）上限。字节便宜，但仍然要有天花板：
	// 长会话里滑过几千张图也不会让它无限涨。
	maxRawBytes = 30 << 20

	// rawTTL 是 L2 的存活时间。
	//
	// 有的直播间把封面设成实时画面，内容分钟级刷新；URL 不变时只有 TTL
	// 能让它更新。5 分钟比 baseline（响应头 max-age=86400）激进两个数量级，
	// 又能让一次浏览里不重复下载同一张图。
	rawTTL = 5 * time.Minute

	// idleTTL 是 L1 的闲置寿命：这么久没被画过的纹理会被清掉。
	//
	// 切走的分区、滚过去的几十屏都靠它自然回收——不需要每处调用点都记得
	// 通知缓存「我换地方了」。正在看的图每帧都会刷新 used，不会被扫到。
	idleTTL = 60 * time.Second

	// protectFor 是淘汰时跳过的「最近用过」窗口。
	//
	// 保证「屏幕上的图不会被挤掉」：不管一屏放得下多少张，只要它这一两秒
	// 内还被画过，就不会被新滚进来的图淘汰。极端情况下缓存会短暂超限，
	// 比屏幕上的图闪成占位好得多。
	protectFor = 3 * time.Second

	// retryAfter 是加载失败后的冷却。失败多是死链或 CDN 临时抖动，
	// 不能每帧重试——那会把一次失败放大成每帧一次的请求风暴。
	retryAfter = 30 * time.Second

	// flushDelay 把这一小段时间内解码完的图合并成一次重绘。
	// 一屏几十张图逐个 win.Update 等于几十次全窗口重建。
	flushDelay = 30 * time.Millisecond

	// sweepEvery 是后台回收的间隔。
	sweepEvery = 30 * time.Second

	// staticFallbackWidth 是「按原图解码」时静态兜底图的上限宽度。
	// 那种场景只出现在整屏播放页封面，480 已经够画满一屏。
	staticFallbackWidth = 480
)

// releaseNote 记录一个必须记住的框架行为：
//
// ui.Bitmap.ReleasePixels 在「app 不再持有原图」时直接返回、什么都不放，
// 在「还持有」时又用 remake 闭包继续引用它——两条路都省不掉 CPU 像素。
// 所以一张缓存图实际占「纹理 + 像素」两份，所有容量数字都按两倍估。

// 下载与解码各自限并发。
//
// 下载 8：对齐 baseline 那套的预取信号量（浏览器渲染路径 6 条连接 + 预取 8，
// 到 CDN 的并发上限是 20）。取 8 是「一屏几十张能在一秒内铺满」和
// 「不把上游惹毛」之间的位置。
// 解码 6：解码是 CPU 密集的，但都在后台 goroutine，8 核以上不会挤压渲染线程。
const (
	maxDownloads = 16
	maxDecodes   = 8
)

// texture 是一张已解码的纹理。
type texture struct {
	bmp   *ui.Bitmap
	url   string
	used  time.Time
	bytes int64
}

// rawImage 是下载下来还没解码的原始字节。
type rawImage struct {
	data []byte
	at   time.Time
}

// pendingImage 是一张解码完、等着上主线程建纹理的图。
type pendingImage struct {
	key, url string
	img      image.Image
}

// pendingAnimation 是一组解码好的动图帧。
type pendingAnimation struct {
	key, url string
	frames   []image.Image
	delays   []time.Duration
}

// Cache 是「URL + 目标宽度」→ 纹理的两层缓存。
type Cache struct {
	mu       sync.Mutex
	textures map[string]*texture
	raw      map[string]*rawImage
	rawBytes int64
	// loading 和 failed 都按 URL 记，不带目标宽度。
	//
	// 同一张头像会同时被 32（卡片）、36（播放页）、40（关注列表）三种尺寸
	// 请求；按 (URL, 宽度) 记的话这三次都会各自走一遍网络，同一张死链图
	// 也会各自失败一次。按 URL 记就是一图一次下载、一次失败冷却。
	loading map[string]bool
	failed  map[string]time.Time
	// last 是每张图最近一次成功上屏的纹理，不带目标宽度。
	//
	// 窗口在拖动时宽度连续变化，目标宽度每过一个 8 像素档位就是新 key；
	// 新纹理解码完成前如果返回 nil，整片封面会退回占位色，看起来就是闪。
	// 用上一张纹理顶着（Fit 会等比缩放到新尺寸），画面连续，解码完再换新图。
	last   map[string]*ui.Bitmap
	bytes  int64
	win    *mygo.Window
	scale  float64 // 设备像素 / DIP，决定解码尺寸
	client *http.Client

	// diskDir 是头像的磁盘缓存目录；空串表示只走内存（测试、拿不到配置目录）。
	// 封面永远不进磁盘，见包注释。
	diskDir string
	// wanted 记这次加载要不要落盘。
	//
	// 同一 URL 可能先被预取（只进内存）再被头像请求（要落盘），所以用 OR：
	// 只要有人要，解码成功后就把字节写下去。
	wanted map[string]bool
	// diskMu 串行化淘汰扫描。读写文件本身不持 mu：渲染线程每帧都要查缓存，
	// 不能等 IO。
	diskMu sync.Mutex
	diskAt time.Time // 上次扫描时间，限制扫描频率

	download chan struct{}
	decode   chan struct{}

	// anims 是动图的帧序列（按 URL + 目标尺寸）。首帧同时在 textures 里，
	// 只看纹理的调用（Bitmap/Ready）不受影响。
	anims map[string]*animation

	// 合批：pending 里攒着等上屏的图，scheduled 表示已经排了一次 flush
	pmu          sync.Mutex
	pending      []pendingImage
	pendingAnims []pendingAnimation
	scheduled    bool

	sweepOnce sync.Once
}

// Default 是应用共用的一份缓存。
var Default = &Cache{
	textures: map[string]*texture{},
	raw:      map[string]*rawImage{},
	loading:  map[string]bool{},
	failed:   map[string]time.Time{},
	last:     map[string]*ui.Bitmap{},
	wanted:   map[string]bool{},
	anims:    map[string]*animation{},
	scale:    1,
	client:   &http.Client{Timeout: 20 * time.Second},
	download: make(chan struct{}, maxDownloads),
	decode:   make(chan struct{}, maxDecodes),
}

// SetDiskDir 打开头像的磁盘缓存；dir 为空串就关掉。
//
// 目录建不出来或不可写不算错：写入失败就当没这层，内存缓存照跑。
func (c *Cache) SetDiskDir(dir string) {
	c.mu.Lock()
	c.diskDir = dir
	c.mu.Unlock()
}

// SetWindow 让缓存拿到窗口：图片就绪后用它触发重绘。
// 顺便记下设备缩放——解码尺寸要按它换算成物理像素，窗口开着时不会变。
func (c *Cache) SetWindow(w *mygo.Window) {
	c.mu.Lock()
	c.win = w
	if d := mygo.Screen.PrimaryDisplay(); d.ScaleFactor > 0 {
		c.scale = d.ScaleFactor
	}
	c.mu.Unlock()
	c.sweepOnce.Do(func() { go c.sweep() })
}

// Bitmap 返回 url 在显示宽度 width（DIP）下对应的纹理；
// 没有就返回 nil，同时在后台开始加载。
//
// 返回 nil 的那一帧用占位画法顶上；图片加载完成后会触发一次重绘，
// 下一帧就能拿到真图——加载什么渲染什么，不等待其它图片。
//
// 只走内存：封面用这个，直播封面是实时画面，落盘只会缓存过期图。
func (c *Cache) Bitmap(url string, width float32) *ui.Bitmap {
	return c.bitmap(url, width, false)
}

// Emote 取一张表情，和 Bitmap 只差一层：字节会落到磁盘缓存。
//
// 表情和头像一样是稳定资源：一张图用到老，重启后重下一遍纯属浪费。
// 弹幕一屏几十条，命中磁盘和走网络的首帧差别很明显。
func (c *Cache) Emote(url string, width float32) *ui.Bitmap {
	return c.bitmap(url, width, true)
}

// Avatar 取一张头像，和 Bitmap 只差一层：字节会落到磁盘缓存。
//
// 头像稳定，重下一遍没有意义；重启后第一次打开关注页也不该再等一轮网络。
// 其余路径完全一样——L1 纹理、L2 字节、后台解码、失败冷却都共享。
func (c *Cache) Avatar(url string, width float32) *ui.Bitmap {
	return c.bitmap(url, width, true)
}

func (c *Cache) bitmap(url string, width float32, persist bool) *ui.Bitmap {
	if url == "" {
		return nil
	}
	target := c.targetPixels(width)
	key := cacheKey(url, target)
	now := time.Now()

	c.mu.Lock()
	if persist {
		if c.wanted == nil {
			c.wanted = map[string]bool{}
		}
		// 在 loading 判断之前记下：同一张头像的另一个尺寸正在下载时，
		// 这次的落盘需求也得算进去。
		c.wanted[url] = true
	}
	if t, ok := c.textures[key]; ok {
		// 命中就刷新使用时间：闲置回收和 LRU 都看这个值，
		// 正在屏幕上的图因此不可能被清掉。
		t.used = now
		c.last[url] = t.bmp
		c.mu.Unlock()
		return t.bmp
	}
	stale := c.last[url]
	if c.loading[url] {
		c.mu.Unlock()
		// 正在解同 URL 的另一个尺寸：先把旧纹理交出去，别让封面空一帧。
		return stale
	}
	if at, ok := c.failed[url]; ok && now.Sub(at) < retryAfter {
		c.mu.Unlock()
		return stale
	}
	c.loading[url] = true

	// L2 命中：拿现成的字节去解码，不用再走网络
	var raw []byte
	if r, ok := c.raw[url]; ok && now.Sub(r.at) < rawTTL {
		raw = r.data
		r.at = now // 刚用过，别让它在我们手上过期
	}
	dir := c.diskDir
	c.mu.Unlock()

	switch {
	case raw != nil:
		go c.decodeRaw(key, url, target, raw)
	case persist && dir != "":
		// 磁盘优先于网络：头像重启后还在，先看它。
		// 只在 persist 时查——封面从来不落盘，查也是白查。
		go c.loadDisk(key, url, target, dir)
	default:
		go c.fetch(key, url, target)
	}
	// 新图在路上：这一帧交给上一张纹理，避免拖动窗口时封面反复闪成占位色。
	return stale
}

// targetPixels 把显示宽度换算成解码宽度，取整到 8 的倍数：
// 相邻的窗口尺寸变化多半落在同一档，不必为几像素的差别重新解码。
func (c *Cache) targetPixels(width float32) int {
	if width <= 0 {
		return 0 // 0 表示按原图解码（播放页整屏那种场景）
	}
	c.mu.Lock()
	scale := c.scale
	c.mu.Unlock()
	px := int(float64(width)*scale + 0.5)
	if px < 64 {
		px = 64
	}
	return (px + 7) / 8 * 8
}

func cacheKey(url string, target int) string {
	if target <= 0 {
		return url
	}
	return url + "|" + strconv.Itoa(target)
}

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"

func (c *Cache) fetch(key, url string, target int) {
	data, err := c.get(url)
	if err != nil {
		c.fail(url)
		return
	}
	c.putRaw(url, data)
	c.decodeRaw(key, url, target, data)
}

// get 下载一张图的原始字节。
//
// 下载并发槽只包住这一段：解码是另一类 CPU 活，有它自己的槽，
// 犯不着在解码期间继续占着下载名额。
func (c *Cache) get(url string) ([]byte, error) {
	c.download <- struct{}{}
	defer func() { <-c.download }()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	// 平台 CDN 有防盗链，缺 Referer 会拿到 403 或一张提示图
	req.Header.Set("Referer", "https://live.bilibili.com/")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media: %s 返回 %d", url, resp.StatusCode)
	}

	// 直播封面一般 200KB 以内，8MB 已经是很宽的上限，防止异常响应吃内存
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("media: %s 返回空响应", url)
	}
	return data, nil
}

// staticVariant 给 B 站图床的图换一个静态 JPEG 变体地址。
//
// 源图是 GIF 时，CDN 的 `.webp` 处理会输出动画 WebP（VP8X + ANIM + ANMF），
// 而 x/image/webp 只解单帧，整张图直接解不出来——动图头像就这样永远停在
// 字母占位上。换成 `.jpg` 拿的是首帧静态图，尺寸也跟着要的宽度走。
//
// 只认 hdslb.com：`@宽w_高h.jpg` 是 B 站图床的私有语法，别的 CDN 加了只会 404。
func staticVariant(rawURL string, target int) string {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.HasSuffix(u.Host, "hdslb.com") {
		return ""
	}
	base, _, ok := strings.Cut(u.Path, "@")
	if !ok {
		return ""
	}
	if target <= 0 {
		target = staticFallbackWidth
	}
	u.Path = fmt.Sprintf("%s@%dw.jpg", base, target)
	return u.String()
}

// putRaw 把原始字节放进 L2，超限时按最久没用过的淘汰。
func (c *Cache) putRaw(url string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.raw[url]; ok {
		c.rawBytes -= int64(len(old.data))
	}
	size := int64(len(data))
	c.raw[url] = &rawImage{data: data, at: time.Now()}
	c.rawBytes += size
	c.evictRawLocked()
}

func (c *Cache) evictRawLocked() {
	for c.rawBytes > maxRawBytes && len(c.raw) > 0 {
		var oldest string
		var at time.Time
		for url, r := range c.raw {
			if oldest == "" || r.at.Before(at) {
				oldest, at = url, r.at
			}
		}
		c.rawBytes -= int64(len(c.raw[oldest].data))
		delete(c.raw, oldest)
	}
}

// decodeRaw 把字节解码、缩放到目标宽度，然后排队等上屏。
func (c *Cache) decodeRaw(key, url string, target int, data []byte) {
	// 先看是不是动图：是的话按帧队列上屏，表情才会真的动起来。
	if frames, delays, ok := c.decodeAnimated(target, data); ok {
		c.persistData(url, data)
		c.enqueueAnimation(key, url, frames, delays)
		return
	}
	img, err := c.decodeBytes(target, data)
	if err != nil {
		// 解不出来多数是动图：GIF 源被 CDN 转成了动画 WebP。
		// 换静态 JPEG 变体再试一次，比直接判死多一条活路。
		if alt := staticVariant(url, target); alt != "" {
			go c.fetchStatic(key, url, alt, target)
			return
		}
		c.fail(url)
		return
	}
	// 解码成功才写盘：解不出来的字节写下去只会让下次启动再失败一遍。
	c.persistData(url, data)
	c.enqueue(key, url, img)
}

// fetchStatic 取静态变体，成功后把它当作这张 URL 的字节。
//
// 覆盖 L2 里那份解不出来的动画字节：同一张头像接下来被别的尺寸请求时
// 直接从缓存解码，不用再走一次网络。这里不再兜底，避免来回打转。
func (c *Cache) fetchStatic(key, url, alt string, target int) {
	data, err := c.get(alt)
	if err != nil {
		c.fail(url)
		return
	}
	c.putRaw(url, data)
	img, err := c.decodeBytes(target, data)
	if err != nil {
		c.fail(url)
		return
	}
	// 解码成功才写盘：解不出来的字节写下去只会让下次启动再失败一遍。
	c.persistData(url, data)
	c.enqueue(key, url, img)
}

// decodeBytes 解码并缩放到目标宽度。
//
// 解码与缩放都留在后台：这是 CPU 密集的一段，绝不能搬到渲染线程上。
func (c *Cache) decodeBytes(target int, data []byte) (image.Image, error) {
	c.decode <- struct{}{}
	defer func() { <-c.decode }()

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if target > 0 {
		img = scaleTo(img, target)
	}
	return img, nil
}

// scaleTo 把图缩到 width 宽，等比。
//
// 直播平台的封面原图动辄 1280×720（虎牙、抖音直接给原图），
// 卡片上只画两三百像素宽，不缩就是几十倍的显存浪费。
func scaleTo(img image.Image, width int) image.Image {
	b := img.Bounds()
	if b.Dx() <= width {
		return img
	}
	h := int(float64(b.Dy())*float64(width)/float64(b.Dx()) + 0.5)
	if h < 1 {
		h = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, h))
	// ApproxBiLinear 缩小到缩略图尺寸足够，比 CatmullRom 快一个量级
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// enqueue 把解码好的图排进待上屏队列，一小段时间内的会合并成一次重绘。
func (c *Cache) enqueue(key, url string, img image.Image) {
	c.pmu.Lock()
	c.pending = append(c.pending, pendingImage{key: key, url: url, img: img})
	if c.scheduled {
		c.pmu.Unlock()
		return
	}
	c.scheduled = true
	c.pmu.Unlock()
	time.AfterFunc(flushDelay, c.flush)
}

// flush 在主线程把这些图一次性建成纹理，只触发一次重绘。
func (c *Cache) flush() {
	c.pmu.Lock()
	batch := c.pending
	anims := c.pendingAnims
	c.pending = nil
	c.pendingAnims = nil
	c.scheduled = false
	c.pmu.Unlock()
	if len(batch) == 0 && len(anims) == 0 {
		return
	}

	c.mu.Lock()
	win := c.win
	c.mu.Unlock()
	if win == nil {
		for _, p := range batch {
			c.fail(p.url)
		}
		for _, a := range anims {
			c.fail(a.url)
		}
		return
	}

	win.Update(func() {
		bms := make([]*ui.Bitmap, 0, len(batch))
		for _, p := range batch {
			bmp := ui.NewBitmap(p.img)
			c.done(p.key, p.url, bmp)
			bms = append(bms, bmp)
		}
		for _, a := range anims {
			frames := make([]*ui.Bitmap, len(a.frames))
			for i, img := range a.frames {
				frames[i] = ui.NewBitmap(img)
			}
			c.doneAnimation(a.key, a.url, frames, a.delays)
			bms = append(bms, frames...)
		}
		// 上屏一帧之后再统一丢 CPU 副本（虽然框架会留下 remake 引用，
		// 但这是唯一能做的；容量按两倍估就是为它留的余量）
		win.Update(func() {
			for _, b := range bms {
				b.ReleasePixels()
			}
		})
	})
}

// done 收尾：入 L1 并按字节淘汰。
func (c *Cache) done(key, url string, bmp *ui.Bitmap) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loading[url] = false
	delete(c.failed, url)
	if _, ok := c.textures[key]; ok {
		return
	}

	w, h := ui.ImageSize(bmp)
	size := int64(w) * int64(h) * 4
	c.textures[key] = &texture{bmp: bmp, url: url, used: time.Now(), bytes: size}
	// 记住这张图最新的纹理：同 URL 换尺寸时先用它顶着，画面才不会闪。
	c.last[url] = bmp
	c.bytes += size
	c.evictTextureLocked()
}

// fail 记一次失败，冷却期内不再重试。
func (c *Cache) fail(url string) {
	c.mu.Lock()
	c.loading[url] = false
	c.failed[url] = time.Now()
	c.mu.Unlock()
}

// evictTextureLocked 按 LRU 淘汰 L1，跳过保护期内的条目。
func (c *Cache) evictTextureLocked() {
	now := time.Now()
	for c.bytes > maxTextureBytes {
		var oldest string
		var at time.Time
		for key, t := range c.textures {
			if now.Sub(t.used) < protectFor {
				continue
			}
			if oldest == "" || t.used.Before(at) {
				oldest, at = key, t.used
			}
		}
		if oldest == "" {
			return // 剩下的都还在屏幕上，超限就超限
		}
		t := c.textures[oldest]
		c.bytes -= t.bytes
		delete(c.textures, oldest)
		// last 指向的正是这张就一起删；指向同 URL 的更新纹理时保留。
		if c.last[t.url] == t.bmp {
			delete(c.last, t.url)
		}
	}
}

// sweep 定期回收：清掉闲置的纹理和过期的字节。
//
// 有了它，切走的分区、滚过去的几十屏都会自己消失，不需要每个调用点
// 都记得通知缓存。正在看的图每帧刷新 used，不会被扫到。
func (c *Cache) sweep() {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		c.mu.Lock()
		for key, t := range c.textures {
			if now.Sub(t.used) > idleTTL {
				c.bytes -= t.bytes
				delete(c.textures, key)
				if c.last[t.url] == t.bmp {
					delete(c.last, t.url)
				}
			}
		}
		for url, r := range c.raw {
			if now.Sub(r.at) > rawTTL {
				c.rawBytes -= int64(len(r.data))
				delete(c.raw, url)
			}
		}
		for key := range c.anims {
			if _, ok := c.textures[key]; !ok {
				delete(c.anims, key)
			}
		}
		c.mu.Unlock()
	}
}

// ReleaseTexturesExcept 清空 L1，但保留 keep 里的 URL。
//
// 切平台时用：旧平台的纹理对新平台毫无用处，但新平台首屏的封面可能
// 已经预取好了，一起清掉等于让用户重新等一遍解码。
func (c *Cache) ReleaseTexturesExcept(keep map[string]bool) {
	c.mu.Lock()
	for key, t := range c.textures {
		if keep[t.url] {
			continue
		}
		c.bytes -= t.bytes
		delete(c.textures, key)
		delete(c.last, t.url)
	}
	for key, a := range c.anims {
		if keep[a.url] {
			continue
		}
		delete(c.anims, key)
	}
	c.mu.Unlock()
	debug.FreeOSMemory()
}

// ReleaseTextures 立刻清空 L1（切平台时调用）。
//
// 只清纹理，不动字节：新平台的图要重新解码，旧平台的图如果之后切回来，
// 还能从 L2 里直接解出来，不用再走网络。
func (c *Cache) ReleaseTextures() {
	c.mu.Lock()
	clear(c.textures)
	clear(c.last)
	clear(c.anims)
	c.bytes = 0
	c.mu.Unlock()
	// 纹理是常驻内存的大头，清完顺手把堆还给系统。
	//
	// Go 的 runtime 拿到内存后不会主动交还，RSS 会一直停在峰值——
	// 任务管理器上看着就像「切了平台也不回收」。切平台本来就是低频的
	// 加载态，这里一次 STW 换回几百 MB 的可见回落，值得。
	debug.FreeOSMemory()
}

// Ready 报告这张图是不是已经有纹理了（只查，不触发加载）。
//
// 启动层用它判断「首屏的封面都到齐了没有」——列表有数据不等于有封面，
// 撤早了用户看到的就是一片带颜色的占位卡。
//
// 加载失败的也算到位：死链不能把启动层一直拖到超时。
func (c *Cache) Ready(url string, width float32) bool {
	if url == "" {
		return true
	}
	key := cacheKey(url, c.targetPixels(width))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.textures[key]; ok {
		return true
	}
	_, failed := c.failed[url]
	return failed
}

// Prefetch 只是把 URL 排进加载队列，用于首屏和「快要滚到」的行提速。
func (c *Cache) Prefetch(width float32, urls ...string) {
	for _, u := range urls {
		c.Bitmap(u, width)
	}
}

// PrefetchAvatars 是头像专用的预取：顺手把落盘需求记上。
//
// 首屏预取的头像随后会被卡片、岛、关注页按几档尺寸再请求一次；走 Bitmap
// 的话那次下载不落盘，下一次启动还得重来。
func (c *Cache) PrefetchAvatars(width float32, urls ...string) {
	for _, u := range urls {
		c.Avatar(u, width)
	}
}
