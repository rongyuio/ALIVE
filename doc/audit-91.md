# 审计发现台账（90 条）

本文件是执行「按照计划全量落地」（会话 seq 4676）时的**工作台账**：每条审计发现一行，
判决只有四种——已修 / 已记账 / 不成立 / 待判。它不是门禁工具，只是把读过代码的结论沉淀
下来。

来源：`.tmp/report-g1.md`、`.tmp/report-g2.md`、`.tmp/report-g3.md`（三个只读 subagent
逐行读 `internal/ui` 的报告，生产代码当时一个字节没改）。

## 对账（第 0 批 H0）

- 三份报告各自声明：g1 22 / g2 25 / g3 44 = **91**
- 逐条枚举实际：g1 20 / g2 26 / g3 44 = **90**
- 差 1 条，来自两份报告自带「统计」表的算术错误：
  - `report-g1.md` 疑点列写 9，正文实际只有 7 条（`player_media.go` 少 1 条、`home.go` 少 1 条）
    → 该组应为 20，不是 22
  - `report-g2.md` 疑点列写 8，正文实际列了 9 条（`player.go:63 vs :786` 也算一条）
    → 该组应为 26，不是 25
  - 净差 91 − 90 = 1
- 复跑：`Select-String -Path .tmp/report-g*.md -Pattern '^\**\[(BUG|隐患|疑点)' | Measure-Object`
- **以枚举为准：14 BUG / 48 隐患 / 28 疑点 = 90 条。**

## 现状分布

| 判决 | 条数 |
|---|---|
| ✔ 已修（有测试或可跑判据） | 57 |
| ◇ 已记账（在 `doc/known-debt.md`） | 30 |
| ✘ 判定不成立（有反证） | 3 |
| 待判 | 0 |
| **合计** | **90** |

## 判决表

按文件、按报告出现顺序。`待判` 的条目由第 1–5 批逐条给出判决。

### `category_bar.go`（8）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/category_bar.go:301` | CategorySearchOpen 只在「焦点刚进来的那一趟」被置真（301-303，靠 | ✔ 已修 |
| BUG | `internal/ui/category_bar.go:479-483` | active 时先 row.Background(FillQuaternary)（479）；紧接着 483 行无条件 | ✔ 已修 |
| 隐患 | `internal/ui/category_bar.go:499,507` | avail := winW - 2*14 - subscribeWidth - 12，winW 来自 c.Size()（整窗）。 | ◇ 已记账（妥协 #12；公式已改但无鉴别判据） |
| 隐患 | `internal/ui/category_bar.go:168-175` | 取消订阅时 kept := a.Subs[:0] 原地压缩 a.Subs。 | ✔ 已修 |
| 隐患 | `internal/ui/category_bar.go:573-585` | btn 的底色在 573-575 按旧的 a.MoreOpen 算，btn.Clicked() 在 576-578 把 | ✔ 已修（moreButton 改 OnClick） |
| 疑点 | `internal/ui/category_bar.go:82-84（未证实，但注释是假的）` | currentSubs() 读 a.subCategories；全仓只有 home.go:33 的声明和 | ✔ 已修 |
| 疑点 | `internal/ui/category_bar.go:117-132` | selectCategory(i) 不校验 i 范围（selectGroup 95-98 校验），越界会把 | ◇ 已记账 |
| 疑点 | `internal/ui/category_bar.go:435-437` | 查询变短后只把越界的 CategoryHit 归 0，不 clamp 到末项。 | ✔ 已修（越界收到末项） |

### `config_transfer.go`（4）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/config_transfer.go:274-280 vs internal/ui/follow.go:54-62` | 导入去重用 followIdentity = "platform\|roomID"，roomID 为空时退回昵称（540-546）；应用自己 | ✔ 已修 |
| BUG | `internal/ui/config_transfer.go:392-394` | after.Dur = time.Duration(f.Danmu.DurMs)*time.Millisecond，只校验 DurMs >  | ✔ 已修 |
| 隐患 | `internal/ui/config_transfer.go:389-402` | Font / Area / Opacity 只判 > 0，没有任何上界，直接写进 a.DanmuFont/DanmuArea/DanmuOp | ✔ 已修 |
| 疑点 | `internal/ui/config_transfer.go:770-782` | pickImportFile 把 a.planImport(...) 放在 win.Update 回调里跑，回调末尾复位 a.SyncBus | ◇ 已记账（妥协 #11） |

### `danmu.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/danmu.go:107-112` | danmuColor 只挡空串和 "#ffffff"，其余直接 theme.HexColor(d.Color) | ✔ 已修 |
| 隐患 | `internal/ui/danmu.go:147-161` | ensureDanmuLoop 用 `active=true` 然后 CAS(loop false→true)；循环体每 33ms 检查 | ◇ 已记账（妥协 #19） |
| 隐患 | `internal/ui/danmu.go:390` | p := float32(now.Sub(d.Born)) / float32(d.Dur)。 | ✔ 已修（danmuProgress 守 Dur<=0） |
| 隐患 | `internal/ui/danmu.go:335-341` | tickDanmu 用 live := a.Danmu[:0] 原地过滤。 | ✔ 已修 |
| 疑点 | `internal/ui/danmu.go:416` | y := (d.Track%tracks)*danmuChannel + 16。 | ✔ 已修 |

### `danmu_panel.go`（4）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/danmu_panel.go:181` | a.PanelList.Key = func(i int) any { return list[i].Seq } —— 闭包捕获的是 | ◇ 已记账（妥协 #20） |
| 隐患 | `internal/ui/danmu_panel.go:168-173` | panelNewDanmu()（228-258）在构建路径里推进 a.panelSeen/a.panelMark， | ◇ 已记账（妥协 #21） |
| 隐患 | `internal/ui/danmu_panel.go:189-204 + 248-252` | 日志满 danmuLogMax 后，上一次的结算点被挤出窗口时 n = len(log)。 | ◇ 已记账 |
| 疑点 | `internal/ui/danmu_panel.go:98-108` | 拖拽状态存在 a.panelDragW/panelDragX 两个 App 字段上，唯一的复位路径是 | ◇ 已记账（妥协 #22） |

### `data.go`（8）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/data.go:299-308 + 355-366（+294 补页不推进 liveSeq）` | 命中过期缓存时置 a.Rooms=cached.rooms、ListLoading=false，随后仍发首屏请求； | ✔ 已修 |
| BUG | `internal/ui/data.go:784-793 + 838-868（+934-936）` | 订阅页同款。过期缓存路径把 SubLoading 置 false 后仍发首屏；loadMoreSubRooms 与它共用 | ✔ 已修 |
| 隐患 | `internal/ui/data.go:269-349` | loadRooms 是取数入口里唯一没有 `a.Win == nil` 早退的（对比 loadMoreRooms:394、 | ✔ 已修 |
| 隐患 | `internal/ui/data.go:758-766` | ensureSubFeed 在渲染路径上用 time.Now() 记/判 30s 冷却，而且不排帧。 | ✘ 不成立 |
| 隐患 | `internal/ui/data.go:649-668` | syncSearch 在查询词变化时清 SearchResults；但 `q != "" && a.Win == nil` 这个分支只把 | ✔ 已修 |
| 隐患 | `internal/ui/data.go:301 / 481-483 / 785 / 859-866` | a.Rooms、a.SubRooms、feedCache[key].rooms 会指向同一块底层数组： | ✘ 不成立 |
| 疑点 | `internal/ui/data.go:620` | Hue: float32(i*37 % 360)，i 是这一块 chunk 内的下标。 | ◇ 已记账（妥协 #26） |
| 疑点 | `internal/ui/data.go:517-524` | d := topUpBackoffMin << min(a.topUpFailures-1, 4)。 | ✔ 已修 |

### `diagnostics_panel.go`（4）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/diagnostics_panel.go:214-219` | counters := snap["counters"].(map[string]int64)、hists["first_frame_ms" | ✔ 已修 |
| 隐患 | `internal/ui/diagnostics_panel.go:415-417、458-460` | 两个后台动作都在 `if win == nil { return }` 处直接返回，而 SyncBusy / | ✔ 已修 |
| 隐患 | `internal/ui/diagnostics_panel.go:450 + internal/ui/sync_panel.go:377` | a.ProfStarted = time.Now()（挂钟）存进去，按钮文案用 | ✔ 已修（倒计时两端都用挂钟） |
| 疑点 | `internal/ui/diagnostics_panel.go:405` | 文件名 bundle-20060102-150405.zip，精确到秒。 | ◇ 已记账 |

### `follow.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/follow.go:371-377` | 每个 worker 处理完一条就 refreshOne → win.Update(写回) 一次，再 | ◇ 已记账 |
| 隐患 | `internal/ui/follow.go:386-388` | 收尾（followsBusy=false）整个挂在 runOnMain(win, ...) 里；runOnMain 在 | ◇ 已记账（妥协 #15） |
| 隐患 | `internal/ui/follow.go:484-487` | refreshOne 无条件 win.Update(writeFollowStatus(...))，但 writeFollowStatus | ✔ 已修（分类进 Update 回调） |
| 隐患 | `internal/ui/follow.go:70-81` | removeFollow 用 kept := a.Follows[:0] 原地压缩。 | ✔ 已修 |
| 疑点 | `internal/ui/follow.go:342-348 + 435-438` | followsRefreshable 把没有房间号的旧记录全部跳过；若 Follows 里全是旧记录， | ✔ 已修（attempted==0 换文案与色调） |

### `follows_page.go`（4）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/follows_page.go:609-616` | busy 时每帧 phase := btn.Loop(...)*360 再 btn.Jump("action-spin-stop", pha | ✔ 已修（收尾往前补满一圈） |
| 疑点 | `internal/ui/follows_page.go:518` | dotKey := "live-"+Platform+"\|"+Name+"\|"+Category —— 不含 RoomID，也不带重复项 | ✔ 已修（followDotKey 带房间号） |
| 疑点 | `internal/ui/follows_page.go:246-248` | tab 的 Clicked() 在构建里直接 a.selectFollowsSection(section)——改 a.FollowsSec | ✔ 已修（改用 OnClick 推迟） |
| 疑点 | `internal/ui/follows_page.go:211-212 + 302-312` | a.followsSectionBox / a.followsBox 把构建期量出来的 rowBox 存进 App 字段。 | ◇ 已记账（妥协 #10） |

### `home.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `home.go:1469-1481（+1439）` | `tickPlatformSwitch` 用 `a.pendingTries++` 计数，上限 `platformSwitchTries = | ✘ 不成立 |
| 隐患 | `home.go:707-709` | 只要不在播放页，每帧调 `a.leavePlayer()`；它无条件 `windowFullScreen(a.Win)`（= `Window | ◇ 已记账 |
| 疑点 | `home.go:1043` | `a.builtCards++` 每次都加，全仓库**没有任何地方复位它，也没有任何地方读它**。 | ✔ 已修 |
| 疑点 | `home.go:718-724` | 播放页里 `fullscreen != a.wasPlayerFullscreen` 的边沿会 `a.routeTransitionUnti | ◇ 已记账（妥协 #7） |
| 疑点 | `home.go:1001-1003` | `avail` 小于 `theme.CardMinWidth` 时被强行抬回 `CardMinWidth`，于是 `cardW` 可能大于真 | ✔ 已修（去掉 avail 抬升） |

### `island.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/island.go:185 + 356（w 参数从头到尾没被用过）` | Island 把弹簧当前值 w 传给 playerIsland(c, w, openW)，但函数体一次都没用 w：文字列宽 | ✔ 已修 |
| 隐患 | `internal/ui/island.go:433-434` | cancel.Background(theme.MediaRow) 下一行又被 cancel.Background(hoverSurface | ✔ 已修 |
| 隐患 | `internal/ui/island.go:316-327` | islandNavReserve 把顶栏两端几何（pad/gap/tab 宽/窗口按钮宽/工具胶囊）抄成字面量， | ◇ 已记账（妥协 #8） |
| 疑点 | `internal/ui/island.go:111-116 + 224-232` | Island 在构建里写状态——enterPlayerIsland 改 a.infoRoom/a.InfoOpen，非播放页时每帧 | ◇ 已记账（妥协 #9） |
| 疑点 | `internal/ui/island.go:515-533` | ringLeft := size-d-1、ringSize := d+4；size=32、d=9.6 时环从 21.4 铺到 35，而 av | ◇ 已记账 |

### `player.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/player.go:582-584` | btn 先 .Background(theme.White)，第 584 行又 btn.Background(hoverSurface(bt | ✔ 已修 |
| BUG | `internal/ui/player.go:666-670` | 全屏返回钮同款：Background(theme.MediaCapsuleBg) 被下一行 hoverSurface 覆盖成 Transpa | ✔ 已修 |
| 隐患 | `internal/ui/player.go:602-606（配合 player_media.go:261-273）` | bufferingPill 在构建路径里调 a.media.stalled(p)，后者读 time.Now() 并**写** | ◇ 已记账 |
| 隐患 | `internal/ui/player.go:619-624` | noticePill 用 time.Now() 取 now；同一文件里 syncChromeIdle 用 c.Now()、islandPil | ✔ 已修 |
| 疑点 | `internal/ui/player.go:63 vs :786` | 同一帧里"是不是全屏"问了两个来源：63 行 a.playerFullscreen()（吃 a.winFullscreen 字段）， | ◇ 已记账（妥协 #27） |

### `player_media.go`（7）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `player_media.go:948 / 975 / 1103` | `scheduleSwap` 在 948 行把 `m.refreshing = true` 立起来，只有三个地方复位它——`adoptSwa | ✔ 已修 |
| 隐患 | `player_media.go:1108（配合 309-319）` | `stopPlayback` 里 `m.tickPending = false`，但它并不取消 312 行已经排上的 `time.After | ✔ 已修（tickGen） |
| 隐患 | `player_media.go:308` | `need := (m.app.inBackground() && m.eng() != nil) \|\| len(a.watch.ses | ✔ 已修（needMediaTick） |
| 隐患 | `player_media.go:567-594` | `adoptLiveSession` 只做 `cancelSession` + 重建 ctx，不复位 `m.swapFailures`、`m | ✔ 已修 |
| 隐患 | `player_media.go:1774-1789` | `applyMedia` 在 `runOnMain` 返回 false（窗口已销毁/未建）时，在**后台 goroutine 上同步执行** | ◇ 已记账（妥协 #6） |
| 疑点 | `player_media.go:1428-1436` | `deadReason` 的缓冲分支用 `m.bufferStallSince` 计时，但 `backgroundEnter`/`foreg | ✔ 已修（foregroundEnter 清 bufferStallSince） |
| 疑点 | `player_media.go:1146` | `trackContinuity` 里用 `p.Engine().(*playback.Engine)` 硬断言取引擎，而同一件事在 463 | ◇ 已记账（engineOf 接缝只能覆盖 SetPresentSize） |

### `state.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/state.go:167-171（配合 105-140）` | saveState 拍完快照就 `go a.writeState(data)`；writeState 里 stateMu 只保证 | ✔ 已修（写盘按序号只往新写） |
| 隐患 | `internal/ui/state.go:178-182` | stateSnapshot 先把 stateSavePending/stateDirty 清零，再 marshal；marshal | ✔ 已修 |
| 隐患 | `internal/ui/state.go:161-163` | markDirty 的 time.AfterFunc 句柄不保存、也不取消；FlushState（236-240）之后 | ✔ 已修（定时器留句柄、快照时取消） |
| 疑点 | `internal/ui/state.go:207-223` | 快照结构里直接塞了 a.roomChoices（活 map）和 a.PlatformOrder（活切片）， | ◇ 已记账（妥协 #23） |
| 疑点 | `internal/ui/state.go:288-301` | restore 对 PlatformOrder/FollowsSectionOrder 做了 normalize，对 Subs | ◇ 已记账（妥协 #24） |

### `surface_motion.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/surface_motion.go:348-355` | transitionMatch 分支里 `if a.exitArm { return }`——直接返回，既不执行这一次的 | ✔ 已修 |
| 隐患 | `internal/ui/surface_motion.go:384-386（配合 355）` | tickExitArm 要求 a.buildSeq >= a.exitArmAt+2，即 arm 之后还要两趟构建； | ✔ 已修（等第二拍时自己请求帧） |
| 隐患 | `internal/ui/surface_motion.go:72-81` | setFocus 对新焦点无条件 s.playing = true，并且 s.volume <= 0 时把它抬到 | ◇ 已记账（妥协 #16） |
| 隐患 | `internal/ui/surface_motion.go:502-506` | backgroundEnter 判"死焦点"的条件里有 `(f.stream.RoomID != "" && !f.stream.Live) | ◇ 已记账（妥协 #17） |
| 疑点 | `internal/ui/surface_motion.go:183,195-196` | 已经在播放页时换台清了 surfaceFlight/exitArm/exitAction/exitMatchKey， | ◇ 已记账（妥协 #18） |

### `sync_panel.go`（3）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `internal/ui/sync_panel.go:347-360` | primary 分支先 btn.Background(theme.Accent)（348）；进入非 busy 分支后 | ✔ 已修 |
| 隐患 | `internal/ui/sync_panel.go:402-412（配合 377）` | profileStopLabel 用 int(秒) 截断再向上取整；start 来自 time.Now()， | ✔ 已修（向上取整 + 挂钟） |
| 隐患 | `internal/ui/sync_panel.go:55-58 vs 66-69` | bg.Clicked() 在 children 之前求值（55），panel.Clicked() 在 children 里 | ◇ 已记账（妥协 #25） |

### `watch.go`（5）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| 隐患 | `internal/ui/watch.go:121（定义在 surface_motion.go:523-525、534-541）` | watchTile 在构建路径里调 a.matchable()，它算的是 | ✔ 已修 |
| 隐患 | `internal/ui/watch.go:152-155 → 530-547` | 过渡窗口里把 player 置 nil 交给舞台画；但 watchState(c, room, sess, player) | ◇ 已记账（妥协 #13） |
| 隐患 | `internal/ui/watch.go:465,481` | volume := a.watchVolume(key) 在构建时读一次；HandleInput 里用 | ✔ 已修（滚轮里现读音量） |
| 疑点 | `internal/ui/watch.go:152-155 对比 internal/ui/player.go:361` | 只有"去的那一侧=舞台"（Router.Path()==routePlayer）被挡。离开播放页的 | ◇ 已记账（妥协 #14） |
| 疑点 | `internal/ui/watch.go:88-91` | watchAspect(key) 忽略 key，恒返回 watch.DefaultAspect，参数是死参数。 | ✔ 已修 |

### `watch_media.go`（8）

| 级别 | 位置 | 事实（报告原话摘要） | 判决 |
|---|---|---|---|
| BUG | `watch_media.go:1100（+ 763）` | `s.stalled = wallStalled(s.player.Ready(), s.player.Playing(), s.still | ✔ 已修 |
| BUG | `watch_media.go:1105` | `watchTick` 对焦点会话直接 `continue`，跳过的不是一条判据，是整段看门狗：首帧预算(1111)、`watchFirst | ✔ 已修 |
| 隐患 | `watch_media.go:595-599` | `closeSession` 直接 `a.focus = nil`，只补了 `closeDanmaku`/`resetDanmuHistor | ✔ 已修 |
| 隐患 | `watch_media.go:1285` | `watchRestart` 把内部 reason 原样写进 `s.err`（`"地址到期"`、`"首帧超时"`、`watchReload` | ✔ 已修 |
| 隐患 | `watch_media.go:1022` | `watchReload` 用 `if s == a.focus && a.fullSize()` 分流。焦点在墙上（`fullSize() | ✔ 已修（watchReloadBelongsToMedia） |
| 隐患 | `watch_media.go:724-771 / 917 / 998` | `acceptWallEngine` 等三处 `runOnMain(win, ...)` 都**忽略返回值**。回调里才有 `ctx.Err | ✔ 已修（acceptWallEngine 接住 runOnMain；实测只有这一处持有引擎） |
| 疑点 | `watch_media.go:117-118` | `wallMedia.tickPending` 全仓库只被写 false（player_media.go:302），从来没有地方写 true | ✔ 已修（wallMedia.tickPending 已删，grep 0 处） |
| 疑点 | `watch_media.go:894 / 900 / 910` | 同一个"探测失败"的退避，`!ok` 两支用 `offlineProbeBackoff(s.failures+1, ...)` 但不 `++ | ✔ 已修（两条早退分支补 s.failures++） |

## 附录 A · 已修 35 条的判据

**14 条 BUG**：三组探针现在全绿。

```sh
go test -tags mock ./internal/ui -run 'TestAudit' -overlay .tmp/audit-g1/overlay.json -count=1
go test -tags mock ./internal/ui -run 'TestAudit' -overlay .tmp/audit-g2/overlay.json -count=1
go test -tags mock ./internal/ui -run 'AuditG3'   -overlay .tmp/audit-g3/overlay.json -count=1
```

g1 那三条原本断言的是「bug 存在」，修完变红，我把断言改成了「证明正确行为」之后才绿——
这是它们变绿的真实原因。

**其余 21 条（隐患/疑点）**，按判据类型分：

| 判据 | 条目 |
|---|---|
| 像素/状态断言（`ui.Tester`） | `danmu.go:107-112`（`TestDanmuColorRejectsGarbage`）、`watch_media.go:1285`（`TestReconnectingTileShowsUserFacingText`）、`player_media.go:567-594`（`TestAdoptLiveSessionResetsSwapProgress`）、`player.go:619-624`（`TestNoticePillUsesFrameClock`）、`watch.go:121`（`TestMatchableFollowsFrameClock`）、`watch_media.go:595-599`（`TestCloseFocusedSessionCancelsMediaRound`）、`data.go:299-308` / `data.go:784-793`（`TestHomeFirstChunkRespectsTopUp` / `TestSubFirstChunkRespectsTopUp`） |
| 结构性事实（`grep`/AST 可数） | `category_bar.go:82-84`、`home.go:1043`、`watch.go:88-91`、`follow.go:70-81`、`category_bar.go:168-175`、`danmu.go:335-341`、`danmu.go:416`、`data.go:517-524`、`state.go:178-182`、`diagnostics_panel.go:214-219`、`config_transfer.go:389-402`、`data.go:269-349`、`data.go:649-668`、`player.go:619-624` |

## 附录 B · 判定不成立 3 条的反证

| 条目 | 报告说 | 实际 |
|---|---|---|
| `home.go:1469-1481` | `pendingTries` 按帧计数，14 帧 ≈ 233ms，不是注释说的 700ms | 该处用 `c.After(platformSwitchPoll)`（50ms 计时器），`platformSwitchTries = 14` → 700ms，与注释一致。按帧走的是旧写法，早已改掉 |
| `data.go:758-766` | 渲染路径用 `time.Now()` 记/判 30s 冷却 | 那是**网络请求**的冷却，按「帧钟服务动画进度、墙钟服务真实经过时间」的分界，墙钟正确 |
| `data.go:301 / 481-483 / 785 / 859-866` | `a.Rooms` / `a.SubRooms` / `feedCache` 共享底层数组 | 共享确实存在，但所有 append 写入都落在 len 之外，另一个持有者看不到；构造不出危害用例，不按缺陷处理 |

## 记新账的格式

位置（`文件:行`）、事实（含可复跑判据）、判决（真改/记账/不成立）。`make agents-check`
会验这里每个 `文件:行` 的文件存在、行号没越界。
