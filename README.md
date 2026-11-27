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
- 🩺 诊断：结构化事件日志、崩溃黑匣子 `crash-*.log`、把日志翻译成答案的 `alive-diag`

## 安装方式

可以在 [release](https://github.com/rongyuio/ALIVE/releases) 目录下载 `alive-setup-<Version>-amd64.exe` 安装，也可以直接用同目录的便携版 `ALIVE.exe`。

安装包没有代码签名，Windows 首次运行：更多信息 → 仍要运行。

配置和登录态在 `%APPDATA%\alive`，日志与缓存在 `%LOCALAPPDATA%\alive`。

## 编译

- 前置：Windows x64、Go 1.27+、make，仓库已包含libmpv DLL（third_party/mpv）

```bash
# 克隆项目
git clone https://github.com/rongyuio/ALIVE.git
cd ALIVE

# 开发调试：改 .go 自动重建重启
make dev

# 打包构建：build/windows-amd64/ 下的便携版和安装包
make build

# 全部门禁：fmt + build + vet + mygo vet + lint + 测试 + 插件测试
make check

# e2e自动化插件构建
make e2e
```

## 排障

```bash
make diag                                              # 编译 build/alive-diag.exe
alive-diag symptoms                                    # 有哪些症状
alive-diag answer player-fail "%LOCALAPPDATA%\alive\logs"
alive-diag bundle "%LOCALAPPDATA%\alive\logs" out.zip  # 提 issue 用这个，已脱敏
```

日志排查：

| 症状 | 怎么查 |
| --- | --- |
| 播放页卡住、黑屏、没声音 | `alive-diag answer player-fail <日志目录>`：取流失败是平台或 CDN 的事，引擎打不开是解码器的事 |
| 某一格一直黑着 | `alive-diag answer black-tile <日志目录>`：看有没有 `wall.open` 却始终没有 `wall.first_frame` |
| 同一个房间反复重连 | `alive-diag answer reconnect-loop <日志目录>`：看几次重开、每次的原因 |
| 进程崩溃 | 这类不在日志里，看 `%LOCALAPPDATA%\alive\logs\crash-*.log`，完整栈就在里面 |

症状与证据的完整对照在 [diagnostics.md](doc/architecture/diagnostics.md)。

## 参考

- 功能界面参考： [chen-zeong/DTV](https://github.com/chen-zeong/DTV)
- 自绘界面框架：[github.com/egoist/mygo](https://github.com/egoist/mygo)
- 播放：[libmpv](https://github.com/mpv-player/mpv)，头文件与 DLL 在 `third_party/mpv`
- 接口字段的实测记录见 [platform.md](doc/architecture/platform.md)，实测数字见 [measurements.md](doc/reference/measurements.md)