package live

import (
	_ "embed"
	"strings"
	"sync"
)

// 平台表情表。
//
// 协议里的正文只带占位符（B 站的 [偷笑]、斗鱼的 [财神]、虎牙的 [大哭]、
// 抖音的 [捂脸]），表情图从哪来看平台：
//
//   - B 站：随消息下发（info[0][15].extra.emots），URL 在 Danmu.Emotes 里，
//     这张表不参与；
//   - 斗鱼：网页端把表情包内置在页面资源里，这张表是 2026-11-17 从表情
//     面板 8 个页签导出的 265 个基础表情（assets/douyu/emotes.tsv）；
//   - 抖音：图片地址由前端表情表提供，已内置（emotes_douyin.go）；
//   - 虎牙：表情包走平台接口（Wup getExpressionEmoticonPackage），
//     拉到后进 runtimeEmotes，不写死在代码里。
//
// 主播专属表情和装扮表情不在基础表里——它们按房间 / 用户变化，要靠平台
// 接口异步补齐；查不到时按原文渲染。
type emoteTable map[string]string

const douyuEmoteBase = "https://shark2.douyucdn.cn/front-publish/live-next-player-aside-master/assets/images/"

// douyuEmotes 是斗鱼基础表情：名称（带方括号）→ 文件名（不含 .png）。
//
// 数据在 assets/douyu/emotes.tsv：2026-11-17 从网页端表情面板的 8 个页签
// 逐项导出，265 条。之前只抄了第一个页签的 57 条，后面页签里的 [开车]、
// [调戏] 这类表情明明在面板里，弹幕里却只剩方括号文字。
//
//go:embed assets/douyu/emotes.tsv
var douyuEmoteData string

var douyuEmotes = parseEmoteData(douyuEmoteData)

// runtimeEmotes 是运行时补进来的平台表情表（目前只有虎牙）。
//
// 虎牙的表情包由 Wup 接口按账号/房间下发，不能写死在代码里；拉回来后
// 存这里，PlatformEmote 先查静态表再查它。
var runtimeEmotes sync.Map // Platform -> emoteTable

// setRuntimeEmotes 填一张运行时表情表。
func setRuntimeEmotes(platform Platform, table emoteTable) {
	if len(table) > 0 {
		runtimeEmotes.Store(platform, table)
	}
}

// emoteTables 按平台索引表情表。没有表的平台返回 nil。
var emoteTables = map[Platform]emoteTable{
	Douyu:  douyuEmotes,
	Douyin: douyinEmotes,
}

// parseEmoteData 解 `名字<TAB>URL` 一行一条的表。
func parseEmoteData(data string) emoteTable {
	table := emoteTable{}
	for _, line := range strings.Split(data, "\n") {
		name, url, ok := strings.Cut(line, "\t")
		if !ok || name == "" || url == "" {
			continue
		}
		table[name] = url
	}
	return table
}

// PlatformEmote 查平台基础表情表。name 是正文里的占位符，带方括号。
//
// 查不到返回 false，调用方按原文渲染——这覆盖了主播专属表情、装扮表情
// 和对方自定义表情：没有映射就还是一行字，不会消失。
func PlatformEmote(platform Platform, name string) (Emote, bool) {
	if table := emoteTables[platform]; table != nil {
		if value, ok := table[name]; ok {
			if platform == Douyu {
				value = douyuEmoteBase + value + ".png"
			}
			return Emote{Name: name, URL: value}, true
		}
	}
	if v, ok := runtimeEmotes.Load(platform); ok {
		if value, ok := v.(emoteTable)[name]; ok {
			return Emote{Name: name, URL: value}, true
		}
	}
	return Emote{}, false
}
