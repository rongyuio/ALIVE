// alive — MyGo 原生直播客户端。
//
// 窗口由 MyGo 自己用 GPU 画（Windows 上是 Direct3D 11），没有 webview，
// 所以没有 Chromium 的 gpu-process / renderer 那一套进程。
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/media"
	appui "alive/internal/ui"

	// 目录里只有打包用的 mygo.* 文案，但导入它才会把应用的语言
	// （zh-Hans）注册给 MyGo 和各个系统的本地化。
	_ "alive/locales"
)

func main() {
	// 头像的磁盘缓存放 %LOCALAPPDATA%\alive\images。
	//
	// 和状态（%APPDATA%）分开：缓存丢了不丢任何配置，系统清理工具也能
	// 安全清掉。拿不到目录就只走内存，不是错误。
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		media.Default.SetDiskDir(filepath.Join(dir, "alive", "images"))
	}

	app := appui.NewApp()

	var win *mygo.Window
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
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
