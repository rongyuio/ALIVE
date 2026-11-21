package ui

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 「换了一个」这件事在这个应用里有四个尺度，从大到小是：
// 平台 tab、一级分区、二级分区、三级分区。它们结构相同——一排并列的选项，
// 其中一个被选中——所以在动效上也应该只有一套语言：
//
//   - 一枚滑块停在选中项下面。换选中项时它**滑过去**，不是旧的高亮灭掉、
//     新的亮起来。位置关系是连续的，用户的眼睛跟着一枚东西走。
//   - 滑块与标签着色用弹簧（theme.Slide），不是曲线：下一次点击要能从
//     当前位置、当前速度接着走，而不是让上一次动完。
//
// 内容层（卡片网格）不在这一套里：它不位移，也不整块淡入淡出。
//
// 这里曾经按方向整块换：Enter 从 dir 那侧进、Exit 往 -dir 那侧出，各 16 DIP，
// 用弹簧跑 500ms。两个问题叠在一起：MyGo 的 Motion 在不写 Opacity 时默认从
// **全透明**进场，而 16 DIP 的位移让整块内容在切换瞬间和分区栏错开——量出来
// 就是「画面不稳、有偏差」。方向这件事交给滑块就够了：它本来就在原地指出
// 「你刚点了哪一格」。内容层现在只换代（换 key），新旧内容直接交换。

// navFlip 让一块内容换代：代次加在 seq 指的那一个上。
//
// 三个内容区各有自己的代次——首页、关注页、订阅分区。只切其中一个，
// 别的区块的 key 不能跟着变：共用一份的话，在关注页切完平台回到首页，
// 首页会凭空换代一次，而它根本没有换内容。
func (a *App) navFlip(seq *int) {
	*seq++
}

// navKey 是一块内容的身份：代次变了才换 key，换掉的是一整棵子树。
func navKey(kind string, seq int) string { return fmt.Sprintf("%s-%d", kind, seq) }

// rowBox 是一排横放选项的几何：每一项的起点和宽度。
//
// 滑块的位置和宽度都从这里排出来，量法与 chipsOverflow 一致——文字宽度
// 加左右 padding，胶囊再加 2 的边框，超出上限就截断。列表换了一组，
// 重新量一次，滑块跨过整行的距离由 SlideFar 承担。
type rowBox struct {
	x, w []float32
}

// last 是最后一项的右边缘。
func (b rowBox) last() float32 {
	if len(b.x) == 0 {
		return 0
	}
	return b.x[len(b.x)-1] + b.w[len(b.w)-1]
}

// at 返回第 i 项的位置；越界时给零宽，滑块会缩回去而不是停在错的地方。
func (b rowBox) at(i int) (float32, float32) {
	if i < 0 || i >= len(b.x) {
		return 0, 0
	}
	return b.x[i], b.w[i]
}

// measureRow 量一列纯文字的选项（一级分区）。maxW 为 0 表示不限宽。
func measureRow(c *ui.Context, names []string, size, weight, padH, gap, maxW float32) rowBox {
	b := rowBox{x: make([]float32, len(names)), w: make([]float32, len(names))}
	var x float32
	for i, n := range names {
		w := textWidth(c, n, size, weight) + 2*padH
		if maxW > 0 && w > maxW {
			w = maxW
		}
		b.x[i], b.w[i] = x, w
		x += w + gap
	}
	return b
}

// measureChips 量一列胶囊选项（二级 / 三级分区）：多 2 的边框，有最大宽。
func measureChips(c *ui.Context, names []string, size, weight, padH, gap, maxW float32) rowBox {
	b := rowBox{x: make([]float32, len(names)), w: make([]float32, len(names))}
	var x float32
	for i, n := range names {
		w := textWidth(c, n, size, weight) + 2*padH + 2
		if maxW > 0 && w > maxW {
			w = maxW
		}
		b.x[i], b.w[i] = x, w
		x += w + gap
	}
	return b
}

// slider 是选中项下面那枚滑块。
//
// 它必须是 track 的第一个子元素：绝对定位不占流，后画的兄弟压在上面，
// 所以 tab 文字不会被遮住。索引为 -1（还没有选中项）时宽度收到 0，
// 弹簧会把它从上一个位置收回去，而不是凭空消失。
//
// key 在同一行里要唯一——三行分区共用一套组件，各自的 key 前缀不同。
func slider(c *ui.Context, key string, box rowBox, index int, h, radius float32, bg ui.Color) ui.Element {
	x, w := box.at(index)
	// ZIndex(-1)：绝对定位的兄弟本来画在所有流元素之上，滑块会把选中项
	// 的文字整块盖住。压到流元素下面，它就是一块底色。
	el := ui.Box(c).Absolute().Top(0).Height(h).Radius(radius).Background(bg).ZIndex(-1)
	el.Left(el.AnimateTo(key+"-x", x, slideFor(box)))
	el.Width(el.AnimateTo(key+"-w", w, slideFor(box)))
	// 索引越界（换了一组选项、还没选中任何一个）时收起来，别停在上一组的位置。
	opacity := float32(1)
	if w == 0 {
		opacity = 0
	}
	el.Opacity(el.AnimateTo(key+"-o", opacity, theme.Slide()))
	return el
}

// slideFor 按这一行有多长挑弹簧：两三格的 tab 用紧的，一屏之外的
// 分区列表用松的，否则长距离的位移会像被抽了一下。
func slideFor(box rowBox) ui.Animation {
	if box.last() > 420 {
		return theme.SlideFar()
	}
	return theme.Slide()
}

// tabIDs 是顶栏 tab 的顺序：关注，然后是可重排的平台。
//
// 订阅分区不再是顶栏 tab —— 它并进了关注页，是那颗星星下的一级分区。
//
// 顺序只有这一处定义：platformTabs 按它排，switchTab 按它算方向。
// 分成两份写过一次就会走样——重排之后高亮和滑向会对不上。
func (a *App) tabIDs() []string {
	ids := make([]string, 0, 1+len(a.PlatformOrder))
	ids = append(ids, "follows")
	return append(ids, a.PlatformOrder...)
}

// navFeedKey 是首页内容层的身份：换代就换 key，换到的是一棵全新的子树。
// 它必须跟着 navSeq 走，不能跟着数据走——数据刷新不该换代。
func (a *App) navFeedKey() string { return navKey("feed", a.navSeq) }
