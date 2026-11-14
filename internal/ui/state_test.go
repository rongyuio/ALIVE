package ui

import (
	"os"
	"testing"
	"time"
)

// 拖拽排序：把拖起来的平台放到目标原来的索引上（与 baseline 的
// splice(from,1) + splice(to,0) 一致）。
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

func TestStateRoundTrip(t *testing.T) {
	t.Setenv("AppData", t.TempDir())

	app := NewMockApp()
	app.PaletteID = "parchment"
	app.toggleFollow(app.Streamers[0])
	app.Subs = []Subscription{
		{PlatformID: "huya", Platform: "虎牙", Group: "手游", Name: "原神"},
	}
	app.Quality = "蓝光"
	app.Line = "线路3"
	app.Volume = 0.25
	app.PlatformOrder = normalizePlatformOrder([]string{"bilibili", "douyu"})
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
	if restored.Quality != "蓝光" || restored.Line != "线路3" {
		t.Errorf("清晰度/线路 %q/%q", restored.Quality, restored.Line)
	}
	if restored.Volume != 0.25 {
		t.Errorf("音量 %v，期望 0.25", restored.Volume)
	}
	if len(restored.PlatformOrder) != 4 || restored.PlatformOrder[0] != "bilibili" {
		t.Errorf("平台顺序 %v", restored.PlatformOrder)
	}
}
