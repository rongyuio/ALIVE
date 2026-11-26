# ALIVE 验收面

这不是文档，是**判定表**。

一个功能只有在这里占一行、并且那一行连着一条**走用户路径**的断言，才算验过。
不在这张表里的东西不算功能，也就不为它写测试。

## 怎么算"验过"

一条断言必须是这样：

```go
tt.Click("直播间:旭旭宝宝")                 // 用户的手
tt.Frame()
tt.Find("播放画面（双击全屏）")              // 用户的眼睛
```

判据只能是 `tt.Click` / `tt.Find` / `tt.HasText` / `tt.Image` / `tt.Arc`。
**断言里出现 `app.` 的内部字段，那条不算验收**——它锁的是我的实现，用户看不见，
改代码时它红、用户那边它绿，净作用是负的。

## 现状（2026-11-26）

33 条，约 4 秒跑完（`features_test.go` / `features_pip_test.go` / `features_fullscreen_test.go` / `features_auth_test.go` / `features_media_test.go`）。
**改完代码跑这一条命令就够了：**

```
go test -tags mock ./internal/ui -run TestFeature
```

`断言`列是**测试函数名**，`make gate` 的 **features** 检查盯着它：表里出现的函数
必须真的存在，`features_*.go` 里的 `TestFeature*` 必须真的在表里。两边任缺一个
就红——这张表不许和代码各说各的。

| # | 用户做什么 | 应该看到什么 | 断言 |
|---|---|---|---|
| 1 | 启动 | 首页出卡片 | `TestFeatureHomeHasCards` |
| 2 | 点平台 tab | 卡片换成该平台的，滑块移过去 | `TestFeaturePlatformSwitch` |
| 3 | 首屏封面没解码完时切平台 | 内容照样立刻换，不等封面 | `TestFeaturePlatformSwitchDoesNotWaitForCovers` |
| 4 | 点一级分区 | 卡片换一批 | `TestFeatureGroupSwap` |
| 5 | 点二级分类 | 卡片换一批 | `TestFeatureCategorySwap` |
| 6 | 点三级子分区 | 卡片换一批 | `TestFeatureSubCategorySwap` |
| 7 | 点「订阅分区」 | 变成「已订阅」 | `TestFeatureSubscribeToggle` |
| 8 | 切订阅项 | 卡片换成那一项的内容 | `TestFeatureSubscribeItemSwitchChangesCards` |
| 9 | 关注页切一级分区 | 内容区换成那一级 | `TestFeatureFollowsSectionSwitchChangesContent` |
| 10 | 点顶栏搜索 | 出「搜索直播间」浮层 | `TestFeatureSearchOpens` |
| 11 | 点卡片 | 进播放页，画面在 | `TestFeatureOpenRoomShowsTheStage` |
| 12 | 进播放页 | 有一段飞行块，走完消失 | `TestFeatureEnteringARoomAnimates` |
| 13 | Esc | 回列表，画面消失 | `TestFeatureEscReturnsToList` |
| 14 | 播放页 X | 回列表，画面消失 | `TestFeatureCloseReturnsToList` |
| 15 | 播放页 X | **没有收尾动画**（明确关掉，不需要演一遍） | `TestFeatureClosingARoomDoesNotAnimate` |
| 16 | 卡片滚出列表后点岛 | 画面从岛起飞，不从屏幕外那张卡 | `TestFeatureIslandFlightStartsFromTheIslandAfterScrolling` |
| 17 | 双击画面 | 进窗口全屏（顶栏让位） | `TestFeatureDoubleClickGoesFullscreen` |
| 18 | 系统全屏时按 Esc | 只退全屏，人还在直播间 | `TestFeatureEscLeavesFullscreenBeforeTheRoom` |
| 19 | 系统全屏 | 画面右上角有最小化/最大化/关闭 | `TestFeatureFullscreenShowsWindowButtons` |
| 20 | 点暂停 | 按钮变「播放」 | `TestFeaturePause` |
| 21 | 点静音 → 取消静音 | 音量回静音前的值 | `TestFeatureMuteKeepsTheVolume` |
| 22 | Tab 到音量滑块 → 按 → | 读屏念得出值，音量真的变大 | `TestFeatureVolumeSliderIsAdjustableWithoutAPointer` |
| 23 | 切清晰度 | 下拉里两档都在，选了值跟着换 | `TestFeatureQualityDropdown` |
| 24 | 切线路 | 下拉里两条都在，选了值跟着换 | `TestFeatureLineDropdown` |
| 25 | 单线路平台 | 不出现「线路」这个控件 | `TestFeatureNoLineDropdownWithoutLines` |
| 26 | 取流被平台拦下 | 屏幕上出现可读原因和重试入口 | `TestFeatureStreamFailureShowsRetry` |
| 27 | 点关闭弹幕 | 变成「开启弹幕」 | `TestFeatureDanmuToggle` |
| 28 | 点弹幕设置 | 出字体/彩色弹幕/描边 | `TestFeatureDanmuSettingsOpens` |
| 29 | 点弹幕屏蔽 | 出屏蔽词面板 | `TestFeatureDanmuFilterOpens` |
| 30 | 加一个屏蔽词再删掉 | 计数 0→1→0，列表跟着变 | `TestFeatureDanmuFilterAddAndRemove` |
| 31 | 收起弹幕面板 | 出「展开弹幕」 | `TestFeatureDanmuPanelCollapse` |
| 32 | 点配色面板 | 出 13 个色板 | `TestFeaturePalettePanelOpens` |
| 33 | 点数据同步 | 出「保存到文件…」 | `TestFeatureSyncPanelOpens` |
| 34 | 鼠标移上卡片 → 点左上角星 | 变「取消关注」；**没**悬停时点击穿透给卡片 | `TestFeatureFollowNeedsHover` |
| 35 | 点「小窗播放」 | 小窗接管；播放器停了会被下一帧拉回来 | `TestFeaturePipKeepsThePictureMoving` |
| 36 | 小窗里点暂停 / 关闭小窗 | 播放状态变；小窗关掉、主界面回来 | `TestFeaturePipControlsWork` |
| 37 | 再点一次「小窗播放」 | 关掉 | `TestFeaturePipToggles` |
| 38 | 点登录 | 出浮层并开始取码；登录后换成账号名 | `TestFeatureLoginOpensAndShowsTheAccount` |
| 39 | 点退出登录 | 回到未登录态 | `TestFeatureLogoutReturnsToLogin` |
## 一个反例：音量值不读音量条上的文本

音量那条断言原来写的是 `tt.HasText("0%")`。它是子串匹配，`"60%"` 里也含 `"0%"`，
于是取消静音之后音量明明是 60%，断言还是红的——我拿去查实现，实现是对的。

真正的问题是那根音量条当时只是一颗 `Label("音量")` 的普通 Box：

```
role=2  name="音量"  value=""     ← 普通分组，没有任何值
role=3  name="60%"   value=""     ← 只是一段文字，不是控件的值
slider 总数 = 0
```

读屏只会念「音量」，念不出当前多少；`AccessIncrement` 在 MyGo 里被换成方向键交给
元素，而那个元素没接，所以读屏用户按了没反应。**能用 `HasText` 猜，正是因为控件
没做成能读的东西。**

修的是控件，不是断言（`player.go` 的 `volumeControlFor`）：

```go
Role(ui.RoleSlider).
Range(0, 100, math.Round(float64(a.focusVolume())*100)).  // → aria-valuemin/max/now
Step(volumeKeyStep * 100).                                // → 读屏念的步进和实际一致
Focusable()
if hot.Shortcut(0, ui.KeyLeft)  { a.setVolume(...) }       // 读屏的减小 → KeyLeft（access.go:1240）
if hot.Shortcut(0, ui.KeyRight) { a.setVolume(...) }
```

修完：

```
role=9  name="音量"  min=0 max=100 now=60 step=5
第 1 次 Tab 停在音量条上
按 →  0.60 → 0.65
```

断言随之变成 `tt.AccessByRole(ui.RoleSlider, "音量").Now` —— 读控件的
`aria-valuenow`，不是猜屏幕文本。

**规则：能量化、有语义的状态，读控件的无障碍值；屏幕文本只用来判断"有没有"，
不用来判断"是多少"。**

## 小窗：卡死的是我的测试写法，不是功能

这条原来记在"验不了"里，理由是 `togglePip` 会 `mygo.NewWindow` 并等窗口就绪，
`ui.Tester` 里永久阻塞，实测挂到 3 分钟超时。**这个理由站不住**：卡住的是我
把窗口创建写死在函数里，不是小窗不能测。

改法是把窗口创建抽成一行间接，测试换成 nil；"小窗开着"从窗口指针里分出来，
独立成 `pipOn`。于是不开真窗口也能断言：

| 用户做什么 | 应该看到什么 |
|---|---|
| 点「小窗播放」 | 小窗接管；播放器单方面停下后，下一帧被拉回来 |
| 小窗里点暂停 | 播放状态真的变 |
| 点「关闭小窗」 | 小窗关掉，主界面回来 |

顺手修到一个真 bug：`invalidateWindows` 拿 `miniOpen()` 当"有没有窗口"的
判据，而 `miniOpen` 回答的是"小窗模式开没开"——窗口为 nil 时那是空指针。

**教训：验不了的时候，先问"是我的测试写法和代码耦合太死吗"。**

## 切平台：也是我的测试写法问题

原来记的理由是"mock 的四个平台用同一份假数据，切过去屏幕上什么都不变"。查下去
发现两件事：

1. 假数据**本来就按平台造了**（`mockStreamers` 里 `Platform: platform`），
   只是每家的主播名池一样，切过去前四个名字碰巧相同。数据没问题，是我没去看。
2. 真正的坑是时钟：`tickPlatformSwitch` 用 `time.Since`（真实时钟），
   `tt.Advance` 推的是虚拟时钟。虚拟推 900ms，平台永远不切。

所以这条要用真实时钟等条件（`waitFor`），滑块那段再用虚拟时钟推弹簧。

现在的断言：点「虎牙」→ 等卡片真的换了一批 → 推 400ms 让滑块走完 →
虎牙那格是 Accent、斗鱼那格不是。

## 系统全屏与登录：同样是我的写法问题

两条原来都记在"验不了"里：

- **系统全屏**：`windowFullScreen` / `IsMaximized` / `SetFullScreen` 直接调
  `*mygo.Window` 的方法，零值 Window 在 `ui.Tester` 里会走 `onMain` 永久阻塞。
- **登录**：点「登录」会发起真实的扫码网络请求。

三个接缝抽出来（`windowMaximized`、`windowSetFullScreen`、`authFor`），两条都
能验了。系统全屏那条验的是 `shell_keys.go` 里写明却从没断言过的规则：
**Esc 只退全屏，不把用户踢出直播间。**

## 清晰度与线路：两个 mock 缺陷

这两条在 mock 数据下根本没东西可点，查出来是两个 mock 的问题，不是功能缺失：

1. `mockStreamers` 造的房间 `RoomID` 是空串，而 `fetchStream` 第一行就是
   「RoomID 空就返回」——取流从来没跑过。
2. mock provider 的 `Stream` 故意不给 `Variants`（给了就会去开真播放器）。

**没有给 `mockStreamers` 补 RoomID**：那会让所有走播放页的测试都开始真取流
（试过，弹幕那 7 条当场红）。只在这几条媒体测试里换成一间有 RoomID、落在当前
分区里的房。

另外两个下拉是「指针移上去就开、点一下反而关」（`h.hover` / `h.clicked`），
和网页端的 `onMouseEnter` 一致。断言用 `Move`，不用 `Click`。

## 缺口：补不上可见判据的（记在这里，不假装验过）

按「删一条先补一条」的规矩，下面这些在 `ui.Tester` 里**没有正面可见判据**，
所以对应的内部断言保留，理由写在这里：

| 守的是什么 | 为什么没有可见判据 |
|---|---|
| 飞行途中被 `Router.Push` 换页，飞行块要作废 | 作废之后屏幕上什么都不该剩下。「没有东西」在 Tester 里只能断言字段为 nil |
| 平台降档时弹提示 | 提示在 `applyMedia` 里发，而 mock 的播放器开不出来（`ErrUnsupported`），走不到那条路。要验得用真引擎（`ALIVE_MPV_LIB`） |
| `Router.Transition` 不串页（退场窗口的 Match 不该被下一个 tab 继承） | 串页的后果是「整页交叉淡化」，而 Tester 里那个字段不影响渲染结果——它只影响真窗口的 MyGo 路由过渡 |
| 重选已选中的分区不能换代 | 试过像素判据，反向回归抓不到：mock 数据一帧就重铺完，像素又回到一样。真实后果是「点击丢失」，而 `tt.Click` 是同步的，不会丢 |

**这四条要么需要真窗口，要么需要真引擎。** 它们不是"省事没做"，是当前这套
测试工具看不见。要补，先得有 `make e2e` + `mygo-automation` 的判据。

## 这些断言没盖住什么

只跑 `features_test.go` 会有一种错觉：全绿。它证明的是**界面逻辑在假数据下对**，
不证明：

- 真实流能不能出画面（要真窗口 + 真平台）
- 切清晰度/线路（mock 的流没有任何档位）
- 断流重连、平台下播
- 12 路墙的内存/CPU

这几条要么真窗口，要么另想办法，不能靠这张表冒充。

## 历史欠账（另行处理，不进这张表）

`internal/ui` 下还有约 190 个测试文件、28000 行，其中大量断言落在
`a.surfaceFlight` / `a.Router.Transition` / `a.focus.present` 这类内部字段上
（实测 1896 次内部断言 vs 268 次用户可见）。它们的净作用是负的：让改动变贵，
保护的是实现而不是行为。

处理顺序：先让上面这张表站稳（已做），再按"断言里有没有 `app.`"逐文件删。
删完 `go test ./internal/ui` 应该更快，而这张表照样全绿。
