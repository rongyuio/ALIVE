# Onevra

`github.com/oneo-me/onevra/source`，当前钉 `v0.2026.19`。它的 `go.mod` 要 `mygo v0.10.0`，
和 `go.mod` 一致——**引它不需要动框架版本**。

引入它升了 7 个版本号：`purego 0.11.1→0.11.3`、`x/crypto x/image x/net x/sys x/text` 各一档。
没有新增传递依赖。purego 那处对 `plugins/video` 的 mpv 回调只验过编译，没验运行期。

## 换过来了什么

| 位置 | 换成 |
|---|---|
| `internal/ui/icons.go` 17 个标准图标 | `icons/lucide`（形状逐字核对过；`Bookmark`/`Activity` 上游已圆角化，跟了上游） |
| 全部 `ui.Text` / `ui.Icon` / `ui.Image` / `ui.Scroll*` | `components.Text` / `Icon` / `Image` / `ScrollView`（零视觉变化，这四个就是上游的薄封装） |
| 9 处 `ui.PopoverBase`（播放页清晰度下拉除外） | `components.Popover` |
| `shortcuts_overlay.go` / `sync_panel.go` 的模态 | `components.Dialog` |
| Toast 视口 | `components.Toasts` |
| Spinner（`spinner.go` 整个删了） | `components.Spinner(SpinnerRing)` |
| 弹幕屏蔽词面板 | `components.TagInput` |
| 弹幕设置的开关 / 色块 / 滑杆 | `components.Switch` / `ColorPicker` / `Slider` |
| 头像 `avatarCircle` | `components.Avatar` |
| 关注页网格 | `components.GridView` |
| 弹幕面板列表 | `components.List` |

## 三条机制约束（都踩过）

**一、换面只能走 `Material`，写 `Background` 没有用。**
`components.Popover/Dialog/Toast` 默认铺 `glass.Glass{Tint: t.Background.Alpha(.92)}`，
而 MyGo 里材质盖过背景：`Material(glass{Tint:红})` 与 `Background(绿)` 同时设，画出来是红的，
顺序无关；`Material(nil)` 直接 panic。应用里想换成 `theme.MediaPanel` 那一族墨、或者
`BgElevated` 那种抬起面，必须也传一个 `glass.Glass`——`internal/ui/onevra.go` 的 `orPanel` 就是干这个的。

**二、按钮的 hover / pressed 在构造期定稿。**
`button.go:67-79`、`icon_button.go:45-58`、`toggle_button.go:17-24` 都在构造时读一次
`Hovered()`/`Pressed()` 就把底色写死。ALIVE 全站统一的弹簧 hover（`theme.HoverInk()` =
`Spring{460,42}`）在这些地方会降级成瞬时切换。事后 `.Background()` 能改颜色，但 hover 反馈就没了。

**三、尺寸锚在 `t.Space(n)` / `t.Rem(n)` 上，不是我们的手调值。**
`theme.Theme()` 给的是 `Spacing=4`、`FontSize=15`、`Radius=12`，于是：
`IconButton` = `max(32, 30)` = 32（compact 20）、`Switch` = 40×20（compact 30×16）、
`Dialog` = 440 宽、`ProgressRing` = 32 直径、`Badge` 字号 12.75、边框圆角 12。
构造函数里写死的 `.Size()` 可以用链式方法盖回（`IconButton` 的 24 就是这么验的，
见 `onevra_bridge_test.go`），但写在 `Children` 闭包里的**盖不回来**——
`IconButton` 的图标尺寸（`t.Rem(1.25)` = 18.75）、`Switch` 的拇指行程、`Slider` 的轨道位置
都是闭包里算的，改外壳只会让它们错位。

## 明明有对应组件却没用它的地方

- **`components.IconButton` 一处没用。** hover 面的圆角写死在 `p.Fill(r, face, t.Radius)`（`icon_button.go:57`），
  改元素圆角会把 hover 面画成「圆里套方块」；图标尺寸也在闭包里。我们的图标按钮是
  24/28/30/34/36/40 各种直径，胶囊、圆、直角都有。
- **`components.List` 只用在弹幕面板。** `components.ListState` 不导出 MyGo 的
  `NearEnd` / `EndReached` / `AtEnd`（`list.go:25-68` 只有 `ScrollTo/ScrollIntoView/ScrollToEnd/Visible/...`），
  首页网格的无限滚动靠这三个，换过去是功能退化。弹幕面板只要贴底判定，用 `Visible()` 自己算
  （`onevra.go` 的 `listAtEnd`）。
- **`components.TextBox` 一处没用。** 它是「输入框即框」的模型，没有框内右侧槽；
  搜索 / 分区搜索 / 关注搜索三个框里都有 spinner 和清空键。
- **`components.Tabs` 不能用，会 panic。** 三个变体都走 `slideBehind`，而它在
  `tabs.List.Draw` 回调里调 `active.LayoutBounds()`——`active` 是构建期存下来的 `ui.Element`，
  MyGo 的 `Draw` 在纯重绘帧里会复用，那时元素已过期：
  `panic: ui: element from pass N used in pass N+3`。页签保持自绘滑块。
- **`Card`、行、分割线、快捷键表、chips、播放器 chrome、灵动岛、观测墙**：上游没有。
  `components/flow.Card` 是流程图节点面，`CardStack` 是翻卡牌组，都不是 16:9 媒体卡。