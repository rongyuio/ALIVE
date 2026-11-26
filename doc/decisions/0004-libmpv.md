# 0004 播放实现换成 libmpv

- **状态**：已决定，已落地（2026-11-16）
- **日期**：2026-11-16
- **取代**：[0003](0003-libav-in-process.md) 的实现方式。方向仍是「自带解码、不交给系统」，
  换掉的是引擎：cgo 直调 libav 自己写调度 → libmpv。
- **影响**：`internal/playback`、`third_party/mygo/plugins/video`、`third_party/mpv`、
  `third_party/mygo/internal/raster`、`mygo.json`、`Makefile`
- **这条线**：[evolution.md](../evolution.md) 第一条，也是它的终点。

## 背景

[0003](0003-libav-in-process.md) 用 cgo 直调 libav，把解封装、解码、重采样、PCM 环、
视频帧槽、内容时钟全写了一遍。它解决了几件事（音视频同轴、反压只加在音频、首帧
零点取关键帧），但走到最后卡在**调度**上，而且越修越糟。

实测（2026-11-16，虎牙原画 1080p，同一台机器、同一时刻）：

| 实现 | 结果 |
|---|---|
| ffmpeg 命令行解同一地址 | **1.32 秒跑完 8 秒流，477 帧，speed 18.3x** |
| 我们自己的引擎 | 12.6fps，8 秒 101 帧 |
| 诊断：解码线程等空帧槽 | **9.976 秒 / 8 秒采样** |
| 诊断：内容时钟选不出帧 | **433 次 / 500 次调用** |

上游供给完全没问题。是我们的调度层在咬自己：解码线程几乎全程阻塞在拿帧槽上，
而帧槽的释放又取决于音频时钟，音频一饿死时钟就落后，于是连锁反应。

这一轮为了修它做过的事：时钟兜底、槽回收延迟、丢包记账、flush、等关键帧、PCM 环
深度。每一条都对应 mpv 内部早就解决了的一个机制，而且有几条改错了方向（等关键帧
靠 `AV_PKT_FLAG_KEY`，在 HLS fMP4 上恒为 false，直接把画面等黑）。

ry 的判断在这件事上是对的：**Go 不好做的、或者有更成熟更方便的方案，就该换。**

## 候选

### A. 继续修自研引擎

- 优点：代码在自己手里，没有新依赖
- 代价：已经在改第五个补丁，每次修都牵动时钟/槽/反压三者；实测最好也只到 59fps
  且边界情况（高码率档、音频设备争用）反复出问题

### B. 接 libmpv

- 优点：potplayer/VLC/ffplay 同源；时钟、缓冲、音画同步、硬解、丢帧策略全在里面；
  我们只负责任务边界（取流、headers、拿帧上屏）
- 代价：软件渲染是**单线程 CPU**（官方原话「this method of rendering is very slow」）；
  只接受 RGB 格式；116MB DLL

### C. 等上游 mygo#124 合并

- 优点：不用自己维护插件
- 代价：那个 PR 是作者自己在 2026-10-06 提的，**提交后当天就搁置**，零人工 review，
  现在 `mergeable: CONFLICTING`。而且它对我们的场景开箱不可用（见下）

## 决策

**走 B，并且照抄 #124 的骨架进 `third_party/mygo/plugins/video`，改掉它对我们不可用的地方。**

#124 的状态机、事件路由、线程模型（414 行 `player.go`）写得细，比重写省事；但它的
渲染路径与接口对直播客户端是错的，逐条改掉：

| #124 的问题 | 后果 | 改法 |
|---|---|---|
| `Load(source string)` 没有任何 header 入口 | 四个平台全要 Referer/UA，开箱直接 403 | `Options.Headers` → `http-header-fields` |
| 默认渲染 1280×720，`resize` 再按上限等比压 | 永远拿不到原画 | 按 `dwidth/dheight` 属性事件渲染，上限 4K |
| 每帧 `make([]byte, stride*h)` RGB | 1080p 8.3MB/帧，60fps = 500MB/s 分配 | 三缓冲轮换复用 |
| 逐像素纯 Go 写 alpha | 1080p 每帧 200 万次写 | 删掉（alpha 在转换时统一给 255） |
| `{"hwdec", "no"}` 写死 | 硬解全关 | 2026-11-21 之前一直是 `no`；当天后台改「保留解码、只停全尺寸呈现」后重测，生产值改为 `auto-safe`（后台 6.4–8.9%，前台持平或更好），见 [0009](0009-live-continuity.md) |
| `renderUpdate()` 返回值丢弃 | 没有新画面也照拿输出缓冲 → 拿到未写过的空内存 | 只看 `MPV_RENDER_UPDATE_FRAME` |

另外两处是实测出来的，不是读代码看出来的：

- **`scale`/`dscale` 改成 `bilinear` + `sws-fast`**：默认 bicubic 下 1080p60 只有
  41.5fps，改完 59.3fps 零丢帧。这条是 mpv 文档自己写的「setting sw-fast may help」。
- **调优选项失败不再致命**：`sw-fast` 在 `vo=libmpv` 下不存在，写死在关键选项列表里
  会让整个播放报 `option not found` 起不来。选项分组：关键项失败即报错，调优项失败忽略。

### 关于渲染格式：只能是 RGB

`render.h` 写得很明确，`MPV_RENDER_PARAM_SW_FORMAT` 只认 `rgb0`/`bgr0`/`0bgr`/`0rgb`
以及「internally marked as RGB, has exactly 1 plane」的其他格式。**NV12 两个平面，
传过去直接 `MPV_ERROR_NOT_SUPPORTED`**。

我们最终选 `bgr0`（单平面，4 字节/像素），通过 `scene.SurfaceBGRA` 上屏。

## 后果

- **软件渲染是单线程的，但 4K60 实测跑得满。** 1080p60 靠 bilinear + sws-fast 才跑到
  59.3fps；4K60 实测 59.9fps、1.61 核（2026-11-16，见
  [measurements](../reference/measurements.md)）。真正的成本是每帧约 27ms 的 CPU 时间——
  解码、YUV→RGB、上传全压在 CPU 上。2026-11-23 起观测墙改走 D3D11 render API 零拷贝
  （12 路 8.7 核 → 0.4 核，见 measurements「视频零拷贝」）：mpv 直接渲进共享纹理，
  播放页仍用软件渲染。零拷贝帧的 CPU 消费者（截图、CPU 光栅器）按需回读。
- **`bgr0` 的第四个字节是不确定值**（文档原话「contains uninitialized garbage」，实测 0）。
  真实窗口走 GPU 采样不看 alpha，所以画面正常；而 CPU 光栅化（截图验收、无 GPU 时的
  渲染器）按预乘 alpha 混合，alpha=0 等于整块透明 → 播放区纯黑。所以在 CPU 路径自己
  转一遍，alpha 一律 255；零拷贝帧的回读也一样。这个坑花了很久才定位：同一份帧，
  GPU 正常、截图全黑。
- **我们 fork 了 mygo 的一处核心代码**：`internal/raster/sampleOp` 在采样前先
  `Image.Pixels()`。GPU 上传后 `ReleasePixels` 会把 CPU 像素丢掉、只留一个 `Restore`，
  而采样路径直接读 `Pix`，读到空切片就画成透明。NV12 有专门分支不受影响，其它格式
  （包括我们的 BGRA）在 CPU 渲染器和截图里就是一块纯黑。
- **116MB DLL 随包**，安装包 41.6MB（LZMA 后）。比之前 5 个 ffmpeg DLL 的 47.2MB 还小。
- **播放层不再需要 cgo**：`CGO_ENABLED=0` 能编译，构建机不再需要 gcc，交叉编译恢复。
  这是意外收获——换掉 libav 之后 `third_party/ffmpeg` 的头文件与导入库成了死重量，
  已于 2026-11-16 从仓库删除，仓库瘦 143MB。
- 我们仍然要为「流给不给得动」负责：高码率档位上游慢时，mpv 会 `buffering=true` 慢放。
  这是真实行为，不是 bug，但需要能诊断——对照组是一次手工对照
  （`TestFfmpegControl` 已随探针移出仓库）。

## 复查条件

- 上游 mygo 开放 OpenGL 渲染上下文（D3D11 共享纹理那条 2026-11-23 已通，只用于观测墙）
- 需要把每帧约 27ms 的 CPU 开销拿掉（省电、风扇、弱机余量）。**4K60 本身软件渲染已经
  跑满**，GPU 互操作是优化项，不是「跑不动」的补救，依据见
  [measurements](../reference/measurements.md) 的「4K60 与 CPU 敏感度」
- 上游 #124 真的合并，并且补上了 headers 与渲染尺寸
- `third_party/mygo/plugins/video` 的骨架与上游偏离到无法再对照