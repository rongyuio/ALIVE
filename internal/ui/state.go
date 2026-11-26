package ui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/diag"
	"alive/internal/theme"
)

// danmuPrefs 是弹幕设置的落盘形状，对应 state.json 与数据同步文件里的 danmu。
type danmuPrefs struct {
	Font    float32  `json:"font"`
	DurMs   int64    `json:"durationMs"`
	Area    float32  `json:"area"`
	Opacity float32  `json:"opacity"`
	Color   [4]uint8 `json:"color"`
	Stroke  [4]uint8 `json:"stroke"`
	// Colorful 是指针：区分「文件里没写」和「明确关掉」。缺失时保留默认（开），
	// 不能因为一个只写了 font 的 danmu 段就把「彩色弹幕」清零。
	Colorful *bool `json:"colorful,omitempty"`
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
		diag.L().Warn().Str(diag.Event, diag.EvStateSaveFail).
			Str(diag.Stage, "path").Msg("拿不到配置目录，状态不会落盘")
		return persisted{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			diag.L().Warn().Str(diag.Event, diag.EvStateLoadFail).
				Str(diag.Stage, "read").Str("path", filepath.Base(path)).
				Dict(diag.ErrField, diag.Err(err)).Msg("读取状态失败")
		}
		return persisted{}, false
	}
	var p persisted
	if err := json.Unmarshal(data, &p); err != nil {
		// 坏文件会在下一次保存时被默认值覆盖。至少先留下「坏过」的证据，
		// 否则用户报「设置全没了」时日志里什么都没有。
		diag.L().Warn().Str(diag.Event, diag.EvStateLoadFail).
			Str(diag.Stage, "parse").Str("path", filepath.Base(path)).
			Dict(diag.ErrField, diag.Err(err)).Msg("状态文件损坏")
		return persisted{}, false
	}
	return p, true
}

// stateMu 串行化写盘：拖音量时会连着触发几次保存。
var stateMu sync.Mutex

func (a *App) writeState(data []byte) {
	path := statePath()
	if path == "" {
		// 拿不到配置目录：静默返回等于「设置改了却永远不落盘」，用户在下一次
		// 启动时只会看到设置全没了。置旗标让界面下一帧变成一条通知，再留一条
		// 日志。这里在后台 goroutine 上，除了 diag 不能再碰视图状态。
		diag.L().Warn().Str(diag.Event, diag.EvStateSaveFail).
			Str(diag.Stage, "path").Msg("拿不到配置目录，状态不会落盘")
		a.warnStateSave()
		return
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		diag.L().Warn().Str(diag.Event, diag.EvStateSaveFail).
			Str(diag.Stage, "mkdir").Str("path", filepath.Base(path)).
			Dict(diag.ErrField, diag.Err(err)).Msg("建状态目录失败")
		a.warnStateSave()
		return
	}
	// 先写临时文件再 rename：写到一半退出也不会留下半个 JSON。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		diag.L().Warn().Str(diag.Event, diag.EvStateSaveFail).
			Str(diag.Stage, "write").Str("path", filepath.Base(path)).
			Dict(diag.ErrField, diag.Err(err)).Msg("写状态失败")
		a.warnStateSave()
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		diag.L().Warn().Str(diag.Event, diag.EvStateSaveFail).
			Str(diag.Stage, "rename").Str("path", filepath.Base(path)).
			Dict(diag.ErrField, diag.Err(err)).Msg("状态落盘失败")
		a.warnStateSave()
	}
}

// warnStateSave 记一次「设置没能落盘」。
//
// writeState 跑在后台 goroutine，视图数据只能在主线程写：这里只落一个
// 原子旗标，Shell 下一帧取走再变成通知。
func (a *App) warnStateSave() { a.stateSaveFailed.Store(true) }

// markDirty 记一次状态变更，300ms 内的连续变更合并成一次落盘。
//
// 真正的序列化在主线程做（saveState），写文件才放到后台 goroutine。
//
// 没有窗口时（测试、启动早期）只置 dirty，不排定时器：定时器得借窗口的
// Update 回主线程。这一份靠关窗时的 FlushState 兜底落盘。
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
	if data, ok := a.stateSnapshot(); ok {
		go a.writeState(data)
	}
}

// stateSnapshot 在主线程拍一份当前 UI 状态的快照；没有待写的改动时 ok=false。
//
// 顺带把排定中的定时器标记清掉：调用方既然已经把这一份写出去，定时器再来
// 一趟看到 stateDirty 也是空的。
func (a *App) stateSnapshot() ([]byte, bool) {
	a.stateSavePending = false
	if !a.stateDirty {
		return nil, false
	}
	a.stateDirty = false

	follows := append([]FollowRecord(nil), a.Follows...)
	subs := append([]Subscription(nil), a.Subs...)
	watchRooms := make([]watchRecord, 0, len(a.WatchRooms))
	for _, s := range a.WatchRooms {
		watchRooms = append(watchRooms, a.watchRecordOf(s))
	}
	volume := a.DefaultVolume
	panelW := a.panelWidth()
	panelOn := a.DanmuPanelOn
	danmuOn := a.DanmuOn
	// 屏蔽词也要落盘：用户加了一堆词，重启就该还在
	filters := append([]string(nil), a.DanmuFilters...)
	colorful := a.DanmuColorful
	danmu := danmuPrefs{
		Font:     a.DanmuFont,
		DurMs:    a.DanmuDur.Milliseconds(),
		Area:     a.DanmuArea,
		Opacity:  a.DanmuOpacity,
		Color:    colorBytes(a.DanmuColor),
		Stroke:   colorBytes(a.DanmuStroke),
		Colorful: &colorful,
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
		return nil, false
	}
	return data, true
}

// FlushState 立刻把还没落盘的状态写出去，写完才返回（主线程调用）。
//
// 关窗时用（main.go 的 OnClose）：markDirty 的 300ms 合并窗口意味着最后那点
// 改动还停在定时器上，进程一退就丢。这里既不再排定时器，也不能交给后台
// goroutine —— OnClose 返回后进程随时可能结束，异步写来不及。
func (a *App) FlushState() {
	if data, ok := a.stateSnapshot(); ok {
		a.writeState(data)
	}
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
			// 每格音量是墙状态的一部分，和房间一起恢复；没存过的不写，
			// 下次上墙继续继承全局音量。
			if r.Volume != nil {
				a.watch.volumes[watchKey(s)] = *r.Volume
			}
		}
		a.WatchRooms = normalizeWatchRooms(rooms)
	}
	if p.RoomChoices != nil {
		a.roomChoices = p.RoomChoices
	}
	if p.Volume != nil {
		a.DefaultVolume = *p.Volume
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
		// 颜色 / 描边只在文件里真的写了颜色时才覆盖。空段（{"danmu":{}}）
		// 解出来是全零，alpha 也是 0——无条件覆盖会把弹幕变成全透明，
		// 用户看到的是「弹幕开着但一条都不显示」。
		if !zeroColor(p.Danmu.Color) {
			a.DanmuColor = rgbaColor(p.Danmu.Color)
		}
		if !zeroColor(p.Danmu.Stroke) {
			a.DanmuStroke = rgbaColor(p.Danmu.Stroke)
		}
		// 同理：没写 colorful 就保留默认（开），不要把「彩色弹幕」清零。
		if p.Danmu.Colorful != nil {
			a.DanmuColorful = *p.Danmu.Colorful
		}
	}
}

// zeroColor 报告一份 4 字节颜色是不是全零（含 alpha）。
//
// state.json 里没写颜色时解出来就是这个值，所以它同时是「这一项没存过」的
// 判据。
func zeroColor(c [4]uint8) bool { return c == [4]uint8{} }

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
