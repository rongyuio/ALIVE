# internal/playback

用 libmpv 播放直播流，做成 MyGo 的 `media.Engine`。这一层是薄壳：真正的播放全在 mpv 里。

- 为什么是 libmpv（含放弃自研引擎的实测数据）→ [doc/decisions/0004](../../doc/decisions/0004-libmpv.md)
- 在整条播放链路里的位置、bgr0 的 alpha 陷阱 → [doc/architecture/playback.md](../../doc/architecture/playback.md)
- 首帧与矩阵实测 → [doc/reference/measurements.md](../../doc/reference/measurements.md)
- 上游 mpv 报名 → [third_party/mpv/README.md](../../third_party/mpv/README.md)

本文件只写接口和实现约定。

## 接口

```go
type Options struct {
    Headers   []string // 回源请求头，例如 "Referer: https://live.bilibili.com/"
    Width     int      // 平台声明尺寸，仅作兜底
    Height    int
    FrameRate float64
}

func Open(ctx context.Context, upstream string, opts Options) (*Engine, error)
```

`Open` 返回时 mpv 已经收到 `loadfile`，首帧要等 `Frame` 第一次给出画面。
找不到 `libmpv-2.dll`、或 mpv 不接受这条流时返回 `ErrUnsupported`，调用方据此落到
可重试的错误终态。

`Frame(t)` 的 `changed` 来自 mpv 的渲染序号：同一帧被重复渲染不会重复通知上屏。

## 只干三件事

1. **headers** → `http-header-fields`。四个平台都校验 Referer/UA，缺了直接 403。
2. **渲染尺寸** → 按 `dwidth/dheight` 的属性事件，上限 4K。事件没到之前用平台声明值顶着。
3. **CPU 路径的 alpha** → `bgr0` 第四个字节是垃圾值（实测 0），GPU 采样不看它、CPU 光栅化
   按预乘 alpha 混合就是整块透明。所以 `RGBA` 自己转一遍，alpha 一律 255。

其余（解封装、解码、缓冲、时钟、音画同步、丢帧、音频输出）全交给 mpv。**不要再往里加
调度逻辑**——上一版自研引擎就是在这里把自己卡死的。

## 库文件

`libmpv-2.dll` 在 `third_party/mpv/bin/`，由 `mygo.json` 的 `resources` 打进包。
找不到可执行文件旁边的库时，`ALIVE_MPV_LIB` 可指定绝对路径（测试用）。

## 测试

```sh
make test    # recipe 里带 ALIVE_MPV_LIB
```

| 测试 | 需要 | 干什么 |
|---|---|---|
| `TestLiveEngine` | `ALIVE_LIVE_ROOM` + `ALIVE_LIVE_PLATFORM` | 真实流端到端：首帧、音频连续、**CPU 转换必须有内容且 alpha 全 255** |
| `TestMatrix` | `ALIVE_MATRIX=1` | 跨平台 × 分区 × 房间 × 档位，输出首帧/解码/上屏/丢帧/断供 |
| `TestFfmpegControl` | `ALIVE_CTRL_ROOM` + `ALIVE_CTRL_PLATFORM` | 同一份签名地址跑 ffmpeg 命令行，判断慢是上游还是我们 |

后两个会连公网，默认跳过。`TestFfmpegControl` 不打印地址——它带签名。