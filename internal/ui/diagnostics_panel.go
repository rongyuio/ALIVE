package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/applog"
	"alive/internal/buildinfo"
	"alive/internal/metrics"
	"alive/internal/playback"
	"alive/internal/support"
	"alive/internal/theme"
)

// syncDiagBody 是「诊断」页签：导出证据包、采一段性能剖析。
//
// 它不读 state.json、不读登录态；只打包日志、最近事件、崩溃/卡死现场、
// 环境指纹和指标。导出完成后的路径在结果提示里。
func (a *App) syncDiagBody(c *ui.Context) {
	ui.Column(c).Padding(theme.Space3, theme.Space4, theme.Space4, theme.Space4).
		Gap(theme.Space3).Children(func() {
		a.syncHint(c, "导出日志、最近事件、崩溃/卡死现场、环境指纹和指标。不包含登录态和 state.json。")

		ui.Row(c).Gap(8).Children(func() {
			a.syncButton(c, iconUpload, "导出诊断包", true, a.exportDiagnostics)
			a.syncButton(c, iconActivity, "30 秒性能剖析", false, a.profileCPU)
		})

		a.syncFeedback(c)

		if len(a.DiagSummary) > 0 {
			ui.Column(c).Gap(2).Padding(8, 10, 8, 10).Radius(theme.RadiusMD).
				Background(theme.FillQuaternary).Children(func() {
				for _, line := range a.DiagSummary {
					ui.Text(c, line).FontSize(theme.Text2XS).TextColor(theme.TextSecondary)
				}
			})
		}
	})
}

// diagnosticSummary 把指标压成几行给人看。只在切到诊断页签/动作完成时算一次，
// 不在每帧构建路径里分配。
func diagnosticSummary() []string {
	snap := metrics.Snapshot()
	counters := snap["counters"].(map[string]int64)
	gauges := snap["gauges"].(map[string]int64)
	hists := snap["histograms"].(map[string]any)
	ff := hists["first_frame_ms"].(map[string]any)
	wf := hists["wall_first_frame_ms"].(map[string]any)
	ms := func(v any) string {
		n, _ := v.(int64)
		if n == 0 {
			return "—"
		}
		return fmt.Sprintf("%.1fs", float64(n)/1000)
	}
	return []string{
		fmt.Sprintf("播放页首帧  p50 %s · p95 %s（%d 次）", ms(ff["p50"]), ms(ff["p95"]), ff["count"]),
		fmt.Sprintf("墙格首帧    p50 %s · p95 %s（%d 次）", ms(wf["p50"]), ms(wf["p95"]), wf["count"]),
		fmt.Sprintf("墙重连 %d · 失败 %d · 未开播 %d", counters["wall_restarts_total"], counters["wall_fail_total"], counters["wall_offline_total"]),
		fmt.Sprintf("引擎当前 %d · 开 %d / 关 %d", gauges["engines_open"], counters["engine_open_total"], counters["engine_close_total"]),
		fmt.Sprintf("mpv 日志 %d · 日志丢弃 %d", counters["mpv_log_total"], counters["log_dropped_total"]),
	}
}

// diagnosticMeta 是 bundle 里 meta.json 的内容。只放身份和计数，不放内容。
func (a *App) diagnosticMeta() map[string]any {
	version := mygo.App.Version()
	if version == "" {
		version = "unknown"
	}
	commit, modified := buildinfo.Commit()
	meta := map[string]any{
		"run":       applog.RunID(),
		"version":   version,
		"commit":    commit,
		"modified":  modified,
		"go":        runtime.Version(),
		"os":        runtime.GOOS + "/" + runtime.GOARCH,
		"pid":       os.Getpid(),
		"log":       applog.Path(),
		"level":     applog.Level().String(),
		"engines":   playback.LiveEngines(),
		"generated": time.Now().Format(time.RFC3339),
	}
	if a.focus != nil {
		meta["focus"] = a.focus.key
	}
	wall := make([]string, 0, len(a.WatchRooms))
	for _, r := range a.WatchRooms {
		wall = append(wall, watchKey(r))
	}
	meta["wall"] = wall
	return meta
}

// exportDiagnostics 把诊断包写到日志目录，不弹系统保存框。
//
// 支持场景里最重要的是「点一下、把提示里的路径发过来」；让用户选目录只会
// 多一步。文件不大，放在日志旁边也方便再次导出时覆盖/区分。
func (a *App) exportDiagnostics() {
	if a.SyncBusy {
		return
	}
	dir := applog.Dir()
	if dir == "" {
		a.SyncError = "日志目录不可用，无法导出诊断包"
		return
	}
	out := filepath.Join(dir, "bundle-"+time.Now().Format("20060102-150405")+".zip")
	meta := a.diagnosticMeta()
	win := a.Win
	a.SyncBusy = true
	a.SyncNotice = ""
	a.SyncError = ""
	go func() {
		_, err := support.Bundle(support.Options{Out: out, LogDir: dir, Meta: meta})
		if win == nil {
			return
		}
		win.Update(func() {
			a.SyncBusy = false
			if err != nil {
				a.SyncError = "导出诊断包失败: " + err.Error()
				return
			}
			a.DiagSummary = diagnosticSummary()
			a.SyncNotice = "诊断包已导出: " + out
		})
	}()
}

// profileCPU 采 30 秒 CPU profile，写到日志目录。
func (a *App) profileCPU() {
	if a.SyncBusy {
		return
	}
	dir := applog.Dir()
	if dir == "" {
		a.SyncError = "日志目录不可用，无法写性能剖析"
		return
	}
	out := filepath.Join(dir, "cpu-"+time.Now().Format("20060102-150405")+".pprof")
	win := a.Win
	a.SyncBusy = true
	a.SyncNotice = ""
	a.SyncError = ""
	go func() {
		err := support.CPUProfile(out, 30*time.Second)
		if win == nil {
			return
		}
		win.Update(func() {
			a.SyncBusy = false
			if err != nil {
				a.SyncError = "性能剖析失败: " + err.Error()
				return
			}
			a.DiagSummary = diagnosticSummary()
			a.SyncNotice = "性能剖析已写出: " + out
		})
	}()
}
