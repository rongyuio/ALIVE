package ui

import (
	"context"
	"log/slog"
	"os"
	"time"

	"alive/internal/diag"
)

// logRef 是一轮会话的关联坐标：logger 已经带 run/room/session，start 是
// 相对毫秒的起点。后台 goroutine 只拿它，不回读 App。
//
// 它取代了原来的 logStage(id, base, stage, ...)：id 和 base 不再散落在
// 调用点，房间也不再需要靠相邻行猜。
type logRef struct {
	log     *slog.Logger
	room    string
	session string
	start   time.Time
}

func (r logRef) event(level slog.Level, event, msg string, args ...any) {
	if r.log == nil {
		return
	}
	args = append([]any{"event", event}, args...)
	if !r.start.IsZero() {
		args = append(args, "ms", time.Since(r.start).Milliseconds())
	}
	r.log.Log(context.Background(), level, msg, args...)
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
func wallLog(room, session string) *slog.Logger {
	return diag.Ref{Sub: "wall", Room: room, Session: session}.Logger()
}

// event 记一条墙格事件。s.log 可能还没建（视图测试、会话刚创建），直接丢弃。
func (s *roomSession) event(level slog.Level, event, msg string, args ...any) {
	if s == nil || s.log == nil {
		return
	}
	diag.Emit(context.Background(), s.log, level, event, msg, args...)
}

// mpvLogLevel 是 mpv 自己的日志订阅级别。默认 warn；排障时用环境变量提高到
// info / v，不需要重编。它不是业务开关，只影响记录详细度。
func mpvLogLevel() string {
	if v := os.Getenv("ALIVE_MPV_LOG_LEVEL"); v != "" {
		return v
	}
	return "warn"
}
