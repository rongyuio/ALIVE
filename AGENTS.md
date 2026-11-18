# ALIVE 项目约定

ALIVE 是一个直播客户端：Go 写全部逻辑，界面由 MyGo（`github.com/egoist/mygo`）用 GPU 直接画出来，没有 webview、没有前端构建。

## 设计令牌

所有视觉数值以 `internal/theme` 为唯一来源，不在 `internal/ui` 里写字面量：

| 项 | 值 |
|---|---|
| 字号 | 11 / 12 / 13 / 15 / 18 / 24 / 32 |
| 圆角 | 8（控件）/ 12（卡片、面板）/ 16（大容器）/ 999（胶囊） |
| 间距 | 4px 基线：4 / 8 / 12 / 16 / 24 |
| 动效 | `--ease-out` = cubic-bezier(0.22,1,0.36,1)；110 / 190 / 280 / 420 / 620ms |
| 布局 | 顶栏 64 高、正文左右 14（≤1240 时 10）、卡片最小宽 220、网格 gap 16 |
| 卡片 | 封面 16:9；信息条 `padding 13 12 15 12`；头像 32；不再显示分类胶囊 |

配色六套：`signal`（默认）、`mono`、`ember`、`parchment`、`daylight`、`paper`。

## 工程结构

新功能按职责落到 `internal/<领域>`；界面代码只写在 `internal/ui`，协议代码不进 `internal/ui`。
顶层目录只有 `doc/` 是文档目录这一个例外。

几条从目录列表看不出来的：

- `third_party/mygo/` 是 replace 到本地的上游源码，**不是纯净上游**。相对上游的漂移：
  `plugins/video`（播放、vid 开关，移植自未合的 PR #124）、`plugins/internal`、`media/`、
  `scene/`（引擎契约的导出）是本地的，`ui/engine_export.go` 是本地的单文件补丁（给
  `VideoPlayer` 补 `Engine()`），`cmd/mygo/nsis.go` 把安装器的「应用正在运行」提示
  改成了 LangString。都有注释说明为什么。`internal/raster` 的重叠区像素修复
  上游 v0.10.0 已合，本地不再需要。
- `third_party/mpv/` 是 libmpv 运行库与头文件，运行时 `LoadLibrary` 加载，不是链接期依赖。
- `resources/icon.png` 要正方形：Windows 只取到 256，源图 ≥256 即可（全部缩小，不糊）；
  出 macOS/Linux 包要求 ≥1024，它们会渲染到 1024 和 512。
- `mygo.json` 是应用元信息（name / identifier / version / out）的唯一来源。
- `.agents/skills/` 是 MyGo 官方 skill，动 MyGo 相关代码前先读。

## 文档

**`doc/README.md` 是唯一入口。** 不在这个索引里的文档等于不存在。改代码前要读哪一篇，
那张「必读映射」表就在 `doc/README.md` 里——不在这里重复第二份。

各包的 `README.md` 只留导航和包内约定，事实写在 `doc/`。新增文档按主题落到
`doc/architecture/`、`doc/decisions/`、`doc/reference/`，并更新 `doc/README.md` 的索引。

声称"变快/变慢"之前先对 `doc/reference/measurements.md` 的基准；写新数字必须带日期和条件。

### 碰 MyGo 之前（强制）

`github.com/egoist/mygo` 是 replace 到 `third_party/mygo` 的本地上游（版本看 `go.mod`），
所以看的文档就是跑的代码；但它还在 0.x，API 在 minor 之间会变，记忆里的写法很可能属于旧
版本。写任何 MyGo 相关东西之前，按这个顺序取信息：

1. `.agents/skills/` 里对应的 skill：`mygo-maintenance`（生命周期、元素身份、线程、验证）、
   `mygo-native-modules`（原生模块）。驱动真实窗口的自动化没有仓库内 skill，指南用
   `mygo-automation skills` 取。
2. `third_party/mygo/docs/` 里对应的那一篇。控件索引是 `docs/ui/README.md`，
   **用到哪个控件读哪个的那一篇**：`list.md`、`scroll.md`、`popover.md`、`motion.md`、
   `performance.md`、`accessibility.md`……不要凭印象调方法。
3. 官方示例 `third_party/mygo/examples/`，以及 `third_party/mygo/ui/*_test.go` 里同一种用法的
   测试——两百多个测试文件，几乎每个 API 都有先例可抄。
4. 上面都没有，才读 `third_party/mygo/ui/` 的源码和注释，并在提交说明里写清为什么要看源码。

跳过这个顺序的代价很具体：写出不存在的方法、用错元素生命周期、或者把已经解决的问题
重新发明一遍——都发生过。

## 命令

播放走 libmpv（运行时 `LoadLibrary`，不是链接期依赖），所以**构建不需要 cgo、不需要
gcc、不需要 `CGO_ENABLED`**。`libmpv-2.dll` 的路径由 `Makefile` 统一给出。

```sh
make              # 打包：便携版 exe + NSIS 安装包，都进 build/<os>-<arch>/
make dev          # 开发模式：改 .go 自动重建重启
make check        # gofmt + build + vet + mygo vet + lint + test，一次跑完
make test         # 带 -tags mock
make lint         # golangci-lint，配置在 .golangci.yml
make run          # 从源码跑（ALIVE_MPV_LIB 已在 recipe 里）
make mock         # 不联网看 UI：假数据层，走 -tags mock
make e2e          # 带 automation 插件的构建，驱动真实窗口用
make mock-offline / tools / tidy / clean
```

`golangci-lint` 必须用**官方 release 二进制**，别用 `go install`：后者拿不到构建
元数据（`--version` 显示 `from (unknown…)`），官方文档 "Install from Sources" 一节
明确写这类安装 "aren't guaranteed to work"。配置在 `.golangci.yml`。

直接调 `go` / `go tool mygo` 而不是 make 时：

```sh
go tool mygo dev          # 运行库会自动放到开发二进制旁边
go tool mygo build        # 便携版 + 安装包
ALIVE_MPV_LIB=... go test ./...   # 见「测试与验证」
```

直接 `go run .` 也能编译运行，但二进制在临时目录、找不到 `libmpv-2.dll`，播放会直接
报错——没有兜底路径了。要看真实播放效果用 `make dev` 或 `make run`。

库的版本与来源见 `third_party/mpv/README.md`；换版本要同时换 DLL 与头文件，并重跑
`doc/reference/measurements.md` 的首帧对照。

## MyGo 生命周期（写 UI 必须遵守）

- 元素（`ui.Element`）和 `*ui.Context` 只在当前构建帧内有效，**不能存进 App 结构体**；只存数据、`ui.Handle`。
- `Children(func())` 共享 Context，只是切换父作用域。
- 视图每帧重建：副作用写在 `Clicked()` / `OnClick` 里，不要在渲染路径里无条件触发。
- 慢 I/O 和耗时计算放 goroutine，结果用 `win.Update(func(){...})` 回主线程；视图数据只在主线程读写。
- 动画用 `AnimateTo(key, target, curve)`，曲线取 `ui.Smooth` / `ui.Snappy`：两者都是弹簧，
  `Snappy` 多带一点回弹，用在需要「弹一下」的地方（岛伸缩、浮层出现）。
- 输入控件需要稳定身份时用 `c.Key(key)`，`Bind(&handle)` 只有真需要跨帧句柄时才用。

## 无障碍标签（强制）

**每一个可交互元素都必须有 `.Label()`**：按钮、tab、卡片、chip、图标按钮、下拉项、可点击的
行和胶囊。自绘 Box 不进无障碍树，没有 Label 的控件对读屏是隐形的，对 `mygo-automation` 也是
隐形的——只能靠坐标点，而坐标一改就失效。

- 名字用用户看得懂的中文，和屏幕上显示的文字一致；同一屏里不能重名。
- 有分类语义的加前缀：`分区:英雄联盟`、`分类:英雄联盟`、`平台:斗鱼`。
- 纯装饰的容器（布局 Box、分隔线、背景层）不加。
- 新增可点击控件时，先写 Label 再写 `Clicked()`。

Label 同时是**测试入口**：`tt.Click("弹幕设置")` 和 `mygo-automation press @e2` 都靠它。

**这条由测试兜底**：`internal/ui/label_test.go` 的 `TestEveryClickableHasLabel` 扫源码，凡是
链上有 `Cursor(ui.CursorPointer)` / `Cursor(ui.CursorText)` 或 `Clicked()` 的控件，没有
`.Label(...)` 就报错并给出行号。用静态检查而不是运行时断言，是因为 MyGo 的无障碍树只收
**有名字**的节点——没 Label 的元素根本不会出现在 `Tester.AccessAll()` 里，断言看不见。
`make check` 会跑到它，所以漏标一个 Label 提交不上去。

## 编码规范

- `gofmt`；包名小写单词；导出的类型和函数写清用途。
- 注释一律中文，解释「为什么」而不是复述代码；界面文案中文只作用于用户可见层，标识符保持英文。
- 需要局部常量时集中在文件顶部并注明来源，颜色、字号、圆角、间距仍走 `internal/theme`。
- 组件签名统一 `func (a *App) xxx(c *ui.Context)` 或 `func xxx(c *ui.Context, ...) ui.Element`。
- 一个文件一个界面职责。拆不拆看里面有没有两件互不相干的事，不看行数。

## 测试与验证

### 按改动选测试

| 改动 | 用什么 | 落在哪 |
|---|---|---|
| 纯逻辑：解析、缓存、过期、调度、状态机 | 普通 `go test`，表驱动 | 包旁边 `*_test.go` |
| 视图：布局、点击、输入、焦点、滚动、动画 | `ui.NewTester` | 同上 |
| 跨文件的规则：「凡是 X 都必须 Y」 | AST 静态测试（`label_test.go` 那种） | `internal/ui/*_test.go` |
| 真实窗口：真渲染、图片加载、真播放、内存 | `mygo-automation` | 手工跑，数字进 `doc/reference/measurements.md` |

- 能用 Tester 断言的事，不开窗口手工点：手工点不可重复，也进不了 `make check`。
- 「所有 X 都必须 Y」这类要求写成静态测试，不写成文档里的口头要求——文档会被跳过，
  `make check` 不会。
- 改 UI 前跑 `make check`（gofmt + build + vet + mygo vet + lint + test，一次跑完）。
- 单独调 `go test` 时要给 `ALIVE_MPV_LIB`（`third_party/mpv/bin/libmpv-2.dll` 的绝对路径）：
  测试二进制落在临时目录，找不到库就走不通播放，涉及播放的用例会直接失败。`make test`
  的 recipe 带上了，平时用它就行。
- 涉及视觉的改动必须截图：默认态一张，被改动的浮层/状态各一张。只看代码不算验证。
- 没有跑过的检查不能写成通过；无关的既有失败要如实说明。

### 探针不进仓库

要连公网、要真实流、跑不进 `make check` 的测试不写进 `internal/`——放 `.tmp/probes/<包名>/`
（`.gitignore` 挡着），需要时拷回对应包。`.gitignore` 另有 `*_probe_test.go` / `zz_*_test.go`
两条规则防回归。基准数字进 `doc/reference/measurements.md`，复现脚本留在 `.tmp/probes/`。

同理，验收开关不写进生产代码：能用 `ui.NewTester` 就用它，要真实窗口就用 `mygo-automation`，
不新增 `ALIVE_*` 这类启动开关（截图定位用的那几个已删）。假数据层也走构建标签：`internal/live`
的 mock 在 `-tags mock` 后面，`internal/ui` 的示例数据是 `mock_test.go`，正式二进制里两者都没有。

### Tester 的硬规矩

`third_party/mygo/docs/ui/testing.md` 是权威，下面是这个项目里最容易踩的几条：

- **时间只走 `tt.Advance(d)`**，测试里不 `time.Sleep`、不拿 `time.After` 决定通过与否。
  滚轮、动画、双击、tooltip、toast 全挂在 Tester 的时钟上，不推就不动——`TestWheelScrollsGrid`
  第一次跑出来列表纹丝不动，就是因为只 `Frame` 没 `Advance`。
- 从视图外面改了状态，先 `tt.Frame()` 再断言或 `Advance`。
- 等条件用带上限的轮询（`player_media_test.go` 的 `waitFor`）：上限只是把卡死变成失败，
  **永远不要为了让它过而调大**。超时说明有竞态要修，不是阈值不够。
- 找元素用 Label 和文本：`tt.Click("弹幕设置")`。所以 Label 不只是无障碍设施。
- 测试里不发真实请求：`a.Win == nil` 时不发请求、不留后台 goroutine。新写的数据路径照办。

### 新测试必须先红

写完一条测试，先在**改动前**的代码上跑一遍，确认它真的会失败。跑不红，就是断言写成了恒真。

这条是拿返工换来的：`ls.Key`、`resetListScroll`、`loadRooms` 里的 `ScrollTo` 三处「修复」，
逐个回滚之后测试仍然全绿——那几条测试根本没在测它们。同一次还删掉了
`TestCategorySwitchResetsScroll`：数据是 600→0→40，天然会重置，测试没有分辨力。

### 驱动真实窗口做验收

`ui.NewTester` 验不到真实渲染、滚动、图片加载、播放和内存。这些用 `make e2e` 起的带
automation 插件的构建，通过 socket 读元素树、点击、滚轮、截图——不需要人工盯着。

命令、这个项目踩过的坑（`--settle` 等不到、自绘元素读不出来、socket 路径、怎么收尾）都在
`doc/reference/acceptance.md`。**先跑 `mygo-automation skills`** 拿当前版本的手册。

## 提交与 PR

- 提交信息用 `type: 描述`，type 取 `feat` / `fix` / `docs` / `refactor` / `test` / `chore`，描述用中文。
- 分支命名 `feat/<主题>`、`fix/<主题>`、`chore/<主题>`；同一任务复用同一分支。
- 一次提交只做一件事，不混入无关文件；提交前看 diff、跑验证。
- 未经明确授权不改写历史、不强制推送、不丢弃改动。

## 安全与配置

- 不提交 cookie、token、证书、签名文件。
- 运行态数据写 `%APPDATA%\alive`，日志与缓存写 `%LOCALAPPDATA%\alive`。
- 平台接口节制轮询，触验证码就退避，不并发轰接口。
