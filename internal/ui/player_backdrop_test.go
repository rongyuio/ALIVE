package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"testing"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// 黑边几何是按 contain 的等比算法来的：视频更宽就上下留边，更高就左右留边。
// 这条函数是「要不要开模糊底」的唯一判据，所以单独表驱动，不经过渲染。
func TestLetterbox(t *testing.T) {
	cases := []struct {
		name                           string
		stageW, stageH, videoW, videoH float32
		top, right, bottom, left       float32
	}{
		{"同比例不留边", 1600, 900, 1920, 1080, 0, 0, 0, 0},
		{"16:9 视频进 4:3 舞台", 400, 300, 1920, 1080, 37.5, 0, 37.5, 0},
		{"竖屏视频进宽舞台", 400, 300, 1080, 1920, 0, 115.625, 0, 115.625},
		{"取整余量不足阈值", 1412, 794, 1920, 1080, 0, 0.222229, 0, 0.222229},
		{"没有尺寸", 0, 0, 1920, 1080, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			top, right, bottom, left := letterbox(tc.stageW, tc.stageH, tc.videoW, tc.videoH)
			if top != tc.top || right != tc.right || bottom != tc.bottom || left != tc.left {
				t.Fatalf("letterbox = (%v,%v,%v,%v)，期望 (%v,%v,%v,%v)",
					top, right, bottom, left, tc.top, tc.right, tc.bottom, tc.left)
			}
			want := max(max(tc.top, tc.bottom), max(tc.left, tc.right)) >= theme.MediaBackdropMinBar
			if got := backdropNeeded(tc.stageW, tc.stageH, tc.videoW, tc.videoH); got != want {
				t.Fatalf("backdropNeeded = %v，期望 %v", got, want)
			}
		})
	}
}

// 有黑边时，黑边区铺的是那张静态底图，不是纯黑。
//
// 这条就是用户报的那件事：拉动窗口之后两侧/上下是纯黑。判据取的是像素：
// 纯红底图在 4:3 舞台上左右各留 50 宽的黑边，那里应该是暗红（被压暗过），
// 而中间 contain 出来的是原色红。
func TestStageBackdropFillsLetterbox(t *testing.T) {
	useBackdropBitmap(t, solidBitmap(4, 4, color.RGBA{255, 0, 0, 255}))
	app := NewMockApp()
	app.Player = Streamer{Name: "测试", CoverURL: "https://example.invalid/cover.jpg"}
	p := gifPlayer(t)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Size(400, 300).Background(ui.RGB(0, 0, 0)).Children(func() {
			app.stageBackdrop(c, p, nil)
			ui.Video(c, p).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Contain)
		})
	}, 400, 300)
	tt.Frame()

	img := tt.Image()
	// 左黑边里取一点：不能是纯黑，也不能是画面原色（压暗过）。
	r, g, b, _ := img.At(10, 150).RGBA()
	bar := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	if bar[0] == 0 && bar[1] == 0 && bar[2] == 0 {
		t.Fatalf("黑边区还是纯黑 %v：静态底图没有画出来", bar)
	}
	if bar[0] >= 250 {
		t.Fatalf("黑边区是底图原色 %v：压暗层没有生效", bar)
	}
	// 中间是 contain 出来的画面，没被压暗。
	cr, cg, cb, _ := img.At(200, 150).RGBA()
	if center := [3]int{int(cr >> 8), int(cg >> 8), int(cb >> 8)}; center[0] < 240 || center[1] > 10 || center[2] > 10 {
		t.Fatalf("中间画面被背景层盖住了：%v", center)
	}
}

// useBackdropBitmap 把背景图取用换成本地一张图：测试不联网、不留后台请求。
func useBackdropBitmap(t *testing.T, bmp *ui.Bitmap) {
	t.Helper()
	old := backdropBitmap
	backdropBitmap = func(string, float32) *ui.Bitmap { return bmp }
	t.Cleanup(func() { backdropBitmap = old })
}

// solidBitmap 造一张纯色底图，当黑边底用。
func solidBitmap(w, h int, c color.RGBA) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return ui.NewBitmap(img)
}

// 比例一致时不该开模糊底：那条路上多画一层全屏模糊是纯浪费。
//
// 判据只能看纯函数——画面上黑边为 0，画没画结果一样；所以这里守的是
// 「判据本身不误判」，渲染成本由真实窗口的帧统计兜底。
func TestBackdropSkippedWhenAspectMatches(t *testing.T) {
	if backdropNeeded(1412, 794, 1920, 1080) {
		t.Fatal("16:9 视频进 16:9 舞台（含 0.25 DIP 取整余量）仍然开了模糊底")
	}
}

// 窗口刚变过的那一帧，布局还是旧的——但底必须已经在。
//
// 不加这条保护，拖窗跨过「比例刚好一致」那一刻时会有一帧纯黑：旧布局说
// 没黑边、新画面已经有黑边了。一帧 16ms，正好落在可见的「闪一下」里。
func TestBackdropDrawnOnResizeFrame(t *testing.T) {
	app := NewMockApp()
	p := gifPlayerSize(t, 32, 18)
	// 暂停：不然 GIF 每帧都申请下一帧，Tester 的 settle 会一次跑好几帧，
	// 量到的就不是「拉窗后的第一帧」了。
	p.Pause()
	app.media.session = &roomSession{player: p}
	app.Player = Streamer{Name: "测试", CoverURL: "https://example.invalid/cover.jpg"}
	useBackdropBitmap(t, solidBitmap(4, 4, color.RGBA{0, 200, 0, 255}))
	tt := ui.NewTester(func(c *ui.Context) {
		stage := ui.Box(c).Fill().Background(ui.RGB(0, 0, 0))
		stage.Children(func() { app.stageFrame(c, stage) })
	}, 400, 225)
	tt.Frame()
	tt.Frame() // 400×225 舞台 + 16:9 视频：正好铺满，稳态不画

	// SetSize 自己就跑了一帧：这一帧窗口已经是 300，布局还是 225。
	// 不能再 Frame 一次——那是下一帧，布局已经更新，量不到要测的那一帧。
	tt.SetSize(400, 300)

	img := tt.Image()
	r, g, b, _ := img.At(200, 10).RGBA()
	if top := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}; top[0] == 0 && top[1] == 0 && top[2] == 0 {
		t.Fatalf("拉窗后的第一帧黑边区是纯黑 %v：按旧布局判了黑边", top)
	}
}

// gifPlayerSize 是一个 w×h 的循环 GIF 播放器，帧色固定，用来量像素。
func gifPlayerSize(t *testing.T, w, h int) *ui.VideoPlayer {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.RGBA{255, 0, 0, 255}}))
	g.Delay = append(g.Delay, 10)
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

// stageFrame 真的会把模糊底画进舞台：走的是「上一帧布局」那条路。
//
// 上面那条直接调 stageBackdrop，验不到 LayoutBounds 的接线。这里用真
// stageFrame、真舞台尺寸跑两帧：第一帧没有上一帧的布局、不画；第二帧
// 拿到尺寸之后黑边区就该有内容。窗口拉宽拉高时黑边跟着变，但底一直在。
func TestStageFrameDrawsBackdrop(t *testing.T) {
	app := NewMockApp()
	app.media.session = &roomSession{player: gifPlayerSize(t, 32, 18)}
	app.Player = Streamer{Name: "测试", CoverURL: "https://example.invalid/cover.jpg"}
	useBackdropBitmap(t, solidBitmap(4, 4, color.RGBA{0, 200, 0, 255}))
	tt := ui.NewTester(func(c *ui.Context) {
		stage := ui.Box(c).Size(400, 300).Background(ui.RGB(0, 0, 0))
		stage.Children(func() { app.stageFrame(c, stage) })
	}, 400, 300)
	tt.Frame() // 布局
	tt.Frame() // 有上一帧的 Bounds，该画了

	img := tt.Image()
	r, g, b, _ := img.At(200, 10).RGBA()
	top := [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
	if top[0] == 0 && top[1] == 0 && top[2] == 0 {
		t.Fatalf("上下黑边区还是纯黑 %v：stageFrame 没有接上模糊底", top)
	}
}

// 参数必须和 B 站播放器 bundle 里的原文对得上。
//
// 这不是「我觉得 35 好看」——2026-11-23 从
// s1.hdslb.com/bfs/static/bilibili-live-player/room-player.prod.min.js 里读到
// 的原文是：
//
//	filter: blur(35px);   transform: scale(1.2);   background: rgba(0,0,0,.5)
//
// 改这三个数之前先回去看一遍源码，别凭手感调。
func TestBackdropTokensMatchBilibili(t *testing.T) {
	if theme.MediaBackdropBlur != 35 {
		t.Errorf("模糊半径 %v，B 站是 blur(35px)", theme.MediaBackdropBlur)
	}
	if theme.MediaBackdropOverscan != 0.2 {
		t.Errorf("过扫 %v，B 站是 scale(1.2)（即外扩 20%%）", theme.MediaBackdropOverscan)
	}
	// rgba(0,0,0,.5) → 255×0.5 取整是 128（±1 给取整方式留余地）。
	if got := theme.MediaBackdropScrim.A; got < 127 || got > 128 {
		t.Errorf("压暗 alpha %d，B 站是 rgba(0,0,0,.5)（≈128）", got)
	}
}
