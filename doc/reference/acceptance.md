# 驱动真实窗口做验收

`ui.NewTester` 验不到真实渲染、滚动、图片加载、播放和内存。这些用 MyGo 的自动化插件，
通过 socket 读运行中窗口的元素树、点击、滚轮、截图——不需要人工盯着。

**先跑 `mygo-automation skills`**：那是当前安装版本的完整指南（命令、ref、session、settle、
diff、错误码）。下面是这个项目常用的那一小撮，别拿它当手册去猜参数。命令版本要跟 app 的
MyGo 一致，升级 MyGo 之后在模块目录里重装一次。

## 起一轮

```sh
make e2e                                          # automation_e2e.go 只在 e2e 构建里存在
MYGO_AUTOMATION=.tmp/alive-e2e.sock build/e2e/alive-e2e.exe &
mygo-automation open --socket .tmp/alive-e2e.sock --session alive-e2e

mygo-automation snapshot -i                  # 带 ref 的元素列表，元素太多就 | head
mygo-automation snapshot --full | grep 斗鱼   # 带 bounds，坐标从这儿拿
mygo-automation press @e2                    # 按 ref 点，不用坐标
mygo-automation fill @e3 "英雄联盟"            # 填输入框，不用先点
mygo-automation wait --text "已显示最近"       # 等内容出现，不要 sleep
mygo-automation wheel 700 700 0 3000         # 滚动，单位 DIP，dy 正数向下
mygo-automation screenshot .tmp/x.png
mygo-automation close                        # 只断会话，app 还在跑
taskkill //F //IM alive-e2e.exe              # 收尾
```

`.tmp/` 已被 `.gitignore` 挡着，socket 和截图都放这里——不要用外部绝对路径，
换台机器、换个盘就失效了。

## 这个项目踩过的坑

- **`--settle` 基本等不到**：灵动岛有常驻动画，窗口一直请求帧，settle 会一路超时到
  `--timeout`。等具体内容用 `wait --text` / `wait <sel>` / `--gone`，**`wait` 后面也别加
  `--settle`**；只想看当前状态就直接 `snapshot`。**任何时候都不要 `sleep` 再截图**。
- **优先 `press` / `fill`，不要 `click x y` / `type`**：ref 不依赖坐标，布局一动坐标就失效；
  ref 失效会明确报 `stale ref`，不会点错东西。
- **自绘元素读不出来**：只有带 Role/Label 的元素进无障碍树。卡片、按钮都是自绘 Box，
  snapshot 里没有，只能按坐标 `click`——给控件补 `.Label()` 才能用 ref 驱动，见根目录
  `AGENTS.md` §3.3「自绘控件是常态」。
- **一个 agent 一个 session**（`--session <名字>`）：ref 存在各自的文件里，互不干扰；
  同一个 session 同时只跑一条命令。
- 右键菜单别用 `click --button right` 开：插件选不了系统菜单。测右键菜单用 Tester 的
  `RightClick` / `ChooseMenuItem`。
- socket 放只有自己能写的目录，路径带上应用名（`alive-e2e.sock`）：共用路径会连到别人的 app，
  上一轮就是这么踩到的。
- 坐标是 DIP，从内容区左上角算，和 `--full` 的 `bounds=` 同一套。
- 量内存：`pwsh -NoProfile -Command 'Get-Process alive-e2e | Select-Object -ExpandProperty WorkingSet64'`。
