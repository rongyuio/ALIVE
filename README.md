<div align="center">
  <img src="resources/icon.png" alt="ALIVE" width="128">
  <h1>ALIVE</h1>
  <p>基于 MyGo 的 Windows 直播桌面客户端</p>
</div>


## 说明

1. 基于 [MyGo](https://github.com/egoist/mygo) 自绘原生界面，无cgo，无webview。播放基于 libmpv，无需额外安装解码组件
2. 平台接口可能有访问频率限制，过于频繁的请求会触发验证码校验，建议合理使用搜索功能
3. 本项目仅供学习与个人使用；所有直播内容与版权都归属各个平台和主播

## 功能

- 📺 平台支持：斗鱼、虎牙、bilibili、抖音四个平台的直播、分区、搜索与关注
- 💬 弹幕显示：四个平台的弹幕归一化，支持 SC、粉丝牌、表情、屏蔽词；弹幕面板可调宽度与字号
- ⭐ 主播收藏：关注列表、订阅分区，支持手动拖拽排序
- 🔲 悲鸣观测：至多 12 间直播观测，每间独立的音量与播放态
- 🎬 播放：断流自动重连、后台收听
- 🔁 数据同步：配置导入导出（导入前先看改动）
- 🌓 主题切换：六族十二套配色，日夜模式，支持跟随系统
- 🩺 诊断：结构化事件日志、崩溃黑匣子 `crash-*.log`、应用内诊断页导出脱敏日志包

## 安装方式

可以在 [release](https://github.com/rongyuio/ALIVE/releases) 目录下载 `alive-setup-<Version>-amd64.exe` 安装，也可以直接用同目录的便携版 `ALIVE.exe`。

安装包没有代码签名，Windows 首次运行：更多信息 → 仍要运行。

配置和登录态在 `%APPDATA%\alive`，日志与缓存在 `%LOCALAPPDATA%\alive`。

## 开发

前置：Windows x64、Go 1.27+、make、gcc、golangci-lint。

```bash
git clone https://github.com/rongyuio/ALIVE.git
cd ALIVE

make deps      # 取 libmpv，它不入库
make mock      # 假数据看界面，不联网
make dev       # 改 .go 自动重建
make build     # 打包到 build/windows-amd64/
make check     # 提交前跑
```

`make run` 和 `make dev` 跑在 `.mygo\dev-home` 沙箱里，不碰真实的 `%APPDATA%\alive`。

发版：打 `v*` tag，CI 建草稿 release 并上传签名产物；本地 `make build` 需要
`MYGO_UPDATER_PRIVATE_KEY` 才会产出更新文件。

## 排障

日志与崩溃现场都在 `%LOCALAPPDATA%\alive\logs`：

- `alive-<run>.log`：一次运行的完整结构化事件日志（`event` 键见 `internal/diag`）。
- `crash-<run>.log`：崩溃时的完整 goroutine 栈，这类不在普通日志里。

要一份能贴进 issue 的脱敏包，用应用内诊断页导出（`internal/support.Bundle`），别手动发原始日志。

| 症状 | 先看什么 |
| --- | --- |
| 播放页卡住、黑屏、没声音 | 先分清上游（平台流断了、CDN 慢）和本机（尺寸、门控、接管）：对照日志里的取流与引擎事件 |
| 某一格一直黑着 | 看有没有 `wall.open` 却始终没有 `wall.first_frame` |
| 同一个房间反复重连 | 数日志里几次重开、每次的原因 |
| 进程崩溃 | 看 `crash-<run>.log` 的完整栈 |

## 参考

- 功能界面参考： [chen-zeong/DTV](https://github.com/chen-zeong/DTV)
- 自绘界面框架：[github.com/egoist/mygo](https://github.com/egoist/mygo)
- 播放：[libmpv](https://github.com/mpv-player/mpv)，头文件与 DLL 在 `third_party/mpv`
- 平台取流约束见 [internal/live/README.md](internal/live/README.md)，播放分层见 [internal/playback/README.md](internal/playback/README.md)