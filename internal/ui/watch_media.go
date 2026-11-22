package ui

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
	"alive/internal/watch"
)

// 观测墙的会话层。
//
// 墙上每一格都是一间「无弹幕的单直播间缩小版」：全档取流、真实 AO、默认静音、
// 按格子物理像素呈现。只有 a.WatchActive 那一路出声；其余只静音，不重开。
//
// active 在激活完成时接管进 a.media；wallSession 只留非 active 预览和还没出
// 首帧的 pending 格。因此 active 的播放/暂停、刷新、音量直接用播放页那套函数，
// 墙上不再有第二套控制逻辑。音量的记账是唯一例外：a.Volume 只表示当前出声那
// 一路，墙上每格另在 wall.volumes 里记自己的一份，切走再切回来用它自己的值。
//
// 进播放页时 watchAdoptActive(false) 把 active 原样交过去；离开播放页时
// detachMedia 保留引擎和全尺寸呈现，墙继续画它。激活/退出 active 只换所有权
// 和声音，不碰呈现尺寸；激活另一格时 watchReleaseActive 把 a.media 的 active
// 移回墙上的 wallSession，静音但不停播。
type wallSession struct {
	key  string
	room Streamer
	prov live.Provider

	player *ui.VideoPlayer
	track  videoTrack
	stream live.Stream
	up     live.Variant

	ctx    context.Context
	cancel context.CancelFunc
	seq    int

	loading  bool
	err      string
	offline  bool
	checking bool

	// openedAt 是这一路当前阶段的起点：取流阶段是开始取流，引擎开出之后
	// 重置为引擎打开的时刻——首帧预算从后者算，取流慢不该吃掉出图的预算。
	openedAt   time.Time
	retryAt    time.Time
	failures   int
	refreshAt  time.Time
	firstFrame bool
	lastPos    time.Duration
	stillSince time.Time

	// renderW/H 是这一格的软件渲染目标，按格子物理像素给。
	renderW, renderH int
}

type wallMedia struct {
	sessions map[string]*wallSession
	// rects 是上一帧墙上的布局，detachMedia / release 用它给 active 设呈现尺寸。
	rects map[string]watch.Rect
	// volumes 是每一格自己的音量（0–1），按 platform|roomID 记。
	// 墙上仍只有一路出声，但音量不再共用 a.Volume 一份：调哪格只动哪格，
	// 切走再切回来还是它自己的值，静音一格也不会把别的格改成 0。
	volumes map[string]float32
	// tickPending 表示墙的心跳定时器已经排上，避免每帧重复排。
	tickPending bool
	// hiddenSince 是墙变成不可见的时刻；零值表示墙可见。超过
	// watchRetention 之后 watchReap 回收非焦点会话。
	hiddenSince time.Time
}

func (m *wallMedia) init() {
	if m.sessions == nil {
		m.sessions = make(map[string]*wallSession)
	}
	if m.rects == nil {
		m.rects = make(map[string]watch.Rect)
	}
	if m.volumes == nil {
		m.volumes = make(map[string]float32)
	}
}

// watchVolume 读这一格记住的音量。第一次见到它时继承当前出声那一路的值，
// 之后这格就只认自己这份。
func (a *App) watchVolume(key string) float32 {
	a.watch.init()
	if v, ok := a.watch.volumes[key]; ok {
		return v
	}
	v := a.Volume
	a.watch.volumes[key] = v
	return v
}

// setVolume 改当前出声那一路的音量：播放页，或墙上 active。
// 墙上 active 时同时把值记到这格名下，切走再切回来才能恢复。
func (a *App) setVolume(v float32) {
	a.Volume = min(1, max(0, v))
	if key := a.WatchActive; key != "" {
		a.watch.init()
		a.watch.volumes[key] = a.Volume
	}
	a.markDirty()
}

func watchKey(s Streamer) string { return s.Platform + "|" + s.RoomID }

// watchRecord 是观测盒落盘的一条。只存身份和展示用的元数据、以及这一格自己的
// 音量，不存签名地址、会话状态或播放进度——重启后重新取流，盒子顺序和置顶关系保留。
type watchRecord struct {
	Platform  string  `json:"platform"`
	RoomID    string  `json:"roomId"`
	Name      string  `json:"name"`
	Title     string  `json:"title,omitempty"`
	Category  string  `json:"category,omitempty"`
	Sub       string  `json:"sub,omitempty"`
	Viewers   int     `json:"viewers,omitempty"`
	Live      bool    `json:"live,omitempty"`
	Hue       float32 `json:"hue,omitempty"`
	CoverURL  string  `json:"cover,omitempty"`
	AvatarURL string  `json:"avatar,omitempty"`
	// Volume 是这一格自己的音量（0–1）。nil 表示从没调过，下次上墙继承当时的
	// 全局音量；指针是为了把 0（静音）和「没存过」区分开。
	Volume *float32 `json:"volume,omitempty"`
}

func (a *App) watchRecordOf(s Streamer) watchRecord {
	r := watchRecord{
		Platform:  s.Platform,
		RoomID:    s.RoomID,
		Name:      s.Name,
		Title:     s.Title,
		Category:  s.Category,
		Sub:       s.Sub,
		Viewers:   s.Viewers,
		Live:      s.Live,
		Hue:       s.Hue,
		CoverURL:  s.CoverURL,
		AvatarURL: s.AvatarURL,
	}
	if v, ok := a.watch.volumes[watchKey(s)]; ok {
		r.Volume = &v
	}
	return r
}

func (r watchRecord) streamer() Streamer {
	return Streamer{
		Platform:  r.Platform,
		RoomID:    r.RoomID,
		Name:      r.Name,
		Title:     r.Title,
		Category:  r.Category,
		Sub:       r.Sub,
		Viewers:   r.Viewers,
		Live:      r.Live,
		Hue:       r.Hue,
		CoverURL:  r.CoverURL,
		AvatarURL: r.AvatarURL,
	}
}

// normalizeWatchRooms 去重、丢掉没有房间身份的记录，并把上限钳到 watch.MaxRooms。
func normalizeWatchRooms(in []Streamer) []Streamer {
	seen := make(map[string]bool, len(in))
	out := make([]Streamer, 0, min(len(in), watch.MaxRooms))
	for _, s := range in {
		if s.Platform == "" || s.RoomID == "" {
			continue
		}
		key := watchKey(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
		if len(out) >= watch.MaxRooms {
			break
		}
	}
	return out
}

func (a *App) watchHas(key string) bool {
	for _, s := range a.WatchRooms {
		if watchKey(s) == key {
			return true
		}
	}
	return false
}

func (a *App) watchRoom(key string) (Streamer, bool) {
	for _, s := range a.WatchRooms {
		if watchKey(s) == key {
			return s, true
		}
	}
	return Streamer{}, false
}

// 画布尺寸从「高是 18 的倍数」的格子里取：高 18k、宽 32k，比例正好
// 16:9，宽高都是偶数。mpv 按这个尺寸开画布，比例和格子差一点点，多出来
// 的部分就会被它补成黑边、再被上屏那一层放大到格子上。
const (
	watchStep = 18
	watchMinK = 10 // 320×180
	watchMaxK = 40 // 1280×720
)

// watchSize 把格子 DIP 换成渲染像素：2 倍留 DPI 余量，上限 1280×720。
// 源流不降档；这只是呈现尺寸。
//
// 画布必须和格子一样是精确的 16:9。旧实现按格子的浮点宽高各乘 2 再取整，
// 得到 1008×566 这种比例，mpv 会把 16:9 的流补出黑边；上屏再 Contain 回
// 16:9 的格子，黑边就落在格子四边上——日间模式里每格一圈 1 物理像素的黑
// 线，非常显眼。撞到 1280 上限时更糟：旧实现先把 w 夹到 1280 再做除法，
// h 根本没缩，宽格子会拿到 1.71 甚至 1.49 的画布，黑边宽到几十像素。
// 所以高度落到最近的 18 的倍数上，比例就精确了。墙上的格子统一 16:9
// （见 watchAspect），只用高度推宽高两项。
func watchSize(r watch.Rect) (int, int) {
	k := int(math.Round(float64(r.H) * 2 / watchStep))
	k = min(max(k, watchMinK), watchMaxK)
	return 32 * k, watchStep * k
}

func (a *App) watchPlayer(key string) *ui.VideoPlayer {
	if key == "" {
		return nil
	}
	if a.media.roomKey == key && a.media.player != nil {
		return a.media.player
	}
	if s := a.watch.sessions[key]; s != nil {
		return s.player
	}
	return nil
}

// watchShow 是墙可见时的每帧同步：补齐会话、更新渲染尺寸、关掉已经不在盒里的。
func (a *App) watchShow(rects map[string]watch.Rect) {
	a.watch.init()
	a.watch.rects = rects
	// 墙又可见了：保留窗口从头计。
	a.watch.hiddenSince = time.Time{}
	// 焦点跟着声音走：媒体层正在放的那一间如果在盒里，它就是 active。
	//
	// 以前只有"在墙上点过一格"才会置 active，于是"列表进播放页 → 回首页 →
	// 进墙"之后整面墙都在静音预览、声音却在响，没有一格有绿点，岛还是时钟。
	// 焦点是标量，它该由"谁在出声"决定，不该由"从哪条路进来的"决定。
	if a.WatchActive == "" && a.media.roomKey != "" && a.watchHas(a.media.roomKey) {
		a.WatchActive = a.media.roomKey
		if a.Player.Name == "" {
			if room, ok := a.watchRoom(a.media.roomKey); ok {
				a.Player = room
			}
		}
	}
	seen := make(map[string]bool, len(a.WatchRooms))
	for _, room := range a.WatchRooms {
		key := watchKey(room)
		seen[key] = true
		// 上墙这一刻就把这格自己的音量定下来。它跟取流无关，视图测试里没有
		// 窗口也要定；否则会等到第一次激活才去读 a.Volume，而那时它可能
		// 已经被别的格子改过了。
		a.watchVolume(key)
		r, ok := rects[key]
		if !ok {
			continue
		}
		w, h := watchSize(r)
		if a.media.roomKey == key {
			// 这一间已经由媒体层持有（从列表进过播放页、或从墙上接管过去
			// 又退回来）。墙只负责把它画在格子里，不再建第二个会话——
			// 以前判据只看 active，于是同一间房会有两个引擎在解同一路流，
			// 其中一个还没有任何地方能关掉它。
			//
			// 呈现尺寸仍由媒体层统一求值（presentSizeFor）：墙上就是格子。
			a.applyPresent()
			continue
		}
		if s := a.watch.sessions[key]; s != nil {
			if s.renderW != w || s.renderH != h {
				s.renderW, s.renderH = w, h
				if s.track != nil {
					s.track.SetPresentSize(w, h)
				}
			}
			continue
		}
		if a.Win == nil {
			// 视图测试没有窗口：不取流、不起引擎，格子停在封面上。
			continue
		}
		s := &wallSession{key: key, room: room, renderW: w, renderH: h}
		a.watch.sessions[key] = s
		a.watchOpenSession(s)
	}
	for key, s := range a.watch.sessions {
		if seen[key] {
			continue
		}
		a.watchCloseSession(s)
	}
	// 盒里没有 active 了：停掉它。
	if a.WatchActive != "" && !seen[a.WatchActive] {
		a.watchStopActive()
	}
	a.watchSyncActiveAudio()
}

// watchRetention 是墙不可见后会话的保留窗口。
//
// "看不见"和"关掉"是两个决定，中间留一段时间：去首页瞄一眼就回来，整面墙
// 还是活的，直接出画；超过这段才回收。以前两者焊在一起，离开几秒回来 11 格
// 同时重新取流，按实测 224ms–4.3s 不等。
//
// 30 秒是取舍：够一次"看一眼就回来"，又不会在用户真的去干别的时一直占着
// 12 路解码。代价写在 doc/decisions/0012。
const watchRetention = 30 * time.Second

// watchHide 在墙不可见时调用：标记不可见，回收交给保留窗口。
//
// 不在这里关会话：可见性不是资源策略。焦点那一路本来就该继续出声，其余
// 的在窗口内保持解码，窗口到期由 watchReap 收掉。
func (a *App) watchHide() {
	a.watch.init()
	if a.watch.hiddenSince.IsZero() {
		a.watch.hiddenSince = time.Now()
	}
}

// watchReap 在墙持续不可见超过保留窗口后回收非焦点会话。
//
// 每拍调用，幂等：窗口没到什么都不做，收完把标记清掉，下次隐藏重新计时。
func (a *App) watchReap(now time.Time) {
	a.watch.init()
	if a.watch.hiddenSince.IsZero() {
		return
	}
	if now.Sub(a.watch.hiddenSince) < watchRetention {
		return
	}
	focus := a.focusKey()
	for _, s := range a.watch.sessions {
		if s.key == focus {
			continue
		}
		a.watchCloseSession(s)
	}
	a.watch.hiddenSince = time.Time{}
}

func (a *App) watchCloseSession(s *wallSession) {
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.player != nil {
		s.player.Close()
		s.player = nil
	}
	s.track = nil
	delete(a.watch.sessions, s.key)
}

// watchOpenSession 取流并起这一格的引擎。所有结果都回到主线程再接纳。
func (a *App) watchOpenSession(s *wallSession) {
	if s == nil || s.loading || s.offline || s.player != nil {
		return
	}
	if a.Win == nil {
		// 视图测试没有窗口；真实窗口在 NewApp 之后就有了。
		return
	}
	prov, ok := providerFor(live.Platform(s.room.Platform))
	if !ok {
		s.err = "该平台还不支持取流"
		return
	}
	// 记下来给 watchEnded 用：判死后要先问平台是不是真下播，没填的话
	// nil 接口断言恒为 false，会被当成「还在播」白白重开一轮。
	s.prov = prov
	if s.renderW <= 0 || s.renderH <= 0 {
		s.renderW, s.renderH = 640, 360
	}
	if s.cancel != nil {
		s.cancel()
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.seq++
	s.loading = true
	s.err = ""
	s.offline = false
	s.openedAt = time.Now()

	ctx, seq, win := s.ctx, s.seq, a.Win
	// 每一格用自己的房间记录：墙上同时挂着几个平台，档位名互不相通。
	quality := a.qualityFor(s.key)
	line := a.lineIDFor(s.key, s.stream)
	room := s.room
	go func() {
		fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		info, v, err := fetchStream(fctx, prov, room.RoomID, line, quality)
		if err != nil {
			if ctx.Err() == nil {
				runOnMain(win, func() {
					if a.watch.sessions[s.key] == s && s.seq == seq {
						a.watchFail(s, "取流失败")
					}
				})
			}
			return
		}
		if !info.Live {
			runOnMain(win, func() {
				if a.watch.sessions[s.key] == s && s.seq == seq {
					a.watchOffline(s)
				}
			})
			return
		}
		if v.URL == "" {
			runOnMain(win, func() {
				if a.watch.sessions[s.key] == s && s.seq == seq {
					a.watchFail(s, "没有拿到可播放的地址")
				}
			})
			return
		}
		// 引擎不挂取流 ctx：同一路可能被交接给播放页 / 墙上的另一格。
		e, err := openNative(context.Background(), v.URL, playback.Options{
			Headers:       v.Headers,
			Width:         v.Width,
			Height:        v.Height,
			FrameRate:     v.FrameRate,
			StartSilent:   true,
			SkipAudioGate: true,
		})
		if err != nil {
			if ctx.Err() == nil {
				runOnMain(win, func() {
					if a.watch.sessions[s.key] == s && s.seq == seq {
						a.watchFail(s, "播放组件打不开这个流")
					}
				})
			}
			return
		}
		runOnMain(win, func() {
			if ctx.Err() != nil || a.watch.sessions[s.key] != s || s.seq != seq {
				_ = e.Close()
				return
			}
			p := ui.NewEngineVideoPlayer(e)
			// 起播先按 0 音量，激活时 adopt 再落这一格自己的值。
			//
			// 不能用 SetMuted：MyGo 把静音视频当「一张画」调度，系统要求
			// 减少动效、窗口被遮住或元素不再显示时会把它按住不播，时间轴
			// 随之停住——墙的看门狗就会把一路好好的流当断流重连。静音一律
			// 走 SetVolume(0)，播放器保持非静音。
			p.SetVolume(0)
			// 格子按自己的物理像素呈现：源流档位、解码分辨率都不动，只把
			// 呈现尺寸收到格子大小。这是唯一的呈现旋钮，接管进播放页时
			// 放回 0 就恢复全尺寸。
			if s.renderW > 0 && s.renderH > 0 {
				e.SetPresentSize(s.renderW, s.renderH)
			}
			s.player, s.track = p, e
			// 引擎已经开出来了：首帧预算从这里重新计时，播放器迟迟不出图
			// 时由 watchTick 接手，不能让取流阶段的耗时提前吃掉这段预算。
			s.openedAt = time.Now()
			s.stream, s.up = info, v
			// 平台确认的档位写回这一格自己的记录：激活 / 进播放页时读的就是它。
			a.learnRoomChoice(s.key, info, v)
			s.loading = false
			s.err = ""
			s.firstFrame = p.Ready()
			s.failures = 0
			s.refreshAt = refreshAtFor(v)
			s.stillSince = time.Now()
			s.lastPos = 0
			if s.key == a.WatchActive {
				a.WatchPending = ""
				a.watchAdoptActive(true)
				a.watchSyncActiveAudio()
			}
			a.invalidateWindows()
		})
	}()
}

func (a *App) watchFail(s *wallSession, msg string) {
	if s == nil {
		return
	}
	if s.player != nil {
		s.player.Close()
		s.player = nil
	}
	s.track = nil
	s.loading = false
	s.err = msg
	s.firstFrame = false
	s.failures++
	s.retryAt = time.Now().Add(watchBackoff(s.failures))
	if s.key == a.WatchActive {
		// 焦点是「谁在出声」。引擎已经关了还留着绿点，岛却按「没有引擎」
		// 回时钟，两个地方各说各话。这里把焦点一起收掉：绿点消失、岛回
		// 时钟、a.Player/a.Playing 复位，和 surfaces.md 不变量 5 一致。
		a.watchDeactivate()
	}
	a.invalidateWindows()
}

func (a *App) watchOffline(s *wallSession) {
	if s == nil {
		return
	}
	if s.player != nil {
		s.player.Close()
		s.player = nil
	}
	s.track = nil
	s.loading = false
	s.offline = true
	s.err = "主播暂时没有开播"
	s.firstFrame = false
	if s.key == a.WatchActive {
		// 下播就是没有声音：焦点跟着收掉，绿点和岛一起消失。
		a.watchDeactivate()
	}
	a.invalidateWindows()
}

func watchBackoff(n int) time.Duration {
	switch {
	case n <= 1:
		return 2 * time.Second
	case n == 2:
		return 8 * time.Second
	case n == 3:
		return 30 * time.Second
	default:
		return 2 * time.Minute
	}
}

// watchReload 是格子控制台上的「重新加载」：作用于当前 hover 的那一格。
// 播放页持有时走 retryStream；墙上自己持有的会话就关掉当前引擎，让下一拍
// heartbeat 重开。冷格没有会话，等 watchShow 重开即可。
func (a *App) watchReload(room Streamer) {
	key := watchKey(room)
	if a.media.roomKey == key {
		a.retryStream()
		return
	}
	s := a.watch.sessions[key]
	if s == nil {
		return
	}
	a.watchRestart(s, "")
	// watchRestart 只关引擎、清错误，不动 offline；不清这一位，下一句
	// watchOpenSession 会在门口直接返回，按钮就是空点。手动重试同时把
	// 退避清零：用户点的这一次不该再等上一轮攒出来的 2 分钟。
	s.offline = false
	s.failures = 0
	s.retryAt = time.Time{}
	a.watchOpenSession(s)
}

// watchDeadReason 报告这一格是不是已经不能继续播了，空串表示还活着。
//
// 直播没有「播完」：播放器自己结束、报错，或者时间轴停住，才说明这颗地址
// 不能用了。三条误判单独处理：
//
//   - 时间轴用「有没有变化」而不是「有没有变大」。HLS 刷新播放列表时位置
//     会重排，甚至往回跳；只认递增会把一路好好的流当成断流。
//   - 还没出图时不判死：慢启动由首帧预算管，不该在 6 秒时先被这里砍掉。
//   - 缓冲给更宽的窗口：6 秒就判死等于把一条正要恢复的流掐掉。20 秒
//     （streamBufferStallTimeout）给了短抖动自己接上的机会。
//
// 不再看音频时钟。eefee76 当年是拿它救 HLS 的「画面冻住、音频照走」，
// 但 FLV 单连接断供时 mpv 连音频一起暂停：2026-11-22 实测四种断法（完全
// 停发 / 慢节点 / 只发音频 / 视频内容损坏），音频都在 250ms 内跟着停，
// 自然播放 420 秒里「画面停、音频走」出现 0 次。音频时钟不再提供任何
// 「还活着」的信息，反而会让一张冻住的画面永远不重连。
//
// 会顺手更新 lastPos / stillSince，调用方只管问。
func watchDeadReason(ready, playing, buffering, ended bool, pos time.Duration,
	lastPos *time.Duration, stillSince *time.Time, now time.Time) string {
	if ended {
		return "播放器报告结束"
	}
	if !ready || !playing {
		*stillSince = now
		return ""
	}
	if pos != *lastPos {
		*lastPos, *stillSince = pos, now
		return ""
	}
	limit := streamStallTimeout
	if buffering {
		limit = streamBufferStallTimeout
	}
	if !stillSince.IsZero() && now.Sub(*stillSince) > limit {
		return fmt.Sprintf("画面停住 %d 秒", int(limit/time.Second))
	}
	return ""
}

// watchTick 驱动墙上所有非 media 持有的会话：首帧预算、断流、地址到期、重试。
//
// active 若由 a.media 持有，不在这里驱动——playerMedia 自己的心跳管它。
func (a *App) watchTick(now time.Time) {
	a.watchReap(now)
	if len(a.watch.sessions) == 0 {
		return
	}
	for _, s := range a.watch.sessions {
		if s.player == nil {
			if s.loading {
				if !s.openedAt.IsZero() && now.Sub(s.openedAt) > firstFrameBudget {
					a.watchFail(s, "首帧超时")
				}
				continue
			}
			if s.offline {
				continue
			}
			if !s.retryAt.IsZero() && now.Before(s.retryAt) {
				continue
			}
			a.watchOpenSession(s)
			continue
		}
		if s.player.Ready() {
			s.firstFrame = true
		}
		if err := s.player.Err(); err != nil {
			a.watchEnded(s, "播放器报错："+err.Error())
			continue
		}
		posBefore := s.player.Position()
		if why := watchDeadReason(
			s.player.Ready(), s.player.Playing(), s.player.Buffering(), s.player.Ended(),
			posBefore, &s.lastPos, &s.stillSince, now,
		); why != "" {
			a.watchEnded(s, why)
			continue
		}
		if !s.refreshAt.IsZero() && now.After(s.refreshAt) {
			a.watchRestart(s, "地址到期")
			continue
		}
		// 引擎构建成功不等于出图：libmpv 收到 loadfile 就返回，一条死地址
		// 可以一直停在 Loading、不报错也不结束。watchDeadReason 对 !Ready
		// 是刻意放过的（慢启动不判死），所以这里必须有首帧预算兜底，和
		// 播放页的 needFirstFrameTimeout 对齐。暂停中的格子不算——用户
		// 主动停着，恢复播放那一拍再继续计时。
		if !s.player.Ready() && s.player.Playing() &&
			!s.openedAt.IsZero() && now.Sub(s.openedAt) >= firstFrameBudget {
			a.watchRestart(s, "首帧超时")
		}
	}
}

// watchEnded 先问平台：真下播就落未开播，仍 live 才重取。
//
// why 是这一路的实际死因（时间轴停住 / 播放器结束 / 报错），只用于日志和
// 重连原因——平台确认还在播时，用户看到的仍是「正在重连」，而不是故障。
func (a *App) watchEnded(s *wallSession, why string) {
	if s == nil || s.checking || s.player == nil {
		return
	}
	s.checking = true
	prov := s.prov
	if prov == nil {
		// 会话可能来自没走过 watchOpenSession 的交接路径：别让 nil 静默
		// 变成「平台说还在播」，那会把真下播的一格送进无谓的重连。
		prov, _ = providerFor(live.Platform(s.room.Platform))
	}
	roomID, win := s.room.RoomID, a.Win
	go func() {
		liveNow := true
		if ri, ok := prov.(live.RoomInfoer); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			room, err := ri.RoomInfo(ctx, roomID)
			cancel()
			if err == nil {
				liveNow = room.Live
			}
		}
		runOnMain(win, func() {
			if a.watch.sessions[s.key] != s {
				return
			}
			s.checking = false
			if !liveNow {
				a.watchOffline(s)
				return
			}
			// 真断了就用房间记录里的线路重开，退避由 watchRestart 管。
			// 不在这里轮换线路：换线等于整路重建，一次抖动会被放大成
			// 一次完整重开——播放页同一条回路已经在 c4d6b93 删掉。
			a.watchRestart(s, why)
		})
	}()
}

func (a *App) watchRestart(s *wallSession, reason string) {
	if s == nil {
		return
	}
	if s.player != nil {
		s.player.Close()
		s.player = nil
	}
	s.track = nil
	s.loading = false
	s.firstFrame = false
	s.failures++
	s.retryAt = time.Now().Add(watchBackoff(s.failures))
	s.err = reason
	slog.Info("观测墙重连", "key", s.key, "name", s.room.Name,
		"reason", reason, "failures", s.failures)
	a.invalidateWindows()
}

// watchActivate 单击格子：切音频焦点，不跳路由。
func (a *App) watchActivate(room Streamer) {
	key := watchKey(room)
	if key == "" {
		return
	}
	if key == a.WatchActive {
		// 点已经是焦点的格 = 重试。失败/未开播的提示写着「点击重试」，
		// 而 active 以前在这里直接 return；这一路通常还挂在墙的 pending
		// 会话上（不在媒体层），控制条上的「重新加载」也够不着它，于是
		// 整格没有任何有效入口。在播/在载时这里什么都不做。
		if a.media.roomKey == key {
			if a.media.player == nil && a.media.err != "" {
				a.Playing = true
				a.markDirty()
				a.retryStream()
			}
		} else if s := a.watch.sessions[key]; s != nil && s.player == nil && !s.loading {
			a.watchReload(s.room)
		}
		return
	}
	// 这一格已知没有声音（离线 / 取流失败，也没有引擎在跑）：点击只重试，
	// 不抢焦点。抢了会先亮绿点、展开岛，等这一路再失败又一起收掉——一次
	// 没有意义的闪动，还会把原来的声音焦点弄丢。
	if s := a.watch.sessions[key]; s != nil && s.player == nil && !s.loading &&
		(s.offline || s.err != "") {
		a.watchReload(s.room)
		return
	}
	a.watch.init()
	old := a.WatchActive
	if old != "" {
		// 先把旧 active 的音量记到它自己名下，再切走；否则调过的那格会丢值。
		a.watch.volumes[old] = a.Volume
	}
	if a.media.roomKey != "" && a.media.roomKey != key {
		a.watchReleaseActive()
		if a.media.roomKey != "" {
			// 没交接成功：旧 active 还在重新加载（媒体层握着房间、引擎还没
			// 出来），release 第一行就返回了，既没 cancel 也没推进 seq。
			// 和 watchDeactivate 同一条收尾：把这一轮取流作废，否则结果回来
			// 仍会被 applyMedia 接纳，在旧格子里按 a.Volume 出声，而焦点和
			// 墙上的控件都已经在新的那一格上了。
			a.closeMedia()
		}
	}
	if old != "" && old != key {
		if s := a.watch.sessions[old]; s != nil && s.player != nil {
			// 让位给新的 active：音量归零，但不进静音态（见上）。
			s.player.SetVolume(0)
		}
	}
	a.WatchActive = key
	a.WatchPending = key
	a.Player = room
	slog.Info("观测墙激活", "key", key, "name", room.Name)
	a.Playing = true
	// 点格子 = 声音放开。这格自己记着多少就是多少；记的是 0（用户静音过）
	// 才恢复到一个可听的音量，并写回这格，不碰别的格。
	volume := a.watchVolume(key)
	if volume <= 0 {
		if a.volumeBeforeMute > 0 {
			volume = a.volumeBeforeMute
		} else {
			volume = 0.6
		}
		a.watch.volumes[key] = volume
	}
	a.Volume = volume
	if s := a.watch.sessions[key]; s != nil {
		if s.player == nil {
			// 点一个失败/离线的格子就是重试它，不用另设按钮。
			s.offline = false
			s.retryAt = time.Time{}
			s.failures = 0
			a.watchOpenSession(s)
		} else {
			// 激活即接管：墙上 active 就是播放页那一路，控制走同一套函数。
			a.watchAdoptActive(true)
		}
	}
	a.watchSyncActiveAudio()
	a.invalidateWindows()
}

// watchSyncActiveAudio 把 a.Volume / a.Playing 落到当前 active 的播放器上。
func (a *App) watchSyncActiveAudio() {
	if a.WatchActive == "" {
		return
	}
	if a.media.roomKey == a.WatchActive && a.media.player != nil {
		a.syncPlayer()
		return
	}
	s := a.watch.sessions[a.WatchActive]
	if s == nil || s.player == nil {
		return
	}
	s.player.SetVolume(float64(a.Volume))
	if a.Playing {
		s.player.Play()
	} else {
		s.player.Pause()
	}
}

// watchReleaseActive 把 a.media 持有的 active 移回墙上的会话：静音、不重开。
func (a *App) watchReleaseActive() {
	m := &a.media
	if m.player == nil || m.roomKey == "" {
		return
	}
	key := m.roomKey
	if !a.watchHas(key) {
		// 不在盒里：真的关掉。不能回头调 closeMedia——它会判"在不在盒里"，
		// 而这里已经知道答案了，绕回去只是多一层判断。
		a.media.reset()
		a.media.roomKey = ""
		return
	}
	a.watch.init()
	// 用 roomKey 找回房间，不能用 a.current()：进播放页时 a.Player 可能已经
	// 换成新的目标了，而这一路的画面/流仍属于旧的 active。
	room, _ := a.watchRoom(key)
	s := &wallSession{
		key: key, room: room, player: m.player, track: engineOf(m.player),
		stream: m.stream, up: m.upstream, firstFrame: true,
		refreshAt: m.refreshAt, stillSince: time.Now(), lastPos: m.player.Position(),
	}
	// 首帧预算跟着这一路走：媒体层已经等过的时长不能因为退回墙上而重置，
	// 否则一条死地址每次交接都能重新领 20 秒。
	s.openedAt = m.openedAt
	if s.openedAt.IsZero() {
		s.openedAt = time.Now()
	}
	// watchEnded 靠它确认是不是真下播；交接路径不填就又会退化成
	// 「nil 恒等于还在播」。
	s.prov, _ = providerFor(live.Platform(room.Platform))
	if r, ok := a.watch.rects[key]; ok {
		s.renderW, s.renderH = watchSize(r)
	} else {
		s.renderW, s.renderH = 640, 360
	}
	// 退出 active 只把音量归零，画面必须和激活时一像素不差。不进静音态：
	// 静音视频会被 MyGo 按住，时间轴一停，看门狗就会重连这一格。
	s.player.SetVolume(0)
	// 不碰呈现尺寸。
	// 这里补 SetPresentSize 正是“active 变非 active 又变一次”的来源。
	// 音量记回这格名下：它已经不在 a.media 了，之后只认自己这份。
	a.watch.volumes[key] = a.Volume
	// 同一间房只能有一个会话：直接覆盖会把旧条目连同它的引擎一起漏掉，
	// 墙和媒体层都不再持有它，没有任何地方能再关。正常路径下 G1 已经从
	// ensureMedia 那一侧堵住了重复（认领前先收掉墙上的会话），这里兜底。
	if old := a.watch.sessions[key]; old != nil && old != s {
		a.watchCloseSession(old)
	}
	a.watch.sessions[key] = s

	// 把 a.media 清空，但不 Close 播放器——它已经归墙上了。
	if m.cancel != nil {
		m.cancel()
	}
	m.seq++
	m.ctx, m.cancel = nil, nil
	m.player = nil
	m.stream = live.Stream{}
	m.upstream = live.Variant{}
	m.roomKey = ""
	m.loading = false
	m.err = ""
	m.firstFrame = false
	m.background = false
	m.refreshAt = time.Time{}
	m.refreshing = false
	m.swapFailures = 0
	a.pushedVolume = 0
	a.closeDanmaku()
	a.DanmuLog = nil
	a.Danmu = nil
	a.panelSeen = 0
	a.panelPending = 0
	a.invalidateWindows()
}

// watchAdoptActive 把墙上的 active 原样交给 a.media。
//
// onWall=true 是墙上激活：引擎留在格子里，音频开、呈现尺寸按格子；
// onWall=false 是进播放页：恢复全尺寸。两条走同一段状态搬迁，不复制。
// 返回 true 表示已经接上，调用方不必再取流。
func (a *App) watchAdoptActive(onWall bool) bool {
	if a.WatchActive == "" || a.media.roomKey == a.WatchActive {
		return false
	}
	a.watch.init()
	s := a.watch.sessions[a.WatchActive]
	if s == nil || s.player == nil {
		return false
	}
	if a.media.player != nil {
		a.watchReleaseActive()
	}
	key := s.key
	where := "播放页"
	if onWall {
		where = "墙上"
	}
	slog.Info("观测墙接管", "where", where, "key", key, "name", s.room.Name)
	delete(a.watch.sessions, key)
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}

	m := &a.media
	m.seq++
	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.win = a.Win
	m.id = fmt.Sprintf("watch#%d", m.seq)
	m.start = time.Now()
	m.roomKey = key
	m.player = s.player
	m.stream = s.stream
	m.upstream = s.up
	// 这一路真正在播的档位写回它自己的房间记录：播放页的下拉只读这份，
	// 不需要任何全局校正。learnRoomChoice 只碰 key 这一间房。
	a.learnRoomChoice(key, s.stream, s.up)
	m.loading = false
	m.err = ""
	// 接管的是这一格的真实状态：引擎还没出图就不能在这里记成「已出图」，
	// 否则媒体层的首帧预算（needFirstFrameTimeout）从此不再看这一路。
	m.firstFrame = s.player.Ready()
	// 预算也跟着这一路走：墙上已经等过的时长带回播放页。
	m.openedAt = s.openedAt
	if m.openedAt.IsZero() {
		m.openedAt = time.Now()
	}
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	m.background = onWall
	m.refreshAt = refreshAtFor(s.up)
	// 接管的是这一格自己记住的音量，不是上一路播放页的值。
	a.Volume = a.watchVolume(key)
	a.pushedVolume = float64(a.Volume)
	m.player.SetVolume(float64(a.Volume))
	m.player.Play()
	if !onWall {
		// 进播放页：呈现尺寸由媒体层统一求值（presentSizeFor），这里不自己算。
		a.applyPresent()
	}
	a.Player = s.room
	a.Playing = true
	a.WatchPending = ""
	a.closeDanmaku()
	a.DanmuLog = nil
	a.Danmu = nil
	a.panelSeen = 0
	a.panelPending = 0
	a.invalidateWindows()
	return true
}

// watchDropActive 停掉当前 active，但不动 a.Player / a.Playing。
//
// 用在「进播放页看另一间」：墙上的 active 要收声，但目标播放项已经换成新的了，
// 不能把 a.Player 清掉。
func (a *App) watchDropActive() {
	key := a.WatchActive
	if key != "" {
		// 停掉这一路之前把音量记回这格名下，下次激活还是它自己的值。
		a.watch.init()
		a.watch.volumes[key] = a.Volume
	}
	a.WatchActive = ""
	a.WatchPending = ""
	if key == "" {
		return
	}
	if a.media.roomKey == key {
		a.closeMedia()
	}
	// 房间还在盒里时，closeMedia 已经把引擎交还成墙上的静音预览；在这里
	// 无条件再关一次会把刚交还的会话连引擎一起删掉，回墙就得重新取流。
	// 会话存亡由"还有没有表面在显示它"推导：不在盒里才真的收掉。
	if !a.watchHas(key) {
		if s := a.watch.sessions[key]; s != nil {
			a.watchCloseSession(s)
		}
	} else if s := a.watch.sessions[key]; s != nil && s.player != nil {
		// 还在盒里、但没被媒体层接管（取流途中，或已经退回墙）：只收声，
		// 画面继续做静音预览。
		s.player.SetVolume(0)
	}
	a.invalidateWindows()
}

// watchStopActive 停掉当前 active：关会话、清岛上的记号。
func (a *App) watchStopActive() {
	a.watchDropActive()
	a.Player = Streamer{}
	a.Playing = false
	a.invalidateWindows()
}

// watchDeactivate 取消 active：声音收掉，画面留在墙上做静音预览。
//
// 和 watchStopActive 的区别是收到哪一层：这里不关引擎、不把房间从盒里
// 移出去，只把所有权从 a.media（或 pending 会话）退回墙上的静音预览，
// active 清空、岛回到时钟。用户只是不想听了，不是想关掉这一格。
func (a *App) watchDeactivate() {
	key := a.WatchActive
	if key == "" {
		return
	}
	if a.media.roomKey == key {
		a.watchReleaseActive()
		if a.media.roomKey == key {
			// 引擎还没建出来，release 没东西可接管，直接收掉这一路。
			a.closeMedia()
		}
	} else {
		a.watch.init()
		a.watch.volumes[key] = a.Volume
		if s := a.watch.sessions[key]; s != nil && s.player != nil {
			s.player.SetVolume(0)
		}
	}
	a.WatchActive = ""
	a.WatchPending = ""
	a.Player = Streamer{}
	a.Playing = false
	a.invalidateWindows()
}

// watchAdd 收一间进观测盒；上限 watch.MaxRooms，重复不收。
func (a *App) watchAdd(room Streamer) bool {
	if room.Platform == "" || room.RoomID == "" {
		return false
	}
	if len(a.WatchRooms) >= watch.MaxRooms {
		// 这里的调用方是首页卡片右键与关注页行菜单，都不在播放页；提示
		// 必须走常驻外壳的 toast。notifyPlayback 只画在播放页的 noticePill
		// 上，用在这里等于什么都没发生。
		a.toastTone(fmt.Sprintf("观测盒最多 %d 间", watch.MaxRooms), toastInfo)
		return false
	}
	key := watchKey(room)
	for _, s := range a.WatchRooms {
		if watchKey(s) == key {
			return false
		}
	}
	a.WatchRooms = append(a.WatchRooms, room)
	a.markDirty()
	a.invalidateWindows()
	return true
}

// watchSwap 交换两格的位置：拖拽是「把这一格换到那一格」，不重排整面墙。
// active 跟房间身份走，不跟格子序号走。
func (a *App) watchSwap(dragKey, targetKey string) {
	if dragKey == targetKey {
		return
	}
	from, to := -1, -1
	for i, s := range a.WatchRooms {
		switch watchKey(s) {
		case dragKey:
			from = i
		case targetKey:
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	a.WatchRooms[from], a.WatchRooms[to] = a.WatchRooms[to], a.WatchRooms[from]
	a.markDirty()
	a.invalidateWindows()
}

// watchRemove 移出一间；如果是 active，声音一起停。
func (a *App) watchRemove(room Streamer) {
	key := watchKey(room)
	// 必须新分配：点「移出观测」是在 watchWall 构建这一格时同步跑的，而
	// watchWall 开头把 a.WatchRooms 的切片头拷给了 rooms。用 [:0] 原地压缩
	// 会改写它正在遍历的底层数组——余下的循环按左移过的房间继续建格，
	// 同一间房在这一帧里画两格，被删的后一位被跳过。
	out := make([]Streamer, 0, len(a.WatchRooms))
	for _, s := range a.WatchRooms {
		if watchKey(s) != key {
			out = append(out, s)
		}
	}
	a.WatchRooms = out
	if a.WatchActive == key {
		a.watchStopActive()
	}
	if s := a.watch.sessions[key]; s != nil {
		a.watchCloseSession(s)
	}
	a.markDirty()
	a.invalidateWindows()
}
