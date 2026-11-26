// Package metrics 从诊断事件里抽出一小组可聚合的数。
//
// 日志回答「为什么」，指标回答「多严重、多频繁」。两者共用同一个事件流，
// 不另开一套埋点：applog 在落盘前解析那一行字符串，Observe 顺手读走
// event/room/stage，按 event 更新计数/直方图。这样「重连率」「首帧 p95」
// 不需要靠数日志行，也不会和日志口径漂移。
package metrics

import (
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"alive/internal/diag"
)

// maxSeries 限制按 room 展开的序列数。房间号基数无上限，桌面应用里 200
// 已经远超一屏 12 路 + 历史会话；超过就丢弃新序列，不让 map 无限长。
const maxSeries = 200

type histogram struct {
	bounds []int64
	counts []int64
	count  int64
	sum    int64
	max    int64
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
	if v > h.max {
		h.max = v
	}
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
			// 溢出桶：报真实最大值，而不是「等于上界」。以前一个卡 60 秒的
			// 首帧会被报成 32000，排障时读成 32 秒，和现场差一倍。
			if h.max > 0 {
				return h.max
			}
			return h.bounds[len(h.bounds)-1]
		}
	}
	if h.max > 0 {
		return h.max
	}
	return h.bounds[len(h.bounds)-1]
}

type registry struct {
	mu       sync.Mutex
	counters map[string]int64
	gauges   map[string]int64
	hist     map[string]*histogram
}

// std 用原子指针而不是裸变量：Observe 在写日志的 goroutine 上跑，Reset 在
// 测试线程上跑，裸指针一换就是数据竞争。
var std atomic.Pointer[registry]

func init() { std.Store(newRegistry()) }

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
		// 只有按实体展开的序列（键里带 '.'，如 wall_open.<room>、mpv_log_module.<mod>）
		// 才受 maxSeries 约束。固定名的汇总指标（wall_open_total 这类）是代码
		// 常量、数量有界，绝不能被房间序列挤掉——那会让「少了一个分类指标」
		// 这种事儿悄无声息地发生。
		if strings.Contains(key, ".") && len(r.counters) >= maxSeries {
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

// Snapshot 返回可 JSON 序列化的快照。bundle 把它写进 meta.json 的 metrics 键。
func Snapshot() map[string]any {
	return std.Load().snapshot()
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
			"max":   h.max,
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
func Reset() { std.Store(newRegistry()) }

// Observe 从一条已经序列化的事件里更新计数。drop 为真表示这行日志被丢弃了。
//
// 以前它是 slog.Handler 包装：在 handler 前观察记录。换成 zerolog 之后没有
// 这条路——它的 Hook 拿到的 *Event 只能追加字段，读不回来。于是观察点挪到
// 落盘前：那里本来就要把这一行解析成 JSON 做脱敏，顺手读走 event/room/stage
// 不额外多一次解析。
//
// fields 是解码后的顶层字段。嵌套对象（err）单独取 class。
func Observe(fields map[string]any) {
	r := std.Load()
	event, _ := fields[diag.Event].(string)
	if event == "" {
		return
	}
	room, _ := fields[diag.Room].(string)
	stage, _ := fields[diag.Stage].(string)
	module, _ := fields[diag.Module].(string)
	level, _ := fields["mpv_level"].(string)
	var class string
	if g, ok := fields[diag.ErrField].(map[string]any); ok {
		class, _ = g[diag.ErrClassField].(string)
	}
	ms := num(fields[diag.MS])
	if ms == 0 {
		ms = num(fields[diag.OpenMS])
	}

	switch event {
	case "session.first_frame":
		if ms > 0 {
			r.observe("first_frame_ms", ms)
		}
	case "wall.first_frame":
		if ms > 0 {
			r.observe("wall_first_frame_ms", ms)
		}
	case "wall.open":
		r.counter("wall_open_total", 1)
		if room != "" {
			r.counter("wall_open."+room, 1)
		}
	case "wall.ready":
		r.counter("wall_ready_total", 1)
	case "wall.restart":
		r.counter("wall_restarts_total", 1)
		if room != "" {
			r.counter("wall_restarts."+room, 1)
		}
	case "wall.fail":
		r.counter("wall_fail_total", 1)
		if stage != "" {
			r.counter("wall_fail_stage."+stage, 1)
		}
	case "wall.offline":
		r.counter("wall_offline_total", 1)
	case "wall.close":
		r.counter("wall_close_total", 1)
	case "engine.open":
		r.counter("engine_open_total", 1)
		r.gauge("engines_open", 1)
	case "engine.open_fail":
		r.counter("engine_open_fail_total", 1)
		if stage != "" {
			r.counter("engine_open_fail_stage."+stage, 1)
		}
	case "engine.close":
		r.counter("engine_close_total", 1)
		r.gauge("engines_open", -1)
	case "engine.gpu_fallback":
		r.counter("engine_gpu_fallback_total", 1)
	case "session.fail", "session.fetch_fail", "session.open_fail":
		r.counter("session_fail_total", 1)
		if class != "" {
			r.counter("session_fail_class."+class, 1)
		}
	case "session.audio_stall":
		r.counter("audio_stall_total", 1)
	case "session.video_stall":
		r.counter("video_stall_total", 1)
	case "danmaku.connect":
		r.counter("danmaku_connect_total", 1)
	case "danmaku.fail":
		r.counter("danmaku_fail_total", 1)
		if class != "" {
			r.counter("danmaku_fail_class."+class, 1)
		}
	case "mpv":
		r.counter("mpv_log_total", 1)
		if level != "" {
			r.counter("mpv_log_level."+level, 1)
		}
		if module != "" {
			r.counter("mpv_log_module."+module, 1)
		}
	case "state.load_fail":
		r.counter("state_load_fail_total", 1)
	case "state.save_fail":
		r.counter("state_save_fail_total", 1)
	case "log.dropped":
		r.counter("log_dropped_total", 1)
	}
}

// num 把 JSON 解出来的数字转成 int64。UseNumber 之后是 json.Number。
func num(v any) int64 {
	switch t := v.(type) {
	case json.Number:
		n, _ := t.Int64()
		return n
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	}
	return 0
}
