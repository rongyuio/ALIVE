package ui

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 几何契约：光带整条落在岛内，body 从岛沿往里 2px 处盖上，光带就收在
// 它和岛沿之间。
//
// 曾经让环一半探出岛沿（inset -1）：深色主题好看，浅色主题里外半圈画在
// 顶栏白底上，paper 的浅灰、parchment 的赭石直接消失，屏幕上又只剩岛内
// 那一条——最早那个「半条框」。光只能落在暗面上。
func TestIslandRingBandSitsInsideTheEdge(t *testing.T) {
	if islandRingInset < 0 {
		t.Fatalf("光面起点 %v：光探出了岛沿，浅色主题里会画到顶栏上", islandRingInset)
	}
	if islandBodyInset < 2 {
		t.Fatalf("光带宽 %v 太细，读起来是描边而不是光", islandBodyInset)
	}
}

// 流光是一段弧，不是一圈边。
//
// 暗段只要带一点色，80% 的圆周就是一条描边；弧的两端必须回到纯墨，
// 否则绕回起点会出现一道接缝。
func TestIslandRingIsAnArcNotARing(t *testing.T) {
	prof := islandRingProfile
	if len(prof) < 2 {
		t.Fatal("色带没有剖面")
	}
	if prof[0].a != 0 || prof[len(prof)-1].a != 0 {
		t.Fatalf("剖面首尾不是纯墨：%v / %v", prof[0].a, prof[len(prof)-1].a)
	}
	first, last := float32(-1), float32(-1)
	for _, s := range prof {
		if s.a <= 0 {
			continue
		}
		if first < 0 {
			first = s.at
		}
		last = s.at
	}
	if first < 0 {
		t.Fatal("整条色带都是墨，岛上不会有光")
	}
	if span := last - first; span > 0.5 {
		t.Fatalf("亮的弧占圆周 %.0f%%，读起来是一圈边而不是一段光", span*100)
	}
}

// 岛面必须是一笔实色：所有停靠点都不透明。
//
// 只要有透明停靠点，岛沿就得靠另一层底色兜底——两层各自抗一次锯齿，
// 弧段上就会叠出比两侧都暗的边（见 TestIslandOutlineHasNoDarkFringe）。
func TestIslandSurfaceIsOneOpaqueFill(t *testing.T) {
	stops := islandGlowStops(1)
	if len(stops) < 2 {
		t.Fatal("岛面没有停靠点")
	}
	// MyGo 的渐变最多 32 个停靠点，多了会被静默截断——色带尾部会突然消失。
	if len(stops) > 32 {
		t.Fatalf("%d 个停靠点，超过 MyGo 的上限 32", len(stops))
	}
	for _, s := range stops {
		if s.Color.A != 255 {
			t.Fatalf("停靠点 at=%v 是透明的：岛面被拆成了两层", s.At)
		}
	}
	// 头部的颜色必须就是强调色本身；拖影与尾巴靠它和墨的混合比例表达。
	hot := theme.AccentOnInk
	found := false
	for _, s := range stops {
		if s.At == 0.30 {
			found = true
			if s.Color.R != hot.R || s.Color.G != hot.G || s.Color.B != hot.B {
				t.Fatalf("弧上最亮的点不是 AccentOnInk：%v vs %v", s.Color, hot)
			}
		}
	}
	if !found {
		t.Fatal("色带上没有 108° 那个头")
	}
}

// 收起态不画流光；首页和播放页展开态必须画。
func TestIslandRingStates(t *testing.T) {
	if ring, _ := islandRings(false, false, 1); ring == 0 {
		t.Fatal("首页时钟岛没有流光")
	}
	if ring, _ := islandRings(true, false, 0); ring == 0 {
		t.Fatal("播放页展开态没有流光")
	}
	if ring, _ := islandRings(true, false, 1); ring != 0 {
		t.Fatal("收起成正圆之后还在画流光")
	}
	if ring, breath := islandRings(false, true, 1); ring != 0 || breath == 0 {
		t.Fatalf("后台收听态：ring=%v breath=%v，应当是纯呼吸环", ring, breath)
	}
}

// 六套配色里，环都必须真的亮在岛的墨底上。
//
// 只断言「画了环」不够：浅色主题里 AccentInk 是压深过的橄榄绿，画上去
// 几乎和岛面同色。这里直接采环带内侧的像素，和对岛面的对比度说话。
func TestIslandRingIsVisibleInEveryPalette(t *testing.T) {
	defer theme.Apply("signal")
	for i := range theme.Palettes {
		p := &theme.Palettes[i]

		app := NewMockApp()
		// 视图每帧按 a.PaletteID 重写包级颜色，只调 theme.Apply 会被覆盖；
		// ThemeMode 还会把配色换成同色系的另一侧，两个都得设。
		app.PaletteID = p.ID
		if p.Dark {
			app.ThemeMode = themeModeDark
		} else {
			app.ThemeMode = themeModeLight
		}
		app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "直播中"}
		app.Router.Push(routePlayer)
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.Frame()

		b, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("%s: 灵动岛没有渲染出来", p.ID)
		}
		if got := islandBandContrast(tt.Image(), b); got < 4.0 {
			t.Fatalf("%s: 环对岛面最高只有 %.2f:1，低于 islandRingMin", p.ID, got)
		}
	}
}

// reduced motion 下光不转，但仍然要在。
func TestIslandRingHoldsStillWithReducedMotion(t *testing.T) {
	app := NewMockApp()
	app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "直播中"}
	app.Router.Push(routePlayer)
	tt := ui.NewTester(app.Shell, 1440, 900)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()

	b, ok := tt.Find("灵动岛")
	if !ok {
		t.Fatal("灵动岛没有渲染出来")
	}
	// 只比环带：播放页中央那颗取流转圈也在动，整图比较会把它算进来。
	before := islandBandPixels(tt.Image(), b)
	tt.Advance(700 * time.Millisecond)
	after := islandBandPixels(tt.Image(), b)
	if !bytes.Equal(before, after) {
		t.Fatal("reduced motion 下流光还在转")
	}
	if got := islandBandContrast(tt.Image(), b); got < 4.0 {
		t.Fatalf("reduced motion 下环消失了（最高对比度 %.2f:1）", got)
	}
}

// islandBandContrast 采样环带压在岛面上的那半圈，返回它和岛面的最高对比度。
//
// 采样范围是圆角矩形边界内侧 0.1–1.2px（SDF 为负的那一侧）：环的内半圈
// 落在那里；外半圈压在顶栏上，颜色不属于岛，不能算进来。
func islandBandContrast(img *image.RGBA, box ui.Rect) float64 {
	if img == nil || box.W <= 0 || box.H <= 0 {
		return 0
	}
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	hw, hh := box.W/2, box.H/2
	r := min(hw, hh)
	best := 0.0
	for y := int(box.Y) - 2; y <= int(box.Y+box.H)+2; y++ {
		for x := int(box.X) - 2; x <= int(box.X+box.W)+2; x++ {
			if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
				continue
			}
			d := roundedBoxSDF(float32(x)+0.5-cx, float32(y)+0.5-cy, hw, hh, r)
			if d < -1.2 || d > -0.1 {
				continue
			}
			px := img.RGBAAt(x, y)
			c := contrastRatio(ui.RGBA(px.R, px.G, px.B, float32(px.A)/255), theme.IslandInk)
			if c > best {
				best = c
			}
		}
	}
	return best
}

func roundedBoxSDF(x, y, hw, hh, r float32) float32 {
	qx := float32(math.Abs(float64(x))) - (hw - r)
	qy := float32(math.Abs(float64(y))) - (hh - r)
	out := float32(math.Hypot(float64(max(qx, 0)), float64(max(qy, 0))))
	return out + min(max(qx, qy), 0) - r
}

func contrastRatio(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if lb > la {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relativeLuminance(c ui.Color) float64 {
	f := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.R) + 0.7152*f(c.G) + 0.0722*f(c.B)
}

// islandBandPixels 取环带压在岛面上的那些像素，供逐帧比较。
func islandBandPixels(img *image.RGBA, box ui.Rect) []uint8 {
	if img == nil || box.W <= 0 || box.H <= 0 {
		return nil
	}
	cx, cy := box.X+box.W/2, box.Y+box.H/2
	hw, hh := box.W/2, box.H/2
	r := min(hw, hh)
	var out []uint8
	for y := int(box.Y) - 1; y <= int(box.Y+box.H)+1; y++ {
		for x := int(box.X) - 1; x <= int(box.X+box.W)+1; x++ {
			if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
				continue
			}
			d := roundedBoxSDF(float32(x)+0.5-cx, float32(y)+0.5-cy, hw, hh, r)
			if d < -1.2 || d > -0.1 {
				continue
			}
			i := y*img.Stride + x*4
			out = append(out, img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3])
		}
	}
	return out
}

// 岛的底色必须铺满整个圆角盒，不能只靠 1px 描边撑形状。
//
// 边只有 1px 时，body 的圆角裁剪会在胶囊两端按行露出顶栏：浅色主题里
// 就是岛沿一圈断断续续的白斑，深色主题里是一串灰点。
//
// 判据只看岛沿往里 2.5–4.5px 这一圈：环带占 0–2px，抗锯齿再吃 0.5px，
// 再往里就是纯底色——内容（头像从 4px 起、文字从 48px 起）够不着这里。
// 这一圈里出现顶栏色，就是裁剪漏了底。
func TestIslandEdgeIsInkNotBar(t *testing.T) {
	// 深色主题里顶栏和墨底只差 3 个色阶，这个判据没有分辨力，只跑浅色。
	for _, id := range []string{"daylight", "paper", "parchment"} {
		app := NewMockApp()
		app.PaletteID = id
		app.ThemeMode = themeModeLight
		app.Player = Streamer{Platform: "douyu", RoomID: "1", Name: "甲", Title: "直播中"}
		app.Router.Push(routePlayer)
		tt := ui.NewTester(app.Shell, 1440, 900)
		tt.Frame()

		b, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("%s: 灵动岛没有渲染出来", id)
		}
		img := tt.Image()
		cx, cy := b.X+b.W/2, b.Y+b.H/2
		hw, hh := b.W/2, b.H/2
		r := min(hw, hh)
		bar := img.RGBAAt(int(cx), int(b.Y)-8) // 岛正上方的顶栏
		same := func(c, ref uint8) bool {
			d := int(c) - int(ref)
			return d >= -6 && d <= 6
		}
		for y := int(b.Y) - 1; y <= int(b.Y+b.H)+1; y++ {
			for x := int(b.X) - 1; x <= int(b.X+b.W)+1; x++ {
				if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
					continue
				}
				d := roundedBoxSDF(float32(x)+0.5-cx, float32(y)+0.5-cy, hw, hh, r)
				if d < -4.5 || d > -2.5 {
					continue
				}
				px := img.RGBAAt(x, y)
				if same(px.R, bar.R) && same(px.G, bar.G) && same(px.B, bar.B) {
					t.Fatalf("%s: 岛沿 (%d,%d) 是顶栏色 %v，裁剪漏了底", id, x, y, bar)
				}
			}
		}
	}
}

// 拖影是一层，落后于头、比头淡，且带宽相同。
//
// 光晕不能靠「加宽」：试过外扩 3 / 6 DIP 的同心弧，浅色主题里外圈直接
// 画在顶栏白底上，paper 与 parchment 的弧等于消失，屏幕上又只剩岛内那
// 一条。也试过 Blur——MyGo 的模糊每帧重建离屏层，实测 2.4ms → 44.7ms/帧。
// 所以余晖只在同一个带宽里往身后拖。
func TestIslandRingEchoTrailsTheHead(t *testing.T) {
	if islandRingEchoLag <= 0 || islandRingEchoLag >= 45 {
		t.Fatalf("拖影落后 %v°，要么看不出两层，要么变成两道光", islandRingEchoLag)
	}
	if islandRingEchoAlpha <= 0 || islandRingEchoAlpha >= 0.5 {
		t.Fatalf("拖影不透明度 %v，必须明显淡于芯", islandRingEchoAlpha)
	}
}

// 岛沿不能出现一圈比两侧都暗的边。
//
// 岛是圆角胶囊：直边的抗锯齿像素正好落在像素格里，覆盖不是 1 就是 0；
// 弧段上则是半覆盖，而且覆盖沿整条圆周连续变化。如果墨底和流光各自抗
// 一次锯齿，边界像素就会是「光 × a + 墨 × 墨覆盖 + 顶栏 × 余下」——三种
// 颜色叠在一起，比环带和顶栏都暗。浅色主题里最明显：黑岛白栏之间那圈
// 弧被描了一道黑边。
//
// 判据不看绝对颜色：岛沿像素必须落在「环带内侧一点」和「岛外一点」这
// 两点的连线上（逐通道，允许 ±14——亚像素取整、阴影、顶部受光边都在
// 这个量级）。比两端都暗，就是有第三种颜色混了进来。
func TestIslandOutlineHasNoDarkFringe(t *testing.T) {
	defer theme.Apply("signal")
	for i := range theme.Palettes {
		p := &theme.Palettes[i]
		app := NewMockApp()
		app.PaletteID = p.ID
		if p.Dark {
			app.ThemeMode = themeModeDark
		} else {
			app.ThemeMode = themeModeLight
		}
		tt := ui.NewTester(app.Shell, 1440, 900)
		// reduced motion：角度停在固定值，热段落在右侧圆弧上——正是弧形
		// 部分出问题的那一段。
		tt.SetPreferences(ui.Preferences{ReduceMotion: true})
		tt.Frame()

		b, ok := tt.Find("灵动岛")
		if !ok {
			t.Fatalf("%s: 灵动岛没有渲染出来", p.ID)
		}
		img := tt.Image()
		cx, cy := b.X+b.W/2, b.Y+b.H/2
		hw, hh := b.W/2, b.H/2
		r := min(hw, hh)
		sdf := func(x, y float32) float32 { return roundedBoxSDF(x-cx, y-cy, hw, hh, r) }
		// 沿岛沿法线取样：内侧 0.9px 是环带，外侧 1.6px 是顶栏。
		blend := func(fx, fy float32) color.RGBA {
			x0, y0 := int(math.Floor(float64(fx))), int(math.Floor(float64(fy)))
			tx, ty := fx-float32(x0), fy-float32(y0)
			mix := func(c00, c10, c01, c11 uint8) uint8 {
				a := float32(c00)*(1-tx) + float32(c10)*tx
				c := float32(c01)*(1-tx) + float32(c11)*tx
				return uint8(a*(1-ty) + c*ty + 0.5)
			}
			a, b := img.RGBAAt(x0, y0), img.RGBAAt(x0+1, y0)
			c, d := img.RGBAAt(x0, y0+1), img.RGBAAt(x0+1, y0+1)
			return color.RGBA{R: mix(a.R, b.R, c.R, d.R), G: mix(a.G, b.G, c.G, d.G),
				B: mix(a.B, b.B, c.B, d.B), A: 255}
		}
		pick := func(x, y int, off float32) color.RGBA {
			gx := sdf(float32(x)+0.5+0.25, float32(y)+0.5) - sdf(float32(x)+0.5-0.25, float32(y)+0.5)
			gy := sdf(float32(x)+0.5, float32(y)+0.5+0.25) - sdf(float32(x)+0.5, float32(y)+0.5-0.25)
			n := float32(math.Hypot(float64(gx), float64(gy)))
			if n == 0 {
				return img.RGBAAt(x, y)
			}
			return blend(float32(x)+0.5+gx/n*off, float32(y)+0.5+gy/n*off)
		}
		between := func(v, a, c uint8) bool {
			lo, hi := int(min(a, c))-14, int(max(a, c))+14
			return int(v) >= lo && int(v) <= hi
		}
		for y := int(b.Y) - 2; y <= int(b.Y+b.H)+2; y++ {
			for x := int(b.X) - 2; x <= int(b.X+b.W)+2; x++ {
				if x < 1 || y < 1 || x >= img.Rect.Dx()-1 || y >= img.Rect.Dy()-1 {
					continue
				}
				d := sdf(float32(x)+0.5, float32(y)+0.5)
				if d < -0.6 || d > 0.4 {
					continue
				}
				edge := img.RGBAAt(x, y)
				in := pick(x, y, -0.9)
				out := pick(x, y, 1.6)
				if !between(edge.R, in.R, out.R) || !between(edge.G, in.G, out.G) ||
					!between(edge.B, in.B, out.B) {
					t.Fatalf("%s: 岛沿 (%d,%d) 是 %v，两侧是 %v / %v：混进了第三种颜色",
						p.ID, x, y, edge, in, out)
				}
			}
		}
	}
}
