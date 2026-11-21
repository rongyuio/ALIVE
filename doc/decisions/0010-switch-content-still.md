# 0010 切换时内容层不动

**决定（2026-11-21）**：切换平台、切换分区时，内容层（卡片网格）不位移、
不整块淡入淡出，卡片也没有入场动画；骨架静止。动效只留在控件上——tab /
分区滑块、悬停放大、按压反馈。验收用帧级不变量，不用「看起来还行」。

## 为什么

「切换后画面一直在抖」这条报障，前后量出两个根因，都不是「动画太快/太慢」，
而是**内容在切换时被挪动过**：

1. **内容层按方向整块横滑**。`feedTransition` 给 Enter/Exit 各 16 DIP 位移，
   用 `theme.Slide()` 弹簧跑约 540ms。真窗口读数：第一张卡 `x=30 DIP`，
   一帧帧滑回 14。
2. **卡片入场带位移和错峰**。`opacity 0→1 + translateY 10→0`，前 6 张各
   错峰 20ms。Tester 逐帧量 `bounds`：整屏从 `y=198` 落到 188，同一行最左
   和最右最多差 **6.6 DIP**，约 150ms 才收齐。

还有两个隐性的放大器：

- `ui.Motion` 不写 `Opacity` 时默认 **0（全透明）**。所以「只是滑一下」的
  设计实际是「整屏从透明里滑进来」——截图里那片发虚的网格就是它。
- 骨架带着 1.6s 的 `Loop` 呼吸，等待期间整块网格在 0.55↔1 之间变亮变暗，
  而且每帧请求重绘。等待本身不该有动效（[0005](0005-no-launch-screen.md)
  说的就是这件事，骨架当时没跟上）。

## 换成了什么

- **内容层只换代**：`navSeq` / `followsSeq` 变了就换 key，新旧子树直接交换。
  没有 `Transition`、没有 `navDir`（方向这件事交给选项栏的滑块，它本来就是
  从当前位置弹过去的）。
- **卡片没有入场**：数据到达那一帧，卡片就在最终位置、完全不透明。删掉了
  `EnterSeq` / `CardsIn` / `resetCards` 一整条链路。
- **封面到达是原地换像素**：同一块 16:9 的几何，占位色换成图。原先套的
  `Transition{Enter: Scale 0.985}` 是死代码——占位和图片共用元素身份，
  `Enter` 根本不播；留着只会在以后某次结构改动里突然开始播。现在直接删掉。
- **骨架静止**：去掉 `Loop` 脉冲，固定材质；信息条按 `cardLineHeight`
  （MyGo 默认行高 = 字号 ×4/3）撑到和真卡一样的 64.33，头像 32、行距 3，
  换屏时整行不会矮一截。

## 怎么验的

六条不变量，`make check` 里各有一条测试（都先在旧代码上跑红）：

| 不变量 | 测试 |
|---|---|
| 换屏不位移（Δ=0） | `TestCategorySwitchKeepsContentInPlace` |
| 数据到达即最终帧（无淡入/位移） | `TestDataArrivalCommitsFinalFrame` |
| 数据到达卡片不动 | `TestDataArrivalKeepsCardsInPlace` |
| 骨架静止且不请求帧 | `TestSkeletonIsStill` |
| 骨架/真卡信息条同高 | `TestCardMetaHeightFormula` |
| 内容层不许有进出场动画（静态扫描 `Transition`/`Translate`/`Loop`） | `TestContentLayerHasNoMotion` |
| 切平台 → 切分区 → 骨架到数据，全程不位移不闪 | `TestContentLayerStableAcrossSwitches` |

前四类用 `internal/ui/frameprobe_test.go` 逐帧判定：bounds 容差 0.01 DIP，
像素差为 0。分类、阈值来源和真实窗口的测量极限见
[ui-regressions.md](../reference/ui-regressions.md)。

真窗口复核（`make e2e` + `mygo-automation snapshot --full` 按 ~18Hz 读
`bounds`，1397×818）：切分区、切平台，第一张卡从第一次采样起就是
`x=14, y=188`。数字见 [measurements.md](../reference/measurements.md#切换动效2026-11-21)。

`TestDataArrivalCommitsFinalFrame` 的写法值得留着：同一视口构建两份——一份
从骨架切到数据、一份一开始就有数据，时钟不推进时任何动画都停在起点。
两份的整帧平均亮度必须一致（±0.5）。这一条同时守住「没有淡入」和「没有位移」。

## 什么情况下重做

想给内容层加动效，先回答三个问题：切换前后内容区的 `bounds` 是否完全相同？
有没有任何一帧的亮度偏离稳定值？任意一帧里内容是否只有一份？三个都是「是」
才有得谈，否则用户会把它读成「画面在抖」。控件上的动效不受这条约束——
滑块、悬停放大、按压反馈继续放开。
