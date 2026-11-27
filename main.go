// alive — MyGo 原生直播客户端。
//
// 窗口由 MyGo 自己用 GPU 画（Windows 上是 Direct3D 11），没有 webview，
// 所以没有 Chromium 的 gpu-process / renderer 那一套进程。
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/trace"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
	"github.com/rs/zerolog"

	"alive/internal/applog"
	"alive/internal/buildinfo"
	"alive/internal/diag"
	"alive/internal/hang"
	"alive/internal/media"
	appui "alive/internal/ui"

	// 目录里只有打包用的 mygo.* 文案，但导入它才会把应用的语言
	// （zh-Hans）注册给 MyGo 和各个系统的本地化。
	_ "alive/locales"
)

func main() {
	started := time.Now()

	// 日志落盘：%LOCALAPPDATA%\alive\logs\alive-<run>.log。
	//
	// 必须排在所有 slog 调用之前。双击 exe 的进程没有 stderr，不落盘就等于
	// 不留日志——之前排「直播卡」时正因为这一点，拿不到 mpv 自己说的话。
	//
	// Setup 失败不能静默：退到 %TEMP% 留一份错误，并继续用 stderr。日志本身
	// 坏掉时，至少要能知道它坏在哪。
	logPath, logErr := applog.Setup()
	if logErr != nil {
		// 日志落盘失败时退到 stderr：双击启动的进程看不见它，但命令行启动
		// 和 go run 时这一行就是唯一的出路——不能因为文件建不出来而全静默。
		//
		// Timestamp 不能省：这一行是「为什么没有日志」的唯一线索，没有时间
		// 就分不清是哪次启动。原来 slog 的 TextHandler 默认带 time=。
		stderrLog := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			Level(diag.LevelInfo).
			With().Timestamp().Logger()
		diag.SetLogger(&stderrLog)
		fallbackLogError(logErr)
		diag.L().Error().Dict(diag.ErrField, diag.Err(logErr)).Msg("日志落盘失败")
	}

	// 崩溃时打印所有 goroutine 的栈；配合 applog 的 crash-<run>.log。
	debug.SetTraceback("all")

	// 按需执行跟踪：ALIVE_DIAG_TRACE=1 时把整段运行写进 trace-<run>.out，
	// 用 `go tool trace` 看 goroutine/GC/阻塞。默认关闭，不增加日常开销。
	var traceFile *os.File
	if os.Getenv("ALIVE_DIAG_TRACE") == "1" && applog.Dir() != "" {
		p := filepath.Join(applog.Dir(), "trace-"+applog.RunID()+".out")
		if f, err := os.Create(p); err == nil {
			if err := trace.Start(f); err == nil {
				traceFile = f
				diag.L().Info().Str(diag.Event, diag.EvTraceStart).Str(diag.Path, p).Msg("执行跟踪已开始")
			} else {
				_ = f.Close()
				diag.L().Warn().Dict(diag.ErrField, diag.Err(err)).Msg("执行跟踪启动失败")
			}
		}
	}

	commit, modified := buildinfo.Commit()
	diag.L().Info().
		Str(diag.Event, diag.EvRunStart).
		Int("pid", os.Getpid()).
		Str("version", appVersion()).
		Str("commit", commit).
		Bool("modified", modified).
		Str("go", runtime.Version()).
		Str("os", runtime.GOOS+"/"+runtime.GOARCH).
		Str("log", logPath).
		Str("level", applog.Level().String()).
		Str("mpv_log", envOr("ALIVE_MPV_LOG_LEVEL", "warn")).
		Str("frame_stats", envOr("MYGO_FRAME_STATS", "")).
		Msg("进程启动")

	// 头像的磁盘缓存放 %LOCALAPPDATA%\alive\images。
	//
	// 和状态（%APPDATA%）分开：缓存丢了不丢任何配置，系统清理工具也能
	// 安全清掉。拿不到目录就只走内存，不是错误。
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		media.Default.SetDiskDir(filepath.Join(dir, "alive", "images"))
	}

	// 单实例锁：两个进程同时跑会共写 %APPDATA%\alive\state.json，后收尾的那份
	// 把先写的整份覆盖掉——用户在另一个窗口里改的设置就这么没了。
	//
	// 第二个实例把命令行交给已经跑着的那个（OnSecondInstance），自己退出。
	// 必须排在建 App / 装插件之前：锁在进程退出前一直持有，Run 返回后的收尾
	// （关播放器、写状态）也就不会和新实例重叠。
	//
	// win 提到这里声明：OnSecondInstance 的回调在主线程上稍后才跑，那时窗口
	// 已经建好；启动途中（win 还是 nil）什么都不用做，正在启动的那个实例
	// 本来就会出现在前面。
	var win *mygo.Window
	if !mygo.App.RequestSingleInstanceLock() {
		diag.L().Info().
			Str(diag.Event, diag.EvRunStop).
			Str(diag.Reason, "second_instance").
			Msg("已有实例在运行，本次启动把命令行交给它")
		applog.Close()
		return // 已有实例接管了这次启动
	}
	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		if win == nil {
			return
		}
		win.Restore()
		win.Focus()
	})

	// 自更新：签名发布在 GitHub release 上的版本，由这枚插件在后台按天查一次，
	// 有新版本自己弹一个原生窗口（native.Plugin 是同一套东西的自绘版，和这个
	// 应用一样不碰 webview——用 updater.Plugin 会为了一个更新对话框把 WebView2
	// 拖进来）。
	//
	// 必须在 App.Run 之前：插件在 Setup 里登记自己，等 App 就绪才开始排检查。
	// 没配 updates 的构建里它什么也不做（见 mygo.json 的 updates 段与
	// third_party/mygo/docs/plugins/updater.md）。
	mygo.Use(native.Plugin)

	app := appui.NewApp()
	// 诊断页的「检查更新」走这一条：界面不认识更新插件，只认识这个函数。
	app.CheckUpdates = updater.CheckForUpdates

	var watcher *hang.Watcher
	var followsPulse *time.Ticker
	// pulseStop 关闭心跳 goroutine：Run 返回后不关就是一条泄漏到进程结束。
	pulseStop := make(chan struct{})
	var minimized atomic.Bool
	mygo.App.WhenReady(func() {
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "ALIVE",
			Width:     1440,
			Height:    900,
			MinWidth:  900,
			MinHeight: 600,
			// 无边框：顶栏完全由 view 画，拖拽靠 Navbar 的 DragWindow。
			Frameless: true,
			// 首帧背景色与 --bg-primary 对齐，避免启动白闪。窗口一出现画的就是
			// 应用本体（顶栏 + 网格），没有单独的启动层，所以这层底色和第一帧
			// 之间没有接缝。
			BackgroundColor: "#151719",
			StateKey:        "main",
			Content:         ui.View(app.Shell),
		})
		// 自绘的窗口按钮要有窗口句柄才能真的最小化 / 最大化 / 关闭
		app.Win = win
		// 全屏状态下关窗，StateKey 会把 fullScreen 一起记进 window-state.json，
		// 下次启动一进来就是全屏。关之前先退出来：状态里留的是普通窗口。
		//
		// OnClose 在状态捕获（closeState）之前同步触发，自绘的关闭按钮和
		// Alt+F4 / 窗口菜单都走这一条，所以只需要挂在这里。
		// 最小化时 Shell 不再重建，墙的"看不见"判据就走不到了。
		// 挂在这里让「暂离时省流量」在最小化时也成立；恢复时清掉标记。
		win.OnMinimize(func() {
			minimized.Store(true)
			win.Update(func() { app.WatchHidden() })
		})
		win.OnRestore(func() {
			minimized.Store(false)
			win.Update(func() {
				app.WatchShown()
				// 挂机回来：关注面的绿点很可能已经停在离开那一刻。
				// 距上一轮超过 60s 才真刷，切来切去不会变成连环请求。
				app.FollowsRestored()
			})
		})
		win.OnClose(func(e *mygo.CloseEvent) {
			// 关窗时立即取消起播和弹幕任务，避免结果回到已销毁的窗口。
			app.CloseMedia()
			// 关窗前最后那点改动也要落地：markDirty 的 300ms 合并窗口还在等
			// 定时器，进程一退就丢了（拖一下音量条、300ms 内关窗就是这种情形）。
			// OnClose 同步返回后进程可能立刻结束，所以这里必须同步写完。
			app.FlushState()
			if win.IsFullScreen() {
				win.SetFullScreen(false)
			}
		})
		// 主线程卡死时，只有独立的观察者还能报案。它不依赖帧循环，
		// 最小化时暂停，避免把系统挂起误判成卡死。
		watcher = hang.Start(win, applog.Dir(), applog.RunID(), &minimized)

		// 图片解码必须回到主线程，缓存拿窗口来触发重绘
		media.Default.SetWindow(win)
		// 窗口一就绪就开始拉数据，不等首页第一帧：
		// 各平台的首屏列表和封面都要时间，早发出去，用户切平台时才是现成的。
		win.Update(func() { app.StartPrefetch() })

		// 关注列表的在线状态：等首屏起来再刷新一次，
		// 别跟首屏那批分区/列表请求抢带宽（也是 1.2s 后自动刷）。
		time.AfterFunc(1200*time.Millisecond, func() {
			win.Update(func() { app.RefreshFollows() })
		})

		// 关注页的心跳：每 20s 问一次「该不该刷」，真刷的间隔由
		// ui.followsHeartbeatFresh（90s）决定。只在关注页的关注主播
		// 那一屏生效，最小化时自动跳过（App.windowHidden）。
		//
		// 它是「关注的状态没人管」的收口：以前只有启动那一轮和手动按钮，
		// 开着一下午绿点就停在早上。live 包的 limiter 仍然在下面兜底。
		followsPulse = time.NewTicker(appui.FollowsPulseEvery)
		go func() {
			for {
				select {
				case <-followsPulse.C:
					win.Update(func() { app.FollowsHeartbeat() })
				case <-pulseStop:
					return
				}
			}
		}()
	})

	runErr := mygo.App.Run()
	if followsPulse != nil {
		followsPulse.Stop()
	}
	close(pulseStop)
	if watcher != nil {
		watcher.Stop()
	}
	if traceFile != nil {
		trace.Stop()
		_ = traceFile.Close()
		diag.L().Info().Str(diag.Event, diag.EvTraceStop).Msg("执行跟踪已结束")
	}
	if runErr != nil {
		diag.L().Error().
			Str(diag.Event, diag.EvRunStop).
			Str(diag.Reason, "error").
			Dict(diag.ErrField, diag.Err(runErr)).
			Msg("进程异常退出")
	} else {
		diag.L().Info().
			Str(diag.Event, diag.EvRunStop).
			Str(diag.Reason, "quit").
			Int64(diag.UptimeMS, time.Since(started).Milliseconds()).
			Msg("进程退出")
	}
	applog.Close()
	if runErr != nil {
		os.Exit(1)
	}
}

// fallbackLogError 在日志系统自身失败时留一份独立痕迹。
//
// 双击启动的进程没有 stderr；如果连日志文件都建不出来，唯一还能写的地方
// 就是 %TEMP%。这份文件不进正式链路，只用于回答「为什么没有日志」。
func fallbackLogError(err error) {
	path := filepath.Join(os.TempDir(), "alive-log-error.txt")
	line := time.Now().Format(time.RFC3339) + " " + err.Error() + "\n"
	_ = os.WriteFile(path, []byte(line), 0o644)
}

// appVersion 在未打包的构建（go run / go build）里是空串；日志里写 unknown，
// 不要留一个看起来像字段缺失的 version=。
func appVersion() string {
	if v := mygo.App.Version(); v != "" {
		return v
	}
	return "unknown"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
