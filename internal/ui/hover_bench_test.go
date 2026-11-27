package ui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 这两条量的是同一件事：指针扫过列表时，一帧要花多少。
//
//   - HoverInBuild：今天的写法，在构建里读 Hovered、按它上底色（会重跑整个视图）。
//   - HoverInDraw：docs/ui/performance.md 建议的写法，只在 Draw 里读。
//
// 列表是虚拟化的（ui.List 只建可见行），所以两者都只付一屏的钱；这两个数存在的
// 意义是：把「86 处在构建里读 Hovered」这条债的代价量出来，而不是靠感觉定性。
const benchRows = 10000

var benchNames = func() []string {
	out := make([]string, benchRows)
	for i := range out {
		out[i] = fmt.Sprintf("第 %d 行", i)
	}
	return out
}()

func BenchmarkHoverInBuild(b *testing.B) {
	view := func(c *ui.Context) {
		ui.List(c, nil, len(benchNames), func(i int) {
			row := ui.Row(c).Height(28).Padding(4, 8)
			if row.Hovered() {
				row.Background(theme.FillTertiary)
			} else {
				row.Background(ui.Transparent)
			}
			ui.Text(c, benchNames[i])
		}).Grow(1)
	}
	tt := ui.NewTester(view, 800, 600)
	tt.Frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}

func BenchmarkHoverInDraw(b *testing.B) {
	view := func(c *ui.Context) {
		ui.List(c, nil, len(benchNames), func(i int) {
			row := ui.Row(c).Height(28).Padding(4, 8)
			row.Draw(func(p *ui.Painter, r ui.Rect) {
				if row.Hovered() {
					p.Fill(r, theme.FillTertiary, 0)
				}
			})
			ui.Text(c, benchNames[i])
		}).Grow(1)
	}
	tt := ui.NewTester(view, 800, 600)
	tt.Frame()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tt.Frame()
	}
}
