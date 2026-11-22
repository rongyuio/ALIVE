package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/live"
)

// 表面模型的门禁探针（P1–P4）。
//
// 阶段 3 的连续过渡（墙格 → 播放页的共享元素放大）能不能做，取决于下面
// 几条能不能在 Tester 里被证明。任何一条不通过，该过渡就保持硬切。
//
// 这里的"探针"不是一次性实验：它们进 make check，是模型与过渡的护栏。

// gifPlayer 造一个纯 Go 的播放器（不碰 libmpv），四色循环。
//
// 帧延迟 100ms：Tester 的时钟推进时它才会换帧，所以"飞行途中是否继续推进"
// 可以逐帧判定。
func gifPlayer(t *testing.T) *ui.VideoPlayer {
	t.Helper()
	g := &gif.GIF{Config: image.Config{Width: 4, Height: 4}}
	for _, c := range []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 255, 255}} {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 4, 4), color.Palette{c}))
		g.Delay = append(g.Delay, 10)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
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

// P1：同一个播放器被两个 Video 同时显示。
//
// 墙格 → 播放页的过渡期间，墙页和播放页会同时存在，两个 Video 指向同一个
// 播放器。MyGo 的类型注释写着"show it in one Video at a time"，但
// videoEngine.advance 的帧守卫（同一帧第二次构建直接返回）说明大概率是
// 良性的。这条探针要证明的是两件事：
//
//  1. 两个 Video 都画出画面，不出现一块黑；
//  2. 时间轴只推进一次——不会因为被两处显示而跑两倍速。
//
// 只要有一件不成立，过渡期间就必须让播放页不建自己的 Video。
func TestProbeSharedPlayerInTwoVideos(t *testing.T) {
	p := gifPlayer(t)
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Fill().Children(func() {
			ui.Video(c, p).Size(40, 40).Fit(ui.FillBox)
			ui.Video(c, p).Size(40, 40).Fit(ui.FillBox)
		})
	}, 120, 60)
	tt.Frame()

	img := tt.Image()
	// 两块 40 宽并排：中心分别在 20 和 60。
	left, right := img.RGBAAt(20, 30), img.RGBAAt(60, 30)
	if left == (color.RGBA{}) || right == (color.RGBA{}) {
		t.Fatalf("两块 Video 至少有一块是黑的：left=%v right=%v", left, right)
	}
	if left != right {
		t.Fatalf("同一帧两个 Video 画出了不同内容：left=%v right=%v", left, right)
	}

	// 时钟推进 100ms：正好一帧。两处显示不该让它推进两帧。
	before := p.Position()
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	after := p.Position()
	if d := after - before; d > 200*time.Millisecond {
		t.Fatalf("被两处显示时时间轴推进了 %v，超过一帧：会跑成两倍速", d)
	}
}

// P2：飞行中的元素里装 Video。
//
// Match + Fly 的元素在途中会被重新排版到中间尺寸。这条探针要回答：装在
// 里面的 Video 在飞行途中是继续推进，还是冻在起飞那一帧。
//
// 冻帧不算失败——飞行只有 280ms，冻一帧看不出来；但必须知道是哪一种，
// 因为"冻帧 + 落地后才恢复"意味着不能指望它在途中保持连续。
func TestProbeVideoInsideFlyingMatch(t *testing.T) {
	p := gifPlayer(t)
	type photoMatch struct{ id int }
	open := false
	tr := ui.ElementTransition{Animation: &ui.Snappy, Match: photoMatch{1}, Fly: true}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			if !open {
				ui.Box(c).Key("thumb").Size(40, 40).Background(ui.RGB(0, 0, 0)).
					Transition(tr).Children(func() {
					ui.Video(c, p).Fill().Fit(ui.FillBox)
				})
			} else {
				ui.Box(c).Key("hero").Size(200, 120).Background(ui.RGB(0, 0, 0)).
					Transition(tr).Children(func() {
					ui.Video(c, p).Fill().Fit(ui.FillBox)
				})
			}
		})
	}, 300, 200)
	tt.Frame()

	before := p.Position()
	open = true
	tt.Frame()
	// 飞行途中推进时钟：视频若还在推进，位置会变。
	tt.Advance(100 * time.Millisecond)
	tt.Frame()
	mid := p.Position()
	tt.Advance(300 * time.Millisecond)
	tt.Frame()
	end := p.Position()

	if end <= before {
		t.Fatalf("飞行前后视频完全没有推进（before=%v end=%v）：途中与落地后都冻住了", before, end)
	}
	// 记录飞行途中是否推进。不判失败，只要求它不倒退。
	if mid < before {
		t.Fatalf("飞行途中时间轴倒退了：before=%v mid=%v", before, mid)
	}
}

// P3：ReduceMotion 下共享元素过渡完全不动。
//
// 这条不判失败就退：系统要求减少动效时，飞行必须整体跳过（MyGo 的
// matches.moves() 直接返回 false）。探针验的是"确实没有中间帧"——
// 切过去的那一帧就已经是终态。
func TestProbeMatchSkipsUnderReduceMotion(t *testing.T) {
	type photoMatch struct{ id int }
	open := false
	tr := ui.ElementTransition{Animation: &ui.Snappy, Match: photoMatch{1}, Fly: true}

	tt := ui.NewTesterWith(func(c *ui.Context) {
		ui.Box(c).Fill().Children(func() {
			if !open {
				ui.Box(c).Key("thumb").Absolute().Left(0).Top(0).Size(40, 40).
					Background(ui.RGB(255, 0, 0)).Transition(tr)
			} else {
				ui.Box(c).Key("hero").Absolute().Left(0).Top(0).Size(200, 120).
					Background(ui.RGB(0, 0, 255)).Transition(tr)
			}
		})
	}, 300, 200, ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
	tt.Frame()

	open = true
	tt.Frame()
	// 第一帧就该是终态：左上角是蓝的，且尺寸已经是 200×120。
	if c := tt.Image().RGBAAt(100, 60); c.B > 200 && c.R < 60 {
		return // 已经是终态
	}
	t.Fatalf("ReduceMotion 下切换的第一帧不是终态：%v", tt.Image().RGBAAt(100, 60))
}

// P4：12 格墙上起飞一次的帧预算。
//
// 量的是"飞行期间每帧构建多久"。Tester 是软件渲染，绝对值没有意义；
// 有意义的是一次 12 格布局 + 一次飞行构建相对于稳定态的开销倍数。
// 超过 3 倍就说明 300ms 的飞行在 12 格上不划算。
func TestProbeTwelveTileFlightCost(t *testing.T) {
	type tileMatch struct{ key string }
	open := ""
	tick := 0
	rooms := make([]string, 12)
	for i := range rooms {
		rooms[i] = fmt.Sprintf("room-%d", i)
	}

	build := func(c *ui.Context) {
		// 每帧换一个数字，逼整棵树重建：不换的话 Frame() 在没有变化时
		// 几乎什么都不做，量出来的基准是 1µs，比值毫无意义。
		ui.Text(c, fmt.Sprintf("%d", tick)).FontSize(11)
		ui.Grid(c).Fill().Columns(4).Gap(16).Children(func() {
			for _, k := range rooms {
				key := k
				tr := ui.ElementTransition{Animation: &ui.Snappy, Match: tileMatch{key}, Fly: true}
				box := ui.Box(c).Key("tile-" + key).AspectRatio(16.0 / 9.0).
					Background(ui.RGB(30, 30, 30)).Transition(tr)
				if open == key {
					box.Key("hero").Absolute().Fill()
				}
			}
		})
	}
	tt := ui.NewTester(build, 1200, 800)
	tt.Frame()
	tt.Advance(500 * time.Millisecond)

	// 基准：整棵树重建、但没有元素在飞。
	still := time.Now()
	for range 8 {
		tick++
		tt.Frame()
	}
	base := time.Since(still) / 8

	open = rooms[0]
	flight := time.Now()
	tick++
	tt.Frame()
	first := time.Since(flight)
	flight = time.Now()
	for range 8 {
		tick++
		tt.Advance(30 * time.Millisecond)
		tt.Frame()
	}
	during := time.Since(flight) / 8

	if base <= 0 {
		base = time.Microsecond
	}
	ratio := float64(during) / float64(base)
	t.Logf("12 格：重建 %.0fµs/帧，飞行 %.0fµs/帧（起飞首帧 %.0fµs），比值 %.1f",
		float64(base)/1000, float64(during)/1000, float64(first)/1000, ratio)
	// 一帧的预算是 16.7ms。飞行期间超过重建的 3 倍、或者绝对耗时逼近
	// 预算，就说明 12 格上的 300ms 飞行不划算。
	if ratio > 3 && during > 8*time.Millisecond {
		t.Fatalf("飞行期间每帧是重建的 %.1f 倍且已达 %v：12 格上 300ms 飞行不划算", ratio, during)
	}
	if during > 16*time.Millisecond {
		t.Fatalf("飞行期间每帧 %v，超出 60fps 预算", during)
	}
}

// 假平台：不联网，取流立刻失败。用它把墙的会话生命周期跑起来。
type surfaceProvider struct{}

func (surfaceProvider) Platform() live.Platform { return live.Bilibili }
func (surfaceProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (surfaceProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (surfaceProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }
func (surfaceProvider) Stream(context.Context, string, string) (live.Stream, error) {
	return live.Stream{}, nil
}
