# 0012 四个表面的统一模型

- **状态**：已决定，落地中（2026-11-22）
- **日期**：2026-11-22
- **影响**：`internal/ui/watch*.go`、`internal/ui/player_media.go`、`internal/ui/island.go`、
  `internal/ui/player.go`、`internal/ui/pages.go`、`internal/ui/surface.go`（新增）
- **取代**：[0011](0011-watch-wall.md) 里关于"所有权交接"的那部分描述，其余仍然有效
- **模型与路径表**：[architecture/surfaces.md](../architecture/surfaces.md)

## 问题

单直播间、后台收听、悲鸣观测、灵动岛四者之间的交互与过渡不成体系。这不是
"过渡不好看"，是**四个表面各自持有一份真相**，靠十个函数手工搬运字段：

`watchAdoptActive` / `watchReleaseActive` / `watchDropActive` / `watchStopActive` /
`watchDeactivate` / `watchHide` / `watchShow` / `detachMedia` / `attachMedia` / `closeMedia`

同一件事被存了多份：音量既是 `a.Volume` 又是 `watch.volumes[k]`；播放态既是
`a.Playing` 又是 `s.player.Playing()`；"这一路在放"既是 `a.media.background`、
又是 `a.WatchActive`、又是 `a.watch.sessions[k].player`。

每加一条路径就要在六处同时改对，漏一处就是一次事故。`presentSizeFor` 的收敛
（`bf2a98a`）证明这条路走得通，但当时只收敛了尺寸一个维度，所有权维度还散着。

## 决策

### 1. 一个会话集合 + 三个正交轴

`S`（会话集合）× `F`（声音焦点，标量或空）× `R`（路由与分区）。

三个派生量各只有一个求值点：呈现尺寸（已有）、声音归属、岛形态。

`background` 字段删除——它就是 `presentation(F) == Hidden`。`WatchActive` 删除，
它就是 `F`。"墙上 active"不是第二种播放，是焦点会话被画在格子里。

### 2. 关闭的语义统一为"结束焦点"，会话存亡由表面推导

一条规则推出两种情况，不写特例分支：

> 焦点结束时，若还有表面在显示这一间（在观测盒里），会话存活、变静音预览；
> 否则关闭。

这条同时修掉三个历史缺陷（见 3）。此前"从墙放大进去再按 X"会关掉引擎，
"墙上换一间看"会关掉上一间，都是因为把"离开一个表面"当成了"结束这一路"。

### 3. 资源策略与可见性分离

`watchHide` 不再"看不见就关"，改为"标记不可见 + 保留窗口 + 回收器"。
保留窗口 30 秒：够"去首页瞄一眼就回来"，不够到该省资源的时候还占着。

代价：离开墙后 30 秒内非焦点会话仍在解码。按 [measurements](../reference/measurements.md)
「观测墙并发」，9 路 640×360 呈现约 1.8 核；12 路更高。这是明确接受的。

### 4. 过渡是契约，且分三处判断

- 顶栏 tab / 分区：不动。方向模型与平级路由不匹配（`d488013`），[0010](0010-switch-content-still.md)
  的理由继续成立。
- 内容层（卡片网格）：永不恢复位移或淡入。[0010](0010-switch-content-still.md) 继续守。
- 表面之间（墙格 ↔ 播放页 ↔ 岛）：做连续过渡。这不是"在内容上动"，是"同一间房
  换位置"，两者不该互相牵连。

格子 → 播放页用 MyGo 的共享元素过渡（`ElementTransition.Match` + `Fly`），
落地前必须先过四个探针（见"未决"）。

## 被否掉的替代

- **保留双存储（`a.media` + `a.watch.sessions`），继续补搬运函数。** 每加一条路径
  就要在六处同时改对，历史缺陷 1–4 都是这么来的。
- **每个表面各开一个播放器。** 墙格 → 播放页要重取流，音频接缝 1.09–1.56 秒
  （[0009](0009-live-continuity.md)），和"同一间房放大"不是一件事。
- **离开墙就关（现状）。** 去首页看一眼再回来整面墙重新 buffering，按实测
  224ms–4.3s 不等。
- **墙上走低档子码流。** ry 已明确否掉（[0011](0011-watch-wall.md)）。
- **恢复内容层的进出场动画。** 0010 的三道门槛至今没被满足，也不该被满足。

## 未决

格子 → 播放页的连续过渡依赖四个探针，未通过则保持硬切（硬切不丢人，
加了动画反而更糊才丢人）：

| # | 要证明 | 失败退到 |
|---|---|---|
| P1 | 同一播放器被两个 `Video` 显示 300ms：不重复解码、不显示旧帧 | 飞行期间播放页不建 `Video` |
| P2 | `Match + Fly` 的到达元素里装 `Video`：飞行途中继续推进 | 飞行元素装静止帧，落地换真 `Video` |
| P3 | `ReduceMotion` 下完全不动且不产生额外双建帧 | 该路径直接跳过 |
| P4 | 12 格墙上起飞一次 300ms 的帧预算 | 缩到 200ms，或 >4 格时不启用 |

模型部分（阶段 2、4）与探针解耦，不受其结论影响。

## 后果

- `internal/ui` 净减少：删 `background`、`WatchActive`、`WatchPending`、`watch.volumes`、
  五个搬运函数；`a.Player` / `a.Playing` 变成焦点会话的视图。
- 新增 `surface.go`：纯函数求值，可在 Tester 里穷举。
- 新增 `session_invariants_test.go`：假引擎 + 动作字母表 + 状态机走查。
- 静态测试限制 `SetVolume` / `SetPresentSize` 的调用点，防止第二套机制回潮。

## 复查条件

- 保留窗口 30 秒在真机上造成可感知的后台 CPU 浪费 → 调整窗口，不改模型。
- 平台对"离开墙后仍保持 N 路连接"有限流反应 → 缩短窗口，或按平台错峰。
- 探针 P1/P2 显示飞行期视频会冻帧 → 放弃连续放大，保持硬切。
- 用户要求"离开墙后立刻释放" → 那是把可见性与策略重新焊回去，需要新理由。
