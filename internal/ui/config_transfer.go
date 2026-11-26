package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
	"alive/internal/watch"
)

// 数据同步文件。
//
// 顶层直接是我们的领域划分——主题、平台顺序、关注、订阅、观测墙、房间播放
// 选择、播放器、弹幕。没有 kind / version / source / data 这类包一层的信封，
// 也没有版本号：软件还没发过版，不存在要按版本拒绝的旧文件；字段对不上时
// 解析会直接失败，不会悄悄丢配置。
//
// 文件头只留一个 app，用来在用户拿错文件时给出明确报错。其余字段全部对应
// 我们自己 state.json 里的东西，没有一个是外部客户端的形状。
const configApp = "ALIVE"

type configFile struct {
	App        string `json:"app"`
	ExportedAt string `json:"exportedAt"`

	Theme fileTheme `json:"theme"`

	// PlatformOrder 是顶栏平台的显示顺序。
	PlatformOrder []string `json:"platformOrder,omitempty"`
	// FollowsSections 是关注页一级分区的显示顺序。
	FollowsSections []string `json:"followsSections,omitempty"`

	Follows       []FollowRecord `json:"follows"`
	Subscriptions []Subscription `json:"subscriptions"`

	// WatchWall 是观测墙上的格子，和 state.json 里同一个形状。空墙也写成 []，
	// 让文件的结构固定。
	WatchWall []watchRecord `json:"watchWall"`

	// Rooms 是每个直播间自己的清晰度 / 线路，键是 platform|roomID。
	// 房间键跨机器不变，所以整张表都能带走。
	Rooms map[string]roomChoice `json:"rooms"`

	Player filePlayer `json:"player"`
	// Danmu 是指针：区分「文件里没有弹幕段」和「写了一段弹幕设置」。
	// 值类型时缺段解出来是全零，导入会把开关关掉、颜色清成全透明。
	Danmu *fileDanmu `json:"danmu"`
}

type fileTheme struct {
	Palette string `json:"palette,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type filePlayer struct {
	// Volume 是指针：区分「文件里没写」和「写了个 0」（静音）。
	Volume *float32 `json:"volume,omitempty"`
}

// fileDanmu 把弹幕开关、外观和屏蔽词收在同一段里。
//
// 不内嵌 danmuPrefs：匿名内嵌的字段名是小写的 danmuPrefs，encoding/json
// 会把它当成未导出字段整个跳过，导出时这段会变成空对象。
type fileDanmu struct {
	On      bool     `json:"on"`
	Font    float32  `json:"font"`
	DurMs   int64    `json:"durationMs"`
	Area    float32  `json:"area"`
	Opacity float32  `json:"opacity"`
	Color   [4]uint8 `json:"color"`
	Stroke  [4]uint8 `json:"stroke"`
	// Colorful 是指针：文件里没写「彩色弹幕」时不该把用户的开关关掉。
	Colorful *bool    `json:"colorful,omitempty"`
	Filters  []string `json:"filters"`
}

// configFileName 是导出时的默认文件名，带时间戳便于区分多份快照。
func configFileName() string {
	return "ALIVE-" + time.Now().Format("20060102-150405") + ".json"
}

// buildConfigFile 拍一份当前配置的完整快照。
func (a *App) buildConfigFile() configFile {
	follows := append([]FollowRecord(nil), a.Follows...)
	subs := append([]Subscription(nil), a.Subs...)
	colorful := a.DanmuColorful

	wall := make([]watchRecord, 0, len(a.WatchRooms))
	for _, s := range a.WatchRooms {
		wall = append(wall, a.watchRecordOf(s))
	}

	// 拷贝一份：payload 会交给后台 goroutine 序列化，不能和主线程共用一张表。
	rooms := make(map[string]roomChoice, len(a.roomChoices))
	maps.Copy(rooms, a.roomChoices)

	volume := a.DefaultVolume
	return configFile{
		App:        configApp,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Theme: fileTheme{
			Palette: a.PaletteID,
			Mode:    a.ThemeMode,
		},
		PlatformOrder:   append([]string(nil), a.PlatformOrder...),
		FollowsSections: a.followsSectionOrder(),
		Follows:         follows,
		Subscriptions:   subs,
		WatchWall:       wall,
		Rooms:           rooms,
		Player:          filePlayer{Volume: &volume},
		Danmu: &fileDanmu{
			On:       a.DanmuOn,
			Font:     a.DanmuFont,
			DurMs:    a.DanmuDur.Milliseconds(),
			Area:     a.DanmuArea,
			Opacity:  a.DanmuOpacity,
			Color:    colorBytes(a.DanmuColor),
			Stroke:   colorBytes(a.DanmuStroke),
			Colorful: &colorful,
			Filters:  append([]string{}, a.DanmuFilters...),
		},
	}
}

// parseConfigFile 读一份导入文件。
//
// 只认 app 是 ALIVE 的文件：拿错文件时明确报错，不能被静默合并成空结果。
func parseConfigFile(data []byte) (configFile, error) {
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return configFile{}, errors.New("文件不是有效的 JSON：该字段类型对不上，或者数字超出范围")
	}
	if f.App != configApp {
		return configFile{}, errors.New("不是 ALIVE 导出的配置文件")
	}
	return f, nil
}

// importSummary 是一次导入实际并入多少东西。
//
// 只数列表类条目；单值偏好改了几项由 importPlan.Overwrites 记录，结果提
// 示也走那份清单，不再在这里数第二遍。
type importSummary struct {
	Follows int
	Subs    int
	Watch   int
	Filters int
}

// importPlan 是一份文件会带来的全部改动。
//
// 「选文件 → 看改动 → 确认」三步里，预览和落地共用这一份清单：
// applyImportPlan 只按清单写，不再回头读文件。用户看到「新增 3 位关注」，
// 确认后加进去的就是那三条；两边不会各算各的。
//
// 清单里的列表只收本机没有的，单值偏好只在确实会变的时候才有值。
// Adds / Overwrites 是给界面看的文案，也在选文件时一次算好，渲染路径不算数。
type importPlan struct {
	FileName string

	Follows []FollowRecord
	Subs    []Subscription
	Watch   []watchRecord
	Filters []string

	// Rooms 是本机没有或取值不同的房间记录；WallVolumes 同理，只收会变的格。
	Rooms       map[string]roomChoice
	WallVolumes map[string]float32

	PaletteID string   // "" = 不变
	ThemeMode string   // "" = 不变
	Volume    *float32 // nil = 不变

	PlatformOrder []string // nil = 不变
	Sections      []string // nil = 不变

	Danmu        danmuAfter
	DanmuChanged bool

	Adds       string
	Overwrites []string
}

// empty 表示这份文件对本机没有任何影响。
func (p importPlan) empty() bool {
	return p.Adds == "" && len(p.Overwrites) == 0
}

func (p importPlan) summary() importSummary {
	return importSummary{
		Follows: len(p.Follows),
		Subs:    len(p.Subs),
		Watch:   len(p.Watch),
		Filters: len(p.Filters),
	}
}

// notice 是导入完成后的结果文案。
//
// 只留「新增了什么、覆盖了几项」：具体覆盖了哪几项，确认前的清单里已经
// 逐条列过，这里再抄一遍只是让人重读一遍长句。
func (p importPlan) notice() string {
	parts := make([]string, 0, 2)
	if p.Adds != "" {
		parts = append(parts, "新增 "+p.Adds)
	}
	if n := len(p.Overwrites); n > 0 {
		parts = append(parts, fmt.Sprintf("覆盖 %d 项设置", n))
	}
	if len(parts) == 0 {
		return "没有可导入的内容：本机数据已经是最新的"
	}
	return "已导入：" + strings.Join(parts, "；")
}

// danmuAfter 是弹幕设置的一组具体取值。字段都可比较：预览拿它和本机现状
// 逐项比，落地时也按它写。
type danmuAfter struct {
	On       bool
	Font     float32
	Dur      time.Duration
	Area     float32
	Opacity  float32
	Color    [4]uint8
	Stroke   [4]uint8
	Colorful bool
}

func (a *App) danmuState() danmuAfter {
	return danmuAfter{
		On:       a.DanmuOn,
		Font:     a.DanmuFont,
		Dur:      a.DanmuDur,
		Area:     a.DanmuArea,
		Opacity:  a.DanmuOpacity,
		Color:    colorBytes(a.DanmuColor),
		Stroke:   colorBytes(a.DanmuStroke),
		Colorful: a.DanmuColorful,
	}
}

// planImport 算出这份文件会带来的全部改动，不改本机任何状态。
//
// 合并规则和 applyImportPlan 一一对应，两边都从这里出发：
//   - 关注、订阅、观测墙、屏蔽词只加不删，按本机现状去重；
//   - 观测墙受 watch.MaxRooms 限制，放不下的房间不算「新增」；
//   - 房间清晰度 / 线路按房间覆盖，本机别的房间不动；
//   - 主题、音量、弹幕以文件为准，只在取值确实不同时列进覆盖项。
func (a *App) planImport(name string, f configFile) importPlan {
	p := importPlan{FileName: name}

	seenFollow := make(map[string]bool, len(a.Follows)+len(f.Follows))
	for _, r := range a.Follows {
		seenFollow[followIdentity(r.Platform, r.RoomID, r.Name)] = true
	}
	for _, rec := range f.Follows {
		// 空平台 / 空昵称的记录认不出是谁，落地后既画不出来也删不掉
		//（sameFollow 先比平台），只会变成一条幽灵关注。
		if rec.Platform == "" || rec.Name == "" {
			continue
		}
		key := followIdentity(rec.Platform, rec.RoomID, rec.Name)
		if seenFollow[key] {
			continue
		}
		seenFollow[key] = true
		p.Follows = append(p.Follows, rec)
	}

	seenSub := make(map[string]bool, len(a.Subs)+len(f.Subscriptions))
	for _, s := range a.Subs {
		seenSub[s.PlatformID+"|"+s.Group+"|"+s.Name] = true
	}
	for _, s := range f.Subscriptions {
		// 订阅同样要有平台和分区名：拉列表要用 PlatformID，chip 显示要用
		// Name，缺一个就是一枚点了没反应的空 chip。
		if s.PlatformID == "" || s.Name == "" {
			continue
		}
		key := s.PlatformID + "|" + s.Group + "|" + s.Name
		if seenSub[key] {
			continue
		}
		seenSub[key] = true
		p.Subs = append(p.Subs, s)
	}

	// 观测墙：本机已有的不算新增；到上限就停，和落地后 normalizeWatchRooms
	// 截断的位置一致——预览说加不进去的房间，确实加不进去。
	wallKeys := make(map[string]bool, len(a.WatchRooms)+len(f.WatchWall))
	for _, s := range a.WatchRooms {
		wallKeys[watchKey(s)] = true
	}
	wallLen := len(a.WatchRooms)
	for _, r := range f.WatchWall {
		if r.Platform == "" || r.RoomID == "" {
			continue
		}
		key := r.Platform + "|" + r.RoomID
		if wallKeys[key] {
			continue
		}
		if wallLen >= watch.MaxRooms {
			break
		}
		wallKeys[key] = true
		wallLen++
		p.Watch = append(p.Watch, r)
	}

	for _, r := range f.WatchWall {
		if r.Platform == "" || r.RoomID == "" || r.Volume == nil {
			continue
		}
		key := r.Platform + "|" + r.RoomID
		v := min(1, max(0, *r.Volume))
		if math.Abs(float64(v-a.watchVolume(key))) < 0.001 {
			continue
		}
		if p.WallVolumes == nil {
			p.WallVolumes = make(map[string]float32)
		}
		p.WallVolumes[key] = v
	}

	for key, c := range f.Rooms {
		if key == "" || (c.Quality == "" && c.Line == "" && c.LineID == "") {
			continue
		}
		if a.roomChoiceOf(key) == c {
			continue
		}
		if p.Rooms == nil {
			p.Rooms = make(map[string]roomChoice)
		}
		p.Rooms[key] = c
	}

	if f.Theme.Palette != "" {
		if pal := theme.Find(f.Theme.Palette); pal != nil && pal.ID != a.PaletteID {
			p.PaletteID = f.Theme.Palette
		}
	}
	switch f.Theme.Mode {
	case themeModeSystem, themeModeDark, themeModeLight:
		if f.Theme.Mode != a.ThemeMode {
			p.ThemeMode = f.Theme.Mode
		}
	}
	if len(f.PlatformOrder) > 0 {
		if order := normalizePlatformOrder(f.PlatformOrder); !slices.Equal(order, normalizePlatformOrder(a.PlatformOrder)) {
			p.PlatformOrder = order
		}
	}
	if len(f.FollowsSections) > 0 {
		if order := normalizeFollowsSectionOrder(f.FollowsSections); !slices.Equal(order, a.followsSectionOrder()) {
			p.Sections = order
		}
	}

	if f.Player.Volume != nil {
		v := min(1, max(0, *f.Player.Volume))
		if math.Abs(float64(v-a.focusVolume())) >= 0.001 {
			p.Volume = &v
		}
	}

	// 弹幕段总是整段导出，读回来时逐项覆盖：开关、颜色、多彩直接取文件，
	// 字号 / 停留 / 区域 / 不透明度只认文件里的正数（0 当没写）。
	//
	// 文件里没有 danmu 段（指针为 nil）就是「不改弹幕」：以前它是值类型，
	// 缺段解出来是一份全零的弹幕设置，导入会把开关关掉、颜色清成透明。
	before := a.danmuState()
	after := before
	if f.Danmu != nil {
		after.On = f.Danmu.On
		if f.Danmu.Font > 0 {
			after.Font = f.Danmu.Font
		}
		if f.Danmu.DurMs > 0 {
			after.Dur = time.Duration(f.Danmu.DurMs) * time.Millisecond
		}
		if f.Danmu.Area > 0 {
			after.Area = f.Danmu.Area
		}
		if f.Danmu.Opacity > 0 {
			after.Opacity = f.Danmu.Opacity
		}
		after.Color = f.Danmu.Color
		after.Stroke = f.Danmu.Stroke
		if f.Danmu.Colorful != nil {
			after.Colorful = *f.Danmu.Colorful
		}
	}
	p.Danmu = after
	p.DanmuChanged = after != before

	if f.Danmu != nil && len(f.Danmu.Filters) > 0 {
		seen := make(map[string]bool, len(a.DanmuFilters)+len(f.Danmu.Filters))
		for _, w := range a.DanmuFilters {
			seen[w] = true
		}
		for _, w := range f.Danmu.Filters {
			w = strings.TrimSpace(w)
			if w == "" || seen[w] {
				continue
			}
			seen[w] = true
			p.Filters = append(p.Filters, w)
		}
	}

	p.Adds, p.Overwrites = a.planLines(&p, before)
	return p
}

// planLines 把计划翻成两段人话：新增概览和逐条覆盖说明。
func (a *App) planLines(p *importPlan, before danmuAfter) (adds string, overwrites []string) {
	var parts []string
	if n := len(p.Follows); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 位关注", n))
	}
	if n := len(p.Subs); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 个订阅", n))
	}
	if n := len(p.Watch); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 格观测墙", n))
	}
	if n := len(p.Filters); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 个屏蔽词", n))
	}
	adds = strings.Join(parts, " · ")

	if p.PaletteID != "" {
		overwrites = append(overwrites, fmt.Sprintf("配色 %s → %s",
			paletteName(a.PaletteID), paletteName(p.PaletteID)))
	}
	if p.ThemeMode != "" {
		overwrites = append(overwrites, fmt.Sprintf("明暗 %s → %s",
			themeModeName(a.ThemeMode), themeModeName(p.ThemeMode)))
	}
	if n := len(p.Rooms); n > 0 {
		overwrites = append(overwrites, fmt.Sprintf("%d 间房的清晰度与线路", n))
	}
	if n := len(p.WallVolumes); n > 0 {
		overwrites = append(overwrites, fmt.Sprintf("%d 格观测墙的音量", n))
	}
	if p.Volume != nil {
		overwrites = append(overwrites, fmt.Sprintf("音量 %d%% → %d%%",
			percentOf(a.focusVolume()), percentOf(*p.Volume)))
	}
	if p.PlatformOrder != nil || p.Sections != nil {
		overwrites = append(overwrites, "顶栏与关注分区顺序")
	}
	if d := danmuChanges(before, p.Danmu); len(d) > 0 {
		if len(d) > 3 {
			d = append(d[:3:3], fmt.Sprintf("等 %d 项", len(d)))
		}
		overwrites = append(overwrites, "弹幕 "+strings.Join(d, "、"))
	}
	return adds, overwrites
}

// danmuChanges 列出两套弹幕设置之间的差异，按「先开关、再外观」排。
func danmuChanges(before, after danmuAfter) []string {
	var out []string
	if before.On != after.On {
		out = append(out, fmt.Sprintf("弹幕 %s → %s", onOff(before.On), onOff(after.On)))
	}
	if before.Font != after.Font {
		out = append(out, fmt.Sprintf("字号 %g → %g", before.Font, after.Font))
	}
	if before.Dur != after.Dur {
		out = append(out, fmt.Sprintf("停留 %d 秒 → %d 秒", int(before.Dur.Seconds()), int(after.Dur.Seconds())))
	}
	if before.Area != after.Area {
		out = append(out, fmt.Sprintf("显示区域 %d%% → %d%%", percentOf(before.Area), percentOf(after.Area)))
	}
	if before.Opacity != after.Opacity {
		out = append(out, fmt.Sprintf("不透明度 %d%% → %d%%", percentOf(before.Opacity), percentOf(after.Opacity)))
	}
	if before.Color != after.Color {
		out = append(out, fmt.Sprintf("颜色 %s → %s", hexColor(before.Color), hexColor(after.Color)))
	}
	if before.Stroke != after.Stroke {
		out = append(out, fmt.Sprintf("描边 %s → %s", hexColor(before.Stroke), hexColor(after.Stroke)))
	}
	if before.Colorful != after.Colorful {
		out = append(out, fmt.Sprintf("多彩弹幕 %s → %s", onOff(before.Colorful), onOff(after.Colorful)))
	}
	return out
}

func onOff(on bool) string {
	if on {
		return "开"
	}
	return "关"
}

func percentOf(v float32) int {
	return int(math.Round(float64(v) * 100))
}

func hexColor(c [4]uint8) string {
	return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2])
}

func paletteName(id string) string {
	if p := theme.Find(id); p != nil {
		return p.Name
	}
	return id
}

func themeModeName(mode string) string {
	switch mode {
	case themeModeDark:
		return "夜"
	case themeModeLight:
		return "日"
	default:
		return "跟随系统"
	}
}

// followIdentity 是关注去重用的键：平台 + 房间号，房间号缺失时退回昵称。
func followIdentity(platform, roomID, name string) string {
	id := roomID
	if id == "" {
		id = name
	}
	return platform + "|" + id
}

// applyImportPlan 按预览时算好的清单落地。用户确认的就是这份清单。
func (a *App) applyImportPlan(p importPlan) importSummary {
	for _, rec := range p.Follows {
		if rec.FollowedAt.IsZero() {
			rec.FollowedAt = time.Now()
		}
		a.Follows = append(a.Follows, rec)
	}

	for _, s := range p.Subs {
		if s.Platform == "" {
			s.Platform = platformLabel(s.PlatformID)
		}
		a.Subs = append(a.Subs, s)
	}

	if len(p.Watch) > 0 {
		for _, r := range p.Watch {
			a.WatchRooms = append(a.WatchRooms, r.streamer())
		}
		// normalize 顺带兜住重复和上限，和 planImport 的截断一致。
		a.WatchRooms = normalizeWatchRooms(a.WatchRooms)
	}

	for key, v := range p.WallVolumes {
		// 会话在时音量住在会话里；冷存只给还没有会话的房间。
		if s := a.watch.sessions[key]; s != nil {
			s.volume = v
			if key == a.focusKey() {
				a.DefaultVolume = v
				a.applyFocusAudio()
			}
		} else {
			a.watch.init()
			a.watch.volumes[key] = v
		}
	}

	if len(p.Rooms) > 0 {
		if a.roomChoices == nil {
			a.roomChoices = make(map[string]roomChoice, len(p.Rooms))
		}
		cur := a.media.roomKey()
		beforeCur := a.roomChoiceOf(cur)
		maps.Copy(a.roomChoices, p.Rooms)
		// 正在播的就是这一间：把新档位 / 新线路真正接到流上。只写房间记录
		// 的话，播放页芯片显示导入的档位，画面还是旧的那一路。
		if cur != "" && a.roomChoiceOf(cur) != beforeCur {
			a.startStream(true)
		}
	}

	if p.PaletteID != "" {
		a.PaletteID = p.PaletteID
	}
	if p.ThemeMode != "" {
		a.ThemeMode = p.ThemeMode
	}
	if p.PlatformOrder != nil {
		a.PlatformOrder = p.PlatformOrder
	}
	if p.Sections != nil {
		a.FollowsSectionOrder = p.Sections
	}
	if p.Volume != nil {
		// 走 setVolume：墙上正出声的那格同步成导入值，别的格保留自己的。
		a.setVolume(*p.Volume)
	}

	if p.DanmuChanged {
		d := p.Danmu
		a.DanmuOn = d.On
		a.DanmuFont = d.Font
		a.DanmuDur = d.Dur
		a.DanmuArea = d.Area
		a.DanmuOpacity = d.Opacity
		a.DanmuColor = rgbaColor(d.Color)
		a.DanmuStroke = rgbaColor(d.Stroke)
		a.DanmuColorful = d.Colorful
	}
	a.DanmuFilters = append(a.DanmuFilters, p.Filters...)

	return p.summary()
}

// applyConfig 是「先算清单、再落地」的便捷入口：测试和一次性合并走这里。
func (a *App) applyConfig(f configFile) importSummary {
	return a.applyImportPlan(a.planImport("", f))
}

// rgbaColor 把文件里的 4 字节颜色装成 ui.Color。
func rgbaColor(c [4]uint8) ui.Color {
	return ui.Color{R: c[0], G: c[1], B: c[2], A: c[3]}
}

// configCounts 是导出页要展示的本机条目数。
type configCounts struct {
	Follows int
	Subs    int
	Watch   int
	Filters int
}

func (a *App) configCounts() configCounts {
	return configCounts{
		Follows: len(a.Follows),
		Subs:    len(a.Subs),
		Watch:   len(a.WatchRooms),
		Filters: len(a.DanmuFilters),
	}
}

// exportConfig 弹保存对话框，把当前配置写成一份 JSON。
//
// 对话框会阻塞到用户选完，所以整段放 goroutine，结果用 win.Update 回到主线程。
func (a *App) exportConfig() {
	if a.SyncBusy || a.Win == nil {
		return
	}
	a.SyncBusy = true
	a.SyncBusyAction = syncActExport
	a.SyncError = ""
	a.SyncNotice = ""
	a.SyncPath = ""
	win := a.Win
	payload := a.buildConfigFile()

	go func() {
		data, err := json.MarshalIndent(payload, "", "  ")
		if err == nil {
			data = append(data, '\n')
		}
		path := ""
		if err == nil {
			path, err = mygo.Dialog.Save(mygo.SaveDialogOptions{
				Parent:      win,
				Title:       "导出数据",
				DefaultPath: configFileName(),
				ButtonLabel: "导出",
				Filters: []mygo.FileFilter{
					{Name: "JSON", Extensions: []string{"json"}},
				},
			})
		}
		if err == nil && path != "" {
			err = os.WriteFile(path, data, 0o644)
		}

		win.Update(func() {
			a.SyncBusy = false
			a.SyncBusyAction = ""
			switch {
			case err != nil:
				a.SyncError = "导出失败：" + err.Error()
			case path == "":
				// 用户取消，不提示
			default:
				a.SyncPath = path
				a.SyncNotice = "已导出：" + filepath.Base(path)
			}
		})
	}()
}

// maxConfigFileSize 是导入文件的大小上限（8 MB）。
//
// 一份配置就是几百条关注 / 订阅加上几十条房间记录，实测最大几十 KB；8 MB
// 已经宽松了两个数量级，再大的文件只可能是选错了。
const maxConfigFileSize = 8 << 20

// readConfigFile 读一份导入文件，超过上限直接拒绝。
//
// 不设上限的话，用户手滑选了一个几百 MB 的文件，os.ReadFile 会先把它整个
// 读进内存再解析——窗口没有任何提示就被撑住。
func readConfigFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("读取文件失败：" + err.Error())
	}
	if info.Size() > maxConfigFileSize {
		return nil, fmt.Errorf("文件太大（%d MB），配置文件上限是 %d MB",
			(info.Size()+1<<20-1)>>20, maxConfigFileSize>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("读取文件失败：" + err.Error())
	}
	return data, nil
}

// pickImportFile 弹打开对话框读一份配置，算好改动清单等用户确认。
//
// 选完不直接落地：文件会覆盖主题、音量、弹幕这些本机正在用的值，先把
// 「新增什么、覆盖什么」摆出来，用户点确认才写。
func (a *App) pickImportFile() {
	if a.SyncBusy || a.Win == nil {
		return
	}
	a.SyncBusy = true
	a.SyncBusyAction = syncActImport
	a.SyncError = ""
	a.SyncNotice = ""
	a.SyncPath = ""
	a.SyncPlan = nil
	win := a.Win

	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: win,
			Title:  "导入数据",
			Filters: []mygo.FileFilter{
				{Name: "JSON", Extensions: []string{"json"}},
			},
		})

		var parsed configFile
		name := ""
		if err == nil && len(paths) > 0 {
			data, readErr := readConfigFile(paths[0])
			if readErr != nil {
				err = readErr
			} else {
				parsed, err = parseConfigFile(data)
				name = filepath.Base(paths[0])
			}
		}

		win.Update(func() {
			a.SyncBusy = false
			a.SyncBusyAction = ""
			switch {
			case err != nil:
				a.SyncError = err.Error()
			case len(paths) == 0:
				// 用户取消，不提示
			default:
				plan := a.planImport(name, parsed)
				a.SyncPlan = &plan
			}
		})
	}()
}

// confirmImport 把预览里列出的改动写进本机。
func (a *App) confirmImport() {
	plan := a.SyncPlan
	if a.SyncBusy || plan == nil {
		return
	}
	a.SyncPlan = nil
	a.applyImportPlan(*plan)
	a.SyncNotice = plan.notice()
	a.SyncCounts = a.configCounts()
	a.markDirty()
}

// cancelImport 丢掉待确认的导入，本机不动。
func (a *App) cancelImport() {
	a.SyncPlan = nil
	a.SyncError = ""
}
