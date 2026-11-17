package ui

import (
	"testing"

	"alive/internal/live"
)

// 正文里的 [表情名] 按平台解析：协议自带的表情优先，其次是平台基础表；
// 都不认的方括号必须原样留在文字里——用户打的 [备注] 不能消失。
func TestSplitDanmuText(t *testing.T) {
	biliDog := live.Emote{Name: "[dog]", URL: "https://example.invalid/dog.png", Width: 20, Height: 20}

	cases := []struct {
		name     string
		platform live.Platform
		text     string
		emotes   []live.Emote
		want     []string // 每段：文字本身，或 "<emote>"
	}{
		{
			name:     "纯文本",
			platform: live.Douyu,
			text:     "你好",
			want:     []string{"你好"},
		},
		{
			name:     "斗鱼内置表情",
			platform: live.Douyu,
			text:     "哈哈[财神]",
			want:     []string{"哈哈", "<emote>"},
		},
		{
			name:     "未知占位符原样保留",
			platform: live.Douyu,
			text:     "好[不存在]",
			want:     []string{"好[不存在]"},
		},
		{
			name:     "协议表情优先于基础表",
			platform: live.Bilibili,
			text:     "a[dog]b",
			emotes:   []live.Emote{biliDog},
			want:     []string{"a", "<emote>", "b"},
		},
		{
			name:     "连续表情",
			platform: live.Douyu,
			text:     "[财神][财神]",
			want:     []string{"<emote>", "<emote>"},
		},
		{
			name:     "未闭合的方括号当普通文字",
			platform: live.Douyu,
			text:     "[财神",
			want:     []string{"[财神"},
		},
		{
			name:     "空方括号不匹配",
			platform: live.Douyu,
			text:     "[]",
			want:     []string{"[]"},
		},
	}

	for _, tc := range cases {
		parts := splitDanmuText(tc.platform, tc.text, tc.emotes)
		if len(parts) != len(tc.want) {
			t.Fatalf("%s：切出 %d 段 %+v，期望 %d 段", tc.name, len(parts), parts, len(tc.want))
		}
		for i, p := range parts {
			got := p.text
			if p.emote != nil {
				got = "<emote>"
			}
			if got != tc.want[i] {
				t.Errorf("%s：第 %d 段 = %q，期望 %q", tc.name, i, got, tc.want[i])
			}
		}
	}
}

// 表情尺寸跟着字号走，保持原图比例；缺宽高时按 1:1。
func TestDanmuEmoteSize(t *testing.T) {
	square := live.Emote{Width: 20, Height: 20}
	w, h := danmuEmoteSize(square, 20)
	if h != 20*danmuEmoteScale || w != h {
		t.Fatalf("正方形表情 = %vx%v", w, h)
	}

	wide := live.Emote{Width: 100, Height: 50}
	w, h = danmuEmoteSize(wide, 20)
	if w != h*2 {
		t.Fatalf("2:1 表情宽 = %v，高 = %v，比例没保住", w, h)
	}

	none := live.Emote{}
	w, h = danmuEmoteSize(none, 20)
	if w != h {
		t.Fatalf("缺宽高的表情 = %vx%v，应按 1:1", w, h)
	}
}

// 有表情时走富文本，没有表情时不为了一个 U+FFFC 走富文本。
func TestBuildDanmuSpans(t *testing.T) {
	plain := buildDanmuSpans(live.Douyu, "普通弹幕", nil, 13)
	if plain.hasEmote {
		t.Fatal("纯文本被判成有表情")
	}

	mixed := buildDanmuSpans(live.Douyu, "哈哈[财神]", nil, 13)
	if !mixed.hasEmote || len(mixed.objs) != 1 {
		t.Fatalf("表情没有进入内联对象：hasEmote=%v objs=%d", mixed.hasEmote, len(mixed.objs))
	}
	var found bool
	for _, s := range mixed.spans {
		if s.ObjectWidth > 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("表情 span 没有内联对象宽度")
	}
}

// 没有名字、只靠位置对应的表情（抖音 rtf 里的图）要按 InlineEmote 出现顺序取。
func TestSplitDanmuTextInlineEmote(t *testing.T) {
	emotes := []live.Emote{
		{URL: "https://example.invalid/1.png"},
		{URL: "https://example.invalid/2.png"},
	}
	text := "哈" + string(live.InlineEmote) + "哈" + string(live.InlineEmote)

	parts := splitDanmuText(live.Douyin, text, emotes)
	if len(parts) != 4 {
		t.Fatalf("切成 %d 段，期望 4 段", len(parts))
	}
	if parts[0].emote != nil || parts[0].text != "哈" {
		t.Errorf("第 1 段 = %+v", parts[0])
	}
	if parts[1].emote == nil || parts[1].emote.URL != "https://example.invalid/1.png" {
		t.Errorf("第 2 段不是第一张图：%+v", parts[1])
	}
	if parts[3].emote == nil || parts[3].emote.URL != "https://example.invalid/2.png" {
		t.Errorf("第 4 段不是第二张图：%+v", parts[3])
	}
}

// 占位符多于表情时，多出来的原样留着，不能吞字。
func TestSplitDanmuTextInlineEmoteExtraPlaceholder(t *testing.T) {
	text := string(live.InlineEmote) + string(live.InlineEmote)
	parts := splitDanmuText(live.Douyin, text, []live.Emote{{URL: "https://example.invalid/1.png"}})
	if len(parts) != 2 {
		t.Fatalf("切成 %d 段，期望 2 段", len(parts))
	}
	if parts[0].emote == nil {
		t.Error("第一个占位符没有换成表情")
	}
	if parts[1].text != string(live.InlineEmote) {
		t.Errorf("多出来的占位符被吞了：%+v", parts[1])
	}
}
