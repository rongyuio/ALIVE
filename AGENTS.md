# ALIVE 工程契约

> 写给会在本仓库里改代码的 agent。每一条都必须能被**一条命令、一条测试或一次 diff** 证明；证明不了的，降级成建议写进 `doc/`，不要放在这里冒充规则。
>
> 事实（数字、取舍、失败过的尝试）住在 `doc/`，入口是 `doc/README.md`。这里只回答两件事：**怎么写**，**写完怎么算数**。

## 0. 四条铁律

1. **先说要证明哪个行为，再写代码。** 说不出来就是没搞清需求，先问。
2. **绿色不证明对，只证明没违反你写下的断言。** 而断言是你自己写的。真正的证据必须来自代码之外。
3. **改完你自己走一遍真实路径。** 没走过用户描述的那条路，不许说「修好了」，更不许把验收丢给他。
4. **没跑的检查不许写成通过。** 「应该」「理论上」「make check 过了」都不是交付语言。

## 1. 事实从哪儿来

界面是自绘的（`internal/ui` 里 145 处 `ui.Box`、0 处 `ui.Button`），播放是运行时加载的（libmpv DLL + purego），平台协议是从网页端实际行为里核对出来的。三块都没有「常识」可依赖，只有一手材料。

| 要回答的问题 | 唯一权威 | 先看这里 |
|---|---|---|
| MyGo 某个控件怎么用 | `third_party/mygo/docs/ui/<控件>.md` | `docs/ui/README.md` 的索引，用到哪个读哪个 |
| Tester / 生命周期 / 线程契约 | `third_party/mygo/docs/ui/testing.md`、`views.md` | 上游 242 个 `ui/*_test.go` 是同一种用法的先例 |
| 某个 API 在 v0.10.0 还在不在 | `third_party/mygo/ui/` 源码 | `rg -n "func \(.*\) Method" third_party/mygo/ui` |
| mpv 的结构体、枚举、参数顺序 | `third_party/mpv/include/mpv/*.h` | `render_d3d11.h`、`client.h`；purego 只绑符号，不校验你的记忆 |
| 播放为什么这么设计 | `doc/architecture/playback.md`、`decisions/0004-libmpv.md` | 首帧模型、bgr0 的 alpha 陷阱 |
| 平台怎么取流、登录门槛 | `doc/architecture/platform.md` | 真实响应样本放 `.tmp/probes/`，不进仓库 |
| 改这个文件要读哪几篇 | `doc/README.md` 的「必读映射」 | 不在这里抄第二份 |

三条禁令，都是踩出来的：

- **不许凭记忆写 MyGo API。** 上游还在 0.x，minor 之间会改（v0.9.1 把 `Element.Key` 挪成了 `Context.Key`）。写之前 `go tool mygo version` 对版本。
- **不许猜结构体字段顺序。** `third_party/mpv/README.md` 记着那次：D3D11 参数结构体靠记忆写，只因为结构体只有一个字段才没崩——「猜本身不该发生」。
- **不许把文档当实现依据。** 和代码冲突时以代码和上游 docs 为准，并顺手修文档。

查版本用 `go tool mygo version`，查 replace 之后的真实路径用 `go list -m -f '{{.Dir}}' github.com/egoist/mygo`。

## 2. 动手之前

1. `git status` + `git branch --show-current`：工作区里有没有别人的改动，有就留着，不要顺手清理。
2. 查 `doc/README.md` 的必读映射，读完对应文档再动。
3. 打开同包现有测试，看它怎么搭场景（`NewMockApp`、`ui.NewTester`、沿用的假边界）。**新造脚手架之前，先确认旧的那套为什么不够。**
4. 写一句验收句：「改完之后，X 操作产生 Y 结果。」这个 Y 必须能在代码之外找到出处（协议样本、头文件、设计文档、真实窗口）。找不到出处，就没法验证，回第 2 步。

## 3. 写代码

### 3.1 分层

- 逻辑进 `internal/<领域>`，界面只写 `internal/ui`；协议代码不进 `internal/ui`。
- 一个文件一个界面职责。拆不拆看里面有没有两件互不相干的事，不看行数。
- 组件签名：`func (a *App) xxx(c *ui.Context)` 或 `func xxx(c *ui.Context, ...) ui.Element`。
- `third_party/mygo` **不是纯净上游**：`plugins/video`、`plugins/internal`、`media/`、`scene/`、`ui/engine_export.go`、`cmd/mygo/nsis.go` 都有本地改动，动之前看文件头。完整清单在 `doc/reference/mygo.md`，整目录覆盖会把这些全弄丢。

### 3.2 MyGo 生命周期（硬约束，违反会崩）

- `ui.Element` 和 `*ui.Context` 只在**当前构建帧**内有效。存进 App、存进 goroutine、留到下一帧再读，都是悬空引用。
- 只存数据、`ui.Handle`、`c.Services()`。
- `Children(func(){...})` 共享 Context，只切换父作用域。
- 副作用写在 `Clicked()` / `OnClick` 里，不写在渲染路径里：一次事件会重建多帧，渲染路径里的副作用会执行多次。
- 慢 I/O 和耗时计算下 goroutine，结果用 `win.Update(func(){...})` 回主线程；视图数据只在主线程读写。
- 稳定身份用 `c.Key(key)`（现在 10 处，都在列表行、岛、状态点这类会重排的元素上）。`Bind(&handle)` 只在真需要跨帧句柄时用；现在 0 处，不是不能用，是还没到需要的时候。

### 3.3 自绘控件是常态，所以规矩更严

本仓库不用 `ui.Button`：卡片、chip、tab、图标按钮全是 `ui.Box` 手画的。MyGo 控件自带键盘、焦点、禁用态、无障碍；自绘的这些都得自己补回来。

每个可交互元素必须同时具备三样：

- `.Label("用户看得懂的中文")`
- `.Cursor(ui.CursorPointer)`（文字输入用 `CursorText`）
- 一个 `Clicked()` 或 `OnClick`

`internal/ui/label_test.go` 会扫源码兜底，但**它只看同一条链和同文件变量**：控件在 A 函数里造、Label 在 B 函数里贴，它看不见。所以 Label 和 Cursor 写在同一个函数、同一条链上。

命名：和屏幕上的文字一致；同一屏不能重名；有分类语义的加前缀（`分区:英雄联盟`、`平台:斗鱼`、`直播间:主播名`）。纯装饰的容器不加。

> Label 同时是测试入口：`tt.Click("弹幕设置")`、`mygo-automation press @e2` 都靠它。补 Label 不是应付检查，是让这个控件可被验证。

### 3.4 视觉令牌

颜色、字号、圆角、间距、动效全部来自 `internal/theme`，`internal/ui` 里不写字面量。六套配色是 `signal`（默认）、`mono`、`ember`、`parchment`、`daylight`、`paper`；改配色先读 `doc/decisions/0008-palette-system.md`。

### 3.5 播放层 / mpv

- 库是运行时 `LoadLibrary` 加载的，不是链接期依赖：构建不需要 cgo、gcc、`CGO_ENABLED`。
- 客户端 API 版本在 `engine.go` 里校验（v2）。换 DLL 就要同时换头文件，并重跑 `doc/reference/measurements.md` 的首帧对照。
- 默认是软件渲染（NV12，1080p 一帧 3MB）；D3D11 零拷贝那条路自己一个 device、共享纹理给渲染器，原因是 `render_d3d11.h` 写明 mpv 用 immediate context，而它非线程安全。**别为了省一次 copy 把两个 device 合成一个**，那次的现场是整格纯黑。
- `MPV_RENDER_PARAM_*` 的字段、枚举值，从 `include/mpv/*.h` 抄，不从别处抄。
- `plugins/video` 是本地补丁（未合的 PR #124 + 渲染门控 + 零拷贝），改它之前读文件头。

### 3.6 错误

- 错误要能被上层分类（`ErrUnsupported`、`ErrAuth` 这类），不要 log 完继续往下走。
- 面向用户的失败是状态，不是日志：播放页有 loading / err / offline 三种呈现，别新开第四种。
- 日志、截图、提交里不放 cookie、token、签名参数。

### 3.7 改动的形状

修一条路径，只许有一个理由。diff 里多出一个 bool、一个枚举分支，多半是把两件事塞进了同一个状态。

看历史：2026-11-21 到 11-24 共 142 次提交，82 个 `fix:`；`watch_media.go` 改了 27 次、`watch.go` 21 次、`player_media.go` 19 次。同一件事——谁活着、谁在响、画面多大——四个文件各写一遍：`Close()` 17 个调用点横跨 3 个文件，`SetVolume` 横跨 5 个。每修一条路径就加一个判断，下一条路径撞上没覆盖的组合，就是「修一个 bug 造十个 bug」。

规则：

- **一个事实一个写者。** 引擎生死、音量、呈现尺寸、岛上显示哪一间，各自只能从一个地方写。新增调用点必须写理由；能收回 `player_media.go` 就收回。
- **同一个文件 7 天内第三次 `fix:` → 停手。** 不许再加补丁。先把这个文件的写者收成一个，再谈行为。
- **修一条路径发现要动三个文件**：那不是修，是重写。停下来，先把新的所有权写清楚，再动一行。
- 这条不是建议：`TestNoSecondPresentMechanism` 用 AST 钉住了 `SetPresentSize` 的写入口；`TestNoSecondFocusWriter`、`TestNoSecondVolumeWriter`、`TestNoSecondEngineCloser` 分别钉住焦点、音量、会话引擎关闭的写入口。
## 4. 写测试

### 4.1 先认清一件事

测试是你写的，代码也是你写的。写代码时理解错了一处，写测试时会照着同一个错误的理解去断言——**绿得理直气壮，用户一用就崩**。

本仓库现在就是全绿，用户照样在报 bug。所以判一条测试，别问「它绿不绿」，问两句：

- **期望值从哪儿来？** 来自代码之外，还是来自你脑子里那份和代码一起长出来的模型？
- **按用户报的那个 bug 把代码改坏，它会红吗？**

### 4.2 断言的来源，决定测试的强度

| 级别 | 期望值来自 | 特征 | 判定 |
|---|---|---|---|
| L0 | 实现自己 | 同义反复 | 废测试 |
| L1 | 实现内部量（计数器、私有字段） | 重构会红，行为坏了不红 | 只能当补充 |
| L2 | 你自己另写一遍的「应该是 400」 | 你和代码一起错，就一起绿 | 只够防回归 |
| L3 | 代码之外的事实：协议样本、`include/mpv/*.h`、`doc/decisions` 里写死的数、上游实现、物理不变量（面积 / 单调 / 守恒） | 断言有独立出处 | 改逻辑必须有 |
| L4 | 真实运行：真流、真窗口、真文件、真点击 | 用户那条路上到底发生什么 | 改界面 / 播放 / 取流必须有 |

**只有 L0–L2 的改动等于没验证。** 不是态度问题：这几级根本不看代码之外的东西。

### 4.3 六类测试，各自能证明什么

| 类型 | 能证明 | 不能证明 | 落在哪 |
|---|---|---|---|
| 纯逻辑表驱动 | 分支、边界、换算、状态机 | 任何与界面、并发、IO 有关的 | 包旁边 `*_test.go` |
| Tester 视图 | 点击、焦点、输入、滚动、动画帧、无障碍树 | 真字体、GPU、图片解码、mpv、内存 | 同上 |
| AST 静态规则 | 「凡是 X 必须 Y」这类跨文件约束 | 运行时行为 | `internal/ui/*_test.go` |
| 帧探针 | 位移、闪现、不静止、像素变化 | 颜色对不对、字糊不糊 | `internal/ui/frameprobe_test.go` |
| 真实窗口 automation | 真渲染、真滚动、真播放、内存 | 1–2 帧的闪（采样约 18Hz）、系统菜单 | 手工，`make e2e` |
| 变异 | 测试本身有没有分辨力 | 需求对不对 | 临时，见 4.7 |

选错类型等于没测：用 Tester 断言「字没糊」，或用截图断言「没闪」，都拿不到证据。

### 4.4 六种废测试，直接删

这六种在本仓库都真实存在。见到就改，不要「先留着」。

1. **没有断言。** `internal/ui/motion_test.go:25` 的 `TestSkeletonRenders` 只构造了一个 Tester，连 `Frame()` 都没调。它永远绿；要么补断言（骨架在场、reduced motion 下不请求帧），要么删。
2. **常量对照常量。** `internal/ui/island_test.go:206` 断言 `islandPlayerMaxW == 360`；`internal/ui/player_backdrop_test.go:194` 断言三个主题常量等于 B 站 CSS 的三个数。它们只在有人手改常量时红，而那时改的人本来就会改测试。常量的值归注释和设计文档管，测试管行为。
3. **会自己跳过。** `internal/ui/nav_motion_test.go:270` 开头 `t.Skip("初值不同，这条断言没有意义")`。前提不满足就跳过，等于把「没测」写成「通过」。前提不满足应该修场景，修不了就删。
4. **断言私有计数器。** `internal/ui/nav_motion_test.go:264` 在已经断言了 `SubIndex`、`FollowsPlatform` 之后，又断言 `app.followsSeq == seq+1`。可观察行为已经测过了，这句只是在钉实现里的代数——重构一动就红，行为坏了却不红。要守「重新进场」，就用帧探针看内容层确实重建，别数计数器。
5. **源码字符串 grep 冒充行为测试。** `internal/ui/watch_watchdog_test.go:67` 用 `bytes.Contains` 找标识符，改个变量名就绕过。同一文件里的 `assertNoMuteCalls` 是正确写法：规则是「不许调用 `SetMuted`」，就用 AST 查调用点。**规则关于结构用 AST，规则关于行为用运行时。**
6. **为了过而调大阈值、超时、容差。** 超时和有像素差都是缺陷报告。`doc/reference/ui-regressions.md` 写过，每次想改数字时重读一遍。

另两条容易糊弄自己的：

- **`time.Sleep` 等 UI。** UI 的时间只走 `tt.Advance(d)`；`Frame` 不推进时钟。现有 16 处 `time.Sleep` 多数在等磁盘或 goroutine 落地（`state_test.go:88`、`watch_test.go:397`），那是 IO 边界，要带上限轮询并在注释里说明；`real_handoff_test.go:95` 每帧睡 8ms 是因为应用自己的过渡截止时刻走墙钟——已知的混用例外，新代码不要扩大它。凡是等动画、滚动、双击、tooltip 的，一律 `Advance`。
- **只喂被测函数它想听的数据。** 表驱动要有边界值、异常值、空值；只覆盖 happy path 的表驱动是常量对照的变体。

### 4.5 反向回归，不是先红

「先红」只排除恒真，不证明正确：你写代码时想错的地方，写测试时还会照错。顺序应该反过来：

1. **先用真实路径确认行为对**（L4：真窗口、真流、真数据）。
2. 把它固化成测试，期望值抄自代码之外（L3：协议样本、头文件、设计文档），不是你现算的。
3. **按用户报的那个 bug 把代码改坏一次**——不是随便改，是复现原始错误——测试必须红。
4. 恢复，跑绿。

第 3 步叫「反向回归」：它证明这条测试盯着的是那个 bug，而不是你以为的那个。

新功能没有「改前」可红，所以第 1 步不能省：行为没在真实路径上确认过，就别写测试，那只是在给自己发合格证。

历史教训：`ls.Key`、`resetListScroll`、`loadRooms` 里的 `ScrollTo` 三处「修复」，逐个回滚之后测试仍然全绿——那些测试根本没在测它们。

红不出来怎么办：断言写成了恒真，或者场景没走到那条分支。**不要靠改代码去迎合测试。**

### 4.6 Tester 的硬规矩

权威是 `third_party/mygo/docs/ui/testing.md`。本项目的几条：

- **时间只走 `tt.Advance(d)`。** 从视图外改了状态，先 `tt.Frame()`，再断言或 `Advance`。
- 等条件用带上限的轮询（`player_media_test.go` 的 `waitFor`）；上限只是把卡死变成失败，不是可调参数。
- 找元素用 Label 和文本，不用坐标。
- 测试里不发真实请求：`a.Win == nil` 时不发请求、不留后台 goroutine。
- 测试不读真实用户目录。`internal/ui/main_test.go` 的 `TestMain` 把 `AppData` / `LocalAppData` 换到临时目录：真实 `state.json` 能让同一份代码在别人机器上绿、在这台机器上红，而且测试会写回真实登录态。新包照做。

### 4.7 量化：变异抽查

想知道一个包的测试有没有分辨力，做变异：改一个比较符、一个常量、一个分支，跑对应测试，期望红。存活的就是没测到的地方。

基线（2026-11-24，`internal/watch/layout.go`，10 个手工变异，`go test ./internal/watch -run TestLayout`）：**5 个存活**。杀死的是 `MaxRooms`、`cols` 上取整、无效 aspect 回落、格内居中；存活的是 `gap` 钳位、初始 `minArea`、格子尺寸下界、同分行数优先、面积比较 `eps`、`contain` 判据。结论：布局的「形状」被测住了，「选择规则」（同分怎么取舍、边界往哪边倒）没被测住。

用法：**新写或重写一个包的测试时，对最关键的判断至少抽一次变异。** 存活就补测试，不要补注释。

### 4.8 改界面、播放、取流：你必须亲手开一次真窗口

不是「可以」，是「必须」。`ui.NewTester` 验不到：真实字体光栅化、图片解码、mpv 画面、GPU 合成、内存。这些正是用户每天看到的东西。

- 用 `make e2e` 起带 automation 插件的构建；先 `mygo-automation skills` 拿当前版本手册。
- `--settle` 在本应用基本等不到（灵动岛常驻动画）；等内容用 `wait --text` / `wait <sel>`，不要 sleep 再截图。
- 优先 `press` / `fill`（按 ref，不依赖坐标）；自绘元素没有 Label 就读不出来。
- 它抓不到 1–2 帧的闪，别假装解决了，写清楚。
- 收尾：`close` 只断会话，还要关掉自己起的 exe。

### 4.9 探针不进仓库

要连公网、要真实流、跑不进 `make check` 的测试放 `.tmp/probes/<包名>/`（`.gitignore` 挡着），基准数字进 `doc/reference/measurements.md`。同理不新增 `ALIVE_*` 验收开关：能用 `ui.NewTester` 就用它，要真实窗口就用 `mygo-automation`；假数据层走构建标签，正式二进制里不存在。

## 5. 排障

**用户报障是事实，不是待核实的说法。** 他给了操作路径，就是给了全部输入：不要第二次索要，不要暗示他操作错了，不要用「我这儿复现不了」结案——那是你没走对路径，不是他没说清。先复现，再归因，再修。复现不了就换环境、换数据、换窗口尺寸继续复现，直到看见他描述的那个现象。

- 「闪 / 抖 / 不对」：先翻成四类不变量（位移 / 明暗 / 闪现 / 不静止），写逐帧探针，先在帧表里量出现象，再改代码。方法在 `doc/reference/ui-regressions.md`。不要先截图。
- 「播放卡 / 黑屏 / 没声」：先分清上游（平台流断了、mpv 在缓冲）和我们（尺寸、门控、接管）。对照 `doc/architecture/playback.md` 的首帧模型和 `doc/reference/measurements.md` 的实测。
- 「崩了」：先拿完整栈，判断是悬空 Element/Context、nil 接管，还是 goroutine 里碰了视图数据；最后一种见 3.2。
- 修 bug 的顺序固定：先用真实路径复现 → 最小修复 → 反向回归（把 bug 注射回去，测试要红）。
- 修完的判定不是「测试绿了」，是你自己点过去、看到对了。绿了没点过 = 没修。
- 复现不了也要交付：列出已排除的路径和证据。「环境问题」不是结论，除非能指出是哪一条环境差异。

## 6. 交付

回复里必须有这五段，缺一段就是没交付：

1. **走过的路径**：你自己点了什么、看到什么（截图或 automation 输出）。没走过就别往下写。
2. **改了什么**：文件 + 一句为什么（不是复述 diff）。
3. **证据**：期望值出处（L3 / L4 是哪一条）、反向回归的输出、帧表、截图（注明参照帧）、实测数字（带日期和条件）。
4. **跑了什么**：命令原文；**没跑什么**、为什么。
5. **风险与未完成**：已知边界、没验证的平台、留给下一次的事。

禁止句：「测试全绿所以没问题」「make check 过了所以能跑」「应该可以」。`make check` 不含真实窗口；CI 也不等于 `make check`（见 7.2）。

## 7. 命令与门禁

### 7.1 常用命令

`make check`（全量：fmt + build + vet + mygo vet + lint + test + test-mygo）、`make test`（`-tags mock`，隔离 APPDATA）、`make dev`、`make e2e`、`make mock`，其余见 `make help`。单独 `go test` 要给 `ALIVE_MPV_LIB`（DLL 的绝对路径），否则测试二进制在临时目录里找不到库；`go run .` 同理，看真实播放用 `make dev`。`golangci-lint` 用官方 release 二进制，不要 `go install`。

### 7.2 门禁不是一道

| 改动 | 最低验证 |
|---|---|
| 纯逻辑 | 包内 `go test` + 先红证明 |
| 界面 | `make check` + 帧探针或截图 |
| 播放 | `make check` + 真实流首帧对照（数字带日期） |
| `third_party/mygo/plugins/video` | `make check`（含 `test-mygo`）+ 真窗口播放 |

**CI 与 `make check` 不等价**：`.github/workflows/check.yml` 跑 gofmt / build / vet / lint / mygo vet / test，**不跑 `test-mygo`**（本地漂移的 `plugins/video` 测试）。改插件那一层时，本地 `make check` 才是完整的。

## 8. 提交、授权与安全

- 提交信息 `type: 描述`，type 取 `feat` / `fix` / `docs` / `refactor` / `test` / `chore`，描述用中文。
- 分支 `feat/<主题>`、`fix/<主题>`、`chore/<主题>`；同一任务复用同一分支。
- 一次提交只做一件事；提交前看 diff、跑验证。
- 未经明确授权不改写历史、不强制推送、不丢弃改动。推送 / PR / 合并 / 发版 / 部署，验收通过后才做，且只做被授权的动作。
- 不提交 cookie、token、证书、签名文件；日志、截图、交付文档里也不放。
- 运行态数据写 `%APPDATA%\alive`，日志与缓存写 `%LOCALAPPDATA%\alive`。
- 平台接口节制轮询，触验证码就退避，不并发轰接口。

## 9. 这份文件本身

- 加一条规则，同时给出**它靠什么检查**；给不出检查方式的，写进 `doc/` 当背景，不写在这里。
- 删一条规则，说明**它曾经防住什么**。
- 规则和代码冲突时，先改代码或先改这条规则，不要让两者长期不一致。