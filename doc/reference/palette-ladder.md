# 表面阶梯：地 / 托盘 / 卡片

**2026-11-19，Windows 11 x64。** 数字由 `internal/theme` 的六套配色算出来，
表可以从测试直接打：`go test ./internal/theme -run TestChromeSitsBetweenGroundAndCard -v`。

## 三档

| 角色 | 令牌 | 用在哪 |
|---|---|---|
| 地 | `BgPrimary` | 窗口底、顶栏底 |
| 托盘 | `SurfaceChrome` | 平台轨道、工具组、窗口按钮组——装一排控件的那层 |
| 卡片 | `BgSecondary` | 直播间卡片、分区面板、浮层内容 |

托盘不是手写的颜色，是**地和卡片在 L\* 上的中点**：
`midpointL(ground, card)`。OKLCH 的 L 不等距，直接除以二会让深色主题的两段
差出一倍，所以用二分把 L\* 等分。

| 配色 | 地 | 托盘 | 卡片 | Δ下 / Δ上 |
|---|---|---|---|---|
| signal | `#0a0b0d` 3.0 | `#111314` 5.7 | `#171819` 8.2 | 2.7 / 2.4 |
| daylight | `#eaebed` 93.0 | `#f4f5f7` 96.5 | `#ffffff` 100.0 | 3.5 / 3.5 |
| mono | `#0a0a0a` 2.7 | `#111211` 5.3 | `#171717` 7.7 | 2.6 / 2.4 |
| paper | `#ebebeb` 93.0 | `#f4f5f4` 96.4 | `#ffffff` 100.0 | 3.4 / 3.6 |
| ember | `#100b07` 3.3 | `#17120f` 5.9 | `#1d1814` 8.7 | 2.6 / 2.8 |
| parchment | `#f1e7d9` 92.1 | `#f9f1e4` 95.4 | `#fffcef` 98.9 | 3.4 / 3.4 |

高对比模式与普通模式**表面相同**：高对比改的是文字阶梯和描边，不是面。
深色主题整条阶梯只有 5 个 L\*，等分后每边 2.4–2.8；浅色有 7 个，每边 3.4–3.6。

## 为什么不用 WCAG 对比度

白卡和地是 1.19:1，填色托盘和地 1.05:1——这个数在浅色区间几乎没有分辨力。
表面的台阶看 L\*，文字的对比看 WCAG（`contrast_test.go`）。两个指标各管一段。

## 规则

- **静态表面不描边。** 卡片、分区面板、托盘、搜索框静止态都没有 1px 线；
  边界由色阶、圆角、间距承担。`internal/ui/container_test.go` 扫源码兜底。
- **描边只给两种东西**：压在内容上的浮层（`BgElevated` + 阴影 + 描边，
  浅色主题里阴影单独撑不住边界），以及功能性边界（聚焦环）。
- **功能性描边要 ≥3:1。** `Border` 对地只有 1.27:1，它不能单独承担"这里可点"。

## 灵动岛：挖孔，不在阶梯上

岛不参与地面/托盘/卡片的阶梯。它是恒定的 `IslandInk`（`#060709`），
像手机的屏幕挖孔：永远比界面暗，因为它是硬件，不是卡片。

一开始它跟卡片一样白、再补 1px 描边；后来改到托盘那一档，三种白并排，
眼睛读不出层级。真正的解法不是再挑一档灰，而是换材质：黑底一铺，
影子有地方落，环有地方发光，浅色主题也不必在半透明深色里假装有光。

代价是岛上的令牌必须自洽：

- 文字、图标、填充、描边走 `Media*`（压在内容上的墨），不借 `Fill*` / `Border`；
- 环走 `AccentOnInk`——对墨底推导的第三支强调色，不跟 `AccentInk` / `AccentLine` 混用；
- 顶部高光用 `IslandTopLight`，不用 `TopLight`。

### 光环：一段弧，2 DIP，整颗岛一笔画完

`islandRingInset = 0`、`islandBodyInset = 2`：整颗岛由 `islandGlow` 一笔
实色 conic 渐变填出（弧外 `IslandInk`、弧内 `AccentOnInk`），body 从岛沿
往里 2px 盖上墨面，露出 2px 宽的光带。**不拆成「环 + 底色」两层**：两层
各抗一次锯齿，弧段上的边界像素就是「光 + 墨 + 顶栏」三色混合，比两侧都暗，
浅色主题里是一道黑描边。`TestIslandSurfaceIsOneOpaqueFill` 要求所有停靠点
不透明，`TestIslandOutlineHasNoDarkFringe` 沿岛沿逐像素检查。

**不往岛外扩**——试过一半探出岛沿，浅色主题里外半圈画在顶栏白底上，paper
的浅灰、parchment 的赭石直接消失，屏幕上又只剩岛内一条。

剖面 `islandRingProfile` 两端回到纯墨，亮的只有约 18% 圆周的一段弧；热段
`AccentOnInk` 对岛面 ≥4.5:1，尾巴用同一支色降 alpha，不混纯黑——混黑会在
暗底上拖出灰边。

余晖靠**拖影**：同宽、落后 12°、不透明度 0.38。它先和芯的 alpha 剖面合成，
再一起换成实色停靠点（`islandGlowStops`），不单独占一层。不往外加宽
（浅色主题会画到顶栏上），也不用 `Blur`（离屏层每帧重建，实测
2.4ms → 44.7ms/帧）。

角速度不是匀速：`islandSweep` 让头快、尾慢，回绕处速度连续。reduced motion
下不转，弧停在固定角度。

岛沿往里 2.5–4.5px 那一圈必须是墨，不是顶栏：岛面由那一笔铺满整个圆角盒，
否则圆角裁剪会在胶囊两端按行露出顶栏。

兜底：`TestIslandRingBandSitsInsideTheEdge`、`TestIslandRingIsAnArcNotARing`、
`TestIslandSurfaceIsOneOpaqueFill`、`TestIslandOutlineHasNoDarkFringe`、
`TestIslandRingEchoTrailsTheHead`、`TestIslandRingIsVisibleInEveryPalette`、
`TestIslandRingHoldsStillWithReducedMotion`、`TestIslandEdgeIsInkNotBar`、
`TestAccentOnInkIsVisible`。
## 之前是什么

顶栏托盘用的是 `FillQuaternary`——那是**相对卡片**的浅填充。放到地上：

- daylight 只剩 1.9 个 L\*（1.05:1），于是补了 1px `Border`，形状是线画的；
- 深色主题里它跑到地上面（+4.8 个 OKLCH L），同一个托盘换主题就从凹槽变凸片。

描边和填充同时存在，再加上中间那颗白色的灵动岛，一条 64 高的顶栏里有四种材质。
岛不在阶梯上，另算一档墨面，见上面「灵动岛」一节。

## 没做的

- **输入框的静止描边。** 分区搜索、顶栏搜索、关注页搜索已经改成"静止不描边、
  聚焦画 accent"。弹幕面板里那几个输入框还在视频材质上，归另一套系统。
- **订阅按钮的 1px 描边。** 未订阅态的"订阅分区"只有一圈 1.27:1 的线，
  按 §1.4.11 它不够 3:1。这是功能性描边，要单独选一支 ≥3:1 的颜色，
  不和装饰边一起做。
- **播放器外壳的描边。** `player.go` 那圈线参与圆角裁剪（`Clip` 裁 padding box），
  在 `container_test.go` 里以 `container: allow` 标注为例外。



