package ui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/media"
)

// danmuPart 是正文的一段：一段文字，或一个表情图。
type danmuPart struct {
	text  string
	emote *live.Emote
}

// danmuEmoteScale 是表情相对字号的倍率。
//
// 各平台的原始尺寸对不上：B 站表情自带 20×20，斗鱼网页显示 28（正文 13），
// 虎牙也是 28×24。这里统一按字号放大，不直接用平台给的像素值——那是给
// 它们自己的字号配的，照搬会在一处过大、一处过小。
const danmuEmoteScale float32 = 1.35

// splitDanmuText 把正文按 [表情名] 切成文字段和表情段。
//
// 能不能换成图由两处决定：本条弹幕自带的 Emotes（B 站协议下发），以及
// 平台基础表情表（斗鱼等）。两处都没有的方括号原样留着——用户打一个
// [备注] 不该从屏幕上消失。
func splitDanmuText(platform live.Platform, text string, emotes []live.Emote) []danmuPart {
	own := make(map[string]live.Emote, len(emotes))
	// inline 是没有名字、只靠位置对应的表情（抖音富文本里的图）。
	// 正文用 live.InlineEmote 占位，按出现顺序依次取。
	var inline []live.Emote
	for _, e := range emotes {
		if e.URL == "" {
			continue
		}
		if e.Name == "" {
			inline = append(inline, e)
			continue
		}
		own[e.Name] = e
	}
	lookup := func(name string) (live.Emote, bool) {
		if e, ok := own[name]; ok {
			return e, true
		}
		return live.PlatformEmote(platform, name)
	}

	runes := []rune(text)
	var parts []danmuPart
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			parts = append(parts, danmuPart{text: plain.String()})
			plain.Reset()
		}
	}

	for i := 0; i < len(runes); {
		if runes[i] == live.InlineEmote && len(inline) > 0 {
			flush()
			emote := inline[0]
			inline = inline[1:]
			parts = append(parts, danmuPart{emote: &emote})
			i++
			continue
		}
		if runes[i] == '[' {
			if j := indexRune(runes, ']', i+1); j > i+1 {
				name := string(runes[i : j+1])
				if e, ok := lookup(name); ok {
					flush()
					emote := e
					parts = append(parts, danmuPart{emote: &emote})
					i = j + 1
					continue
				}
			}
		}
		plain.WriteRune(runes[i])
		i++
	}
	flush()
	return parts
}

func indexRune(runes []rune, r rune, from int) int {
	for i := from; i < len(runes); i++ {
		if runes[i] == r {
			return i
		}
	}
	return -1
}

// danmuEmoteSize 算表情在当前字号下的显示宽高，保持原比例。
//
// 平台给的宽高偶尔缺失（抖音的表情表只有 URL），按 1:1 兜底。
func danmuEmoteSize(e live.Emote, fontSize float32) (float32, float32) {
	h := fontSize * danmuEmoteScale
	if e.Width <= 0 || e.Height <= 0 {
		return h, h
	}
	return h * float32(e.Width) / float32(e.Height), h
}

// danmuSpans 是带表情的正文：文本按表情占位符切开后的 spans，
// 以及每个内联对象位置对应的表情。
type danmuSpans struct {
	spans []ui.Span
	objs  map[int]live.Emote
	// hasEmote 报告这条正文里有没有可渲染的表情。没有时调用方走纯文本，
	// 不为了一个 U+FFFC 走富文本。
	hasEmote bool
}

// buildDanmuSpans 把一条弹幕的正文变成 spans。
func buildDanmuSpans(platform live.Platform, text string, emotes []live.Emote, fontSize float32) danmuSpans {
	parts := splitDanmuText(platform, text, emotes)
	out := danmuSpans{objs: map[int]live.Emote{}, spans: make([]ui.Span, 0, len(parts))}
	pos := 0
	for _, p := range parts {
		if p.emote == nil {
			out.spans = append(out.spans, ui.Span{Text: p.text, Size: fontSize})
			pos += len([]rune(p.text))
			continue
		}
		w, h := danmuEmoteSize(*p.emote, fontSize)
		out.spans = append(out.spans, ui.Span{
			Text: "￼",
			Size: fontSize,
			// 表情不坐在基线上：留一点下沉，和汉字的重心对齐。
			ObjectWidth:   w,
			ObjectHeight:  h * 0.78,
			ObjectDescent: h * 0.22,
		})
		out.objs[pos] = *p.emote
		out.hasEmote = true
		pos++
	}
	return out
}

// richText 按参数构建富文本，并把表情图挂到内联对象上。
func (s danmuSpans) richText(c *ui.Context, fontSize float32, color ui.Color) ui.Element {
	el := ui.RichText(c, s.spans...).FontSize(fontSize).TextColor(color)
	objs := s.objs
	el.DrawObjects(func(p *ui.Painter, at int, r ui.Rect) {
		e, ok := objs[at]
		if !ok {
			return
		}
		bmp, wait, anim := media.Default.EmoteFrame(e.URL, r.W, c.Now())
		if bmp == nil {
			return
		}
		p.Image(bmp, r, ui.Contain)
		if anim && wait > 0 {
			// 动图：到点再请求一帧，否则表情会停在当前帧。
			c.After(wait)
		}
	})
	return el
}
