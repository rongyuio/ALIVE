package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// danmuPrefs 是弹幕设置的落盘形状，对应 baseline 的 dtv_danmu_preferences_v1。
type danmuPrefs struct {
	Font     float32  `json:"font"`
	DurMs    int64    `json:"durationMs"`
	Area     float32  `json:"area"`
	Opacity  float32  `json:"opacity"`
	Color    [4]uint8 `json:"color"`
	Stroke   [4]uint8 `json:"stroke"`
	Colorful bool     `json:"colorful"`
}

func colorBytes(c ui.Color) [4]uint8 { return [4]uint8{c.R, c.G, c.B, c.A} }

// persisted 是落地到 %APPDATA%/alive/state.json 的 UI 状态。
//
// 对应 baseline 的一组 localStorage 键：主题（theme_preference）、平台顺序
// （dtv_platform_tab_order_v1）、关注（followedStreamers）、订阅
// （dtv_custom_categories_v1）、播放器清晰度 / 线路 / 音量。
// 指针字段用来区分「没存过」和「存了一个空值」：用户可以把订阅和关注清空，
// 重启后必须还是空的。
type persisted struct {
	Palette       string          `json:"palette,omitempty"`
	PlatformOrder []string        `json:"platformOrder,omitempty"`
	Follows       *[]string       `json:"follows,omitempty"`       // 旧格式：只有「平台|昵称」
	FollowRecords *[]FollowRecord `json:"followRecords,omitempty"` // 完整关注记录
	Subs          *[]Subscription `json:"subscriptions,omitempty"`
	Quality       string          `json:"quality,omitempty"`
	Line          string          `json:"line,omitempty"`
	RelayEncoder  string          `json:"relayEncoder,omitempty"`
	Volume        *float32        `json:"volume,omitempty"`
	DanmuOn       *bool           `json:"danmuOn,omitempty"`
	Danmu         *danmuPrefs     `json:"danmu,omitempty"`
}

// statePath 是状态文件的位置；拿不到用户配置目录时返回空串，调用方跳过落盘。
func statePath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "alive", "state.json")
}

func loadState() (persisted, bool) {
	path := statePath()
	if path == "" {
		return persisted{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return persisted{}, false
	}
	var p persisted
	if json.Unmarshal(data, &p) != nil {
		return persisted{}, false
	}
	return p, true
}

// stateMu 串行化写盘：拖音量时会连着触发几次保存。
var stateMu sync.Mutex

func writeState(data []byte) {
	path := statePath()
	if path == "" {
		return
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	// 先写临时文件再 rename：写到一半退出也不会留下半个 JSON。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// markDirty 记一次状态变更，300ms 内的连续变更合并成一次落盘。
//
// 真正的序列化在主线程做（saveState），写文件才放到后台 goroutine。
func (a *App) markDirty() {
	a.stateDirty = true
	if a.stateSavePending || a.Win == nil {
		return
	}
	a.stateSavePending = true
	win := a.Win
	time.AfterFunc(300*time.Millisecond, func() {
		win.Update(func() { a.saveState() })
	})
}

// saveState 拍一份当前 UI 状态的快照并异步写盘。
func (a *App) saveState() {
	a.stateSavePending = false
	if !a.stateDirty {
		return
	}
	a.stateDirty = false

	follows := append([]FollowRecord(nil), a.Follows...)
	subs := append([]Subscription(nil), a.Subs...)
	volume := a.Volume
	danmuOn := a.DanmuOn
	danmu := danmuPrefs{
		Font:     a.DanmuFont,
		DurMs:    a.DanmuDur.Milliseconds(),
		Area:     a.DanmuArea,
		Opacity:  a.DanmuOpacity,
		Color:    colorBytes(a.DanmuColor),
		Stroke:   colorBytes(a.DanmuStroke),
		Colorful: a.DanmuColorful,
	}

	p := persisted{
		Palette:       a.PaletteID,
		PlatformOrder: a.PlatformOrder,
		FollowRecords: &follows,
		Subs:          &subs,
		Quality:       a.Quality,
		Line:          a.Line,
		RelayEncoder:  a.RelayEncoder,
		Volume:        &volume,
		DanmuOn:       &danmuOn,
		Danmu:         &danmu,
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return
	}
	go writeState(data)
}

// restore 把落盘的状态盖回新 App 的默认值上；文件不存在或坏掉就保持默认。
func (a *App) restore() {
	p, ok := loadState()
	if !ok {
		return
	}
	if p.Palette != "" && theme.Find(p.Palette) != nil {
		a.PaletteID = p.Palette
	}
	if len(p.PlatformOrder) > 0 {
		a.PlatformOrder = normalizePlatformOrder(p.PlatformOrder)
	}
	switch {
	case p.FollowRecords != nil:
		a.Follows = *p.FollowRecords
	case p.Follows != nil:
		// 旧格式只有「平台|昵称」，没有房间号：迁移成记录，房间号留空，
		// isFollowed 会按昵称兜底匹配，用户下次关注/取关时自然补全。
		for _, key := range *p.Follows {
			platform, name, ok := strings.Cut(key, "|")
			if !ok {
				continue
			}
			a.Follows = append(a.Follows, FollowRecord{Platform: platform, Name: name})
		}
	}
	if p.Subs != nil {
		a.Subs = *p.Subs
		// 旧版本把 NewApp 的示例订阅一并写进了 state.json：四条、CategoryID
		// 全空、顺序和示例池完全一致。这种组合只可能来自默认值——真订阅过
		// 的分区至少有 CategoryID，或者组合不同。整体丢掉，不然用户从没
		// 订阅过，订阅页却一直挂着四个假分区，删了下次启动还会回来。
		if isLegacyDemoSubs(a.Subs) {
			a.Subs = nil
		}
	}
	if p.Quality != "" {
		a.Quality = p.Quality
	}
	if p.Line != "" {
		a.Line = p.Line
	}
	if p.RelayEncoder != "" {
		a.RelayEncoder = p.RelayEncoder
	}
	if p.Volume != nil {
		a.Volume = *p.Volume
	}
	if p.DanmuOn != nil {
		a.DanmuOn = *p.DanmuOn
	}
	if p.Danmu != nil {
		if p.Danmu.Font > 0 {
			a.DanmuFont = p.Danmu.Font
		}
		if p.Danmu.DurMs > 0 {
			a.DanmuDur = time.Duration(p.Danmu.DurMs) * time.Millisecond
		}
		if p.Danmu.Area > 0 {
			a.DanmuArea = p.Danmu.Area
		}
		if p.Danmu.Opacity > 0 {
			a.DanmuOpacity = p.Danmu.Opacity
		}
		a.DanmuColor = ui.Color{
			R: p.Danmu.Color[0], G: p.Danmu.Color[1],
			B: p.Danmu.Color[2], A: p.Danmu.Color[3],
		}
		a.DanmuStroke = ui.Color{
			R: p.Danmu.Stroke[0], G: p.Danmu.Stroke[1],
			B: p.Danmu.Stroke[2], A: p.Danmu.Stroke[3],
		}
		a.DanmuColorful = p.Danmu.Colorful
	}
}

// legacyDemoSubs 是 NewApp 曾经写死的示例订阅。只用来识别被固化进
// state.json 的脏数据，不参与任何渲染。
var legacyDemoSubs = []Subscription{
	{PlatformID: "douyu", Platform: "斗鱼", Group: "网游", Name: "英雄联盟"},
	{PlatformID: "bilibili", Platform: "B站", Group: "娱乐", Name: "虚拟主播"},
	{PlatformID: "huya", Platform: "虎牙", Group: "手游", Name: "王者荣耀"},
	{PlatformID: "bilibili", Platform: "B站", Group: "单机", Name: "主机游戏"},
}

// isLegacyDemoSubs 报告 subs 是否就是当年那组示例订阅。
//
// 判据是「四条、顺序与示例池一致、CategoryID 全空」：真订阅至少有
// CategoryID（订阅页拉列表要用它），或者条数、组合不同。这个组合只可能
// 来自默认值，所以命中就整组丢掉。
func isLegacyDemoSubs(subs []Subscription) bool {
	if len(subs) != len(legacyDemoSubs) {
		return false
	}
	for i, s := range subs {
		want := legacyDemoSubs[i]
		if s.PlatformID != want.PlatformID || s.Group != want.Group || s.Name != want.Name {
			return false
		}
		if s.CategoryID != "" {
			return false
		}
	}
	return true
}

// movePlatform 把拖起来的平台放到目标 tab 的位置。
//
// baseline 在 dragenter 时就实时交换；这里在释放时一次落位，最终顺序一致：
// 先摘掉拖拽项，再插到目标原来的索引上。
func (a *App) movePlatform(dragID, targetID string) {
	order := normalizePlatformOrder(a.PlatformOrder)
	from, to := -1, -1
	for i, id := range order {
		switch id {
		case dragID:
			from = i
		case targetID:
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	order = append(order[:from], order[from+1:]...)
	order = append(order, "")
	copy(order[to+1:], order[to:])
	order[to] = dragID
	a.PlatformOrder = order
	a.markDirty()
}

// normalizePlatformOrder 把任意顺序整理成「认识的平台各一次，缺的补在后面」，
// 和 baseline 的 normalize 一致。
func normalizePlatformOrder(in []string) []string {
	known := map[string]bool{"douyu": true, "huya": true, "douyin": true, "bilibili": true}
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, id := range in {
		if known[id] && !seen[id] {
			out = append(out, id)
			seen[id] = true
		}
	}
	for _, id := range []string{"douyu", "huya", "douyin", "bilibili"} {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out
}
