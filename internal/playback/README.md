# internal/playback

用 libmpv 播放直播流，做成 MyGo 的 `media.Engine`。这一层是薄壳：真正的播放全在 mpv 里。

bgr0 的 alpha 陷阱见下方「只干三件事」；上游 mpv 说明见
[third_party/mpv/README.md](../../third_party/mpv/README.md)。

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

本项目自有的 `_test.go` 目前在瘦身中整体移除，包里没有测试。要补纯逻辑测试（音频门控、
CPU 采样）就写 `_test.go`，不需要网络也不需要真实流。

手动的真实流 / 基准测量不进仓库——真实流端到端（首帧、音频连续、**CPU 转换必须有内容
且 alpha 全 255**）、跨平台矩阵、同一份签名地址跑 ffmpeg 命令行（判断慢是上游还是我们）
这类脚本放 `.tmp/probes/playback/`，跑之前拷回本目录。它们要连公网或要本地推流，不进
`make check`。
