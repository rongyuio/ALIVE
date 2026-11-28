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
| 6 | [player_media.go:1789](../internal/ui/player_media.go) | `applyMedia` 的 `stale()` 在 `runOnMain` 返回 false（窗口已销毁/未建）时于**后台 goroutine 同步执行**。现有两个 stale 只是 `e.Close()`，所以现在是安全的；但这是「回调只在主线程跑」这个前提的破口 | 等到有 stale 需要写 App 状态（而不只是关资源）时必须改：把它拆成「纯资源清理」和「主线程收尾」两半，或者证明那种清理能在后台安全做。当前没有这种调用者 |
| 7 | [home.go:716](../internal/ui/home.go) | 播放页里按 F / 双击切全屏的那个边沿会 `routeTransitionUntil = time.Time{}` + `Router.Transition = TransitionNone`，把还没演完（~400ms）的进/出播放页过渡直接抽掉，画面从 Match 动画变硬跳 | 这是有意让「全屏边沿优先」——不这么做整页会做一次 0.5s 弹簧。要改得先有「过渡中切全屏」的观感判据（真窗口逐帧），否则只是换一种观感。等真有人报「按 F 画面跳一下」再动 |
| 8 | [island.go:324](../internal/ui/island.go) | `islandNavReserve` 把顶栏两端固定控件的几何（pad 14/10、gap 14/8、tab 64/56、窗口按钮 40/34、chromeH、各处 3×2 内边距）抄成字面量，和 home.go 的 `Navbar` 是两份。 | 现在两组数值一致（已逐项核对：`chromeH=34`、`tabW 64/56`、pad/gap 14/8/10）。要改得先在两处共用同一组常量——但那要把 `Navbar` 的内部布局提出来，属于结构改动。**改顶栏几何时两处必须一起改**，否则岛会压到 tab 上或留着一段空隙。 |
| 9 | [island.go:111](../internal/ui/island.go) | 构建路径里调 `enterPlayerIsland()` / 清 `a.infoRoom`，属于"构建时写 App 状态"。 | 写的是从 `a.current()` 推出来的记号，且函数入口有 `if room == a.infoRoom { return }` 兜住，同一帧多次构建是幂等的，所以现在安全。等到它需要副作用（发请求、改路由）时，必须挪到 `OnClick`/`c.After`。 |
| 10 | [follows_page.go:212](../internal/ui/follows_page.go) | `a.followsSectionBox` / `a.followsBox` 把构建时 `measureRow`/`measureChips` 量出来的行盒存进 App 字段。 | 存的是这一帧的布局，供同帧的滑块与命中用；跨帧读就是过期布局。现在只在同一帧内用，安全。**任何跨帧读这两个字段的改动都要先把它们改成每帧重算**。 |
| 11 | [config_transfer.go:777](../internal/ui/config_transfer.go) | 导入收尾挂在 `win.Update(...)` 里；窗口销毁时回调被丢弃，`SyncBusy` 会永久立着。 | 两个入口（`exportConfig`/`pickImportFile`）都已经 `if a.SyncBusy || a.Win == nil { return }` 挡住"没有窗口"；剩下的只有"窗口在飞行中被销毁"，而那是关机路径——进程随即结束，锁死不可见。**将来若支持多窗口或窗口重建再回来看**。 |
| 12 | [category_bar.go:495](../internal/ui/category_bar.go) | 二级分区行的 `avail` 原来按整窗减两个 14 算，而这一行嵌在 Home 的 Column（`bodyPadX`：≤1240 时 10）+ 分区面板 `Padding(12,14,14,14)` + `ScrollHorizontal.Padding(2)` 里，把可用宽度算大 10~20 DIP。已按真实 padding 改正。 | 改正是按布局推导的，但**构造不出能区分新旧公式的窗口宽度**（700–1300 每 8 DIP 扫一遍，两种写法的 overflow 结论处处相同），所以没有可跑判据证明旧写法真会误判。要坐实得在真窗口里把宽度调到临界值，看最后一枚 chip 是否被裁且没有「全部」入口。 |
| 13 | [watch.go:540](../internal/ui/watch.go) | 过渡那一帧 `watchState` 收到 `player == nil`（画面交给舞台画），而 `sess.player` 非 nil、`sess.loading` 为假，于是落到 `sess.stalled` 分支，可能在缩小的格子上闪一次「缓冲中…」。 | 现在 `sess.stalled` 要「时间轴 1.5 秒没动」才为真，过渡只有几百毫秒，误报的先决条件很窄。要坐实得真窗口 60fps 逐帧看过渡期间那一格的字。 |
| 14 | [watch.go:153](../internal/ui/watch.go) | 只有「去的那一侧是舞台」被 `routeTransitionUntil` 挡住。离开播放页时 Router 会同时建两页，墙格和淡出中的舞台可能同一帧各建一个 `ui.Video`。 | 仓库自己的结论是「一个 VideoPlayer 只能被一个 ui.Video 画出来（实测两个元素只有一个出画）」，而按当前 `videoEngine.advance` 第二次调用会 early return、两块元素引用同一个 bitmap——也可能两个都出画。**两种结论我都没在真窗口验过**，守卫是单向的这件事本身成立。要改先得录 60fps 看飞行块里有没有画面。 |
| 15 | [follow.go:388](../internal/ui/follow.go) | 刷新的收尾（`followsBusy = false`）整个挂在 `runOnMain(win, ...)` 里；窗口销毁时回调被丢弃，`followsBusy` 永久为 true，本会话不会再有第二轮刷新。 | 和 `config_transfer.go:777` 同一类：只在窗口销毁（关机路径）时发生，进程随即结束，锁死不可见。将来支持多窗口/窗口重建再回来。 |
| 16 | [surface_motion.go:72](../internal/ui/surface_motion.go) | `setFocus` 对新焦点无条件 `s.playing = true`，且 `s.volume <= 0` 时抬到 `volumeBeforeMute` 或 0.6。 | 「点开它就是要听」只对墙格成立。从岛/列表重新进入一个用户特意暂停并静音的房间时，它会自己开播并出声。要改得先定语义：重新进入到底该恢复上次的播放意图，还是一律开播？这是产品决定。 |
| 17 | [surface_motion.go:502](../internal/ui/surface_motion.go) | `backgroundEnter` 判「死焦点」的条件里有 `(f.stream.RoomID != "" && !f.stream.Live)`，而 `stream` 是会话的值字段，很多路径根本不填。 | RoomID 为空时这一条恒不成立，于是「没有引擎、没在取流、也没在加载」的焦点会留在岛上（岛显示一条不存在的路）。判据依赖一个可选字段的有无，不是状态本身。要改得先把「死焦点」重新定义成只看引擎/取流/加载三件事，那会改变若干路径的收尾时机。 |
| 18 | [surface_motion.go:183](../internal/ui/surface_motion.go) | 已经在播放页时换台清了 `surfaceFlight/exitArm/exitAction/exitMatchKey`，但没清 `routeTransitionUntil`（只清了 `playerExitUntil`）。 | 旧窗口没过期就还留着，`watch.go` 的判据靠它决定要不要让墙格画这个 player——跨台时会多余地触发一次「墙格不画」。要坐实得真窗口连点两间隔台看过渡帧。 |
| 19 | [danmu.go:147](../internal/ui/danmu.go) | `ensureDanmuLoop` 用 `active=true` 再 CAS(loop false→true)；循环体每 33ms 检查 active，退出时才把 loop 放回 false。 | stop→start 若发生在旧 goroutine「已读到 active=false、还没跑到 defer」的三十几毫秒里，CAS 失败直接 return，随后旧循环退出并置 loop=false——没有任何 goroutine 在跑而 active 为 true。恢复全靠下一次构建再调一次。要改得把「谁在跑」表示成一个不靠 CAS 的状态机。 |
| 20 | [danmu_panel.go:181](../internal/ui/danmu_panel.go) | `a.PanelList.Key` 闭包捕获这一趟构建的局部切片 `list`（还可能被截到末 200 条），而 Key 存进 App 的长期 `ui.ListState`。 | 面板收起后 `danmuPanelList` 不再执行，闭包一直是旧快照；任何在别的路径上对这个 ListState 调 Key 的地方都拿到过期切片，越界即 panic。现在唯一的重建点每次都先赋值，所以还能跑。要改得把身份改成不依赖闭包捕获（比如直接读 `a.DanmuLog` 并自己 clamp）。 |
| 21 | [danmu_panel.go:168](../internal/ui/danmu_panel.go) | `panelNewDanmu()` 在构建路径里推进 `a.panelSeen`/`a.panelMark`，并累加 `a.panelPending`。 | 一帧构建多次时第二次读 n=0（自己把已读位推了）不双计；但这一帧只要没建到面板（收起、或在别的路由），已读位就整段漏掉，`panelPending` 少算。要改得把「读到哪了」挪出构建路径。 |
| 22 | [danmu_panel.go:98](../internal/ui/danmu_panel.go) | 拖拽状态存在 `a.panelDragW/panelDragX` 两个 App 字段上，唯一复位路径是 `!pressed && !held`。 | `Dragged()` 只在主键按下期间为真；指针被系统抢走（PressCanceled）或窗口失焦时 pressed/held 同时为假，靠这一条复位是巧合成立的——代码里没有 PressCanceled 分支。要改得显式处理取消事件。 |
| 23 | [state.go:207](../internal/ui/state.go) | 快照结构里直接塞了 `a.roomChoices`（活 map）和 `a.PlatformOrder`（活切片），靠「同函数内同步 marshal」才没炸。 | 这是把跨线程写盘的设计挂在一个隐含前提上：任何人把 marshal 挪进 goroutine 就是数据竞争。要改得在快照里深拷贝这两个值。 |
| 24 | [state.go:288](../internal/ui/state.go) | `restore` 对 `PlatformOrder`/`FollowsSectionOrder` 做了 normalize，对 `Subs` 只挡了一组历史示例数据，对 `FollowRecords`/`WatchRooms` 的 `Platform` 字段完全不校验（只有 WatchRooms 过 `normalizeWatchRooms` 挡空串）。 | 手改/旧版写坏的文件里出现 `"douyu "`（带空格）之类的值时，会安静留下一条永远刷不到、永远匹配不上的关注记录，而且一直被写回文件。要改得先定「坏记录是丢弃还是修字段」。 |
| 25 | [sync_panel.go:55](../internal/ui/sync_panel.go) | `bg.Clicked()` 在 children 之前求值（55），`panel.Clicked()` 在 children 里再把 `a.SyncOpen` 设回 true（67-69）。 | 这是靠「求值顺序恰好如此」来抵消遮罩点击。一旦 MyGo 改成 backdrop 先吃掉点击再建 children，或调换顺序，点面板内部就会直接关掉弹窗。要改得看框架有没有「点击是否落在某元素内」的正式接口。 |
| 26 | [data.go:620](../internal/ui/data.go) | `Hue: float32(i*37 % 360)`，i 是这一段 chunk 里的下标。 | 位置决定色相：同一间房在首页第一页和在补页里拿到的色相不同，翻页/刷新后封面渐变色会变。改成按房间号哈希就稳定了，但那会一次性改变所有已有卡片的配色（视觉变更），得先确认可以接受。 |
| 27 | [player.go:63](../internal/ui/player.go) | 同一帧里「是不是全屏」有两个来源：`a.playerFullscreen()` 读 `a.winFullscreen` 字段（Shell 每帧写），`:786` 那处直接问窗口。 | 两个来源在同一帧内可能不同（字段是这一帧开头写的，直接问窗口是当下）。要改得统一成一处，但那要把 `winFullscreen` 的写入时机前移到所有读者之前。 |

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

**收尾自查那一轮**（跑全部审计探针，发现上一轮的修复没真生效）：

- 「补页推远了就别用这一份首屏」的守卫放在了 loadRooms 的闭包里，
  `applyFirstChunk` 本身还是无条件覆盖。探针直接调它，列表照样被打回
  40 条。守卫挪进函数内部（返回 bool），调用者绕不过去。
- `closeSession` 对焦点那一间只做了 `a.focus = nil`，没有 cancelSession：
  `m.ctx` 还活着、后台取流不会被取消、`applyMedia` 的代次检查也认为
  它仍是当前一轮。判据：会话销毁后 `media.ctx` 必须为 nil 且 seq 推进。
- 审计探针自身有两处假信号，一并修了：`TestAuditOverlayButtonBackground`
  的取样点在按钮**外面**（`r.X-12`，而 Find 返回的就是按钮），修好之后
  无论按钮什么颜色都报红；`feedKey` 格式变了之后几条探针的前提断言没跟上。
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





