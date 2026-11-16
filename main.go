// alive — MyGo 原生直播客户端。
//
// 窗口由 MyGo 自己用 GPU 画（Windows 上是 Direct3D 11），没有 webview，
// 所以没有 Chromium 的 gpu-process / renderer 那一套进程。
//
// 自动化验收：设置 ALIVE_CAPTURE=<path> 会在首屏稳定后截一张 PNG 并退出，
// ALIVE_CAPTURE_KEEP=1 保留窗口。
package main

import (
	"log"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	appui "alive/internal/ui"
)

func main() {
	app := appui.NewApp()
	// ALIVE_ROUTE 只在截图验收时用：直接从某个页面启动
	// 只收 subscribe / player 这种不带斜杠的名字：Git Bash 会把以 / 开头的
	// 环境变量值当成 Windows 路径改写（/subscribe -> D:/Dev/Git/subscribe），
	// 路由就永远匹配不上。
	if name := os.Getenv("ALIVE_ROUTE"); name != "" {
		if !strings.HasPrefix(name, "/") {
			name = "/" + name
		}
		app.Router = ui.NewRouter(name)
		slog.Info("起始路由", "path", app.Router.Path())
	}
	// 截图验收用：启动即停在某个平台（douyu / huya / douyin / bilibili）
	if p := os.Getenv("ALIVE_PLATFORM"); p != "" {
		app.Platform = p
	}
	// 截图验收用：ALIVE_ROOM=<platform>:<roomID> 直接进某个房间
	if v := os.Getenv("ALIVE_ROOM"); v != "" {
		if p, id, ok := strings.Cut(v, ":"); ok && id != "" {
			app.Player = appui.Streamer{Platform: p, RoomID: id, Name: "验收房间"}
			app.Playing = true
			app.Router = ui.NewRouter("/player")
		}
	}
	// 截图验收用：预置观看时长（ALIVE_WATCH=2h），验小时级的岛收起态
	if v := os.Getenv("ALIVE_WATCH"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			app.SetWatchForCapture(d)
		}
	}
	// 截图验收用：启动即展开某个浮层
	switch os.Getenv("ALIVE_UI") {
	case "palette":
		app.PaletteOpen = true
	case "island":
		// 全部关注页（原灵动岛展开的面板），用于截图验收
		app.Router = ui.NewRouter("/follows")
	case "search":
		app.SearchOpen = true
	case "quality":
		// 播放页清晰度下拉展开，用于截图验收
		app.QualityOpen = true
	case "danmu":
		// 播放页弹幕设置面板展开
		app.DanmuSettingsOpen = true
	case "filter":
		// 播放页弹幕过滤面板展开，用于截图验收
		app.DanmuFilterOpen = true
	case "sync":
		// 顶栏数据同步面板打开，用于截图验收
		app.SyncOpen = true
		app.SyncTab = "export"
	case "splash":
		// 启动层常驻，用于截图验收
		app.SplashHold = true
	case "nocover":
		// 封面全部画占位，用于验收占位样式
		appui.ForcePlaceholder = true
	}

	var win *mygo.Window
	mygo.App.WhenReady(func() {
		// 截图验收用：ALIVE_SIZE=1000x700 改初始窗口尺寸，验窄屏的两档媒体查询
		w, hgt := 1440, 900
		if v := os.Getenv("ALIVE_SIZE"); v != "" {
			if sw, sh, ok := strings.Cut(v, "x"); ok {
				if n, err := strconv.Atoi(sw); err == nil && n > 0 {
					w = n
				}
				if n, err := strconv.Atoi(sh); err == nil && n > 0 {
					hgt = n
				}
			}
		}
		win = mygo.NewWindow(mygo.WindowOptions{
			Title:     "alive",
			Width:     w,
			Height:    hgt,
			MinWidth:  900,
			MinHeight: 600,
			// 无边框：顶栏完全由 view 画，拖拽靠 Navbar 的 DragWindow。
			Frameless: true,
			// 首帧背景色与 --bg-primary 对齐，避免启动白闪。
			BackgroundColor: "#0a0b0d",
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
		win.OnClose(func(e *mygo.CloseEvent) {
			// 关窗时立即取消起播和弹幕任务，避免结果回到已销毁的窗口。
			app.CloseMedia()
			if win.IsFullScreen() {
				win.SetFullScreen(false)
			}
		})
		// StateKey 会在创建时把上次的窗口尺寸恢复回去，覆盖掉上面的
		// Width/Height，所以验收用的尺寸必须在这之后再压一次。
		if os.Getenv("ALIVE_SIZE") != "" {
			win.SetContentSize(w, hgt)
		}
		// 截图验收：等列表加载完后滚到末尾，检查页脚与分页状态
		if os.Getenv("ALIVE_UI") == "bottom" {
			// 滚到底会触发下一页，加载完内容又变长了：多滚几次才真的停在末尾
			for _, d := range []time.Duration{4 * time.Second, 8 * time.Second, 11 * time.Second} {
				time.AfterFunc(d, func() {
					win.Update(func() { app.HomeList.ScrollToEnd() })
				})
			}
		}
		// 截图验收：启动 700ms 后切到全部关注页
		if os.Getenv("ALIVE_UI") == "island-anim" {
			time.AfterFunc(700*time.Millisecond, func() {
				win.Update(func() { app.Router.Push("/follows") })
			})
		}
		// 图片解码必须回到主线程，缓存拿窗口来触发重绘
		media.Default.SetWindow(win)

		// 关注列表的在线状态：等首屏起来再刷新一次，
		// 别跟首屏那批分区/列表请求抢带宽（baseline 也是 1.2s 后自动刷）。
		time.AfterFunc(1200*time.Millisecond, func() {
			win.Update(func() { app.RefreshFollows() })
		})

		if path := os.Getenv("ALIVE_CAPTURE"); path != "" {
			go capture(win, path)
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// capture 等首屏稳定后截图，用于自动化验收。
func capture(win *mygo.Window, path string) {
	delay := 2 * time.Second
	if v := os.Getenv("ALIVE_CAPTURE_DELAY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			delay = d
		}
	}
	time.Sleep(delay)
	data, err := win.CapturePage()
	if err != nil {
		slog.Warn("窗口截图失败", "error", err)
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		slog.Warn("写入截图失败", "error", err)
		return
	}
	slog.Info("已保存窗口截图", "path", path, "bytes", len(data))
	if os.Getenv("ALIVE_CAPTURE_KEEP") == "" {
		mygo.App.Quit()
	}
}
