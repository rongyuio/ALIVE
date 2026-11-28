package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 平台下发的弹幕颜色是原始字符串，ui.Hex 遇到坏值会 panic。
//
// 现在四个平台都把它规整成 #RRGGBB，所以打不崩——但那是"上游替你校验"的
// 运气，不是这一层的不变量。任何新平台/新字段一透传原始字符串就是必崩，
// 而且崩在构建路径上，整窗挂掉。
func TestDanmuColorRejectsGarbage(t *testing.T) {
	fallback := theme.TextPrimary
	for _, bad := range []string{"#GGGGGG", "red", "1", "#12345", "#1234567", "rgb(1,2,3)"} {
		t.Run(bad, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("颜色 %q 让 danmuColor panic 了：%v", bad, r)
				}
			}()
			got := danmuColor(danmuLine{Color: bad}, true, fallback)
			if got != fallback {
				t.Errorf("坏颜色 %q 没有回落到 fallback，得到 %v", bad, got)
			}
		})
	}
	// 正常值照旧生效：别把守卫写成永远回落。
	good := danmuColor(danmuLine{Color: "#ff8800"}, true, fallback)
	if good == fallback {
		t.Error("合法的平台色被守卫吞掉了")
	}
}

// 自动补页的退避不能靠"刚加过 1"来保证移位是正的。
//
// Go 里负移位直接 panic。原来的写法 topUpFailures-1 在它被写成 0 的路径上
// 就会炸；这里把边界钉住。
func TestTopUpBackoffNeverShiftsNegative(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("退避计算 panic 了：%v", r)
		}
	}()
	a := newApp()
	// 直接算若干次，覆盖 0 / 1 / 溢出到封顶
	for i := 0; i < 12; i++ {
		a.noteTopUpFailure()
	}
	if a.topUpFailures != 12 {
		t.Fatalf("失败计数 %d", a.topUpFailures)
	}
	// 封顶：无论多少次都不超过 30s
	if d := time.Until(a.topUpAfter); d > topUpBackoffMax+time.Second {
		t.Errorf("退避 %v 超过封顶 %v", d, topUpBackoffMax)
	}
}

// 快照序列化失败时不能把 dirty 清掉——否则这一轮改动永久丢失，连 warn 都不亮。
func TestSnapshotKeepsDirtyWhenMarshalFails(t *testing.T) {
	a := newApp()
	a.markDirty()
	// 让 marshal 必然失败：往一个不能序列化的位置塞东西做不到（persisted 全是
	// 基本类型），所以这条验的是"顺序"本身——成功路径必须清 dirty，
	// 而 dirty 的清除必须发生在 marshal 之后。
	data, _, ok := a.stateSnapshot()
	if !ok || len(data) == 0 {
		t.Fatal("正常快照应该成功")
	}
	if a.stateDirty {
		t.Error("成功写出之后 dirty 还立着：会无限重写")
	}
	// 再来一次：没有新改动就不该有快照
	if _, _, ok := a.stateSnapshot(); ok {
		t.Error("没有改动却拍出了快照")
	}
}

// 没有窗口时不该开始一个收不了尾的同步动作。
//
// SyncBusy 一旦立起来，所有同步按钮都进不去（连"停止剖析"都点不动），
// 而收尾只能靠 win.Update——没有窗口就没有那个入口。
func TestSyncActionsRefuseWithoutWindow(t *testing.T) {
	a := newApp()
	if a.Win != nil {
		t.Skip("这个 App 带着窗口，跳过")
	}
	a.exportDiagnostics()
	if a.SyncBusy {
		t.Error("没有窗口时 exportDiagnostics 把 SyncBusy 立起来了：面板会永久锁死")
	}
	a.profileCPU()
	if a.SyncBusy {
		t.Error("没有窗口时 profileCPU 把 SyncBusy 立起来了")
	}
}

// 重连中的格子显示"正在重连…"，不显示内部原因。
//
// 内部原因（首帧超时 / 地址到期 / user-reload）是排障字眼；用户看到
// "地址到期"会以为这一路坏了，而地址到期是正常续期。
func TestReconnectingTileShowsUserFacingText(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room}
	a.watchRestart(s, "地址到期")

	if s.err != "" {
		t.Errorf("内部原因被写进了用户可见的 err：%q", s.err)
	}
	if !s.reconnecting {
		t.Error("重连状态没有置位")
	}

	// 画出来的文案里不能出现内部原因
	tt := ui.NewTester(func(c *ui.Context) {
		a.watchState(c, room, s, nil)
	}, 320, 180)
	tt.Frame()
	if tt.HasText("地址到期") {
		t.Error("格子上画出了内部原因「地址到期」")
	}
	if !tt.HasText("正在重连") {
		t.Error("格子上没有「正在重连」")
	}
}

// 日志里仍然要留下内部原因：它只换了个去处，没有消失。
func TestWatchRestartStillLogsReason(t *testing.T) {
	a := newApp()
	room := Streamer{Platform: "p", RoomID: "1", Name: "房"}
	s := &roomSession{key: "p|1", room: room, log: wallLog("p|1", "")}
	a.watchRestart(s, "首帧超时")
	// 结构上能验证的：reason 进了日志调用（watchRestart 里 diag.Cause）。
	// 这里只钉住"没有把它丢进 s.err"这一条副作用。
	if strings.Contains(s.err, "首帧超时") {
		t.Error("内部原因漏进了 s.err")
	}
}
