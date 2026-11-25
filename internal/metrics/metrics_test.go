package metrics

import (
	"encoding/json"
	"testing"
)

// fields 造一条「从 JSON 解码出来」的字段表。
//
// 数字用 json.Number 而不是 int：Observe 的入参就是落盘前那次解码的结果，
// 拿 int 去喂它测的是一段生产上不存在的形状。
func fields(kv ...any) map[string]any {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

func TestObserveEvents(t *testing.T) {
	Reset()
	room := "douyu|1"
	Observe(fields("event", "session.first_frame", "room", room, "open_ms", json.Number("1200")))
	Observe(fields("event", "wall.first_frame", "room", room, "open_ms", json.Number("800")))
	Observe(fields("event", "wall.restart", "room", room, "cause", "ended"))
	Observe(fields("event", "wall.fail", "room", room, "stage", "fetch",
		"err", map[string]any{"class": "upstream"}))
	Observe(fields("event", "engine.open"))
	Observe(fields("event", "engine.close"))
	Observe(fields("event", "mpv", "module", "hls", "mpv_level", "error"))

	snap := Snapshot()
	counters := snap["counters"].(map[string]int64)
	for k, want := range map[string]int64{
		"wall_restarts_total":   1,
		"wall_restarts.douyu|1": 1,
		"wall_fail_total":       1,
		"wall_fail_stage.fetch": 1,
		"engine_open_total":     1,
		"engine_close_total":    1,
		"mpv_log_total":         1,
		"mpv_log_level.error":   1,
		"mpv_log_module.hls":    1,
	} {
		if counters[k] != want {
			t.Fatalf("%s=%d want %d; all=%v", k, counters[k], want, counters)
		}
	}
	gauges := snap["gauges"].(map[string]int64)
	if gauges["engines_open"] != 0 {
		t.Fatalf("engines_open=%d", gauges["engines_open"])
	}
	hists := snap["histograms"].(map[string]any)
	ff := hists["first_frame_ms"].(map[string]any)
	if ff["count"].(int64) != 1 || ff["p50"].(int64) < 1000 {
		t.Fatalf("first_frame_ms=%v", ff)
	}
	wf := hists["wall_first_frame_ms"].(map[string]any)
	if wf["count"].(int64) != 1 {
		t.Fatalf("wall_first_frame_ms=%v", wf)
	}
}

// 没有 event 的行不该产生任何计数：普通日志不是事件。
func TestObserveIgnoresPlainLines(t *testing.T) {
	Reset()
	Observe(fields("msg", "普通日志"))
	Observe(nil)
	counters := Snapshot()["counters"].(map[string]int64)
	if len(counters) != 0 {
		t.Fatalf("普通日志进了指标: %v", counters)
	}
}

func TestHistoryQuantiles(t *testing.T) {
	h := newHistogram()
	for _, v := range []int64{100, 200, 300, 9000, 20000} {
		h.observe(v)
	}
	// 分位数按桶上界返回：100/200 落在 250 桶，300/9000/20000 各占一桶。
	if h.quantile(0.5) != 250 {
		t.Fatalf("p50=%d", h.quantile(0.5))
	}
	if h.quantile(0.95) != 16000 {
		t.Fatalf("p95=%d", h.quantile(0.95))
	}
}
