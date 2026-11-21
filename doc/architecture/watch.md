# 悲鸣观测（观测墙）

日期：2026-11-21。状态：已落地。取舍见 [0011](../decisions/0011-watch-wall.md)，
平台并发实测见 [measurements](../reference/measurements.md) 的「观测墙并发」。

## 它是什么

关注页的第三个一级分区。观测盒里最多 9 间，同屏显示实时画面；每一格都是一个
**无弹幕的单直播间缩小版**：全档取流、真实音频输出、默认静音、按格子物理像素
呈现。

- 单击格子：激活它，声音切过去，上一间只静音、不重开、不跳路由。
- 双击格子 / 点灵动岛：放大成播放页，**同一个引擎接管**，不重新取流。
- 墙上不显示弹幕，也不保留弹幕连接；只有放大成播放页时才连一条全新的。
- 墙头没有常驻状态；正在播放谁由灵动岛承担。墙上岛展开显示当前房间。
- 墙头的「＋」展开添加面板：一行说明「从关注里挑，点一下收进观测盒」+ 候选
  chip（`＋ [品牌色平台] 名字`）；点 chip 收进观测盒，面板收起。
- 最多 9 间，不是按带宽算的；用户带宽不在产品判断里。
- 墙格可以拖动换位，顺序落盘；右键还有「前移 / 后移」，给键盘和读屏一条不依赖
  拖拽的路径。active 跟着房间身份走，换位不会把声音切到别的格子。

## 分层

```text
internal/ui/follows_page.go     第三个一级分区、分区顺序
internal/ui/watch.go            墙面：布局、格子、控件、添加面板
internal/ui/watch_media.go      wallMedia / wallSession：每格取流、静音、重连
internal/watch/layout.go        纯布局：1–9 间、16:9、铺满不裁剪
internal/ui/player_media.go     active 在播放页时的所有权、弹幕生命周期
internal/playback              libmpv 薄壳；MaxWidth/Height、起播静音、跳过音频门
```

墙和播放页不是两套播放器。`wallSession` 和 `playerMedia` 是同一路引擎在不同
位置的两种所有权。

## 所有权与交接

`a.WatchActive` 是当前出声那间的 `platform|roomID`。**一激活就接管进
`playerMedia`**；`wallSession` 只保留还没出首帧的 pending 格和所有非 active 格。

| 归属 | 什么时候 | 画面 | 声音 | 谁驱动 |
|---|---|---|---|---|
| `wallSession` | 非 active 预览；active 还没出首帧 | 格子物理像素 | 无 | `watchTick` |
| `playerMedia` | 激活完成；从播放页退回墙 | 格子物理像素 | 开 | `mediaTick` |

交接规则：

- **墙上激活 / 退出 active**：只换所有权、声音焦点和强调环；**呈现尺寸
  一像素不动**。activate 和 release 两边都不调 `SetPresentSize`——这两处
  曾经各自补过一次，造成“点一下就变”和“active 变非 active 又变”。首帧前
  是 pending，首帧一到大就自动接管。
- **墙 → 播放页**：`watchAdoptActive(false)` 把 `player` / `stream` / `upstream`
  原样交给 `playerMedia`，`SetPresenting(true)` 恢复全尺寸；弹幕由
  `ensureDanmaku` 重新连一条。取流、首帧、地址都不重来。
- **播放页 → 墙**：`detachMedia` 保留引擎和全尺寸呈现，墙直接画
  `a.media.player`，视频在卡片里 `Contain`；不把渲染目标收到格子。
  弹幕在离开时断开并清空记录。墙上激活、退出、从播放页回来都不碰呈现尺寸；
  只有进播放页的 `SetPresenting(true)` 会切全尺寸。
- **墙上 active 的刷新 / 热切换**：新引擎沿用旧 player 的 `Size()`，不缩回
  160×90；只有首页等非墙页面的后台收听才收小。`adoptPlayback` 用 `lastW/H`，
  `adoptSwap` 用旧 player 尺寸，两条都保住原渲染目标。
- **在墙上激活另一格**：`watchReleaseActive` 把 `playerMedia` 持有的这一路移回
  `wallSession`，静音但不停播；再接管新的一格。旧格子的 `player.Close()` 归墙
  管理，不再由取流 ctx 管。
- **引擎不挂取流 ctx**：`playback.Open` 用 `context.Background()`，生命周期归
  `*ui.VideoPlayer`。否则墙上的画面会在切房 / 换档时被顺手关掉。

墙不可见（播放页、首页、最小化）时，`watchHide` 关掉非 active 的会话；active
继续出声。回墙时 `watchShow` 按布局重新开，错峰由各平台 limiter 兜着。

## 生命周期

- 开格：`fetchStream` 取和播放页同一档位；`playback.Open` 全档、`StartMuted`、
  `SkipAudioGate`、`MaxWidth/Height` 按格子物理像素（2× DIP，上限 1280×720）。
- `ended` / 长时间不推进：先 `RoomInfo`。平台说 `live=false` → 未开播态，停止
  重连；仍 live → 重取流。退避 2s / 8s / 30s / 120s。
- 签名地址到期：`Variant.Expires` 前重开这一路。斗鱼的 300 秒直链在墙上同样适用。
- 单格失败只重试自己；点击失败格就是重试，不需要额外按钮。
- 10 分钟混合 9 路浸泡里见过一次 `ended`，查 `RoomInfo` 是主播下播，不是并发上限。
  这条分流是必须的：下播后无限重连只会把一格永远卡在“连接结束”。

## 弹幕

弹幕只属于播放页。

- 墙里没有 `startDanmaku`、没有 `roomCtx`、没有 WebSocket。
- 离开播放页：`closeDanmaku`，清空 `DanmuLog` / 飞行弹幕 / 面板游标；再进来
  （从墙或从列表）都是全新连接，不保留上一段聊天。
- `ensureDanmaku` 每帧问一次；失败后退避 30 秒，避免平台拒绝时每帧重连。

## 布局

`internal/watch.Layout` 是纯函数，排的是**每格等大的网格**：枚举行数 1..n，
列数 = ceil(n/rows)，所有格同宽同高；每格画面按 16:9 contain，不裁剪。评分取
「所有格里最小的画面面积」最大，同分时空格更少、行数更多者优先；行按列左
对齐，最后一行不满时空位留在行尾右侧。

1440×800 下：2 间一行各约 700×394；3 间 2+1（C 在第二行左列，右下空），
每格约 700×394；5 间 3+2；7 间 3+3+1（右下两格空），每格约 460×259；9 间 3×3。
**不会出现一大几小**，也不会为了塞满而裁剪。行数多了以后单格变小，这是等大网格的代价；换来的是每间房一样大、
视线不用在大小格之间跳。

墙上的格子**统一 16:9**，视频 `Contain`。不按每一路源比例排：

- 平台声明比例不可靠：同一批 B 站房间会给出 4:3，实际是 16:9；
- 读 `player.Size()` 更糟：那是渲染目标，而渲染目标又按格子比例设，会形成反馈，
  首帧到达后整面墙重排一次。

统一 16:9 是稳定的；竖屏/4:3 的流左右留黑，不裁剪。

墙面不画底色，落在页面的 `BgPrimary` 上；每一格是一个卡片：`RadiusMD` +
`BgSecondary` + 顶部 `TopLight`，和直播卡片同一套表面。格间用 `GridGap`（16），
不是自己写的 12。没有整块黑色矩形。

常态格子里只有画面；active 只加一圈强调色内阴影，不改格子大小——激活不该让
整面墙重排。hover 或键盘聚焦时，底部升起一条底栏，**底栏本身就是遮罩**，信息
和控件是一块：第一行是品牌色平台胶囊 + 主播名 + 标题，第二行直接复用播放页控制台：44 高玻璃条，左侧播放/刷新/音量（滑条+百分比），右侧放大/移出；弹幕/清晰度/线路/小窗/全屏都精简掉。每个 hover 的格子都显示完整控制条。active 的播放/暂停、刷新、音量分别直接调用
播放页的 `togglePlayback` / `retryStream` / `volumeControl`，没有墙专用分支；
非 active 格的播放/暂停切自己的 `player`，刷新只重载自己。**音量按格各存一份**
（`wall.volumes`，key 是 `platform|roomID`）：非 active 格显示自己记住的百分比和
静音图标，滑条填充用暗一档的 accent；点滑条 / 滚轮会先激活这一格再改它自己的
值——改的始终只是这格，别的格的值一动不动。active 格的音量仍走 `volumeControl`，
`setVolume` 负责把它记到当前 active 名下，切走再切回来还是那个值。每格音量随
`watchRooms` 一起落盘（`watchRecord.Volume`），重启后还是自己的；没调过的格不写，
重新上墙时继承当时的全局音量。加载态直接复用播放页整块遮罩、30px spinner 和 `正在拉取直播流…`，不再自己画小胶囊。不 hover 时不显示这些。

接通中 / 未开播 / 预览失败的提示不等 hover，常显在格子顶部中间；那是状态，
不是 chrome。

## 灵动岛

- 在观测墙上、且有 active 时：岛展开，显示当前房间标题、平台/房间号、头像；
  整块是回播放页的热区。
- 观测墙上没有 active 时：保持首页的时钟岛，不缩成圆。
- 其它页面后台收听：仍收成一枚头像圆。
- 岛上显示的是实际播放的档位；格子左上角也写 `平台 · 档位`，避免“墙上是原画、
  点进去变蓝光”没人发现。

## 落盘

`state.json` 的 `watchRooms` 只存房间身份和展示元数据：platform、roomID、name、
title、cover、avatar、hue、viewers。不存签名地址、不存会话状态、不存播放进度。
恢复时先出封面，不自动起播；最多 9 间，重复与空身份在读盘时清掉。

## 验证

- `internal/watch`：布局 1–9 间、16:9/竖屏/混合、边界、无重叠、上限。
- `internal/ui`：空态、两间大格、九间上墙、单击激活不跳路由、静音控件、双击进
  播放页、9 间上限与去重、添加面板、落盘、卡片右键入口、墙上岛展开。
- 真实窗口（2026-11-21）：7 间同时出画；单击激活；岛展开；点岛进播放页日志
  `观测墙接管进播放页`，无加载层；退回墙日志 `detach`，同一引擎继续出声。
  截图在 `/d/tmp/watch-final.png` 等（临时文件，不入仓库）。
- 未跑：12 间以上的布局（产品上限 9）；4K 格子的 CPU；平台限流时的真实退避曲线。
