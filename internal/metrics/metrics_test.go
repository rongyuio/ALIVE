package metrics

import (
	"log/slog"
	"strings"
	"testing"
)

func TestObserveEvents(t *testing.T) {
	Reset()
	var buf strings.Builder
	log := slog.New(Wrap(slog.NewTextHandler(&buf, nil))).With("room", "douyu|1")
	log.Info("首帧", "event", "session.first_frame", "open_ms", int64(1200))
	log.Info("墙首帧", "event", "wall.first_frame", "open_ms", int64(800))
	log.Warn("重连", "event", "wall.restart", "cause", "ended")
	log.Warn("失败", "event", "wall.fail", "stage", "fetch",
		slog.Group("err", slog.String("class", "upstream")))
	log.Info("引擎开", "event", "engine.open")
	log.Info("引擎关", "event", "engine.close")
	log.Warn("mpv", "event", "mpv", "module", "hls", "mpv_level", "error")

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
