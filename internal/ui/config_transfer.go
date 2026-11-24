package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
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
	Danmu  fileDanmu  `json:"danmu"`
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
	On       bool     `json:"on"`
	Font     float32  `json:"font"`
	DurMs    int64    `json:"durationMs"`
	Area     float32  `json:"area"`
	Opacity  float32  `json:"opacity"`
	Color    [4]uint8 `json:"color"`
	Stroke   [4]uint8 `json:"stroke"`
	Colorful bool     `json:"colorful"`
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
		Danmu: fileDanmu{
			On:       a.DanmuOn,
			Font:     a.DanmuFont,
			DurMs:    a.DanmuDur.Milliseconds(),
			Area:     a.DanmuArea,
			Opacity:  a.DanmuOpacity,
			Color:    colorBytes(a.DanmuColor),
			Stroke:   colorBytes(a.DanmuStroke),
			Colorful: a.DanmuColorful,
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
type importSummary struct {
	Follows int
	Subs    int
	Watch   int
	Filters int
	Prefs   bool // 主题 / 清晰度 / 音量 / 弹幕这些单值偏好是否被文件覆盖
}

func (s importSummary) String() string {
	parts := make([]string, 0, 5)
	if s.Follows > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 位关注", s.Follows))
	}
	if s.Subs > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 个订阅", s.Subs))
	}
	if s.Watch > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 个观测盒", s.Watch))
	}
	if s.Filters > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 个屏蔽词", s.Filters))
	}
	if s.Prefs {
		parts = append(parts, "更新了主题与播放设置")
	}
	if len(parts) == 0 {
		return "没有新增内容：本机数据已经是最新的"
	}
	return "已导入：" + strings.Join(parts, "，")
}

// followIdentity 是关注去重用的键：平台 + 房间号，房间号缺失时退回昵称。
func followIdentity(platform, roomID, name string) string {
	id := roomID
	if id == "" {
		id = name
	}
	return platform + "|" + id
}

// applyConfig 把导入的配置并进当前状态。
//
// 列表类（关注、订阅、观测墙、屏蔽词）只增不减——导入不该删掉本机的东西；
// 单值偏好（主题、音量、弹幕）以文件为准；清晰度 / 线路按房间合并，文件里
// 有的房间覆盖本机同一间，本机其他房间不动。
func (a *App) applyConfig(f configFile) importSummary {
	var sum importSummary

	seenFollow := make(map[string]bool, len(a.Follows)+len(f.Follows))
	for _, r := range a.Follows {
		seenFollow[followIdentity(r.Platform, r.RoomID, r.Name)] = true
	}
	for _, rec := range f.Follows {
		key := followIdentity(rec.Platform, rec.RoomID, rec.Name)
		if seenFollow[key] {
			continue
		}
		seenFollow[key] = true
		if rec.FollowedAt.IsZero() {
			rec.FollowedAt = time.Now()
		}
		a.Follows = append(a.Follows, rec)
		sum.Follows++
	}

	seenSub := make(map[string]bool, len(a.Subs)+len(f.Subscriptions))
	for _, s := range a.Subs {
		seenSub[s.PlatformID+"|"+s.Group+"|"+s.Name] = true
	}
	for _, s := range f.Subscriptions {
		key := s.PlatformID + "|" + s.Group + "|" + s.Name
		if seenSub[key] {
			continue
		}
		seenSub[key] = true
		if s.Platform == "" {
			s.Platform = platformLabel(s.PlatformID)
		}
		a.Subs = append(a.Subs, s)
		sum.Subs++
	}

	// ---- 观测墙：本机已有的格子保留原位，文件里多出来的追加到末尾；
	// 同一格的音量以文件为准。
	beforeRooms := len(a.WatchRooms)
	for _, r := range f.WatchWall {
		if r.Platform == "" || r.RoomID == "" {
			continue
		}
		key := r.Platform + "|" + r.RoomID
		if !a.watchHas(key) {
			a.WatchRooms = append(a.WatchRooms, r.streamer())
		}
		if r.Volume != nil {
			v := min(1, max(0, *r.Volume))
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
	}
	// normalize 顺带兜住文件里重复的房间和超过上限的部分。
	a.WatchRooms = normalizeWatchRooms(a.WatchRooms)
	sum.Watch = max(0, len(a.WatchRooms)-beforeRooms)

	// ---- 清晰度 / 线路：按房间合并。
	rooms := make(map[string]roomChoice, len(f.Rooms))
	for key, c := range f.Rooms {
		if key == "" || (c.Quality == "" && c.Line == "" && c.LineID == "") {
			continue
		}
		rooms[key] = c
	}
	if len(rooms) > 0 {
		if a.roomChoices == nil {
			a.roomChoices = make(map[string]roomChoice, len(rooms))
		}
		cur := a.media.roomKey()
		beforeCur := a.roomChoiceOf(cur)
		maps.Copy(a.roomChoices, rooms)
		sum.Prefs = true
		// 正在播的就是这一间：把新档位 / 新线路真正接到流上。只写房间记录
		// 的话，播放页芯片显示导入的档位，画面还是旧的那一路。
		if cur != "" && a.roomChoiceOf(cur) != beforeCur {
			a.startStream(true)
		}
	}

	if f.Theme.Palette != "" && theme.Find(f.Theme.Palette) != nil && f.Theme.Palette != a.PaletteID {
		a.PaletteID = f.Theme.Palette
		sum.Prefs = true
	}
	switch f.Theme.Mode {
	case themeModeSystem, themeModeDark, themeModeLight:
		if a.ThemeMode != f.Theme.Mode {
			a.ThemeMode = f.Theme.Mode
			sum.Prefs = true
		}
	}
	if len(f.PlatformOrder) > 0 {
		a.PlatformOrder = normalizePlatformOrder(f.PlatformOrder)
	}
	if len(f.FollowsSections) > 0 {
		a.FollowsSectionOrder = normalizeFollowsSectionOrder(f.FollowsSections)
	}

	if f.Player.Volume != nil {
		// 走 setVolume：墙上正出声的那格同步成导入值，别的格保留自己的。
		a.setVolume(*f.Player.Volume)
		sum.Prefs = true
	}

	// 弹幕段总是整段写出，读回来时逐项覆盖；只要文件里有内容就算改过偏好。
	fileDan := danmuPrefs{
		Font:     f.Danmu.Font,
		DurMs:    f.Danmu.DurMs,
		Area:     f.Danmu.Area,
		Opacity:  f.Danmu.Opacity,
		Color:    f.Danmu.Color,
		Stroke:   f.Danmu.Stroke,
		Colorful: f.Danmu.Colorful,
	}
	if fileDan != (danmuPrefs{}) || f.Danmu.On != a.DanmuOn {
		sum.Prefs = true
	}
	a.DanmuOn = f.Danmu.On
	if f.Danmu.Font > 0 {
		a.DanmuFont = f.Danmu.Font
	}
	if f.Danmu.DurMs > 0 {
		a.DanmuDur = time.Duration(f.Danmu.DurMs) * time.Millisecond
	}
	if f.Danmu.Area > 0 {
		a.DanmuArea = f.Danmu.Area
	}
	if f.Danmu.Opacity > 0 {
		a.DanmuOpacity = f.Danmu.Opacity
	}
	a.DanmuColor = rgbaColor(f.Danmu.Color)
	a.DanmuStroke = rgbaColor(f.Danmu.Stroke)
	a.DanmuColorful = f.Danmu.Colorful

	if len(f.Danmu.Filters) > 0 {
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
			a.DanmuFilters = append(a.DanmuFilters, w)
			sum.Filters++
		}
	}

	return sum
}

// rgbaColor 把文件里的 4 字节颜色装成 ui.Color。
func rgbaColor(c [4]uint8) ui.Color {
	return ui.Color{R: c[0], G: c[1], B: c[2], A: c[3]}
}

// exportConfig 弹保存对话框，把当前配置写成一份 JSON。
//
// 对话框会阻塞到用户选完，所以整段放 goroutine，结果用 win.Update 回到主线程。
func (a *App) exportConfig() {
	if a.SyncBusy || a.Win == nil {
		return
	}
	a.SyncBusy = true
	a.SyncError = ""
	a.SyncNotice = ""
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
			switch {
			case err != nil:
				a.SyncError = "导出失败：" + err.Error()
			case path == "":
				// 用户取消，不提示
			default:
				a.SyncNotice = "已导出：" + filepath.Base(path)
			}
		})
	}()
}

// importConfig 弹打开对话框读一份配置并合并进来。
func (a *App) importConfig() {
	if a.SyncBusy || a.Win == nil {
		return
	}
	a.SyncBusy = true
	a.SyncError = ""
	a.SyncNotice = ""
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
		if err == nil && len(paths) > 0 {
			data, readErr := os.ReadFile(paths[0])
			if readErr != nil {
				err = errors.New("读取文件失败：" + readErr.Error())
			} else {
				parsed, err = parseConfigFile(data)
			}
		}

		win.Update(func() {
			a.SyncBusy = false
			switch {
			case err != nil:
				a.SyncError = err.Error()
			case len(paths) == 0:
				// 用户取消，不提示
			default:
				sum := a.applyConfig(parsed)
				a.SyncNotice = sum.String()
				a.markDirty()
			}
		})
	}()
}
