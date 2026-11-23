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
	// 又进播放页：上一次「离开播放页」的收尾还没演完就取消，别把刚接上
	// 的这一路当后台收听 detach 掉。
	if target == routePlayer {
		a.exitDetach = false
	}
	a.Router.Push(target)
}

// exitPlayer 结束这一路焦点，退回上一页。岛上的 X、全屏时左上角那颗按钮
// 走这里：明确退出，关引擎、断弹幕，不留后台声音。
//
// Esc 不走这里——它是「收进岛」（见 collapsePlayer）：同一间房换个形态
// 继续放，不是结束。
//
// 退出也是连续过渡，而且**引擎要留到过渡结束才放手**。这是唯一能让收缩
// 动画有画面的排法：
//
//   - Router 在淡出期间会重建旧页面（播放页）。如果这时媒体层已经松手，
//     旧页面那一侧没有引擎可画，第 0 帧就是黑的（帧探针实测 0,0,0）；
//   - 留着引擎，旧页面的舞台继续画这一路，新的墙格画的是**同一个
//     roomSession** 的同一个播放器，两边的画面天然一致。
//
// 放手延迟到 tickRouteTransition 到点（280+120ms）。playback.Options 的
// 引擎生命周期归 *ui.VideoPlayer，延迟这几百毫秒只是晚一点收声。
//
// 判据要在 closeMedia 之前取——焦点是连续过渡的前提。
func (a *App) exitPlayer() {
	if a.Router == nil {
		return
	}
	key := a.focusKey()
	fly := key != "" && a.watchHas(key) && a.media.eng() != nil
	if !fly {
		// 退回去的那一页没有这一间的画面可接（从列表进的播放页退回首页）。
		// 给内容层加交叉淡化违反 0010，硬切才是对的。
		a.Router.Transition = ui.TransitionNone
		a.closeMedia()
		a.Router.Back()
		return
	}
	a.Router.Transition = ui.TransitionMatch
	a.routeTransitionUntil = time.Now().Add(routeTransitionDur + 120*time.Millisecond)
	// 到点再放手，见 tickRouteTransition。
	a.exitRelease = true
	a.exitDetach = false
	a.Router.Back()
}

// collapsePlayer 是 Esc / 点岛的第一档：离开播放页，这一路留着。
//
// 和 exitPlayer 的区别只在演完之后的放手方式：这里 detach 而不是 close——
// 引擎、取流、签了名还没过期的地址、房间记号全都留着，岛继续显示这一路，
// 点它回来接着看。
//
// 退回的是**打开播放页之前那一页**——从观测墙点进来的回墙，从关注页、
// 首页卡片点进来的回那一页。不能因为这一间恰好在观测盒里就跳去墙：用户
// 离开的根本不是那面墙。
//
// 退回观测墙时是连续过渡：上一帧的舞台带着 Match 身份，回去那一页里同一间
// 的墙格也带着，画面就缩回那一格。退回列表页（首页卡片、关注行）时不飞：
// 那里没有和舞台同形的元素，把整张卡片拉成全屏再缩回去，看到的是一块白板
// 盖住内容——那种情况走页面过渡。
func (a *App) collapsePlayer() {
	if a.Router == nil {
		return
	}
	if a.focusKey() == "" {
		// 没有声音焦点：岛上画的是时钟，没有可以收起的那一条。
		a.Router.Transition = ui.TransitionNone
		a.Router.Back()
		return
	}
	// 只有退回去那一页可能画着同一间（观测墙）才值得这两拍。退到首页卡片、
	// 关注行时那边没有能接住的元素，飞行没有终点——发一块白板盖住列表，
	// 比直接淡过去更糟；那种情况当场换路由。
	if a.FollowsSection != followsSectionWatch {
		a.finishCollapse()
		return
	}
	// 连按不抢跑：第一拍只让舞台带上 Match 身份，等 tickExitArm 把两趟
	// 走完再换路由；中间再按一次什么都不做。
	if a.exitArm {
		return
	}
	a.exitArm = true
	a.exitArmAt = a.buildSeq
	a.invalidateWindows()
}

// tickExitArm 是「离开播放页」的第二拍：上一帧的舞台已经带上身份，
// 这一帧换路由，退回去那一页里的同一间从舞台的矩形收回去。
//
// 每帧在 Shell 开头、构建之前调用：这一帧的 Router 就会同时建出离开的
// 播放页（带身份）和回去的那一页。
func (a *App) tickExitArm() {
	// 等两趟：第一趟只是把身份挂上（Transition 新建的那一帧不参与匹配），
	// 第二趟才把它记进「上一帧在哪」，第三趟换路由才有起点。
	if !a.exitArm || a.buildSeq < a.exitArmAt+2 {
		return
	}
	a.exitArm = false
	a.finishCollapse()
}

func (a *App) finishCollapse() {
	if a.Router == nil || a.Router.Path() != routePlayer {
		return
	}
	if a.focusKey() == "" {
		a.Router.Transition = ui.TransitionNone
		a.Router.Back()
		return
	}
	a.exitRelease = false
	// 演完再放手：过渡期间呈现尺寸还得是全尺寸，提前收成格子就糊了。
	a.exitDetach = true
	a.Router.Transition = ui.TransitionMatch
	a.routeTransitionUntil = time.Now().Add(routeTransitionDur + 120*time.Millisecond)
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
	if a.exitRelease {
		// 退出过渡演完了：现在才让媒体层放手，墙格接手继续画同一个播放器。
		a.exitRelease = false
		a.closeMedia()
	}
	if a.exitDetach {
		// 收进岛演完了：只关呈现，引擎和声音留着。
		a.exitDetach = false
		a.detachMedia()
	}
}

// matchable 报告此刻能不能给元素挂 Match。
//
// 刚改过窗口尺寸的一小段时间里不能：带 Match 的元素在 resize 时会做尺寸
// 动画，整墙的格子会一帧帧弹过去、视频每帧重新适配（用户报的「最大化会闪」）。
// 窗口稳定之后恢复，飞行的起点就又有了。
func (a *App) matchable() bool {
	return time.Since(a.resizedAt) > matchSettleAfter
}

// matchSettleAfter 是窗口改尺寸之后多久才重新挂 Match。
//
// 比一次布局重排宽裕得多，又比用户点下一格快：实测 resize 的尺寸动画持续
// 十几帧（约 200ms）。
const matchSettleAfter = 400 * time.Millisecond

// noteWindowSize 记下窗口尺寸变化，供 matchable 判断。每帧调用。
func (a *App) noteWindowSize(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	if a.lastWindowW != w || a.lastWindowH != h {
		a.lastWindowW, a.lastWindowH = w, h
		a.resizedAt = time.Now()
	}
}

// stageMatchKey 是播放舞台这一刻该带的 Match 身份：焦点会话的 key。
//
// 只用于进场（墙格 → 播放页）。退出是硬切，舞台不参与。
func (a *App) stageMatchKey() string {
	return a.focusKey()
}
