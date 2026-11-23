package ui

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/watch"
)

// 观测墙的画布尺寸必须是精确的 16:9。
//
// mpv 按这个尺寸开画布：比例只要差一个像素，它就把 16:9 的流补成黑边，
// 上屏再 Contain 回 16:9 的格子，黑边就被放大到格子四边上。日间模式下
// 每格一圈黑线非常显眼，而且只在某些窗口宽度下出现——因为尺寸是从格子
// 的浮点宽高算出来的。
func TestWatchRenderTargetIsExactSixteenNine(t *testing.T) {
	for _, w := range []float32{60, 120, 160, 240, 320, 400, 504.2, 664.9, 764, 900, 1200} {
		tw, th := watchSize(watch.Rect{W: w, H: w / watch.DefaultAspect})
		if tw*9 != th*16 {
			t.Fatalf("格子宽 %.1f → 画布 %dx%d，比例不是 16:9", w, tw, th)
		}
		if tw%2 != 0 || th%2 != 0 {
			t.Fatalf("格子宽 %.1f → 画布 %dx%d，不是偶数", w, tw, th)
		}
		if tw > 1280 || th > 720 || tw < 320 || th < 180 {
			t.Fatalf("格子宽 %.1f → 画布 %dx%d，超出 320×180–1280×720", w, tw, th)
		}
	}
}

// solidClip 造一个 w×h 的纯色解码器，冒充 mpv 那一帧。
func solidClip(t *testing.T, c color.RGBA, w, h int) *ui.VideoPlayer {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{c}))
	g.Delay = append(g.Delay, 100)
	g.Disposal = append(g.Disposal, gif.DisposalNone)
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	d, err := ui.NewGIFDecoder(b.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	p, err := ui.NewVideoPlayer(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// requireTilesCovered 逐格检查边框向内两个物理像素：画面必须铺满，
// 露出一像素的底色就是一条黑边。
func requireTilesCovered(t *testing.T, tt *ui.Tester, rooms []Streamer, scale float32) {
	t.Helper()
	img := tt.Image()
	for i, room := range rooms {
		r, ok := tt.Find(watchTileLabel(room))
		if !ok {
			t.Fatalf("找不到第 %d 格", i)
		}
		x0, y0 := int(r.X*scale), int(r.Y*scale)
		x1, y1 := int((r.X+r.W)*scale), int((r.Y+r.H)*scale)
		// 圆角处本来就该看到页面底色，避开四个角。
		inset := int(24 * scale)
		var bad []string
		check := func(x, y int) {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				return
			}
			if c := img.RGBAAt(x, y); c.R < 200 {
				bad = append(bad, fmt.Sprintf("(%d,%d)=%v", x, y, c))
			}
		}
		for d := range 2 {
			for x := x0 + inset; x < x1-inset; x++ {
				check(x, y0+d)
				check(x, y1-1-d)
			}
			for y := y0 + inset; y < y1-inset; y++ {
				check(x0+d, y)
				check(x1-1-d, y)
			}
		}
		if len(bad) > 0 {
			n := len(bad)
			if n > 3 {
				bad = bad[:3]
			}
			t.Errorf("第 %d 格 %.1fx%.1f @%.1f,%.1f 边缘露出底色 %d 处：%v",
				i, r.W, r.H, r.X, r.Y, n, bad)
		}
	}
}

// 观测墙每格的画面必须盖满格子，不能露出底下那层视频井的黑。
//
// 画面尺寸走生产路径的 watchSize，和真机上 mpv 拿到的画布一致；窗口扫几组
// 尺寸和缩放，是因为落点是不是整数物理像素取决于这两者。
func TestWatchWallTilesCoverTheirEdges(t *testing.T) {
	for _, scale := range []float32{1, 1.5} {
		for _, size := range [][2]int{{1440, 900}, {1572, 1068}} {
			t.Run(fmt.Sprintf("s%.2f_%dx%d", scale, size[0], size[1]), func(t *testing.T) {
				t.Setenv("AppData", t.TempDir())
				rooms := watchTestRooms(3)
				app := NewMockApp()
				app.WatchRooms = rooms
				app.Router = ui.NewRouter(routeFollows)
				app.FollowsSection = followsSectionWatch
				tt := ui.NewTester(app.Shell, size[0], size[1])
				tt.SetScale(scale)
				tt.Frame()
				tt.Advance(time.Millisecond)
				tt.Frame()

				for _, room := range rooms {
					r, ok := tt.Find(watchTileLabel(room))
					if !ok {
						t.Fatalf("找不到 %s", watchTileLabel(room))
					}
					w, h := watchSize(watch.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H})
					key := watchKey(room)
					app.watch.sessions[key] = &roomSession{
						key: key, room: room,
						player: solidClip(t, color.RGBA{255, 0, 0, 255}, w, h),
					}
				}
				tt.Frame()
				tt.Advance(16 * time.Millisecond)
				tt.Frame()
				requireTilesCovered(t, tt, rooms, scale)
			})
		}
	}
}
