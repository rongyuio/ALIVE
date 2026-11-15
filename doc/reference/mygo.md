# MyGo 速查

框架层面的版本演进、能力边界和踩过的坑。写 UI 的硬规则在根目录 `AGENTS.md`，
这里只记那些"看代码看不出来"的部分。

## 当前版本

`github.com/egoist/mygo v0.9.1`（2026-11-16 从 v0.9.0 升级）

## v0.9.1 带来的变化

- Windows 上非 ASCII 目录里的视频可以播（此前路径含中文会失败）
- 构造过程中修改窗口尺寸不再画一帧拉伸的画面
- 布局快 8–23%
- SVG 支持 CSS 变量、文本、marker
- **破坏性变更**：`Element.Key` 迁移到 `Context.Key`。升级时要用官方 codemod，
  手改容易漏（`follows_page.go`、`island.go`、`loop_test.go` 都受影响）
- `ui.NewTester` 的默认外观随版本变化，依赖配色的测试要显式设置
  （`main_test.go` 的 `TestPaletteSwitch` 因此改成显式 `ThemeMode = "light"`）

## 能力边界

这些不是 bug，是当前版本的设计，直接影响我们的播放路线（见
[decisions/0001](../decisions/0001-player-backend.md)）：

| 事实 | 影响 |
|---|---|
| `ui/video` 在 Windows 上由 Media Foundation 实现 | 用系统解码器；没有 HEVC/AV1 扩展就只能转码 |
| `ui.VideoPlayer.Buffering()` 只读，`media.Engine` 没有缓冲水位/低延迟开关 | 直播延迟和卡顿不受我们控制 |
| `ui.Bitmap.Update` 从 CPU 内存上传 | 自解码要 CPU→GPU 拷一次，不是零拷贝 |
| `Bitmap.UpdateYUV` 支持 NV12 + `BT709`，色彩转换在 GPU 做 | 自解码走这条，上传量是 RGBA 的一半 |
| `platform.ViewSurface.EmbedView` 只有 darwin 实现 | Windows 不能嵌原生播放视图 |
| `ui.NewEngineVideoPlayer` 依赖内部包 `internal/media.Engine` | 应用无法实现自己的引擎 |

## 上游状态

- `#789`（已关闭）：`ui/video` 用系统媒体引擎实现
- `#973`（开放）：硬件帧直通未做，作者原话是"暂时只能 `av_hwframe_transfer_data` 到 bgra 再 `Bitmap.Update`"
- `#124`（开放，+4054 行，2026-10-06 起未合）：`plugins/video`，基于 libmpv
- `#105`（路线图）：第 16 条「Native-view hosting」未勾选

## 自动化验收的坑

`ui.NewTester` 验不到真实渲染、滚动、图片加载和内存，真实窗口验收用
`mygo-automation`（命令见 `AGENTS.md`）。两个必须知道的行为：

- **`--settle` 在本应用里基本等不到。** 灵动岛有常驻动画，窗口一直请求帧，
  settle 会一路超时到 `--timeout`。要等具体内容用 `wait --text` / `wait <sel>`，
  或者直接 `snapshot` 看当前状态。
- **自绘元素读不出来。** 只有带 Role/Label 的元素进无障碍树。卡片、按钮都是自绘 Box，
  snapshot 里没有，只能按坐标 `click`。给控件补 `.Label()` 才会出现。

## 升级流程

1. 改 `go.mod` 版本
2. 跑官方 codemod（如果有破坏性变更）
3. `go build ./...`、`go vet ./...`、`go tool mygo vet .`、`go test ./...`
4. 截图验收：默认态一张 + 每个被改动的浮层/状态各一张
5. 播放相关的升级（v0.9.x 都算）要重跑
   [reference/measurements.md](measurements.md) 的首帧对照
