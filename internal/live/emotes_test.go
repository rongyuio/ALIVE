package live

import (
	"strings"
	"testing"
)

// 斗鱼表情表是全量面板导出的：8 个页签 265 条。
//
// 这条测试挡的是「只抄了第一个页签」那类回归——[开车]、[调戏] 在后面的
// 页签里，漏掉时弹幕里只剩方括号文字。
func TestDouyuEmoteTable(t *testing.T) {
	if len(douyuEmotes) < 250 {
		t.Fatalf("斗鱼表情表只有 %d 条，应该覆盖网页端全部页签", len(douyuEmotes))
	}
	for name, file := range douyuEmotes {
		if !strings.HasPrefix(name, "[") || !strings.HasSuffix(name, "]") {
			t.Fatalf("表情名 %q 没带方括号", name)
		}
		if file == "" || strings.Contains(file, "/") {
			t.Fatalf("表情 %q 的文件名 = %q", name, file)
		}
	}

	for _, name := range []string{"[开车]", "[调戏]", "[财神]"} {
		e, ok := PlatformEmote(Douyu, name)
		if !ok {
			t.Fatalf("%s 查不到", name)
		}
		if !strings.HasPrefix(e.URL, douyuEmoteBase) || !strings.HasSuffix(e.URL, ".png") {
			t.Fatalf("%s 的地址 = %s", name, e.URL)
		}
	}
	if e, _ := PlatformEmote(Douyu, "[开车]"); !strings.Contains(e.URL, "kaiche_") {
		t.Fatalf("[开车] 的地址 = %s", e.URL)
	}
}
