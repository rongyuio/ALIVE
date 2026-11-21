package ui

import (
	"context"
	"fmt"
	"log/slog"
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
// active 有两种归属：
//   - 墙持有（wallSession）：点格子激活、还没进播放页时；
//   - a.media 持有：从播放页退回观测盒之后。这时墙直接画 a.media.player，
//     同一个引擎、同一路声音，不再重开。
//
// 进播放页时 watchAdoptActive 把墙上的 active 原样交给 a.media；离开播放页时
// detachMedia 保留引擎，墙继续画它。激活另一格时 watchReleaseActive 把 a.media
// 的 active 移回墙上的 wallSession，静音但不停播。
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
	// tickPending 表示墙的心跳定时器已经排上，避免每帧重复排。
	tickPending bool
	// activeW/H 是 media 持有的 active 在墙上最后一次设过的呈现尺寸，
	// 避免每帧重复 SetPresentSize（那会每帧强制重渲染一次）。
	activeW, activeH int
}

func (m *wallMedia) init() {
	if m.sessions == nil {
		m.sessions = make(map[string]*wallSession)
	}
	if m.rects == nil {
		m.rects = make(map[string]watch.Rect)
	}
}

func watchKey(s Streamer) string { return s.Platform + "|" + s.RoomID }

// watchRecord 是观测盒落盘的一条。只存身份和展示用的元数据，不存签名地址、
// 会话状态或播放进度——重启后重新取流，盒子顺序和置顶关系保留。
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
}

func watchRecordOf(s Streamer) watchRecord {
	return watchRecord(s)
}

func (r watchRecord) streamer() Streamer {
	return Streamer(r)
}

// normalizeWatchRooms 去重、丢掉没有房间身份的记录，并把上限钳到 9。
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

// watchSize 把格子 DIP 换成渲染像素：2 倍留 DPI 余量，上限 1280×720。
// 源流不降档；这只是呈现尺寸。
func watchSize(r watch.Rect) (int, int) {
	w := int(r.W * 2)
	h := int(r.H * 2)
	if w < 320 {
		w = 320
	}
	if h < 180 {
		h = 180
	}
	if w > 1280 {
		w = 1280
		h = h * 1280 / w
		w = 1280
	}
	if h > 720 {
		w = w * 720 / h
		h = 720
	}
	return w, h
}

func (a *App) watchTrack(key string) videoTrack {
	if key == "" {
		return nil
	}
	if a.media.roomKey == key && a.media.player != nil {
		return engineOf(a.media.player)
	}
	if s := a.watch.sessions[key]; s != nil {
		return s.track
	}
	return nil
}

// watchQuality 是这一格实际在播的档位，显示在格子上，避免“墙上是原画、
// 点进去变蓝光”没人发现。平台自己降档时这里写的就是实际档。
func (a *App) watchQuality(key string) string {
	if a.media.roomKey == key {
		return a.media.upstream.Label
	}
	if s := a.watch.sessions[key]; s != nil {
		return s.up.Label
	}
	return ""
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
	seen := make(map[string]bool, len(a.WatchRooms))
	for _, room := range a.WatchRooms {
		key := watchKey(room)
		seen[key] = true
		r, ok := rects[key]
		if !ok {
			continue
		}
		w, h := watchSize(r)
		if key == a.WatchActive && a.media.roomKey == key {
			if tr := a.watchTrack(key); tr != nil && (a.watch.activeW != w || a.watch.activeH != h) {
				tr.SetPresentSize(w, h)
				a.watch.activeW, a.watch.activeH = w, h
			}
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

// watchHide 在墙不可见时调用：关掉所有非 active 会话，active 继续出声。
func (a *App) watchHide() {
	a.watch.init()
	for _, s := range a.watch.sessions {
		if s.key == a.WatchActive {
			continue
		}
		a.watchCloseSession(s)
	}
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
	quality := a.Quality
	room := s.room
	w, h := s.renderW, s.renderH
	go func() {
		fctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		info, v, err := fetchStream(fctx, prov, room.RoomID, "", quality)
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
			MaxWidth:      w,
			MaxHeight:     h,
			StartMuted:    true,
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
			p.SetMuted(true)
			p.SetVolume(float64(a.Volume))
			s.player, s.track = p, e
			s.stream, s.up = info, v
			s.loading = false
			s.err = ""
			s.firstFrame = p.Ready()
			s.failures = 0
			s.refreshAt = refreshAtFor(v)
			s.stillSince = time.Now()
			s.lastPos = 0
			if s.key == a.WatchActive {
				a.WatchPending = ""
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
		a.WatchPending = ""
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
		a.WatchPending = ""
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

// watchTick 驱动墙上所有非 media 持有的会话：首帧预算、断流、地址到期、重试。
//
// active 若由 a.media 持有，不在这里驱动——playerMedia 自己的心跳管它。
func (a *App) watchTick(now time.Time) {
	if len(a.watch.sessions) == 0 {
		return
	}
	for _, s := range a.watch.sessions {
		if s.player == nil {
			if s.loading {
				if !s.openedAt.IsZero() && now.Sub(s.openedAt) > 20*time.Second {
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
		if s.player.Playing() {
			pos := s.player.Position()
			if pos > s.lastPos {
				s.lastPos = pos
				s.stillSince = now
			} else if !s.stillSince.IsZero() && now.Sub(s.stillSince) > 6*time.Second {
				a.watchEnded(s)
				continue
			}
		} else {
			s.stillSince = now
		}
		if s.player.Ended() || s.player.Err() != nil {
			a.watchEnded(s)
			continue
		}
		if !s.refreshAt.IsZero() && now.After(s.refreshAt) {
			a.watchRestart(s, "地址到期")
		}
	}
}

// watchEnded 先问平台：真下播就落未开播，仍 live 才重取。
func (a *App) watchEnded(s *wallSession) {
	if s == nil || s.checking || s.player == nil {
		return
	}
	s.checking = true
	prov, roomID, win := s.prov, s.room.RoomID, a.Win
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
			a.watchRestart(s, "连接结束")
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
	slog.Info("观测墙重连", "key", s.key, "reason", reason, "failures", s.failures)
	a.invalidateWindows()
}

// watchActivate 单击格子：切音频焦点，不跳路由。
func (a *App) watchActivate(room Streamer) {
	key := watchKey(room)
	if key == "" || key == a.WatchActive {
		return
	}
	a.watch.init()
	old := a.WatchActive
	if a.media.roomKey != "" && a.media.roomKey != key {
		a.watchReleaseActive()
	}
	if old != "" && old != key {
		if s := a.watch.sessions[old]; s != nil && s.player != nil {
			s.player.SetMuted(true)
		}
	}
	a.WatchActive = key
	a.WatchPending = key
	a.Player = room
	slog.Info("观测墙激活", "key", key, "name", room.Name)
	a.Playing = true
	// 点格子 = 声音放开。用户之前主动静音过也恢复到一个可听的音量。
	if a.Volume <= 0 {
		if a.volumeBeforeMute > 0 {
			a.Volume = a.volumeBeforeMute
		} else {
			a.Volume = 0.6
		}
	}
	if s := a.watch.sessions[key]; s != nil && s.player == nil {
		// 点一个失败/离线的格子就是重试它，不用另设按钮。
		s.offline = false
		s.retryAt = time.Time{}
		s.failures = 0
		a.watchOpenSession(s)
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
	s.player.SetMuted(a.Volume <= 0)
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
		a.closeMedia()
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
	if r, ok := a.watch.rects[key]; ok {
		s.renderW, s.renderH = watchSize(r)
	} else {
		s.renderW, s.renderH = 640, 360
	}
	s.player.SetMuted(true)
	if s.track != nil {
		s.track.SetPresentSize(s.renderW, s.renderH)
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
	a.watch.activeW, a.watch.activeH = 0, 0
	a.closeDanmaku()
	a.DanmuLog = nil
	a.Danmu = nil
	a.panelSeen = 0
	a.panelPending = 0
	a.invalidateWindows()
}

// watchAdoptActive 进播放页时把墙上的 active 原样交给 a.media。
// 返回 true 表示已经接上，ensureMedia 不必再取流。
func (a *App) watchAdoptActive() bool {
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
	slog.Info("观测墙接管进播放页", "key", key, "name", s.room.Name)
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
	m.loading = false
	m.err = ""
	m.firstFrame = true
	m.openedAt = time.Now()
	m.pauseStarted = time.Time{}
	m.pausedFor = 0
	m.background = false
	m.refreshAt = refreshAtFor(s.up)
	a.pushedVolume = float64(a.Volume)
	m.player.SetMuted(false)
	m.player.SetVolume(float64(a.Volume))
	m.player.Play()
	if tr := engineOf(s.player); tr != nil {
		tr.SetPresenting(true)
	}
	a.Player = s.room
	a.Playing = true
	a.WatchPending = ""
	a.watch.activeW, a.watch.activeH = 0, 0
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
	a.WatchActive = ""
	a.WatchPending = ""
	a.watch.activeW, a.watch.activeH = 0, 0
	if key == "" {
		return
	}
	if a.media.roomKey == key {
		a.closeMedia()
	}
	if s := a.watch.sessions[key]; s != nil {
		a.watchCloseSession(s)
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

// watchAdd 收一间进观测盒；上限 9，重复不收。
func (a *App) watchAdd(room Streamer) bool {
	if room.Platform == "" || room.RoomID == "" {
		return false
	}
	if len(a.WatchRooms) >= watch.MaxRooms {
		a.notifyPlayback("观测盒最多 9 间")
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

// watchMove 把一间放到目标原来的位置；规则和 movePlatform / moveFollowsSection
// 同一套：先摘掉拖拽项，再插到目标索引上。active 跟房间身份走，不跟格子序号走。
func (a *App) watchMove(dragKey, targetKey string) {
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
	rooms := append([]Streamer(nil), a.WatchRooms...)
	moved := rooms[from]
	rooms = append(rooms[:from], rooms[from+1:]...)
	rooms = append(rooms, Streamer{})
	copy(rooms[to+1:], rooms[to:])
	rooms[to] = moved
	a.WatchRooms = rooms
	a.markDirty()
	a.invalidateWindows()
}

// watchMoveBy 是右键「前移 / 后移」：给键盘和读屏一条不依赖拖拽的路径。
func (a *App) watchMoveBy(room Streamer, delta int) {
	key := watchKey(room)
	for i, s := range a.WatchRooms {
		if watchKey(s) != key {
			continue
		}
		to := i + delta
		if to < 0 || to >= len(a.WatchRooms) {
			return
		}
		a.watchMove(key, watchKey(a.WatchRooms[to]))
		return
	}
}

// watchRemove 移出一间；如果是 active，声音一起停。
func (a *App) watchRemove(room Streamer) {
	key := watchKey(room)
	out := a.WatchRooms[:0]
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
