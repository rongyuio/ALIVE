// Package diag 定义诊断事件的语言：字段名、事件名、关联标识、错误保真与脱敏。
//
// 它不是日志库。日志库回答「怎么把一行写出去」，diag 回答「为了让下一个人
// 在五分钟内定位问题，这一行必须包含什么」。
//
// 三条硬约定，所有调用点共用：
//
//  1. 每个事件都有稳定的英文 event 名和固定的字段名。msg 是给人看的中文，
//     event 是给 alive-diag 和测试用的键；改文案不改 event。
//  2. 关联标识只有一套：run（进程）→ room（platform|roomID）→ session
//     （一轮起播/一路墙格）→ engine（一个播放引擎实例）。任何一层都不许
//     另造第二套 id。
//  3. 错误永远走 Err：class 之外保留可安全记录的 op/status/host/首行原文；
//     原文先过 Sanitize。只留一个词等于没记。
package diag

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// Level 是 diag 自己的级别语言。
//
// 它是 zerolog.Level 的别名，但调用点只认这里的名字：日志库换了，改这一处，
// 几十个调用点不用跟着动。
type Level = zerolog.Level

const (
	LevelDebug = zerolog.DebugLevel
	LevelInfo  = zerolog.InfoLevel
	LevelWarn  = zerolog.WarnLevel
	LevelError = zerolog.ErrorLevel
)

// base 是进程级 logger，由 applog.Setup 注入。
//
// 放在 diag 而不是让调用点去拿 applog：live / playback 这些包只需要认识
// diag 这一种日志语言，不该为了写一行日志去依赖日志的落盘方式。
var base atomic.Pointer[zerolog.Logger]

// SetLogger 由 applog 在 Setup 时调用。传 nil 恢复成丢弃一切的空 logger。
func SetLogger(l *zerolog.Logger) {
	base.Store(l)
}

// bootstrap 是 applog 就绪之前的兜底：写 stderr，一行一个 JSON。
//
// 必须是真的写出去，不能是 Nop。有些日志发生在 main() 之前——ui 包的 init
// 里注册字体，失败时要留一条；那时日志文件还没打开，丢了就永远查不到「为什么
// 字体没生效」。用 sync.OnceValue 只建一次，且不碰任何全局状态。
var bootstrap = sync.OnceValue(func() *zerolog.Logger {
	l := zerolog.New(os.Stderr).With().Timestamp().Logger()
	return &l
})

// L 返回进程级 logger。applog 就绪前走 stderr 兜底，不会静默吞掉。
func L() *zerolog.Logger {
	if l := base.Load(); l != nil {
		return l
	}
	return bootstrap()
}

// 字段名。全部小写、下划线分隔；带单位的字段以单位结尾。
const (
	Event   = "event"
	Sub     = "sub"
	Time    = "time"
	Msg     = "msg"
	Run     = "run"
	Room    = "room"
	Session = "session"
	Engine  = "engine"
	Role    = "role"

	Stage    = "stage"
	Cause    = "cause"
	Attempt  = "attempt"
	Failures = "failures"
	Restarts = "restarts"

	RetryMS      = "retry_ms"
	RetryAt      = "retry_at"
	SinceFrameMS = "since_frame_ms"
	AgeMS        = "age_ms"
	OpenMS       = "open_ms"
	MS           = "ms"
	UptimeMS     = "uptime_ms"
	StalledMS    = "stalled_ms"
	RecoverMS    = "recover_ms"

	Live        = "live"
	Quality     = "quality"
	Requested   = "requested"
	Applied     = "applied"
	Line        = "line"
	Transport   = "transport"
	GPU         = "gpu"
	GPUFrames   = "gpu_frames"
	Renders     = "renders"
	HWDec       = "hwdec"
	DeclaredW   = "declared_width"
	DeclaredH   = "declared_height"
	DecodedW    = "decoded_width"
	DecodedH    = "decoded_height"
	PositionMS  = "pos_ms"
	AudioMS     = "audio_ms"
	CacheMS     = "cache_ms"
	CachePct    = "cache_pct"
	AVSyncS     = "avsync_s"
	Buffering   = "buffering"
	AudioOK     = "audio_ok"
	Module      = "module"
	Text        = "text"
	Variants    = "variants"
	Lines       = "lines"
	NewFrames   = "new_frames"
	RenderCalls = "render_calls"
	URLHost     = "url_host"
	DisplayFPS  = "display_fps"
	VFPS        = "vfps"
	Dropped     = "dropped"
	Rooms       = "rooms"
	Path        = "path"
	File        = "file"
	SizeBytes   = "size_bytes"
	// LevelKey 是 "level" 这个字段名。它不能叫 Level：Level 是下面的级别类型，
	// 调用点写 diag.LevelInfo 比 diag.LogLevelInfo 顺手得多。
	LevelKey = "level"
	Reason   = "reason"
	Result   = "result"
	From     = "from"
	To       = "to"

	// 错误以 slog group 落盘，logfmt 里是 err.class / err.text 这种键。
	// 读侧和写侧共用这些常量，重命名不会让查询静默变空。
	ErrField         = "err"
	ErrClassField    = "class"
	ErrTypeField     = "type"
	ErrPlatformField = "platform"
	ErrOpField       = "op"
	ErrStatusField   = "status"
	ErrTextField     = "text"
	ErrHostField     = "host"
	ErrTimeoutField  = "timeout"
	ErrClassKey      = "err.class"
	ErrTextKey       = "err.text"
	ErrOpKey         = "err.op"
	ErrStatusKey     = "err.status"
	ErrHostKey       = "err.host"
	ErrPlatformKey   = "err.platform"
	ErrTypeKey       = "err.type"
)

// 事件名。第一个点号前是子系统，后面是状态机里的那条边沿。
//
// 命名规则：进入用动词原形（begin/open/fetch），离开用过去式或 fail/close；
// 同一个状态机必须成对出现，只有一半的事件视为缺陷。
const (
	EvRunStart = "run.start"
	EvRunStop  = "run.stop"

	EvSessionBegin       = "session.begin"
	EvSessionCancel      = "session.cancel"
	EvSessionPath        = "session.path"
	EvSessionOpenFail    = "session.open_fail"
	EvSessionVideoStall  = "session.video_stall"
	EvSessionFetch       = "session.fetch"
	EvSessionFetchFail   = "session.fetch_fail"
	EvSessionOpen        = "session.open"
	EvSessionFirstFrame  = "session.first_frame"
	EvSessionFail        = "session.fail"
	EvSessionRecover     = "session.recover"
	EvSessionSwapStart   = "session.swap_start"
	EvSessionSwapDone    = "session.swap_done"
	EvSessionSwapFail    = "session.swap_fail"
	EvSessionBuffer      = "session.buffer"
	EvSessionAudioStop   = "session.audio_stall"
	EvSessionRetry       = "session.retry"
	EvSessionQualityPick = "session.quality_pick"
	EvSessionLinePick    = "session.line_pick"

	EvWallShow       = "wall.show"
	EvWallHide       = "wall.hide"
	EvWallOpen       = "wall.open"
	EvWallReady      = "wall.ready"
	EvWallFirstFrame = "wall.first_frame"
	EvWallFail       = "wall.fail"
	EvWallOffline    = "wall.offline"
	EvWallRestart    = "wall.restart"
	EvWallClose      = "wall.close"
	EvWallProbe      = "wall.probe"
	EvWallReload     = "wall.reload"

	EvEngineOpen           = "engine.open"
	EvEngineOpenFail       = "engine.open_fail"
	EvEngineClose          = "engine.close"
	EvEngineDestroy        = "engine.destroy"
	EvEngineDestroyTimeout = "engine.destroy_timeout"
	EvEngineGPUFallback    = "engine.gpu_fallback"

	EvFollowsRefresh     = "follows.refresh"
	EvFollowsRefreshDone = "follows.refresh_done"

	EvFocusChange   = "focus.change"
	EvPresentChange = "present.change"
	EvRouteChange   = "route.change"

	EvDanmakuConnect = "danmaku.connect"
	EvDanmakuFail    = "danmaku.fail"
	EvDanmakuRecover = "danmaku.recover"

	EvStateLoadFail = "state.load_fail"
	EvStateSaveFail = "state.save_fail"

	EvMpv        = "mpv"
	EvLogDropped = "log.dropped"
	EvLogRotate  = "log.rotate"
	EvLogStart   = "log.start"
	EvLogStop    = "log.stop"
	EvHang       = "hang"
	EvTraceStart = "trace.start"
	EvTraceStop  = "trace.stop"
)

// Ref 是一行日志的关联坐标。空字段不写。
type Ref struct {
	Sub     string
	Room    string
	Session string
	Engine  string
}

// Logger 返回带关联字段的 logger。run 由 applog 统一注入，这里不重复；
// 重复就会有两个写者。
func (r Ref) Logger() *zerolog.Logger {
	l := L().With()
	if r.Sub != "" {
		l = l.Str(Sub, r.Sub)
	}
	if r.Room != "" {
		l = l.Str(Room, r.Room)
	}
	if r.Session != "" {
		l = l.Str(Session, r.Session)
	}
	if r.Engine != "" {
		l = l.Str(Engine, r.Engine)
	}
	lg := l.Logger()
	return &lg
}

// Level 是 diag 自己的级别语言，只是 zerolog.Level 的别名。
//
// 调用点写 diag.LevelWarn 而不是 zerolog.WarnLevel：日志库真换了，改这一处。

// ParseLevel 把字符串解析成级别。未知值返回 info 和 false。
func ParseLevel(s string) (Level, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, true
	case "", "info":
		return LevelInfo, true
	case "warn", "warning":
		return LevelWarn, true
	case "error":
		return LevelError, true
	}
	return LevelInfo, false
}
