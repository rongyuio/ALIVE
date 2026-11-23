package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 拖拽排序：把拖起来的平台放到目标原来的索引上。
func TestMovePlatform(t *testing.T) {
	app := NewMockApp()
	app.PlatformOrder = []string{"douyu", "huya", "douyin", "bilibili"}

	app.movePlatform("douyu", "douyin")
	want := []string{"huya", "douyin", "douyu", "bilibili"}
	for i := range want {
		if app.PlatformOrder[i] != want[i] {
			t.Fatalf("拖 斗鱼→抖音 后顺序 %v，期望 %v", app.PlatformOrder, want)
		}
	}

	app.movePlatform("bilibili", "huya")
	want = []string{"bilibili", "huya", "douyin", "douyu"}
	for i := range want {
		if app.PlatformOrder[i] != want[i] {
			t.Fatalf("拖 B站→虎牙 后顺序 %v，期望 %v", app.PlatformOrder, want)
		}
	}
}

// 一级分区拖动换位：把拖起来的分区放到目标原来的索引上。
//
// 只有两格时结果就是交换，但落位规则和 movePlatform 同一套——以后加第三个
// 分区不用重写。认不出来的身份不改变顺序。
func TestMoveFollowsSection(t *testing.T) {
	app := NewMockApp()
	app.FollowsSectionOrder = []string{sectionIDStreamers, sectionIDSubs}

	app.moveFollowsSection(sectionIDStreamers, sectionIDSubs)
	if got, want := app.FollowsSectionOrder, []string{sectionIDSubs, sectionIDStreamers, sectionIDWatch}; !equalStrings(got, want) {
		t.Fatalf("拖「关注主播」到「订阅分区」后顺序 %v，期望 %v", got, want)
	}

	// 状态表被改坏时：不认识的丢掉，缺的按默认补在后面
	app.FollowsSectionOrder = []string{"unknown", sectionIDSubs}
	if got, want := app.followsSectionOrder(), []string{sectionIDSubs, sectionIDStreamers, sectionIDWatch}; !equalStrings(got, want) {
		t.Fatalf("坏数据整理后顺序 %v，期望 %v", got, want)
	}

	// 拖到自己身上不动
	before := append([]string(nil), app.FollowsSectionOrder...)
	app.moveFollowsSection(sectionIDSubs, sectionIDSubs)
	if got := app.FollowsSectionOrder; !equalStrings(got, before) {
		t.Fatalf("拖到自己身上改了顺序：%v → %v", before, got)
	}
}

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.PaletteID = "parchment"
	app.toggleFollow(app.Streamers[0])
	app.Subs = []Subscription{
		{PlatformID: "huya", Platform: "虎牙", Group: "手游", Name: "原神"},
	}
	app.setRoomChoice("bilibili|1001", roomChoice{Quality: "蓝光", Line: "线路3", LineID: "l3"})
	app.Volume = 0.25
	app.PlatformOrder = normalizePlatformOrder([]string{"bilibili", "douyu"})
	app.FollowsSectionOrder = []string{sectionIDSubs, sectionIDStreamers}
	app.stateDirty = true
	app.saveState()

	// 写盘在后台 goroutine，等文件落地
	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	restored := NewMockApp()
	if restored.PaletteID != "parchment" {
		t.Errorf("配色 %q，期望 parchment", restored.PaletteID)
	}
	if !restored.isFollowed(app.Streamers[0]) {
		t.Errorf("关注 %q 没有恢复", app.Streamers[0].Name)
	}
	if len(restored.Subs) != 1 || restored.Subs[0].Name != "原神" {
		t.Errorf("订阅 %+v，期望只有「原神」", restored.Subs)
	}
	if c := restored.roomChoiceOf("bilibili|1001"); c.Quality != "蓝光" || c.Line != "线路3" || c.LineID != "l3" {
		t.Errorf("房间的清晰度/线路没有恢复：%+v", c)
	}
	if len(restored.roomChoices) != 1 {
		t.Errorf("房间选择多出了别的项：%+v", restored.roomChoices)
	}
	if restored.Volume != 0.25 {
		t.Errorf("音量 %v，期望 0.25", restored.Volume)
	}
	if len(restored.PlatformOrder) != 4 || restored.PlatformOrder[0] != "bilibili" {
		t.Errorf("平台顺序 %v", restored.PlatformOrder)
	}
	if got, want := restored.followsSectionOrder(), []string{sectionIDSubs, sectionIDStreamers, sectionIDWatch}; !equalStrings(got, want) {
		t.Errorf("一级分区顺序 %v，期望 %v", got, want)
	}
}

// 弹幕面板是拖宽的，重启必须还是那个宽度：拖拽只改内存里的
// DanmuPanelW，不落盘的话下次启动又回到 196。
func TestDanmuPanelWidthPersists(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.DanmuPanelW = 264
	app.stateDirty = true
	app.saveState()

	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	restored := NewMockApp()
	if restored.DanmuPanelW != 264 {
		t.Errorf("弹幕面板宽 %v，期望 264", restored.DanmuPanelW)
	}
}

// 手改过的 state.json 也要收得回范围：面板宽只认 168–320。
func TestDanmuPanelWidthClampedOnRestore(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"danmuPanelW": 900}`), 0o644); err != nil {
		t.Fatal(err)
	}

	restored := NewMockApp()
	if got := restored.panelWidth(); got != danmuPanelMaxW {
		t.Errorf("恢复后面板宽 %v，期望钳到 %v", got, danmuPanelMaxW)
	}
}

// 面板收起之后重启还是收起：默认是展开，只有真的落盘 false 才能区分。
func TestDanmuPanelOpenStatePersists(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.DanmuPanelOn = false
	app.stateDirty = true
	app.saveState()

	path := statePath()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	restored := NewMockApp()
	if restored.DanmuPanelOn {
		t.Error("面板收起状态没有恢复，重启后又是展开")
	}
}

// 关注页停在哪个一级分区要记住：下次打开还是悲鸣观测，不该回到关注主播。
func TestFollowsSectionPersists(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.selectFollowsSection(followsSectionWatch)
	app.saveState()

	path := statePath()
	if path == "" {
		t.Fatal("拿不到状态文件路径")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("状态文件没有落盘：%s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	restored := NewMockApp()
	if restored.FollowsSection != followsSectionWatch {
		t.Fatalf("重启后停在 %d，期望悲鸣观测", restored.FollowsSection)
	}
}

// 旧版 state.json 里 FollowRecord / Subscription 的键是 Go 字段名（PascalCase）。
// 给它们补 json tag 之后旧文件必须还能读——json 匹配键名不区分大小写，
// 这条把兼容性钉住，免得以后改 tag 时以为旧数据不重要。
func TestLegacyStateKeyCase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)

	raw := `{
  "followRecords": [
    {
      "Platform": "bilibili",
      "RoomID": "1001",
      "Name": "老王",
      "AvatarURL": "https://a/b.png",
      "Viewers": 3200,
      "Live": true,
      "FollowedAt": "2026-11-21T19:59:22+08:00"
    }
  ],
  "subscriptions": [
    {
      "PlatformID": "douyu",
      "Platform": "斗鱼",
      "Group": "网游",
      "Name": "英雄联盟",
      "CategoryID": "1_1"
    }
  ]
}`
	path := filepath.Join(dir, "alive", "state.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	app := NewMockApp()
	if len(app.Follows) != 1 {
		t.Fatalf("旧格式关注读出了 %d 条", len(app.Follows))
	}
	f := app.Follows[0]
	if f.Platform != "bilibili" || f.RoomID != "1001" || f.Name != "老王" || f.Viewers != 3200 || !f.Live {
		t.Errorf("关注字段没有对上：%+v", f)
	}
	if f.FollowedAt.IsZero() {
		t.Error("关注时间丢了")
	}
	if len(app.Subs) != 1 || app.Subs[0].CategoryID != "1_1" {
		t.Errorf("旧格式订阅没有对上：%+v", app.Subs)
	}
}
