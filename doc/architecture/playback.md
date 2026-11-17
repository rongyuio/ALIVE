# 播放链路

一条直播从「用户点了卡片」到「画面上屏」。实测数字见
[reference/measurements.md](../reference/measurements.md)，为什么用 libmpv 见
[decisions/0004](../decisions/0004-libmpv.md)。

## 分层职责

```text
平台接口（internal/live）
  └─ 真实档位、编码、线路、签名 URL、回源请求头
       ↓ 只解析本次要播的档位；菜单可含尚未解析的项
播放会话（internal/ui/player_media.go）
  └─ 起播代次、取消、首帧观测、一次回退、错误终态
  └─ 后台收听：vid=no 留声音，回来时 vid=auto（见下）
       ↓
播放引擎（internal/playback）—— 默认，也是唯一的主路径
  └─ 一个薄壳：把 URL 与 headers 交给 libmpv，把 bgr0 帧包成 media.Picture
       ↓
libmpv（third_party/mygo/plugins/video，purego 运行时绑定）
  ├─ 解封装、解码、缓冲、时钟、音画同步、丢帧策略
  ├─ 音频直接输出（WASAPI），我们不碰 PCM
  └─ 软件渲染（MPV_RENDER_API_TYPE_SW）→ bgr0 → 三缓冲复用
       ↓
呈现（MyGo）
  └─ scene.SurfaceBGRA → GPU 采样；截图与 CPU 渲染器走 Picture.RGBA
```

平台层不决定系统能否解码，播放器不拼接平台协议。

**没有兜底路径。** `internal/relay`（透明代理 / 转封装 / 转码）和 `internal/ffmpeg`
已于 2026-11-16 删除。它们的触发面实际只有「找不到 `libmpv-2.dll`」——而那属于安装
损坏，relay 依赖的 ffmpeg 组件同样会缺，兜底在最需要它的时候和主路径一起坏。
现在 `playback.Open` 失败就直接落到可重试的错误终态，界面说清原因，用户点重试或
切线路。`ALIVE_NATIVE` 开关随之删除。

## mpv 负责什么，我们负责什么

这是这套架构最要紧的一条分工线。写过一版自研引擎（[0003](../decisions/0003-libav-in-process.md)）
之后才看清：**问题从来不在解码，在调度。**

mpv 负责，我们一行都不写：

- 解封装、探测、读取节奏、HLS/FLV 分支
- 音频解码与输出（含重采样、设备选择）
- 主时钟、音画同步、追帧与丢帧
- 缓冲水位（`buffering` 状态真的表示在等数据）

我们负责，只有三件：

1. **回源请求头。** `Options.Headers` → `mpv_set_option_string(h, "http-header-fields", ...)`。
   四个平台几乎都校验 Referer/UA，缺了直接 403。mpv 自己不会编这些。
2. **渲染尺寸。** 按 `dwidth`/`dheight` 的属性事件走（这两个已经算进旋转与像素宽高比，
   竖屏直播要靠它），上限 4K。事件还没到之前用平台声明值顶着，免得第一帧先按
   640×360 渲染再跳变。
3. **CPU 路径的 alpha。** `bgr0` 的第四个字节是不确定值，CPU 光栅化按预乘 alpha 混合
   就是整块透明。见下。

## 渲染：为什么是 bgr0，以及那个 alpha 陷阱

mpv 的软件渲染只接受 RGB 单平面格式（`render.h` 里列得很清楚），**NV12 传过去是
`MPV_ERROR_NOT_SUPPORTED`**。我们选 `bgr0`，每像素 4 字节，交给 `scene.SurfaceBGRA`。

⚠️ **`bgr0` 里那个 `0` 字节是垃圾值，不是 alpha。** 实测为 0。真实窗口走 GPU 采样，
着色器不读 alpha，画面完全正常；而 CPU 光栅化（截图验收、没有 GPU 时的渲染器）按
预乘 alpha 混合，`alpha=0` 等于整块全透明——播放区纯黑，UI 其余部分正常。

症状是「同一份帧，屏幕上有画面，截出来全黑」，极容易被误判成解码失败。所以
`internal/playback` 的 `RGBA` 不走 `Surface.RGBA`，自己转一遍，alpha 一律 255。

`live_test.go` 把这条变成了断言：CPU 转换必须有内容，且 alpha 全 255。

另有一处 mygo 侧的修复：`raster.sampleOp` 采样前要先 `Image.Pixels()`。GPU 上传后会
`ReleasePixels` 丢掉 CPU 像素、只留一个 `Restore`，而采样路径直接读 `Pix`，读到空
切片就当透明像素——又一类「看起来没画」的纯黑，且只在 CPU 路径出现。

## 缓冲与延迟

不再有我们自己设的 PCM 环、帧槽、预缓冲。这些数现在归 mpv：默认 `cache=auto`，
直播流读前量为秒级。我们只调两处，都是实测出来的：

| 选项 | 值 | 依据 |
|---|---|---|
| `scale` / `dscale` | `bilinear` | 默认 bicubic 下 1080p60 只有 41.5fps；改完 59.3fps |
| `sws-fast` | `yes` | 官方文档推荐，SW 渲染的色彩转换走快速路径 |
| `hwdec` | `auto-safe` | 解出来的帧要下到内存再转 RGB，但解码本身硬解仍然快得多 |

`MPV_RENDER_PARAM_BLOCK_FOR_TARGET_TIME` 关掉：渲染由 render context 的更新回调驱动，
再在这里等一次目标时间只会把画面推后一帧。

## 首帧

模型变了：不再有「取流 + 850ms × 拉流次数」。mpv 只连一次、只探一次。

> **首帧 = 平台取流 + mpv 打开流与出第一帧。**

实测（2026-11-16，本机，四个平台 49 个档位）：

| 场景 | 首帧 |
|---|---:|
| 真实窗口，B 站 1080P 原画 | 413ms（其中取流约 350ms） |
| 引擎层，B 站 1080P 原画 | 22ms |
| 引擎层，抖音 1440p | 13ms |
| 引擎层，虎牙 1440p | 13ms |
| 引擎层，斗鱼 1080P60 | 15~490ms（看档位地址冷热） |

mpv 自己出帧是十几到几十毫秒量级。起播的大头在平台取流，不在播放。

## 地址会过期：提前换一份

平台的签名地址有寿命。斗鱼的直链写着 `expire=300`，到 300 秒 CDN 主动断开连接，
mpv 把「连接被关」当成文件播完——`phase=ended`，画面停在最后一帧，没有错误、没有
缓冲提示、也没有声音。这不是卡顿，是地址到期（实测与网页端的处理见
[reference/measurements.md](../reference/measurements.md)）。

`live.Variant.Expires` 把失效时刻带出来，播放层据此提前 60 秒换地址：

```
到期前 60s → 重新取流（同档位、同线路）→ 开第二路 → 等它出图 → 主线程切过去
                                                              └─ 旧的关掉
```

新的一路先出图再切，屏幕上不会经过加载态；切换那一刻画面连续，只有声音有一次
几百毫秒的接缝。网页端同样要付这个代价，它的做法是 `destroy` + 重新取流。

到期没换成功（取流失败、新地址不出图），或者这一路先停了——播放器自己 `ended`、
或者时间轴 10 秒不动——走同一条路兜底；连试两次都失败才退回整轮重来。重试有
退避，不会每帧都重开一路。

## 后台收听：离开直播间，声音留着

离开播放页默认**不关播放**。左上角的叉和 Esc 是明确退出，其余离开方式
（顶栏 tab、灵动岛、搜索跳转）都进后台收听：引擎活着、声音继续、视频轨关掉。

### 靠的是 mpv 里音频和画面本来就分开

`vid=no` 关掉视频轨之后，mpv 的音频输出在自己的线程上照常走，`time-pos`
（由音频驱动，`video-sync=audio`）继续前进，而 render context 不再唤醒窗口。
实测斗鱼 9999：关掉视频轨后一秒推进 1.010 秒，渲染序号停住
（[measurements](../reference/measurements.md)）。

所以这一态不需要碰 `VideoPlayer` 的 hold 逻辑，也不需要假窗口。它只做两件事：
`vid=no`，以及**别把看门狗一起停掉**。

### 看门狗不能挂在视图上

`trackFirstFrame` 原来由 `PlayerPage` 每帧调用。后台态页面不构建，它就死了——
而斗鱼的直链签 300 秒，没人续期，五分钟后声音自己断。所以它挪到了 `Shell`
的常驻心跳（`mediaTick`），判据从「在播放页」改成「有引擎」。

后台态下没有视频元素在请求帧，所以 `kickMediaTick` 另排一条 250ms 的定时器
驱动它。这一条顺带修掉一个既有 bug：主窗口最小化超过五分钟，音频也会断。

### 后台态的健康判据是音频时钟

`Ready` / `Frame` 在 `vid=no` 之后不再说明任何事。后台态改用 `Position()`：
6 秒没有一毫秒推进才算断了。**首帧预算在后台态不适用**——画面本来就不该有，
用画面判死会把一路好好出声的流关掉（`TestBackgroundIgnoresFirstFrameBudget`）。

### 换地址要换判据

`runHotSwap` 原来等新一路出图再切。后台态下 `Frame()` 永远不给画面，这条
判据会 100% 失败，所以 `swapRequest.background` 让它在后台态改等 `Position() > 0`。
换上去之后还要补一次 `vid=no`：新引擎的视频轨默认是开的。

### 回来要等关键帧

`vid=auto` 之后 mpv 从下一个关键帧开始解码。斗鱼实测中位数 **1.67 秒**
（三轮：2.58 / 1.66 / 1.67）。这段画面停在离开时那一帧，属于已知代价——
它随平台 GOP 变，不随档位变。首帧预算在 `attachMedia` 里从这一刻重新起算。

### 全局仍然只有一个引擎

后台收听让「切台」这条路径变多，但不变的是：**两路 mpv 同时开会抢 WASAPI，
`video-sync=audio` 的时钟停住，整路冻死**（见下面「一次性资源与线程」）。
从后台收听点开另一个直播间时，`ensureMedia` 发现 `roomKey` 变了，走
`reset` → `stopPlayback` → 重开一路，旧引擎在 `stopPlayback` 里关掉。

## 什么时候会慢，以及怎么区分

慢有两种，必须先分清：

1. **上游供给慢。** mpv 会报 `buffering=true`，并按实时速度慢放（因为它拿不到数据）。
   这不是 bug。判据：同一份签名地址用 ffmpeg 命令行解，8 秒流要跑十几秒、`speed`
   小于 1x。`internal/playback/ffmpeg_test.go` 就是干这个的，URL 带签名所以不打印。
2. **我们自己的问题。** 表现是解码/上屏帧率低于 mpv 给的，或者 `State()` 卡在
   `Loading`。这时候才该改代码。

另一种少见但真实的情况是上游返回 403 / `loading failed (-13)`：地址过期或档位不可用，
属于错误终态。

## 一次性资源与线程

- 每个 mpv 实例有自己的 AO 与 render context。**关闭是异步的**（不能阻塞 UI 线程），
  所以下一个实例创建前要等 `Done()`。不等的话上一个还占着 WASAPI，下一个打不开音频
  设备，`video-sync=audio` 的时钟就停住，整路冻在原地——实测这个坑会让 5 个档位集体
  卡死，等 `Done()` 之后降到 1 个（那 1 个是上游问题）。
- render context 的回调在 mpv 的线程上，只往里塞一个 channel，不碰任何 mpv API 与 UI。
- 帧缓冲三块轮换：渲染在 mpv 线程、上屏在 UI 线程、中间还隔着 GPU 的异步采样。只用
  一块，等于在 GPU 读它的同时让渲染线程写它——屏幕上就是两帧叠在一起。

## 日志

- 用 `id` 关联一轮起播，`ms` 相对本轮起点。
- 首帧可用表示第一帧交给呈现端，不是精确的屏幕呈现时刻。
- 签名地址与本地令牌不进入诊断。

## 验收要求

**一台干净 Windows：只装 alive，不装 FFmpeg、不装 HEVC/AV1 扩展。**

- 四个平台各取若干房间，点开即出画出声，首帧 5 秒内
- 1080P60 档位稳定 59fps 上下，零丢帧
- 连续播放 10 分钟不断音、不停顿
- 退出后无残留进程、无残留句柄

工程检查：

```sh
make check        # gofmt + build + vet + mygo vet + test
```

跨平台矩阵与上游对照（都要连公网，默认跳过）。这两个测试不进仓库——脚本在
`.tmp/probes/playback/`，跑之前先拷回 `internal/playback/`：

```sh
$env:ALIVE_MATRIX="1"; $env:ALIVE_MATRIX_PLATFORMS="bilibili,douyu,huya,douyin"
go test ./internal/playback -run TestMatrix -v -count=1 -timeout 900s

$env:ALIVE_CTRL_ROOM="3428783"; $env:ALIVE_CTRL_PLATFORM="bilibili"
go test ./internal/playback -run TestFfmpegControl -v -count=1
```