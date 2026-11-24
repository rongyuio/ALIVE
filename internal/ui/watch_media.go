package ui

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
	"alive/internal/watch"
)

// 直播间的会话层。
//
// 墙上每一格都是一间「无弹幕的单直播间缩小版」：全档取流、真实 AO、默认静音、
// 按格子物理像素呈现。只有焦点那一间出声；其余只静音，不重开。
//
// 会话只有一个家：a.watch.sessions，一间房一个 roomSession。播放页、墙格、
// 后台收听画的是同一个对象，谁在前台只是查表拿到它——没有任何搬移。焦点是
// 一个指针（App.focus），墙上的 active、后台收听、呈现尺寸都从它推导。
//
// 音量和播放态也住在这里。a.Volume / a.Playing 时代「音量既是全局又是每格」、
// 「播放态既是 App 又是播放器」的两份真相互相追，是切台后声音不对、暂停态
// 串台的来源。现在：焦点音量就是焦点会话的音量，App 上只留新房间的默认值。
//
// roomSession 是一个直播间的全部运行态：引擎、流、看门狗、音量、首帧预算。
//
// 墙格和播放页画的是**同一个对象**。以前这两边各有一份状态（roomSession 和
// playerMedia），靠 watchAdoptActive / watchReleaseActive 搬移引擎；每搬一次
// 都要重算呈现尺寸、重对焦点、重对音量，而「进播放页变成另一个直播间」、
// 「回到墙闪封面」、「清 WatchActive 变黑」这些 BUG 全是搬移的副作用。
//
// 现在没有搬移：一间房一个 roomSession，谁画它只是查表拿到它。
//
// 呈现尺寸不在这里——那是视图属性，由 presentSizeFor 求。
type roomSession struct {
	key  string
	room Streamer
	prov live.Provider

	// player 是这一路的播放器；nil 表示还没有引擎（取流中 / 失败 / 未开播）。
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
	// probing 表示正在静默探测这一格开没开播：只查 RoomInfo，不动画面、
	// 不动 loading/err。墙上不能因为一次探测就闪出加载态或刷新提示。
	probing bool

	// openedAt 是这一路当前阶段的起点：取流阶段是开始取流，引擎开出之后
	// 重置为引擎打开的时刻——首帧预算从后者算，取流慢不该吃掉出图的预算。
	openedAt   time.Time
	retryAt    time.Time
	failures   int
	refreshAt  time.Time
	firstFrame bool
	lastPos    time.Duration
	stillSince time.Time
	// stalled 表示画面停住了但还没到判死阈值：这一格在缓冲。
	stalled bool

	// present 是这一路该呈现多大。视图属性，由 applyPresent 从
	// presentSizeForSession 求出来再推给引擎；值没变就不推。
	present presentSize

	// volume 是这一间自己的音量（0–1），playing 是这一间的播放意图。
	//
	// 它们住在这里，不住在 App 上：以前 a.Volume / a.Playing 只表示焦点
	// 那一路，墙上每格又在 wall.volumes / player.Playing() 各记一份，
	// 两份真相互相追——切台后声音不对、暂停态串台都是这么来的。
	// 会话不在时，墙上的音量由 wall.volumes 冷存（见 roomVolume）。
	volume  float32
	playing bool
}

type wallMedia struct {
	sessions map[string]*roomSession
	// rects 是上一帧墙上的布局，presentSizeForSession 用它给格子设呈现尺寸。
	rects map[string]watch.Rect
	// volumes 是会话不在时每格的音量冷存（0–1），按 platform|roomID 记。
	// 建会话时移交给 roomSession.volume，关会话时（还在盒里的）写回来；
	// 同一时刻只有一个家。
	volumes map[string]float32
	// resume 是关掉引擎时留下的签名地址与档位表，按 platform|roomID 记。
	// 直播重连里最贵的一段不是网络，是重新问平台要地址；地址还在有效期
	// 内就直接 loadfile。只留元数据，不留引擎、不留连接。
	resume map[string]wallResume
	// tickPending 表示墙的心跳定时器已经排上，避免每帧重复排。
	tickPending bool
	// lastProbe 是上一次离线探测的时刻。一排离线格同时到点时错开，别让
	// 一次 tick 把整个平台问一遍。
	lastProbe time.Time
	// hiddenSince 是墙变成不可见的时刻；零值表示墙可见。只有用户开了
	// 「暂离时省流量」才在 watchReapGrace 之后回收。
	hiddenSince time.Time
}

// wallResume 是一格关掉引擎之后留下的"重启凭据"。
//
// 只有地址和档位表，没有引擎、没有连接、没有解码器。几 KB 的元数据，
// 换来的是回来时省掉一次平台往返（要签名地址 → 拿档位表）。
type wallResume struct {
	stream live.Stream
	up     live.Variant
	until  time.Time
}

// watchResumeGuess 是平台没声明地址寿命时，保留凭据的保守窗口。
//
// 猜短了只是多一次完整取流，猜长了会先失败一次再退回完整取流——两条路都
// 不会让用户看到错误，所以这里不需要精确。
const watchResumeGuess = 45 * time.Second

func (m *wallMedia) init() {
	if m.sessions == nil {
		m.sessions = make(map[string]*roomSession)
	}
	if m.rects == nil {
		m.rects = make(map[string]watch.Rect)
	}
	if m.volumes == nil {
		m.volumes = make(map[string]float32)
	}
	if m.resume == nil {
		m.resume = make(map[string]wallResume)
	}
}

// dropEngine 是这一间引擎的唯一关闭入口：关掉并从会话上摘下来。
//
// 只关会话自己持有的那一枚。后台还没交付的引擎由创建它的人关
// （adoptPlayback 拒收、runHotSwap 的失败回调），它们从来没挂到会话上。
func (s *roomSession) dropEngine() {
	if s == nil || s.player == nil {
		return
	}
	s.player.Close()
	s.player = nil
	s.track = nil
}

// attachPlayer 把新引擎的播放器挂到会话上，并把记录音量对齐到引擎的起点。
//
// 所有引擎都按 playback.Options.StartSilent 起播：mpv 的真实音量是 0。
// 但 ui.NewEngineVideoPlayer 把播放器记录的音量初始化成 1。音量写者
// applySessionVolume 用记录值判断「要不要推」——用户把音量拉到 100% 时，
// 目标 1 和记录 1 相等，第一次推送被跳过，引擎永远停在 0。分区卡片进
// 播放页没有声音就是这么来的（813702e）。
//
// 所以挂上播放器的第一件事是把记录值归零；真正的音量由写者随后推。
// 这也是测试夹具 fakeSession 一直在守的初值（「音量必须从 0 起」）。
func (s *roomSession) attachPlayer(p *ui.VideoPlayer) {
	if s == nil || p == nil {
		return
	}
	s.player = p
	p.SetVolume(0)
}

// watchVolume 读这一间自己的音量。
//
// 会话存在时以会话为准；会话还没建（重启后还没上墙）时读冷存；都没有就
// 继承新房间的默认音量。冷存只在"会话不在"时存在，建会话时移交过去，
// 关会话时（还在盒里的）写回来——同一时刻只有一个家。
func (a *App) watchVolume(key string) float32 {
	if s := a.watch.sessions[key]; s != nil {
		return s.volume
	}
	a.watch.init()
	if v, ok := a.watch.volumes[key]; ok {
		return v
	}
	return a.DefaultVolume
}

// setVolume 改焦点这一间的音量。
func (a *App) setVolume(v float32) {
	v = min(1, max(0, v))
	if a.focus == nil {
		a.DefaultVolume = v
		a.markDirty()
		return
	}
	a.focus.volume = v
	// 调焦点音量同时成为新房间的默认音量：和以前 a.Volume 的行为一致。
	a.DefaultVolume = v
	a.applyFocusAudio()
	a.markDirty()
}

// setTileVolume 改**某一间自己**记住的音量，不碰焦点。
//
// 墙上每一格都记着自己的音量，但只有焦点那一路在出声。非焦点格的滑条
// 以前会先 watchActivate 再改值，于是"调一个预览格的音量"实际把整个声音
// 焦点切走了——控件做了它看起来不该做的事。
func (a *App) setTileVolume(room Streamer, v float32) {
	key := watchKey(room)
	v = min(1, max(0, v))
	if s := a.watch.sessions[key]; s != nil {
		s.volume = v
	} else {
		a.watch.init()
		a.watch.volumes[key] = v
	}
	// 这格正好是焦点时才同步到真正在出声的那一路。
	if key == a.focusKey() {
		a.DefaultVolume = v
		a.applyFocusAudio()
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
	// 会话在时音量住在会话里；冷存只在没有会话时才有值。
	if sess := a.watch.sessions[watchKey(s)]; sess != nil {
		v := sess.volume
		r.Volume = &v
	} else if v, ok := a.watch.volumes[watchKey(s)]; ok {
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

// videoTrack 取这一间的呈现开关。墙格路径建引擎时直接记了 track（那是
// acceptWallEngine 拿到的 *playback.Engine）；媒体层起的引擎没有，就从
// player 取一次。两处指向同一个引擎。
func (s *roomSession) videoTrack() videoTrack {
	if s == nil {
		return nil
	}
	// 只有一个来源：从播放器取。acceptWallEngine 记下的 s.track 和这个
	// 指的是同一个 *playback.Engine，存两份只会让「换引擎时漏更新一份」
	// 这类 BUG 有地方藏。
	return engineOf(s.player)
}

// sessionFor 返回这一间的运行态，没有就建一个空的。
//
// 播放页从列表直接进来时，墙上没有这一格；这里建的对象和墙格用的是同一个
// 类型、同一张表，所以「上墙」不需要任何搬迁——它从一开始就是同一间房。
func (a *App) sessionFor(key string, room Streamer) *roomSession {
	a.watch.init()
	if s := a.watch.sessions[key]; s != nil {
		return s
	}
	s := &roomSession{
		key:     key,
		room:    room,
		volume:  a.watchVolume(key),
		playing: true,
	}
	// 冷存的音量移交给会话：之后同一时刻只有一个家。
	delete(a.watch.volumes, key)
	a.watch.sessions[key] = s
	return s
}

func (a *App) watchHas(key string) bool {
	for _, s := range a.WatchRooms {
		if watchKey(s) == key {
			return true
		}
	}
	return false
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

// watchPlayer 返回这一间此刻的播放器。墙格和播放页画的是同一个对象，
// 所以只需查会话表——媒体层不再单独持有另一份。
func (a *App) watchPlayer(key string) *ui.VideoPlayer {
	if key == "" {
		return nil
	}
	if s := a.watch.sessions[key]; s != nil {
		return s.player
	}
	return nil
}

// watchShow 是墙可见时的每帧同步：补齐会话、更新渲染尺寸、关掉已经不在盒里的。
func (a *App) watchShow(rects map[string]watch.Rect) {
	a.watch.init()
	wasHidden := !a.watch.hiddenSince.IsZero()
	a.watch.rects = rects
	// 墙又可见了：保留窗口从头计。
	a.watch.hiddenSince = time.Time{}
	seen := make(map[string]bool, len(a.WatchRooms))
	for _, room := range a.WatchRooms {
		key := watchKey(room)
		seen[key] = true
		// 上墙这一刻先把这格自己的音量定下来：没调过的格继承当时的全局
		// 音量，之后各格独立。会话已经在了就以会话为准。
		if a.watch.sessions[key] == nil {
			if _, ok := a.watch.volumes[key]; !ok {
				a.watch.volumes[key] = a.DefaultVolume
			}
		}
		if _, ok := rects[key]; !ok {
			continue
		}
		// 已有的会话：推一次呈现尺寸。值没变时 applyPresentTo 自己会跳过。
		// 焦点那一路也在这张表里，墙格和播放页画的是同一个播放器。
		if s := a.watch.sessions[key]; s != nil {
			a.applyPresentTo(s)
			continue
		}
		if a.Win == nil {
			// 视图测试没有窗口：不取流、不起引擎，格子停在封面上。
			continue
		}
		s := a.sessionFor(key, room)
		a.watchOpenSession(s)
	}
	for key, s := range a.watch.sessions {
		if !seen[key] {
			// 焦点那一间可能不在盒里（从列表打开的、收在岛里的）：
			// 它不在盒里不等于不要了，墙只画盒里的格子。
			if a.focus == s {
				continue
			}
			a.closeSession(s)
			continue
		}
		if wasHidden && s.offline {
			// 墙刚回到屏上：离线格立刻探一次，不用等离开前攒下的退避。
			s.retryAt = time.Time{}
		}
	}
}

// watchReapGrace 是"墙看不见"到"真的回收"之间的防抖窗口。
//
// 它不是一个策略性保留期：策略已经交给用户（WatchKeepPlaying）。这里只挡
// 一种情况——点进直播间、马上又退回来，一个来回不到两秒；为此把 11 路全拆
// 掉再重建纯属浪费。
//
// 以前这里是 30 秒，担的是"去首页瞄一眼就回来"这个场景。那条路现在由
// "离开后继续播放"开关和保留签名地址承担，不需要用解码时长去换。
const watchReapGrace = 2 * time.Second

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

// WatchHidden 是"窗口整体看不见"的入口：最小化、或者主窗口被小窗换下去。
//
// 和 watchHide 的区别只在调用者：Shell 每帧按路由判断"墙在不在屏上"，
// 而最小化时 Shell 根本不重建，所以那条路走不到它。没有这个入口，用户
// 开着「暂离时省流量」把窗口最小化，墙上的会话就永远不会被回收。
func (a *App) WatchHidden() { a.watchHide() }

// WatchShown 是它的反向：窗口回来了，保留窗口从头计。
// 墙真的重建时会由 watchShow 再清一次，这里只是让"最小化 → 恢复"这段
// 不留下一个已经到期的 hiddenSince。
func (a *App) WatchShown() {
	a.watch.init()
	a.watch.hiddenSince = time.Time{}
}

// watchReap 回收墙上已经看不见的会话——只在用户要求省流量时。
//
// 默认什么都不做：看不见的会话留着，只是不再渲染（见插件的渲染门控）。
// "暂离"的全部意义就是还会回来，拆掉再建就是把它变成"离开"。
//
// 用户开了「暂离时省流量」才回收：离开墙 watchReapGrace（2 秒防抖）之后
// 关掉非焦点会话，回来重新取流。这时省的是带宽，付的是回来的一次加载。
//
// 每拍调用，幂等：条件没到什么都不做，收完把标记清掉，下次隐藏重新计时。
func (a *App) watchReap(now time.Time) {
	a.watch.init()
	if a.watch.hiddenSince.IsZero() {
		return
	}
	if !a.WatchSaveData {
		return
	}
	if now.Sub(a.watch.hiddenSince) < watchReapGrace {
		return
	}
	focus := a.focusKey()
	for _, s := range a.watch.sessions {
		if s.key == focus {
			continue
		}
		a.closeSession(s)
	}
	a.watch.hiddenSince = time.Time{}
}

// takeResume 取走这一格保留的签名地址（一次性），过期了就丢掉。
func (a *App) takeResume(key string) (wallResume, bool) {
	a.watch.init()
	r, ok := a.watch.resume[key]
	if !ok {
		return wallResume{}, false
	}
	delete(a.watch.resume, key)
	if !time.Now().Before(r.until) {
		return wallResume{}, false
	}
	return r, true
}

// rememberResume 在关掉引擎时留下签名地址，供下次重连直接用。
func (a *App) rememberResume(s *roomSession) {
	if s == nil || s.up.URL == "" {
		return
	}
	until := s.up.Expires
	if until.IsZero() {
		until = time.Now().Add(watchResumeGuess)
	}
	if !time.Now().Before(until) {
		return
	}
	a.watch.init()
	a.watch.resume[s.key] = wallResume{stream: s.stream, up: s.up, until: until}
}

// closeSession 是会话唯一的销毁入口：留下重连凭据，把音量交回冷存（还在
// 盒里时），关引擎、删表项。焦点指向它时一并清掉——焦点不能指向已删的会话。
func (a *App) closeSession(s *roomSession) {
	if s == nil {
		return
	}
	// 关引擎之前把签名地址留下来：下次重连能省掉一次平台往返。
	a.rememberResume(s)
	if a.watchHas(s.key) {
		a.watch.init()
		a.watch.volumes[s.key] = s.volume
	} else {
		delete(a.watch.volumes, s.key)
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.dropEngine()
	delete(a.watch.sessions, s.key)
	if a.focus == s {
		a.focus = nil
		a.closeDanmaku()
		a.resetDanmuHistory()
	}
}

// watchOpenSession 取流并起这一格的引擎。所有结果都回到主线程再接纳。
func (a *App) watchOpenSession(s *roomSession) {
	if s == nil || s.loading || s.player != nil {
		return
	}
	// 离线格也允许重开：蹲开播靠的就是它。调用方负责退避。
	if s.offline {
		s.offline = false
		s.err = ""
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
	// 保留的凭据必须在主线程取走：那张表只属于主线程。
	resume, fromResume := a.takeResume(s.key)
	go func() {
		// 先试保留的签名地址：重连最贵的一段不是网络，是重新问平台要地址。
		// 打不开（过期 / 房间没了）就退回完整取流，不额外退避。
		if fromResume {
			if e, err := openNative(context.Background(), resume.up.URL, wallPlaybackOptions(resume.up)); err == nil {
				a.acceptWallEngine(s, ctx, seq, win, e, resume.stream, resume.up)
				return
			}
		}
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
		e, err := openNative(context.Background(), v.URL, wallPlaybackOptions(v))
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
		a.acceptWallEngine(s, ctx, seq, win, e, info, v)
	}()
}

// wallPlaybackOptions 是墙上每一格起播时的选项。
//
// 和播放页唯一的差别是 NoBackBuffer：直播不能回退，回退缓存永远不会被读到，
// 一屏 12 路各留一份是纯浪费。前向缓存不动，抗抖动靠的是它。
func wallPlaybackOptions(v live.Variant) playback.Options {
	return playback.Options{
		Headers:       v.Headers,
		Width:         v.Width,
		Height:        v.Height,
		FrameRate:     v.FrameRate,
		StartSilent:   true,
		SkipAudioGate: true,
		NoBackBuffer:  true,
		GPU:           true,
	}
}

// acceptWallEngine 把开好的引擎接到这一格上。必须在主线程调用（内部走 runOnMain）。
func (a *App) acceptWallEngine(s *roomSession, ctx context.Context, seq int, win *mygo.Window, e *playback.Engine, info live.Stream, v live.Variant) {
	runOnMain(win, func() {
		if ctx.Err() != nil || a.watch.sessions[s.key] != s || s.seq != seq {
			_ = e.Close()
			return
		}
		p := ui.NewEngineVideoPlayer(e)
		// 呈现尺寸不在这里推：applyPresent 每帧从 presentSizeFor 求一次，
		// 值变了才推给引擎。这一格上墙时就是格子物理像素。
		s.attachPlayer(p)
		s.track = e
		// 音量按「是不是焦点」求：非焦点预览一律 0，焦点落它自己的值。
		// 不能用 SetMuted：MyGo 把静音视频当「一张画」调度，系统要求
		// 减少动效、窗口被遮住或元素不再显示时会把它按住不播，时间轴
		// 随之停住——墙的看门狗就会把一路好好的流当断流重连。
		a.applySessionVolume(s)
		// 引擎已经开出来了：首帧预算从这里重新计时，播放器迟迟不出图
		// 时由 watchTick 接手，不能让取流阶段的耗时提前吃掉这段预算。
		s.openedAt = time.Now()
		s.stream, s.up = info, v
		// 平台确认的档位写回这一格自己的记录：激活 / 进播放页时读的就是它。
		a.learnRoomChoice(s.key, info, v)
		s.loading = false
		s.err = ""
		s.firstFrame = false
		s.refreshAt = refreshAtFor(v)
		s.stillSince = time.Now()
		s.lastPos = 0
		if s == a.focus {
			// 焦点这一间后开出来的引擎：把这一间自己的播放意图推上去。
			a.applyFocusAudio()
			a.applyPresentForce()
		}
		a.invalidateWindows()
	})
}

// focusFailed 处理「焦点这一间没有声音了」。
//
// 在播放页上保留焦点：错误层和岛上的退出键都还要用，用户能看见发生了什么。
// 在别处（墙上、后台收听）收掉焦点——没有声音就不该继续占着岛。
func (a *App) focusFailed(s *roomSession) {
	if s == nil || a.focus != s {
		return
	}
	if a.Router != nil && a.Router.Path() == routePlayer {
		return
	}
	a.closeFocus()
}

func (a *App) watchFail(s *roomSession, msg string) {
	if s == nil {
		return
	}
	s.dropEngine()
	s.loading = false
	s.err = msg
	s.firstFrame = false
	s.failures++
	s.retryAt = time.Now().Add(watchBackoff(s.failures))
	// 焦点是「谁在出声」。引擎已经关了还留着绿点，岛却按「没有引擎」
	// 回时钟，两个地方各说各话。收掉焦点：绿点消失、岛回时钟。
	a.focusFailed(s)
	a.invalidateWindows()
}

func (a *App) watchOffline(s *roomSession) {
	if s == nil {
		return
	}
	s.dropEngine()
	s.loading = false
	s.offline = true
	s.err = "主播暂时没有开播"
	s.firstFrame = false
	// 旧地址作废：这一格再开播要重新问平台。留着旧签名地址重开，只会拿
	// 一条已经没内容的流再等一次 20 秒首帧预算。
	s.up = live.Variant{}
	a.watch.init()
	delete(a.watch.resume, s.key)
	// 蹲开播：按离线节奏探测，上限 5 分钟。failures 不清零——一个长期
	// 没开播的房间不该每 30 秒问一次平台。
	s.failures++
	s.retryAt = time.Now().Add(offlineProbeBackoff(s.failures, s.key))
	// 下播就是没有声音：焦点跟着收掉，绿点和岛一起消失。
	a.focusFailed(s)
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

// offlineProbeBackoff 是离线格探测开播的间隔：30s、1m、2m、4m，再加按房间
// 摊开的抖动（最多 59 秒）。
//
// 上限 4m59s 来自产品要求：主播开播之后，墙上最多 5 分钟跟上。不要求实时。
// 抖动按房间号算而不是随机，测试里可复现，也保证一排离线格不同时到点。
func offlineProbeBackoff(failures int, key string) time.Duration {
	base := 30 * time.Second
	for i := 1; i < failures && base < 4*time.Minute; i++ {
		base *= 2
	}
	if base > 4*time.Minute {
		base = 4 * time.Minute
	}
	var h uint32 = 2166136261 // FNV-1a
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return base + time.Duration(h%60)*time.Second
}

// wallOnScreen 报告墙此刻是不是真的在屏上：路由在观测墙，且没有被最小化
// 或小窗换下去。离线探测只在它为真时发生——看不见的格子开没开播没有意义。
func (a *App) wallOnScreen() bool {
	return a.onWatchWall() && a.watch.hiddenSince.IsZero()
}

// watchProbe 静默问一次平台这一格开没开播。
//
// 无感是硬要求：探测期间不动 loading、err、offline，格子上还是
// 「主播暂时没有开播」，没有 spinner、没有「正在刷新」。只有平台说开播了，
// 才走正常起播——那时才允许出现加载态。
func (a *App) watchProbe(s *roomSession, now time.Time) {
	if s == nil || s.probing || s.loading || s.player != nil || s.checking {
		return
	}
	a.watch.init()
	// 一排离线格同时到点时错开：一次 tick 最多起一个探测。
	if !a.watch.lastProbe.IsZero() && now.Sub(a.watch.lastProbe) < 500*time.Millisecond {
		return
	}
	prov, ok := providerFor(live.Platform(s.room.Platform))
	if !ok {
		s.retryAt = now.Add(offlineProbeBackoff(s.failures+1, s.key))
		return
	}
	ri, ok := prov.(live.RoomInfoer)
	if !ok {
		// 平台没有单点查询：只能退回完整取流，退避照算。
		s.retryAt = now.Add(offlineProbeBackoff(s.failures+1, s.key))
		a.watchOpenSession(s)
		return
	}
	win := a.Win
	if win == nil {
		return
	}
	s.probing = true
	a.watch.lastProbe = now
	s.failures++
	s.retryAt = now.Add(offlineProbeBackoff(s.failures, s.key))
	key, roomID := s.key, s.room.RoomID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		room, err := ri.RoomInfo(ctx, roomID)
		runOnMain(win, func() {
			if a.watch.sessions[key] != s {
				return
			}
			s.probing = false
			if err != nil || !room.Live {
				// 还是没开播：什么都不变，格子不闪。
				return
			}
			s.offline = false
			s.err = ""
			s.failures = 0
			s.retryAt = time.Time{}
			s.up = live.Variant{}
			a.watchOpenSession(s)
		})
	}()
}

// watchFirstFrame 记这一格真的出了第一帧。退避只在这里清零。
//
// 引擎开出来不算数：一条平台说在播、实际不出帧的流，如果按"开出来了"清零
// failures，就会 20 秒超时 → 2 秒重开 → 又清零，永远循环下去。
func (a *App) watchFirstFrame(s *roomSession) {
	if s == nil || s.firstFrame {
		return
	}
	s.firstFrame = true
	s.failures = 0
	s.retryAt = time.Time{}
}

// watchFirstFrameLost 处理"引擎开着、过了首帧预算还没画面"。
//
// 先收掉引擎（这一格立刻回到可重试状态），再问一次平台：真下播就落未开播，
// 之后按离线节奏探测；还在播才按失败退避重开。不问平台的话，平台已经下播
// 的房间会被一轮轮重开，永远按"在播"处理。
func (a *App) watchFirstFrameLost(s *roomSession) {
	if s == nil || s.checking {
		return
	}
	a.watchRestart(s, "首帧超时")
	prov := s.prov
	if prov == nil {
		prov, _ = providerFor(live.Platform(s.room.Platform))
	}
	ri, ok := prov.(live.RoomInfoer)
	win := a.Win
	if !ok || win == nil {
		return
	}
	s.checking = true
	key, roomID := s.key, s.room.RoomID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		room, err := ri.RoomInfo(ctx, roomID)
		runOnMain(win, func() {
			if a.watch.sessions[key] != s {
				return
			}
			s.checking = false
			if err != nil || room.Live {
				return
			}
			a.watchOffline(s)
		})
	}()
}

// watchReload 是格子控制台上的「重新加载」，作用于当前 hover 的那一格。
//
// 同一间房只有一个会话：播放页上看的就是它时走媒体的整路重取（保留弹幕），
// 墙上预览走墙的重开。两条路都先清掉终态与退避——用户点的这一次不该再等
// 上一轮攒出来的 2 分钟。
func (a *App) watchReload(room Streamer) {
	key := watchKey(room)
	s := a.watch.sessions[key]
	if s == nil {
		return
	}
	if s == a.focus && a.fullSize() {
		s.offline = false
		s.err = ""
		s.failures = 0
		s.retryAt = time.Time{}
		a.retryStream()
		return
	}
	a.watchRestart(s, "")
	s.offline = false
	s.err = ""
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

// wallStalled 报告这一格是不是在缓冲：有画面、在播，但时间轴停住了。
//
// 判据和播放页的 bufferingPill 同源：不信 VideoPlayer.Buffering()（直播 HLS
// 上它会永久停在 true），看时间轴有没有动。
func wallStalled(ready, playing bool, since, now time.Time) bool {
	return ready && playing && !since.IsZero() && now.Sub(since) > stalledThreshold
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
		// 焦点由媒体层驱动（播放页的首帧/断流/换地址在 trackFirstFrame，
		// 后台收听的音频时钟在 trackBackground）。这里再管一遍就是第二套
		// 看门狗：同一条断流会被两边各重连一次。
		if s == a.focus {
			continue
		}
		if s.player == nil {
			if s.loading {
				if !s.openedAt.IsZero() && now.Sub(s.openedAt) > firstFrameBudget {
					a.watchFail(s, "首帧超时")
				}
				continue
			}
			if s.offline {
				// 蹲开播：只问平台「开没开播」，不取流、不碰画面。
				//
				// 不在墙上就完全不探测——看不见的格子开没开播没有意义，
				// 用户回到墙上时 watchShow 会把退避清零，立刻探一次。
				if !a.wallOnScreen() {
					continue
				}
				if !s.retryAt.IsZero() && now.Before(s.retryAt) {
					continue
				}
				a.watchProbe(s, now)
				continue
			}
			if !s.retryAt.IsZero() && now.Before(s.retryAt) {
				continue
			}
			a.watchOpenSession(s)
			continue
		}
		if s.player.Ready() {
			a.watchFirstFrame(s)
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
		s.stalled = wallStalled(s.player.Ready(), s.player.Playing(), s.stillSince, now)
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
			a.watchFirstFrameLost(s)
		}
	}
}

// watchEnded 先问平台：真下播就落未开播，仍 live 才重取。
//
// why 是这一路的实际死因（时间轴停住 / 播放器结束 / 报错），只用于日志和
// 重连原因——平台确认还在播时，用户看到的仍是「正在重连」，而不是故障。
func (a *App) watchEnded(s *roomSession, why string) {
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

func (a *App) watchRestart(s *roomSession, reason string) {
	if s == nil {
		return
	}
	s.dropEngine()
	s.loading = false
	s.firstFrame = false
	s.failures++
	s.retryAt = time.Now().Add(watchBackoff(s.failures))
	s.err = reason
	slog.Info("观测墙重连", "key", s.key, "name", s.room.Name,
		"reason", reason, "failures", s.failures)
	a.invalidateWindows()
}

// watchTileDead 报告这一格是不是已知没有声音：没有引擎、没在取流，而且
// 已经落了失败或未开播的终态。点它只该重试，不该抢焦点。
//
// 「抢了会先亮绿点、展开岛，等这一路再失败又一起收掉——一次没有意义的
// 闪动，还会把原来的声音焦点弄丢。」这条判据同时被 watchActivate 和
// watchOpenPlayer 用，两处各写一遍迟早会漂。
func (a *App) watchTileDead(key string) bool {
	s := a.watch.sessions[key]
	return s != nil && s.player == nil && !s.loading && (s.offline || s.err != "")
}

// watchActivate 是墙上「在墙上听」：切声音焦点，不跳路由。单击格子走的也是它。
//
// 焦点只有一个写者——setFocus。这里只负责两件本地判断：已经是焦点的
// 格点一下是重试；已知没有声音的格不抢焦点，只重试。
func (a *App) watchActivate(room Streamer) {
	key := watchKey(room)
	if key == "" {
		return
	}
	s := a.watch.sessions[key]
	if a.focusKey() == key {
		// 已经是焦点：失败/未开播的一格点一下就是重试。
		if s != nil && s.player == nil && !s.loading {
			a.watchReload(room)
		}
		return
	}
	// 已知没有声音（离线 / 取流失败，也没有引擎在跑）：只重试，不抢焦点。
	if a.watchTileDead(key) {
		a.watchReload(room)
		return
	}
	if s == nil {
		s = a.sessionFor(key, room)
	}
	a.setFocus(s)
	if s.player == nil {
		a.watchOpenSession(s)
	}
	a.invalidateWindows()
}

// watchOpenPlayer 从墙上打开某一间：双击格子和控制条上的「放大观测」都走这里。
//
// 在播 / 在载的一格先切焦点（同一个引擎接进播放页，不重取流）；死格先
// 重试一轮，播放页自己画错误层。目标永远是这一间，不是墙上原来的 active。
func (a *App) watchOpenPlayer(room Streamer) {
	if watchKey(room) == "" {
		return
	}
	if a.watchTileDead(watchKey(room)) {
		a.watchReload(room)
	} else {
		a.watchActivate(room)
	}
	a.openRoom(room)
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

// watchRemove 移出一间；如果它是焦点，声音一起停。盒子不再显示它，会话也不该留。
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
	if a.focusKey() == key {
		// 移出盒子之后 endSessionFocus 会按"不在盒里"关掉它；closeFocus 同时
		// 清焦点、让岛回时钟。
		a.closeFocus()
	}
	if s := a.watch.sessions[key]; s != nil {
		a.closeSession(s)
	}
	a.markDirty()
	a.invalidateWindows()
}
