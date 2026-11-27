# 已知的债

[AGENTS.md](../AGENTS.md) 说"现有代码不是标准"，这里记着**还没改对**的地方。**是待办，
不是规范**：别照着这些写法写新代码；改到哪把它改对，改不动就在交付里记一行。

分三层，因为三种东西的处置完全不同：

- **真错** —— 行为和意图不一致，用户能撞到。欠的是修。
- **妥协** —— 知道更好的做法，权衡后选了现在这个。欠的是一个前提：什么条件下该改。
- **取舍** —— 不是错，是产品/工程决定。欠的是一句话：为什么是这样。

`make agents-check` 会验这里每个 `文件:行` 的文件存在、行号没越界——账本会随代码漂，
行号过期就该改或删。

## 真错

**没有开着的。** 上一轮那五条已经全部修掉，判据留在「清掉的」里。

## 妥协

| # | 位置 | 事实 | 什么条件下该改 |
|---|---|---|---|
| 1 | `internal/ui` 全仓 86 处 | 在**构建里**读 `.Hovered()` / `.Pressed()` 再喂 `AnimateColor`（统一入口 [hover.go](../internal/ui/hover.go)）。`docs/ui/performance.md` 的建议相反：只有绘制随 hover 变时，应该在 `Draw` 里读 | 实测一屏 **21µs**（`BenchmarkHoverInBuild`），搬进 `Draw` 时间不变、反而每次多 15 次分配（1→16 allocs）。**收益 0.02ms，风险是全站视觉回归。** 等到有性能测量说 hover 是瓶颈再动。新代码如果 hover 只影响绘制、又不在虚拟化列表里，按文档在 `Draw` 里读 |
| 2 | [follows_page.go](../internal/ui/follows_page.go) | 关注页的刷新是每个 worker 处理完一条就 `win.Update` 一次（写回状态）又一次（`followRefreshDone++`）。`Window.Update` 每次都重建整个窗口：54 位关注 = 每轮 ~108 次整窗重建 | 刷新期间机器明显更热、滚动掉帧。**改成攒批**（每 N 条或每 100ms 一次）。当前关注数下用户感知不到，所以没动 |
| 3 | [player_media.go:268](../internal/ui/player_media.go) | `stalled(p)` 在**构建路径**里被 `bufferingPill` 调用，而它会写 `m.lastPos` / `m.lastPosAt`。构建路径写状态本身是违反"构建值是临时的"精神的 | 目前只写两个时间戳、都在主线程、每帧重算幂等，所以是安全的。**改成把观测挪到 `mediaTick`**，`bufferingPill` 只读结果。前提是先有一帧的滞后可以被接受（现在不会） |
| 4 | [island.go:515](../internal/ui/island.go) | 岛的呼吸环被 body 裁掉一部分。视觉上是有意的（环从本体边缘长出来），但代码里没写清楚这是设计还是意外 | 有人报"岛边缘有一圈被切掉的亮边"时，先看这里 |
| 5 | [config_transfer.go](../internal/ui/config_transfer.go) | 诊断包文件名精确到秒（`bundle-20060102-150405.zip`）：同一秒导出两次，第二次覆盖第一次 | 有用户在同一秒点两次。**改成带毫秒或加序号**，代价是文件名变长 |

## 取舍

| # | 位置 | 事实 | 为什么是这样 |
|---|---|---|---|
| 1 | `third_party/mygo` | `go tool mygo migrate-ui .` 在那里列 14 个上游文件；25 个插件包里有 12 个没有文档页 | 上游的账，别修——修了下次升级就被覆盖。逐文件见 [reference/mygo.md](reference/mygo.md) |
| 2 | [surface_flight.go](../internal/ui/surface_flight.go) | 卡片 ↔ 播放页仍然手搓一块像素飞行块，而岛 ↔ 播放页用的是框架的 `Match + Fly` | 两端**内容不同种**：卡片是封面图，播放页是视频。`Fly` 搬的是元素，搬不了内容。等 MyGo 有"跨内容种的共享过渡"再换 |
| 3 | [category_bar.go:168](../internal/ui/category_bar.go) | `selectCategory(i)` 不校验 i 范围（`selectGroup` 校验）。越界会把 `CategoryIndex` 写成不存在的下标 | 所有调用点都传合法下标。**保留不校验**是为了不让每处都多一层防御；真的越界时 `currentCategory()` 会兜底返回 `cats[0]`，界面不崩，只是选中态丢失 |
| 4 | [home.go:712](../internal/ui/home.go) | 每帧都调一次 `windowFullScreen(a.Win)`（一次 syscall） | 换成事件驱动要多挂一个窗口回调，而 `PlayerMini` 也用同一份状态。一次 syscall 每帧的代价量不出来 |
| 5 | [danmu_panel.go](../internal/ui/danmu_panel.go) | 日志满 `danmuLogMax` 后，上一次的结算点被挤出窗口时 `n = len(log)`，胶囊会写"300 条新弹幕" | 用户离开一分钟后回来看到三位数确实不可信。**没有改成精确计数**是因为那要在每条弹幕上记一个序号，而日志本身是有上限的环形缓冲——两个上限对不上时精确值也拿不到。现在的行为至少不会漏（宁多不少） |

## 清掉的

按时间倒序。每一轮都留了当时的判据，改坏了能查回是哪一次。

**清完真错那一轮**（把账本上最后五条修掉）：

- `matchable()` 和 `noticePill` 两处视图态判据读了墙钟：`resizedAt` /
  `noticeUntil` 用 `time.Now()` 写、用帧钟读。改成一个时基之后，
  island_card_bug_test.go 里那句 `time.Now().Add(-time.Hour)` 的手工绕过
  可以拆掉——它本身就是这条债的证据。判据：`tt.Advance` 推过
  `matchSettleAfter` 之后必须能挂 Match，推过 `noticeDuration` 之后提示必须消失。
- 三级分区整条路径删除（`subCategories` / `SubCatIndex` / `tertiaryRow` /
  `subChip` / sub-slide / `clampSubIndex` / `filterStreamers` 的 sub 参数 /
  `Streamer.Sub` / `watchRecord.Sub`）。那个映射从来没有写入点，
  `len(currentSubs()) > 0` 恒假，控件从未出现；而 home.go 的注释说"示例数据
  由 NewApp 填"，newApp 里也没有。判据：分区栏只有一级 + 二级两层 chip。
- `watchAspect(key)` 收 key 却恒返回同一个值——直接读常量，删掉这个入口。
  判据：`watch.Layout` 出来的每格比例相同。
- 弹幕轨道 `Track % tracks` 在窗口变窄后会把两条不同轨道撞到一起
  （Track=7 和 Track=2 在 5 轨时都落到 2），改成钳到最后一轨：
  宁叠不丢，但不乱跳。

**代码审计那一轮**（逐行读 17496 行、44 个非测试文件，100 条发现）：



- 同一类 bug 复制了 9 处：静态底色被 `hoverSurface` 的 `Transparent` 抹掉。
  `Element.Background` 是赋值不是叠加，最后一次生效。抹掉的有：播放页状态层的
  「重试」白胶囊（深色字压深色遮罩，对比度≈1.05:1）、全屏「退出直播间」的胶囊底、
  键盘 ↑↓ 选中的搜索结果底色、空态两个按钮、退出登录、岛的 X、同步面板主按钮。
  改法：`hoverSurfaceRest` / `hoverSurfaceRestOn` 收常态色，9 个调用点把静态那层删掉。
- 首屏刷新把「加载更多」已经取回来的那一段打回去（首页 + 订阅页）。滚到底部时缓存
  刚好过期就是日常组合：列表从 120 条被打回 40 条，而缓存记的还是 120 条的 cursor。
- 热切换在途时换焦点 → `refreshing` 永真 → 地址续期与断流重连永久失效，画面停住。
- 导入去重用的等价关系和运行时 `sameFollow` 不一致（同一主播变两条，取关一次删光）；
  且 `sameFollow` **不是等价关系**，不能用键集合实现。
- 弹幕时长无上界，`MaxInt64 * time.Millisecond` 溢出成 -1ms → 弹幕一条都不显示。
- 焦点格的「缓冲中…」永久挂着（`s.stillSince` 只由非焦点路径推进）。
- 岛展开时内容跑到岛外被裁（`playerIsland` 的 `w` 参数没用过 + `Transition` 和
  `AnimateTo` 两个弹簧追同一个宽度属性）。
- 三处 `[:0]` 原地压缩（`a.Subs` / `a.Follows` / `a.Danmu`）。
- 弹幕颜色不校验（`ui.Hex` 遇到坏值 panic）、退避移位可能为负、快照先清 dirty
  再序列化、诊断面板没有窗口时锁死、格子把内部原因当文案显示、`loadRooms` 缺窗口
  守卫、`syncSearch` 不清错误。


**审计收尾那四条**（都是在核对"到底改完没有"时发现的）：

- 第 10 处 hover 覆盖：空墙按钮的常态底色。上一轮全仓扫描按"相邻行"找，
  漏掉了中间夹着 .Center().Cursor().Label() 链式调用的那处。
- Esc 收起分区搜索后再打字，面板不再打开：CancelFocus 只清"待处理的
  聚焦请求"，放不掉已经生效的焦点，边沿永远不再来。
- 离开播放页的第二次请求被整条吞掉：	ransitionMatch 要把 action 推迟两趟
  构建，那期间第二次 Esc/X 直接 return。
- 焦点会话在"没有引擎、媒体层也没有活轮次"时没人看门狗：两边都以为自己
  不该管。可达路径是墙上取流（openNative 用 context.Background，没超时）
  时点进播放页，nsureFocusMedia 见 loading 早早返回。
**表面过渡那一轮**：岛 ↔ 播放页原来手搓的像素飞行块删掉，改成两端各挂一次
`ui.ElementTransition{Match, Fly}`。原来那块是同一个 `*ui.VideoPlayer` 的第二个
`ui.Video`，一帧里画出两份同一路画面——用户报的「一个直播间同时出现放大后的终态和
过渡态」就是这个。

**再往前**：spinner 的包级构建期标记（改成每个环自己 `Loop`）、切平台等首屏的跨帧
时间戳比较（改成 `c.After` 计时器计数）、观测墙等第一帧的 `c.After(1ms)`（改成
`c.Invalidate`）、构建路径里的墙钟给 Esc 去重、`ui.Scroll` 里铺长列表、下标当行身份、
颜色绕过令牌、弹幕推动器靠轮询路由自停、`ErrAuth` 缺失、0 个项目自有测试、9 处注释
指向已删的 `doc/`。

## 记新债

发现新的不合规写法，按这个格式加一行：位置（`文件:行`）、事实（可复跑的量法）、
**归到哪一层**。归层比写清楚事实更重要——真错、妥协、取舍的处置方式完全不同，
混在一起写就会变成"什么都记了，什么都没改"。




