# 0001 播放器后端

- **状态**：已决定，已落地（2026-11-16 接入播放页）
- **实现方式已取代**：两个 ffmpeg 子进程 → cgo 进程内 libav（[0003](0003-libav-in-process.md)）→ libmpv（[0004](0004-libmpv.md)）。方向不变，本文的取舍与上游调研仍然有效
- **日期**：2026-11-16

## 背景

用户在比 potplayer。它除了网络原因几乎不卡、画质清晰、开流即出画；我们在 MyGo 上做的东西
首帧慢、直播延迟大、偶尔卡住等缓冲。

差异不在参数上，在结构上。potplayer 的路径是：

```
URL → libavformat 解封装 → libavcodec 硬解 → D3D11 纹理直出
```

一次解封装、一次解码、零拷贝上屏，时钟和缓冲全在自己手里。

而我们在 Windows 上的默认路径是把流交给 MyGo 的 `ui/video`，底层是 Media Foundation：

```
上游 → 代理/转封装 → 本地 HTTP HLS → MF HLS source（二次解封装）→ MF 解码 → MF 呈现
```

**MF 的 HLS source 是为点播设计的。** 它要等播放列表稳定、等分片可寻址、缓冲水位保守。
2–3 秒一片 × 缓冲 2–3 片，就是 6–9 秒的延迟底噪；水位被抽干就重新缓冲，用户看到的就是"卡住等"。

关键在于：**这条路上我们改不了它。** `ui.VideoPlayer` 只暴露只读的 `Buffering()`，
`media.Engine` 接口里没有任何缓冲水位、低延迟、预读长度的入口。这不是调参能解决的，
是设计目标不同。

## 候选

### A. 继续用系统解码（现状）

- 优点：零维护，视频播放的所有边界情况由系统兜底；代理路径首帧最快（1.4s）
- 代价：延迟和缓冲不受控；系统没有 HEVC/AV1 解码器时只能转码（画质有损、首帧 3.8s 起）

### B. 自带解码，自己做呈现（`internal/playback`）

- 优点：不依赖系统解码器；不转码所以画质等于源码率；时钟、丢帧、缓冲全在自己手里，
  延迟可以做到 1 秒以内；和 potplayer 同构
- 代价：呈现要走 CPU 上传（非零拷贝）。当时的代码是 `ui.Bitmap.UpdateYUV`，
  现在的播放页走 `scene.SurfaceBGRA`，观测墙走 D3D11 共享纹理

### C. 借一个成熟引擎（libmpv）

- 优点：potplayer 那一类实现，一次解封装、硬解、自带渲染，边界情况成熟
- 代价：**上游不给接口**，当时做不了（见下）。后来上游用 `media/`、`scene/` 别名包和
  `ui/engine_export.go` 把这层打开了，C 才走通（见 [0004](0004-libmpv.md)）

## 上游调研（2026-11-16）

issue 状态以 GitHub 为准，这里记的是它对我们意味着什么：

| 位置 | 状态 | 结论 |
|---|---|---|
| `egoist/mygo#789` | 已关闭 | `ui/video` 用系统媒体引擎实现，即现在的 MF 路径 |
| `egoist/mygo#973` | 开放 | 作者原话：「not yet. `Bitmap.Update` uploads from cpu memory, so for now `av_hwframe_transfer_data` to bgra + `Bitmap.Update` is the way… importing a CVPixelBuffer/IOSurface or a d3d11 shared texture goes together with the platform decoders in #789」硬件帧直通还没做 |
| `egoist/mygo#124` | 开放，2026-10-06 起未合 | `plugins/video`，基于 libmpv，Windows 加载 `libmpv-2.dll` / `mpv-2.dll`，帧经 `paint.Image(bitmap, r, FillBox)` 呈现 |
| `egoist/mygo#105` | 路线图 | 第 16 条「Specialized content plugins… Native-view hosting」未勾选 |
| `docs/ui/video.md` | 文档 | App 自解码直播流走 `Bitmap.UpdateYUV` |

另外两条当时的硬约束，现在仍成立：Windows 没有原生视图嵌入
（`platform.ViewSurface.EmbedView` 只有 darwin / mobile 实现）；
`ui.VideoDecoder` 只覆盖画面帧，不含声音与时钟。

## 决策

**走 B，同时把 A 的迁移路径留好。**

理由：C 做不了（上游没接口），A 有天花板（延迟和缓冲不受控，且系统解不了就退化成转码）。
B 是当时唯一能动手、且方向正确的一条。

## 落地时翻过的车

B 落地后 1080p60 只有约 50fps。当时的判断是**瓶颈在上屏**——CPU 上传太慢。逐层排掉
之后发现是三个自己的实现错误叠在一起：探测窗口压到 32KB 导致缺参考帧、呈现时刻用
「帧序号 × 声明帧时长」推算、解码进程直接读 CDN 没有任何缓冲。修完 1080p60 零丢帧。

**上屏从来不是瓶颈。** 记这一条是因为它教的东西比结论重要：先怀疑自己的实现，
再怀疑架构。

这条线上后来又拐了两次（libav → libmpv，两小时内），完整过程在
[evolution.md](../evolution.md) 第一条。

## 后果

- 我们要自己维护播放的边界情况：时钟漂移、丢帧、音画同步、断流重连。
  MF 帮我们挡掉的那些事，得有人扛——B 的时候全在自己手里，换了 libmpv 之后
  一部分交回给了引擎（见 [0004](0004-libmpv.md)）。
- 呈现比 potplayer 的零拷贝多一次拷贝。播放页到今天仍是单平面 bgr0 上传；
  观测墙 2026-11-23 起走 D3D11 共享纹理，那条是零拷贝。数字见
  [measurements.md](../reference/measurements.md)。

## 复查条件

- `egoist/mygo#124` 合并，或上游开放了引擎接口 / 原生视图托管
- MyGo 给 `Bitmap` 增加了 GPU 侧上传路径（`#973` 的硬件帧直通落地）