package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 诊断页要有版本行和「检查更新」入口。
//
// 更新的主路径是后台自动（mygo 的更新插件按天查一次，有新版自己弹窗），
// 所以这一条盯的是另一件事：用户自己想问一次的时候找得到地方。没有手动入口
// 的自动更新，在「是不是该有新版了」的那一刻只能干等。
//
// 期望值来自屏幕上的口径：测试二进制没有版本号，写「本地构建」而不是留白
// 或者 unknown（后者是给导出包里的脚本看的，见 diagVersion）。
func TestDiagShowsVersionAndChecksOnClick(t *testing.T) {
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()

	calls := 0
	app.CheckUpdates = func() { calls++ }

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if !tt.HasText("版本") {
		t.Fatalf("诊断页没有版本行：%v", tt.Texts())
	}
	if !tt.HasText("ALIVE 本地构建") {
		t.Errorf("未打包的构建应写成「本地构建」：%v", tt.Texts())
	}
	if err := tt.Click("检查更新"); err != nil {
		t.Fatalf("点不到检查更新：%v", err)
	}
	if calls != 1 {
		t.Errorf("点一次检查更新调了 %d 次，期望 1", calls)
	}
}

// 没有注入更新能力时（Tester，以及任何不 Use 更新插件的构建）不画那枚按钮。
//
// 这一条不是洁癖：updated 插件的 CheckForUpdates 在没 Use 过插件时直接 panic
// （updater.used），画一枚点了就崩的按钮比没有按钮更糟。版本行仍然要在——
// 它跟插件无关，读的是构建注入的版本号。
func TestDiagHidesCheckWithoutUpdatePlugin(t *testing.T) {
	app := NewMockApp()
	app.openSyncPanel()
	app.SyncTab = "diag"
	app.DiagGroups, app.DiagEmpty = diagnosticGroups()

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if _, ok := tt.Find("检查更新"); ok {
		t.Error("没有注入 CheckUpdates 时不该画检查更新按钮")
	}
	if !tt.HasText("ALIVE 本地构建") {
		t.Errorf("版本行仍要在：%v", tt.Texts())
	}
}
