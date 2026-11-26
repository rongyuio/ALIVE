# 现状简报（STATE）

> 这一页由 `cmd/alive-symptoms` 从 git 历史生成，**不要手改**。
> 要改判据改 `doc/symptoms.json`，然后重跑 `go run ./cmd/alive-symptoms`。
> 每次会话开头先读它：先知道现在哪儿在烧，再决定要不要动那个文件。
> 要问「这个文件第几次了」：`go run ./cmd/alive-symptoms -file <路径>`。
> `make gate` 跑的是同一条规则里能自动检查的那几条（churn / commits / claims / evaluator / facts / probes）；这一页补的是它数不到的那一半——按症状，而不是按文件。

| 项 | 值 |
|---|---|
| 锚点 | `70ba91f` 2026-11-26 08:58（HEAD，committer 时间） |
| 窗口 | 7 天：2026-11-19 08:58 → 2026-11-26 08:58 |
| 闸门 | 同一症状或同一文件，7 天内第 3 次 `fix:` → 停手，先写清前几次为什么没修对 |
| 冷却期 | 7 天：最后一次之后这么久没再犯，才算这条线停住 |
| 症状表 | `doc/symptoms.json`（3 条，人工维护） |
| 全部历史 | 427 次提交，170 个 `fix:` |

## 一、现在：最近 7 天最热的文件

| # | 文件 | 改动 | 其中 `fix:` | 累计改动 | 最近一次 |
|---|--:|--:|--:|--:|---|
| 1 | `internal/ui/watch_media.go` | 54 | 28 | 54 | 11-25 12:53 |
| 2 | `internal/ui/home.go` | 46 | 17 | 92 | 11-26 08:57 |
| 3 | `doc/architecture/watch.md` | 39 | 19 | 39 | 11-26 08:58 |
| 4 | `internal/ui/watch.go` | 38 | 22 | 38 | 11-25 09:48 |
| 5 | `internal/ui/player_media.go` | 38 | 21 | 59 | 11-25 17:36 |
| 6 | `internal/ui/player.go` | 30 | 17 | 60 | 11-25 16:38 |
| 7 | `internal/ui/watch_test.go` | 30 | 16 | 30 | 11-25 09:48 |
| 8 | `doc/reference/measurements.md` | 30 | 12 | 48 | 11-26 08:58 |
| 9 | `doc/decisions/0011-watch-wall.md` | 23 | 10 | 23 | 11-26 08:58 |
| 10 | `doc/architecture/surfaces.md` | 23 | 9 | 23 | 11-26 08:58 |

热不等于该修。这张表回答的是「最近有人在哪里反复按手印」，不回答「该不该再按一次」——那个在第二节。

## 二、正在燃烧：7 天内第 3 次及以上

「现在」那一列是这一节的关键：**还在动**的才是今天正被第 N 次修、`make gate` 会拦；**欠账**的是闸门早过了但已经停了几天——规则不拦，账还在。

### 2.1 按症状（`AGENTS.md` §3.7 管不到的那一半）

| 症状 | 窗内 | 累计 | 最后一次 | 现在 | 结论 |
|---|--:|--:|---|---|---|
| 切换闪 / 抖动 | 10 | 14 | 11-25 09:31 | **还在动** | **窗内第 10 次，该停手了**：先写清前 9 次为什么没修对，再动代码 |
| 回到墙不无缝 / 冻结 | 3 | 3 | 11-23 15:56 | 欠账（已冷却 2 天） | 窗内第 3 次，该停手了（已冷却，账还在）：先写清前 2 次为什么没修对 |

未越闸门的：灵动手抖（窗内 0 次、累计 3 次、最后一次 11-18 14:21）。

### 2.2 按文件（§3.7 的原算法，看的是文件不是问题）

| 文件 | 窗内 `fix:` | churn 口径 | 最后 fix | 现在 | 结论 |
|---|--:|--:|---|---|---|
| `internal/ui/player_media.go` | 21 | 18 | 11-25 17:36 | **还在动** | **窗内第 21 次，该停手了**：`.gate-allow` 记着「18 次 fix / 7 天」，和现在一致——绿的是豁免，不是许可 |
| `internal/ui/player.go` | 17 | 13 | 11-25 16:38 | **还在动** | **窗内第 17 次，该停手了**：`.gate-allow` 记着「13 次 fix / 7 天」，和现在一致——绿的是豁免，不是许可 |
| `internal/ui/surface_motion.go` | 8 | 6 | 11-25 09:31 | **还在动** | **窗内第 8 次，该停手了**：`.gate-allow` 记着「6 次 fix / 7 天」，和现在一致——绿的是豁免，不是许可 |
| `internal/ui/surface.go` | 5 | 4 | 11-25 09:31 | **还在动** | **窗内第 5 次，该停手了**：`.gate-allow` 记着「4 次 fix / 7 天」，和现在一致——绿的是豁免，不是许可 |
| `internal/ui/watch_media.go` | 28 | 27 | 11-25 00:37 | 欠账（已冷却 1 天） | 窗内第 28 次，欠账：churn 最后一次在 2 天前，已冷却，不拦 |
| `internal/ui/watch.go` | 22 | 20 | 11-25 00:37 | 欠账（已冷却 1 天） | 窗内第 22 次，欠账：churn 最后一次在 2 天前，已冷却，不拦 |
| `internal/ui/home.go` | 17 | 13 | 11-25 00:37 | 欠账（已冷却 1 天） | 窗内第 17 次，欠账：churn 最后一次在 1 天前，已冷却，不拦 |
| `internal/ui/watch_test.go` | 16 | 16 | 11-24 00:38 | 欠账（已冷却 2 天） | 窗内第 16 次，欠账：churn 口径最后一次在 2 天前，已冷却，不拦 |
| `internal/ui/island.go` | 14 | 11 | 11-24 02:09 | 欠账（已冷却 2 天） | 窗内第 14 次，欠账：churn 口径最后一次在 2 天前，已冷却，不拦 |
| `internal/playback/engine.go` | 8 | 6 | 11-24 14:07 | 欠账（已冷却 1 天） | 窗内第 8 次，欠账：churn 口径最后一次在 1 天前，已冷却，不拦 |
| `internal/ui/island_test.go` | 8 | 7 | 11-24 00:38 | 欠账（已冷却 2 天） | 窗内第 8 次，欠账：churn 口径最后一次在 2 天前，已冷却，不拦 |
| `internal/ui/player_media_test.go` | 8 | 7 | 11-24 00:27 | 欠账（已冷却 2 天） | 窗内第 8 次，欠账：churn 口径最后一次在 2 天前，已冷却，不拦 |

按「gate 现在会拦 → 窗内 fix 多」排序还有 22 个文件也越闸门，这里不铺开：要查具体一个用 `-file <路径>`。

两列数字为什么不一样：「窗内 `fix:`」是 §3.7 原文口径（不剔测试、不剔横切提交）；「churn 口径」是 `make gate` 真正数的（跳过动 >8 个文件的横切提交，跳过 `doc/` 与 `.md`，只拦 24 小时内还在动的）。标「欠账」的门禁不会红——不是它没问题，是规则只拦今天。

**`.gate-allow` 已经豁免的 4 个文件**（豁免的是存量，不是继续打补丁的许可）：

- `internal/ui/player.go` — 记着「13 次 fix / 7 天」：战场文件，写者收口未完成
- `internal/ui/player_media.go` — 记着「18 次 fix / 7 天」：同上
- `internal/ui/surface.go` — 记着「4 次 fix / 7 天」：同上
- `internal/ui/surface_motion.go` — 记着「6 次 fix / 7 天」：同上（「飞回格子」修了 6 次）

### 2.3 两条规则的分歧

- **切换闪 / 抖动**：10 次修散在 25 个文件上（最密的是 `internal/ui/home.go` 8 次），其中 14 个文件自己也够 3 次 —— §3.7 点到的是那些文件，一次都没点到这条症状本身。
- **回到墙不无缝 / 冻结**：3 次修散在 6 个文件上（最密的是 `internal/playback/engine.go` 2 次），其中 4 个文件自己也够 3 次 —— §3.7 点到的是那些文件，一次都没点到这条症状本身。
- 有 8 个源码文件在烧但没有任何症状认领：`internal/live/bilibili_stream.go`、`internal/live/auth.go`、`internal/live/douyu_stream.go`、`internal/theme/palette.go`、`internal/ui/config_transfer.go`、`internal/live/bilibili_danmaku.go`（另有 2 个略） —— 症状表可能缺一条，也可能只是重构痕迹，看过再决定。

## 三、上次说了什么：最近 10 次提交

1. `70ba91f` 11-26 08:58 —— feat: 真窗口逐帧录制与比对，补上 1-2 帧那段盲区
   - 改：`cmd/alive-framediff/main.go`、`cmd/alive-framediff/metrics.go`、`cmd/alive-framediff/metrics_test.go`、`cmd/alive-framediff/seq.go`、`cmd/alive-framediff/thresholds.go`、`doc/reference/frame-recording.md`、…(+6)
2. `be3854a` 11-26 08:58 —— feat: 症状账本，按症状而不是按文件数返工次数
   - 改：`cmd/alive-symptoms/allow.go`、`cmd/alive-symptoms/history.go`、`cmd/alive-symptoms/main.go`、`cmd/alive-symptoms/render.go`、`cmd/alive-symptoms/symptoms.go`、`cmd/alive-symptoms/symptoms_test.go`、…(+2)
3. `81a1291` 11-26 08:58 —— feat: 把工程契约里能自动检查的那几条做成门禁
   - 改：`.gate-allow`、`.githooks/commit-msg`、`.githooks/pre-commit`、`.github/workflows/check.yml`、`.gitignore`、`AGENTS.md`、…(+5)
4. `fbd992c` 11-26 08:58 —— docs: 按 git 历史重做文档，补一份演化史
   - 改：`doc/README.md`、`doc/architecture/diagnostics.md`、`doc/architecture/platform.md`、`doc/architecture/playback.md`、`doc/architecture/surfaces.md`、`doc/architecture/watch.md`、…(+22)
5. `af1626f` 11-26 08:57 —— feat: 接入自更新，诊断页给出版本行和检查入口
   - 改：`doc/decisions/0015-self-update.md`、`internal/ui/diagnostics_panel.go`、`internal/ui/home.go`、`internal/ui/update_entry_test.go`、`main.go`、`mygo.json`
6. `183d054` 11-26 08:57 —— refactor: 画面归属收成一个求值点
   - 改：`internal/ui/surface.go`
7. `a50440d` 11-25 19:49 —— chore: README 暂时不入库，等 owner 验收
   - 改：`.gitignore`
8. `5dfdd41` 11-25 18:12 —— docs: 包内文档与注释同步「未登录态」口径
   - 改：`doc/reference/measurements.md`、`internal/live/README.md`、`internal/live/douyin.go`、`internal/live/huya.go`、`internal/live/login_coverage_test.go`
9. `50a1198` 11-25 18:09 —— docs: 统一对外口径，补 README 与使用边界
   - 改：`AGENTS.md`、`doc/architecture/platform.md`、`doc/decisions/0007-douyin-search.md`、`doc/decisions/0009-live-continuity.md`、`internal/live/douyin_abogus.go`、`internal/live/huya_danmaku.go`
10. `775fa3e` 11-25 17:36 —— fix: 从墙上带进播放页的一路补齐媒体轮次
   - 改：`doc/architecture/diagnostics.md`、`doc/architecture/playback.md`、`internal/diag/diag.go`、`internal/ui/player_media.go`、`internal/ui/player_swap_cancel_test.go`

提交标题是这一页唯一的叙述来源：只有标题说了「修的是什么症状」，症状表才数得到它。
标题写成「调整一下」的修复，在这里等于没发生。

## 四、你说过但没做到的

判据（写死在 `cmd/alive-symptoms` 里，不是手填的）：症状表里**累计被报 ≥ 3 次**，且**最后一次离锚点不足 7 天**——冷却期没走完，就算还没收敛。

### 切换闪 / 抖动 —— 累计 14 次

- 报的是什么：换平台、换分区、换屏、进出播放页时，内容层先消失再出现，或者整块位移一下、卡片重播入场动画。用户的原话通常是「切的时候闪一下」「抖一下」。
- 窗内 10 次，最后一次 2026-11-25 09:31（23 小时前）
- 结论：**累计第 14 次 —— 该停手了**：先写清前 13 次为什么没修对，再动代码
- 已试过的修法（按标题抄，最近 5 次）：
  - `7091893` 11-25 09:31 表面过渡不再把封面盖在活画面上
  - `5b129a9` 11-25 00:37 修一轮表面过渡、起播音量与列表续取
  - `a0d98bc` 11-24 14:31 没开播的房间收起回墙也飞回格子
  - `0112d92` 11-24 14:21 点岛打开播放页时记下来源，墙上退回来仍飞回格子
  - `6bcc7ad` 11-24 00:28 Esc 与点岛退回来处；观测墙退出恢复飞行
  - …更早还有 9 次
- 判据的代价：「抖」会命中「抖音」这个平台名，所以抖音相关提交用 titleNone 一票否决。判据是宽口径：标题写了现象（闪/抖/位移/硬切/横滑），或者改了过渡那四个文件，任一条都算一次举报。代价是 11-21 的 ab53d16（切换时内容层完全不动）、11-16 的 0c5bec5（不再重播入场动画）没被抓住——标题里没有现象词。要收紧或放宽带宽，就改 titleAny / filesAny。

### 回到墙不无缝 / 冻结 —— 累计 3 次

- 报的是什么：离开观测墙（或播放页）再回来，画面冻在离开那一帧、要等关键帧，或者整路重取流从头开始——「回来不无缝」。
- 窗内 3 次，最后一次 2026-11-23 15:56（2 天前）
- 结论：**累计第 3 次 —— 该停手了**：先写清前 2 次为什么没修对，再动代码
- 已试过的修法（按标题抄，最近 5 次）：
  - `93a6935` 11-23 15:56 回到墙不再冻帧：门控唤醒写反了，解码不该挂起
  - `f1e151a` 11-21 04:51 后台保留解码，回来 4ms 接上全尺寸画面
  - `d166158` 11-21 03:39 回到播放页接上现有这一路，不再整路重取流
- 判据的代价：标题里出现「回到墙 / 冻帧 / 接上 / 无缝」就算。这一条只按标题数，没有挂文件：这个病的爆炸半径横跨 internal/playback 和 internal/ui/player_media.go，挂上去会把 §3.7 已经在数的改动重复算一遍。刻意不收两个词：「回来」会把 0112d92「墙上退回来仍飞回格子」那类切换闪的提交抓进来；「重取流」会抓到 0760e42「退出登录也重取流」——那是登录档位的事。两条症状最怕的就是互相污染。代价是 daec85f「离线格自己回来」落在外面。11-23 那次的回归是 c4beeb0 那个 perf: 提交引进来的，perf 不算一次举报，所以数字比实际发生的少一次。

已停住（累计 ≥3 次，之后 7 天没再犯）：

- **灵动手抖**：累计 3 次，最后一次 2026-11-18 14:21，空窗 7 天。

这一节不列「应该怎么修」。它只负责让「第几次了」不能靠感觉回答。
