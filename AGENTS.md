# AGENTS.md

ALIVE 是 Windows 上的直播桌面客户端。界面是 MyGo 的原生 UI（D3D11 自绘，没有 webview），
播放是 libmpv 的 DLL 经 purego 加载（没有 cgo）。入口 `main.go`，窗口内容是 `App.Shell`。
产品怎么用、界面长什么样，看 [README.md](README.md)；这份文件只管"把活干对"。

## 先读什么

动手前按需读，不要凭记忆：

- [third_party/mygo/AGENTS.md](third_party/mygo/AGENTS.md) —— 框架的硬规则：无 cgo、线程模型、
  purego 回调、只为用到的功能付费。动任何 UI 代码前先读它。
- `.agents/skills/mygo-maintenance/SKILL.md` —— MyGo 官方给应用维护者的技能（生命周期、身份、
  线程、测试、验证）。**改代码或写测试前先读它。** 技能随 CLI 版本走，`go tool mygo install-skills .` 更新。
- `third_party/mygo/docs/ui/views.md` —— 构建生命周期、事件、Key、跨 goroutine。
- `third_party/mygo/docs/ui/migration.md` —— Element 值、Handle、FocusBind。
- `third_party/mygo/docs/ui/testing.md` —— Tester。真窗口 e2e 看 `third_party/mygo/docs/plugins/automation.md`。
- 某个 API 在钉住的版本（v0.10.0，见 `go.mod`）里存不存在、签名是什么：读 `third_party/mygo/ui/` 源码或 `go doc`。

**现有代码不是标准。** 仓库里带着不合下面规矩的遗留，账在 [doc/known-debt.md](doc/known-debt.md)。
改到哪改对哪，别拿"代码里本来就是这样"当依据。

**`third_party/mygo` 不是上游原样。** 它被改过：5 个文件改、20 个文件新增（引擎契约导出、
GPU 帧回读、mpv 播放引擎、安装器文案）。逐文件的原因和升级办法在
[doc/reference/mygo.md](doc/reference/mygo.md)。

## 命令

全在 `Makefile` 里，`make help` 列全。天天用的是这些：

```sh
make run        # 从源码跑，沙箱在 .mygo\dev-home，真实 %APPDATA% 一个字节不写
make dev        # 同上，改文件自动重建
make mock       # 假数据层（直播 + 弹幕），不联网
make build      # mygo build 打包到 build\
make deps       # 按 sha256 取 libmpv-2.dll（116MB，不入库）

make test       # go test -tags mock，跑前清 .mygo\test-home
make lint       # golangci-lint run --build-tags mock
make vet        # go build + go vet + go tool mygo vet
make check      # fmt-check + vet + lint + test + agents-check
make e2e        # 真构建（-tags e2e），automation 插件跟着进去
```

提交前跑 `make check`。它和 [.github/workflows/check.yml](.github/workflows/check.yml) 一一对应，
改一边就同步另一边。

`go tool mygo` 的子命令也会用到：`vet .`（查构建值逃逸）、`migrate-ui .`（旧元素 API 迁移，
预览；加 `-write` 应用）、`install-skills .`（更新技能）、`doctor`（环境体检）。

## 目录

| 放什么 | 在哪 |
|---|---|
| 入口、窗口、生命周期 | `main.go` |
| 四平台协议（分区、列表、搜索、取流、弹幕） | `internal/live` |
| mpv 播放薄壳 | `internal/playback` |
| 视图（全部界面） | `internal/ui` |
| 视觉令牌（颜色、字号、圆角、间距、动效） | `internal/theme` |
| 图片缓存与解码 | `internal/media` |
| 日志、指标、脱敏导出、主线程卡死观察 | `internal/applog`、`internal/metrics`、`internal/support`、`internal/hang` |
| 生成物 | `build/`（不入库） |
| 沙箱：`APPDATA`/`LOCALAPPDATA` 被指到这里 | `.mygo/`（不入库） |
| 探针（要真网络/真流的实验）、e2e 证据 | `.tmp/probes/`、`.tmp/e2e/`（不入库） |

新代码进对应的域。`internal/ui` 只依赖 `internal/theme` 和 `mygo/ui`，不依赖协议。

## 怎么写代码

按 MyGo 的方式。正文里的每条规矩，一手的原文都在下面的 §出处 里。

- **构建值是临时的。** 视图在主线程上按状态建帧，一帧可能构建多次。`ui.Element`、`ui.Context`、
  各控件的 `*Parts` 只能当参数或返回值——不许进结构体字段、包变量、goroutine。元素不存在用
  `ui.Element{}` 加 `Valid()` 判，不要用 `recover` 盖过期元素的 panic。持久的只有三样：
  应用自己的数据、`ui.Handle`（控件身份）、`ui.Services`（跨构建回调）。
- **身份用 Key 给，不用位置。** 有状态的控件在建之前给：`ui.Column(c.Key("feed"))`、
  `ui.TextInput(c.Key("q"), &q)`；容器的 Key 必须在 children 之前。列表用 `ItemKey`；
  行内样式读 `ListRow` 的 `Selected`/`Hovered`，别在行内子元素上读 `Hovered()`——点击会被行吃掉。
- **输入先写回，再决定。** 绑指针的控件在构造函数返回前就把编辑写进指针，下一句就能读到新值；
  `Changed`/`Submitted` 报告这一趟变了。`Clicked()` 用于内联控制流；会改正在构建的那个集合时，
  用 `OnClick`/`OnChange`/`OnSubmit` 推迟到本帧构建之后。框架已保证一次输入只触发一次——
  别再用时钟窗口自己给输入去重，那只会吞掉真实的第二次按键。
- **跨 goroutine 只走 `Window.Update`。** 只想重绘用 `Invalidate`；轮询少变数据用 `mygo.RunOnMain`
  在主线程比较，变了才 `Invalidate`。慢 I/O 一律离开 UI 线程：构建路径里不放 `time.Sleep`、网络、文件。
  窗口关掉以后不要再往它 `Update`。
- **谁起的活谁收。** ticker、订阅、流式请求用 `ui.Local`（值实现 `io.Closer` 就自动关）或
  `OnUnmount` 绑在元素上。只跑一次的函数交给 `OnUnmount`。绘制回调只画，不做副作用——它会被反复调用。
- **线程模型是框架的，不是可选的。** 主 goroutine 锁在主线程，`App.Run` 必须从它调；后端方法和
  事件回调都在主线程跑；公开方法必须能从任意 goroutine 安全调用；绝不让主线程等一个需要它自己的活。
- **purego 回调建一次。** 每种签名启动时建一次，用 user data 路由，不按窗口/请求/调用新建——
  它们永不释放。我们的原生边界是 `third_party/mygo/plugins/video` 调 libmpv，照这条写。
  常走路径别碰 `reflect` 的方法查找。
- **长集合用虚拟化控件。** `List`、`Table`、`GridView`、`Outline` 只建可见的行；`Scroll` 每帧全建。
  hover 只在 `Draw` 里面读。

本项目另外定的（不是 MyGo 的规定）：视觉令牌只有一个来源，就是 `internal/theme`——`internal/ui`
里不写颜色字面量；协议代码不进 `internal/ui`；错误要能分类（`ErrUnsupported`、`ErrBlocked`、
`ErrAuth`），用户可见的失败是状态不是日志；cookie/token/签名不进日志、截图、诊断包；
`internal/playback` 是薄壳，只做 headers、尺寸、alpha。

## 怎么写测试

测试在 `internal/ui`、`internal/diag`、`internal/theme` 里，`make test` 跑它们（`-tags mock`）。新写的按这里来。

- 视图测试用 `ui.NewTester(view, w, h)`；要初始偏好或深色，用 `ui.NewTesterWith(view, w, h, ui.TesterOptions{...})`。
- 按用户看得见的东西断言：`tt.Click`、`tt.Find`、`tt.HasText`，元素按文本或 `Label` 找。
- **时间只走 `tt.Advance`**：不 sleep、不按挂钟判过不过。外部改了状态先 `tt.Frame()` 再 `Advance`。
- 无障碍是断言面，不是附加项：`tt.AccessByRole`、`tt.AccessAll`、`tt.Announcements`。
  所以按钮必须有可读名字，角色和状态要真的发出去。
- 真窗口 e2e：`make e2e` 出的构建里带着 automation 插件（只在 `automation_e2e.go` 的
  `//go:build e2e` 里 import），用 `MYGO_AUTOMATION=<socket 路径>` 起它，再用
  `mygo-automation open` → `snapshot -i` → `press '@eN'` / `fill` → `screenshot` → `close` 驱动。
  直播页每帧人气和弹幕都在变，`--settle` 永远等不到——给 `--timeout`，关键断言改用
  `wait --text` 或反复 `snapshot`。PowerShell 里 ref 要写 `'@e4'`，不加引号会被当 splatting 吃掉。

```sh
go test -tags mock ./... -count=1       # 单测；`make test` 还会先清空 .mygo\test-home
CGO_ENABLED=1 go test ./... -race       # -race 自身要 cgo，不能同时 CGO_ENABLED=0
```

## 边界

- **别改 `third_party/mygo` 的补丁而不知道自己在改什么。** 先读 `doc/reference/mygo.md`。
  升级 MyGo 时那 25 个文件要一起核对。`go tool mygo migrate-ui .` 在 third_party 里还会列
  14 个上游文件——那是上游的账，别顺手修。
- **别引依赖来解决框架已有的问题**：先看 `third_party/mygo/ui/` 有没有现成控件。
- **别把现有写法当范本**：不合下面这些规矩的地方，账记在 [doc/known-debt.md](doc/known-debt.md)。
  照抄之前先看它是不是还没改的那一条。
- **密钥不进日志、截图、诊断包**；诊断包只打包已经脱敏的产物。
- **只有 Windows 是目标平台。** 别为 macOS/Linux 写分支，也别引 cgo。

## 提交与交付

- 一任务一分支：`feat/`、`fix/`、`docs/`、`chore/` 加主题，同一任务复用一条。不强推、不改写历史、不丢别人的改动。
- 提交信息 `type: 中文`（feat / fix / docs / refactor / test / chore）；不写 `[skip ci]`。
  `.githooks/pre-commit` 拦没 gofmt 的 `.go`，`.githooks/commit-msg` 限 `fix:` 一次不超过 3 个文件。
- 多个 agent 并行时写作用域不相交，同一文件同一时刻只有一个写者；命令、格式化、代码生成器会
  绕过冲突检测，集成方必须复核最终 `git diff` 并补跑 `make check`。
- 交付时说清五件事：改了什么；依据哪份一手材料（文件:行 / 技能条目）；证据在哪（日志、截图、
  `snapshot`）；跑了哪些检查；没跑什么、为什么。"通过了"要能被别人用同一条命令复现。

## 出处

上面每条规矩的一手来源。`make agents-check` 逐行验：文件在、行号在文件里、引号里的话就在那几行；
左栏那句必须还出现在正文里，所以表和正文不可能走散。

| 正文里的规矩 | 一手出处 |
|---|---|
| 构建值是临时的 | `third_party/mygo/docs/ui/views.md:12「Each ui.Element value belongs to one build pass.」` |
| 不许进结构体字段、包变量、goroutine | `third_party/mygo/docs/ui/views.md:18「Keep build values out of app state and background work.」` |
| 元素不存在用 ui.Element{} | `third_party/mygo/docs/ui/migration.md:38「Use ui.Element{} instead of assigning or returning nil.」` |
| 不要用 `recover` 盖过期元素的 panic | `.agents/skills/mygo-maintenance/SKILL.md:15「Constructors return checked ui.Element values.」` |
| 身份用 Key 给 | `third_party/mygo/docs/ui/views.md:152「Give a control its key before construction」` |
| 有状态的控件在建之前给 | `third_party/mygo/docs/ui/migration.md:57-58「Use Context.Key for stateful widgets, including custom base controls.」` |
| 持久的只有三样 | `third_party/mygo/docs/ui/migration.md:99「Store ui.Handle instead of an element」` |
| 行内样式读 ListRow | `third_party/mygo/docs/ui/migration.md:176-177「ListRow supplies the shared Context, index, selection, list focus」` |
| 用 OnClick/OnChange/OnSubmit 推迟 | `third_party/mygo/docs/ui/views.md:81-82「Use callbacks for actions that modify the collection being built」` |
| 跨 goroutine 只走 Window.Update | `third_party/mygo/docs/ui/views.md:185-186「Change it from other goroutines with Window.Update」` |
| 只想重绘用 Invalidate | `third_party/mygo/docs/ui/views.md:197「Window.Invalidate only draws a new frame」` |
| 慢 I/O 一律离开 UI 线程 | `.agents/skills/mygo-maintenance/SKILL.md:30「Keep slow I/O and expensive computation off the UI thread.」` |
| 谁起的活谁收 | `third_party/mygo/docs/ui/views.md:222-223「closes when the element is no longer built」` |
| 只跑一次的函数交给 OnUnmount | `third_party/mygo/docs/ui/views.md:226「A view that only needs to run a function as an element goes」` |
| 绘制回调只画 | `.agents/skills/mygo-maintenance/SKILL.md:31「Keep drawing callbacks limited to painting」` |
| 主 goroutine 锁在主线程 | `third_party/mygo/docs/architecture.md:216「The main goroutine is locked to the main thread」` |
| 公开方法必须能从任意 goroutine 安全调用 | `third_party/mygo/docs/architecture.md:233「Every public method is safe from any goroutine.」` |
| 后端方法和事件回调都在主线程跑 | `third_party/mygo/AGENTS.md:38「Backend methods and handler callbacks run on the main thread.」` |
| purego 回调建一次 | `third_party/mygo/AGENTS.md:41「purego callbacks are a scarce, never-freed resource.」` |
| 不按窗口/请求/调用新建 | `third_party/mygo/docs/architecture.md:286-287「Never create callbacks per window, per request or per call.」` |
| 常走路径别碰 reflect 的方法查找 | `third_party/mygo/AGENTS.md:49-50「Never call reflect.Type.Method, Value.Method or MethodByName there」` |
| List、Table、GridView、Outline 只建可见的行 | `third_party/mygo/docs/ui/performance.md:51「List and Table build only the rows in view」` |
| Scroll 每帧全建 | `third_party/mygo/docs/ui/performance.md:52-53「Put long collections in them rather than in a Scroll that builds every row.」` |
| hover 只在 Draw 里面读 | `third_party/mygo/docs/ui/performance.md:62「read Hovered in its Draw callback instead」` |
| 视觉令牌只有一个来源 | `internal/theme/theme.go:1「Package theme 是全部视觉令牌的唯一来源」` |
| 协议代码不进 internal/ui | `internal/live/README.md:29「协议代码不进 internal/ui。」` |
| 错误要能分类 | `internal/playback/engine.go:33「ErrUnsupported —— 这一条流解不了」` |
| 诊断包只打包已经脱敏的产物 | `internal/support/bundle.go:3「它只打包已经脱敏的产物，绝不读取 state.json 或登录态文件」` |
| internal/playback 是薄壳 | `internal/playback/README.md:3「这一层是薄壳：真正的播放全在 mpv 里」` |
| 视图测试用 ui.NewTester | `third_party/mygo/docs/ui/testing.md:3「ui.NewTester runs a view without a window」` |
| 不 sleep、不按挂钟判过不过 | `third_party/mygo/docs/ui/testing.md:101-102「never sleep to let something happen, advance the clock」` |
| 时间只走 tt.Advance | `third_party/mygo/docs/ui/testing.md:94「A tester has a clock of its own」`；`third_party/mygo/AGENTS.md:212「Tests never pass or fail on wall-clock time」` |
| 无障碍是断言面 | `third_party/mygo/docs/ui/testing.md:139「tt.AccessByRole(role, name) returns what screen readers get」` |
| automation 插件 | `third_party/mygo/docs/plugins/automation.md:23-24「Keep it out of the app's release with a build tag」` |
| MYGO_AUTOMATION=<socket 路径> | `third_party/mygo/docs/plugins/automation.md:39「It listens only when MYGO_AUTOMATION names the path of the socket」` |



