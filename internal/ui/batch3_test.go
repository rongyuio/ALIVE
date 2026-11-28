package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 二级分区的溢出判据要按「这一行真实拿到的宽度」算。
//
// 原来 `avail := winW - 2*14 - subscribeWidth - 12`，而这一行嵌在 Home 的
// Column（bodyPadX：≤1240 时是 10）+ 分区面板 Padding(12,14,14,14) +
// ScrollHorizontal.Padding(2) 里。用裸的 2*14 会把可用宽度算大 10~20 DIP：
// 二级分区刚好落进误差带时 overflow 判假，「全部」按钮不画，最后几枚 chip
// 被 Scroll 裁掉——还能横滑，但入口没了。
//
// 判据不循环：**「全部」不在 ⇒ 最后一枚 chip 不许压到订阅按钮上**。
//
// 老实说：这条**不能**区分新旧公式。我把 700–1300 每 8 DIP 扫了一遍，两种写法
// 的 overflow 结论处处相同（chip 组总宽与那 24 DIP 的误差带不重合），所以它
// 证明的是「判成装得下时确实没压到按钮上」这个不变量，不是「旧公式一定会误判」。
// 公式按真实 padding 改了（这一行嵌在 bodyPadX + 面板 14 + Scroll 2 里），
// 但那条更严的结论得靠真窗口把宽度调到临界值实测。
func TestSecondaryRowOverflowMatchesRealWidth(t *testing.T) {
	names := []string{"英雄联盟", "无畏契约", "三角洲行动", "CS2", "吃鸡行动", "APEX英雄", "暗区突围无限", "守望先锋", "lol云顶之弈", "DOTA2"}
	a := newApp()
	a.LiveMode = true
	a.Groups = []CategoryGroup{{
		Name: "网游", Categories: names,
		CategoryIDs: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"},
	}}

	checkedOverflow, checkedFit := 0, 0
	var widths []int
	for w := 700; w <= 1300; w += 8 {
		widths = append(widths, w)
	}
	for _, w := range widths {
		tt := ui.NewTester(a.Shell, w, 700)
		tt.Frame()
		tt.Frame()

		sub, ok := tt.Find("订阅分区")
		if !ok {
			t.Fatalf("w=%d：找不到订阅按钮", w)
		}
		last, ok := tt.Find("分类:" + names[len(names)-1])
		if !ok {
			t.Fatalf("w=%d：找不到最后一枚 chip", w)
		}
		_, hasMore := tt.Find("更多分区")
		if hasMore {
			checkedOverflow++
			continue
		}
		checkedFit++
		// 没有「全部」这一档，说明判成"装得下"：最后一枚 chip 就该整枚在
		// 订阅按钮左边，不许压上去（压上去就是被裁掉了）。
		if last.X+last.W > sub.X {
			t.Errorf("w=%d：没画「全部」，但最后一枚 chip 右沿 %.1f 越过了订阅按钮左沿 %.1f（被裁）",
				w, last.X+last.W, sub.X)
		}
	}
	if checkedOverflow == 0 || checkedFit == 0 {
		t.Fatalf("这组宽度没有同时覆盖装得下与装不下两种情形：overflow=%d fit=%d",
			checkedOverflow, checkedFit)
	}
}

// 关注刷新按 writeFollowStatus 的结果判成功。
//
// 原来返回 patch != nil，而真正写回的是 writeFollowStatus——刷新期间被取关的
// 那条找不到记录返回 false，却已经算进 updated。
func TestFollowRefreshScoresInsideWriteback(t *testing.T) {
	body := funcBody(t, "follow.go", "func (a *App) refreshOne(")
	if strings.Contains(body, "return patch != nil") {
		t.Error("还在按 patch != nil 记成功：找不到记录的那条会被算成已刷新")
	}
	if !strings.Contains(body, "if a.writeFollowStatus(r.Platform, r.RoomID, patch) && patch != nil {") {
		t.Error("没有用 writeFollowStatus 的返回值判成功")
	}
	if !strings.Contains(body, "updated.Add(1)") || !strings.Contains(body, "failed.Add(1)") {
		t.Error("分类不在写回里")
	}
}

// funcBody 取某个函数在源码里的正文（到下一个顶层 func 为止）。
func funcBody(t *testing.T, file, signature string) string {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("读不到 %s：%v", file, err)
	}
	text := string(src)
	i := strings.Index(text, signature)
	if i < 0 {
		t.Fatalf("%s 里找不到 %q", file, signature)
	}
	body := text[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	return body
}
