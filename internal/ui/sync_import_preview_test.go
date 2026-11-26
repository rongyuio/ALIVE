package ui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 导入分两步：选完文件只算计划、摆出改了什么，确认之后才写本机。
// 文件会覆盖主题、音量、弹幕这些正在用的值，不给用户看一眼就直接改，
// 等于把「撤销」也一起藏起来。
func TestImportPreviewThenConfirm(t *testing.T) {
	app := NewMockApp()
	app.Follows = nil
	app.Subs = nil
	app.WatchRooms = nil
	app.DanmuFilters = nil
	app.ThemeMode = themeModeSystem

	plan := app.planImport("ALIVE-20261125.json", configFile{
		App:   configApp,
		Theme: fileTheme{Mode: themeModeDark},
		Follows: []FollowRecord{
			{Platform: "bilibili", RoomID: "42", Name: "新人"},
		},
		Danmu: &fileDanmu{On: true, Font: 30},
	})
	if plan.empty() {
		t.Fatal("文件里有新增和覆盖，计划却是空的")
	}
	if len(app.Follows) != 0 || app.ThemeMode != themeModeSystem {
		t.Fatalf("只算计划就改了本机：关注 %d 条、明暗 %q", len(app.Follows), app.ThemeMode)
	}
	if !strings.Contains(plan.Adds, "1 位关注") {
		t.Errorf("新增概览没提到关注：%q", plan.Adds)
	}
	if !strings.Contains(strings.Join(plan.Overwrites, "；"), "明暗") {
		t.Errorf("覆盖清单没提到明暗：%v", plan.Overwrites)
	}

	app.SyncOpen = true
	app.SyncTab = "import"
	app.SyncPlan = &plan

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if _, ok := tt.Find("ALIVE-20261125.json"); !ok {
		t.Fatal("预览里没有文件名")
	}
	if _, ok := tt.Find("覆盖"); !ok {
		t.Fatal("预览里没有覆盖项")
	}
	if _, ok := tt.Find("确认导入"); !ok {
		t.Fatal("预览里没有确认入口")
	}
	if len(app.Follows) != 0 || app.ThemeMode != themeModeSystem {
		t.Fatalf("预览阶段就落地了：关注 %d 条、明暗 %q", len(app.Follows), app.ThemeMode)
	}

	if err := tt.Click("确认导入"); err != nil {
		t.Fatalf("点不到确认导入：%v", err)
	}
	tt.Frame()

	if len(app.Follows) != 1 || app.Follows[0].RoomID != "42" {
		t.Fatalf("确认后关注没落地：%+v", app.Follows)
	}
	if app.ThemeMode != themeModeDark {
		t.Errorf("确认后明暗没落地：%q", app.ThemeMode)
	}
	if app.SyncPlan != nil {
		t.Error("确认后计划没有清掉")
	}
	if app.SyncNotice == "" {
		t.Error("确认后没有结果提示")
	}
	if _, ok := tt.Find("确认导入"); ok {
		t.Error("确认后还停在预览页")
	}
}

// 取消就是不导入：本机一个字段都不动。
func TestImportCancelKeepsMachineUntouched(t *testing.T) {
	app := NewMockApp()
	app.Follows = nil
	app.ThemeMode = themeModeSystem

	plan := app.planImport("ALIVE.json", configFile{
		App:     configApp,
		Theme:   fileTheme{Mode: themeModeDark},
		Follows: []FollowRecord{{Platform: "douyu", RoomID: "9", Name: "老张"}},
	})
	app.SyncOpen = true
	app.SyncTab = "import"
	app.SyncPlan = &plan

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if err := tt.Click("取消"); err != nil {
		t.Fatalf("点不到取消：%v", err)
	}
	tt.Frame()

	if app.SyncPlan != nil {
		t.Error("取消后计划还在")
	}
	if len(app.Follows) != 0 || app.ThemeMode != themeModeSystem {
		t.Fatalf("取消还是改了本机：关注 %d 条、明暗 %q", len(app.Follows), app.ThemeMode)
	}
}

// 文件里的东西本机都有时，清单为空，不给「确认导入」——没什么可确认的。
func TestImportEmptyPlanHasNoConfirm(t *testing.T) {
	app := NewMockApp()
	f := app.buildConfigFile()
	// 先按自己的快照导入一次，把能加的都加进去；再算一遍应当是空的。
	app.applyConfig(f)
	plan := app.planImport("ALIVE.json", f)
	if !plan.empty() {
		t.Fatalf("同一份快照算出了改动：新增 %q、覆盖 %v", plan.Adds, plan.Overwrites)
	}

	app.SyncOpen = true
	app.SyncTab = "import"
	app.SyncPlan = &plan

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if _, ok := tt.Find("确认导入"); ok {
		t.Fatal("没有改动还给了确认入口")
	}
	if _, ok := tt.Find("没有可导入的内容：这些数据本机已经有了"); !ok {
		t.Fatal("没有告诉用户为什么没得导")
	}
}

// 预览说改多少，确认后就改多少：两边都从同一份清单出发，不许各算各的。
func TestImportPreviewMatchesResult(t *testing.T) {
	app := NewMockApp()
	app.Follows = []FollowRecord{{Platform: "bilibili", RoomID: "7", Name: "老粉"}}
	app.Subs = nil
	app.WatchRooms = []Streamer{{Platform: "huya", RoomID: "9", Name: "老张"}}
	app.DanmuFilters = []string{"加微信"}

	f := configFile{
		App: configApp,
		Follows: []FollowRecord{
			{Platform: "bilibili", RoomID: "7", Name: "老粉"},
			{Platform: "douyu", RoomID: "77", Name: "老李"},
			{Platform: "douyu", RoomID: "78", Name: "老王"},
		},
		Subscriptions: []Subscription{{PlatformID: "douyu", Group: "网游", Name: "英雄联盟"}},
		WatchWall: []watchRecord{
			{Platform: "huya", RoomID: "9", Name: "老张"},
			{Platform: "douyu", RoomID: "77", Name: "老李"},
		},
		Danmu: &fileDanmu{On: true, Filters: []string{"加微信", "抽奖"}},
	}
	plan := app.planImport("ALIVE.json", f)
	sum := app.applyImportPlan(plan)

	if sum.Follows != 2 || sum.Subs != 1 || sum.Watch != 1 || sum.Filters != 1 {
		t.Fatalf("按清单落地 %+v，期望 2 关注 / 1 订阅 / 1 观测墙 / 1 屏蔽词", sum)
	}
	for _, want := range []string{"2 位关注", "1 个订阅", "1 格观测墙", "1 个屏蔽词"} {
		if !strings.Contains(plan.Adds, want) {
			t.Errorf("新增概览 %q 里没有 %q", plan.Adds, want)
		}
	}
	if len(app.Follows) != 3 || len(app.Subs) != 1 || len(app.WatchRooms) != 2 || len(app.DanmuFilters) != 2 {
		t.Fatalf("落地后的实际数量对不上预览：关注 %d、订阅 %d、观测墙 %d、屏蔽词 %d",
			len(app.Follows), len(app.Subs), len(app.WatchRooms), len(app.DanmuFilters))
	}
	if !strings.Contains(plan.notice(), "新增 2 位关注") {
		t.Errorf("结果文案没说清新增了什么：%q", plan.notice())
	}
}

// 预览属于「导入」这一页：切到别的页签就丢掉，不能跟着串到导出页去。
func TestTabSwitchDropsPendingImport(t *testing.T) {
	app := NewMockApp()
	before := len(app.Follows)
	plan := app.planImport("ALIVE.json", configFile{
		App:     configApp,
		Follows: []FollowRecord{{Platform: "douyu", RoomID: "1", Name: "新人"}},
	})
	app.SyncOpen = true
	app.SyncTab = "import"
	app.SyncPlan = &plan

	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.Frame()

	if err := tt.Click("诊断"); err != nil {
		t.Fatalf("点不到诊断页签：%v", err)
	}
	tt.Frame()

	if app.SyncPlan != nil {
		t.Error("切页签后还留着上一页的导入预览")
	}
	if len(app.Follows) != before {
		t.Errorf("切页签把预览落地了：关注 %d → %d", before, len(app.Follows))
	}
}
