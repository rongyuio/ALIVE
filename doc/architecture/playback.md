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
  └─ 后台收听：停上屏、留声音，视频轨不关（见下）
       ↓
播放引擎（internal/playback）—— 默认，也是唯一的主路径
  └─ 一个薄壳：把 URL 与 headers 交给 libmpv，把 bgr0 帧包成 media.Picture
       ↓
libmpv（third_party/mygo/plugins/video，purego 运行时绑定）
  ├─ 解封装、解码、缓冲、时钟、音画同步、丢帧策略
  ├─ 音频直接输出，后端由 mpv 按平台选，我们不碰 PCM
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
`TestRGBAOpaque` 把这条钉住：转换结果有内容，且 alpha 全 255。

**零拷贝帧同样要兑现这条。** 观测墙走 D3D11 零拷贝时，画面只住在共享纹理里，
`Picture.RGBA` 必须按需回读（插件里 `gpuTarget.readPixels`：拿 keyed mutex →
`CopyResource` 到 staging → `Map`，BGRA 换回 RGBA、alpha 一律 255），否则截图和
CPU 渲染器拿到的是空像素——真实窗口正常、截图里整格是底色，又是一次「同一份帧，
两条路径两种结果」。回读只在有人要像素时发生，不在渲染热路径上。
`TestGPUFramesConvertForCPU` 是这条的断言（本地合成源，`GPU: true`）。

另有一处 mygo 侧的修复：`raster.sampleOp` 采样前要先 `Image.Pixels()`。GPU 上传后会
`ReleasePixels` 丢掉 CPU 像素、只留一个 `Restore`，而采样路径直接读 `Pix`，读到空
切片就当透明像素——又一类「看起来没画」的纯黑，且只在 CPU 路径出现。

## 缓冲与延迟

不再有我们自己设的 PCM 环、帧槽、预缓冲。这些数现在归 mpv：默认 `cache=auto`，
直播流读前量为秒级。我们只调这几处，都是实测出来的：

| 选项 | 值 | 依据 |
|---|---|---|
| `scale` / `dscale` | `bilinear` | 默认 bicubic 下 1080p60 只有 41.5fps；改完 59.3fps |
| `sws-fast` | `yes` | 官方文档推荐，SW 渲染的色彩转换走快速路径 |
| `hwdec` | `auto-safe` | 后台保留解码后，硬解把后台从 21.7–33.4% 降到 6.4–8.9%；前台 22.6–23.3% vs 软件 23.2–49.5%，丢帧 0 |

> `hwdec` 在 2026-11-21 之前一直是 `no`（更早的提交信息误写成 auto-safe，当天按代码
> 改正过）。后台改成「保留解码、只停全尺寸呈现」之后重测：硬解后台 6.4–8.9%、
> 软件 21.7–33.4%；前台硬解 22.6–23.3%、软件 23.2–49.5%，丢帧/失时都是 0。
> 据此把生产值改为 `auto-safe`；驱动不支持时它会退回软件，不会起不来。

`MPV_RENDER_PARAM_BLOCK_FOR_TARGET_TIME` 关掉：渲染由 render context 的更新回调驱动，
再在这里等一次目标时间只会把画面推后一帧。

## 连续性：谁在断，怎么判断

直播的「短暂不连续」有五种来源，`Position` 和 `Buffering` 只能看出其中一种。
`third_party/mygo/plugins/video` 因此多观测了 `audio-pts`、`hwdec-current`、
`demuxer-cache-duration`、`cache-buffering-state`、`decoder-frame-drop-count`、
`mistimed-frame-count`、`estimated-vf-fps`、`audio/video-speed-correction`、
`total-avsync-change`，由 `video.State` 带出来。`playback.Engine.Debug()` 能直接读到。

判据与归因顺序见 [decisions/0009](../decisions/0009-live-continuity.md)，实测数字见
[reference/measurements.md](../reference/measurements.md) 的「直播连续性」一节。
生产日志只在状态翻转时写：`缓冲开始` / `缓冲结束` / `音频停走` / `视频停帧`，
正常播放不产生日志。

结论先写在这里，免得再被 `paused-for-cache` 一个信号带偏：

- **画面冻结时，音频 gap 不是独立故障。** 2026-11-21 B 站实测，50 次音频 gap 全部落在
  `paused-for-cache` 窗口内，`audio-pts` 自己一次都没停——数据没到，声音当然不走。
- **上游空档的长度决定冻结长度。** B 站 m3u8 是 7 个 1 秒分片；播放器稳态读前缓存
  中位数 1.67s，一次 3.3 秒的 CDN 空档就变成 4.06 秒冻结（空档 + 恢复迟滞）。
- **热切换的新一路是真的出了声才放行画面。** 三次实测首帧 258/663/391ms，
  放行时 `audio-pts` 已经大于 0，不是被 `audioGate` 的 2 秒预算兜出来的。

## 首帧

模型变了：不再有「取流 + 850ms × 拉流次数」。mpv 只连一次、只探一次。

> **首帧 = 平台取流 + mpv 打开流与出第一帧。**

2026-11-21 把这条拆成四段重测过：首帧的大头是平台取流（0.38–0.48s）与
mpv 连接/探流/第一声（0.35–1.29s），等关键帧只有 2–3ms——新连接从分片边界
起播，第一帧和第一声几乎同时到。数字见
[decisions/0009](../decisions/0009-live-continuity.md) 与
[measurements.md](../reference/measurements.md)。

实测（2026-11-16，Ryzen 7 9700X + RX 9070 XT，四个平台 49 个档位）：

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
或者时间轴 6 秒不动——走同一条路兜底；连试两次都失败才退回整轮重来。重试有
退避，不会每帧都重开一路。

### 换地址的凭据是「这一轮取流」，不是「播放器还在」

`playbackSession` 里有两样东西，它们**不同步**：

- `ctx` 是这一轮取流的存在凭据。`cancelSession`（换焦点、关直播间、closeMedia）
  第一件事就是把它置 nil、推进 `seq`。
- `m.eng()` 是焦点会话的播放器。它在 `cancelSession` 之后照旧活着——墙里的静音
  预览、一次焦点交接的中间态，都是这个形态。

所以在「ctx 没了、播放器还在」的窗口里，`mediaTick` 仍然会看到「播放器报告结束」
并派一次热切换。判据只能问 ctx，不能问 eng：**没有活会话就不该去要新地址**，
后台回调也只许用 `sess.expired()`（nil 也算作废）。

2026-11-25 的生产崩溃就是这个：`runHotSwap` 的失败回调对 nil ctx 调 `Err()`，
`addr=0x28`——那是 itab 里 Err 的槽位。修法是三条一起：

- `scheduleSwap` 用 `m.ctx == nil` 挡住没有活会话的换地址；
- 「这一轮还有效吗」收敛成一个判据 `playbackSession.expired()`（nil 也算作废），
  所有后台回调——`runHotSwap` 的失败路径、`applyMedia` 的接纳口、取流失败与
  等首帧循环里的取消判断——都只问它，谁都不许自己写 `ctx.Err()`；
- 回归测试 `internal/ui/player_swap_cancel_test.go` 四条：没有活会话不派换地址、
  作废会话的失败不计数（改前 panic）、活会话的失败仍然计数（防 `expired()`
  写成恒真）、`applyMedia` 丢掉作废会话的结果并走 stale 分支（改前 panic）。

### 从墙上带进来的那一枚播放器，也要有自己的一轮

上面那个窗口在旧实现里不是一瞬，而是常驻：从观测墙 / 墙上岛进播放页时，画面
是墙格自己开的播放器，没走 `beginSession`；`setFocus` 清掉上一轮的 ctx 之后，
`ensureFocusMedia` 看到「已经有播放器」就再也不会起播。结果这一路在播放页上
**没有轮次**——地址不续、`ended` 不重拉，热切换只能拿空凭据去要地址。

所以 `ensureFocusMedia` 补一条：有播放器、但 `media.ctx` 为空时走
`adoptLiveSession`——建 ctx / id / `refreshAt`，首帧与 opened 按「已经在出图」
写，**不动播放器本身**。日志里是 `session.begin` + `cause=adopt`（`接管#N`）。

真窗口验证（2026-11-25 17:30，`-tags e2e` + 真实流的 profile 副本）：
从「悲鸣观测」里把虎牙那间（485503，就是崩溃那间）激活 → 岛上回到播放页，
日志先出现 `接管#2`，随后 405ms 一次 `swap_start`（dead-ended）→ 915ms
`swap_done`，15.4s / 30.4s 又各换一次地址，进程全程存活、`crash-*.log` 为空。
旧代码在这个位置是「拿 nil ctx 去要地址」，最好情况是静默冻帧。

## 后台收听：离开直播间，声音留着

离开播放页默认**不关播放**。左上角的叉是明确退出；Esc 第一下是收进岛（后台
收听），再按一次才结束这一路。其余离开方式（顶栏 tab、灵动岛、搜索跳转）都进
后台收听：引擎活着、声音继续、画面不再上屏。

### 后台保留解码，只停全尺寸呈现

后台不再 `vid=no`。视频轨一直选中、一直解码，只把软件渲染的目标收到 160×90：
音频输出、`time-pos`、demuxer、参考帧链全都不动。回来时把渲染目标恢复成视频
原始尺寸，并用当前帧立刻重渲染一次——实测 4ms 出全尺寸画面，音频停走 21ms
（就是 `audio-pts` 的事件粒度），2 秒窗口推进 2.0 秒，声音一秒没断。

后台单核（B 站 545068，1080P60 HEVC，2026-11-21）：硬解 6.4–8.9%，软件解码
21.7–33.4%。前台同一路硬解 22.6–23.3%、软件 23.2–49.5%，丢帧/失时都是 0。
所以生产把 `hwdec` 从 `no` 改成 `auto-safe`：后台便宜三倍，前台持平或更好。

为什么不继续用 `vid=no`：它只花 1.6%，但回来要重新选视频轨——mpv 会清空
demuxer 缓存并等 live 边缘的**下一个**关键帧，实测 0.08–5.2 秒。B 站的分片
不保证带 IDR（6–7 个 1 秒 fMP4 分片，`#EXT-BILI-AUX` 的 K/N 与
`first_sample_flags` 一致），关键帧间隔是场景相关的多秒级。把 60 秒流抓成
本地文件后，同一路 `vid=no` 回来只要 60–139ms（8 轮，p50 116ms）——解码不是
瓶颈，**瓶颈是「下一个关键帧还没被编码器产出来」**。网络、缓存、seek 都绕不过
它，只有保持解码能保证 <1 秒。

| 方案 | 后台 CPU（1080p60） | 回来到画面 | 音频 |
|---|---:|---:|---|
| **保留解码 + 小尺寸呈现（现在）** | **6.4–8.9%（硬解）** | **4ms** | **不断（停走 21ms = 事件粒度）** |
| `vid=no` → `vid=auto`（已弃） | 1.6% | 0.08–5.2s（GOP） | 通常 52–94ms，上游慢时 0.76s |
| 整路重新起播（更早） | 1.6% | 0.75–1.7s | 1.09–1.56s |
| 影子引擎（`ao=null` 预热） | +10% 以上，且两条连接 | ~5ms | 无（两套时钟） |

这张表就是取舍：**要保证画面 <1 秒，后台就得付解码的钱**。5% 的后台预算在这条
流上做不到——硬解 6.4–8.9% 已经是 Ryzen 7 9700X + RX 9070 XT 的地板，软件解码是
21.7–33.4%。判据与全部否证数据见
[decisions/0009](../decisions/0009-live-continuity.md)。

试过并否掉的中路：

- **回来 seek 到当前音频位置**：让它回头找上一个关键帧。一轮 5 次是 114–404ms，
  另一轮 10 次里 3 次 1.0–1.4s；音频停 120–425ms，还前跳过约 1.1 秒。不构成保证。
- **`vd-lavc-skipframe=nonref`**：恢复 138–158ms，后台 CPU 27–32%。
- **`vd-lavc-skipframe=nonkey` / `bidir`**：后台 3–10%，但切换选项会触发解码器
  reinit + refresh seek，把音频往回拖 4.7–71 秒（实测）。出局。
- **`audio-buffer=1.0`**：能把 `vid=auto` 重读的停走从 0.26–1.34s 压到 52–94ms，
  但那条路本身要等关键帧，已经不用；生产保持默认 0.2。
- **`cache-secs` / `live_start_index`**：三组样本重叠；`cache-secs=10` 还出现过
  10 秒不给画面。不改。

整路重新起播的四段时延保留作对照（2026-11-21，三轮）：

| 段 | 内容 | 实测 |
|---|---|---:|
| 1 | 平台取流（主 master + 选档） | 383 / 399 / 417ms |
| 2 | mpv 打开探流（Open → 第一声音频；连接、播放列表、首个分片、AO 起步） | 350 / 647 / 1287ms |
| 3 | 等第一个关键帧（第一声 → 第一帧有内容的画面） | 2 / 3 / 3ms |
| 4 | `audioGate` 等音频 | −2 / −3 / −3ms |
| 合计 | 画面放行 | 747 / 1077 / 1699ms |

新连接从分片边界起播，第三段几乎为零；首帧的大头是取流与连接/探流。它的
音频接缝是实打实的 1.09–1.56 秒。

### 看门狗不能挂在视图上

`trackFirstFrame` 原来由 `PlayerPage` 每帧调用。后台态页面不构建，它就死了——
而斗鱼的直链签 300 秒，没人续期，五分钟后声音自己断。所以它挪到了 `Shell`
的常驻心跳（`mediaTick`），判据从「在播放页」改成「有引擎」。

后台态下没有视频元素在请求帧，所以 `kickMediaTick` 另排一条 250ms 的定时器
驱动它。这一条顺带修掉一个既有 bug：主窗口最小化超过五分钟，音频也会断。

### 后台态的健康判据是音频时钟

后台态没人拉帧、帧也只是 160×90 的小尺寸，`Ready` / `Frame` 说明不了观看状态。
后台态改用 `Position()`：
6 秒没有一毫秒推进才算断了。**首帧预算在后台态不适用**——画面本来就不该有，
用画面判死会把一路好好出声的流关掉（`TestBackgroundIgnoresFirstFrameBudget`）。

### 换地址要换判据

`runHotSwap` 原来等新一路出图再切。后台态下没人拉帧，这条判据会 100% 失败，
所以 `swapRequest.background` 让它在后台态改等 `Position() > 0`。
换上去之后还要按当前位置推一次呈现尺寸（`applyPresentForce`）：新引擎的
呈现尺寸是它自己的默认值，不会继承旧那一路。

### 两路 mpv 不抢 WASAPI

这里曾经写「两路 mpv 同时开会抢 WASAPI，`video-sync=audio` 的时钟停住，
整路冻死」。2026-11-21 复核实测：热切换三次两路并存，新路 `audio-pts` 照常
推进；主路 + 一路 `ao=null` 影子跑 20 秒，主路音频 8s/8s 一秒不少。

真正的问题在**关闭是异步的**：换引擎时不等旧实例 `Done()` 就建新的，旧 AO
还占着设备，新实例打不开音频——那是「先关后开」的时序，不是共存。现在
`VideoPlayer.Close()` 仍立刻返回，真正的等待发生在下一次 `playback.Open` 的
`waitDestroys()`；热切换是「先开后关」（`adoptSwap`），旧实例在新实例出图后
才关，不等 `Done()`。

从后台收听点开另一个直播间时，旧焦点走 `endSessionFocus`：还在墙上就只收声、
引擎留着做静音预览；不在墙上就连引擎一起关。所以同一时刻可能有两路引擎，
但只有一路出声。

## 什么时候会慢，以及怎么区分

慢有两种，必须先分清：

1. **上游供给慢。** mpv 会报 `buffering=true`，并按实时速度慢放（因为它拿不到数据）。
   这不是 bug。判据：同一份签名地址用 ffmpeg 命令行解，8 秒流要跑十几秒、`speed`
   小于 1x。仓库里没有对应的自动化测试，这是一次手工对照。
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

- `ms` 相对本轮起播的起点。关联字段（run / room / session / engine）的权威表在
  [diagnostics.md](diagnostics.md) §1。
- 首帧可用表示第一帧交给呈现端，不是精确的屏幕呈现时刻。

## 验收要求

**一台干净 Windows：只装 alive，不装 FFmpeg、不装 HEVC/AV1 扩展。**

- 四个平台各取若干房间，点开即出画出声，首帧 5 秒内
- 1080P60 档位稳定 59fps 上下，零丢帧
- 连续播放 10 分钟不断音、不停顿
- 退出后无残留进程、无残留句柄

工程检查：

```sh
make check        # gofmt + build + vet + mygo vet + lint + test + test-mygo
```

跨平台矩阵与上游对照（都要连公网）。这两条探针不进仓库，也不在 `make check`
里；需要时重新写一个，跑之前拷回 `internal/playback/`。
命令形状如下（当前仓库内没有 `TestMatrix` / `TestFfmpegControl`，不要直接复制
执行）：

```sh
$env:ALIVE_MATRIX="1"; $env:ALIVE_MATRIX_PLATFORMS="bilibili,douyu,huya,douyin"
go test ./internal/playback -run TestMatrix -v -count=1 -timeout 900s

$env:ALIVE_CTRL_ROOM="3428783"; $env:ALIVE_CTRL_PLATFORM="bilibili"
go test ./internal/playback -run TestFfmpegControl -v -count=1
```