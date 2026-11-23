package ui

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// 表面之间的过渡。
//
// 三处"没有动画"是三件事，不能互相牵连（见 doc/architecture/surfaces.md）：
//
//   - 顶栏 tab / 分区之间：不动。方向模型与平级路由不匹配（d488013）。
//   - 内容层（卡片网格）：永不位移、永不淡入（0010）。在用户正在读的内容上
//     施加位移，一定会被读成"画面在抖"。
//   - 表面之间（墙格 ↔ 播放页）：做连续过渡。这不是"在内容上动"，是
//     "同一间房换位置"。
//
// 所以 Router.Transition 不能是一个全局开关——它得按次决定。这个文件把它
// 收在一处：调用点只说"这次要连续"，什么时候开、什么时候关、什么时候
// 恢复都不散到外面。

// surfaceMatch 是共享元素的身份：同一间房在墙格与播放页上是同一个值。
//
// 用自定义类型而不是字符串，避免和别的 Match 撞身份（MyGo 文档的建议）。
type surfaceMatch struct{ key string }

// routeTransitionDur 是表面之间过渡的时长。
//
// 280ms 和 theme 的 EaseOut 一档对齐。比顶栏滑块的 220ms 长一点：
// 那是"换个选项"，这是"同一间房换个位置"，位移量级大得多。
const routeTransitionDur = 280 * time.Millisecond

// pushRoute 走一条表面之间的过渡。
//
// 只有"墙格 ↔ 播放页"这一对需要连续：两边画的是同一帧画面，硬切等于
// 骗人。其余 Push 保持 TransitionNone——顶栏 tab 是平级页面，方向模型
// 不匹配，滑起来只会时滑时淡。
//
// 转场本身由 Router 驱动，恢复靠 routeTransitionUntil 记的截止时刻，
// 在 Shell 每帧的 tickRouteTransition 里收尾。不用定时器：窗口最小化时
// 定时器还会跑，而那时根本没有帧要画。
func (a *App) pushRoute(target string) {
	if a.Router == nil {
		return
	}
	if a.continuousRoute(target) {
		a.Router.Transition = ui.TransitionMatch
		a.routeTransitionUntil = time.Now().Add(routeTransitionDur + 120*time.Millisecond)
	}
	a.Router.Push(target)
}

// exitPlayer 结束这一路焦点，退回上一页。三个退出入口共用。
//
// 岛上的 X、Esc、全屏时左上角那颗按钮要的是同一件事，所以只有一个入口。
//
// 退出是硬切，不做页面过渡。不是懒：一个 VideoPlayer 只能被一个 ui.Video
// 画出来（两个元素只有一个出画），而退出时画面必须已经交回墙上的格子——
// 播放舞台这一侧就没有引擎可画了。TransitionMatch 会在淡出期间重建旧页面，
// 那一帧舞台的 player 是 nil，整块闪成封面或黑底（帧探针实测第 0–2 帧全黑，
// 之后才淡入）。硬切则新墙的第一帧就带着同一路画面，没有空窗。
//
// 进场（墙格 → 播放页）仍然连续：那边画面在进来的舞台上，出去的是墙，
// 飞行元素有东西可画。
func (a *App) exitPlayer() {
	if a.Router == nil {
		return
	}
	// 进场后 400ms 内就退出时，路由器可能还停在 TransitionMatch。
	a.Router.Transition = ui.TransitionNone
	a.closeMedia()
	a.Router.Back()
}

// continuousRoute 报告这次跳转该不该连续。
//
// 判据只有一条：目标或来源是播放页，而且这一间房在两边都有画面可接。
// 从墙格放大进去、从播放页退回墙上，都是同一间房换位置。
func (a *App) continuousRoute(target string) bool {
	if a.Router == nil {
		return false
	}
	from := a.Router.Path()
	if from != routePlayer && target != routePlayer {
		return false
	}
	// 没有画面可接时不必连续：取流还没回来，飞过去也是一张封面。
	return a.focusKey() != ""
}

// tickRouteTransition 在转场结束后把路由器恢复成"不播页面过渡"。
//
// 每帧调用，幂等。恢复得晚一点（+120ms）而不是卡着 280ms：Router 自己的
// 淡出还在收尾，提前切回 None 会把旧页面直接抽走，看起来是闪一下。
func (a *App) tickRouteTransition(now time.Time) {
	if a.routeTransitionUntil.IsZero() {
		return
	}
	if now.Before(a.routeTransitionUntil) {
		return
	}
	a.routeTransitionUntil = time.Time{}
	if a.Router != nil {
		a.Router.Transition = ui.TransitionNone
	}
}

// stageMatchKey 是播放舞台这一刻该带的 Match 身份：焦点会话的 key。
//
// 只用于进场（墙格 → 播放页）。退出是硬切，舞台不参与。
func (a *App) stageMatchKey() string {
	return a.focusKey()
}
