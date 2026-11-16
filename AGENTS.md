# Repository Guidelines

alive 是一个直播客户端：Go 写全部逻辑，界面由 MyGo（`github.com/egoist/mygo`）用 GPU 直接画出来，没有 webview、没有前端构建。

## 设计令牌

所有视觉数值以 `internal/theme` 为唯一来源：

| 项 | 值 |
|---|---|
| 字号 | 11 / 12 / 13 / 15 / 18 / 24 / 32 |
| 圆角 | 8（控件）/ 12（卡片、面板）/ 16（大容器）/ 999（胶囊） |
| 间距 | 4px 基线：4 / 8 / 12 / 16 / 24 |
| 动效 | `--ease-out` = cubic-bezier(0.22,1,0.36,1)；110 / 190 / 280 / 420 / 620ms |
| 布局 | 顶栏 64 高、正文左右 14（≤1240 时 10）、卡片最小宽 220、网格 gap 16 |
| 卡片 | 封面 16:9；信息条 `padding 13 12 15 12`；头像 32；不再显示分类胶囊（卡片已属于该分区） |

配色六套：`signal`（默认）、`mono`、`ember`、`parchment`、`daylight`、`paper`。

## 工程结构（MyGo 原生约定）

```
main.go            窗口生命周期、启动参数、数据拉取
main_test.go       视图测试（ui.NewTester，不弹窗口）
mygo.json          name / identifier / version / out，应用元信息唯一来源
resources/         随包资源；icon.png 为应用图标，正方形。Windows 只取到 256，源图 ≥256 即可
                   （全部缩小，不糊）；出 macOS/Linux 包要求 ≥1024，它们会渲染到 1024 和 512
go.mod             MyGo CLI 作为 tool：go tool mygo
.agents/skills/    MyGo 官方 skill（维护约定、原生模块、自动化），动 MyGo 相关代码前先读
internal/ui/       界面：Shell、顶栏、分区栏、卡片、订阅页、播放页、搜索浮层、配色面板、灵动岛
internal/theme/    设计令牌与配色表
internal/media/    图片下载、解码与缓存
internal/live/     各平台数据层：分区、列表、搜索、取流、弹幕，归一化成同一套类型
internal/playback/ 播放引擎：薄壳。把 URL 与回源 headers 交给 libmpv，把 bgr0 帧包成 media.Picture
doc/               文档唯一入口：架构、决策、实测。从 doc/README.md 进
third_party/mygo/  上游源码的 replace。除 engines 契约的导出，只改了 plugins/video（播放）与
                   internal/raster 的一处（软件光栅化采样前先取回像素），都有注释说明为什么
third_party/mpv/   libmpv 运行库与头文件。运行时 LoadLibrary 加载，不是链接期依赖
```

顶层目录只有这一个例外：`doc/` 是文档目录。新功能按职责落到 `internal/<领域>`；
界面代码只写在 `internal/ui`，协议代码不进 `internal/ui`。

## 文档

**`doc/README.md` 是唯一入口。** 不在这个索引里的文档等于不存在。

改下面这些代码之前，先读对应文档——这是流程，不是建议：

| 要动的代码 | 必读 |
|---|---|
| `internal/playback/*` | `doc/architecture/playback.md`、`doc/decisions/0004-libmpv.md` |
| `third_party/mygo/plugins/video/*` | `doc/decisions/0004-libmpv.md`、`third_party/mpv/README.md` |
| `third_party/mygo/internal/raster/*` | `doc/architecture/playback.md` 的「渲染」一节 |
| `internal/live/*` | `doc/architecture/platform.md` |
| `internal/ui/player_media.go` | `doc/architecture/playback.md` |
| `internal/ffmpeg/*` | `doc/decisions/0002-ffmpeg-provisioning.md` |

各包的 `README.md` 只留导航和包内约定，事实写在 `doc/`。新增文档按主题落到
`doc/architecture/`、`doc/decisions/`、`doc/reference/`，并更新 `doc/README.md` 的索引。

声称"变快/变慢"之前先对 `doc/reference/measurements.md` 的基准；写新数字必须带日期和条件。

### 碰 MyGo 之前（强制）

`github.com/egoist/mygo` 是 replace 到 `third_party/mygo` 的本地上游（版本看 `go.mod`），所以看的文档就是
跑的代码；但它还在 0.x，API 在 minor 之间会变，记忆里的写法很可能属于旧版本。写任何
MyGo 相关东西之前，按这个顺序取信息：

1. `.agents/skills/` 里对应的 skill：`mygo-maintenance`（生命周期、元素身份、线程、验证）、
   `mygo-native-modules`（原生模块）、`mygo-automation`（驱动真实窗口）。
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
make check        # gofmt + build + vet + mygo vet + test，一次跑完
make test
make run          # 从源码跑（ALIVE_MPV_LIB 已在 recipe 里）
make mock / mock-offline / probe / e2e / tools / tidy / clean
```

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

截图验收（要改 `ALIVE_*` 环境变量时用 pwsh，不要用 Git Bash —— 它会改写以 `/` 开头的值）：

```pwsh
$env:ALIVE_ROUTE="subscribe"; $env:ALIVE_UI="palette"
$env:ALIVE_CAPTURE="D:\tmp\alive.png"; $env:ALIVE_CAPTURE_DELAY="2s"
make run   # 不要用 go run .：ALIVE_MPV_LIB 由 Makefile 的 recipe 传进去
```

`ALIVE_ROUTE` 取 `home` / `subscribe` / `player`；`ALIVE_UI` 取 `palette` / `island` / `search` / `quality` / `danmu` / `sync` / `splash` / `nocover`；`ALIVE_WATCH=2h` 预置播放页的观看时长（验小时级的岛收起态）；`ALIVE_CAPTURE_KEEP=1` 截完不退出。

## MyGo 生命周期（写 UI 必须遵守）

- 元素（`ui.Element`）和 `*ui.Context` 只在当前构建帧内有效，**不能存进 App 结构体**；只存数据、`ui.Handle`。
- `Children(func())` 共享 Context，只是切换父作用域。
- 视图每帧重建：副作用写在 `Clicked()` / `OnClick` 里，不要在渲染路径里无条件触发。
- 慢 I/O 和耗时计算放 goroutine，结果用 `win.Update(func(){...})` 回主线程；视图数据只在主线程读写。
- 动画用 `AnimateTo(key, target, curve)`，曲线取 `ui.Smooth` / `ui.Snappy`：两者都是弹簧，`Snappy` 多带一点回弹，用在需要「弹一下」的地方（岛伸缩、浮层出现）。
- 输入控件需要稳定身份时用 `c.Key(key)`，`Bind(&handle)` 只有真需要跨帧句柄时才用。

## 无障碍标签（强制）

**每一个可交互元素都必须有 `.Label()`**：按钮、tab、卡片、chip、图标按钮、下拉项、
可点击的行和胶囊。自绘 Box 不进无障碍树，没有 Label 的控件对读屏是隐形的，
对 `mygo-automation` 也是隐形的——只能靠坐标点，而坐标一改就失效。

- 名字用用户看得懂的中文，和屏幕上显示的文字一致；同一屏里不能重名。
- 有分类语义的加上前缀，便于定位：`分区:英雄联盟`、`分类:英雄联盟`、`平台:斗鱼`。
- 纯装饰的容器（布局 Box、分隔线、背景层）不加。
- 新增一个可点击控件时，先写 Label 再写 `Clicked()`。

Label 同时是**测试入口**：`tt.Click("弹幕设置")`、`mygo-automation press @e2`、
`tt.AccessByRole(...)` 全靠它。没有 Label 的控件在测试和自动化里等于不存在。

**这条由测试兜底**：`internal/ui/label_test.go` 的 `TestEveryClickableHasLabel`
扫 `internal/ui` 的源码，凡是链上有 `Cursor(ui.CursorPointer)` / `Cursor(ui.CursorText)`
或 `Clicked()` 的控件，没有 `.Label(...)` 就报错并给出行号。

为什么是静态检查而不是运行时断言：MyGo 的无障碍树只收**有名字**的节点，没 Label
的元素根本不会出现在 `Tester.AccessAll()` 里——运行时断言看不见它们。

`make check` 会跑到它，所以漏标一个 Label 提交不上去。

## 编码规范

- `gofmt`；包名小写单词；导出的类型和函数写清用途。
- 注释一律中文，解释「为什么」而不是复述代码；界面文案中文只作用于用户可见层，标识符保持英文。
- 颜色、字号、圆角、间距一律走 `internal/theme`，不在 `internal/ui` 里写字面量；确实需要局部常量时集中在文件顶部并注明来源。
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
- 改 UI 前跑 `make check`（gofmt + build + vet + mygo vet + test，一次跑完）。
- 单独调 `go test` 时要给 `ALIVE_MPV_LIB`（`third_party/mpv/bin/libmpv-2.dll` 的绝对
  路径）：测试二进制落在临时目录，找不到库就走不通播放，`TestMatrix` / `TestLiveEngine`
  会直接失败。`make test` 的 recipe 已经带上了，平时用它就行。
- 涉及视觉的改动必须截图：默认态一张，被改动的浮层/状态各一张。只看代码不算验证。
- 没有跑过的检查不能写成通过；无关的既有失败要如实说明。

### Tester 的硬规矩

`third_party/mygo/docs/ui/testing.md` 是权威，下面是这个项目里最容易踩的几条：

- **时间只走 `tt.Advance(d)`**，测试里不 `time.Sleep`、不拿 `time.After` 决定通过与否。
  滚轮、动画、双击、tooltip、toast 全挂在 Tester 的时钟上，不推就不动——`TestWheelScrollsGrid`
  第一次跑出来列表纹丝不动，就是因为只 `Frame` 没 `Advance`。
- 从视图外面改了状态，先 `tt.Frame()` 再断言或 `Advance`。
- 等条件用带上限的轮询（`player_media_test.go` 的 `waitFor`）：上限只是把卡死变成失败，
  **永远不要为了让它过而调大**。超时说明有竞态要修，不是阈值不够。
- 找元素用 Label 和文本：`tt.Click("弹幕设置")`。所以 Label 不只是无障碍设施，见上一节。
- 测试里不发真实请求：`a.Win == nil` 时不发请求、不留后台 goroutine。新写的数据路径照办。

### 新测试必须先红

写完一条测试，先在**改动前**的代码上跑一遍，确认它真的会失败。跑不红，就是断言写成了恒真。

这条是拿返工换来的：`ls.Key`、`resetListScroll`、`loadRooms` 里的 `ScrollTo` 三处「修复」，
逐个回滚之后测试仍然全绿——那几条测试根本没在测它们。同一次还删掉了
`TestCategorySwitchResetsScroll`：数据是 600→0→40，天然会重置，测试没有分辨力。

### 驱动真实窗口做验收（mygo-automation）

`ui.NewTester` 验不到真实渲染、滚动、图片加载、播放和内存。这些用 MyGo 的自动化插件，
通过 socket 读运行中窗口的元素树、点击、滚轮、截图——不需要人工盯着。

**先跑 `mygo-automation skills`**：那是当前安装版本的完整指南（命令、ref、session、settle、
diff、错误码）。下面是这个项目常用的那一小撮，别拿它当手册去猜参数。命令版本要跟 app 的
MyGo 一致，升级 MyGo 之后在模块目录里重装一次。

```sh
go install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation

go build -tags e2e -o /d/tmp/alive-e2e.exe .        # automation_e2e.go 只在 e2e 构建里存在
MYGO_AUTOMATION=/d/tmp/alive-e2e.sock /d/tmp/alive-e2e.exe &
mygo-automation open --socket /d/tmp/alive-e2e.sock --session alive-e2e

mygo-automation snapshot -i                  # 带 ref 的元素列表，元素太多就 | head
mygo-automation snapshot --full | grep 斗鱼   # 带 bounds，坐标从这儿拿
mygo-automation press @e2                    # 按 ref 点，不用坐标
mygo-automation fill @e3 "英雄联盟"            # 填输入框，不用先点
mygo-automation wait --text "已显示最近"       # 等内容出现，不要 sleep
mygo-automation wheel 700 700 0 3000         # 滚动，单位 DIP，dy 正数向下
mygo-automation screenshot /d/tmp/x.png
mygo-automation close                        # 只断会话，app 还在跑
taskkill //F //IM alive-e2e.exe              # 收尾
```

几条必须知道的：

- **`--settle` 在本应用里基本等不到**：灵动岛有常驻动画，窗口一直请求帧，settle 会一路
  超时到 `--timeout`。等具体内容用 `wait --text` / `wait <sel>` / `--gone`，**`wait` 后面也别加
  `--settle`**；只想看当前状态就直接 `snapshot`。**任何时候都不要 `sleep` 再截图**。
- **优先 `press` / `fill`，不要 `click x y` / `type`**：ref 不依赖坐标，布局一动坐标就失效；
  ref 失效会明确报 `stale ref`，不会点错东西。
- **自绘元素读不出来**：只有带 Role/Label 的元素进无障碍树。卡片、按钮都是自绘 Box，
  snapshot 里没有，只能按坐标 `click`——给控件补 `.Label()` 才能用 ref 驱动。
- **一个 agent 一个 session**（`--session <名字>`）：ref 存在各自的文件里，互不干扰；
  同一个 session 同时只跑一条命令。
- 右键菜单别用 `click --button right` 开：插件选不了系统菜单。测右键菜单用 Tester 的
  `RightClick` / `ChooseMenuItem`。
- socket 放只有自己能写的目录，路径带上应用名（`alive-e2e.sock`）：共用路径会连到别人的 app，
  上一轮就是这么踩到的。
- 坐标是 DIP，从内容区左上角算，和 `--full` 的 `bounds=` 同一套。
- 量内存：`pwsh -NoProfile -Command 'Get-Process alive-e2e | Select-Object -ExpandProperty WorkingSet64'`。
- 收尾要真的收：`close` 只断会话，app 还在跑；再 `taskkill` 掉自己起的那个 exe，不留后台进程和 socket。

## 提交与 PR

- 提交信息用 `type: 描述`，type 取 `feat` / `fix` / `docs` / `refactor` / `test` / `chore`，描述用中文。
- 分支命名 `feat/<主题>`、`fix/<主题>`、`chore/<主题>`；同一任务复用同一分支。
- 一次提交只做一件事，不混入无关文件；提交前看 diff、跑验证。
- 未经明确授权不改写历史、不强制推送、不丢弃改动。

## 安全与配置

- 不提交 cookie、token、证书、签名文件。
- 运行态数据写 `%APPDATA%\alive`，日志与缓存写 `%LOCALAPPDATA%\alive`。
- 平台接口节制轮询，触验证码就退避，不并发轰接口。
