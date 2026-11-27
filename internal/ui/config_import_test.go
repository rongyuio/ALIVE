package ui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"alive/internal/live"
)

// 导入去重和运行时「是不是同一个人」必须是同一个等价关系。
//
// sameFollow 的规则是「两侧都有房间号才比房间号，否则比昵称」。导入去重
// 如果只认「平台|房间号」这一把键，一条 roomID 为空的老记录和一条带房间号
// 的新记录会被当成两个人：导入后同一主播变成两条，而取消关注用 sameFollow
// 匹配 → 一次删掉两条。
func TestImportDedupesSameFollowAcrossMissingRoomID(t *testing.T) {
	a := newApp()
	a.Follows = []FollowRecord{
		// 老记录：只有昵称，没有房间号（state.json 迁移出来就是这样）
		{Platform: "bilibili", Name: "甲"},
	}
	f := configFile{Follows: []FollowRecord{
		// 同一主播，这次带了房间号
		{Platform: "bilibili", RoomID: "42", Name: "甲"},
	}}
	p := a.planImport("t.json", f)
	if len(p.Follows) != 0 {
		t.Fatalf("导入把它当成 %d 条新关注，应为 0：同一主播会被写成两条", len(p.Follows))
	}
}

// 反过来：本机有房间号、文件里只有昵称，也不能重复。
func TestImportDedupesReverseMissingRoomID(t *testing.T) {
	a := newApp()
	a.Follows = []FollowRecord{{Platform: "bilibili", RoomID: "42", Name: "甲"}}
	f := configFile{Follows: []FollowRecord{{Platform: "bilibili", Name: "甲"}}}
	p := a.planImport("t.json", f)
	if len(p.Follows) != 0 {
		t.Fatalf("导入把它当成 %d 条新关注，应为 0", len(p.Follows))
	}
}

// 真的不同的人还是要加进来：别把守卫写成永远去重。
func TestImportKeepsRealNewFollow(t *testing.T) {
	a := newApp()
	a.Follows = []FollowRecord{{Platform: "bilibili", RoomID: "42", Name: "甲"}}
	f := configFile{Follows: []FollowRecord{{Platform: "bilibili", RoomID: "99", Name: "乙"}}}
	p := a.planImport("t.json", f)
	if len(p.Follows) != 1 {
		t.Fatalf("真的新关注被吞了：%d 条", len(p.Follows))
	}
}

// 同一个文件内部的两条也不能重（去重集合要边加边更新）。
func TestImportDedupesWithinFile(t *testing.T) {
	a := newApp()
	f := configFile{Follows: []FollowRecord{
		{Platform: "bilibili", RoomID: "42", Name: "甲"},
		{Platform: "bilibili", Name: "甲"}, // 同一个主播，没带号
	}}
	p := a.planImport("t.json", f)
	if len(p.Follows) != 1 {
		t.Fatalf("文件内重复没去干净：%d 条", len(p.Follows))
	}
}

// 弹幕参数必须钳进合法区间（导入路径）。
//
// 不钳制的话 MaxInt64 * time.Millisecond 溢出成负数，而负时长的弹幕在
// tickDanmu 里 `now.Sub(d.Born) < d.Dur` 恒假——每条入队当帧就被丢掉，
// 用户看到的是「弹幕开着但一条都不显示」。
func TestImportClampsDanmuParams(t *testing.T) {
	a := newApp()
	maxInt := int64(^uint64(0) >> 1)
	f := configFile{Danmu: &fileDanmu{
		On: true, Font: 1e9, DurMs: maxInt, Area: 1e30, Opacity: 1e30,
	}}
	p := a.planImport("t.json", f)
	a.applyImportPlan(p)

	if a.DanmuDur <= 0 {
		t.Fatalf("时长为 %v：弹幕一条都不会显示", a.DanmuDur)
	}
	if a.DanmuDur > time.Duration(danmuDurMaxMs)*time.Millisecond {
		t.Errorf("时长 %v 超出上限", a.DanmuDur)
	}
	if a.DanmuFont > danmuFontMax || a.DanmuFont < danmuFontMin {
		t.Errorf("字号 %v 超出 [%v, %v]", a.DanmuFont, danmuFontMin, danmuFontMax)
	}
	if a.DanmuArea > danmuAreaMax || a.DanmuArea < danmuAreaMin {
		t.Errorf("显示区域 %v 超出 [%v, %v]", a.DanmuArea, danmuAreaMin, danmuAreaMax)
	}
	if a.DanmuOpacity > danmuOpacityMax || a.DanmuOpacity < danmuOpacityMin {
		t.Errorf("不透明度 %v 超出 [%v, %v]", a.DanmuOpacity, danmuOpacityMin, danmuOpacityMax)
	}
}

// 恢复（state.json）走同一套钳制：手改过的文件同样能写进坏值。
func TestRestoreClampsDanmuParams(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AppData", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	// os.UserConfigDir 在 Windows 上读 AppData；把这个目录指到临时目录。
	if cfg, err := os.UserConfigDir(); err != nil || cfg == "" {
		t.Skip("拿不到配置目录，跳过")
	} else {
		full := filepath.Join(cfg, "alive")
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Skipf("建不了目录：%v", err)
		}
		maxInt := int64(^uint64(0) >> 1)
		body := `{"danmu":{"font":1e9,"durationMs":` + itoa(maxInt) + `,"area":1e30,"opacity":1e30}}`
		if err := os.WriteFile(filepath.Join(full, "state.json"), []byte(body), 0o644); err != nil {
			t.Skipf("写不了 state.json：%v", err)
		}
	}

	a := newApp()
	a.restore()
	if a.DanmuDur <= 0 {
		t.Fatalf("恢复出来的时长是 %v：弹幕一条都不会显示", a.DanmuDur)
	}
	if a.DanmuFont > danmuFontMax {
		t.Errorf("恢复出来的字号 %v 超上限", a.DanmuFont)
	}
}

// itoa 只用于拼上面那段 JSON。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// sameFollow 不是等价关系：A 有号、B 无号、C 有另一个号时，A~B~C 但 A≁C。
// 所以导入去重不能用「身份键 → 集合」，必须逐条问 sameFollow。这条钉住
// 那个非传递的例子，防止有人图省事把它改回键集合。
func TestImportDedupUsesSameFollowNotKeys(t *testing.T) {
	a := newApp()
	a.Follows = []FollowRecord{
		{Platform: "b", RoomID: "1", Name: "甲"},
	}
	f := configFile{Follows: []FollowRecord{
		{Platform: "b", RoomID: "2", Name: "甲"}, // 同昵称不同号：不是同一个人
	}}
	p := a.planImport("t.json", f)
	if len(p.Follows) != 1 {
		t.Fatalf("同昵称不同号被判成同一个人：%d 条新增，应为 1", len(p.Follows))
	}
}

var _ = live.Bilibili
