package media

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 头像的磁盘缓存。只服务 Avatar：封面是实时画面，落盘命中的是过期图。
//
// 这一层解决的是「重启之后还在」：L1/L2 都是内存，进程一退就空了。头像
// 几个月不换一次，每次启动都重下一遍没有意义，进关注页还要先闪一排字母占位。
//
// 存的是原始字节而不是解码后的纹理：同一张头像会被 26/32/34/40 四种尺寸
// 请求，存字节只占一份，解码时照旧按目标宽度缩放。几十 KB 一张，几百个
// 头像也就十几 MB。
const (
	// maxDiskBytes 是磁盘缓存的上限。超出后按 mtime 从旧到新删。
	//
	// 64MB 够放一两千张头像——远超正常用户关注量的一个数量级，又不至于
	// 让缓存目录失控。
	maxDiskBytes = 64 << 20

	// diskLowWaterNum/Den 是淘汰水位：超限后删到上限的 4/5（80%）。
	//
	// 删到刚好 64MB 的话，下一次写入又会超限、又要全目录扫一遍。留一段
	// 余量，扫描就从「每次写入」退化成「偶尔一次」。
	diskLowWaterNum = 4
	diskLowWaterDen = 5

	// diskScanEvery 限制目录扫描频率。写盘本身很便宜，扫目录不是。
	diskScanEvery = 30 * time.Second

	// maxDiskFileBytes 是单张图的合理上限，和网络读取的 8MB 对齐。
	// 超过就当文件坏了删掉——正常头像不可能有这么大。
	maxDiskFileBytes = 8 << 20

	// diskExt 是缓存文件后缀。只有这个后缀的文件参与容量统计和淘汰，
	// 目录里混进别的东西不会被误删。
	diskExt = ".img"

	// diskTmpLeft 是临时文件的最长存活时间。写入是 tmp + rename，正常不会
	// 留下中间状态；进程在两步之间被杀才会剩下 .tmp，扫目录时顺手清掉。
	diskTmpLeft = time.Hour
)

// diskPath 把 URL 换成缓存文件名。
//
// 用哈希而不是原 URL：URL 里有 `?`、`/`、签名参数，直接当路径会写出目录
// 之外；同时哈希也不把房间号之类的东西摆进文件名。
func diskPath(dir, url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(dir, hex.EncodeToString(sum[:16])+diskExt)
}

// loadDisk 是 Avatar 的加载起点：磁盘命中就直接解码，没有再走网络。
func (c *Cache) loadDisk(key, url string, target int, dir string) {
	if data, ok := readDiskFile(diskPath(dir, url)); ok {
		// 字节已经躺在盘上了，这次解码不必再写回去。
		//
		// 不清这个标记的话，decodeRaw 成功后会按 wanted 把同一份字节重写
		// 一遍：内容没变，mtime 却每次启动都刷新，缓存看上去像是一直在失效。
		c.mu.Lock()
		delete(c.wanted, url)
		c.mu.Unlock()

		// 顺手放进 L2：同一张头像换个尺寸再请求时不必回盘。
		c.putRaw(url, data)
		c.decodeRaw(key, url, target, data)
		return
	}
	c.fetch(key, url, target)
}

// readDiskFile 读一个缓存文件；坏文件（空、超大、读不出）删掉并当作未命中。
//
// 删掉而不是留着：内容坏了下次还会坏，留在那儿只是让每次启动都白读一遍。
func readDiskFile(path string) ([]byte, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > maxDiskFileBytes {
		_ = os.Remove(path)
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		_ = os.Remove(path)
		return nil, false
	}
	return data, true
}

// persistData 把原始字节写进磁盘缓存，只对请求过 Avatar 的 URL 生效。
//
// 调用点在解码成功之后：解不出来的字节（比如 CDN 转出来的动画 WebP）写
// 下去只会让下次启动再失败一遍。
func (c *Cache) persistData(url string, data []byte) {
	c.mu.Lock()
	want := c.wanted[url]
	delete(c.wanted, url)
	dir := c.diskDir
	c.mu.Unlock()
	if !want || dir == "" || len(data) == 0 || len(data) > maxDiskFileBytes {
		return
	}
	go c.writeDiskFile(dir, url, data)
}

// writeDiskFile 先写临时文件再 rename，和 state.json 同一套：
// 写到一半退出也不会留下半个图片文件。
func (c *Cache) writeDiskFile(dir, url string, data []byte) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	path := diskPath(dir, url)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	c.maybeSweepDisk()
}

// maybeSweepDisk 在写入之后按节流触发一次容量清理。
func (c *Cache) maybeSweepDisk() {
	c.mu.Lock()
	dir := c.diskDir
	c.mu.Unlock()
	if dir == "" {
		return
	}

	c.diskMu.Lock()
	if time.Since(c.diskAt) < diskScanEvery {
		c.diskMu.Unlock()
		return
	}
	c.diskAt = time.Now()
	c.diskMu.Unlock()

	sweepDisk(dir)
}

// sweepDisk 把缓存目录压到水位以下：按 mtime 从旧到新删。
//
// 只看 .img：目录里可能还有正在写的 .tmp 和别的东西，误删会打断写入。
func sweepDisk(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var files []entry
	var total int64
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		info, err := e.Info()
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(name, diskExt):
			files = append(files, entry{filepath.Join(dir, name), info.Size(), info.ModTime()})
			total += info.Size()
		case strings.HasSuffix(name, ".tmp") && now.Sub(info.ModTime()) > diskTmpLeft:
			// 崩溃留下的半成品，清掉免得一直占着位置
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	if total <= maxDiskBytes {
		return
	}
	low := int64(maxDiskBytes) * diskLowWaterNum / diskLowWaterDen
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= low {
			break
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
		}
	}
}
