---
name: mygo-automation
description: 读运行中 MyGo 应用的原生界面元素并对它操作：按 ref 点击、填输入框、发指针 / 滚轮 / 按键 / 文本输入、选应用菜单项、在页面里执行脚本、截图、等界面出现某个内容。需要验真实窗口的渲染、滚动、图片加载、播放或内存时用。
---

这个文件只是发现入口，不是用法说明。跑任何 `mygo-automation` 命令之前，先取当前安装版本
自带的指南：

```sh
mygo-automation skills
```

不要先用 `--help` / `--version` 试探。只有命令不存在时，才在应用模块目录里装（这样装到的
版本和 app 依赖的 MyGo 一致）：

```sh
go install github.com/egoist/mygo/plugins/automation/cmd/mygo-automation
```

命令版本和 app 的 MyGo 不一致时，ref、diff、错误码都可能对不上；升级 MyGo 后重装一次。

本项目的验收约定（哪些事必须用自动化验、`--settle` 为什么等不到、session 怎么分）写在
仓库根目录 `AGENTS.md` 的「驱动真实窗口做验收」一节。
