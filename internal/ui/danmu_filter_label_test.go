package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 弹幕屏蔽词输入框也要有名字。
//
// 其它三处 TextInputBase（顶栏搜索、分区搜索、关注页搜索）都写了 Label，
// 只有这里只有 Placeholder；MyGo 的无障碍名只来自 Label，读屏和
// mygo-automation 都只能按坐标找它。
func TestDanmuFilterFieldHasAccessibleName(t *testing.T) {
	app := NewMockApp()
	app.Player = app.Streamers[0]
	app.Router.Push(routePlayer)
	app.DanmuFilterOpen = true

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	for _, n := range tt.AccessAll() {
		if n.Name == "弹幕屏蔽词" {
			return
		}
	}
	t.Fatal("弹幕屏蔽词输入框没有 .Label：无障碍树里找不到「弹幕屏蔽词」")
}
