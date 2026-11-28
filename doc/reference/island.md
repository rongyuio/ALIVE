# 灵动岛：一个 Live Activity，不是一个迷你播放器

这份文件说清楚顶栏中间那枚岛跟直播间、观测墙、路由之间的关系。改 `island.go`
或任何 `surface_*` 之前先读它。视觉本身（墨底、流光、翻牌钟、头像、投影）
不在这里规定——那是 `island.go` 的事，这份只管**行为**。

## 心智模型

把应用看成 iPhone：

| iPhone | ALIVE |
|---|---|
| 桌面 | 首页 |
| App 切换器 | 关注页的「悲鸣观测」 |
| 打开某个 App | 进某个直播间的播放页 |
| App 内返回桌面 | Esc / 左上返回 = 收起（进程还在，声音继续） |
| **灵动岛 / Live Activity** | **焦点直播间**，浮在所有界面之上，不属于任何 App |

两条从 iPhone 抄来的规矩：

1. **岛不变形成 App，App 也不缩进岛。** 打开 App 时是 App 自己的窗口从岛的
   位置长出来；收起时缩回去。岛留在原地，只是自己的呈现变了。
2. **岛的形变只发生在它自己的呈现之间**（时钟 ↔ 这一路），与路由无关。
   进 / 出播放页时它不挪、不变宽。

## 状态

只有一个判据：`activityKey()`（`internal/ui/surface.go`）。它回答「这一刻
还有没有 Live Activity」——焦点在，而且不是一条死路（`roomSession.dead()`：
没有引擎、没在取流、且已落终态）。

形态由 `islandFormOf()` 求，两条规则：

1. **播放页上一律回时钟。** 照 HIG：*"Your Live Activity appears in the
   Dynamic Island while your app isn't in use"*。App 打开时它自己的活动不该
   占着岛——退出钮和房间名跟着搬进播放页自己的顶栏（见 `playerTopbar`）。
2. 其余路由：有活动就是这一路，没有就是时钟。

| 形态 | 条件 | 岛 | 点岛 | 岛上的 X |
|---|---|---|---|---|
| 时钟 | 没有活动，**或在播放页** | 24 小时翻牌钟 | 无操作 | 不画 |
| 这一路 · 观测墙 | 有活动 && 悲鸣观测 && 在墙上 | 头像 + 标题 + `平台 · 主播 · ID` + 星 + X | 打开播放页（从岛长出） | 停止观测 |
| 这一路 · 桌面 | 有活动 && 其它路由 | 同上 | 打开播放页（从岛长出） | 关闭直播间 |

「点岛收起」这条手势**不存在**：iPhone 上没有它（岛在 App 里是时钟，没有
热区）。收起是 App 自己的返回——Esc，或播放页顶栏的返回钮。

墙上的绿点也是它的推论（`wallActiveKey()`）：**同一间房，同一个来源**。
以前岛的形态看 `focusAlive`、绿点看 `wallActiveKey`，两套判据各说各话，
所以能出现「声音在响、墙上有绿点、岛却是时钟」。

## 动作

- **点岛** = 打开这一间的 App（`islandTap`）。播放页上岛是时钟，这条无处可点。
- **X** = 结束这一路（`closeFocus`）。文案按「这一下会得到什么」写。
- **星** = 关注 / 取消关注。
- **Esc / 播放页顶栏的返回钮** = 收起（App 退到后台，声音继续）。
- **播放页顶栏的 X** = 结束这一路（不留后台声音）。

## 过渡：一律硬切

播放页和其它表面之间**没有任何动画**。进播放页、离开播放页、换台，全是
`Router.Transition = ui.TransitionNone` 之后立刻换路由。

2026-11-28 之前这里有三套并存的做法：元素级的 `Match + Fly`（墙格 ↔
播放页）、整页 `AnimatePose` 等比缩放（岛 / 卡片）、手搓的飞行块。每一套
都要在两帧之间对齐几何，各带一个跨帧状态机；三套一起放时它们互相踩，
用户先后报的「在岛那里点进去，为什么是从墙上放大的」「放大 → 已经有直播
画面 → 模糊画面 → 显示画面」「岛和墙上一起飞」都是这么来的。全拆了。

为什么没有一条能留下来：

- **`Match + Fly` 的 `ScaleContent` 是非等比缩放**
  （`third_party/mygo/ui/match.go:472-486`，`kx = w/e.w`、`ky = h/e.h`）。
  岛是 `288×38`（7.6:1），舞台是 `1200×760`（1.58:1），等比不了——用它
  会把画面拉扁。
- **不加 `ScaleContent` 时飞行元素每帧重新排版**
  （`ui/transition.go:862-874` 的 `default` 分支走 `layoutBox`）：岛那种
  形状里 16:9 画面按 Contain 只剩中间一条，四周全是模糊底，那就是「画面 →
  模糊 → 画面」的来源。
- **整页 `AnimatePose` 能修掉上面两条**（等比、只排版一次），但它要求
  退场时「先缩、后换路由」，也就是把换路由推迟一整个动画时长。推迟带来的
  是一整类新状态：摆位期间的路由、源点、谁挂身份、输入怎么处理。
- **`Exit` Motion 走不通**：换路由那一帧播放页根本不会被构建
  （`ui/router.go` 的 `TransitionNone` 分支只建当前页），`Exit` 没有
  机会执行；ghost 副本画的也只是最后一帧的定格。

现在这条路上只有一个不变式：**换表面的那一帧，新页面的第一帧就是最终版面。**

### 谁都不许挂 `Match`

`Match` 在 MyGo 里是「跨帧共享身份」——匹配器只看上一帧同一个值出现在哪
（`ui/match.go:562-566`），完全不看调用方想干什么。所以只要两块表面同时
带着同一个身份，它就会自己把其中一块飞起来。

现在没有还原点可对齐，所以**一块都不许挂**。`TestNoSurfaceCarriesMatch`
直接盯 `player.go` / `watch.go` / `island.go` / `home.go` 的源码里
有没有 `Match:`。

### 岛为什么不参与

岛每一帧都在构建，它不是「正在出现的元素」，`ElementTransition` 表达不了
它；而且两端都挂 `Fly` 时源元素自己也会飞
（`third_party/mygo/ui/match.go:794`「still built, it flies itself」）。
岛的宽度只由 `AnimateTo("island-w", …)` 一条弹簧驱动，和路由无关。
## 路由

| 动作 | 处理 |
|---|---|
| 点岛 / 点卡片 / 点关注行 / 双击墙格 / 搜索结果 | `openRoom(room)`：`setFocus` → `TransitionNone` → 推路由 |
| Esc / 左上返回 / 顶栏 tab（在播放页） | `collapsePlayer()`：焦点留着，当场换路由 |
| 岛上的 X | `closeFocus()`：结束这一路，当场换路由 |
| 已在播放页换台 | `Router.Replace`，不叠历史 |

所有入口都汇到 `openRoom` / `collapsePlayer` / `closeFocus` 三个函数，调用点
不再关心「从哪一块表面进来」——没有过渡可选了。

## 一帧只有一个 `ui.Video`

一个 `ui.VideoPlayer` 只能被一个 `ui.Video` 画出来；两个元素同时画，实测只有
一个出画、另一个是黑的。`videoOwnerOf(key)`（`internal/ui/surface.go`）是
唯一的裁决点，返回 `stage / tile / none`：

- 焦点这一间在播放页归舞台；
- 否则在观测墙上归格子；
- 否则没有表面在画它（后台收听）。

墙格的状态提示也跟着这条走：画面归舞台的那一帧，格子不报「缓冲中…」。

## 播放意图

`setFocus(s, intent)` 有两个意图：

- `intentListen`（卡片 / 关注行 / 墙格「在墙上听」）：点开就是要听。
- `intentRestore`（从岛回去）：接着刚才那样——暂停的仍暂停，静音的仍静音。

## 怎么验证

单测在 `internal/ui/island_*_test.go`：岛的矩形在各路由之间不变、点岛时源点是
岛、活动判据只有一个、一帧只有一个 `ui.Video`、从岛回去不推翻暂停与静音、
墙格与岛同源。

真窗口用 `make e2e` 出的构建 + automation：

```sh
MYGO_AUTOMATION=<socket> ALIVE_DRAWN_MENUS=1 build/e2e/alive-e2e.exe &
mygo-automation open
```

`ALIVE_DRAWN_MENUS=1` 让窗口自己画弹出菜单（`Context.SetMenuPresentation(ui.PopoverMenus)`）：
系统菜单（Windows 上是 Win32 弹出菜单）不在这套自绘元素树里，automation 够不着；
画在窗口里的菜单，条目就是元素，`snapshot` 看得见、`press` 点得到。没有它，
"右键卡片 → 悲鸣观测"这条链在 e2e 里驱动不了。

要看的三条：四种状态下岛的 `bounds=`；点岛 / Esc 之后它是否逐字不变；墙上那
一格 `listItem` 的 `checked`（= 读屏能听出哪一格在响）。

## 刻意偏离 iPhone 的地方

1. **长按展开成 expanded 没有做。** iPhone 上长按 compact 会展开大卡片，
   向内滑收起。我们只有一个形态（38 高的胶囊），compact 那条放不下头像 +
   标题。要做就得先有第二套排版，那是 UI 改动。
2. **墙上其余格子不启用 minimal 呈现。** 它们是 App 切换器里的静音预览，
   不是第二条 Live Activity。
3. **两种全屏下不画岛**：满屏画面上只留返回钮和控制条。
4. **观测墙上单击一格是「切声音焦点」，不是打开 App。** iPhone 切换器里
   单击卡片就是打开；这里保留了「在墙上听」这个独立动作（双击才进播放页），
   因为观测墙本来就承担了一屏预览多路。

## 一手依据

- Apple HIG · Live Activities：compact / minimal / expanded 三种呈现，长按
  展开，点开 App，最大 2 秒的过渡动画；**App 打开时它自己的 Live Activity
  不占岛**。
  <https://developer.apple.com/design/human-interface-guidelines/live-activities>
- Dynamic Island 四种手势：点开 App、长按展开、向内滑收起、横向滑切换。
  <https://rottenwifi.com/how-to-use-the-dynamic-island-on-iphone-16-practical-ways/>
- `third_party/mygo/docs/ui/transitions.md:274-341` —— `Match` / `Fly` 的确切
  语义（从上一帧那块矩形过来；`Fly` 时源元素跟着飞并淡出）。
- `third_party/mygo/ui/match.go:562-566`、`:794` —— 起飞要求源元素上一帧带
  身份（**只看 Match 值相等，不看调用方的意图**）；源元素仍被构建时
  「flies itself」。
- `third_party/mygo/docs/ui/motion.md:20` —— `AnimateTo` 的值第一次就以目标
  为准，所以「从起点飞出来」不能用它起手。
- `third_party/mygo/docs/ui/navigation.md:155-177` —— `TransitionNone` /
  `TransitionMatch` 的区别。
- `third_party/mygo/docs/ui/testing.md`、`docs/plugins/automation.md` —— 判据
  与真窗口取证的写法。
