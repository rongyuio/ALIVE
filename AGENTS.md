# Repository Guidelines

alive 是一个直播客户端：Go 写全部逻辑，界面由 MyGo（`github.com/egoist/mygo`）用 GPU 直接画出来，没有 webview、没有前端构建。它是 `D:\Project\DTV` 的 `baseline` 分支（Go + React 的 webview 版）的原生重写——**同一套界面、同一套交互，换一套实现**。

## 视觉与交互基准

UI 视觉效果与交互必须与 DTV baseline 一致，所有改动以它为唯一标准。

- 基准仓库：`D:\Project\DTV`，分支 `baseline`。
- 对照文件：`src/styles/theme.css`（设计令牌）、`src/styles/base.css`（全局约定）、`src/components/shell/*`（顶栏、平台 tab、搜索、关注面板、窗口按钮）、`src/components/categories/CategoryBar*`（分区栏）、`src/components/streamers/*`（卡片与网格）、`src/screens/*`（首页、订阅、播放）。
- 细节以 baseline 的 CSS 实际值为准，不凭印象；CSS 里带 `font-size`、`gap`、`padding`、`transition` 的地方都要照抄数值，不四舍五入。

固定令牌（来自 `theme.css`，Go 侧在 `internal/theme`）：

| 项 | 值 |
|---|---|
| 字号 | 11 / 12 / 13 / 15 / 18 / 24 / 32 |
| 圆角 | 8（控件）/ 12（卡片、面板）/ 16（大容器）/ 999（胶囊） |
| 间距 | 4px 基线：4 / 8 / 12 / 16 / 24 |
| 动效 | `--ease-out` = cubic-bezier(0.22,1,0.36,1)；110 / 190 / 280 / 420 / 620ms |
| 布局 | 顶栏 64 高、正文左右 14（≤1240 时 10）、卡片最小宽 220、网格 gap 16 |
| 卡片 | 封面 16:9；信息条 `padding 13 12 15 12`；头像 32；分类胶囊在卡片宽 < 260 时隐藏 |

配色六套：`signal`（默认）、`mono`、`ember`、`parchment`、`daylight`、`paper`。

## 工程结构（MyGo 原生约定）

```
main.go            窗口生命周期、启动参数、数据拉取
main_test.go       视图测试（ui.NewTester，不弹窗口）
mygo.json          name / identifier / version / out，应用元信息唯一来源
resources/         随包资源；icon.png 为应用图标（当前是占位图，替换时保持 1024×1024）
go.mod             MyGo CLI 作为 tool：go tool mygo
.agents/skills/    MyGo 官方 skill（维护约定、原生模块），动 MyGo 相关代码前先读
internal/ui/       界面：Shell、顶栏、分区栏、卡片、订阅页、播放页、搜索浮层、配色面板、灵动岛
internal/theme/    设计令牌与配色表，对齐 baseline 的 theme.css
internal/media/    图片下载、解码与缓存
internal/bili/     B 站协议（推荐列表、分区名）
internal/relay/    ffmpeg 转封装成本地 HLS，供播放器使用
```

不新增顶层目录。新功能按职责落到 `internal/<领域>`；界面代码只写在 `internal/ui`，协议代码不进 `internal/ui`。

## 命令

```sh
go run .                  # 直接运行
go tool mygo dev          # 开发模式，改 .go 自动重建重启
go tool mygo build        # 打包到 build/<os>-<arch>/
go build ./...            # 编译检查
go vet ./...              # 静态检查
go tool mygo vet .        # MyGo 专属检查：跨帧保存的元素、goroutine 捕获
go test ./...             # 视图测试
```

截图验收（要改 `ALIVE_*` 环境变量时用 pwsh，不要用 Git Bash —— 它会改写以 `/` 开头的值）：

```pwsh
$env:ALIVE_ROUTE="subscribe"; $env:ALIVE_UI="palette"
$env:ALIVE_CAPTURE="D:\tmp\alive.png"; $env:ALIVE_CAPTURE_DELAY="2s"
go run .
```

`ALIVE_ROUTE` 取 `home` / `subscribe` / `player`；`ALIVE_UI` 取 `palette` / `island` / `search`；`ALIVE_CAPTURE_KEEP=1` 截完不退出。

## MyGo 生命周期（写 UI 必须遵守）

- 元素（`ui.Element`）和 `*ui.Context` 只在当前构建帧内有效，**不能存进 App 结构体**；只存数据、`ui.Handle`。
- `Children(func())` 共享 Context，只是切换父作用域。
- 视图每帧重建：副作用写在 `Clicked()` / `OnClick` 里，不要在渲染路径里无条件触发。
- 慢 I/O 和耗时计算放 goroutine，结果用 `win.Update(func(){...})` 回主线程；视图数据只在主线程读写。
- 动画用 `AnimateTo(key, target, curve)`，曲线取 `ui.Smooth` / `ui.Snappy`，对应 baseline 的 `--ease-out` / 弹簧。
- 输入控件需要稳定身份时用 `c.Key(key)`，`Bind(&handle)` 只有真需要跨帧句柄时才用。

## 编码规范

- `gofmt`；包名小写单词；导出的类型和函数写清用途。
- 注释一律中文，解释「为什么」而不是复述代码；界面文案中文只作用于用户可见层，标识符保持英文。
- 颜色、字号、圆角、间距一律走 `internal/theme`，不在 `internal/ui` 里写字面量；确实需要局部常量时集中在文件顶部并注明来源。
- 组件签名统一 `func (a *App) xxx(c *ui.Context)` 或 `func xxx(c *ui.Context, ...) ui.Element`。
- 一个文件一个界面职责，超过约 600 行先拆。

## 测试与验证

- 视图测试用 `ui.NewTester(view, w, h)`，在内存里点击、输入、按键；测试文件放包旁边，命名 `*_test.go`。
- 改 UI 前跑 `go build ./...`、`go vet ./...`、`go tool mygo vet .`、`go test ./...`。
- 涉及视觉的改动必须截图对照 baseline：默认态一张，被改动的浮层/状态各一张。只看代码不算验证。
- 没有跑过的检查不能写成通过；无关的既有失败要如实说明。

### 驱动真实窗口做验收（mygo-automation）

`ui.NewTester` 验不到真实渲染、滚动、图片加载和内存。这些用 MyGo 的自动化插件，
通过 socket 读运行中窗口的元素树、点击、滚轮、截图——不需要人工盯着。

```sh
go install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation

go build -tags e2e -o /d/tmp/alive-e2e.exe .        # automation_e2e.go 只在 e2e 构建里存在
MYGO_AUTOMATION=/d/tmp/alive.sock /d/tmp/alive-e2e.exe &
mygo-automation open --socket /d/tmp/alive.sock

mygo-automation snapshot                     # UI 元素树，元素太多就 | head
mygo-automation snapshot --full | grep 斗鱼   # 带 bounds，坐标从这儿拿
mygo-automation wheel 700 700 0 3000         # 滚动，单位 DIP，dy 正数向下
mygo-automation click 177 32                 # 点平台 tab
mygo-automation wait --text "已显示最近"       # 等文案出现
mygo-automation screenshot /d/tmp/x.png
mygo-automation close                        # 只断会话，app 还在跑
taskkill //F //IM alive-e2e.exe              # 收尾
```

几个必须知道的点：

- **`--settle` 在本应用里基本等不到**：灵动岛有常驻动画，窗口一直请求帧，
  settle 会一路超时到 `--timeout`。要等具体内容用 `wait --text` / `wait <sel>`，
  或者干脆 `snapshot` 看当前状态。
- **自绘元素读不出来**：只有带 Role/Label 的元素进无障碍树。卡片、按钮都是
  自绘 Box，snapshot 里没有，只能按坐标 `click`。给控件补 `.Label()` 才会出现。
- 坐标是 DIP，从内容区左上角算，和 `--full` 的 `bounds=` 同一套。
- 量内存：`pwsh -NoProfile -Command 'Get-Process alive-e2e | Select-Object -ExpandProperty WorkingSet64'`。

## 提交与 PR

- 提交信息用 `type: 描述`，type 取 `feat` / `fix` / `docs` / `refactor` / `test` / `chore`，描述用中文。
- 分支命名 `feat/<主题>`、`fix/<主题>`、`chore/<主题>`；同一任务复用同一分支。
- 一次提交只做一件事，不混入无关文件；提交前看 diff、跑验证。
- 未经明确授权不改写历史、不强制推送、不丢弃改动。

## 安全与配置

- 不提交 cookie、token、证书、签名文件。
- 运行态数据写 `%APPDATA%\alive`，日志与缓存写 `%LOCALAPPDATA%\alive`。
- 平台接口节制轮询，触验证码就退避，不并发轰接口。
