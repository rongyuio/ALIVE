# 0003 自带解码的实现方式：cgo + libav 进程内

- **状态**：已决定，已落地（2026-11-16）
- **日期**：2026-11-16
- **补充**：[0001](0001-player-backend.md)。方向没变（自带解码，不交给 Media Foundation），
  换掉的是落地手段：两个 ffmpeg 子进程 → 进程内 cgo 调 libav
- **影响**：`internal/libav`、`internal/playback`、`internal/ui/player_media.go`、`mygo.json`、
  `third_party/ffmpeg`

## 背景

[0001](0001-player-backend.md) 定了走自带解码。第一版实现是两个 `exec.Cmd`：视频用
`-f rawvideo` 走 stdout 管道，音频用 `-f f32le` 走本地 TCP 端口。那条路走到底，问题
不是慢，是**结构上就不可能对**：

- **反压串在一起。** FFmpeg 往两个输出写数据是串行的。视频读得慢，FFmpeg 整个进程停住，
  音频跟着断。实测 12 秒里断供 12 次，每一次都是视频队列满导致的。
- **PTS 靠文本。** 时间戳要从 stderr 的 `-vf showinfo` 里逐行解析，`-af astats` 再来一遍。
  任何一行格式变化都是静默错位。
- **两路必然不同轴。** 两个进程各自建立连接、各自探测、各自有起播偏移。用户听到的就是
  音画不同步，而"对齐"只能事后打补丁——把视频时间轴往音频上拽，或者反过来。

ry 的原话概括得比这准：「流本来就是音画同步的东西，让你接入进来怎么能出这么多问题。」
直播流本身是同一条时间轴上的一串包，拆成两个进程再拼回去，拆的过程就已经把同步丢了。

## 候选

### A. 继续用子进程，改成单进程双输出

`ffmpeg -i URL -map 0:v -map 0:a -f nut pipe:1`，自己解 nut 复用。

- 优点：仍然是纯 Go，零 cgo，交叉编译不受影响
- 代价：要在 Go 里解一层复用格式；反压仍然是单管道，一个消费者慢就全慢；
  seek/flush/时间基换算全得自己做。等于自己写半个 libavformat，而且写不好

### B. cgo + libav，进程内

`avformat_open_input` → `av_read_frame` → `avcodec_send_packet` / `avcodec_receive_frame`，
音视频共享同一个 demuxer。

- 优点：和 potplayer、ffplay、VLC 同构；包自带正确时间戳；音视频天然同轴；
  反压由我们自己按流类型分别决定；一次连接、一次探测
- 代价：引入 cgo。不能交叉编译、构建机要有 gcc、运行时要带 139MB 的 DLL、
  GPL 传染（BtbN 的 build 是 GPL 版）

### C. 等上游 mygo #124（libmpv 插件）

- 优点：potplayer 那一类成熟实现，边界情况不用自己扛
- 代价：PR 从 2026-10-06 起未合，作者态度见 [0001](0001-player-backend.md) 的上游调研。
  等它等于把产品发布挂在别人的排期上

## 决策

**走 B。** 分支上把三种都摆出来对比过，B 是唯一一个「问题从结构上消失」的选项，
A 和 C 都只是把问题往后推。

ry 的判断标准值得写下来，它比任何基准数字都更能说明为什么选 B：

> 很多东西 C 能写，为什么用 Go/Rust？因为它们写着方便也能解决需求。那 Go 不好做的、
> 做不到的、或者有更成熟更方便的方案——比如 libav、cgo——为什么不用？
> 交叉编译是为了解决问题；为了解决问题不能交叉编译，又怎么样呢？
> 以后要适配其他平台，就给那个平台再写一份代码。

换句话说：**不引入 cgo 的前提是 Go 自己就能干净做到。** 这里做不到。在 Go 侧硬写
libav 的等价物，代价不是"多一点代码"，是持续产出一类我们无法彻底修好的 bug——
音画不同步、反压饿死、PTS 错位——然后反复打补丁。那个成本早就超过了买一份成熟实现。

## 落地形状

```text
libav（cgo，internal/libav）
  ├─ Demuxer    avformat_open_input / av_read_frame / 包引用与克隆
  └─ Decoder    avcodec_send_packet / avcodec_receive_frame
                ├─ 视频：sws_scale → NV12
                └─ 音频：swr_convert → s16/f32 48kHz 立体声

playback（internal/playback）
  ├─ 解封装 goroutine   读包 → 按流分到两个包队列（cap 256）
  ├─ 音频解码 goroutine → PCM 环（4 秒）→ MyGo audio.Player
  └─ 视频解码 goroutine → 12 个帧槽 → ui.Bitmap.UpdateYUV
```

反压只加在音频（PCM 环是唯一的节流点），视频永远不阻塞：槽满了丢最旧的帧。
这是直播的正确语义——宁可丢一帧画面，也不能让声音断。

音频是主时钟，但**零点取第一个可解码视频帧（关键帧）的 PTS，不是音频首帧**，
音频跳过关键帧之前的部分。这一条是首帧从 4.5 秒降到 0.4 秒的关键：直播流接上之后
音频立刻流动，视频要等下一个 IDR，实测斗鱼两者差三秒多。

## 后果

- **不能交叉编译。** Windows 的包只能在 Windows（或配了 MinGW 的 CI）上构建。
  这是这个决策最实的一笔代价，写在 [0002](0002-ffmpeg-provisioning.md) 的约束里。
- **构建机需要 gcc**（MinGW-w64）和 `CGO_ENABLED=1`。mygo 的构建默认关 cgo，
  显式设了才照传。
- **139MB 运行库随包。** avcodec 一个就 114MB。安装包 LZMA 压缩后 47MB。
  打进仓库会让仓库变重，这是可接受的：它是构建输入，不是产物。
- **GPL。** BtbN 的 shared build 是 GPL 版，链接它意味着整个应用受 GPL 约束。
  这一点在子进程方案里同样成立（当时发的也是这个 build），不是新增限制，但分发时
  要一并提供对应源码。
- 我们接手了播放的全部边界情况：丢帧、缓冲、断流、时钟漂移。MF 曾经帮我们挡掉的那些，
  现在得自己扛——换来的是这些事**终于在自己手里**。

## 复查条件

- 上游 mygo 开放引擎接口或原生视图托管（[0001](0001-player-backend.md) 的复查条件），
  届时可以重新评估 libmpv
- 需要支持一个不能跑 MinGW 的构建环境（例如无 Windows 的 CI）
- 139MB 的分发体积成为实际障碍