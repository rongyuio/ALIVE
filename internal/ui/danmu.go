package ui

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/media"
	"alive/internal/theme"
)

// 弹幕默认参数取自 baseline 的 DEFAULT_DANMU_SETTINGS：
// 字号 20px、滚动穿过屏幕 10s、显示区域为画面上方 50%、不透明 1。
// 通道高度 = 字号 × 1.5 = 30。
const (
	danmuFontSize float32 = 20
	danmuDuration         = 10 * time.Second
	danmuChannel  float32 = 30
	danmuAreaFrac float32 = 0.5
	// danmuFlyMax 是画面上同时最多飞多少条；danmuLogMax 是右侧面板留多少历史。
	// baseline 里本来就是两个数：Overlay 只处理当帧，面板吃的是 messages
	//（MAX_MESSAGES = 300，渲染最后 200 条）。
	danmuFlyMax = 40
	danmuLogMax = 300
	danmuTick   = 33 * time.Millisecond
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

// danmuStickerScale 是纯图弹幕（房间表情、收藏集表情贴纸）相对字号的倍率。
//
// 这类弹幕整条就是一张图，网页端画得比一行字高得多（约 3 倍字号），
// 和正文里混排的小表情不是一回事。按 1.3 倍画出来太小，远看像缩略图。
const danmuStickerScale float32 = 2.8

// danmuState 是推动器的并发标志：active 由主线程写，循环读；
// loop 保证同时只有一个 goroutine 在跑。
type danmuState struct {
	active atomic.Bool
	loop   atomic.Bool
}

// ensureDanmuLoop 保证弹幕推动器在跑。
//
// 只有播放页需要连续帧：33ms 一次让主线程按时间重算位置。
// 离开播放页后 tickDanmu 把 active 置 false，goroutine 自行退出。
func (a *App) ensureDanmuLoop() {
	if a.Win == nil {
		return
	}
	a.danmu.active.Store(true)
	if !a.danmu.loop.CompareAndSwap(false, true) {
		return
	}
	win := a.Win
	go func() {
		defer a.danmu.loop.Store(false)
		ticker := time.NewTicker(danmuTick)
		defer ticker.Stop()
		for range ticker.C {
			if !a.danmu.active.Load() {
				return
			}
			win.Update(func() { a.tickDanmu() })
		}
	}()
}

// tickDanmu 在主线程推进弹幕：清掉过期的，按间隔补新的。
func (a *App) tickDanmu() {
	if a.Router.Path() != routePlayer {
		a.danmu.active.Store(false)
		return
	}

	now := time.Now()
	live := a.Danmu[:0]
	for _, d := range a.Danmu {
		if now.Sub(d.Born) < d.Dur {
			live = append(live, d)
		}
	}
	a.Danmu = live
}

// danmuLayer 是画面上的弹幕层：每条从右边缘匀速穿到左边缘外。
//
// baseline 由 danmu.js 负责轨道与位移，这里按同一组参数自己算：
// 区域为画面上方 50%，通道 30px；滚动时长 10s 穿过整屏。
func (a *App) danmuLayer(c *ui.Context) {
	if !a.DanmuOn || len(a.Danmu) == 0 {
		return
	}
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

	now := time.Now()
	// 用四边绝对定位铺满 stage：Fill() 在 Box 里不会撑开高度，
	// 那样弹幕会以塌缩后的容器中心定位，整层掉到画面中部。
	layer := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Clip()
	layer.Children(func() {
		for _, d := range a.Danmu {
			p := float32(now.Sub(d.Born)) / float32(d.Dur)
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
			x := stageW - p*(stageW+tw)
			y := float32((d.Track%tracks)*int(danmuChannel)) + 16

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
				rs.richText(c, a.DanmuFont, a.DanmuColor).FontWeight(700).
					TextStroke(1, a.DanmuStroke).
					SingleLine().Opacity(a.DanmuOpacity).
					Absolute().Left(x).Top(y)
				continue
			}

			ui.Text(c, d.Text).FontSize(a.DanmuFont).FontWeight(700).
				TextColor(a.DanmuColor).
				TextStroke(1, a.DanmuStroke).
				SingleLine().Opacity(a.DanmuOpacity).
				Absolute().Left(x).Top(y)
		}
	})
}
