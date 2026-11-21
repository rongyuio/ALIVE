# ALIVE 文档

这里是唯一的文档入口。代码旁边只留导航，事实写在这里。

散落在各包的 README 曾经没人读——因为没有任何地方指向它们。这份索引就是那个指向。

## 分类

| 目录 | 放什么 | 回答的问题 |
|---|---|---|
| `architecture/` | 系统怎么组织、数据怎么流动 | 这条链路长什么样？为什么这样分层？ |
| `decisions/` | 选型取舍、调研结论、迁移方案 | 为什么选它？以后要换成什么？ |
| `reference/` | 实测数字、外部依赖约束、速查 | 现在到底多快？MyGo 这个 API 怎么用？ |

## 全部文档

### architecture

| 文档 | 内容 | 什么时候必读 |
|---|---|---|
| [playback.md](architecture/playback.md) | 播放链路：mpv 负责什么、我们只负责的三件事、bgr0 的 alpha 陷阱、首帧模型、怎么区分上游慢和我们慢 | **动 `internal/playback`、`third_party/mygo/plugins/video`、`internal/ui/player_media.go` 之前** |
| [platform.md](architecture/platform.md) | 四个平台的取流约束：入口、登录门槛、线路语义、编码分支 | 动 `internal/live` 之前，或遇到"取不到流 / 档位不对 / 选错了分支" |

### decisions

| 文档 | 内容 | 什么时候必读 |
|---|---|---|
| [0001-player-backend.md](decisions/0001-player-backend.md) | 播放器后端：Media Foundation、自带 FFmpeg 解码、libmpv 三条路线的取舍与迁移条件 | 想改播放器实现，或觉得"是不是该换 libmpv"之前 |
| [0002-ffmpeg-provisioning.md](decisions/0002-ffmpeg-provisioning.md) | FFmpeg 随包分发、版本锁定、校验落盘（**落地方式已废止**，播放改由 libmpv 自带的 FFmpeg） | 想知道为什么不再分发 FFmpeg、或又想把兜底路径加回来之前 |
| [0003-libav-in-process.md](decisions/0003-libav-in-process.md) | cgo + libav 取代 ffmpeg 子进程的取舍与代价（**实现方式已被 0004 取代**） | 想知道为什么放弃子进程、或重提自研引擎时 |
| [0004-libmpv.md](decisions/0004-libmpv.md) | 播放换成 libmpv：实测数据、mygo#124 为什么不能直接用、软件渲染的天花板、fork 了 mygo 哪一处 | 动播放层、怀疑「是不是该自己写引擎」、升级 mpv 之前 |
| [0005-no-launch-screen.md](decisions/0005-no-launch-screen.md) | 冷启动删掉启动层：为什么它撒谎、锁输入、不可测；换成了首帧即应用 + 静止占位 | 想加回启动画面 / 加载遮罩之前 |
| [0006-palette-ladder.md](decisions/0006-palette-ladder.md) | 配色由六个参数生成，对比度写成测试；强调色拆成填充/当字/当线 | 加配色、改主题令牌、讨论毛玻璃之前 |
| [0007-douyin-search.md](decisions/0007-douyin-search.md) | 抖音为什么只收直播间号：登录只解开 2483，搜索仍要浏览器指纹，且网页搜索召回的是账号不是直播间 | 想给抖音加关键词搜索、或又想把浏览器登录接回来之前 |
| [0008-palette-system.md](decisions/0008-palette-system.md) | 配色的定位、舒适契约、六套主题、旧六套审计、负参考与状态色（**2026-11-20 已落地**） | 改配色、强调色、状态色、表面阶梯之前 |
| [0009-live-continuity.md](decisions/0009-live-continuity.md) | 直播连续性的五类判据、实测归因（上游空档）、弹幕时钟与轨道的定点修复、回到播放页「音频不断 + 画面 <1s」的取舍与否证 | 改播放缓存/音频参数/热切换/后台收听，或再怀疑「直播卡顿」之前 |
| [0010-switch-content-still.md](decisions/0010-switch-content-still.md) | 切平台 / 分区时内容层不动：两个位移根因、六条帧级不变量、动效只留在控件上 | 给首页/关注页内容加动效、改骨架、或再报「切换闪/抖」之前 |

### reference

| 文档 | 内容 | 什么时候必读 |
|---|---|---|
| [measurements.md](reference/measurements.md) | 所有实测：首帧成本模型、路径对照、验证矩阵、编码扫描 | 声称"变快了 / 变慢了"之前，先看基准数 |
| [acceptance.md](reference/acceptance.md) | 驱动真实窗口做验收：mygo-automation 的命令、这个项目踩过的坑、怎么收尾 | 要验真实渲染 / 播放 / 内存，或改完 UI 要截图之前 |
| [ui-regressions.md](reference/ui-regressions.md) | 界面「闪 / 不对」的判定方法：四类现象、逐帧探针、阈值来源、先红证明、真实窗口的极限 | **收到 UI 抖动/闪烁类报障、动内容层或骨架之前** |
| [palette-ladder.md](reference/palette-ladder.md) | 表面三档（地 / 托盘 / 卡片）的 hex、L\*、台阶，以及"静态表面不描边"的规则 | 改顶栏、卡片、面板的底色或描边之前 |
| [palette-briefs.md](reference/palette-briefs.md) | 六套新配色的身份、由来、参照、LCH 输入与取舍（**2026-11-20 已落地**） | 写配色 spec、改主题命名或强调关系之前 |
| [mygo.md](reference/mygo.md) | MyGo 版本演进、生命周期约束、已知限制 | 写 UI、升 MyGo 版本、怀疑是框架问题时 |

## 必读映射

改这些代码之前，先读对应文档。不是建议，是流程。

| 要动的代码 | 必读 |
|---|---|
| `internal/playback/*` | [architecture/playback.md](architecture/playback.md)、[decisions/0004](decisions/0004-libmpv.md) |
| `third_party/mygo/plugins/video/*` | [decisions/0004](decisions/0004-libmpv.md)、`third_party/mpv/README.md` |
| `internal/ui/player_media.go` | [architecture/playback.md](architecture/playback.md)、[decisions/0009](decisions/0009-live-continuity.md) |
| `internal/ui/danmu*.go` | [decisions/0009](decisions/0009-live-continuity.md) |
| `internal/live/*` | [architecture/platform.md](architecture/platform.md) |
| `internal/ui/*`（界面） | [reference/mygo.md](reference/mygo.md)、[reference/palette-ladder.md](reference/palette-ladder.md)、[reference/ui-regressions.md](reference/ui-regressions.md)、根目录 `AGENTS.md` |

## 写作约定

- **一个事实只写一处。** 要引用就写链接，不要复制段落。复制出来的第二份一定会过期。
- **数字必须带日期和条件。** "首帧 2245ms" 没有意义，"B 站 545068 HEVC 1080P，2026-11-16，Ryzen 7 9700X，2245ms" 才有。
- **写为什么，不写是什么。** 是什么看代码。文档的价值在于解释代码里看不出来的取舍、失败过的尝试和它们的代价。
- **失败的尝试要留记录。** "试过 X，更慢，回退" 比 "现采用 Y" 有用得多——它让下一个人不必再试一遍。
- **没测过的不写肯定句。** 标"未实测"，或者干脆不写。

## 新增文档

按主题落到 `architecture/`、`decisions/`、`reference/` 之一，然后在上面两张表里加一行。
没有索引的文档等于不存在。



