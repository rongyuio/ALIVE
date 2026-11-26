# ALIVE 文档

这里是唯一的文档入口。代码旁边只留导航，事实写在这里。

散落在各包的 README 曾经没人读——因为没有任何地方指向它们。这份索引就是那个指向。

## 先读这一份

**每次会话开头先看 [STATE.md](STATE.md)**——它由 `cmd/alive-symptoms` 从 git 历史生成：最近 7 天哪儿在烧、哪个症状第几次了、上次提交改了什么。`make state` 重算。


**[evolution.md](evolution.md)——五条线的演化史。** 这个项目 11 天、421 次提交里，
真正在发生的是五个问题被反复处理。决策文档只记「决定了什么」，没有一篇记「三天后
又推翻了」。改代码之前先在这里找到你踩的是哪条线，再看那条线已经试过什么、拐错了
几次。

## 分类

| 目录 | 放什么 | 回答的问题 |
|---|---|---|
| `evolution.md` | 五条问题的演化：触发、当时以为的原因、实际原因、拐点、白干过什么 | 这条路走过几遍？在哪儿拐弯？ |
| `architecture/` | 系统怎么组织、数据怎么流动 | 这条链路长什么样？为什么这样分层？ |
| `decisions/` | 选型取舍、调研结论、失败过的尝试 | 为什么选它？以后要换成什么？ |
| `reference/` | 实测数字、外部依赖约束、速查 | 现在到底多快？这个 API 怎么用？ |

## architecture

| 文档 | 内容 | 什么时候必读 |
|---|---|---|
| [playback.md](architecture/playback.md) | 播放链路：mpv 负责什么、我们只负责的三件事、bgr0 的 alpha 陷阱、首帧模型、怎么区分上游慢和我们慢 | **动 `internal/playback`、`third_party/mygo/plugins/video`、`internal/ui/player_media.go` 之前** |
| [platform.md](architecture/platform.md) | 四个平台的取流约束：入口、登录门槛、线路语义、编码分支 | 动 `internal/live` 之前，或遇到"取不到流 / 档位不对 / 选错了分支" |
| [watch.md](architecture/watch.md) | 悲鸣观测：12 间全档会话、所有权交接、弹幕边界、16:9 布局、灵动岛 | **动 `internal/watch`、`internal/ui/watch*.go` 之前** |
| [surfaces.md](architecture/surfaces.md) | 四个表面（单直播间 / 后台收听 / 观测墙 / 灵动岛）的统一模型、七条不变量、操作路径与过渡契约 | **动 `internal/ui` 里任何播放所有权、呈现尺寸、岛形态、页面过渡之前** |
| [diagnostics.md](architecture/diagnostics.md) | 诊断事件 schema、run/room/session/engine 关联、症状→证据矩阵、崩溃黑匣子、隐私边界 | **加日志、改状态机、动 `internal/applog`/`internal/diag`/`cmd/alive-diag`、或排「黑格/卡/没声/闪退」之前** |

## reference

| 文档 | 内容 | 什么时候必读 |
|---|---|---|
| [measurements.md](reference/measurements.md) | 所有实测：首帧成本模型、路径对照、编码扫描、内存基线 | 声称"变快了 / 变慢了"之前，先看基准数 |
| [acceptance.md](reference/acceptance.md) | 驱动真实窗口做验收：mygo-automation 的命令、这个项目踩过的坑、怎么收尾 | 要验真实渲染 / 播放 / 内存，或改完 UI 要截图之前 |
| [ui-regressions.md](reference/ui-regressions.md) | 界面「闪 / 不对」的判定方法：四类现象、逐帧探针、阈值来源、真实窗口的极限 | **收到 UI 抖动/闪烁类报障、动内容层或骨架之前** |
| [frame-recording.md](reference/frame-recording.md) | 真窗口逐帧录制（`ALIVE_RECORD`）与两段序列逐帧比对（`cmd/alive-framediff`）：录到的是哪一层、两条判据、阈值的实测出处、灵敏度边界 | **要验 1–2 帧的闪/跳、或要证「改前改后画面一样」之前** |
| [palette-ladder.md](reference/palette-ladder.md) | 表面三档（地 / 托盘 / 卡片）的规则与几何；灵动岛的光环、余晖、动效 | 改顶栏、卡片、面板、灵动岛之前 |
| [palette-briefs.md](reference/palette-briefs.md) | 六族配色的身份、由来、参照、LCH 输入与取舍 | 写配色 spec、改主题命名或强调关系之前 |
| [mygo.md](reference/mygo.md) | 能力边界、上游状态、升级流程、踩过的坑 | 写 UI、升 MyGo 版本、怀疑是框架问题时 |
| [testing.md](testing.md) | 测试怎么才算测住了：L0-L4 断言分级、六类测试、六种废测试、反向回归、变异抽查 | 写或改测试、判断一条测试有没有分辨力之前 |

## decisions

编号递增，一次决策一份，状态和取代关系在
[decisions/README.md](decisions/README.md) 的索引里。被推翻的不删——那是「试过什么、
为什么不行」的唯一记录。

## 必读映射

改这些代码之前，先读对应文档。不是建议，是流程。

| 要动的代码 | 必读 |
|---|---|
| `internal/playback/*` | [architecture/playback.md](architecture/playback.md)、[decisions/0004](decisions/0004-libmpv.md) |
| `third_party/mygo/plugins/video/*` | [decisions/0004](decisions/0004-libmpv.md)、`third_party/mpv/README.md` |
| `internal/ui/player_media.go` | [architecture/playback.md](architecture/playback.md)、[decisions/0009](decisions/0009-live-continuity.md) |
| `internal/ui/danmu*.go` | [decisions/0009](decisions/0009-live-continuity.md) |
| `internal/live/*` | [architecture/platform.md](architecture/platform.md) |
| `internal/watch/*`、`internal/ui/watch*.go` | [architecture/watch.md](architecture/watch.md)、[decisions/0011](decisions/0011-watch-wall.md)、[architecture/playback.md](architecture/playback.md)、[reference/ui-regressions.md](reference/ui-regressions.md) |
| `internal/ui/player*.go`、`island.go`、`pages.go`、`surface.go` | [architecture/surfaces.md](architecture/surfaces.md)、[decisions/0012](decisions/0012-surface-model.md)、[decisions/0009](decisions/0009-live-continuity.md) |
| `internal/ui/*`（界面） | [reference/mygo.md](reference/mygo.md)、[reference/palette-ladder.md](reference/palette-ladder.md)、[reference/ui-regressions.md](reference/ui-regressions.md)、根目录 `AGENTS.md` |
| `record_e2e.go`、`cmd/alive-framediff/*`、`third_party/mygo/ui/record.go` | [reference/frame-recording.md](reference/frame-recording.md) |
| `internal/theme/*` | [decisions/0008](decisions/0008-palette-system.md)、[reference/palette-ladder.md](reference/palette-ladder.md)、[reference/palette-briefs.md](reference/palette-briefs.md) |
| 任何动画 / 过渡 / 悬停反馈 | `internal/theme/motion.go`（唯一的时长表）、`internal/ui/surface_plan.go`（唯一的过渡求值点）、`internal/ui/hover.go`（悬停的两个取用函数）、[architecture/surfaces.md](architecture/surfaces.md) 的「过渡契约」 |
| `internal/ui/follow.go`（关注状态与刷新时机） | [decisions/0014](decisions/0014-follows-freshness.md) |
| `main.go`、`mygo.json`（打包、插件、更新） | [decisions/0015](decisions/0015-self-update.md)、[reference/mygo.md](reference/mygo.md) |
| `internal/applog/*`、`internal/diag/*`、`internal/hang/*`、`internal/metrics/*`、`internal/support/*`、`cmd/alive-diag/*` | [architecture/diagnostics.md](architecture/diagnostics.md) |
| 任何状态机的日志/错误分支 | [architecture/diagnostics.md](architecture/diagnostics.md) 的「事件目录」与「症状→证据矩阵」 |

## 写作约定

**一个事实只写一处。** 要引用就写链接，不要复制段落。复制出来的第二份一定会过期。

**数字必须带日期和条件。** "首帧 2245ms" 没有意义，"B 站 545068 HEVC 1080P，2026-11-16，
Ryzen 7 9700X，2245ms" 才有。

**写为什么，不写是什么。** 是什么看代码。文档的价值在于解释代码里看不出来的取舍、
失败过的尝试和它们的代价。

**失败的尝试要留记录。** "试过 X，更慢，回退" 比 "现采用 Y" 有用得多——它让下一个人
不必再试一遍。

**没测过的不写肯定句。** 标"未实测"，或者干脆不写。

### 四条不要再犯的

这四条是 2026-11-25 那次全量审计清出来的，每一条都在仓库里真实存在过：

**不写会过期的计数。** 「`internal/ui` 里 96 处字面量」「强调色只有两处」这类数字，
写下去的第二天就开始错，而且没有判据能让人判断它错在哪。要写就写**判据**
（「表面色不该写字面量，弹幕颜色本来就该自由」），计数留给实测文档。

**不写这台机器的路径和临时产物。** `.tmp/probes/xxx/`、`/d/tmp/x.png`、`D:\...`——
探针不进仓库，读者按路径找不到任何东西，还以为是自己的问题。写方法和条件，不写路径。

**不写编辑史。** 「本文写作时…后来改成…」「上一节说错了」「第 7 条是文档漂移，顺手改掉」——
读者要解析两遍才知道现在是什么。直接写现在对的那个。

**已废弃的实现用过去时。** 标了「已被取代」的决策文档，正文仍然用现在时写着已经不存在的
函数、变量、界面文案——读者不会记得先读抬头。要么改成过去时，要么删掉那一段。

