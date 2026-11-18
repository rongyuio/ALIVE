package ui

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

// 归一化之后，控制条上每一颗图标的墨迹必须一样高。
//
// 这条量 SVG 本身（栅格化后扫包围盒），屏幕像素那条由
// TestPlayerIconsShareInkBand 兜。两条一起才说明「一整排看着一样大」。
func TestPlayerChromeInkNormalized(t *testing.T) {
	const tolerance = 0.35
	icons := []struct {
		name string
		s    *ui.SVG
	}{
		{"暂停", iconPause}, {"播放", iconPlay}, {"重新加载", iconSync},
		{"静音", iconVolumeMute}, {"音量低", iconVolumeLow}, {"音量高", iconVolumeHigh},
		{"小窗", iconPip}, {"窗口全屏", iconWinFull}, {"收起窗口全屏", iconWinShrink},
		{"全屏", iconFullscreen}, {"退出全屏", iconMinimize2}, {"弹幕电视", iconDanmuTV},
	}
	for _, ic := range icons {
		if h := svgInkHeight(ic.s); h < iconInkTarget-tolerance || h > iconInkTarget+tolerance {
			t.Errorf("「%s」墨迹高 %.2f，期望 %.2f±%.2f：这一排又会参差",
				ic.name, h, iconInkTarget, tolerance)
		}
		if _, _, _, _, clipped := svgInkBox(ic.s); clipped {
			t.Errorf("「%s」的墨迹贴到了 24 网格边缘，缩小后会被裁", ic.name)
		}
	}
}

// 归一化顺带把描边也统一了：以前喇叭和小窗为了让墨迹够高被推到 26 字号，
// 笔画跟着变成 2.2，一排里那两颗明显更重。
//
// 只量带轴对齐笔画的字形（矩形边、竖线、弧线顶端）。全屏 / 窗口全屏那几颗
// 全是 45° 斜线和圆角，轴向扫描量到的是笔画宽 ×√2 或更宽，这不反映粗细，
// 所以单列一组按 √2 折算。
func TestPlayerChromeStrokeUniform(t *testing.T) {
	const (
		want      = 2.0
		tolerance = 0.3
		sample    = 24
	)
	icons := []struct {
		name string
		s    *ui.SVG
	}{
		{"重新加载", iconSync}, {"音量高", iconVolumeHigh}, {"小窗", iconPip},
		{"弹幕电视", iconDanmuTV},
	}
	for _, ic := range icons {
		if w := svgStrokeWidth(ic.s, sample); w < want-tolerance || w > want+tolerance {
			t.Errorf("「%s」描边 %.2fpx，期望 %.2f±%.2f：一排里粗细不齐",
				ic.name, w, want, tolerance)
		}
	}
}

// svgInkBox 把 SVG 栅格化后返回它在 24 网格里的墨迹包围盒；clipped 表示
// 墨迹贴到了网格边缘，说明缩小后会被裁。
func svgInkBox(s *ui.SVG) (x0, y0, x1, y1 float32, clipped bool) {
	const n = 1024
	img := s.Image(n, n, color.White)
	minX, minY, maxX, maxY := n, n, -1, -1
	for y := 0; y < n; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+n*4]
		for x := 0; x < n; x++ {
			if row[x*4+3] > 8 {
				if x < minX {
					minX = x
				}
				if x > maxX {
					maxX = x
				}
				if y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 {
		return 0, 0, 0, 0, false
	}
	g := float32(n) / 24
	x0, y0 = float32(minX)/g, float32(minY)/g
	x1, y1 = float32(maxX+1)/g, float32(maxY+1)/g
	clipped = minX == 0 || minY == 0 || maxX == n-1 || maxY == n-1
	return x0, y0, x1, y1, clipped
}

// svgInkHeight 是墨迹的高度，24 网格单位。
func svgInkHeight(s *ui.SVG) float32 {
	_, y0, _, y1, _ := svgInkBox(s)
	return y1 - y0
}

// svgStrokeWidth 量笔画宽度，单位是 24 网格。
//
// 做法是把字形栅格化到 size×10 像素，然后横竖两个方向扫过去，把每段连续
// 不透明像素的长度做成直方图，取出现次数最多的那个宽度——竖直笔画的横向
// 厚度、水平笔画的纵向厚度都会落在同一档上，斜线和圆角是长尾，不影响。
//
// 不能只看中线那一行：全屏、窗口全屏的四角括号中线那一行是空的。
func svgStrokeWidth(s *ui.SVG, size float32) float32 {
	const sub = 10
	px := int(size * sub)
	img := s.Image(px, px, color.White)
	opaque := func(x, y int) bool { return img.RGBAAt(x, y).A > 100 }

	const bins = 200
	var hist [bins]int
	add := func(run int) {
		if run < 4 || run >= bins {
			return
		}
		hist[run]++
	}
	for y := 0; y < px; y++ {
		run := 0
		for x := 0; x < px; x++ {
			if opaque(x, y) {
				run++
			} else if run > 0 {
				add(run)
				run = 0
			}
		}
		if run > 0 {
			add(run)
		}
	}
	for x := 0; x < px; x++ {
		run := 0
		for y := 0; y < px; y++ {
			if opaque(x, y) {
				run++
			} else if run > 0 {
				add(run)
				run = 0
			}
		}
		if run > 0 {
			add(run)
		}
	}
	best, bestN := 0, 0
	for r, n := range hist {
		if n > bestN {
			best, bestN = r, n
		}
	}
	return float32(best) / sub
}
