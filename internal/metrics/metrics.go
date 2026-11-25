// Package metrics 从诊断事件里抽出一小组可聚合的数。
//
// 日志回答「为什么」，指标回答「多严重、多频繁」。两者共用同一个事件流，
// 不另开一套埋点：Wrap 在 slog handler 前观察记录，按 event 更新计数/直方图。
// 这样「重连率」「首帧 p95」不需要靠数日志行，也不会和日志口径漂移。
package metrics

import (
	"context"
	"log/slog"
	"maps"
	"sort"
	"sync"
)

// maxSeries 限制按 room 展开的序列数。房间号基数无上限，桌面应用里 200
// 已经远超一屏 12 路 + 历史会话；超过就丢弃新序列，不让 map 无限长。
const maxSeries = 200

type histogram struct {
	bounds []int64
	counts []int64
	count  int64
	sum    int64
}

func newHistogram() *histogram {
	b := []int64{250, 500, 1000, 2000, 4000, 8000, 16000, 32000}
	return &histogram{bounds: b, counts: make([]int64, len(b)+1)}
}

func (h *histogram) observe(v int64) {
	if v < 0 {
		return
	}
	i := sort.Search(len(h.bounds), func(i int) bool { return v <= h.bounds[i] })
	h.counts[i]++
	h.count++
	h.sum += v
}

func (h *histogram) quantile(q float64) int64 {
	if h.count == 0 {
		return 0
	}
	want := max(int64(float64(h.count)*q), 1)
	var seen int64
	for i, c := range h.counts {
		seen += c
		if seen >= want {
			if i < len(h.bounds) {
				return h.bounds[i]
			}
			return h.bounds[len(h.bounds)-1]
		}
	}
	return h.bounds[len(h.bounds)-1]
}

type registry struct {
	mu       sync.Mutex
	counters map[string]int64
	gauges   map[string]int64
	hist     map[string]*histogram
}

var std = newRegistry()

func newRegistry() *registry {
	return &registry{
		counters: map[string]int64{},
		gauges:   map[string]int64{},
		hist: map[string]*histogram{
			"first_frame_ms":      newHistogram(),
			"wall_first_frame_ms": newHistogram(),
		},
	}
}

func (r *registry) counter(key string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.counters[key]; !ok {
		if len(r.counters) >= maxSeries {
			return
		}
	}
	r.counters[key] += delta
}

func (r *registry) gauge(key string, delta int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[key] += delta
}

func (r *registry) observe(name string, v int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if h := r.hist[name]; h != nil {
		h.observe(v)
	}
}

// Snapshot 返回可 JSON 序列化的快照。bundle 直接把它写进 metrics.json。
func Snapshot() map[string]any {
	return std.snapshot()
}

func (r *registry) snapshot() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	counters := make(map[string]int64, len(r.counters))
	maps.Copy(counters, r.counters)
	gauges := make(map[string]int64, len(r.gauges))
	maps.Copy(gauges, r.gauges)
	hists := map[string]any{}
	for name, h := range r.hist {
		hists[name] = map[string]any{
			"count": h.count,
			"sum":   h.sum,
			"p50":   h.quantile(0.50),
			"p95":   h.quantile(0.95),
		}
	}
	return map[string]any{
		"counters":   counters,
		"gauges":     gauges,
		"histograms": hists,
	}
}

// Reset 只给测试用。
func Reset() { std = newRegistry() }

// Wrap 把指标观察接到一个 slog.Handler 前面。
func Wrap(next slog.Handler) slog.Handler {
	return &handler{next: next}
}

type handler struct {
	next  slog.Handler
	attrs []slog.Attr
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *handler) WithAttrs(as []slog.Attr) slog.Handler {
	next := &handler{next: h.next.WithAttrs(as)}
	next.attrs = append(append([]slog.Attr(nil), h.attrs...), as...)
	return next
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{next: h.next.WithGroup(name), attrs: h.attrs}
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var event, room, stage, module, level, class string
	var ms int64
	scan := func(a slog.Attr) bool {
		switch a.Key {
		case "event":
			event = a.Value.String()
		case "room":
			room = a.Value.String()
		case "stage":
			stage = a.Value.String()
		case "module":
			module = a.Value.String()
		case "mpv_level":
			level = a.Value.String()
		case "ms", "open_ms":
			if ms == 0 {
				ms = a.Value.Int64()
			}
		case "err":
			if a.Value.Kind() == slog.KindGroup {
				for _, g := range a.Value.Group() {
					if g.Key == "class" {
						class = g.Value.String()
					}
				}
			}
		}
		return true
	}
	for _, a := range h.attrs {
		scan(a)
	}
	r.Attrs(scan)
	if event == "" {
		return h.next.Handle(ctx, r)
	}

	switch event {
	case "session.first_frame":
		std.observe("first_frame_ms", ms)
	case "wall.first_frame":
		std.observe("wall_first_frame_ms", ms)
	case "wall.open":
		std.counter("wall_open_total", 1)
		if room != "" {
			std.counter("wall_open."+room, 1)
		}
	case "wall.ready":
		std.counter("wall_ready_total", 1)
	case "wall.restart":
		std.counter("wall_restarts_total", 1)
		if room != "" {
			std.counter("wall_restarts."+room, 1)
		}
	case "wall.fail":
		std.counter("wall_fail_total", 1)
		if stage != "" {
			std.counter("wall_fail_stage."+stage, 1)
		}
	case "wall.offline":
		std.counter("wall_offline_total", 1)
	case "wall.close":
		std.counter("wall_close_total", 1)
	case "engine.open":
		std.counter("engine_open_total", 1)
		std.gauge("engines_open", 1)
	case "engine.open_fail":
		std.counter("engine_open_fail_total", 1)
		if stage != "" {
			std.counter("engine_open_fail_stage."+stage, 1)
		}
	case "engine.close":
		std.counter("engine_close_total", 1)
		std.gauge("engines_open", -1)
	case "engine.gpu_fallback":
		std.counter("engine_gpu_fallback_total", 1)
	case "session.fail", "session.fetch_fail", "session.open_fail":
		std.counter("session_fail_total", 1)
		if class != "" {
			std.counter("session_fail_class."+class, 1)
		}
	case "session.audio_stall":
		std.counter("audio_stall_total", 1)
	case "session.video_stall":
		std.counter("video_stall_total", 1)
	case "danmaku.connect":
		std.counter("danmaku_connect_total", 1)
	case "danmaku.fail":
		std.counter("danmaku_fail_total", 1)
		if class != "" {
			std.counter("danmaku_fail_class."+class, 1)
		}
	case "mpv":
		std.counter("mpv_log_total", 1)
		if level != "" {
			std.counter("mpv_log_level."+level, 1)
		}
		if module != "" {
			std.counter("mpv_log_module."+module, 1)
		}
	case "state.load_fail":
		std.counter("state_load_fail_total", 1)
	case "state.save_fail":
		std.counter("state_save_fail_total", 1)
	case "log.dropped":
		std.counter("log_dropped_total", 1)
	}
	return h.next.Handle(ctx, r)
}
