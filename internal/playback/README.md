# internal/playback

用 libav 在进程内解码直播流，做成 MyGo 的 `media.Engine`。

- 为什么不用系统解码、和 Media Foundation / libmpv 的取舍 → [doc/decisions/0001](../../doc/decisions/0001-player-backend.md)
- 为什么是 cgo 而不是 ffmpeg 子进程 → [doc/decisions/0003](../../doc/decisions/0003-libav-in-process.md)
- 在整条播放链路里的位置、缓冲取值、首帧模型 → [doc/architecture/playback.md](../../doc/architecture/playback.md)
- 实测数字 → [doc/reference/measurements.md](../../doc/reference/measurements.md)

本文件只写接口和实现约定。

## 接口

`Open` 返回 `*Engine`，它实现 `media.Engine`：呈现端每帧问 `Frame(t)` 要画面，
声音由 `audio.Player` 从 `pcmRing` 读。调用方不需要知道里面跑着什么。

```go
type Options struct {
    Headers   []string // 回源请求头，例如 "Referer: https://live.bilibili.com/"
    Width     int      // 平台声明的尺寸，仅作提示
    Height    int
    FrameRate float64
}

func Open(ctx context.Context, upstream string, opts Options) (*Engine, error)
```

`Open` 返回时解封装已经跑起来，首帧要等 `Frame` 第一次给出画面。
地址为空或 libav 不接受这条流时返回 `ErrUnsupported`，调用方据此回退到 relay。

## 内部实现

```text
Open
 └─ libav.OpenDemuxer  avformat_open_input（一次连接，音视频共享）
     ├─ 解封装 goroutine    av_read_frame → 按流分到两个包队列（cap 256）
     ├─ 音频解码 goroutine  解码 → swr 重采样 48kHz 立体声 f32 → PCM 环（4 秒）
     │                       └─ audio.Player 按 WASAPI 实际出声节奏消费
     └─ 视频解码 goroutine  解码 → sws 转 NV12 → 12 个帧槽
                             └─ Frame(t) 按音频时钟挑帧，用完还槽
```

几个刻意的选择：

- **反压只加在音频。** PCM 环满 → 音频解码停 → 解封装停，这是唯一会阻塞整条链的地方。
  视频永不阻塞：槽满了丢最旧的帧。宁可丢一帧画面，也不能让声音断。
- **视频解码跟着呈现节奏走**（`free` 槽信号量）。不节流的话解码会跑到前面几秒，
  槽里堆满未来的帧，反而把当前该显示的那一帧挤掉。
- **时钟零点取第一个可解码视频帧的 PTS，不是音频首帧。** 直播流接上后音频立刻流动，
  视频要等下一个 IDR，实测斗鱼两者差三秒多。音频跳过关键帧之前的部分，两路都从
  关键帧起步——这是首帧从 4.5 秒降到 0.4 秒的关键。
- **不分片代理。** 分片由 libav 自己按需取，和网页端一样：一条下载流、一次一片。
  曾经套过一层后台预取 6 片的本地代理，实测是反的——预取和正在读的那一片抢带宽，
  12 秒里音频断 4~17 次；关掉之后同样的房间 0 断供。抖动的正确吸收位置是播放端的
  环形缓冲，不是输入端的二次搬运。
- **PCM 环 4 秒、预缓冲 2 秒。** 取值依据是实测的断供数据，推导写在 `engine.go` 的
  常量注释里，别凭感觉改。
- **缓冲环就是 `audio.Decoder`。** `pcmRing` 实现 `Read` / `SampleRate` / `Channels`，
  直接交给 `audio.Player`，中间不再有搬运层。

## 诊断

```go
func (e *Engine) Stats() (frames, dropped uint64, audioOn bool)
func (e *Engine) Starved() (count uint64, worst time.Duration)  // PCM 环见底的次数与最长空等
func (e *Engine) DemuxStall() (count uint64, worst time.Duration)
```

判"卡"只看前两个：`Starved` 有值说明缓冲不够或网络真的断了，`DemuxStall` 说明
`av_read_frame` 在等数据。

## 测试

```sh
$env:PATH = "D:\Project\alive\third_party\ffmpeg\bin;$env:PATH"
go test ./internal/playback -count=1
go test -race ./internal/playback -count=1
```

PATH 那一步不能省：测试二进制落在临时目录，找不到 libav 的 DLL 会以 `0xc0000135`
直接退出（加载器在进程入口点之前就解析 import table，Go 代码一行都跑不到）。

`live_test.go` 是接真实公网流的端到端验收，需要 `ALIVE_LIVE_ROOM`，默认跳过。