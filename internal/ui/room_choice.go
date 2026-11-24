package ui

import "alive/internal/live"

// roomChoice 是某个直播间自己的播放选择。
//
// 清晰度和线路都属于房间，不属于应用：同一平台不同房间能选的档位可能不同，
// 跨平台更是两套完全不同的名字（B站 主线/备线、虎牙 CDN 代号、斗鱼 蓝光4M）。
// 放进全局字段里，切房间和从观测墙接管时就只能靠一段「校正」把上一个平台
// 的名字擦掉，漏一处就是串台——这个 bug 已经发生过一次。
type roomChoice struct {
	Quality string `json:"quality,omitempty"`
	Line    string `json:"line,omitempty"`
	// LineID 是线路在平台里的节点 id。标签只用于显示，取流要 id；
	// 而房间自己的 stream 可能已经不在手上（切台、从墙上接管之后）。
	LineID string `json:"lineId,omitempty"`
}

// choiceKey 是「当前这一路」的房间键：播放页用媒体层记下的 key，还没起播
// 时退回当前选中的直播间。拿不到房间时返回空串，调用方跳过读写。
func (a *App) choiceKey() string {
	if a.media.roomKey() != "" {
		return a.media.roomKey()
	}
	s := a.current()
	if s.Platform == "" || s.RoomID == "" {
		return ""
	}
	return watchKey(s)
}

func (a *App) roomChoiceOf(key string) roomChoice {
	if a.roomChoices == nil {
		return roomChoice{}
	}
	return a.roomChoices[key]
}

func (a *App) qualityFor(key string) string { return a.roomChoiceOf(key).Quality }

func (a *App) lineFor(key string) string { return a.roomChoiceOf(key).Line }

// setRoomChoice 写一个房间的选择；三项都空就删掉记录，状态文件里不留空壳。
func (a *App) setRoomChoice(key string, c roomChoice) {
	if key == "" {
		return
	}
	if c.Quality == "" && c.Line == "" && c.LineID == "" {
		delete(a.roomChoices, key)
	} else {
		if a.roomChoices == nil {
			a.roomChoices = make(map[string]roomChoice)
		}
		a.roomChoices[key] = c
	}
	a.markDirty()
}

func (a *App) setRoomQuality(key, label string) {
	c := a.roomChoiceOf(key)
	c.Quality = label
	a.setRoomChoice(key, c)
}

func (a *App) setRoomLine(key, label, id string) {
	c := a.roomChoiceOf(key)
	c.Line, c.LineID = label, id
	a.setRoomChoice(key, c)
}

// lineIDFor 把房间记下的线路标签换成平台节点 id。
//
// stream 是这个房间已经到手的那份档位表（可能没有）。标签命不中时用记下的
// id；房间第一次打开时两者都空，交给平台的默认线路。
func (a *App) lineIDFor(key string, stream live.Stream) string {
	c := a.roomChoiceOf(key)
	if c.Line == "" {
		return ""
	}
	for _, l := range stream.Lines {
		if l.Label == c.Line {
			return l.ID
		}
	}
	return c.LineID
}

// learnRoomChoice 用平台这一轮的返回校正房间自己的选择。
//
// 平台可能给不了所点的档位（斗鱼会把「原画」降成「蓝光4M」），也可能没有
// 线路概念；校正只写回这一间房，别的房间原样不动。
func (a *App) learnRoomChoice(key string, stream live.Stream, up live.Variant) {
	if key == "" {
		return
	}
	c := a.roomChoiceOf(key)
	switch {
	// 平台确认了实际档位就听它的：请求「原画」但服务端只给了「蓝光」时，
	// 菜单必须显示真正在播的那一档，不能让标签撒谎。
	case up.URL != "" && (up.Label == "" || up.ID == ""):
		c.Quality = "默认"
	case up.Label != "":
		c.Quality = up.Label
	case len(stream.Variants) > 0 && !hasVariantLabel(stream.Variants, c.Quality):
		c.Quality = defaultVariantLabel(stream.Variants)
	}
	switch {
	// 单线路的平台（抖音）根本没有线路概念，控件整块不出现。
	case len(stream.Lines) == 0:
		c.Line, c.LineID = "", ""
	case !hasLineLabel(stream.Lines, c.Line):
		c.Line, c.LineID = stream.Lines[0].Label, stream.Lines[0].ID
	default:
		c.LineID = lineIDIn(stream.Lines, c.Line)
	}
	a.setRoomChoice(key, c)
}

// lineIDForLabel 把界面上的线路标签换成节点 id。stream 是这个房间已经
// 到手的档位表（可能为空）；标签不在这份表里时退回房间记下的 id。自动
// 换线用的是临时标签，不走 roomChoice，所以单独一个入口。
func (a *App) lineIDForLabel(key string, stream live.Stream, label string) string {
	if label == "" {
		return ""
	}
	if id := lineIDIn(stream.Lines, label); id != "" {
		return id
	}
	c := a.roomChoiceOf(key)
	if c.Line == label {
		return c.LineID
	}
	return ""
}

func lineIDIn(lines []live.Line, label string) string {
	for _, l := range lines {
		if l.Label == label {
			return l.ID
		}
	}
	return ""
}
