package ui

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/media"
	"alive/internal/theme"
)

// 弹幕默认参数：
// 字号 20px、滚动穿过屏幕 10s、显示区域为画面上方 50%、不透明 1。
// 通道高度 = 字号 × 1.5 = 30。
const (
	danmuFontSize float32 = 20
	danmuDuration         = 10 * time.Second
	danmuChannel  float32 = 30
	danmuAreaFrac float32 = 0.5
	// danmuFlyMax 是画面上同时最多飞多少条；danmuLogMax 是右侧面板留多少历史。
	// 本来就是两个数：Overlay 只处理当帧，面板吃的是 messages
	//（MAX_MESSAGES = 300，渲染最后 200 条）。
	danmuFlyMax = 40
	danmuLogMax = 300
	danmuTick   = 33 * time.Millisecond

	// danmuStallGrace 是「画面没前进」的容忍时间。
	//
	// time-pos 按视频帧更新，实测 30fps 流上相邻两次间隔 20–40ms。短于这个
	// 宽限的「位置没变」只是采样间隔，弹幕照常按墙钟走；超过它才认定画面
	// 真的停住（暂停、缓冲、卡死），弹幕跟着冻住。
	danmuStallGrace = 250 * time.Millisecond
)

// danmuKind 是能上屏的两类弹幕。
//
// 进场提醒、礼物、系统公告都不在这里——那些不是用户想看的东西，
// 数据层就不会产出（见 live.DanmuKind）。
type danmuKind int

const (
	danmuChat danmuKind = iota
	// danmuSuperChat 是付费置顶留言：面板里单独占一块，且不上飞屏
	// （真实直播间的 SC 是钉在画面顶部的，跟着滚反而更怪）。
	danmuSuperChat
)

// danmuLine 是一条收到的弹幕。
//
// Track 在生成时定死，这样别的弹幕消失时它不会跳轨道；
// 面板只用 Text / Nick / Level / Kind，不关心 Track。
type danmuLine struct {
	// Seq 是收到顺序的序号，从 1 递增。它是这条弹幕的身份：列表拿它当行的
	// Key，日志被截断、前面的行被挤掉时，剩下每行的身份不会跟着位置漂。
	Seq   uint64
	Text  string
	Nick  string
	Level int
	// Medal 是粉丝牌；Name 为空表示没有。
	Medal live.Medal
	// Platform 决定正文里的 [表情名] 查哪张表情表。
	Platform live.Platform
	Kind     danmuKind
	// Emotes 是这条弹幕携带的表情图。Text 为空且 Emotes 非空时，
	// 整条弹幕就是一张图（B 站的纯表情弹幕）。
	Emotes []live.Emote
	// Color 是平台下发的原生字色（#RRGGBB），空串表示平台没给。
	// B 站、斗鱼、虎牙都会给：B 站和虎牙是 RGB 整数（白是 0xFFFFFF），
	// 斗鱼是 1–7 的调色板索引。抖音的聊天消息没有这个字段，永远是空。
	Color string
	// Price 是醒目留言的金额（元），0 表示普通弹幕。
	Price int
	// Background 是醒目留言的平台背景色（#RRGGBB），空串表示用强调色。
	Background string
	Track      int
	Born       time.Time
	Dur        time.Duration
}

// imageOnly 报告这条弹幕是不是「整条就是一张图」。
func (d danmuLine) imageOnly() bool {
	return strings.TrimSpace(d.Text) == "" && len(d.Emotes) > 0
}

// emoteSize 按目标高度算出表情图的显示宽高，保持原比例。
//
// 平台给的宽高偶尔是 0（老表情），这种按 1:1 处理，不会除以零。
func emoteSize(e live.Emote, height float32) (float32, float32) {
	w, h := float32(e.Width), float32(e.Height)
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	return height * w / h, height
}

// danmuColor 是一条弹幕正文该用的颜色。
//
// 平台给了原生字色且「彩色弹幕」开着时用平台色（B 站大会员彩色弹幕、
// 斗鱼调色板、虎牙自定义色）；其余情况用 fallback。
//
// 平台的白（#ffffff / #FFFFFF）是「没选颜色」的默认值，不能直接铺上去：
// 飞屏还行，面板在浅色主题下会变成白字压白底。所以白一律换算成 fallback——
// 飞屏传设置里的字色，面板传主题正文色。
func danmuColor(d danmuLine, colorful bool, fallback ui.Color) ui.Color {
	if !colorful || d.Color == "" || strings.EqualFold(d.Color, "#ffffff") {
		return fallback
	}
	// ui.Hex 遇到坏值会 panic，而 d.Color 是平台下发的原始字符串。现在四个
	// 平台都把它规整成 #RRGGBB，所以打不崩；但那是"上游替你校验"的运气，
	// 不是这一层的不变量。任何新平台/新字段一透传原始字符串就是必崩。
	// 同一个仓里 SC 背景色早就先过 validHexRGB 了（danmu_panel.go）。
	if !validHexRGB(d.Color) {
		return fallback
	}
	return theme.HexColor(d.Color)
}

// danmuStickerScale 是纯图弹幕（房间表情、收藏集表情贴纸）相对字号的倍率。
//
// 这类弹幕整条就是一张图，网页端画得比一行字高得多（约 3 倍字号），
// 和正文里混排的小表情不是一回事。按 1.3 倍画出来太小，远看像缩略图。
const danmuStickerScale float32 = 2.8

// danmuState 是推动器的并发标志：active 由主线程写，循环读；
// loop 保证同时只有一个 goroutine 在跑。
type danmuState struct {
	active  atomic.Bool
	running bool
	// startMu 保护 active/running 的迁移（ensureDanmuLoop / stopDanmuLoop / 循环退出）。
	startMu sync.Mutex
	// seq 是 danmuLine.Seq 的发号器，只在主线程里读写。
	seq uint64
}

// ensureDanmuLoop 保证弹幕推动器在跑。
//
// 只有播放页需要连续帧：33ms 一次让主线程按时间重算位置。它的生命周期绑在播放页
// 那个元素上（OnUnmount → stopDanmuLoop），离开页面或关窗时由框架收掉，不再靠
// 轮询路由自己发现。
//
// 弹幕关着时不需要这个 ticker：tickDanmu 只维护画面上正在飞的那批
// （见 tickDanmu），而右侧面板读的是 DanmuLog、由每帧的视图重建直接算出来，
// 新弹幕到达本身就会经 runOnMain 触发一次重建——面板不要推动器。
func (a *App) ensureDanmuLoop() {
	if a.Win == nil {
		return
	}
	// 状态迁移全在这把锁里做：active / running 只在持锁时改。
	//
	// 原来是「active=true 然后 CAS(loop false→true)」，循环体每拍只看 active、
	// 退出时把 loop 放回 false。stop→start 若发生在旧 goroutine「已读到
	// active=false、还没跑到 defer」的那三十几毫秒里，CAS 失败直接 return，
	// 随后旧循环退出并置 loop=false——**没有任何 goroutine 在跑而 active 为
	// true**。恢复全靠下一次构建再调一次；播放页停止重建（窗口被遮挡、暂停）
	// 时弹幕就冻住。
	a.danmu.startMu.Lock()
	defer a.danmu.startMu.Unlock()
	if !a.DanmuOn {
		a.danmu.active.Store(false)
		return
	}
	a.danmu.active.Store(true)
	if a.danmu.running {
		return // 已经有循环在跑，它会继续看到 active=true
	}
	a.danmu.running = true
	win := a.Win
	go func() {
		ticker := time.NewTicker(danmuTick)
		defer ticker.Stop()
		for range ticker.C {
			if a.danmu.active.Load() {
				win.Update(func() { a.tickDanmu() })
				continue
			}
			// 要退出：必须在锁里再确认一次 active。否则 start 可能刚刚在锁外
			// 把它设回 true，而这一拍已经决定退出——又是一个没人跑的推动器。
			a.danmu.startMu.Lock()
			if a.danmu.active.Load() {
				a.danmu.startMu.Unlock()
				continue
			}
			a.danmu.running = false
			a.danmu.startMu.Unlock()
			return
		}
	}()
}

// stopDanmuLoop 停下推动器：把 active 放下，正在跑的循环下一拍退出。
// 由播放页元素的 OnUnmount 调用——谁起的活谁收。
func (a *App) stopDanmuLoop() {
	a.danmu.startMu.Lock()
	a.danmu.active.Store(false)
	a.danmu.startMu.Unlock()
}

// danmuTime 是弹幕当前用的时间，第一次用到时用注入的时钟落一个初值。
func (a *App) danmuTime() time.Time {
	if a.danmuNow.IsZero() {
		a.danmuNow = a.clock()
	}
	return a.danmuNow
}

// danmuAdvance 决定这一拍弹幕时钟该走多少。
//
// 画面在前进时一律按墙钟 dt 走。time-pos 只按视频帧率更新：实测 30fps 流上
// 约 30 次/秒，相邻两次间隔 20–40ms 不等，每次整跳 33.33ms。拿它的增量当
// 步长，60fps 的界面就会一半的帧不动、动的帧也只走被夹短的一小截——肉眼看到
// 的「顿」就是这么来的。1x 播放时墙钟和播放时钟同速，用墙钟做步长既均匀，
// 也不会累积漂移。
//
// 位置没变时先容忍：still 是画面连续没前进的累计时长，短于 danmuStallGrace
// 只是采样间隔，继续按墙钟走；超过它才认定真停顿（暂停、缓冲、卡死）并冻结。
// 用户自己按了暂停就不用等宽限，当场停住。位置前跳（seek / 换路）和后退都
// 只更新基准，不补时长，弹幕不会瞬移。
func danmuAdvance(dt time.Duration, hasPlayer, playing bool, pos, lastPos, still time.Duration) (add, newStill time.Duration) {
	if dt <= 0 {
		return 0, still
	}
	if !playing {
		return 0, 0
	}
	if !hasPlayer {
		return dt, 0
	}
	if pos != lastPos {
		return dt, 0
	}
	still += dt
	if still > danmuStallGrace {
		return 0, still
	}
	return dt, still
}

// advanceDanmuClock 把弹幕时钟往前推。
//
// 判据是「画面有没有真的前进」，不是墙钟：位置不动（暂停、缓冲、卡死）时
// 冻结，前进时按墙钟均匀推进。没有播放器时（还在取流）才直接看播放态。
func (a *App) advanceDanmuClock() {
	now := a.clock()
	if a.danmuLast.IsZero() {
		a.danmuLast = now
		return
	}
	dt := now.Sub(a.danmuLast)
	a.danmuLast = now
	if dt <= 0 {
		return
	}
	if p := a.media.eng(); p != nil {
		pos := p.Position()
		add, still := danmuAdvance(dt, true, a.focusPlaying(), pos, a.danmuPos, a.danmuStill)
		a.danmuPos, a.danmuStill = pos, still
		a.danmuNow = a.danmuNow.Add(add)
		return
	}
	add, _ := danmuAdvance(dt, false, a.focusPlaying(), 0, 0, 0)
	a.danmuNow = a.danmuNow.Add(add)
}

// danmuLane 是一条轨道上最后一条弹幕的参数。
//
// 轨道分配要判断的是「后一条会不会追上前一条」，所以必须记住前一条的出生
// 时刻和宽度：宽度决定速度（v = (屏宽+宽度)/时长），也决定入场要走多久。
type danmuLane struct {
	at time.Time
	w  float32
}

// takeDanmuTrack 给一条新弹幕分轨道。
//
// 从上往下找**第一条放得下的轨道**，不是先找空轨：空轨当然放得下，已经飞了
// 一段、能安全接力的同轨也放得下。跳过能用的轨道硬往下排，屏幕上就会排成
// 一条固定斜线。
//
// 「放得下」有两条判据，缺一不可：
//  1. 入场不重叠：前一条的尾巴已经完全进入画面；
//  2. 不追尾：后一条不会在前一条离场前追上它。
//
// 第 2 条只在后一条更快（更宽）时才会失败。照着当前速度模型
// v = (S+w)/D 化简后就是 w2*(D-dt) <= S*dt，不用逐步模拟。
//
// 所有轨道都放不下时才叠，挑最早能空出来的那条：宁叠不丢。
func (a *App) takeDanmuTrack(now time.Time, w2 float32) int {
	n := a.danmuTracks
	if n < 1 {
		n = 14
	}
	if len(a.danmuLanes) < n {
		a.danmuLanes = append(a.danmuLanes, make([]danmuLane, n-len(a.danmuLanes))...)
	}
	S := float64(a.danmuStageW)
	if S <= 0 {
		S = 1200
	}
	D := a.DanmuDur.Seconds()
	if D <= 0 {
		D = 10
	}
	dur := time.Duration(D * float64(time.Second))

	for i := 0; i < n; i++ {
		l := a.danmuLanes[i]
		if l.at.IsZero() || !now.Before(l.at.Add(dur)) { // 上一条已经完全离开
			a.danmuLanes[i] = danmuLane{at: now, w: w2}
			return i
		}
		dt := now.Sub(l.at).Seconds()
		if dt <= 0 {
			continue
		}
		v1 := (S + float64(l.w)) / D
		if dt < float64(l.w)/v1 { // 前一条还没完全进入
			continue
		}
		if float64(w2)*(D-dt) <= S*dt { // 不会追尾
			a.danmuLanes[i] = danmuLane{at: now, w: w2}
			return i
		}
	}

	// 所有轨道都排不下：叠在最早能接力的那条上。
	best := 0
	for i := 1; i < n; i++ {
		if a.danmuLanes[i].at.Before(a.danmuLanes[best].at) {
			best = i
		}
	}
	a.danmuLanes[best] = danmuLane{at: now, w: w2}
	return best
}

// danmuWidthEstimate 在入队时估一条弹幕的滚动宽度。
//
// 分配器需要它来判断入场时间与追尾；入队时没有排版上下文，只能按字符宽度
// 推。中文/全角按一个字号，ASCII 按 0.55，再留 15% 余量——估窄了会放出会
// 追尾的弹幕，估宽了只是轨道用得稀一点。
func danmuWidthEstimate(d danmuLine, font float32) float32 {
	if d.imageOnly() {
		w, _ := emoteSize(d.Emotes[0], font*danmuStickerScale)
		return w
	}
	var w float32
	for _, r := range d.Text {
		if r < 0x2E80 {
			w += font * 0.55
		} else {
			w += font
		}
	}
	for _, e := range d.Emotes {
		ew, _ := emoteSize(e, font)
		w += ew
	}
	return w * 1.15
}

// tickDanmu 在主线程推进弹幕：清掉过期的，按间隔补新的。
func (a *App) tickDanmu() {
	a.advanceDanmuClock()
	now := a.danmuTime()
	// 新分配，不原地压缩：danmuLayer 在构建路径里直接 range a.Danmu，
	// 两者虽然都在主线程，但一次顺序调整就会变成"边遍历边左移"。
	live := make([]danmuLine, 0, len(a.Danmu))
	for _, d := range a.Danmu {
		if now.Sub(d.Born) < d.Dur {
			live = append(live, d)
		}
	}
	a.Danmu = live
}

// danmuLayer 是画面上的弹幕层：每条从右边缘匀速穿到左边缘外。
//
// 轨道与位移都在这里算：
// 区域为画面上方 50%，通道 30px；滚动时长 10s 穿过整屏。
func (a *App) danmuLayer(c *ui.Context) {
	if !a.DanmuOn {
		return
	}
	// 位置每帧重算。33ms 的 tick 只负责补充与清理；如果时钟只在 tick 里
	// 推进，弹幕就是 30Hz，配 60fps 的画面看起来像掉帧。
	a.advanceDanmuClock()
	w, h := c.Size()
	// 画面矩形 = 窗口 − 顶栏（全屏时没有顶栏）− 左右 padding（全屏时没有）
	// − 右侧弹幕面板。面板在系统全屏下不画（见 PlayerPage），宽度也就不能扣。
	stageW, stageH := w, h
	if !a.playerFullscreen() {
		stageW -= 2 * a.bodyPadX(c)
		stageH -= theme.NavbarHeight
	}
	if a.DanmuPanelOn && !a.sysFullscreen() {
		stageW -= a.panelWidth()
	}
	if stageW <= 0 || stageH <= 0 {
		return
	}
	tracks := int(stageH * a.DanmuArea / danmuChannel)
	if tracks < 1 {
		tracks = 1
	}
	// 记下这一帧的真实轨道数和 stage 宽度：新弹幕入队时按它们分配轨道。
	// 视图每帧重建，这两个值只是给入队路径用的观测缓存。
	a.danmuTracks = tracks
	a.danmuStageW = stageW

	// 没有在飞的弹幕也要先把轨道数记下来：第一条弹幕入队时就按真实窗口
	// 分配，不然会先按默认的 14 条排一轮，小窗口里立刻重叠。
	if len(a.Danmu) == 0 {
		return
	}

	now := a.danmuTime()
	// 用四边绝对定位铺满 stage：Fill() 在 Box 里不会撑开高度，
	// 那样弹幕会以塌缩后的容器中心定位，整层掉到画面中部。
	layer := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Clip()
	layer.Children(func() {
		for _, d := range a.Danmu {
			p := danmuProgress(now, d.Born, d.Dur)
			if p < 0 {
				continue
			}
			if p > 1 {
				p = 1
			}
			// 纯图弹幕按图宽算滚动距离，文字弹幕按文字宽：
			// 两者都要「从右边缘完全进来到完全出去」。
			var tw, imgW, imgH float32
			var rs danmuSpans
			if d.imageOnly() {
				imgW, imgH = emoteSize(d.Emotes[0], a.DanmuFont*danmuStickerScale)
				tw = imgW
			} else {
				rs = buildDanmuSpans(d.Platform, d.Text, d.Emotes, a.DanmuFont)
				if rs.hasEmote {
					tw, _ = c.MeasureText(0, rs.spans...)
				} else {
					tw = textWidth(c, d.Text, a.DanmuFont, 700)
				}
			}
			// 正文颜色：开着「彩色弹幕」且平台给了非白色时用平台色，
			// 否则用设置里的字色。
			fg := danmuColor(d, a.DanmuColorful, a.DanmuColor)
			x := stageW - p*(stageW+tw)
			// Track 在入队时按**当时**的轨道数定死。窗口变小之后轨道数变少，
			// 原来那颗 Track 可能已经超出可见范围。
			//
			// 这里钳到最后一轨，**不能取模**：取模会把两条不同轨道的弹幕映射到
			// 同一条上（Track=7 和 Track=2 在 5 轨时都落在 2），屏幕上短暂重叠，
			// 而且正好破坏了「Track 定死之后不会跳轨道」这件事本身。钳位则是
			// 「宁叠不丢」那条既定取舍：多出来的挤在最后一轨，看得见、不乱跳。
			track := d.Track
			if track >= tracks {
				track = tracks - 1
			}
			if track < 0 {
				track = 0
			}
			y := float32(track*int(danmuChannel)) + 16

			if d.imageOnly() {
				if bmp, wait, anim := media.Default.EmoteFrame(d.Emotes[0].URL, imgW, c.Now()); bmp != nil {
					ui.Image(c, bmp).Size(imgW, imgH).
						Opacity(a.DanmuOpacity).
						Absolute().Left(x).Top(y)
					if anim && wait > 0 {
						c.After(wait)
					}
				}
				continue
			}

			if rs.hasEmote {
				// 表情和文字在同一行里混排：走 RichText，表情画在内联对象上。
				// 描边和纯文本时一样保留，飘在画面上要压得住背景。
				rs.richText(c, a.DanmuFont, fg).FontWeight(700).
					TextStroke(1, a.DanmuStroke).
					SingleLine().Opacity(a.DanmuOpacity).
					Absolute().Left(x).Top(y)
				continue
			}

			ui.Text(c, d.Text).FontSize(a.DanmuFont).FontWeight(700).
				TextColor(fg).
				TextStroke(1, a.DanmuStroke).
				SingleLine().Opacity(a.DanmuOpacity).
				Absolute().Left(x).Top(y)
		}
	})
}

// danmuProgress 是这条弹幕走到哪了：0 刚进场，1 完全离场。
//
// Dur <= 0 时返回 1（当作已经走完），**不能直接相除**：0/0 是 NaN，而
// `p < 0` 和 `p > 1` 对 NaN 都为假，NaN 会一路塞进 Absolute().Left(x)，
// 元素位置成了 NaN。Dur 取 a.DanmuDur（只在 >0 时恢复，默认 10s），
// 但导入/手改的文件能把它写成 0。
func danmuProgress(now, born time.Time, dur time.Duration) float32 {
	if dur <= 0 {
		return 1
	}
	return float32(now.Sub(born)) / float32(dur)
}
