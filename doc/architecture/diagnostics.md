# 诊断事件：让日志回答排障问题

> 这份文档回答一件事：出了问题，**从哪条事件、用哪条命令、在多长时间内**能定位。
> 它不是日志字段清单，是「症状 → 证据」的契约。改状态机、加错误分支、动播放/墙/弹幕
> 之前先读这里；新增事件先改这张表，再改代码。

## 0. 结论先行

日志的价值不是行数，是**可回答的问题数**。一条事件只有在同时具备下面四件事时才
算证据：

1. **身份**：`run`（进程）→ `room`（`platform|roomID`）→ `session`（一轮起播/一路
   墙格）→ `engine`（一个播放引擎实例）。缺任何一层，多开、重启、12 路同屏时就
   无法归因。
2. **边沿**：每个状态机记**进入和退出**两侧。只记失败不记恢复，日志只能证明「坏
   过」，不能证明「好过」；重连循环和正常刷新会长得一模一样。
3. **原因**：错误保留 `class` 之外可安全记录的 `op` / `status` / `host` / 首行原文。
   只留一个词等于没记。
4. **顺序**：每行有毫秒时间戳；跨线程/跨进程用 `run` + 单调 `session` 序号排序。

代码落点：

| 层 | 位置 | 职责 |
|---|---|---|
| 语言 | `internal/diag` | 字段名、事件名、关联 Ref、错误拆解、唯一脱敏出口 |
| 落盘 | `internal/applog` | 每 run 一个文件、异步写、级别/组件过滤、环形缓冲、crash 输出 |
| 读侧 | `cmd/alive-diag` | timeline、症状查询、support bundle |
| 接线 | `internal/ui/*`、`internal/playback`、`third_party/mygo/plugins/video` | 在状态机边沿发事件 |

## 1. 关联模型

```
run   = <日期>-<时刻>-<pid>-<进程内序号>   一次进程启动（也是日志文件名）
  room    = huya|485503               一间房，跨 run 稳定
    session = watch#17                 这一格的第 17 次起播（墙）
    session = 取流#8                   播放页的第 8 轮起播
      engine = 42                      这一路的播放引擎实例（进程内单调）
```

规则：

- `run` 由 logger 本身带着（`With().Str("run", …)`），调用点不许自己写。它不在
  每行追加而是在造 logger 时挂上——结构上就不可能写出第二个 `run`。
  例外：`log.rotate` / `log.dropped` 由写入器直接落盘，不带 `run`。
- `room` 只来自 `platform|roomID`，不写房间名（名字会变、会重名）。
- `session` 只在**重开**时变；换清晰度/换线路保留同一个 session，弹幕另有
  `danmaku#N`。
- `engine` 只在 `playback.Open` 成功时分配，`engine.open` / `close` / `destroy`
  三条边沿共用它。

**依赖方向**：`diag` 不认识任何业务包。错误的分组和细节由业务错误自己实现
`DiagClass() string` 和 `DiagError() (platform, op string, status int, text string)`，
`diag` 只认接口。这条不是洁癖：`live` 要写日志就得 import `diag`，而 diag 曾经
反过来 import `live` 去认 `*UpstreamError`——两边一旦互相依赖，谁也编译不过。

## 2. 事件 schema

### 2.1 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `time` | string | 事件时刻，RFC3339 纳秒精度；同一秒内的先后不能丢 |
| `level` | 枚举 | 小写 `debug` / `info` / `warn` / `error` |
| `msg` | string | 给人看的中文；机器只认 `event`，改文案不改 key |
| `source` | string | 最近的日志调用点 `文件:行`，只留最后两段路径 |
| `event` | 枚举 | 稳定英文键，`子系统.边沿`；改文案不改它 |
| `sub` | 枚举 | `player` / `wall` / `danmaku` / `focus` / `nav` / `follows` |
| `run` | string | 进程运行 ID |
| `room` | string | `platform|roomID` |
| `session` | string | `取流#N` 或 `watch#N` |
| `engine` | uint | 引擎实例 ID |
| `stage` | 枚举 | 失败发生在哪一步：`fetch` / `variant` / `native` / `library` / `new` / `volume` / `load` / `play` / `provider` / `first-frame` / `empty` |
| `cause` | string | 这次边沿的原因；可以是用户话术（墙上常见「地址到期」「首帧超时」），但机器判据优先用 `expire` / `stalled` / `ended` / `error` / `user-reload` |
| `attempt` / `restarts` | int | 单调重开次数；只在用户手动 reload 时清零 |
| `failures` | int | 当前退避计数；**会被首帧清零**，不能拿来数循环 |
| `retry_ms` / `retry_at` | int / time | 下一次重试的间隔与时刻 |
| `since_frame_ms` | int | 距离上一次首帧多久；没有出过帧时不写 |
| `open_ms` | int | 从开始取流/引擎打开到这一条边沿的耗时 |
| `age_ms` | int | 引擎/会话存活时长 |
| `ms` | int | 相对 session 起点的耗时（播放页事件） |
| `err.class` | 枚举 | `timeout` / `canceled` / `blocked` / `unsupported` / `upstream` / `network` / `fs` / `error` |
| `err.op` / `err.status` / `err.host` / `err.text` / `err.platform` / `err.type` / `err.timeout` | | 可安全记录的细节；`err.text` 已脱敏、截断 240 字 |
| `quality` | string | 平台档位标签 |
| `line` | string | 实际选中的线路节点 id（斗鱼 cdn / 虎牙 tx-al-hs） |
| `url_host` | string | 选中地址的 scheme+host；不含 path/query |
| `variants` / `lines` | int | 平台返回的档位/线路数 |
| `requested` / `applied` | string | 用户意图与实际生效值（清晰度/线路） |
| `pos_ms` / `audio_ms` / `audio_ok` | int / bool | 时间轴位置、音频时钟、音频属性是否可用 |
| `declared_width` / `declared_height` | int | 平台声明的分辨率；0 表示平台没给 |
| `decoded_width` / `decoded_height` | int | 解码器实际解出的分辨率 |
| `display_fps` / `vfps` | float | 显示器刷新率 / 实际视频帧率 |
| `new_frames` / `render_calls` | uint | 停帧窗口内解出的新帧数 / 视图取帧次数 |
| `gpu` / `gpu_frames` / `renders` | bool / uint | 零拷贝是否启用、实际出帧数、渲染次数 |
| `module` / `text` / `path` | string | mpv 模块前缀、mpv 原文、状态文件路径 |
| `origin` | string | slog 桥记录的真实调用点（`文件:行`）；系统 `source` 只到桥 |
| `reason` | string | `run.stop` 的退出原因（`quit` / `error`） |

单位后缀是契约：`_ms` 毫秒，`_bytes` 字节，`_s` 秒。没有后缀的字符串字段不加单位。

**错误只挂在 `err` 这一个键上**（`diag.ErrField`），不另开 `error`。落盘时它是一个
嵌套对象，读侧按 `err.class` 这种点分键取；`alive-diag` 把嵌套摊平成扁平键，
所以读 event 字段和读错误细节是同一套写法。

事件还可以带自己的排障字段（`failures_before`、`cache_ms`、`hwdec` 等）；本表只列
跨事件通用的那些，`schema_test` 保证读侧用到的字段都在表里。

### 2.2 事件目录（必须成对出现）

| 子系统 | 进入 | 离开/恢复 |
|---|---|---|
| 进程 | `run.start` | `run.stop` |
| 播放页 | `session.begin` / `session.path` / `session.open` | `session.fail` / `session.cancel` / `session.open_fail` |
| 取流 | `session.fetch` | `session.fetch_fail` |
| 引擎 | `engine.open` | `engine.open_fail` / `engine.close` / `engine.destroy` / `engine.destroy_timeout` / `engine.gpu_fallback` |
| 首帧 | `session.first_frame` | `session.recover`（失败后第一次出帧） |
| 换地址 | `session.swap_start` | `session.swap_done` / `session.swap_fail` |
| 换地址被跳过 | `session.swap_skip`（debug：这一路没有媒体轮次） | — |
| 连续性 | `session.buffer`（state=start） | `session.buffer`（state=end） / `session.audio_stall` / `session.video_stall` |
| 墙格 | `wall.open` | `wall.fail` / `wall.offline` / `wall.close` |
| 墙格就绪 | `wall.ready` | `wall.first_frame` |
| 墙格重试 | `wall.restart` | `wall.first_frame`（恢复） |
| 墙可见性 | `wall.show` | `wall.hide` |
| 墙探测/重载 | `wall.probe` | `wall.reload`（用户动作） |
| 用户重试 | `session.retry` | — |
| 焦点 | `focus.change`（from/to） | — |
| 路由 | `route.change`（from/to） | — |
| 呈现尺寸 | `present.change`（from/to 档位） | — |
| 用户选择 | `session.quality_pick` / `session.line_pick`（requested） | — |
| 弹幕 | `danmaku.connect` | `danmaku.fail` / `danmaku.recover` |
| 关注刷新 | `follows.refresh`（reason: boot/enter/restore/heartbeat/manual） | `follows.refresh_done`（updated/failed/retry_ms） |
| 状态 | `state.load_fail` / `state.save_fail` | — |
| mpv | `mpv`（module/level/text） | — |
| 日志自身 | `log.start` / `log.rotate` / `log.dropped` / `log.stop` | — |
| 执行跟踪 | `trace.start` | `trace.stop` |
| 卡死 | `hang`（同时写 `hang-<run>-<stamp>.log/.dmp`） | — |

**级别约定**：终态失败 `Warn`（进程级/不可恢复 `Error`）；正常阶段 `Info`；每帧/
每条细节 `Debug`；正常取消（用户关房、换台）`Debug`，不当 `Warn` 记。

## 3. 症状 → 证据矩阵

每条都对应 `alive-diag answer <症状> <日志>` 的查询。查询必须在 **≤3 条命令、
≤5 分钟**内由没参与实现的人答出。

| 症状 | 第一条命令 | 决策树 | 必需事件 |
|---|---|---|---|
| 某一格黑着 | `answer black-tile` | 有 `wall.open` 无 `wall.first_frame` → 看最后一次 `wall.fail` 的 `stage`/`err`；有 `wall.first_frame` 后变黑 → 看 `wall.restart` 的 `cause` 与 `since_frame_ms` | `wall.open` / `wall.ready` / `wall.first_frame` / `wall.fail` |
| 一直重连 | `answer reconnect-loop` | `restarts` 增长且 `since_frame_ms` 很小 → 崩溃循环；墙的地址到期记 `cause=地址到期`（播放页换地址记 `expire`）；`cause=user-reload` → 用户操作 | `wall.restart` / `wall.first_frame` / `session.swap_*` |
| 播放页打不开 | `answer player-fail` | `session.fetch_fail` → 平台/CDN；`session.open_fail` → 看 `stage=library/new/load/play` 与 `err.text` | `session.fetch_fail` / `session.open_fail` / `session.fail` |
| 没声音 | `answer no-audio` | `audio_ok=false` → 没有音轨或属性不可用；`session.audio_stall` 且 `audio_ok=true` → 音频时钟停；`mpv module=ao` → 设备层 | `session.audio_stall` / `session.buffer` / `mpv` |
| 画面糊/档位不对 | `answer quality` | 请求档位与 `decoded_width/height` 对比；`declared_*=0` → 平台没声明；`quality` 与请求不同 → 平台降档 | `session.fetch` / `session.first_frame` |
| 卡一下/停帧 | `answer freeze` | `session.video_stall` 看 `render_calls` 与 `new_frames`：都不涨=没人取帧，前者涨后者不涨=解码/渲染；`mpv` 看 `hls` 重载 | `session.video_stall` / `session.buffer` / `mpv` |
| 弹幕不显示 | `answer danmaku` | `danmaku.fail` 的 `err.class`：`blocked`=风控，`network`=网络，`upstream`=握手；有没有 `danmaku.connect` 判断是否恢复 | `danmaku.connect` / `danmaku.fail` / `danmaku.recover` |
| 绿点是旧的 / 全灭 | — | `follows.refresh_done` 的 `failed`：整轮全失败看 `retry_ms`（正在退避，下一轮会自动重试）；`reason` 告诉你这一轮是谁发起的——没有 `enter`/`heartbeat` 说明停留在关注页期间根本没刷 | `follows.refresh` / `follows.refresh_done` |
| 设置丢了 | `answer settings` | `state.load_fail` 的 `stage=read/parse`；`state.save_fail` 的 `stage=mkdir/write/rename` | `state.*` |
| 闪退/打不开 | `answer startup` + `answer last-run` | 启动行看 `version/commit/pid`；没有 `run.stop` = 非正常退出；`crash-<run>.log` 看栈 | `run.start` / `run.stop` |
| 卡死/未响应 | `answer freeze` + `crash/recent` | 日志最后一条之后完全安静 → 看 `recent-<run>.log` 的最后 512 行；`hang-<run>-<stamp>.log` 里有 goroutine 全量栈和 minidump | `run.start` / `hang` |

## 4. 读侧

```text
make diag                                     # 构建 build/alive-diag.exe
alive-diag symptoms                           # 症状表
alive-diag timeline <日志或目录>              # 按 room/session 重建时间线
alive-diag answer reconnect-loop <日志或目录>  # 直接回答一个问题
alive-diag bundle <日志目录> <out.zip>        # 收集证据（日志/recent/crash/hang/trace/pprof）
```

桌面入口：顶栏「数据同步」→「诊断」→「导出诊断包 / 30 秒性能剖析」。诊断包写到
日志目录，面板上直接显示完整路径；不需要选目录，也不读 state.json/登录态。

`timeline` 的输出以 `== <room> <session>  (N 条) ==` 分组；每行是
`时间 级别 event 中文消息 字段...`。机器解析只依赖 `event` 和字段名，中文消息
可以改。

## 5. 落盘、崩溃与隐私

**格式是 JSON，一行一个对象。** 日志是给机器和 agent 读的：`alive-diag` 和
任何一次排障都按字段取值，JSON 让这件事由 `encoding/json` 保证，而不是靠一个
手写的 `key=value` 切分器——那个切分器要自己处理引号、转义、带空格的值，解析
错一处就是排障时少一行证据。嵌套对象照原样保留（`err{class,text,…}`）。

- **每 run 一个文件**：`%LOCALAPPDATA%\alive\logs\alive-<run>.log`；保留最近
  `ALIVE_LOG_KEEP` 份（默认 5）。多开、重启、旧构建不会再混成一份。
- **异步写**：logger 只做格式化，`io.Writer` 过滤后入队（4096 行）；解析、指标、
  脱敏、重编码和文件 I/O 全在单写 goroutine 上。调用方（含 UI 线程）只付一次
  拷贝和一次入队。队列满时丢**新来的**那一行并计数（`log.dropped` 在 writer
  退出时补写一条）。
- **一次解析，两件事**：写线程把那一行 JSON 解码一次——`metrics.Observe`
  读走 `event`/`room`/`stage`/`err.class`，`redactValue` 改写每个字符串。指标和
  脱敏都不另开一条通路，也就不会和日志口径漂移。
- **黑匣子**：最近 512 行常驻内存，每 2 秒写 `recent-<run>.log`；崩溃时
  `debug.SetCrashOutput` 写 `crash-<run>.log`，`main` 里设了
  `debug.SetTraceback("all")`，所以这份是**全量** goroutine 栈。
- **脱敏**：`diag.Sanitize` 是唯一出口——URL 去 query/fragment/userinfo
  （http、rtmp、ws 都认）、`token/sign/key/session/cookie/bili_jct/...` 打码、
  `Authorization: Bearer/Basic` 的凭据本体打码、Windows 用户名目录换 `***`
  （正反斜杠都认）、换行压平、截断 240 字。
  它挂在**落盘前**（`applog` 的 `redactValue`），对所有字符串一视同仁，不只在
  错误路径上。位置比 slog 时代低了半层，性质没变：仍然不依赖调用点自觉。

  为什么不在写入时脱敏：slog 的 `ReplaceAttr` 能逐个属性改写，zerolog 没有等价物
  ——它的 `Hook` 只能追加字段，改不了已经写进去的值。于是这层挪到「写线程解析
  出来的对象上」，顺便和指标观察共用同一次解码。
- **标准库桥**：`Setup` 用 `installStd` 把 std log 和 slog 接到同一个 sink。
  MyGo 全程 `log.Printf`，`plugins/video` 用 `slog.Warn/Debug/Info`；不接的话
  它们退回 stderr，而双击启动的 GUI 进程没有 stderr——mpv 的警告和帧统计会
  从日志里消失（换 zerolog 那次就漏了这一步）。std log 记为 `event=stdlog`，
  原文放 `text`；slog 保留级别和结构化属性，真实调用点放 `origin`（系统
  `source` 只能看到桥这一帧）。`Close` 时还原，`TestStdRestoredOnClose` 钉住。
- **级别/组件**：`ALIVE_LOG_LEVEL=debug|info|warn|error`；`ALIVE_LOG_COMPONENTS=
  wall,player,danmaku` 只放行这些子系统的 Debug。mpv 级别用
  `ALIVE_MPV_LOG_LEVEL=info|v`。这些都是记录详细度开关，不切换业务路径。
- **启动前的日志**：`applog.Setup()` 之前的调用（`ui` 包的 `init` 里注册字体失败）
  走 stderr 的兜底 logger。命令行启动可见；双击启动没有 stderr，这一条仍不可见
  ——冷路径，已知。
- **卡死黑匣子**：`internal/hang` 每 5 秒向 UI 线程投一次探针，10 秒无响应写
  `hang-<run>-<stamp>.log`（全量 goroutine 栈 + 最近 512 条事件 + 指标 + 引擎数）
  和同名 `.dmp`（`dbghelp!MiniDumpWriteDump`）。最小化时暂停，5 分钟限一次。
- **执行跟踪**：`ALIVE_DIAG_TRACE=1` 写 `trace-<run>.out`，`go tool trace` 打开；
  诊断页签的 30 秒剖析写 `cpu-<stamp>.pprof`，`go tool pprof` 打开。

## 6. 外部标准

实现和验收不靠「我觉得够了」，对照这些公开标准：

| 标准 | 用在哪 |
|---|---|
| [OpenTelemetry Logs Data Model](https://opentelemetry.io/docs/specs/otel/logs/data-model/) / Semantic Conventions | 字段命名（`service.version` 类）、severity、body/attributes 分离、trace/span 关联；不引入 SDK，只借形状 |
| Google SRE Book / Workbook（四个黄金信号、调试一章） | 延迟/错误/饱和度的划分；排障按「先分层再归因」 |
| Brendan Gregg, *Systems Performance*（USE：Utilization/Saturation/Errors） | GPU/CPU/句柄/音频的饱和度证据；`renders`/`gpu_frames`/`cache_ms` 的选择 |
| Go 官方诊断文档（`runtime/trace`、`pprof`、`debug.SetCrashOutput`、`GODEBUG`） | 崩溃、卡死、goroutine 栈的权威来源 |
| `third_party/mpv/include/mpv/client.h` | mpv 日志级别、事件 `reason`、属性名；不凭记忆写 |
| Windows Performance Toolkit / ETW / WPR | D3D11/GPU/线程停顿的深挖配方（下一批） |
| OWASP Logging Cheat Sheet | 不记录凭据/PII；脱敏测试 |
| RFC 3339 / ISO 8601 | 时间戳；`_ms`/`_s` 单位 |
| 本仓库 `AGENTS.md` §4 | L3/L4 证据、反向回归、一个事实一个写者 |

## 7. 验收：怎么证明它真的能排障

不是「日志里有了这些字段」，而是下面五条都能复现：

1. **症状查询**：`cmd/alive-diag/symptoms_test.go` 用一份 fixture 覆盖 10 类故障，
   逐条断言 `alive-diag answer` 给出上面矩阵里的答案。
2. **事件序列**：`internal/ui/diag_events_test.go` 固定墙的
   `fail → first_frame → restart → offline → close` 序列，断言每条都带
   `room/session/event` 和排障字段。删掉任一事件，测试必须红。
3. **隐私**：`internal/applog/applog_test.go` 喂入带 `?token=`、`C:\Users\<名>`
   的错误，断言落盘内容不含 token/用户名、但保留 host。
4. **性能与热路径**：分两层看。
   - `BenchmarkEmit`（`internal/diag`）测的是 zerolog 构造一条事件，
     不含 applog 的任何处理；它说明事件 DSL 的开销，不是「写一行日志」的开销。
   - 真实落盘（`applog.L().Info()…Msg()`，2026-11-25，Ryzen 7 9700X）：
     **1.0 µs/op、910 B/op、14 allocs/op**；带 `diag.Err` 的错误路径
     **2.6 µs/op、1674 B/op、44 allocs/op**。这是调用方真正付的钱：一次拷贝、
     一次入队。解码、脱敏、重编码都在写线程上。
   - 同一探针在把 `consume` 挪进 writer 之前是 **18.6 µs/op、5567 B/op、
     163 allocs/op**——当时解析和脱敏同步压在调用方线程上，文档曾误写成
     「不占调用方时间」。
   - `BenchmarkErr` 5.2 µs/op 测的是 `diag.Err` 本身（含脱敏正则），不是写路径。
   日志入队不阻塞调用方，队列满丢新来的行并计数。
   `internal/ui/render_log_test.go` 用 AST 钉住：渲染文件里
   不许出现 `zerolog`、`diag.L()`、`.event(...)` 家族。
5. **落盘不等 Close**：`internal/applog/flush_test.go` 连续写十几条、跨过两次
   `recent` 刷新，断言主日志一行不少。只测第一条会放过「writer 起来一下就死了」。
6. **反向回归**：把 `watchFail` 的 `s.event` 删掉，`TestWallLifecycleEvents`
   必须红；把 `diag.Sanitize` 的 URL 分支删掉，隐私测试必须红；把 `run` 写回
   `hang.dump` 的调用点，`TestDumpEmitsHangEvent` 必须红；把 `runWriter` 退出时
   那次 `metrics.Observe` 删掉，`TestDroppedIsCountedAndReported` 必须红
   （报「log_dropped_total 还是 0」）。只绿不算数。

### 7.1 一次真实运行的证据

**2026-11-25，换 JSON 之后**：隔离 `APPDATA`/`LOCALAPPDATA` 启动 e2e 构建。
下面是 `alive-<run>.log` 的形状照抄，路径和 `source` 的行号已隐——行号随代码移动，
抄进来第二天就不对了：

```json
{"components":"","event":"log.start","keep":5,"level":"info","max_bytes":8388608,"msg":"日志就绪","path":"…\\alive\\logs\\alive-20261125-124609-10660-1.log","run":"20261125-124609-10660-1","source":"applog/applog.go:162","time":"2026-11-25T12:46:09.2432538+08:00"}
{"commit":"eb5e393…","event":"run.start","frame_stats":"","go":"go1.27.3","level":"info","log":"…","modified":true,"mpv_log":"warn","msg":"进程启动","os":"windows/amd64","pid":10660,"run":"20261125-124609-10660-1","source":"ALIVE/main.go:89","time":"2026-11-25T12:46:09.2466273+08:00","version":"unknown"}
{"event":"wall.hide","level":"info","msg":"墙隐藏","run":"20261125-124609-10660-1","source":"ui/watch_media.go:481","sub":"wall","time":"2026-11-25T12:46:09.2750153+08:00"}
```

三个字段值得对照：`msg` 是契约里的键（不是 zerolog 默认的 `message`）、
`source` 只留最后两段路径、`time` 是纳秒精度。

**同一批代码在日志换成 JSON 之前的形状**（隔离环境、观测墙预置三间房）：

```text
event=wall.show rooms=3 opened=3 closed=0
event=wall.open room=bilibili|1880715130 session=watch#1 attempt=0
event=wall.offline room=bilibili|1880715130 session=watch#1 cause=offline retry_ms=85000
event=wall.open room=douyu|138243 session=watch#1 attempt=0 quality=蓝光4M
event=engine.open engine=1 room=douyu|138243 session=watch#1 role=wall gpu=true
                url_host=https://hwa.douyucdn2.cn open_ms=81
event=wall.ready room=douyu|138243 session=watch#1 open_ms=601 quality=蓝光4M gpu=true
event=present.change room=douyu|138243 session=watch#1 from=full to=tile w=1280 h=720
event=wall.first_frame room=douyu|138243 session=watch#1 open_ms=1402 failures_before=0
event=engine.close engine=1 room=douyu|138243 session=watch#1 role=wall age_ms=21998
event=run.stop reason=quit uptime_ms=30140
```

（这一段是 `alive-diag timeline` 渲染出来的视图，不是落盘格式。）

`alive-diag timeline` 把三间房分成三条线；`answer last-run` 报「正常退出 reason=quit」。

## 8. 现状与下一批

已落地（2026-11-25）：

- `internal/diag`：字段/事件/Ref/Err/Sanitize + 测试。
- `internal/applog`：每 run 文件、异步写、级别/组件过滤、环形缓冲、recent、
  crash 输出、保留策略 + 测试。
- 播放页：`session.begin/fetch/fetch_fail/path/open/open_fail/first_frame/
  fail/recover/swap_*/buffer/audio_stall/video_stall/retry`，每条带 room/session。
- 墙：`wall.open/ready/first_frame/fail/offline/restart/close/show/hide/probe/
  reload`，重连带 `attempt`（单调）与 `since_frame_ms`。
- 引擎：`engine.open/open_fail/close/destroy/destroy_timeout/gpu_fallback`；
  `open` / `open_fail` / `close` 和等待销毁时的 `destroy` 带 id/key/role，
  `destroy_timeout` 与插件里的 `gpu_fallback` 只带 cause/err。插件零拷贝成功行
  降为 Debug，回退有原因。
- 焦点：`focus.change`（from/to）；呈现尺寸：`present.change`（档位）。
- mpv：焦点路完整转发（module/level/text/engine），非焦点墙格只转 error/fatal
  且每引擎限 50 条；游标按播放器复位。
- 读侧：`alive-diag timeline/answer/symptoms/bundle` + fixture 测试。

第二批（2026-11-25，已落地）：

- **卡死看门狗**：`internal/hang` 独立 goroutine 向 UI 线程投探针，10 秒无响应
  写 `hang-<run>-<stamp>.log`（全量 goroutine 栈 + 最近 512 条事件 + 指标 +
  引擎数），并调 `support.Minidump` 写同名 `.dmp`；写完后在主日志补一条
  `event=hang`（reason / run / file），让 `timeline` 和 `answer freeze` 能找到
  这次卡死。最小化时暂停，5 分钟限一次。
- **support bundle**：「数据同步 → 诊断 → 导出诊断包」把最近 3 次运行日志、
  recent、crash、hang、trace、pprof 和 `meta.json` 打成一个 zip（指标在
  `meta.json` 的 `metrics` 键里，没有单独的 `metrics.json`），落在日志目录；
  不读 state.json/登录态。
- **指标**：`internal/metrics` 在落盘前观察事件（`Observe` 读解码后的字段），
  输出首帧 p50/p95、每房重连数、失败按 stage/class、引擎存活数、mpv 错误按
  module、日志丢弃数。丢弃数不再是恒 0：补写那一条会自己补一次指标。
- **标准库桥**：`installStd` 把 std log 与 slog 接进 sink；`TestStdLogAndSlogBridge`
  断言帧统计被 noise 丢掉、普通 std log 落成 `event=stdlog`、slog 带着级别和
  属性落盘并保留 `origin`。`installStd` 删掉，这条测试必红。
- **trace/pprof**：`ALIVE_DIAG_TRACE=1` 写 `trace-<run>.out`；诊断页签的
  「30 秒性能剖析」写 `cpu-<stamp>.pprof`，`go tool pprof` 直接打开。
- **Windows minidump**：`dbghelp!MiniDumpWriteDump`（stdlib syscall，不引 cgo），
  单测验证当前进程能写出非空 dump。
- **schema 测试**：`internal/diag/schema_test.go` 解析本文事件表，断言文档与
  代码的 `Ev*` 常量双向一致；字段侧也钉住：`alive-diag` 的查询字段必须来自
  `diag` 常量并出现在本文字段表里，`e.str("字面量")` 直接报错。
- **UI 意图**：`route.change`（from/to）、`session.quality_pick` /
  `session.line_pick`（requested）；`session.fetch` / `wall.ready` 带 `line`
  和 `url_host`（`wall.open` 只带 quality/line）；`session.first_frame` 带
  `display_fps` / `vfps`。
- **故障模式**：`ALIVE_MOCK=blocked|timeout` 让 mock provider 真的返回
  `ErrBlocked` / `DeadlineExceeded`，走真实的 `fetch_fail` / `danmaku.fail`
  路径；`TestCorruptStateLogsLoadFailure` 用坏 `state.json` 验 `state.load_fail`。
- **实测**：真实窗口点「导出诊断包」生成 2.1 KB zip（日志 + recent + crash +
  meta + metrics，无 state.json）；「30 秒性能剖析」生成 27 KB pprof，
  `go tool pprof -top` 可读；`ALIVE_DIAG_TRACE=1` 生成 3.6 MB trace，
  `go tool trace -d=parsed` 可解析；正常关窗 68 秒无卡死误报。
- **失败路径实测**：`ALIVE_MOCK=blocked` 真实运行打开一个 mock 房间，日志给出
  `route.change from=/ to=/player`、`danmaku.fail err.class=blocked
  err.text="live: 平台风控: mock 403"`、`session.fetch_fail err.class=blocked`，
  正常关窗 `run.stop reason=quit`。坏 `state.json` 由
  `TestCorruptStateLogsLoadFailure` 验 `state.load_fail stage=parse`。

仍然留给下一批：

1. **真实网络故障注入**：mock 的 `blocked`/`timeout` 和坏 state 已覆盖错误
   分类路径；真实平台的 DNS 失败、慢流、真实 403 还没有注入，需要本地代理
   或录制回放。
2. **真实崩溃的 minidump**：当前 minidump 是卡死/按需触发；未处理异常需要
   `SetUnhandledExceptionFilter` 或独立看护进程。
3. **枚举级 schema**：字段名/事件名已进 CI；`stage`/`cause`/`err.class` 的
   取值集合还没校验。
4. **指标持久化**：现在只有当前进程快照；跨运行的趋势（每天首帧 p95）需要
   落一份小 JSON 或走指标存储。

## 9. 不记什么

- **不按帧写**：任何 `Frame`/`Layout`/`Paint` 路径不写日志。状态翻转才写。
- **不记原始 URL**：只记 `scheme://host`；query 就是签名。
- **不记 cookie/token/响应体全文**：`err.text` 只留首行、脱敏、截断。
- **不把日志当指标**：重连率、首帧分位数走指标，日志只回答「为什么」。

## 10. 为什么是 zerolog（2026-11-25）

前一批的结论是「不引第三方日志库，`slog` + 一个 handler 够用」。这条被推翻了，
但推翻它的**不是性能**——渲染线程本来就被 AST 禁止写日志，494 ns/op 没人感觉得到。

真正的理由是那 300 行手写 handler 一直在出错：

- `run` 靠 handler 每行 `AddAttrs` 补，于是调用点手滑写一次就落成两个 `run=`；
- 队列丢弃计数是直接写文件的，绕过了 handler，所以 `log_dropped_total` 恒为 0；
- 一个事实（「这一行带哪些字段」）分散在 `Handle`、`WithAttrs`、`ReplaceAttr` 三处。

换成 zerolog 之后，前两条从结构上消失：`run` 在造 logger 时就挂上（`With()`），
不存在「每行补一次」这个动作；指标和脱敏合并到落盘前的同一次 JSON 解码，补写的
行也能自己补一次指标。

**代价，说清楚**：

- `zerolog.Hook` 只能追加字段，读不回来也改不了。所以指标观察从 Handler 挪到
  writer（解析后读 event），脱敏从 `ReplaceAttr` 挪到解析后的对象（`redactValue`）。
  「唯一脱敏出口」这条不变量换了位置，没换性质。
- 消息键默认是 `message`，不是契约里的 `msg`；时间默认秒精度。两个都要在
  `applog` 的 `init()` 里改掉，而且必须在任何 logger 造出来之前生效。
- 字段写法从 `slog.Info("消息", "k", v)` 变成 `.Info().Str(k, v).Msg("消息")`。
  一百四十多处调用点一次性迁移，`internal/ui` 里用 `logRef.event()` /
  `roomSession.eventWarn()` 这类入口提供上下文（ms、room、session），调用点写
  原生链式。

顺带修掉的两条老账（§1 和 §5）：`diag` 不再反向 import `live`（错误细节由业务
错误实现 `DiagClass`/`DiagError` 交出），`debug.SetTraceback("all")` 补进 `main`，
`crash-*.log` 现在是全量 goroutine 栈。
