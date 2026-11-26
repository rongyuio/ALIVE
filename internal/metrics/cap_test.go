package metrics

import (
	"strconv"
	"testing"
)

// 固定名的汇总指标不能被房间序列挤掉。maxSeries 只该约束带 '.' 的序列键。
func TestCounterCapOnlySeries(t *testing.T) {
	Reset()
	r := std.Load()
	for i := 0; i < maxSeries+50; i++ {
		r.counter("wall_open.room-"+strconv.Itoa(i), 1)
	}
	r.counter("wall_offline_total", 1)
	c := Snapshot()["counters"].(map[string]int64)
	if c["wall_offline_total"] != 1 {
		t.Fatalf("固定名指标被序列挤掉了: wall_offline_total=%d", c["wall_offline_total"])
	}
}

// 溢出桶要报真实最大值，而不是「等于上界」。一个卡 60 秒的首帧曾经报成 32000。
func TestHistogramOverflowReportsMax(t *testing.T) {
	h := newHistogram()
	h.observe(60000)
	if got := h.quantile(0.95); got != 60000 {
		t.Fatalf("p95=%d want 60000", got)
	}
	if got := h.quantile(0.50); got != 60000 {
		t.Fatalf("p50=%d want 60000", got)
	}
}
