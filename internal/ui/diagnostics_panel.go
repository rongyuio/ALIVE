package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/oneo-me/onevra/source/components"

	"alive/internal/applog"
	"alive/internal/buildinfo"
	"alive/internal/metrics"
	"alive/internal/playback"
	"alive/internal/support"
	"alive/internal/theme"
)

// 性能剖析的采样窗口。「30 秒」这个数字同时写进按钮文案，两处必须同源。
const profileWindow = 30 * time.Second

// diagFirstFrameSlowMS 是首帧 p95 标红的门槛。
//
// 实测得来的：同一台机器上 B 站 545068 走正常路径
// 首帧 1666ms，而改造前被判为「慢」的那条路是 3648ms。3000ms 落在这两点之间：
// 正常路径够不着，坏路径一定跨过去。
const diagFirstFrameSlowMS int64 = 3000

// diagLabelW 是指标行左列宽度：「mpv 日志」是最长的标签，留出余量后右值列
// 才能在整张卡里对齐。以前用空格凑对齐，换字体就散。
const diagLabelW float32 = 76

// diagStat 是一行指标：左标签、右值。
//
// bad 只给「有东西真的坏了」的行：首帧过慢、日志丢弃、打开失败。计数非零
// 不等于坏——墙重连、引擎开过又关都是正常运行的一部分。
type diagStat struct {
	label string
	value string
	bad   bool
}

// diagGroup 是一组指标。分组让读者知道横向在比什么：播放延迟、观测墙、
// 运行时各自成段。
type diagGroup struct {
	title string
	rows  []diagStat
}

// syncDiagBody 是「诊断」页签：本次运行的指标、导出证据包、采一段 CPU 剖析。
//
// 它不读 state.json、不读登录态；只打包日志、最近事件、崩溃/卡死现场、
// 环境指纹和指标。顺序是「状态 → 动作 → 结果」：用户先看清现在什么样，
// 再决定导不导，导出后的产物就在正下方，带复制路径和定位。
func (a *App) syncDiagBody(c *ui.Context) {
	ui.Column(c).Padding(theme.Space3, theme.Space4, theme.Space4, theme.Space4).
		Gap(theme.Space3).Children(func() {
		a.syncHint(c, "导出日志、最近事件、崩溃/卡死现场、环境指纹和指标。不包含登录态和 state.json。")

		a.updateLine(c)

		a.diagCard(c)

		ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
			if !a.DiagEmpty {
				a.diagTextAction(c, "复制摘要", func() {
					mygo.Clipboard.WriteText(a.diagText())
					a.toastTone("诊断摘要已复制", toastSuccess)
				})
			}
			ui.Box(c).Grow(1)
			a.syncButton(c, syncActProfile, iconActivity, "30 秒性能剖析", false, a.profileCPU, a.stopProfile)
			a.syncButton(c, syncActExport, iconUpload, "导出诊断包", true, a.exportDiagnostics, nil)
		})

		a.diagResult(c)
	})
}

// diagCard 画指标卡：标题行 + 分组 + 行。
//
// 全零时整块压成一句话。冷启动的截图原来是一屏「—」和「0」，看起来像坏了；
// 那五行只是还没开始播。
func (a *App) diagCard(c *ui.Context) {
	ui.Column(c).Gap(theme.Space2).Padding(8, 12, 10, 12).Radius(theme.RadiusMD).
		Background(theme.FillQuaternary).Children(func() {
		ui.Row(c).Height(20).AlignItems(ui.Center).Children(func() {
			components.Text(c, "本次运行").FontSize(theme.Text2XS).FontWeight(600).
				TextColor(theme.TextMeta).Grow(1)
			a.diagTextAction(c, "刷新指标", func() {
				a.DiagGroups, a.DiagEmpty = diagnosticGroups()
			})
		})

		if a.DiagEmpty {
			components.Text(c, "还没有播放记录。导出包里仍然有环境指纹、日志和指标原始值。").
				FontSize(theme.TextXS).TextColor(theme.TextMeta)
			return
		}

		for gi, g := range a.DiagGroups {
			if gi > 0 {
				ui.Box(c).Height(theme.Space1)
			}
			// 组标题比行标签更深、更重：不然「播放页」读起来像又一行指标。
			components.Text(c, g.title).FontSize(theme.Text2XS).FontWeight(600).
				TextColor(theme.TextSecondary)
			for _, row := range g.rows {
				ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
					components.Text(c, row.label).FontSize(theme.TextXS).TextColor(theme.TextMeta).
						Width(diagLabelW).SingleLine()
					col := theme.TextSecondary
					if row.bad {
						col = theme.DangerInk
					}
					components.Text(c, row.value).FontSize(theme.TextXS).TextColor(col).
						Grow(1).TextAlign(ui.End).SingleLine()
				})
			}
		}
	})
}

// diagTextAction 是卡内的次级文字动作（刷新指标、复制摘要）。
//
// 自绘控件三件套：Label 跟屏幕文字一致、指针光标、Clicked 写在链上。
func (a *App) diagTextAction(c *ui.Context, label string, on func()) {
	btn := ui.Box(c).Height(20).Padding(0, 4, 0, 4).Radius(theme.RadiusXS).
		Center().Cursor(ui.CursorPointer).Label(label)
	col := hoverInk(btn, "diag-text-ink", btn.Hovered(), theme.TextMeta, theme.TextPrimary)
	btn.Children(func() {
		components.Text(c, label).FontSize(theme.Text2XS).FontWeight(600).TextColor(col)
	})
	if btn.Clicked() {
		on()
	}
}

// diagIconAction 是结果条右侧的圆形图标动作（复制路径、打开所在文件夹）。
func (a *App) diagIconAction(c *ui.Context, ico *ui.SVG, label string, on func()) {
	btn := ui.Box(c).Size(24, 24).Radius(theme.RadiusFull).Center().
		Cursor(ui.CursorPointer).Label(label)
	col := hoverInk(btn, "diag-act-ink", btn.Hovered(), theme.AccentInk, theme.TextPrimary)
	btn.Background(hoverSurface(btn, "diag-act-bg", btn.Hovered(), theme.AccentSoft))
	if btn.Clicked() {
		on()
	}
	btn.Children(func() {
		components.Icon(c, ico).FontSize(14).TextColor(col)
	})
}

// diagResult 画上一次动作的结果。
//
// 三种状态共用一块固定高度的位置：空 / 进行中 / 已写出。高度一变，居中的
// 模态整体挪，用户刚点过的按钮就跑了——剖析一开始清空上一条提示，按钮会
// 上跳二十几像素，手指还没离开。
//
// 和导入页的 syncFeedback 分开：诊断的产物是文件，结果条要让用户下一步就能
// 拿到它——复制完整路径、在资源管理器里选中——而不是自己去日志目录翻。
func (a *App) diagResult(c *ui.Context) {
	row := ui.Row(c).Gap(8).AlignItems(ui.Center)
	// 只有「已写出」是一条带底色的结果条；空、进行中、错误都不铺底。
	notice := a.SyncError == "" && !a.SyncBusy && a.SyncNotice != ""
	if notice {
		row.Height(28).Padding(0, 10, 0, 6).Radius(theme.RadiusMD).
			Background(theme.AccentSofter).Border(1, theme.AccentSoft)
	} else if a.SyncError == "" {
		row.Height(28)
	}
	row.Children(func() {
		switch {
		case a.SyncError != "":
			components.Icon(c, iconAlert).FontSize(14).TextColor(theme.DangerInk)
			components.Text(c, a.SyncError).FontSize(theme.TextXS).TextColor(theme.DangerInk).Grow(1)

		case a.SyncBusy && a.SyncBusyAction != syncActImport:
			// 进行中也占住这一行：用户点完就知道在等什么、等多久。
			label := "正在打包诊断包…"
			if a.SyncBusyAction == syncActProfile {
				label = "正在剖析 30 秒样本…"
			}
			components.Icon(c, iconSync).FontSize(14).TextColor(theme.TextTertiary)
			components.Text(c, label).FontSize(theme.TextXS).TextColor(theme.TextTertiary).Grow(1)

		case a.SyncNotice != "":
			components.Icon(c, iconOkCircle).FontSize(15).TextColor(theme.AccentInk)
			components.Text(c, a.SyncNotice).FontSize(theme.TextXS).TextColor(theme.TextSecondary).
				Grow(1).SingleLine().TruncateMiddle()
			if a.SyncPath != "" {
				a.diagIconAction(c, iconCopy, "复制文件路径", func() {
					mygo.Clipboard.WriteText(a.SyncPath)
					a.toastTone("已复制文件路径", toastSuccess)
				})
				a.diagIconAction(c, iconFolderOpen, "打开所在文件夹", func() {
					if err := support.Reveal(a.SyncPath); err != nil {
						a.toastTone(err.Error(), toastError)
					}
				})
			}
		}
	})
}

// diagnosticGroups 把指标压成给人看的分组。只在切页签 / 动作完成 / 点刷新时
// 调用，不在每帧构建路径里分配。
func diagnosticGroups() (groups []diagGroup, empty bool) {
	snap := metrics.Snapshot()
	// 全部用 comma-ok：Snapshot 返回的是 map[string]any，形状一变（改名、
	// 改成按需注册直方图、被替换实现）这里就直接 panic，而它在构建路径上，
	// 整窗会挂掉。拿不到就当成空指标，面板显示"还没有数据"。
	counters, _ := snap["counters"].(map[string]int64)
	gauges, _ := snap["gauges"].(map[string]int64)
	hists, _ := snap["histograms"].(map[string]any)
	ff, _ := hists["first_frame_ms"].(map[string]any)
	wf, _ := hists["wall_first_frame_ms"].(map[string]any)

	// latency 把一路首帧直方图拼成「p50 · p95 · 次数」。bad 只看 p95：
	// 中位数正常、尾部很慢，正是用户能感觉到卡的那种分布。
	//
	// 分位数前面写 ≤：metrics.quantile 返回的是桶上界，真实样本只会更小，
	// 写成「2.0s」会让人以为是精确值。
	latency := func(h map[string]any) (value string, bad bool, count int64) {
		count, _ = h["count"].(int64)
		if count == 0 {
			return "—", false, 0
		}
		p50, _ := h["p50"].(int64)
		p95, _ := h["p95"].(int64)
		return fmt.Sprintf("p50 ≤%s · p95 ≤%s · %s 次",
			fmtLatency(p50), fmtLatency(p95), fmtCount(count)), p95 > diagFirstFrameSlowMS, count
	}

	playFF, playBad, playCount := latency(ff)
	wallFF, wallBad, wallCount := latency(wf)

	restarts := counters["wall_restarts_total"]
	fails := counters["wall_fail_total"]
	offline := counters["wall_offline_total"]
	openFail := counters["engine_open_fail_total"]
	dropped := counters["log_dropped_total"]

	groups = []diagGroup{
		{"播放页", []diagStat{
			{"首帧", playFF, playBad},
		}},
		{"观测墙", []diagStat{
			{"首帧", wallFF, wallBad},
			{"重连", fmt.Sprintf("%s 次 · 失败 %s · 未开播 %s",
				fmtCount(restarts), fmtCount(fails), fmtCount(offline)), false},
		}},
		{"运行时", []diagStat{
			{"引擎", fmt.Sprintf("存活 %s · 累计开 %s · 关 %s",
				fmtCount(gauges["engines_open"]), fmtCount(counters["engine_open_total"]),
				fmtCount(counters["engine_close_total"])), false},
			{"打开失败", fmtCount(openFail), openFail > 0},
			{"mpv 日志", fmt.Sprintf("%s 条", fmtCount(counters["mpv_log_total"])), false},
			{"日志丢弃", fmtCount(dropped), dropped > 0},
		}},
	}

	// 空态看「有没有播过 / 引擎有没有起来」：没有这些，下面的重连、失败、
	// 日志全是 0，摆出来只会让人以为播放出了问题。日志丢弃是例外——它说明
	// 证据本身少了，哪怕没播过也要摆出来。
	empty = playCount == 0 && wallCount == 0 &&
		counters["engine_open_total"] == 0 && counters["mpv_log_total"] == 0 && dropped == 0
	return groups, empty
}

// diagText 是「复制摘要」写进剪贴板的文本。支持场景里这比截图好用：
// 用户可以改，也可以贴进对话。
func (a *App) diagText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "ALIVE %s · run %s · %s · %s/%s\n",
		diagVersion(), applog.RunID(), time.Now().Format("2006-01-02 15:04"),
		runtime.GOOS, runtime.GOARCH)
	if a.DiagEmpty {
		b.WriteString("本次运行还没有播放记录")
		return b.String()
	}
	for _, g := range a.DiagGroups {
		for _, row := range g.rows {
			fmt.Fprintf(&b, "%s %s：%s\n", g.title, row.label, row.value)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// fmtLatency 把直方图里的毫秒写成给人看的时间。
//
// metrics.quantile 返回的是桶上界（250 / 500 / 1000 …），所以这串数天然是
// 量级而不是精确值。短于 1s 保持毫秒：首帧的判断吃 100ms 级的差别，
// 一刀切成 %.1fs 会把 80ms 和 140ms 都写成 0.1s。
func fmtLatency(ms int64) string {
	if ms <= 0 {
		return "—"
	}
	if ms < 1000 {
		return strconv.FormatInt(ms, 10) + "ms"
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}

// fmtCount 给计数加千分位。这些数会被念出来、贴出去，1204 不如 1,204 好读。
func fmtCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// diagVersion 取当前构建的版本号。空版本（本地 go run）写成 unknown，
// 免得摘要里出现一串空格。
func diagVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "unknown"
}

// appVersionText 是屏幕上显示的版本。空版本说「本地构建」而不是留白：
// go run 出来的二进制没有版本号，写 unknown 会让人以为版本信息坏了。
//
// 和 diagVersion 分两个函数：那个的值进导出的诊断包，脚本要能解析，所以用
// 一个稳定的英文词；这里给人看。
func appVersionText() string {
	if v := mygo.App.Version(); v != "" {
		return "ALIVE " + v
	}
	return "ALIVE 本地构建"
}

// updateLine 是诊断页上的版本行 + 手动「检查更新」。
//
// 更新本身归 mygo 的更新插件：它在后台按天自己查一次，有新版本自己弹窗。
// 这一行只是让用户能主动问一次，顺带告诉他现在跑的是哪一版——诊断页是
// 顶栏那唯一一个应用级入口，版本属于这里。
func (a *App) updateLine(c *ui.Context) {
	ui.Row(c).Gap(theme.Space2).AlignItems(ui.Center).Children(func() {
		components.Text(c, "版本").FontSize(theme.Text2XS).FontWeight(600).
			TextColor(theme.TextMeta).Width(diagLabelW).SingleLine()
		components.Text(c, appVersionText()).FontSize(theme.TextXS).
			TextColor(theme.TextSecondary).Grow(1).SingleLine()
		if a.CheckUpdates != nil {
			a.diagTextAction(c, "检查更新", a.CheckUpdates)
		}
	})
}

// diagnosticMeta 是 bundle 里 meta.json 的内容。只放身份和计数，不放内容。
func (a *App) diagnosticMeta() map[string]any {
	commit, modified := buildinfo.Commit()
	meta := map[string]any{
		"run":       applog.RunID(),
		"version":   diagVersion(),
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
	// 没有窗口就没有回主线程的入口：runOnMain / Update 会丢掉回调，
	// SyncBusy 就永久立着，之后所有同步按钮都点不动（连"停止剖析"都进
	// 不去）。宁可不开始，也不要开一个收不了尾的动作。
	if win == nil {
		return
	}
	a.SyncBusy = true
	a.SyncBusyAction = syncActExport
	a.SyncNotice = ""
	a.SyncPath = ""
	a.SyncError = ""
	go func() {
		_, err := support.Bundle(support.Options{Out: out, LogDir: dir, Meta: meta})
		if win == nil {
			return
		}
		win.Update(func() {
			a.SyncBusy = false
			a.SyncBusyAction = ""
			if err != nil {
				a.SyncError = "导出诊断包失败: " + err.Error()
				return
			}
			a.DiagGroups, a.DiagEmpty = diagnosticGroups()
			a.SyncPath = out
			a.SyncNotice = "诊断包已导出：" + filepath.Base(out)
		})
	}()
}

// profileCPU 采一段 CPU profile，写到日志目录。
//
// 采样跑在 goroutine 里，取消入口留在 ProfCancel 上：30 秒是一段很长的
// 等待，用户点错了或者只是想看个大概，必须能中途停。
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
	// 同 exportBundle：没有窗口就没有收尾的入口，SyncBusy 会永久立着。
	if win == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.SyncBusy = true
	a.SyncBusyAction = syncActProfile
	a.ProfStarted = time.Now()
	a.ProfCancel = cancel
	a.SyncNotice = ""
	a.SyncPath = ""
	a.SyncError = ""
	go func() {
		took, err := support.CPUProfile(ctx, out, profileWindow)
		cancel() // 释放 ctx，别让计时器留到窗口结束
		if win == nil {
			return
		}
		win.Update(func() {
			a.SyncBusy = false
			a.SyncBusyAction = ""
			a.ProfStarted = time.Time{}
			a.ProfCancel = nil
			if err != nil && !errors.Is(err, support.ErrProfileCanceled) {
				a.SyncError = "性能剖析失败: " + err.Error()
				return
			}
			a.DiagGroups, a.DiagEmpty = diagnosticGroups()
			a.SyncPath = out
			if errors.Is(err, support.ErrProfileCanceled) {
				// 停下来的文件仍然可读：说清楚采了多久，别让用户以为是废文件。
				a.SyncNotice = fmt.Sprintf("性能剖析已停止：%s（采样 %s）",
					filepath.Base(out), fmtLatency(took.Milliseconds()))
				return
			}
			a.SyncNotice = "性能剖析已写出：" + filepath.Base(out) + "（和诊断包在同一目录）"
		})
	}()
}

// stopProfile 是剖析中的「停止剖析」按钮：只负责叫停，收尾仍在采样
// goroutine 里做，按钮状态由它统一恢复。重复点没有副作用。
func (a *App) stopProfile() {
	if a.ProfCancel == nil {
		return
	}
	a.ProfCancel()
	a.ProfCancel = nil
}
