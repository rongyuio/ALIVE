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

// trackRoute 记下路由变化，给退出过渡判断"从哪一页进来的"。
//
// 必须在这里统一记，不能在各条进入播放页的路径上各记一次：进入播放页有
// 四条路（首页卡片、关注行、搜索、墙格），散着记一定漏，而漏掉的那条会
// 退化成硬切。每帧调用一次，幂等。
func (a *App) trackRoute() {
	if a.Router == nil {
		return
	}
	path := a.Router.Path()
	if path == a.lastRoute {
		return
	}
	if path == routePlayer {
		a.playerFrom = a.lastRoute
		// 新的一次进入：上一次退出留下的记号不能带进来，否则这一趟会
		// 从一个早就关掉的会话那里起飞。
		a.exitMatchKey = ""
	}
	a.lastRoute = path
}

// exitPlayer 结束这一路焦点，退回上一页。三个退出入口共用。
//
// 岛上的 X、Esc、全屏时左上角那颗按钮要的是同一件事，所以只有一个入口。
//
// 顺序是这个函数的全部内容：**先决定这一趟要不要连续，再结束焦点**。
// closeMedia 会把焦点清空，而连续判据依赖焦点——先 close 再判，就永远
// 飞不起来。这不是"忘了调 pushRoute"，是结构上飞不起来，所以修在顺序上。
//
// 只有退回去的那一页确实还画着这一间（从墙格放大进来的），才连续。从列表
// 进的播放页退回首页时首页没有对应的画面可接，硬切才是对的——给内容层加
// 交叉淡化违反 0010。
func (a *App) exitPlayer() {
	if a.Router == nil {
		return
	}
	key := a.focusKey()
	if key != "" && a.playerFrom == routeFollows && a.watchHas(key) {
		// 焦点会在下面被清掉，舞台要凭这个记号继续带 Match 身份，
		// 否则源元素在退出那一帧消失，Match 没有起点可飞。
		a.exitMatchKey = key
		a.Router.Transition = ui.TransitionMatch
		a.routeTransitionUntil = time.Now().Add(routeTransitionDur + 120*time.Millisecond)
	}
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
	a.exitMatchKey = ""
	if a.Router != nil {
		a.Router.Transition = ui.TransitionNone
	}
}

// stageMatchKey 是播放舞台这一刻该带的 Match 身份。
//
// 两个来源，顺序固定：退出途中的记号优先（那时焦点已经被 closeMedia 清掉，
// 但舞台还要飞），否则就是焦点会话。返回空串表示这一帧的舞台不参与过渡。
func (a *App) stageMatchKey() string {
	if a.exitMatchKey != "" {
		return a.exitMatchKey
	}
	return a.focusKey()
}
