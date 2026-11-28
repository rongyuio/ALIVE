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

// 「更多分区」按钮的底色、字色、箭头必须按同一帧的同一个 a.MoreOpen 算。
//
// 内联 btn.Clicked() 会在这一帧中途翻转 a.MoreOpen：底色（上面几行）已按旧值
// 算过，字色和箭头当场变成新值——同一颗按钮在一帧里两副长相，而下面的
// PopoverBase 又已经用新值把浮层打开了。改用 OnClick 推迟到本帧构建之后。
func TestMoreButtonDefersToggle(t *testing.T) {
	body := funcBody(t, "category_bar.go", "func (a *App) moreButton(")
	if strings.Contains(body, "if btn.Clicked() {") {
		t.Error("moreButton 还在内联翻转 a.MoreOpen：底色和字色会在同一帧里打架")
	}
	if !strings.Contains(body, "btn.OnClick(func() { a.MoreOpen = !a.MoreOpen })") {
		t.Error("moreButton 没有把翻转推迟到本帧构建之后")
	}
}

// 查询变短导致选中项越界时，收到**末项**，不要甩回第一条。
//
// 归 0 会让还在连续打字的人看到选中项从末尾跳回开头，回车落到一条他根本
// 没看过的分区上。
func TestCategoryHitClampsToLastNotFirst(t *testing.T) {
	a := newApp()
	a.Groups = []CategoryGroup{{
		Name:        "网游",
		Categories:  []string{"英雄联盟", "DOTA2", "网游其他"},
		CategoryIDs: []string{"1", "2", "3"},
	}}
	a.CategoryQuery = "游"
	a.catHitQuery = "游" // 让 categoryHits 不把它当成"查询变了"重置成 0
	a.CategoryHit = 99

	hits := a.categoryHits()
	if len(hits) < 2 {
		t.Fatalf("这组分区下「游」只命中 %d 条，测不出 clamp", len(hits))
	}
	if a.CategoryHit != len(hits)-1 {
		t.Fatalf("越界后 CategoryHit = %d，应为末项 %d（归 0 会让选中项跳回开头）",
			a.CategoryHit, len(hits)-1)
	}
}

// 关注刷新里"这一条算成功还是失败"要在**写回的同一个回调里**判。
//
// 原来返回 `patch != nil`，真正写回的是 writeFollowStatus——它找不到记录
// （刷新期间被取关了）返回 false，可那一条已经算进 updated 了。
func TestFollowRefreshScoresInsideWriteback(t *testing.T) {
	body := funcBody(t, "follow.go", "func (a *App) refreshOne(")
	if strings.Contains(body, "return patch != nil") {
		t.Error("refreshOne 还在按 patch != nil 记成功：找不到记录的那条会被算成已刷新")
	}
	if !strings.Contains(body, "writeFollowStatus(r.Platform, r.RoomID, patch) && patch != nil") {
		t.Error("refreshOne 没有用 writeFollowStatus 的返回值判成功")
	}
	if !strings.Contains(body, "updated.Add(1)") || !strings.Contains(body, "failed.Add(1)") {
		t.Error("分类没有放进 Update 回调")
	}
}

// 一条都没试的时候不许说「已刷新 0 位主播」配成功色调。
func TestRefreshSummarySaysNothingToRefresh(t *testing.T) {
	msg, tone := refreshSummary(0, 0, 0)
	if strings.Contains(msg, "已刷新") {
		t.Errorf("没有可刷新的记录却说 %q", msg)
	}
	if tone == toastSuccess {
		t.Error("没有可刷新的记录却给了成功色调")
	}
	// 原来的两种情形行为不变。
	if m, tn := refreshSummary(3, 0, 3); m != "已刷新 3 位主播" || tn != toastSuccess {
		t.Errorf("正常成功分支变了：%q %v", m, tn)
	}
	if _, tn := refreshSummary(0, 3, 3); tn != toastError {
		t.Errorf("全失败分支变了：%v", tn)
	}
}

// 墙格音量每次滚轮事件都现读，不用构建时缓存的旧值。
//
// 同一帧里两次滚轮事件都基于同一个旧 volume，只有最后一次生效——触控板快滑
// 两格只跳一格。
func TestWallTileVolumeReadsFreshOnEachWheel(t *testing.T) {
	body := funcBody(t, "watch.go", "func (a *App) watchTileAudio(")
	i := strings.Index(body, "hot.HandleInput(func(ev ui.InputEvent) bool {")
	if i < 0 {
		t.Fatal("找不到 HandleInput")
	}
	h := body[i:]
	if j := strings.Index(h, "\n\t\t})"); j > 0 {
		h = h[:j]
	}
	if !strings.Contains(h, "a.watchVolume(watchKey(room))") {
		t.Error("滚轮处理里没有现读音量：同一帧两次事件只会有一次生效")
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
