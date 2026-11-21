# 0009 直播连续性与弹幕时钟

日期：2026-11-21（第八轮 2026-11-22）。状态：弹幕模块已落地；直播卡顿的根因是 HLS 在 libmpv 下每片新建连接，B 站已改走 FLV，缓冲驱动的换线在播放页与观测墙都已删除，断流判据按 FLV 实测收紧，日志已落盘（第四至八轮）；「后台收听 → 回到播放页」按验收标准改为「保留解码 + 小尺寸呈现」（见文末）。

## 问题

1. libmpv 的缓存配置与使用方式，是否不足以支撑「直播不短暂中断」？断在哪一层？
2. 弹幕「到达即上屏、墙钟驱动、Track = seq % 14、无占用检查」是否合理？

先写死判据与预算，再看数；不把「重构」当默认答案。

## 五类事件与判据

| # | 事件 | 判据 |
|---|---|---|
| 1 | 上游断供 | `paused-for-cache=true`；`demuxer-cache-duration` 逼近 0；`cache-buffering-state=0`；`time-pos` 停 |
| 2 | 音频断流 | `audio-pts` 停而 `time-pos` 仍走；`audio-speed-correction ≠ 1` 持续 |
| 3 | 呈现抖动 | `decoder-frame-drop-count` / `mistimed-frame-count` 增长；`estimated-vf-fps < display-fps×0.95` |
| 4 | 热切换接缝 | 新路放行时 `audio-pts` 是否为 0（被 `audioGate` 的 2s 兜底掩盖） |
| 5 | 弹幕时钟 | 弹幕上屏时刻与它对应的画面内容时刻之差；暂停/换路后的跳变 |

预注册预算：10 分钟 ≥250ms 冻结 ≤2 次、音频 gap ≤45ms（ITU-R BT.1359-1）、
弹幕 offset p95 ≤250ms / p99 ≤500ms、暂停/恢复/换路无可见跳变、首帧/内存/CPU 不退化。
「回来」的验收口径后来单独收紧为「音频不断 + 画面 <1s」，见文末；CPU 在该口径下
从约束降为成本记录。

## 证据

数字与条件在 [reference/measurements.md](../reference/measurements.md)「直播连续性」。
要点：

- 10 分钟 B 站 1080P60 HEVC：稳态 ≥250ms 冻结 **0 次**，缓冲期外音频 gap **0 次**，
  解码丢帧 / 失时 **0 / 0**，缓存中位 1.67s，CPU 35.2% 单核。
- 另一次 120 秒：2 次冻结（879ms / 4.06s），都伴随 `cache=0`、`paused-for-cache=true`；
  50 次音频 gap **全部**落在缓冲窗口内。上游空档 3.3s + 恢复迟滞 0.6–0.8s = 4.06s。
- m3u8 是 7 个 1 秒分片。`live_start_index=-5` 在新开一路时把稳态缓存从 1.67s 提到
  3.42s，代价是画面晚约 3.4s；样本不足以证明冻结率下降。
- 热切换三次，新路首帧 258/663/391ms，放行时 `audio-pts` 均 >0；2 秒兜底未触发。
- 虎牙 180s：冻结 0、音频 gap 0、丢帧 0。斗鱼 403，没测到。

## 决策

**第一轮：缓存参数不进生产。** 冻结的因果链是上游数据空档，不是 mpv 参数配错，
也不是我们的呈现调度：丢帧与失时为 0，音频在缓冲期外从未停。把缓存加深（例如
`live_start_index=-5`）确实能吸收更长的空档，但代价是等量的直播延迟，而现有样本
还不足以证明冻结率有可测量的下降。按「一次只改一个变量、没有可测量收益就回滚」，
保持 `cache=auto` 与全部默认值。

**第三轮（回来标准收紧后）：播放层改两处。** 后台从 `vid=no` 改成「保留解码、
只把渲染目标收到 160×90」，`hwdec` 从 `no` 改成 `auto-safe`；依据与数字见文末。
缓存参数（`cache-secs`、`live_start_index`、`audio-buffer`）仍然保持默认。

回滚/重启条件：拿到浏览器同房间对照，或可控丢包实验证明「浏览器不冻而应用冻」，
且冻结仍伴随缓存耗尽；那时再按 3.4s 延迟的代价评估 `live_start_index`。

**第四轮（2026-11-21 晚）：缓冲深度与恢复门槛配对，删掉缓冲驱动的换线。**

ry 报「看直播频繁缓冲卡顿，网页端同一条流没问题」。第一轮的归因（上游空档）只在
探针环境成立：探针直接 `playback.Open`，没有 UI、没有换线，600 秒里只有 2 段冻结
共 1.42 秒。应用里比探针多两层自己写的东西，两层都在缓冲上做动作：

1. **默认参数的门槛高于深度。** 这个构建的默认是 `demuxer-readahead-secs=1` 配
   `cache-pause-wait=1.0`——预读 1 秒，缓存抽干后要重新攒满 1 秒才恢复播放。
   一次几十毫秒的抖动因此变成至少 1 秒的可见停顿，每次抽干都重来一遍。
2. **缓冲驱动换线。** 缓冲持续 8 秒就 `startStream(true)` 整路重建：demuxer 缓存
   清零、重新取流、重新等首帧。一次小抖动被放大成一次完整重建，重建期间又短暂
   缓冲，可能再次触发——这是自生成回路，和网络好坏无关。

改动（`internal/playback`、`third_party/mygo/plugins/video`、`internal/ui`）：

- ~~播放页 `demuxer-readahead-secs=5`、观察墙 2 秒~~——**这一段是错的，见下面的
  「后半段」。** hls 的缓冲深度不由这个选项决定，设了不兑现。
- 两者都配 `cache-pause-wait=0.2`（保留）。mpv 默认 1.0：缓存一空就要重新攒满
  1 秒才继续播，等于每次抖动至少卡一秒。
- 删掉播放页的 `maybeFailoverLine` 与它为服务的字段（`autoLine` / `failoverTried` /
  `failoverExhausted` / `healthySince`）。缓冲不再触发任何重建。保留的只有：地址
  过期前热切换、播放器 `ended` / 报错重连、时间轴 6 秒不动判死。
- 同一条回路在观测墙上也删掉了（`fix/watch-wall-no-failover`）：判死后用房间
  记录里的线路直接重开，退避仍由 `watchBackoff` 管，不再在格子内部轮换线路。

**第四轮的后半段（同日晚）：真正的旋钮是起播位置，不是预读深度。**

上面那条改完，ry 实测仍然卡，并给了真实日志。日志直接否掉了它：`cache_ms` 全程
最高 895ms，`demuxer-readahead-secs=5` 根本没兑现——选项读回确实是 5.000000，但
hls 的缓冲深度不由它决定。**已从代码里删掉。**

日志里真正指向原因的是这三行：

```
[hls] skipping 3 segments ahead, expired from playlists
[hevc] Could not find ref with POC 56
[hevc] Error constructing the frame RPS.
```

`live_start_index` 默认是 **0**，意思是「从 playlist 最后一个分片起播」——站在直播
边缘上，前面一个字节都没有，缓存深度必然贴着 0。任何抖动立刻 `paused-for-cache`；
playlist 一滚动，落后的分片过期被跳过；跳过的分片里有关键参考帧，HEVC 参考链断掉，
就是满屏 `Could not find ref with POC`。音频时钟继续走，画面追不上，两者一起崩。

**这一条后来被否掉了一半，见下面的「第五轮」。** 起播位置确实有影响，但它不是主要
矛盾，而且单次 90 秒样本根本不足以支撑结论——同一组参数换一天、换一次运行就能测出
相反的结果。

### 第五轮：用 CDP 挖网页端，对照实测

前四轮都是在自己这边调参数、猜机制，代价是来回三轮拿不到结论。这一轮改成直接
打开网页端，从浏览器里读它到底在干什么（Chrome DevTools Protocol）。

**网页端的真实行为（B 站 263605，未登录，`livePlayer.getPlayerInfo()`）**

| 项 | 实测 |
|---|---|
| 实际播放档位 | `qn=250`（720P 超清），不是 1080P |
| 前方缓冲 | **1.9–3.1 秒** |
| 后方缓冲 | 15–20 秒，不断 `SourceBuffer.remove` 丢弃重建 |
| 每分片下载 | **40–62ms** |
| 取数模型 | 串行，取完立刻取下一个，从不停下等 |

它**不靠深缓冲**。前方只留 2–3 秒，靠的是下载够快、跟不上就丢。

**网页端的播放器源码**（`room-player.a98b754a.prod.min.js`，原生 MSE，不是 hls.js）
里的调度核心：

```js
_doNextFetch() {
  if (count > 0 && (endOfStream || this._totalDuration < 4e3
      || playlist.remainDuration > this._minBufferLength))
    this._fetchNextM4s();          // 有就立刻取
  else { /* 等下一个 m3u8 刷新周期 */ }
}
_onM4sFetched() { this._totalDuration += this._lastDuration; this._doNextFetch(); }
```

`_minBufferLength` 初始为 0，所以判据基本恒真：**只要有分片就立刻取下一个**。

**mpv 这一侧的实测**

| 项 | 实测 |
|---|---|
| 每分片下载 | **134–3465ms**（同一房间同一分片） |
| 起播 | 7.7 秒，期间 playlist 滚过去，触发 `skipping N segments ahead` |

差 10–20 倍。直接量 Go 的 HTTP：**新建连接 380ms，复用连接 40ms**——和网页端的
40–62ms 是同一个量级。差别在连接复用。

于是查 mpv 为什么不复用，在它自己的日志里拿到答案：

```
hls: Disabling http_persistent due to custom io_open.
hls: Disabling http_multiple due to custom io_open.
```

**libmpv 的嵌入式用法强制走自定义 io_open，mpv 因此主动关掉连接复用和并行下载。**
这解释了每片 380ms。也验证过：去掉 `http-header-fields` 一样被禁用（不是请求头
引起的），而且不带请求头更差——所以这条在 libmpv 里没有绕过的方法。

**这一轮改了什么**

- `internal/live/bilibili_master.go`：`master/url` 返回的地址有两种形态，一种直接
  是媒体 playlist，一种还是 master（要再跳一层，且常换域名）。后者交给 mpv 起播要
  多跳一跳，7.7 秒里 playlist 就滚过去了。现在在取流层展开成媒体地址，和网页端
  对齐。实测 263605 的 1080P 原画从「90 秒里缓冲 23 秒」降到 4 秒。
- `internal/applog`：日志落盘（详见下一节）。这一轮最贵的是三轮拿不到 mpv 自己的
  话，不是改错。

**没解决的部分，说清楚**

`custom io_open` 导致的连接不复用，在 libmpv 这条路上没有开关可关。这是 HLS 形态下
的硬限制。

### 第六轮：换成 FLV，问题消失

第五轮的最后一句写的是「要真正对齐网页端的取数速度，只有自己取分片」。这句话错了
一半——**还有一条更简单的路：不用 HLS。**

用户指了一个参照实现 `xiaoyaocz/dart_simple_live`（同样基于 media_kit，也就是
libmpv）。它的取流参数是：

```dart
"protocol": "0,1",
"format": "0,2",   // 只要 FLV 和 fMP4，明确排除 TS
"codec": "0",      // 只要 AVC
```

**它不用 HLS。** 原因正是第五轮查到的那个：`custom io_open` 关掉连接复用，HLS
每片新建连接就废了。而 FLV 是单条长连接——io_open 只发生一次，之后数据连续到达，
`http_persistent` 开不开都无所谓。

实测（B 站 263605，登录态，2026-11-21，各 60 秒）：

| 档位 | HLS 缓冲 | HLS avsync | **FLV 缓冲** | **FLV avsync** |
|---|---:|---:|---:|---:|
| 1080P 原画 | 23.0s | −1.99 | **0.0s** | −0.017 |
| 1080P 蓝光 | 17.0s | 4.40 | **0.0s** | 0.000 |
| 720P 超清 | 3.2s | — | **0.0s** | −0.033 |

首帧也从 7.7 秒降到 1.4 秒。**三个档位全部零缓冲。**

改动（`internal/live/bilibili_stream.go`）：

- `biliPickPlayable` 的打分从「HLS +100」改成「FLV +200」。旧实现里 FLV 永远
  选不上，这是问题一直存在的直接原因。
- `biliFetchPlayInfo` 合并两条路：master/url 给档位骨架（全档位、中文名、分辨率、
  线路链），`attachFLVURLs` 逐档用 getRoomPlayInfo 补 FLV 地址。master 只出
  HLS（地址写死 `index.m3u8`），必须补。
- `biliTierURLs` 的档位判据加上**流名后缀**：逐档接口的 `current_qn` 不可靠——
  登录态请求 10000 时服务端回的仍是 `current_qn=250`，但 base_url 是 `_bluray`，
  于是原画档拿到了蓝光的流。后缀（`_prohevc` / `_bluray` / `_2500`）是唯一和实际
  内容绑定的字段。
- 补进来的 FLV 候选要继承 master 那一档的分辨率 / 码率 / 帧率：逐档接口的响应里
  没有 `RESOLUTION` / `BANDWIDTH`，不补就是菜单上全是 0。

测试（先红后绿）：`internal/live/bilibili_pick_test.go`

- `TestBiliPickPlayablePrefersFLV`：同时有 HLS 和 FLV 时选 FLV。把打分改回 HLS
  优先会红，已验。
- `TestBiliPickPlayableFallsBackToHLS`：只有 HLS 时仍要能选——有些房间不给 FLV。
- `TestBiliStreamSuffixQN`：后缀到档位的映射，认不出来返回 0 保持原判断。
- `TestBiliStreamSuffixQNIgnoresQuery`：query 里的字样不能误判。

**没做的**

- `codec` 没有照抄参照实现的 `"0"`（只要 AVC）：B 站的 1080P 原画常常只有 HEVC
  分支，只要 AVC 会把最高档整档丢掉。

### 第七轮：取流拆成「列菜单」与「解析地址」，墙才开得起来

第六轮改完，观测墙 10 格全部卡在「正在拉取直播流」。原因不是播放，是取流：

```
Stream(roomID)
├─ RoomInfo                     1 次
└─ biliFetchPlayInfo
   ├─ getRoomPlayInfo(qn=0)     1 次
   ├─ master/url                1 次   → 只有 HLS
   └─ expandMasterURLs          每个「档位 × 线路」各 1 次 → 3 档 × 4 线 ≈ 12 次
```

**一个房间约 15 个请求。** 墙 10 格 = 150 个，全挤在同一个限流器（6 并发、
60ms 间隔）后面，每格只有 25 秒预算，于是整墙超时。这两个放大器都是第六轮
加的（`expandMasterURLs` / `attachFLVURLs`），当时没算这层成本。

**根问题是分工错了。** 项目里本来就有正确的接口，`internal/live/types.go` 写着：

> `PlaybackStreamer` 让需要逐档请求的平台只解析当前播放地址，避免起播等待
> 全部档位。

斗鱼实现了它（只解析选中那一档，其余 URL 留空），**B 站没实现**，所以每次都把
全部档位解析一遍。

改动（`internal/live/bilibili_stream.go`、`bilibili_master.go`）：

- **`Stream()` 只列菜单**：发 1 次 `getRoomPlayInfo` + 1 次 `master/url`，拿档位名、
  分辨率、线路链；`Variants` 全部地址留空。
- **`StreamForPlayback()` 只解析选中那一档**：1 次 `getRoomPlayInfo(qn=目标档)`，
  直接拿到 FLV。走 `PlaybackStreamer` 接口，和斗鱼一致。
- 删掉 `expandMasterURLs` / `expandMaster` / `biliFetchTiersByQN` 和
  `biliVariantConcurrency`——播放路径不再需要它们。
- 规格（分辨率 / 码率 / 帧率）从「地址」挪到「档位」上：菜单要显示它们，而地址
  按需解析、解析出来的候选不带这些值。不挪的话菜单上全是 0x0。
- `biliPickPlayable` 的 FLV 优先、`biliStreamSuffixQN` 的档位校正都保留。

实测（2026-11-21，B 站 263605）：

| 场景 | 改前 | 改后 |
|---|---:|---:|
| 单房间取流请求数 | ~15 | **2** |
| 单房间耗时 | 1.5s+ | **0.4–1.4s** |
| 10 格并发 | 整墙卡死 | **10/10 成功，总耗时 2.05s** |

测试（先红后绿）：`internal/live/bilibili_menu_test.go`

- `TestBiliStreamDoesNotResolveAddresses`：菜单只许发 1 次取流请求，且所有
  档位的地址为空。把逐档解析加回去会红（实测 4 次 vs 1 次），已验。

`internal/live/empty_stream_test.go` 的契约随之调整：`Stream()` 可以列出档位但
不带地址，只保证「在播状态不丢」。

### 第八轮（2026-11-22）：断流判据按 FLV 实测收紧

eefee76 给墙加的音频时钟分支是 HLS 时代的：CDN 一阵一阵供数，画面时钟冻住
5–20 秒而音频照走。B 站改走 FLV 之后这一点还成不成立，用真实 FLV 测了一遍
（数字与条件见 `reference/measurements.md`「FLV 断流判据」）。

结论：

- FLV 单连接断供时 mpv 连音频一起暂停。四种断法（完全停发 / 慢节点 / 只发音频
  / 视频内容损坏）音频都在 250ms 内跟着停；自然播放 420 秒里「画面停、音频走」
  出现 0 次。音频时钟不再提供任何「还活着」的信息，反而会让一张冻住的画面
  永远不重连——从 `watchDeadReason` 删掉。
- 墙的判据只剩时间轴 + 两个窗口：常规 6 秒，缓冲态 20 秒
  （`streamBufferStallTimeout`，和播放页共用）。
- 顺带测出播放页的洞：`deadReason` 原来在 buffering 时直接返回「还活着」。
  FLV 静默断流时 mpv 既不 ended 也不报错（实测 150 秒仍是 playing），播放页
  会永远停在最后一帧。现在缓冲态也有 20 秒上限，超时判 `buffered-too-long`。
- 本地断流端到端复核：播放页在第 35 秒判死（断流点 15 秒，即 20 秒后）。

### 其余三个平台（2026-11-21 实测）

同一套探针，各 45–60 秒，登录态：

| 平台 | 房间 | 缓冲段 | cache 中位 | avsync | 格式 |
|---|---|---:|---:|---:|---|
| B 站 | 263605 | 0 | 2.71s | −0.017 | FLV（本轮修的） |
| 虎牙 | 485503 | 0 | 5.81s | −0.006 | FLV |
| 斗鱼 | 8489391 原画 | 0 | 0.21s | −0.000 | FLV |
| 斗鱼 | 8489391 蓝光 | 0 | 3.73s | −0.033 | FLV |
| 斗鱼 | 7592343 原画 | 0 | 6.83s | −0.016 | FLV |
| 抖音 | 531854687617 | 0 | 2.07s | −0.012 | FLV |

**四个平台全部零缓冲。** 除 B 站外，其余三个平台本来就只给 FLV
（`douyinTierQuality` 就是 `url := stream.FLV`，为空才用 HLS；虎牙的地址在
`huyaStreamBaseURL` 里**代码自己拼 `.flv`**，写死不会变），所以从来没有这个问题
——**卡顿只在 B 站，因为只有 B 站走了 HLS**。

**斗鱼是同类的雷，已拆。** 上一版 `douyuParsePlayURL` 是：

```go
live := strings.TrimSpace(html.UnescapeString(data.RtmpLive))
if douyuIsPlayable(live) { return live, nil }   // 第一个非空就返回
```

斗鱼的请求表单里**没有格式参数**（只有 `enc_data` / `rate` / `cdn` / `hevc=0`），
服务端给什么就是什么；而这段代码既不挑格式、也不留日志。也就是说「实测三次都是
FLV」只是当时的运气，哪天斗鱼改下发 m3u8，会静默踩上 B 站同一个坑，而且从日志里
看不出来。

现在：把响应里全部候选收集起来，**优先 FLV**，并写一条 debug 日志记录候选数、
最终是不是 FLV、是不是 HLS（只记格式，不记地址——地址带签名）。测试
`internal/live/douyu_pick_test.go` 守住这个行为，先红后绿已验。

斗鱼 8489391 原画档 cache 只有 0.21 秒，但缓冲段是 0：mpv 消费得快、缓存维持得
浅，和 B 站之前那种「cache=0 + 反复 paused-for-cache」不是一回事。同一房间蓝光档
3.73 秒。样本只有两次，不下结论。

斗鱼 / 虎牙的 `Variant.Bitrate` 都是 0（平台接口没给，既有行为，与本次改动无关）。



**这一轮失败过的做法，留记录**

- `demuxer-readahead-secs` 5/30/100：选项读回都正确，但 hls 的缓冲深度不吃它。
- `cache-pause-wait` 0.2：保留，但对「每片新建连接」这个瓶颈无效。
- `live_start_index` -3/-6：单次样本结论相反，不足以下结论。
- 把四组实验塞进同一个进程：`playback.Close()` 是异步的，后面的组会和残留引擎抢
  连接，数据全部作废。教训是**一个进程只跑一组**。
- `hwdec=no` / `d3d11va` / `d3d11va-copy`：单次样本互相矛盾，没有可复现差异。


**同轮：日志落盘。** 这次排查最贵的不是改错，是三轮拿不到 mpv 自己说的话——
`slog` 只输出到 stderr，双击启动的进程没有 stderr，等于不留日志。现在
`internal/applog` 把日志写到 `%LOCALAPPDATA%\alive\logs\alive.log`（8MiB 轮转一份），
播放器用 `mpv_request_log_messages("warn")` 订阅 mpv 日志，经 `State.Log` 由媒体层
增量转进同一个文件。`duplicated MOOV Atom` 只留第一条——B 站 fMP4 每个分片重带
init 段，实测 60 秒 50 条全是它，会把故障行淹掉。

代价与未验证：

- 直播延迟增加约 3 秒。想压回来把 `live_start_index` 往 0 调，但那会重新贴回边缘。
- **-3 之外的分档没有长期样本。** 只有 90 秒 × 3 组的对照；不同平台、不同分片
  时长（虎牙/斗鱼是 FLV，不走 hls，不受这个选项影响）未测。
- 播放延迟的实际增加量没测。

**弹幕是定点模块，改。** 它违反了两条可观察的不变量：

1. 画面暂停/缓冲时弹幕继续走（墙钟驱动）。实测暂停后弹幕位置继续变化，
   暂停越久，弹幕相对画面超前越多，且恢复时不会归位。
2. `Track = seq % 14` 与窗口实际轨道数无关。900×600 窗口只有 8 条轨道，
   第 9 条 Track=8，渲染时 `%8` 撞回第 0 条；且没有占用检查，同轨重叠。

改动（`internal/ui/danmu.go`、`player_media.go`）：

- 弹幕时钟只在画面真的前进时才走，但**步长取墙钟**：`time-pos` 只按视频帧率更新
  （实测 30fps 流上约 30 次/秒、每次整跳 33.33ms，间隔 20–40ms 抖动，见
  [measurements](../reference/measurements.md)），拿它的增量当步长，60fps 的界面
  会一半的帧不动、动的帧只走被夹短的一小截，肉眼看就是「顿」。位置没变时先容忍
  250ms（`danmuStallGrace`，覆盖采样间隔），超过才认定暂停/缓冲/卡死并冻结；用户
  按暂停则当场停住。seek/换路只更新基准，不补时长，不瞬移。无播放器时才直接看
  播放态。位置**每帧**重算，不再只在 33ms 的 tick 里推进。
- 轨道分配用上一帧算出的真实轨道数，并做**追尾预测**：每条轨道记住最后一条的出生
  时刻与宽度；新弹幕能进该轨，当且仅当「前一条已完全进入」且「不会在前一条离场前
  追上」。当前速度模型是 `v = (S+w)/D`（宽弹幕更快，与 B 站系一致），第二条判据化
  简为 `w₂·(D−dt) ≤ S·dt`。分配是**从上往下找第一条放得下的轨道**：空轨放得下，
  能安全接力的同轨也放得下；跳过能用的轨道硬往下排会排成一条固定斜线。所有轨道
  都放不下时才叠（宁叠不丢）。宽度按字符估算（中文一个字号、ASCII 0.55，留 15%
  余量），只用于这两条判据。
- 离开播放页（后台收听）清空飞行中的弹幕并重置时钟；回来不重放旧聊天。
- `clock` 可注入，测试直接推进，不 sleep。

测试（先红后绿）：`internal/ui/danmu_clock_test.go`

- `TestDanmuClockFollowsPicture`：播放前进、暂停停住、恢复不补跳；
- `TestDanmuClockStepsEvenlyAcrossSampleGaps`：按 30Hz 采样、60fps 推帧，
  每帧步长必须一致（旧实现是 `16.7 / 0 / 16.7 / 0…`，先红后绿）；
- `TestDanmuMovesEveryFrame`：不推 tick、只推一帧，位置也必须动；
- `TestDanmuTracksFitActualLaneCount`：8 条轨道时 Track 不得越过 7；
- `TestDanmuSharesLaneWhenSafe`：同速/更慢的后一条可以接力同轨；
- `TestDanmuSkipsCatchUpLane`：更宽（更快）的后一条必须换轨；
- `TestDanmuReusesFirstSafeLane`：上面那条放得下就继续用，不硬往下排；
- `TestDanmuAdvanceRules`：前进 / 采样间隙 / 超宽限冻结 / 用户暂停 / seek 前跳 /
  回退 / 无播放器 / 时间没走的表格测试。

验证：`make check` 全绿（含 `TestEveryClickableHasLabel`）；`make e2e` 构建通过。
**真实窗口验收（`mygo-automation` 截图 + 暂停前后对比）没有跑**——位置不变量由
`ui.NewTester` 的真实布局坐标断言覆盖，但观感与真窗口的帧调度仍需人工确认。

**观测进生产日志，但不进开关。** `third_party/mygo/plugins/video` 多观测 13 个属性
（含 `hwdec-current`，确认 auto-safe 真的启用了硬解），`video.State` 带出；
`trackContinuity` 只在缓冲起止和音频停走时各写一条日志
（`缓冲开始` / `缓冲结束` / `音频停走`），正常播放不产生日志。

## 回到播放页：音频不断 + 画面 <1s

### 标准（2026-11-21 修订，按 ry 的验收口径）

| 项 | 预算 | 测法 |
|---|---|---|
| 音频 | **不断** | 5ms 采样 `audio-pts`；2 秒窗口应推进约 2 秒；真实 WASAPI underrun 另用 loopback |
| 画面接上 | **< 1000ms** | 从「决定回来」到第一帧有内容、且尺寸为视频原始尺寸的画面 |
| 后台 CPU | **记录，不设硬上限** | 进程 CPU 时间 / 墙钟。旧的 5% 预算与「<1s」不能同时成立，见下 |

这个标准只看用户感知：声音一秒不能断，画面一秒内必须回来。CPU 从约束降为
成本记录，因为在这条流上两者不可兼得。

### 结论

**后台保留解码，只停全尺寸呈现。** 视频轨一直选中，软件渲染目标收到 160×90；
mpv 的 demuxer、AO、音频时钟、参考帧链都不动。回来恢复视频原始尺寸，并用
当前帧立刻重渲染一次。实测（B 站 545068，1080P60 HEVC，硬解）：

- 后台单核 **6.4–8.9%**（5 × 10 秒窗口）；
- 后台 10 秒音频推进 **10.001 秒**；
- 回来到全尺寸画面 **4ms**（5/5 轮）；
- 切换瞬间音频停走 **21ms**（就是 `audio-pts` 的事件粒度），随后 2 秒推进 2.0 秒。

这是唯一同时满足两条标准的方案。其余路径都被数据否掉，记录在下面。

### 证据

**1. 解码不是瓶颈——把网络剥掉。** 把 60 秒直播分片抓成本地 fMP4，同一路
`vid=no → vid=auto` 8 轮恢复画面 **60–139ms，p50 116ms**。解码、渲染、
关键帧搜索本身都很快。

**2. 直播里慢的是「下一个关键帧还没被产出来」。** 同一路直播 `vid=no → vid=auto`
实测 **0.08–5.2 秒**。B 站 m3u8 是 6–7 个 1 秒 fMP4 分片，抽 8 个分片只有 1 个
的首视频样本是 sync sample；`#EXT-BILI-AUX` 的 K/N 与 `first_sample_flags` 一致。
**分片不保证带 IDR，关键帧间隔是场景相关的多秒级**。`vid=no` 重新选轨时 mpv
还会 refresh seek、清空 demuxer 缓存（源码注释：discard the buffered data），
实测每轮 cache 落到 0，所以连回头的关键帧都没有。网络、缓存、seek 都绕不过
「编码器还没吐下一个 IDR」。

**3. 整路重新起播也不满足。** 四段拆解三轮（取流 / 打开探流 / 等关键帧 /
audioGate）：383+350+3−3 = 747ms；399+647+3−3 = 1077ms；417+1287+2−2 =
1699ms。新连接从分片边界起播，第三段几乎为零，首帧大头是取流与连接/探流。
它的音频接缝是 **1.09 / 1.56 秒**（旧路最后一声 → 新路首声，两次），直接违反
「音频不断」。

**4. 保留解码 + 小尺寸呈现（生产）。** 5 轮：后台 6.4–8.9%，音频 10.001/10s，
回全尺寸 4ms，音频停走 21ms。软件解码同一模式后台 21.7–33.4%，回来同样 4ms。

**5. 硬解 A/B。** 前台 20 秒：软件 23.2% / 49.5%，硬解 23.28% / 22.58%；
后台小尺寸：软件 21.7–33.4%，硬解 6.4–8.9%。全部 0 丢帧、0 失时、vf_fps 60。
所以生产把 `hwdec` 从 `no` 改成 `auto-safe`（不支持时退回软件）。

**6. 否掉的中路。**

| 方案 | 数据 | 否掉的原因 |
|---|---|---|
| 回来 seek 到当前音频位置 | 一轮 5 次 114–404ms；另一轮 10 次里 3 次 1.0–1.4s | 不保证 <1s；音频停 120–425ms，还前跳约 1.1 秒 |
| `vd-lavc-skipframe=nonref` | 恢复 138–158ms，后台 CPU 27–32% | CPU 成本与常开解码同价 |
| `vd-lavc-skipframe=nonkey` | 后台 3–10%，恢复 25–34ms | 切回 `none` 触发解码器 reinit + refresh seek，音频往回拖 4.7–71 秒（10 轮） |
| `vd-lavc-skipframe=bidir` | 后台 2–4% | 同上，音频退回 7–13 秒 |
| 影子引擎（第二路 `ao=null`） | 进程 35% → 44.1%，两条连接 | 比常开解码更贵，还多一套时钟 |
| `audio-buffer=1.0` | 把 `vid=auto` 重读的停走 0.26–1.34s → 52–94ms | 只救被弃的 `vid=auto` 路径；生产已回默认 0.2 |
| `cache-secs` / `live_start_index` | 三组样本重叠，`cache-secs=10` 出现 10 秒无画面 | 无可复现收益，保持 mpv 默认 |

### 决策与代价

- `video.Player` 删除本地补丁 `SetVideo`（`vid=no`），换成 `SetPresentSize`
  （160×90 / 原始尺寸 / 格子像素都走它）。改尺寸会用当前帧强制重渲染
  一次，避免一帧小图闪烁。这是本地漂移，和 `hwdec` / `scale` 同类的引擎能力，
  不是新模块、不是用户开关。
- `attachMedia` 只调 `applyPresent()` 放回全尺寸；不取流、不开新引擎、不进 loading。
- **旧 5% 后台预算作废**：硬解 6.4–8.9% 已是这台机器 + RX 9070 XT 的地板，
  软件解码 21.7–33.4%。如果 5% 是硬要求，这条流上「画面 <1s」做不到——
  两者只能选一个。2K/4K 未测，后台成本预计更高。
- 160×90 是给解码器看的，不该被任何界面显示：后台路径（灵动岛）不画视频，
  小窗播放不走 `detachMedia`。若以后有界面在后台画这个流，会看到模糊的小图。

### 未达成 / 未验证

- **真实 WASAPI 接缝**没有 loopback 录音；21ms 是 `audio-pts` 的事件粒度，
  不能当成「音频绝对连续」的硬件级证据。但切换不碰 AO，2 秒推进 2.0 秒。
- **A/V 偏差**没有切换前后对照。
- **2K/4K、斗鱼**的后台 CPU 没测；本轮只有 B 站 1080p60。硬解 `d3d11va-copy`
  每帧要把帧下到内存再转 BGRA，4K 上可能比软件解码更贵；拿到 4K 房间后先复测，
  再决定要不要按分辨率分档。
- **关键帧样本小**：8 个分片、1 个 sync；GOP 的场景相关性没有长期统计。
- **浏览器同房间对照没能做成**：自动化 Chrome 后台标签仍报
  `visibilityState=visible`，`requestVideoFrameCallback` 继续回调，模拟不出切标签。
- `video-pts` 属性在本构建不存在（`property-list` 里没有），曾试加观测，已撤回。

## 未验证

- Clumsy/netem 可控丢包、WASAPI loopback 录音（浏览器对照见上文本轮记录）；
- 弹幕相对画面内容的 offset（缺合成流烧录时间码）；当前只有「约等于稳态读前缓存」
  的估算；
- 斗鱼样本。

## 遗留风险

- 弹幕时钟依赖 `VideoPlayer.Position()`。如果某个后端在音频播放时位置不推进（当前
  libmpv 不会），弹幕会跟着冻结；日志里的 `音频停走` 能看出来。
- 轨道宽度是估算值（留 15% 余量）。估窄了仍可能把会追尾的弹幕放进同轨；真窗口里
  如果还看到叠字，先把余量调大，再考虑首帧实测宽度回填。
- 所有轨道都排不下时的重叠是有意的降级，不是 bug；高密度直播间应该先看到「叠」，
  而不是「弹幕消失」。
- 冻结率样本量小（同一房间、不同时段，0–4 次/180s），不能当长期保证。
