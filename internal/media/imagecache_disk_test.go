package media

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor 是带上限的轮询：磁盘和网络都在别的 goroutine 上，没有虚拟时钟可推。
//
// 上限只是把卡死变成失败，不要为了让它过而调大——超时说明加载链路断了。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等 %s 超时", what)
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(0, 0, color.RGBA{R: 200, G: 30, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testDiskCache 在 testCache 的基础上补上真实网络与磁盘需要的东西。
func testDiskCache(dir string) *Cache {
	c := testCache()
	c.client = &http.Client{Timeout: 5 * time.Second}
	c.download = make(chan struct{}, maxDownloads)
	c.decode = make(chan struct{}, maxDecodes)
	c.SetDiskDir(dir)
	return c
}

// 文件名必须是稳定的哈希：同一 URL 永远同一个文件，不同 URL 不会撞。
//
// 不能拿 URL 直接拼路径——里面有 `?`、`/`、签名参数，会写到目录外面去。
func TestDiskPath(t *testing.T) {
	dir := t.TempDir()
	a := diskPath(dir, "https://example.invalid/face/a.jpg?x=1&y=2")
	b := diskPath(dir, "https://example.invalid/face/a.jpg?x=1&y=2")
	c := diskPath(dir, "https://example.invalid/face/b.jpg")

	if a != b {
		t.Fatalf("同一 URL 生成两个路径：%q / %q", a, b)
	}
	if a == c {
		t.Fatal("不同 URL 撞到同一个缓存文件")
	}
	if filepath.Dir(a) != dir {
		t.Fatalf("文件没有落在缓存目录里：%q", a)
	}
	base := strings.TrimSuffix(filepath.Base(a), diskExt)
	if len(base) != 32 || strings.ContainsAny(base, `/\?:*"<>|`) {
		t.Fatalf("文件名不是干净的哈希：%q", base)
	}
}

// 写进去的字节要能原样读出来——这是整个磁盘层的地基。
func TestDiskWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	url := "https://example.invalid/face/a.jpg"
	data := pngBytes(t)

	c := testDiskCache(dir)
	c.writeDiskFile(dir, url, data)

	got, ok := readDiskFile(diskPath(dir, url))
	if !ok {
		t.Fatal("写进去的文件读不出来")
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("读回来的字节和写进去的不一样：%d vs %d", len(got), len(data))
	}
}

// 坏文件要删掉并当作未命中，而不是每次启动都白读一遍。
func TestReadDiskFileDropsCorrupt(t *testing.T) {
	dir := t.TempDir()

	empty := filepath.Join(dir, "empty"+diskExt)
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDiskFile(empty); ok {
		t.Fatal("空文件被当成命中")
	}
	if _, err := os.Stat(empty); !os.IsNotExist(err) {
		t.Fatal("空文件没有被删掉")
	}

	huge := filepath.Join(dir, "huge"+diskExt)
	if err := os.WriteFile(huge, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(huge, maxDiskFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, ok := readDiskFile(huge); ok {
		t.Fatal("超大文件被当成命中")
	}
	if _, err := os.Stat(huge); !os.IsNotExist(err) {
		t.Fatal("超大文件没有被删掉")
	}
}

// 超限后按 mtime 从旧到新删，删到水位以下就停。
//
// 用 Truncate 造稀疏文件，90MB 的目录不真占 90MB 磁盘。
func TestSweepDiskEvictsOldest(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()

	mk := func(name string, size int64, age time.Duration) string {
		p := filepath.Join(dir, name+diskExt)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(p, size); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
		return p
	}

	const mb = 1 << 20
	oldest := mk("a", 30*mb, 3*time.Hour)
	middle := mk("b", 30*mb, 2*time.Hour)
	newest := mk("c", 30*mb, time.Hour)

	sweepDisk(dir)

	// 90MB 超过 64MB，删到 80%（51.2MB）：删掉两个最旧的，留一个。
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Fatal("最旧的文件没有被删")
	}
	if _, err := os.Stat(middle); !os.IsNotExist(err) {
		t.Fatal("第二旧的文件没有被删")
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatal("最新的文件被误删了")
	}
}

// 没超限就一个都不删，目录里的非缓存文件也不许碰。
func TestSweepDiskKeepsUnderLimit(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "a"+diskExt)
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(other, []byte("不要删我"), 0o644); err != nil {
		t.Fatal(err)
	}

	sweepDisk(dir)

	for _, p := range []string{keep, other} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s 被误删了", filepath.Base(p))
		}
	}
}

// 头像走磁盘：第一轮下载并落盘，第二轮换一个实例（等价于重启）不再走网络。
func TestAvatarLoadsFromDiskWithoutNetwork(t *testing.T) {
	data := pngBytes(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	url := srv.URL + "/face.png"

	first := testDiskCache(dir)
	first.Avatar(url, 40)
	waitFor(t, "第一轮下载", func() bool {
		first.mu.Lock()
		defer first.mu.Unlock()
		_, ok := first.raw[url]
		return ok
	})
	waitFor(t, "头像落盘", func() bool {
		_, err := os.Stat(diskPath(dir, url))
		return err == nil
	})
	if got := hits.Load(); got != 1 {
		t.Fatalf("第一次加载发了 %d 次请求，期望 1 次", got)
	}

	// 重启：新实例、同一缓存目录、不碰网络
	path := diskPath(dir, url)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	second := testDiskCache(dir)
	second.Avatar(url, 40)
	waitFor(t, "第二轮从磁盘读", func() bool {
		second.mu.Lock()
		defer second.mu.Unlock()
		_, ok := second.raw[url]
		return ok
	})
	if got := hits.Load(); got != 1 {
		t.Fatalf("磁盘命中之后又发了请求：总数 %d", got)
	}

	// 磁盘命中后不该把同一份字节写回去。写回是异步的，给一个明确窗口：
	// 期间 mtime 一变就说明又写了一遍。
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.ModTime().Equal(before.ModTime()) {
			t.Fatal("磁盘命中之后又把文件重写了一遍")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 封面（Bitmap）不落盘：直播封面是实时画面，缓存下来命中的是过期图。
func TestBitmapDoesNotWriteDisk(t *testing.T) {
	data := pngBytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	url := srv.URL + "/cover.png"

	c := testDiskCache(dir)
	c.Bitmap(url, 320)
	waitFor(t, "封面下载", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		_, ok := c.raw[url]
		return ok
	})

	// 解码与写盘都是异步的，给它们一个明确的上限；出现就算失败。
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(diskPath(dir, url)); err == nil {
			t.Fatal("封面被写进了磁盘缓存")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
