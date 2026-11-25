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
run   = 20261125-013933-4821-1        一次进程启动（也是日志文件名）
  room    = huya|485503               一间房，跨 run 稳定
    session = watch#17                 这一格的第 17 次起播（墙）
    session = 取流#8                   播放页的第 8 轮起播
      engine = 42                      这一路的播放引擎实例（进程内单调）
```

规则：

- `run` 由 `applog` 的 handler 注入每一行，调用点不许自己写。
- `room` 只来自 `platform|roomID`，不写房间名（名字会变、会重名）。
- `session` 只在**重开**时变；换清晰度/换线路保留同一个 session，弹幕另有
  `danmaku#N`。
- `engine` 只在 `playback.Open` 成功时分配，`engine.open` / `close` / `destroy`
  三条边沿共用它。

## 2. 事件 schema

### 2.1 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `event` | 枚举 | 稳定英文键，`子系统.边沿`；改文案不改它 |
| `sub` | 枚举 | `player` / `wall` / `danmaku` / `focus` / `mpv` / `state` / `app` |
| `run` | string | 进程运行 ID |
| `room` | string | `platform|roomID` |
| `session` | string | `取流#N` 或 `watch#N` |
| `engine` | uint | 引擎实例 ID |
| `stage` | 枚举 | 失败发生在哪一步：`fetch` / `variant` / `native` / `library` / `new` / `volume` / `load` / `play` / `provider` / `first-frame` |
| `cause` | string | 这次边沿的原因；可以是用户话术，但必须能区分 `expire` / `stalled` / `ended` / `error` / `user-reload` |
| `attempt` / `restarts` | int | 单调重开次数；只在用户手动 reload 时清零 |
| `failures` | int | 当前退避计数；**会被首帧清零**，不能拿来数循环 |
| `retry_ms` / `retry_at` | int / time | 下一次重试的间隔与时刻 |
| `since_frame_ms` | int | 距离上一次首帧多久；没有出过帧时不写 |
| `open_ms` | int | 从开始取流/引擎打开到这一条边沿的耗时 |
| `age_ms` | int | 引擎/会话存活时长 |
| `ms` | int | 相对 session 起点的耗时（播放页事件） |
| `err.class` | 枚举 | `timeout` / `canceled` / `blocked` / `unsupported` / `upstream` / `network` / `error` |
| `err.op` / `err.status` / `err.host` / `err.text` / `err.platform` / `err.type` | | 可安全记录的细节；`err.text` 已脱敏、截断 240 字 |
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
| `reason` | string | `run.stop` 的退出原因（`quit` / `error`） |

单位后缀是契约：`_ms` 毫秒，`_bytes` 字节，`_s` 秒。没有后缀的字符串字段不加单位。

### 2.2 事件目录（必须成对出现）

| 子系统 | 进入 | 离开/恢复 |
|---|---|---|
| 进程 | `run.start` | `run.stop` |
| 播放页 | `session.begin` / `session.path` / `session.open` | `session.fail` / `session.cancel` / `session.open_fail` |
| 取流 | `session.fetch` | `session.fetch_fail` |
| 引擎 | `engine.open` | `engine.open_fail` / `engine.close` / `engine.destroy` / `engine.destroy_timeout` / `engine.gpu_fallback` |
| 首帧 | `session.first_frame` | `session.recover`（失败后第一次出帧） |
| 换地址 | `session.swap_start` | `session.swap_done` / `session.swap_fail` |
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
| 一直重连 | `answer reconnect-loop` | `restarts` 增长且 `since_frame_ms` 很小 → 崩溃循环；`cause=expire` → 地址寿命；`cause=user-reload` → 用户操作 | `wall.restart` / `wall.first_frame` / `session.swap_*` |
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

`timeline` 的输出以 `== <session> room=<key> ==` 分组；每行是
`时间 级别 event 中文消息 字段...`。机器解析只依赖 `event` 和字段名，中文消息
可以改。

## 5. 落盘、崩溃与隐私

- **每 run 一个文件**：`%LOCALAPPDATA%\alive\logs\alive-<run>.log`；保留最近
  `ALIVE_LOG_KEEP` 份（默认 5）。多开、重启、旧构建不会再混成一份。
- **异步写**：`slog.Handler` 只做格式化和入队（4096 行），单写 goroutine 落盘；
  队列满丢最旧并写 `log.dropped`。UI/渲染线程不做同步文件 I/O。
- **黑匣子**：最近 512 行常驻内存，每 2 秒写 `recent-<run>.log`；崩溃时
  `debug.SetCrashOutput` 写 `crash-<run>.log`（`GOTRACEBACK=all` 的全量栈）。
- **脱敏**：`diag.Sanitize` 是唯一出口——URL 去 query/fragment、`token/sign/key/
  session/cookie/...` 打码、Windows 用户名目录换 `***`、换行压平、截断 240 字。
  handler 对**所有字符串属性和 error 值**执行，不只在错误路径。
- **级别/组件**：`ALIVE_LOG_LEVEL=debug|info|warn|error`；`ALIVE_LOG_COMPONENTS=
  wall,player,danmaku` 只放行这些子系统的 Debug。mpv 级别用
  `ALIVE_MPV_LOG_LEVEL=info|v`。这些都是记录详细度开关，不切换业务路径。
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
4. **性能与热路径**：`BenchmarkEmit` 实测 494 ns/op、144 B/op、2 allocs/op；
   `BenchmarkErr` 5.45 µs/op（错误路径，含脱敏正则）。日志入队不阻塞调用方，
   队列满丢最旧并计数。`internal/ui/render_log_test.go` 用 AST 钉住：渲染文件
   里不许出现 `slog`/`applog`/`.event`/`.Log`。
5. **反向回归**：把 `watchFail` 的 `s.event` 删掉，`TestWallLifecycleEvents`
   必须红；把 `diag.Sanitize` 的 URL 分支删掉，隐私测试必须红。只绿不算数。

### 7.1 一次真实运行的证据（2026-11-25）

隔离 `APPDATA`/`LOCALAPPDATA` 启动 e2e 构建，观测墙预置三间房，打开「悲鸣观测」：

```text
event=wall.show rooms=3 opened=3 closed=0
event=wall.open room=bilibili|1880715130 session=watch#1 attempt=0
event=wall.offline room=bilibili|1880715130 session=watch#1 cause=offline retry_ms=85000
event=wall.open room=douyu|138243 session=watch#1 attempt=0 quality=蓝光4M
event=engine.open engine=1 room=douyu|138243 session=watch#1 role=wall gpu=true
                host=https://hwa.douyucdn2.cn open_ms=81
event=wall.ready room=douyu|138243 session=watch#1 open_ms=601 quality=蓝光4M gpu=true
event=present.change room=douyu|138243 session=watch#1 from=full to=tile w=1280 h=720
event=wall.first_frame room=douyu|138243 session=watch#1 open_ms=1402 failures_before=0
event=engine.close engine=1 room=douyu|138243 session=watch#1 role=wall age_ms=21998
event=run.stop reason=quit uptime_ms=30140
```

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
- 引擎：`engine.open/open_fail/close/destroy/destroy_timeout/gpu_fallback`，
  带 id/key/role；插件零拷贝成功行降为 Debug，回退有原因。
- 焦点：`focus.change`（from/to）；呈现尺寸：`present.change`（档位）。
- mpv：焦点路完整转发（module/level/text/engine），非焦点墙格只转 error/fatal
  且每引擎限 50 条；游标按播放器复位。
- 读侧：`alive-diag timeline/answer/symptoms/bundle` + fixture 测试。

第二批（2026-11-25，已落地）：

- **卡死看门狗**：`internal/hang` 独立 goroutine 向 UI 线程投探针，10 秒无响应
  写 `hang-<run>-<stamp>.log`（全量 goroutine 栈 + 最近 512 条事件 + 指标 +
  引擎数），并调 `support.Minidump` 写同名 `.dmp`。最小化时暂停，5 分钟限一次。
- **support bundle**：「数据同步 → 诊断 → 导出诊断包」把最近 3 次运行日志、
  recent、crash、hang、trace、pprof 和 `meta.json` + `metrics.json` 打成一个
  zip，落在日志目录；不读 state.json/登录态。
- **指标**：`internal/metrics` 在 slog handler 前观察事件，输出首帧 p50/p95、
  每房重连数、失败按 stage/class、引擎存活数、mpv 错误按 module、日志丢弃数。
- **trace/pprof**：`ALIVE_DIAG_TRACE=1` 写 `trace-<run>.out`；诊断页签的
  「30 秒性能剖析」写 `cpu-<stamp>.pprof`，`go tool pprof` 直接打开。
- **Windows minidump**：`dbghelp!MiniDumpWriteDump`（stdlib syscall，不引 cgo），
  单测验证当前进程能写出非空 dump。
- **schema 测试**：`internal/diag/schema_test.go` 解析本文事件表，断言文档与
  代码的 `Ev*` 常量双向一致；字段侧也钉住：`alive-diag` 的查询字段必须来自
  `diag` 常量并出现在本文字段表里，`e.str("字面量")` 直接报错。
- **UI 意图**：`route.change`（from/to）、`session.quality_pick` /
  `session.line_pick`（requested）；`session.fetch` / `wall.open` / `wall.ready`
  带 `line` 和 `url_host`；`session.first_frame` 带 `display_fps` / `vfps`。
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
- **不把日志当指标**：重连率、首帧分位数走指标（下一批），日志只回答「为什么」。
- **不引第三方日志库**：这里要的是「文件不会无限长、写入不阻塞、能脱敏」三条，
  `slog` + 一个 handler 足够；引 zap/logrus 会把级别、字段、轮转拆成三套配置。
