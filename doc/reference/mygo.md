# MyGo 速查

框架层面的版本演进、能力边界和踩过的坑。写 UI 的硬规则在根目录 `AGENTS.md`，
这里只记那些"看代码看不出来"的部分。

## 当前版本

`github.com/egoist/mygo v0.10.0`（2026-11-18 从 v0.9.1 升级）

## v0.10.0 带来的变化

对这里有影响的：

- 布局快约 12%（arewelayoutyet 基准），灵活行与折行最多快 20%——列表滚动直接受益
- 附着的弹出层（`SelectBase` / `ComboboxBase` / `PopoverBase` 的面板）不再超出窗口高度，
  内容多就在面板内滚动
- Windows 触摸屏单指滚动（此前单指是「按下并选择」）
- 三击选中整段，拖动按段落扩展（多行输入框和可选文本）
- `mygo dev` 重启后恢复滚动位置、`ui.Router` 的位置和 `ui.Local` 的值
- 新增 `ui/netimage`（认 `Last-Modified` / `ETag` 的内存与磁盘缓存）——我们用自己的
  `internal/media`，没采用
- 手机/平板（iOS / Android）：桌面构建完全不受影响，不启用

v0.9.2 的内容也一并包含（单实例锁等旧进程退出、`Element.Touches()`、菜单可保持打开
并带彩色徽章、页面可中止 Go 调用）。

## v0.9.1 带来的变化（历史）

- Windows 上非 ASCII 目录里的视频可以播（此前路径含中文会失败）
- 构造过程中修改窗口尺寸不再画一帧拉伸的画面
- 布局快 8–23%
- SVG 支持 CSS 变量、文本、marker
- **破坏性变更**：新增 `Context.Key`（有状态控件用）；`Element.Key` 保留，仍用于容器
- `ui.NewTester` 的默认外观随版本变化，依赖配色的测试要显式设置
  （`internal/ui/app_test.go` 的 `TestPaletteSwitch` 因此改成显式 `ThemeMode = "light"`）

## 通知

通知走 MyGo 的 toast 管理器加 `ToastViewportBase`（`internal/ui/home.go` 的
`toastViewport`），不再自绘单条。三条约束：

- **外观自绘，行为交给框架。** 队列（最多 3 条）、计时、指针悬停暂停、读屏播报、
  Esc 关闭都由 `ui.Toast` 提供，我们只画胶囊。外观用 `theme.Media*` 那组「压在画面
  上的墨」：通知会出现在首页、播放页和全屏画面上，跟着配色变的浅色底会融进背景。
- **`toastTone` 只入队，Shell 每帧认领一次。** 关注刷新完成落在 `win.Update` 回调里，
  那里没有构建帧的 `Context`，不能直接 `AddToast`。队列认领即清空，同一次事件重建的
  后续帧不会重弹。
- **ID 就是消息文本。** 同一条消息再次触发只刷新那一条的计时，不叠第二条。

2026-11-25 之前是自绘单条 `a.ToastText`：第二条直接顶掉第一条、固定 3.2 秒消失、
读屏不播报。用户反馈「经常注意不到」。

## 能力边界

这些不是 bug，是当前版本的设计边界。ALIVE 现在的播放主路径是
[0004](../decisions/0004-libmpv.md) 的 libmpv，下面与 `ui/video` / `UpdateYUV`
相关的行只描述框架自带的那套播放器，不是我们的主路径：

| 事实 | 影响 |
|---|---|
| `ui/video` 在 Windows 上由 Media Foundation 实现 | 用系统解码器；没有 HEVC/AV1 扩展就只能转码。ALIVE 已不走它 |
| `ui.VideoPlayer.Buffering()` 只读，`media.Engine` 没有缓冲水位/低延迟开关 | 直播延迟和卡顿不受我们控制 |
| `ui.Bitmap.Update` 从 CPU 内存上传 | 自解码要 CPU→GPU 拷一次，不是零拷贝 |
| `Bitmap.UpdateYUV` 支持 NV12 + `BT709`，色彩转换在 GPU 做 | 框架自带的自解码走这条；ALIVE 的 libmpv 引擎走 `scene.SurfaceBGRA`，观测墙再走 D3D11 共享纹理 |
| `platform.ViewSurface.EmbedView` 只有 darwin / mobile 实现 | Windows 不能嵌原生播放视图 |
| `ui.NewEngineVideoPlayer` 依赖内部包 `internal/media.Engine` | 上游未导出；ALIVE 用 `media/`、`scene/` 别名包 + `ui/engine_export.go` 把这层打开，自研引擎就是这样接进去的 |

## 上游状态

- `#789`（已关闭）：`ui/video` 用系统媒体引擎实现
- `#973`（开放）：硬件帧直通未做，作者原话是"暂时只能 `av_hwframe_transfer_data` 到 bgra 再 `Bitmap.Update`"
- `#124`（开放，2026-10-06 起未合）：`plugins/video`，基于 libmpv。我们的
  `third_party/mygo/plugins/video` 以这一份为骨架，另加了渲染门控、零拷贝 D3D11
  和连续性诊断等本地补丁，不是原样
- `#105`（路线图）：第 16 条「Native-view hosting」未勾选

## 自动化验收的坑

`ui.NewTester` 验不到真实渲染、滚动、图片加载和内存，真实窗口验收用
`mygo-automation`（命令见 [reference/acceptance.md](acceptance.md)）。两个必须知道的行为：

- **`--settle` 在本应用里基本等不到。** 灵动岛有常驻动画，窗口一直请求帧，
  settle 会一路超时到 `--timeout`。要等具体内容用 `wait --text` / `wait <sel>`，
  或者直接 `snapshot` 看当前状态。
- **自绘元素读不出来。** 只有带 Role/Label 的元素进无障碍树。卡片、按钮都是自绘 Box，
  snapshot 里没有，只能按坐标 `click`。给控件补 `.Label()` 才会出现。

## 升级流程

`third_party/mygo` 是**整份源码副本**（`replace` 到本地），不是普通模块依赖，所以
`go get -u` 那套不适用。实际步骤：

1. 在临时 module 里 `go mod download github.com/egoist/mygo@vX.Y.Z`，源码落在
   `$(go env GOMODCACHE)/github.com/egoist/mygo@vX.Y.Z`
2. `diff -rq third_party/mygo <新版本>`，把差异分成两类：
   - **我们的改动**：`media/`、`scene/`、`plugins/video/`、`plugins/internal/`、
     `ui/engine_export.go`、`internal/scene/surface_id.go`（Surface 发号，上游只有
     约定）、`cmd/mygo/nsis.go`、`cmd/mygo/languages.go`（安装器里「应用正在运行」
     的两句提示改成 LangString——上游写死英文，声明了语言的安装包只有向导是中文）
   - **旧版遗留**：上游新版本已删掉的文件（比如 `website/showcase`），丢掉即可
3. 把我们的改动移出去 → 整目录替换成新版本 → 再移回来
4. 改 `go.mod` 的版本号，`go mod tidy`
5. `make check`
6. 截图验收：默认态一张 + 每个被改动的浮层/状态各一张
7. 播放相关的升级要重跑 [measurements.md](measurements.md) 的首帧对照

两个容易踩的点：

- **`third_party/mygo` 有自己的 `go.mod`，是独立模块**，`go build ./...` / `go test ./...`
  根本不进去。`go test ./...` 在 `cmd/mygo` 会编译失败：本地把
  `nsisLanguageMacros` 的签名改了，`languages_test.go` 没跟着改。这是本地补丁
  留下的，不是上游未发布 API（`PopoverMenuOptions` 在上游已定义，`ui` 包能编译）。
- 上游发布说明写 "No breaking changes to the Go API" 也要自己验：v0.10.0 这次 ALIVE
  一行没改就过了 `make check`，但 `internal/raster` 的重叠区像素修复上游已经合进去了，
  本地的补丁要删掉，否则会重复。
