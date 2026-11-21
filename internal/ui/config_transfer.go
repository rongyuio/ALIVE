package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 数据同步的文件格式。
//
// 导出写成 alive 自己的形状（kind: alive-config）；导入同时接受 DTV
// 的两种文件——dtv-config（导出 JSON）与 dtv-lan-sync（局域网快照）。
// 那两种的 entries 是一组 localStorage 键值，值本身还是 JSON 字符串，
// 所以要多拐一层解析，形状映射也在本文件里。
const (
	configKind     = "alive-config"
	configVersion  = 1
	dtvConfigKind  = "dtv-config"
	dtvLanSyncKind = "dtv-lan-sync"
	configClient   = "ALIVE"
)

// 导入兼容用的 localStorage 键名。
//
// 清晰度和线路的键带平台前缀（BILIBILI_preferred_quality 这种），
// 前缀用大写平台名。
const (
	dtvKeyFollows = "followedStreamers"
	dtvKeySubs    = "dtv_custom_categories_v1"
	dtvKeyDanmu   = "dtv_danmu_preferences_v1"
	dtvKeyTheme   = "theme_preference"
	dtvKeyVolume  = "dtv_player_volume_v1"
	dtvQualSuffix = "_preferred_quality"
	dtvLineSuffix = "_preferred_line"
	dtvPrefixes24 = "DOUYU,DOUYIN,HUYA,BILIBILI"
)

// portableFollow 是关注记录在文件里的形状。
//
// 不直接序列化 FollowRecord：那个结构没有 json tag，落盘键名是 Go 字段名，
// 既不好看也没法和 DTV 的记录对上。
type portableFollow struct {
	Platform   string `json:"platform"`
	RoomID     string `json:"roomId"`
	Name       string `json:"name"`
	Title      string `json:"title,omitempty"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	CoverURL   string `json:"coverUrl,omitempty"`
	Category   string `json:"category,omitempty"`
	Viewers    int    `json:"viewers,omitempty"`
	Live       bool   `json:"live,omitempty"`
	FollowedAt string `json:"followedAt,omitempty"`
}

// portableSub 是订阅分区在文件里的形状。
type portableSub struct {
	PlatformID string `json:"platformId"`
	Platform   string `json:"platform"`
	Group      string `json:"group"`
	Name       string `json:"name"`
	CategoryID string `json:"categoryId,omitempty"`
}

// configData 是文件里真正携带的内容。
//
// 单值偏好用指针，和 persisted 一样区分「文件里没有这一项」与「有，但是空值」。
type configData struct {
	Palette       string   `json:"palette,omitempty"`
	PlatformOrder []string `json:"platformOrder,omitempty"`
	// FollowsSectionOrder 是关注页一级分区的显示顺序
	FollowsSectionOrder []string         `json:"followsSectionOrder,omitempty"`
	Follows             []portableFollow `json:"follows"`
	Subs                []portableSub    `json:"subscriptions"`
	Quality             string           `json:"quality,omitempty"`
	Line                string           `json:"line,omitempty"`
	Volume              *float32         `json:"volume,omitempty"`
	DanmuOn             *bool            `json:"danmuOn,omitempty"`
	Danmu               *danmuPrefs      `json:"danmu,omitempty"`
}

type configSource struct {
	Client     string `json:"client"`
	AppVersion string `json:"appVersion,omitempty"`
}

type configPayload struct {
	Kind       string       `json:"kind"`
	Version    int          `json:"version"`
	ExportedAt string       `json:"exportedAt"`
	Source     configSource `json:"source"`
	Data       configData   `json:"data"`
}

// configFileName 是导出时的默认文件名，带时间戳便于区分多份快照。
func configFileName() string {
	return "ALIVE-config-" + time.Now().Format("20060102-150405") + ".json"
}

// buildConfigPayload 拍一份当前配置的完整快照。
func (a *App) buildConfigPayload() configPayload {
	follows := make([]portableFollow, 0, len(a.Follows))
	for _, r := range a.Follows {
		at := ""
		if !r.FollowedAt.IsZero() {
			at = r.FollowedAt.UTC().Format(time.RFC3339)
		}
		follows = append(follows, portableFollow{
			Platform:   r.Platform,
			RoomID:     r.RoomID,
			Name:       r.Name,
			Title:      r.Title,
			AvatarURL:  r.AvatarURL,
			CoverURL:   r.CoverURL,
			Category:   r.Category,
			Viewers:    r.Viewers,
			Live:       r.Live,
			FollowedAt: at,
		})
	}
	subs := make([]portableSub, 0, len(a.Subs))
	for _, s := range a.Subs {
		subs = append(subs, portableSub(s))
	}
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

	return configPayload{
		Kind:       configKind,
		Version:    configVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Source:     configSource{Client: configClient},
		Data: configData{
			Palette:             a.PaletteID,
			PlatformOrder:       append([]string(nil), a.PlatformOrder...),
			FollowsSectionOrder: a.followsSectionOrder(),
			Follows:             follows,
			Subs:                subs,
			// 清晰度 / 线路属于房间：导出时带走当前这一间的，导入时
			// 也只写回当前这一间。没有当前房间就没有可写的归属。
			Quality: a.qualityFor(a.choiceKey()),
			Line:    a.lineFor(a.choiceKey()),
			Volume:  &volume,
			DanmuOn: &danmuOn,
			Danmu:   &danmu,
		},
	}
}

// parseConfigFile 读一份导入文件。只认得上层的三种 kind，别的直接报错，
// 免得用户拿错文件时被静默合并成空结果。
func parseConfigFile(data []byte) (configData, error) {
	var head struct {
		Kind    string                     `json:"kind"`
		Data    json.RawMessage            `json:"data"`
		Entries map[string]json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return configData{}, errors.New("文件不是有效的 JSON")
	}

	switch head.Kind {
	case configKind:
		var p configPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return configData{}, errors.New("ALIVE 配置文件损坏，读不出内容")
		}
		return p.Data, nil
	case dtvConfigKind, dtvLanSyncKind:
		return configFromDTVEntries(head.Entries), nil
	case "":
		return configData{}, errors.New("文件缺少 kind 字段，不是配置导出文件")
	default:
		return configData{}, fmt.Errorf("不认识的文件类型：%s", head.Kind)
	}
}

// entryText 取出一条 entries 的值。
//
// DTV 的 PortableEntries 是 Record<string, string>，值是被再序列化一次的
// JSON；但手改过的文件可能直接写成对象，两种都接受。
func entryText(raw json.RawMessage) []byte {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []byte(s)
	}
	return raw
}

// configFromDTVEntries 把 DTV 的 localStorage 快照翻译成 alive 的配置。
//
// 只翻译两边都有的东西：关注、订阅、弹幕设置、主题、音量、清晰度与线路。
// 分组、关注排序、Cookie 这些 DTV 独有或 alive 还没做的直接跳过。
func configFromDTVEntries(entries map[string]json.RawMessage) configData {
	var out configData

	// ---- 关注
	if raw, ok := entries[dtvKeyFollows]; ok {
		var list []struct {
			ID            string  `json:"id"`
			CurrentRoomID string  `json:"currentRoomId"`
			Platform      string  `json:"platform"`
			Nickname      string  `json:"nickname"`
			Title         string  `json:"title"`
			RoomTitle     string  `json:"roomTitle"`
			Avatar        string  `json:"avatar"`
			AvatarURL     string  `json:"avatarUrl"`
			Viewers       float64 `json:"viewers"`
			IsLive        bool    `json:"isLive"`
			LiveStatus    string  `json:"liveStatus"`
		}
		if json.Unmarshal(entryText(raw), &list) == nil {
			for _, f := range list {
				platform := normalizePlatformID(f.Platform)
				if platform == "" {
					continue
				}
				roomID := strings.TrimSpace(f.ID)
				if roomID == "" {
					roomID = strings.TrimSpace(f.CurrentRoomID)
				}
				name := strings.TrimSpace(f.Nickname)
				if name == "" && roomID == "" {
					continue
				}
				title := f.Title
				if title == "" {
					title = f.RoomTitle
				}
				avatar := f.Avatar
				if avatar == "" {
					avatar = f.AvatarURL
				}
				out.Follows = append(out.Follows, portableFollow{
					Platform:  platform,
					RoomID:    roomID,
					Name:      name,
					Title:     title,
					AvatarURL: avatar,
					Viewers:   int(f.Viewers),
					Live:      f.IsLive || f.LiveStatus == "LIVE",
				})
			}
		}
	}

	// ---- 订阅
	if raw, ok := entries[dtvKeySubs]; ok {
		var list []struct {
			Platform  string `json:"platform"`
			Cate1Name string `json:"cate1Name"`
			Cate2ID   string `json:"cate2Id"`
			Cate2Name string `json:"cate2Name"`
		}
		if json.Unmarshal(entryText(raw), &list) == nil {
			for _, s := range list {
				platform := normalizePlatformID(s.Platform)
				if platform == "" || s.Cate2Name == "" {
					continue
				}
				out.Subs = append(out.Subs, portableSub{
					PlatformID: platform,
					Platform:   platformLabel(platform),
					Group:      s.Cate1Name,
					Name:       s.Cate2Name,
					CategoryID: s.Cate2ID,
				})
			}
		}
	}

	// ---- 弹幕设置
	if raw, ok := entries[dtvKeyDanmu]; ok {
		var prefs struct {
			Enabled  bool `json:"enabled"`
			Settings struct {
				Color       string  `json:"color"`
				StrokeColor string  `json:"strokeColor"`
				FontSize    string  `json:"fontSize"` // "20px"
				Duration    float64 `json:"duration"`
				Area        float64 `json:"area"`
				Opacity     float64 `json:"opacity"`
				Colorful    bool    `json:"colorful"`
			} `json:"settings"`
		}
		if json.Unmarshal(entryText(raw), &prefs) == nil {
			enabled := prefs.Enabled
			out.DanmuOn = &enabled
			d := danmuPrefs{
				Font:     parsePixelSize(prefs.Settings.FontSize),
				DurMs:    int64(math.Round(prefs.Settings.Duration)),
				Area:     float32(prefs.Settings.Area),
				Opacity:  float32(prefs.Settings.Opacity),
				Color:    parseHexColor(prefs.Settings.Color),
				Stroke:   parseHexColor(prefs.Settings.StrokeColor),
				Colorful: prefs.Settings.Colorful,
			}
			out.Danmu = &d
		}
	}

	// ---- 主题（裸字符串）
	if id := entryString(entries, dtvKeyTheme); id != "" && theme.Find(id) != nil {
		out.Palette = id
	}

	// ---- 音量（裸数字）
	if raw, ok := entries[dtvKeyVolume]; ok {
		text := entryString(entries, dtvKeyVolume)
		if text == "" {
			text = strings.TrimSpace(string(entryText(raw)))
		}
		if v, err := strconv.ParseFloat(text, 32); err == nil {
			f := float32(v)
			out.Volume = &f
		}
	}

	// ---- 清晰度 / 线路：键带平台前缀，取第一个认识的平台
	for platform := range strings.SplitSeq(dtvPrefixes24, ",") {
		if v := entryString(entries, platform+dtvQualSuffix); v != "" {
			out.Quality = v
		}
		if v := entryString(entries, platform+dtvLineSuffix); v != "" {
			out.Line = v
		}
		if out.Quality != "" && out.Line != "" {
			break
		}
	}

	return out
}

// entryString 取一条 entries 里的裸字符串值。
func entryString(entries map[string]json.RawMessage, key string) string {
	raw, ok := entries[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	// DTV 的单值偏好都是裸字符串（loadRaw），但手改过的文件可能多包一层引号
	return strings.Trim(strings.TrimSpace(s), `"`)
}

// normalizePlatformID 把 DTV 的大写平台名归一成 alive 的小写 id。
func normalizePlatformID(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "douyu":
		return "douyu"
	case "huya":
		return "huya"
	case "douyin":
		return "douyin"
	case "bilibili", "bili":
		return "bilibili"
	}
	return ""
}

// parsePixelSize 把 "20px" 这样的字号解析成数值，认不出来就返回 0。
func parsePixelSize(raw string) float32 {
	text := strings.TrimSuffix(strings.TrimSpace(raw), "px")
	v, err := strconv.ParseFloat(text, 32)
	if err != nil || v <= 0 {
		return 0
	}
	return float32(v)
}

// parseHexColor 把 #rrggbb / #rrggbbaa 解析成 RGBA，认不出来就当透明。
func parseHexColor(raw string) [4]uint8 {
	text := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(text) != 6 && len(text) != 8 {
		return [4]uint8{}
	}
	v, err := strconv.ParseUint(text[:6], 16, 32)
	if err != nil {
		return [4]uint8{}
	}
	out := [4]uint8{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
	if len(text) == 8 {
		if a, err := strconv.ParseUint(text[6:], 16, 8); err == nil {
			out[3] = uint8(a)
		}
	}
	return out
}

// importSummary 是一次导入实际并入多少东西。
type importSummary struct {
	Follows int
	Subs    int
	Prefs   bool // 主题 / 清晰度 / 音量 / 弹幕这些单值偏好是否被文件覆盖
}

func (s importSummary) String() string {
	parts := make([]string, 0, 3)
	if s.Follows > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 位关注", s.Follows))
	}
	if s.Subs > 0 {
		parts = append(parts, fmt.Sprintf("新增 %d 个订阅", s.Subs))
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
// 关注和订阅只增不减——导入不该删掉本机已经关注的东西；主题、清晰度、
// 音量、弹幕这些单值偏好按文件内容覆盖。
func (a *App) applyConfig(d configData) importSummary {
	var sum importSummary

	seenFollow := make(map[string]bool, len(a.Follows)+len(d.Follows))
	for _, r := range a.Follows {
		seenFollow[followIdentity(r.Platform, r.RoomID, r.Name)] = true
	}
	for _, f := range d.Follows {
		key := followIdentity(f.Platform, f.RoomID, f.Name)
		if seenFollow[key] {
			continue
		}
		seenFollow[key] = true
		rec := FollowRecord{
			Platform:  f.Platform,
			RoomID:    f.RoomID,
			Name:      f.Name,
			Title:     f.Title,
			AvatarURL: f.AvatarURL,
			CoverURL:  f.CoverURL,
			Category:  f.Category,
			Viewers:   f.Viewers,
			Live:      f.Live,
		}
		if at, err := time.Parse(time.RFC3339, f.FollowedAt); err == nil {
			rec.FollowedAt = at
		} else {
			rec.FollowedAt = time.Now()
		}
		a.Follows = append(a.Follows, rec)
		sum.Follows++
	}

	seenSub := make(map[string]bool, len(a.Subs)+len(d.Subs))
	for _, s := range a.Subs {
		seenSub[s.PlatformID+"|"+s.Group+"|"+s.Name] = true
	}
	for _, s := range d.Subs {
		key := s.PlatformID + "|" + s.Group + "|" + s.Name
		if seenSub[key] {
			continue
		}
		seenSub[key] = true
		platform := s.Platform
		if platform == "" {
			platform = platformLabel(s.PlatformID)
		}
		a.Subs = append(a.Subs, Subscription{
			PlatformID: s.PlatformID,
			Platform:   platform,
			Group:      s.Group,
			Name:       s.Name,
			CategoryID: s.CategoryID,
		})
		sum.Subs++
	}

	if d.Palette != "" && theme.Find(d.Palette) != nil && d.Palette != a.PaletteID {
		a.PaletteID = d.Palette
		sum.Prefs = true
	}
	if len(d.PlatformOrder) > 0 {
		a.PlatformOrder = normalizePlatformOrder(d.PlatformOrder)
	}
	if len(d.FollowsSectionOrder) > 0 {
		a.FollowsSectionOrder = normalizeFollowsSectionOrder(d.FollowsSectionOrder)
	}
	// 清晰度 / 线路按房间存：只有当前这一路能接住导入值，没有房间时
	// 丢掉——把它们塞回全局正是串台的来源。
	if key := a.choiceKey(); key != "" {
		if d.Quality != "" {
			a.setRoomQuality(key, d.Quality)
			sum.Prefs = true
		}
		if d.Line != "" {
			a.setRoomLine(key, d.Line, "")
			sum.Prefs = true
		}
	}
	if d.Volume != nil {
		// 走 setVolume：墙上正出声的那格同步成导入值，别的格保留自己的。
		a.setVolume(*d.Volume)
		sum.Prefs = true
	}
	if d.DanmuOn != nil {
		a.DanmuOn = *d.DanmuOn
		sum.Prefs = true
	}
	if d.Danmu != nil {
		if d.Danmu.Font > 0 {
			a.DanmuFont = d.Danmu.Font
		}
		if d.Danmu.DurMs > 0 {
			a.DanmuDur = time.Duration(d.Danmu.DurMs) * time.Millisecond
		}
		if d.Danmu.Area > 0 {
			a.DanmuArea = d.Danmu.Area
		}
		if d.Danmu.Opacity > 0 {
			a.DanmuOpacity = d.Danmu.Opacity
		}
		a.DanmuColor = rgbaColor(d.Danmu.Color)
		a.DanmuStroke = rgbaColor(d.Danmu.Stroke)
		a.DanmuColorful = d.Danmu.Colorful
		sum.Prefs = true
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
	payload := a.buildConfigPayload()

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

		var parsed configData
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
