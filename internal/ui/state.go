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

// danmuPrefs 是弹幕设置的落盘形状，对应 state.json 与数据同步文件里的 danmu。
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
// 键名就是下面这些 json tag，全部是 alive 自己的形状。改 tag 等于改文件
// 格式，要连带迁移旧 state.json 和数据同步文件。
// 指针字段用来区分「没存过」和「存了一个空值」：用户可以把订阅和关注清空，
// 重启后必须还是空的。
type persisted struct {
	Palette       string   `json:"palette,omitempty"`
	ThemeMode     string   `json:"themeMode,omitempty"` // system / dark / light
	PlatformOrder []string `json:"platformOrder,omitempty"`
	// FollowsSectionOrder 是关注页一级分区的显示顺序，
	// FollowsSection 是上次停在哪一区（用身份串，顺序拖过也不会串）。
	FollowsSectionOrder []string        `json:"followsSectionOrder,omitempty"`
	FollowsSection      string          `json:"followsSection,omitempty"`
	Follows             *[]string       `json:"follows,omitempty"`       // 旧格式：只有「平台|昵称」
	FollowRecords       *[]FollowRecord `json:"followRecords,omitempty"` // 完整关注记录
	Subs                *[]Subscription `json:"subscriptions,omitempty"`
	WatchRooms          *[]watchRecord  `json:"watchRooms,omitempty"`
	WatchSaveData       *bool           `json:"watchSaveData,omitempty"`
	// RoomChoices 是每个直播间自己的清晰度 / 线路（键 platform|roomID）。
	RoomChoices map[string]roomChoice `json:"roomChoices,omitempty"`
	// Quality / Line 是旧版的全局字段。新模型按房间存，读旧文件时无法
	// 判断这两个值属于哪一间房，只能丢弃；字段留着是为了旧 JSON 能解析，
	// 不再写盘。
	Quality      string      `json:"quality,omitempty"`
	Line         string      `json:"line,omitempty"`
	Volume       *float32    `json:"volume,omitempty"`
	DanmuOn      *bool       `json:"danmuOn,omitempty"`
	DanmuPanelW  *float32    `json:"danmuPanelW,omitempty"`
	DanmuPanelOn *bool       `json:"danmuPanelOn,omitempty"`
	Danmu        *danmuPrefs `json:"danmu,omitempty"`
	DanmuFilters *[]string   `json:"danmuFilters,omitempty"`
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
	watchRooms := make([]watchRecord, 0, len(a.WatchRooms))
	for _, s := range a.WatchRooms {
		watchRooms = append(watchRooms, a.watchRecordOf(s))
	}
	volume := a.Volume
	saveData := a.WatchSaveData
	panelW := a.panelWidth()
	panelOn := a.DanmuPanelOn
	danmuOn := a.DanmuOn
	// 屏蔽词也要落盘：用户加了一堆词，重启就该还在
	filters := append([]string(nil), a.DanmuFilters...)
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
		Palette:             a.PaletteID,
		ThemeMode:           a.ThemeMode,
		PlatformOrder:       a.PlatformOrder,
		FollowsSectionOrder: a.followsSectionOrder(),
		FollowsSection:      followsSectionID(a.FollowsSection),
		FollowRecords:       &follows,
		Subs:                &subs,
		WatchRooms:          &watchRooms,
		WatchSaveData:       &saveData,
		RoomChoices:         a.roomChoices,
		Volume:              &volume,
		DanmuOn:             &danmuOn,
		DanmuPanelW:         &panelW,
		DanmuPanelOn:        &panelOn,
		Danmu:               &danmu,
		DanmuFilters:        &filters,
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
	switch p.ThemeMode {
	case themeModeSystem, themeModeDark, themeModeLight:
		a.ThemeMode = p.ThemeMode
	}
	if len(p.PlatformOrder) > 0 {
		a.PlatformOrder = normalizePlatformOrder(p.PlatformOrder)
	}
	if len(p.FollowsSectionOrder) > 0 {
		a.FollowsSectionOrder = normalizeFollowsSectionOrder(p.FollowsSectionOrder)
	}
	if validFollowsSectionID(p.FollowsSection) {
		a.FollowsSection = followsSectionOf(p.FollowsSection)
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
	if p.WatchRooms != nil {
		rooms := make([]Streamer, 0, len(*p.WatchRooms))
		a.watch.init()
		for _, r := range *p.WatchRooms {
			s := r.streamer()
			rooms = append(rooms, s)
			// 每格音量是盒子状态的一部分，和房间一起恢复；没存过的不写，
			// 下次上墙继续继承全局音量。
			if r.Volume != nil {
				a.watch.volumes[watchKey(s)] = *r.Volume
			}
		}
		a.WatchRooms = normalizeWatchRooms(rooms)
	}
	if p.WatchSaveData != nil {
		a.WatchSaveData = *p.WatchSaveData
	}
	if p.RoomChoices != nil {
		a.roomChoices = p.RoomChoices
	}
	if p.Volume != nil {
		a.Volume = *p.Volume
	}
	if p.DanmuOn != nil {
		a.DanmuOn = *p.DanmuOn
	}
	if p.DanmuPanelOn != nil {
		a.DanmuPanelOn = *p.DanmuPanelOn
	}
	if p.DanmuPanelW != nil && *p.DanmuPanelW > 0 {
		// 拖拽宽度是本地布局，不进弹幕偏好；手改过的文件可能越界，读回来就钳一次。
		a.DanmuPanelW = *p.DanmuPanelW
		a.DanmuPanelW = a.panelWidth()
	}
	if p.DanmuFilters != nil {
		a.DanmuFilters = *p.DanmuFilters
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
// 拖拽项的交换也可以边拖边换；这里在释放时一次落位，最终顺序一致：
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

// normalizePlatformOrder 把任意顺序整理成「认识的平台各一次，缺的补在后面」。
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
