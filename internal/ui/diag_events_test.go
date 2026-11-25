package ui

import (
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"alive/internal/diag"
	"alive/internal/live"
)

// capture 收集诊断事件，按 key=value 摊平（err.class 这种嵌套对象也摊平）。
type capture struct {
	mu   sync.Mutex
	list []map[string]string
}

func (c *capture) add(m map[string]string) {
	c.mu.Lock()
	c.list = append(c.list, m)
	c.mu.Unlock()
}

func (c *capture) events() []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]string(nil), c.list...)
}

func (c *capture) find(event string) map[string]string {
	for _, m := range c.events() {
		if m["event"] == event {
			return m
		}
	}
	return nil
}

// captureWriter 是捕获侧的真管道：事件先真的序列化成 JSON，再摊平回来。
//
// 以前这里是 slog 的 Handler，能看到每个 Attr。zerolog 没有那样的接口——
// 事件只能写不能读。于是捕获点挪到 writer，测的是**落盘形状**：嵌套的 err
// 真的落成了对象、数字真的是数字。这比逐 Attr 展开更接近读侧看到的东西。
type captureWriter struct{ c *capture }

func (w *captureWriter) Write(p []byte) (int, error) {
	var obj map[string]any
	if err := json.Unmarshal(p, &obj); err != nil {
		return len(p), nil
	}
	m := map[string]string{}
	flattenInto("", obj, m)
	w.c.add(m)
	return len(p), nil
}

func flattenInto(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(key, sub, out)
		}
	case string:
		out[prefix] = t
	case bool:
		if t {
			out[prefix] = "true"
		} else {
			out[prefix] = "false"
		}
	case float64:
		out[prefix] = trimFloat(t)
	case nil:
	default:
		out[prefix] = ""
	}
}

// trimFloat 让 403 变成 "403" 而不是 "403.000000"。
func trimFloat(f float64) string {
	n := int64(f)
	if float64(n) == f {
		return strconv.FormatInt(n, 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func captureLogs(t *testing.T) *capture {
	t.Helper()
	c := &capture{}
	old := diag.L()
	l := zerolog.New(&captureWriter{c: c}).Level(zerolog.DebugLevel).With().Logger()
	diag.SetLogger(&l)
	t.Cleanup(func() { diag.SetLogger(old) })
	return c
}

// 墙的状态机必须留下成对的边沿：失败、首帧、重连、未开播、关闭。
//
// 这条测试盯的不是「有没有日志」，而是每一条都带 room/session 和排障字段。
// 期望字段来自 doc/architecture/diagnostics.md 的事件表，不是实现。
func TestWallLifecycleEvents(t *testing.T) {
	c := captureLogs(t)
	app := NewMockApp()
	room := Streamer{Platform: "douyu", RoomID: "1", Name: "测试房间"}
	key := watchKey(room)
	s := app.sessionFor(key, room)
	s.log = wallLog(key, "watch#1")
	s.openedAt = time.Now().Add(-1200 * time.Millisecond)

	app.watchFail(s, "fetch", "取流失败", &live.UpstreamError{
		Platform: live.Douyu, Op: "stream", Status: 403, Message: "forbidden",
	})
	if got := c.find(diag.EvWallFail); got == nil {
		t.Fatal("没有 wall.fail")
	} else {
		for _, k := range []string{"room", "session", "stage", "cause", "err.class", "err.status", "retry_ms"} {
			if got[k] == "" {
				t.Fatalf("wall.fail 缺 %s: %v", k, got)
			}
		}
		if got["err.status"] != "403" {
			t.Fatalf("err.status=%q", got["err.status"])
		}
	}

	s.openedAt = time.Now().Add(-900 * time.Millisecond)
	app.watchFirstFrame(s)
	if got := c.find(diag.EvWallFirstFrame); got == nil {
		t.Fatal("没有 wall.first_frame")
	} else {
		if got["room"] != key || got["session"] != "watch#1" {
			t.Fatalf("首帧事件身份不对: %v", got)
		}
		if got["open_ms"] == "" || got["failures_before"] == "" {
			t.Fatalf("首帧事件缺 open_ms/failures_before: %v", got)
		}
	}

	app.watchRestart(s, "first-frame-timeout")
	if got := c.find(diag.EvWallRestart); got == nil {
		t.Fatal("没有 wall.restart")
	} else {
		if got["attempt"] != "1" {
			t.Fatalf("attempt=%q（restarts 应该从 1 开始）", got["attempt"])
		}
		if got["cause"] != "first-frame-timeout" || got["retry_ms"] == "" {
			t.Fatalf("重连事件缺原因/退避: %v", got)
		}
	}

	app.watchOffline(s)
	if got := c.find(diag.EvWallOffline); got == nil {
		t.Fatal("没有 wall.offline")
	} else if got["retry_ms"] == "" {
		t.Fatalf("未开播事件缺 retry_ms: %v", got)
	}

	app.closeSession(s, "test")
	if got := c.find(diag.EvWallClose); got == nil {
		t.Fatal("没有 wall.close")
	} else {
		if got["cause"] != "test" {
			t.Fatalf("关闭原因=%q", got["cause"])
		}
		if got["age_ms"] == "" || got["restarts"] == "" {
			t.Fatalf("关闭事件缺 age_ms/restarts: %v", got)
		}
	}
}

// 焦点切换是「谁在响」的唯一答案，必须有 from/to。
func TestFocusChangeEvent(t *testing.T) {
	c := captureLogs(t)
	app := NewMockApp()
	room := Streamer{Platform: "bilibili", RoomID: "9", Name: "焦点房"}
	s := app.sessionFor(watchKey(room), room)
	app.setFocus(s)
	got := c.find(diag.EvFocusChange)
	if got == nil {
		t.Fatal("没有 focus.change")
	}
	if got["to"] != watchKey(room) {
		t.Fatalf("to=%q", got["to"])
	}
}

// 录制一条 JSON-ish 的日志行给 alive-diag 的解析测试用？不——这里只保证
// 事件名常量不会被误改成中文，机器侧依赖它。
func TestEventNamesAreStable(t *testing.T) {
	for _, name := range []string{
		diag.EvWallOpen, diag.EvWallReady, diag.EvWallFirstFrame,
		diag.EvWallFail, diag.EvWallOffline, diag.EvWallRestart,
		diag.EvWallClose, diag.EvSessionFirstFrame, diag.EvSessionFail,
		diag.EvEngineOpen, diag.EvEngineClose, diag.EvRunStart, diag.EvRunStop,
	} {
		if name == "" || name[0] < 'a' || name[0] > 'z' || !containsDot(name) {
			t.Fatalf("事件名必须是 subsystem.edge 形式: %q", name)
		}
	}
}

func containsDot(s string) bool {
	for _, r := range s {
		if r == '.' {
			return true
		}
	}
	return false
}

// 页面切换必须有 from/to；同一页换台由 focus.change 覆盖，不在这里重复。
func TestRouteChangeEvent(t *testing.T) {
	c := captureLogs(t)
	app := NewMockApp()
	if app.Router == nil {
		t.Fatal("测试 App 没有 Router")
	}
	app.pushPage(routeFollows)
	got := c.find(diag.EvRouteChange)
	if got == nil {
		t.Fatal("没有 route.change")
	}
	if got["from"] != routeHome || got["to"] != routeFollows {
		t.Fatalf("route.change=%v", got)
	}
}

// 用户的清晰度/线路选择是「意图」，session.fetch/open 是「结果」，两者分开。
func TestQualityAndLinePickEvents(t *testing.T) {
	c := captureLogs(t)
	app := NewMockApp()
	app.media.log = newLogRef("player", "douyu|1", "取流#1", time.Now())

	app.PickQuality("原画")
	q := c.find(diag.EvSessionQualityPick)
	if q == nil {
		t.Fatal("没有 session.quality_pick")
	}
	if q["requested"] != "原画" {
		t.Fatalf("quality_pick=%v", q)
	}

	app.pickLine("备线1")
	l := c.find(diag.EvSessionLinePick)
	if l == nil {
		t.Fatal("没有 session.line_pick")
	}
	if l["requested"] != "备线1" {
		t.Fatalf("line_pick=%v", l)
	}
}
