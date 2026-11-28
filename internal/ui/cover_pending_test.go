//go:build mock

package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 封面还没到时，那块区域**什么都不许画**——必须露出卡片自己的底色。
//
// 这条守的是用户报的「切换分区画面闪烁」。录屏逐帧量过：整屏亮度
// 143.6 → 202.1 → 166.5，中间只有一帧。原因是封面没到的那一帧画了一块
// 平涂占位色（先是 BgTertiary，后来是 MediaPlaceholder），下一帧被照片整个
// 替换——33ms 内先亮一下再暗回去，方向掉头了。
//
// 斗鱼和 B站 这块区域从头到尾都是透明的（实测 card / item / cover-wrap /
// cover 四层容器 background-color 全是 rgba(0,0,0,0)），只有「空」和「图」
// 两种状态，所以不会闪。这里对齐它们。
//
// 判据是「平」和「等于卡片底」两件事，不是「亮度落在某个区间」：
// 只要画了任何东西（色块、受光边、柔光），就至少有一条会红。
func TestPendingCoverPaintsNothing(t *testing.T) {

	for _, palette := range []string{"tech-day", "tech-night"} {
		t.Run(palette, func(t *testing.T) {
			theme.Apply(palette)
			t.Cleanup(func() { theme.Apply("tech-day") })

			pts := samplePendingCover(t)
			t.Logf("配色 %s：封面区三点 = %v，卡片底 = %v", palette, pts, theme.BgSecondary)

			// 1) 平：三个取样点必须一样。画了色块或柔光，这里就会散开。
			for i := 1; i < len(pts); i++ {
				for c := 0; c < 3; c++ {
					if d := int(pts[i][c]) - int(pts[0][c]); d > 2 || d < -2 {
						t.Fatalf("封面区不平：点 0 = %v，点 %d = %v —— 这块还画了东西", pts[0], i, pts[i])
					}
				}
			}

			// 2) 等于卡片底：不画，露出的就是卡片自己的底色。
			bg := theme.BgSecondary
			want := [3]uint8{bg.R, bg.G, bg.B}
			for c := 0; c < 3; c++ {
				if d := int(pts[0][c]) - int(want[c]); d > 8 || d < -8 {
					t.Fatalf("封面区 %v 不等于卡片底 %v：占位色又回来了，切列表会闪", pts[0], want)
				}
			}
		})
	}
}

// samplePendingCover 画一张封面永远解析不出来的卡片，返回封面区三个点的颜色。
func samplePendingCover(t *testing.T) [][3]uint8 {
	t.Helper()
	room := Streamer{
		Platform: "bilibili", RoomID: "42", Name: "甲", Title: "标题",
		CoverURL: "https://example.invalid/never.jpg", Live: true,
	}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Background(theme.BgPrimary).Padding(20).Children(func() {
			Card(c, room, 280, false, func() {}, func() {}, func() {})
		})
	}, 400, 300)
	tt.Frame()
	// 图片缓存是异步的：多推几帧，确认「确实没图」而不是「还没来得及请求」。
	tt.Advance(500e6)
	tt.Frame()

	out := make([][3]uint8, 0, 3)
	for _, y := range []int{60, 100, 140} {
		r, g, b := pixelAt(tt.Image(), 160, y)
		out = append(out, [3]uint8{r, g, b})
	}
	return out
}
