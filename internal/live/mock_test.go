//go:build mock

package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// mockScript 是 UI 验收用的剧本，必须覆盖面板和飞屏要处理的每一种消息。
// 少了一种，就意味着那条渲染分支只能等真实消息来了才可能被发现出错。
//
// 这里的断言写得比「有没有这一类」更细：长度、档位、昵称边界都要卡住。
// 只断言「存在一条长文本」的话，把长度从 200 改成 60 也能过，
// 而那已经不足以撑出三行了。
func TestMockScriptCoversEveryKind(t *testing.T) {
	var (
		plainCount   int
		shortText    bool
		longChat     bool
		asciiLong    bool
		hasURL       bool
		specialChars bool
		spacePadded  bool

		colors     = map[string]bool{}
		levels     = map[int]bool{}
		minNickLen = 99
		maxNickLen int

		inlineCount   int
		inlineMulti   bool
		imageOnly     int
		gifEmote      bool
		sizelessEmote bool

		scPrices    = map[int]bool{}
		scLong      bool
		scHuge      bool
		scNoLevel   bool
		scLongNick  bool
		scNoBackgr  int
		nickRepeats = map[string]int{}
	)

	for _, d := range mockScript() {
		nickRepeats[d.Nickname]++
		if n := len([]rune(d.Nickname)); n < minNickLen {
			minNickLen = n
		}
		if n := len([]rune(d.Nickname)); n > maxNickLen {
			maxNickLen = n
		}

		if d.Kind == DanmuSuperChat {
			if d.Price <= 0 {
				t.Errorf("醒目留言 %q 没有金额，面板的 ¥ 徽章会显示成 ¥0", d.Content)
			}
			scPrices[d.Price] = true
			if len([]rune(d.Content)) >= 80 {
				scLong = true
			}
			if len([]rune(d.Content)) >= 200 {
				scHuge = true
			}
			if d.Level == 0 {
				scNoLevel = true
			}
			if len([]rune(d.Nickname)) >= 12 {
				scLongNick = true
			}
			if len(d.Background) != 7 {
				scNoBackgr++
			}
			continue
		}

		if d.Color != "" {
			colors[d.Color] = true
		}
		levels[d.Level] = true

		for _, e := range d.Emotes {
			if strings.Contains(e.URL, ".gif") {
				gifEmote = true
			}
			if e.Width <= 0 || e.Height <= 0 {
				sizelessEmote = true
			}
		}

		switch {
		case d.Content == "" && len(d.Emotes) > 0:
			imageOnly++
		case len(d.Emotes) > 0:
			// 内联表情：正文里的占位符必须和 Emotes 里的名字对得上，
			// 否则渲染层找不到对应图片
			for _, e := range d.Emotes {
				if e.Name == "" {
					t.Errorf("内联表情弹幕 %q 的 Emote 缺少占位符名", d.Content)
					continue
				}
				if !strings.Contains(d.Content, e.Name) {
					t.Errorf("内联表情弹幕 %q 的正文里找不到占位符 %q", d.Content, e.Name)
				}
			}
			inlineCount++
			if len(d.Emotes) > 1 {
				inlineMulti = true
			}
		default:
			plainCount++
			n := len([]rune(d.Content))
			if n <= 2 {
				shortText = true
			}
			if n >= 80 {
				longChat = true
			}
			if n >= 60 && !strings.ContainsAny(d.Content, " \t") && isASCII(d.Content) {
				asciiLong = true
			}
			if strings.Contains(d.Content, "http") {
				hasURL = true
			}
			if strings.ContainsAny(d.Content, "#$%^&*()[]{}|;'\"<>") {
				specialChars = true
			}
			if d.Content != strings.TrimSpace(d.Content) {
				spacePadded = true
			}
		}
	}

	checks := []struct {
		name string
		ok   bool
	}{
		{"至少 8 条纯文字", plainCount >= 8},
		{"极短文字（≤2 字）", shortText},
		{"长文字（≥80 字，能折两三行）", longChat},
		{"无空格长英文串（测 word-break）", asciiLong},
		{"带 URL 的文字", hasURL},
		{"带特殊字符的文字", specialChars},
		{"首尾带空格的文字", spacePadded},
		{"平台原生颜色 ≥3 种", len(colors) >= 3},
		{"等级 0（不显示徽章）", levels[0]},
		{"等级 1（最小）", levels[1]},
		{"等级 60（最大）", levels[60]},
		{"昵称最短 1 个字", minNickLen == 1},
		{"昵称最长 ≥12 个字（测截断）", maxNickLen >= 12},
		{"内联表情 ≥2 条", inlineCount >= 2},
		{"一条内联里带多个表情", inlineMulti},
		{"纯图片 ≥2 条", imageOnly >= 2},
		{"纯图片里有 gif", gifEmote},
		{"纯图片里有缺宽高的", sizelessEmote},
		{"醒目留言金额 ≥4 档", len(scPrices) >= 4},
		{"醒目留言里有长文（≥80 字）", scLong},
		{"醒目留言里有超长文（≥200 字）", scHuge},
		{"醒目留言里有不带等级的", scNoLevel},
		{"醒目留言里有超长昵称", scLongNick},
		{"同一个昵称出现多次（哈希色要稳定）", anyRepeat(nickRepeats)},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("mock 剧本缺少：%s", c.name)
		}
	}
	if scNoBackgr > 0 {
		t.Errorf("%d 条醒目留言没有背景色，面板会退回强调色", scNoBackgr)
	}
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func anyRepeat(m map[string]int) bool {
	for _, n := range m {
		if n > 1 {
			return true
		}
	}
	return false
}

// mock 模式下 Stream 必须返回「在播但没有任何档位」：上层据此跳过
// ffmpeg 转封装，画面留在封面上，UI 仍可完整操作。
func TestMockStreamSkipsPlayback(t *testing.T) {
	prov := newMockProvider(Bilibili, MockDanmu)
	s, err := prov.Stream(context.Background(), "mock-1", "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if !s.Live {
		t.Error("danmu 模式应报告在播")
	}
	if len(s.Variants) != 0 {
		t.Errorf("不应给出可播地址（会触发真实转封装），得到 %d 档", len(s.Variants))
	}

	off := newMockProvider(Bilibili, MockOffline)
	s, err = off.Stream(context.Background(), "mock-1", "")
	if err != nil {
		t.Fatalf("取流失败: %v", err)
	}
	if s.Live {
		t.Error("offline 模式应报告未开播")
	}
}

// RoomsFrom 的默认模式必须和原来 Rooms 的行为一致：一段 8 条，取完就停。
func TestMockRoomsFromDefault(t *testing.T) {
	// 钉死成「未设置」：外面 shell 里恰好导出了 partial 时，这条测试不能跟着变。
	t.Setenv("ALIVE_MOCK_FEED", "")
	prov := newMockProvider(Douyu, MockDanmu)
	ctx := context.Background()

	chunk, err := prov.RoomsFrom(ctx, "1_1", 0, 40)
	if err != nil {
		t.Fatalf("RoomsFrom(0) 失败: %v", err)
	}
	if len(chunk.Rooms) != 8 {
		t.Fatalf("RoomsFrom(0) 应返回 8 条，得到 %d 条", len(chunk.Rooms))
	}
	if chunk.Cursor != 8 {
		t.Errorf("RoomsFrom(0) 的 Cursor 应为 8，得到 %d", chunk.Cursor)
	}
	if chunk.HasMore {
		t.Error("8 条取完后 HasMore 应为 false")
	}
	if chunk.Partial {
		t.Error("默认模式不该标 Partial")
	}
	for i, r := range chunk.Rooms {
		want := fmt.Sprintf("mock-%d", i+1)
		if r.RoomID != want {
			t.Errorf("第 %d 条 RoomID 应为 %q，得到 %q", i+1, want, r.RoomID)
		}
		if r.Platform != Douyu || !r.Live {
			t.Errorf("第 %d 条应为斗鱼在播房间，得到 platform=%s live=%v", i+1, r.Platform, r.Live)
		}
	}

	next, err := prov.RoomsFrom(ctx, "1_1", 8, 40)
	if err != nil {
		t.Fatalf("RoomsFrom(8) 失败: %v", err)
	}
	if len(next.Rooms) != 0 {
		t.Errorf("RoomsFrom(8) 应为空，得到 %d 条", len(next.Rooms))
	}
	if next.Cursor != 8 || next.HasMore {
		t.Errorf("RoomsFrom(8) 应停在 Cursor=8、HasMore=false，得到 Cursor=%d HasMore=%v",
			next.Cursor, next.HasMore)
	}
}

// partial 模式：400 条、每段 40 条，cursor=40 那一段只回 20 条并标 Partial；
// 按 Cursor 续到 400 条时 HasMore=false，全程 RoomID 不重复、不跳号。
func TestMockRoomsFromPartial(t *testing.T) {
	t.Setenv("ALIVE_MOCK_FEED", "partial")
	prov := newMockProvider(Bilibili, MockDanmu)
	ctx := context.Background()

	first, err := prov.RoomsFrom(ctx, "1_1", 0, 40)
	if err != nil {
		t.Fatalf("RoomsFrom(0) 失败: %v", err)
	}
	if len(first.Rooms) != 40 {
		t.Fatalf("RoomsFrom(0) 应返回 40 条，得到 %d 条", len(first.Rooms))
	}
	if first.Cursor != 40 || !first.HasMore || first.Partial {
		t.Errorf("RoomsFrom(0) 应为 Cursor=40、HasMore=true、Partial=false，得到 Cursor=%d HasMore=%v Partial=%v",
			first.Cursor, first.HasMore, first.Partial)
	}

	second, err := prov.RoomsFrom(ctx, "1_1", 40, 40)
	if err != nil {
		t.Fatalf("RoomsFrom(40) 失败: %v", err)
	}
	if len(second.Rooms) != 20 {
		t.Fatalf("RoomsFrom(40) 应只回 20 条，得到 %d 条", len(second.Rooms))
	}
	if second.Cursor != 60 || !second.HasMore || !second.Partial {
		t.Errorf("RoomsFrom(40) 应为 Cursor=60、HasMore=true、Partial=true，得到 Cursor=%d HasMore=%v Partial=%v",
			second.Cursor, second.HasMore, second.Partial)
	}

	third, err := prov.RoomsFrom(ctx, "1_1", 60, 40)
	if err != nil {
		t.Fatalf("RoomsFrom(60) 失败: %v", err)
	}
	if len(third.Rooms) != 40 {
		t.Fatalf("RoomsFrom(60) 应返回 40 条，得到 %d 条", len(third.Rooms))
	}
	if third.Cursor != 100 || !third.HasMore || third.Partial {
		t.Errorf("RoomsFrom(60) 应为 Cursor=100、HasMore=true、Partial=false，得到 Cursor=%d HasMore=%v Partial=%v",
			third.Cursor, third.HasMore, third.Partial)
	}

	seen := make(map[string]bool, mockFeedTotal)
	cursor := 0
	want := 1
	for {
		chunk, err := prov.RoomsFrom(ctx, "1_1", cursor, 40)
		if err != nil {
			t.Fatalf("cursor=%d 取数失败: %v", cursor, err)
		}
		for _, r := range chunk.Rooms {
			id := fmt.Sprintf("mock-feed-%d", want)
			if r.RoomID != id {
				t.Fatalf("第 %d 条 RoomID 应为 %q，得到 %q（跳号或顺序错）", want, id, r.RoomID)
			}
			if seen[r.RoomID] {
				t.Fatalf("RoomID %q 重复出现", r.RoomID)
			}
			seen[r.RoomID] = true
			if r.Platform != Bilibili || !r.Live {
				t.Fatalf("第 %d 条应为 B 站在播房间，得到 platform=%s live=%v", want, r.Platform, r.Live)
			}
			if !strings.Contains(r.Title, fmt.Sprintf("#%d", want)) {
				t.Errorf("第 %d 条的 Title 里没有序号：%q", want, r.Title)
			}
			want++
		}
		if !chunk.HasMore {
			if len(chunk.Rooms) == 0 {
				t.Fatalf("cursor=%d 返回空段且 HasMore=false，没取满 %d 条", cursor, mockFeedTotal)
			}
			if chunk.Cursor != mockFeedTotal {
				t.Errorf("末段的 Cursor 应为 %d，得到 %d", mockFeedTotal, chunk.Cursor)
			}
			break
		}
		if chunk.Cursor <= cursor {
			t.Fatalf("HasMore=true 但 Cursor 没前进：%d -> %d", cursor, chunk.Cursor)
		}
		cursor = chunk.Cursor
	}
	if want-1 != mockFeedTotal {
		t.Errorf("应取到 %d 条，实际 %d 条", mockFeedTotal, want-1)
	}
	if len(seen) != mockFeedTotal {
		t.Errorf("应见到 %d 个不同 RoomID，实际 %d 个", mockFeedTotal, len(seen))
	}
}

// blocked / timeout 两个模式必须走真实的错误类型，上层才能用
// errors.Is 分类，日志里才会出现 err.class=blocked/timeout。
func TestMockFailureModes(t *testing.T) {
	ctx := context.Background()
	blocked := newMockProvider(Douyu, MockBlocked)
	if _, err := blocked.Stream(ctx, "x", ""); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked Stream: %v", err)
	}
	if _, err := blocked.Danmaku(ctx, "x", nil); !errors.Is(err, ErrBlocked) {
		t.Fatalf("blocked Danmaku: %v", err)
	}
	timeout := newMockProvider(Douyu, MockTimeout)
	if _, err := timeout.Stream(ctx, "x", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout Stream: %v", err)
	}
	if _, err := timeout.Danmaku(ctx, "x", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout Danmaku: %v", err)
	}
}
