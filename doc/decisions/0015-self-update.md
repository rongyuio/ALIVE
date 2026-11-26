# 0015 自更新：谁签名、谁发布、界面在哪

**2026-11-25 落地。** 结论：走 MyGo 自带的签名更新（`mygo.Updater`），窗口用
`plugins/updater/native`，手动入口放在「数据同步 → 诊断」页签上，版本行同期加。

## 为什么之前没有

不是漏了按钮，是三层都空（2026-11-25 逐处核实）：

| 层 | 状态 |
|---|---|
| `mygo.json` 的 `updates` | 没有这一段 → `mygo.Updater.Check` 直接返回 `ErrUpdatesDisabled`（`third_party/mygo/updater.go` 的 `Check` 第一句） |
| `internal/` 的更新代码 | 零行。没有版本比对、没有 release 请求。唯一痕迹是 `diagVersion()` 把版本号写进导出的诊断包，屏幕上不显示 |
| 界面入口 | 顶栏只有「数据同步」一个应用级入口，三个页签是导出 / 导入 / 诊断 |

## 为什么用 mygo 的签名更新，不自己写

自己写就得自己回答四个问题：版本从哪儿读、下载的东西怎么防止被换、装到哪个目录、
装完怎么让用户跑到新版本上。MyGo 的这套已经回答了全部（Ed25519 签名 + 每个平台的
manifest + 同目录替换 + `App.Relaunch`），而且 delta 更新是白送的：第二个版本起，
`mygo build` 会下载上一个版本的归档，算出二进制补丁，用户只下变动的部分。

代价是接受它的约束：**发布位置必须匿名可读**，且私钥丢了就再也推不动已装的用户。

## 为什么是 native.Plugin 不是 updater.Plugin

`plugins/updater` 的默认窗口是一个网页，跑在 webview 里。ALIVE 没有 webview
（`ui.View` 自绘，Windows 上是 D3D11），为了一个「有新版本」的对话框把 WebView2
的运行时装进依赖里，等于给一个 31MB 的原生应用加一个 Chromium 分支。

`plugins/updater/native` 是同一个 session、同一套状态机的自绘窗口版本：它只依赖
`package ui`，画法跟主窗口一致，文案走插件的 zh-Hans 翻译（ALIVE 的 locale 就是
zh-Hans）。实测窗口标题「软件更新」，开发构建下正文「开发版本不会自行更新。」

## 为什么手动入口在诊断页

顶栏只有一个应用级入口（「数据同步」），导出 / 导入 / 诊断三页都在回答「这个应用
自己的状态」。版本号天然属于那里——它是「我在跑什么」的一半。

没有为它新开一枚顶栏按钮：更新是后台按天自动的，主路径是插件自己弹窗；手动入口
只是让用户在「是不是该有新版了」的时刻能主动问一次。为这个频率在顶栏争一个位置
不划算。

诊断页那一行的实现见 `internal/ui/diagnostics_panel.go` 的 `updateLine`：

```
版本   ALIVE 0.1.0                    检查更新
```

版本文字来自 `appVersionText()`：打包构建给 `ALIVE <version>`，`go run` 给
`ALIVE 本地构建`。**不要**在这里用 `diagVersion()`——那个的值进导出的诊断包，
脚本要能解析，所以它写 `unknown`；屏幕上写英文词会让人以为版本信息坏了。

按钮走 `App.CheckUpdates` 注入，不是直接 import 插件：插件只在 `main` 里
`mygo.Use` 之后才活着（`updater.used()` 取不到实例会 panic），而 `internal/ui` 的
测试直接 `NewMockApp`，从不 Use 任何插件。`CheckUpdates == nil` 时按钮不出现。

## 密钥

- 私钥 `mygo-update.key`、公钥 `mygo-update.pub`：`go tool mygo keygen` 生成在
  `%APPDATA%\mygo\update-keys`（Windows），**不进仓库**。`.gitignore` 挡着同名文件
  兜底。
- 公钥的内容写在 `mygo.json` 的 `updates.publicKey`，它编进二进制。装了旧版的人
  只接受用对应私钥签的更新，所以**私钥丢了就再也推不动他们**。
- 构建时私钥走环境变量 `MYGO_UPDATER_PRIVATE_KEY`（CI 里用 secret）。没有私钥时
  `mygo build` 照常出安装包，只是不产更新文件，并在输出里说一句。

## 发布流程

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat mygo-update.key)" go tool mygo build -upload
```

产出（`build/windows-amd64/`，2026-11-25 实测，0.1.0 首版）：

| 文件 | 大小 |
|---|---|
| `ALIVE.exe` | 31.1 MB |
| `alive-setup-0.1.0-amd64.exe` | 43.4 MB |
| `alive-0.1.0-windows-amd64.tar.gz` | 59.2 MB |
| `update-windows-amd64.json` | 1 KB |
| `install.ps1` | 7 KB |

`update-windows-amd64.json` 是 app 真正读的东西：版本、日期、归档的 URL、大小、
签名。首版没有 delta，构建输出会写
`No delta updates for windows-amd64: no version is published yet`——这是对的，
第二个版本起才有。

`-upload` 把文件传到对应版本的 **draft** release；发布那个 release 之后，它成为
latest，app 从 `releases/latest/download/` 读 manifest。**draft 不算发布**：装了
0.1.0 的人查更新时读不到 draft 里的 manifest。

## 一条硬约束：发布位置必须匿名可读

装的 app 是匿名去取 manifest 的。私有仓库的 release 资产对未认证请求返回 404，
所以仓库是 private 的那一天，更新链路就是死的——**这不是配置问题，是路径设计**。

2026-11-25 之前 `rongyuio/ALIVE` 是私有仓库，当天转成了 public（转之前扫过历史，
没有凭据、状态文件或日志入库），main 同时开了分支保护：禁强推、禁删除，
不强制 PR（这个仓库一直在 main 上直线提交）。

另外两条路没有采用，但配置换过去只动 `mygo.json` 的三行，界面和插件不用改：
`updates.url` 指向自己的 HTTPS 目录（文件手动上传，manifest 最后传），
或者 `updates.s3` 推 R2/S3 桶。

## 验证边界

- 真实窗口验的是 e2e 构建（没有版本号 → 「ALIVE 本地构建」），点击后弹「无法更新 /
  开发版本不会自行更新。」——证明插件接上、文案是中文、按钮真的调到了
  `CheckForUpdates`。
- 打包构建的版本号从日志核过：`run.start` 的 `version=0.1.0`（2026-11-25）。
  **没有**在打包构建上跑到「真的下载并替换自己」：那需要先发布一个版本，而本仓库
  至今没有 release。这条路径在第一次发版后必须补验：装 0.1.0 → 发 0.1.1 →
  让它自己升上去。