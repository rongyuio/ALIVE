package ui

import (
	"os"
	"time"

	"github.com/rs/zerolog"

	"alive/internal/diag"
)

// logRef 是一轮会话的关联坐标：logger 已经带 run/room/session，start 是
// 相对毫秒的起点。后台 goroutine 只拿它，不回读 App。
//
// 它取代了原来的 logStage(id, base, stage, ...)：id 和 base 不再散落在
// 调用点，房间也不再需要靠相邻行猜。
type logRef struct {
	log     *zerolog.Logger
	room    string
	session string
	start   time.Time
}

// event 返回这一轮的空事件，调用点直接链式写字段：
//
//	m.log.event().Str(diag.Event, diag.EvSessionBegin).Msg("开始起播")
//
// ms（相对 r.start 的耗时）已经挂上，调用点不再各自算。zerolog 的事件方法
// 对 nil 安全，日志没配好时整条静默丢弃——比在几十处加 nil 判断划算。
func (r logRef) event() *zerolog.Event {
	if r.log == nil {
		return nil
	}
	e := r.log.Info()
	if !r.start.IsZero() {
		e = e.Int64(diag.MS, time.Since(r.start).Milliseconds())
	}
	return e
}

// debug 与 event 同源，只是级别不同。zerolog 的级别是构造时定的，所以
// 四个级别各一个入口，而不是传一个 level 参数再在内部 switch。
func (r logRef) debug() *zerolog.Event {
	if r.log == nil {
		return nil
	}
	e := r.log.Debug()
	if !r.start.IsZero() {
		e = e.Int64(diag.MS, time.Since(r.start).Milliseconds())
	}
	return e
}

func (r logRef) warn() *zerolog.Event {
	if r.log == nil {
		return nil
	}
	e := r.log.Warn()
	if !r.start.IsZero() {
		e = e.Int64(diag.MS, time.Since(r.start).Milliseconds())
	}
	return e
}

func (r logRef) err() *zerolog.Event {
	if r.log == nil {
		return nil
	}
	e := r.log.Error()
	if !r.start.IsZero() {
		e = e.Int64(diag.MS, time.Since(r.start).Milliseconds())
	}
	return e
}

func newLogRef(sub, room, session string, start time.Time) logRef {
	return logRef{
		log:     diag.Ref{Sub: sub, Room: room, Session: session}.Logger(),
		room:    room,
		session: session,
		start:   start,
	}
}

// wallLog 是墙格的 logger。session 随每次重开变化（watch#N），room 不变。
func wallLog(room, session string) *zerolog.Logger {
	return diag.Ref{Sub: "wall", Room: room, Session: session}.Logger()
}

// event 返回墙格这一路的空事件，调用点直接链式写字段。
//
// s.log 可能还没建（视图测试、会话刚创建），那时返回 nil，整条静默丢弃。
func (s *roomSession) event() *zerolog.Event {
	if s == nil || s.log == nil {
		return nil
	}
	return s.log.Info()
}

func (s *roomSession) eventWarn() *zerolog.Event {
	if s == nil || s.log == nil {
		return nil
	}
	return s.log.Warn()
}

func (s *roomSession) eventError() *zerolog.Event {
	if s == nil || s.log == nil {
		return nil
	}
	return s.log.Error()
}

// mpvLogLevel 是 mpv 自己的日志订阅级别。默认 warn；排障时用环境变量提高到
// info / v，不需要重编。它不是业务开关，只影响记录详细度。
func mpvLogLevel() string {
	if v := os.Getenv("ALIVE_MPV_LOG_LEVEL"); v != "" {
		return v
	}
	return "warn"
}
