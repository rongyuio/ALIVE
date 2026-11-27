# 本地这份 MyGo 与上游的差

一句话：`third_party/mygo` 不是 `github.com/egoist/mygo@v0.10.0` 的原样副本。
它是一份**本地分支**，装了三块上游没有的能力。升级 MyGo 前先读这一页。

## 事实（可复跑）

```
go.mod:
  require github.com/egoist/mygo v0.10.0
  replace github.com/egoist/mygo => ./third_party/mygo
```

核对本地改了什么：

```powershell
$up = "$(go env GOMODCACHE)\github.com\egoist\mygo@v0.10.0"
git diff --no-index --name-status -- $up third_party/mygo
```

当前结果：**5 个文件被改，20 个文件是新增**（`2336 insertions(+), 7 deletions(-)`）。
`go.mod` 里那句「上游代码一行未改」在过去某次改动后已经不成立，已改掉。

分组如下。每组都给了「为什么」和「升级时怎么办」，原因取自代码自己的注释，
不是事后推断。

---

## A 组：把引擎契约导出（4 新增 + 1 修改）

| 文件 | 干什么 |
|---|---|
| `media/media.go` | 类型别名重导出 `internal/media` 的 `Engine`/`Picture`/`State` |
| `scene/scene.go` | 类型别名重导出 `internal/scene` 的 `Surface`/`Plane`/`SurfaceFormat`/`YUVMatrix` |
| `internal/scene/surface_id.go` | `NextSurfaceID()`：全进程唯一的 `Surface.NativeID` 发号器 |
| `ui/engine_export.go` | `(*ui.VideoPlayer).Engine() media.Engine` |

为什么：`ui.NewEngineVideoPlayer(e media.Engine)` 是导出的，但 `media` 在
`internal/` 下，应用写不出参数类型。别名指向同一个类型对象，所以不需要动
`internal/` 的实现（`media/media.go:1-7`）。

`surface_id.go` 的原因更硬：渲染器按 `NativeID` 缓存已打开的纹理，而两个生产者
（`plugins/video` 的 mpv、`ui/video` 的 Media Foundation）各自从 1 数会撞号，
撞号就是整墙同一画面（`internal/scene/surface_id.go:5-9`，`ui/video/mf_windows.go`
因此从本地计数器改成 `scene.NextSurfaceID()`）。

升级时：上游若把 `internal/media`、`internal/scene` 变成可命名的公开类型，删掉
`media/`、`scene/`，把应用与 `engine_export.go` 的 import 换回上游路径；否则保留。

## B 组：把 GPU 画的那一帧读回来（2 新增 + 2 修改）

| 文件 | 改了什么 |
|---|---|
| `ui/record.go`（新增） | `PresentedFrame`、包级 `FrameSink` / `FrameErrorSink`、`windowHost.framed` |
| `internal/gpu/d3d11/capture.go`（新增） | `Renderer.SetFrameSink`、`SetCaptureErrorSink`、`captureFrame`、丢帧计数 |
| `internal/gpu/d3d11/d3d11.go`（改） | `Renderer` 加 `sink`/`captureErrs`/`captureLog`；`drawFrame` 在 Present 前调 `captureFrame()`，并且**有 sink 时不再只画脏区**（`known = known && r.held && r.sink == nil`） |
| `ui/window.go`（改） | `windowHost.frames` 计数；`makeGPU` 里在首帧前把 sink 交给渲染器 |

为什么：要在**真构建**上录/截「用户看到的那一帧」，只能从交换链后备缓冲读。
`ui.Window.CapturePage` 画的是内存里的最后一帧（raster），不是 GPU 画的
（`ui/record.go` 的类型注释说清了这两者的区别）。没有 sink 的窗口只多一次指针判断。

升级时：这套 API 是新增的，不会与上游冲突；但 `drawFrame` 里那条
`r.sink == nil` 必须还在——它保证「要读整帧时别只画脏区」，否则读回的帧四周是旧画面。

## C 组：mpv 播放引擎（14 个新增文件）

`plugins/video/`：上游没有的 optional content engine。用 purego 加载
`libmpv-2.dll`（`plugins/video/player.go:1-3`），软件渲染拿 **NV12** 帧
（`engine.go:15-22` 解释了为什么不是 RGBA：1080p 每帧 3MB vs 8.3MB），另有
D3D11 共享纹理零拷贝路径和给截图/CPU 光栅器用的 `ReadBack`（两处都标了
「ALIVE 本地补丁」，见 `engine.go:26-38`、`plugins/video/d3d11_windows.go`）。

`plugins/internal/contentlib/`：只被 `plugins/video` 用的库加载器，负责
「显式路径 → 可执行文件旁边 → 打包的 Resources」的顺序找人要的 DLL
（`contentlib/library.go:13-25`）。

升级时：这 14 个文件是本地新增，冲突面在 `ui.NewEngineVideoPlayer` 的签名和
`media.Engine` 接口；签名一变它们会一起编译失败，这就是最好的提醒。

**已知欠账（本章记账，不是规范）**：`plugins/video/README.md:4,6` 指向的
docs/plugins/video.md 与 examples/content-native，两个文件都不存在；这个插件也没有
`mygo-plugin.json`，所以 `libmpv-2.dll` 不在 `mygo build` 的资源描述里，靠应用自己的
`mygo.json` 的 `resources` 加 `make deps` 提供。

## D 组：安装器文案走 LangString（2 修改）

`cmd/mygo/languages.go`、`cmd/mygo/nsis.go`：安装器里「应用正在运行 / 仍未退出」
两句原本写死英文，声明了语言的安装包只有向导是中文、弹框还是英文。现在按应用声明
的语言取 `LangString`，没有的回落英文。

一个坑写在代码里：`nsisString` 会把 `$` 转义成 `$$`，所以这两句不能过它
（`cmd/mygo/nsis.go` 的注释）。

升级时：代码注释自己写了「升级 MyGo 时这处要一起搬回去」。

---

## 上游自己的债（不是我们的，别顺手改）

- `go tool mygo migrate-ui .` 会在 `third_party/mygo` 里列出 14 个文件
  （`examples/` 3、`internal/e2e` 2、`ui/editor` 6、`website` 1、+2 条 review 提示）。
  这是 v0.10.0 自带的形态，固定基线；它变了说明 pin 变了。
- `docs/plugins/` 只有 13 个页面，但 `plugins/` 里有 25 个插件包；`media`、`scene`
  两个公开包没有任何文档；`ui.NativeView` 没有 `docs/ui/` 页面。
  这些都记在上游账上，我们不补——补了下次升级就被覆盖。

## 核对

`make agents-check` 会验这一页引用的每个路径存在、每处行号在文件范围内。


