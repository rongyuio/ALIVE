package ui

import (
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// probe 逐帧采样一个视口：被监视元素的 bounds、指定区域的像素差。
//
// 它只记录，不判断；断言方法读记录，失败时打印整张帧表。这张表就是
// 截图想给的全部信息，而且带帧号、带数值、可重复。
//
// 只比 region 之内的像素：顶栏的灵动岛有常驻动画，全窗比对永远不静止
// （见 doc/reference/acceptance.md，「--settle 基本等不到」）。
type probe struct {
	t      *testing.T
	tt     *ui.Tester
	names  []string
	region ui.Rect
	step   time.Duration
	recs   []rec
	prev   *image.RGBA
}

type rec struct {
	frame   int
	at      time.Duration
	boxes   map[string]ui.Rect
	missing []string
	changed int // 与上一帧在 region 内不同的像素数；-1 表示第一帧没有参照
}

// newProbe 在当前位置采第 0 帧。调用前视图应该已经 Frame 过。
func newProbe(t *testing.T, tt *ui.Tester, region ui.Rect, step time.Duration, names ...string) *probe {
	t.Helper()
	p := &probe{t: t, tt: tt, region: region, step: step, names: names}
	p.sample()
	return p
}

// sample 读最后一帧。视图外改了状态时先调 Frame()，这是 Tester 的规矩。
func (p *probe) sample() {
	r := rec{frame: len(p.recs), boxes: map[string]ui.Rect{}, changed: -1}
	if n := len(p.recs); n > 0 {
		r.at = p.recs[n-1].at + p.step
	}
	for _, n := range p.names {
		box, ok := p.tt.Find(n)
		if !ok {
			r.missing = append(r.missing, n)
			continue
		}
		r.boxes[n] = box
	}

	img := p.tt.Image()
	if p.prev != nil {
		r.changed = diffRect(p.prev, img, p.region)
	}
	p.prev = img
	p.recs = append(p.recs, r)
}

// Step 推进一格时钟并采样一帧。没东西在动时 Advance 不跑帧，画面理应不变。
func (p *probe) Step() {
	p.tt.Advance(p.step)
	p.sample()
}

// Run 连续采样 n 格。
func (p *probe) Run(n int) {
	for range n {
		p.Step()
	}
}

// shift 是一次帧间位移。
type shift struct {
	frame  int
	name   string
	from   ui.Rect
	to     ui.Rect
	dx, dy float32
}

// shifts 列出所有超过容差的帧间位移。
//
// 单独暴露出来是为了让探针自己可测：合成一个会动的视图，断言它确实
// 被抓住。断言方法只是对这份列表的包装。
func (p *probe) shifts(tol float32) []shift {
	var out []shift
	for i := 1; i < len(p.recs); i++ {
		// 按 names 的顺序走，不遍历 map：多个元素同时动时，报告的第一个
		// 位移必须是稳定的，否则同一份代码每次跑出不同的失败信息。
		for _, n := range p.names {
			box, ok := p.recs[i].boxes[n]
			if !ok {
				continue
			}
			prev, ok := p.recs[i-1].boxes[n]
			if !ok {
				continue
			}
			dx := float32(math.Abs(float64(box.X - prev.X)))
			dy := float32(math.Abs(float64(box.Y - prev.Y)))
			if dx > tol || dy > tol {
				out = append(out, shift{frame: i, name: n, from: prev, to: box, dx: dx, dy: dy})
			}
		}
	}
	return out
}

// RequireNoShift 要求同名元素在相邻两帧之间不动。
//
// 容差 0.01 DIP：远小于一个 4x 下的物理像素，任何肉眼能看见的位移都会红。
// 只比较两帧都在场的元素——换代时旧一屏消失、新一屏出现，不是位移。
func (p *probe) RequireNoShift(tol float32) {
	p.t.Helper()
	if s := p.shifts(tol); len(s) > 0 {
		p.fail(s[0].frame, "%s 从 (%.2f,%.2f) 挪到 (%.2f,%.2f)，Δ=(%.2f,%.2f) DIP",
			s[0].name, s[0].from.X, s[0].from.Y, s[0].to.X, s[0].to.Y, s[0].dx, s[0].dy)
	}
}

// changesAfter 列出第 frame 帧之后所有仍在变化的像素数。
func (p *probe) changesAfter(frame int) []int {
	var out []int
	for i := frame + 1; i < len(p.recs); i++ {
		if p.recs[i].changed != 0 {
			out = append(out, p.recs[i].changed)
		}
	}
	return out
}

// RequireStillAfter 要求第 frame 帧之后，region 内一个像素都不再变。
//
// 这是「没有淡入 / 没有呼吸 / 没有循环动画」的判据：稳态之后的任何变化
// 都会继续请求帧，用户看到的就是闪。
func (p *probe) RequireStillAfter(frame int) {
	p.t.Helper()
	if frame < 0 || frame >= len(p.recs) {
		p.t.Fatalf("RequireStillAfter(%d)：只有 %d 帧", frame, len(p.recs))
	}
	for i := frame + 1; i < len(p.recs); i++ {
		if p.recs[i].changed != 0 {
			p.fail(i, "稳态（第 %d 帧）之后 region 内仍有 %d 个像素在变", frame, p.recs[i].changed)
		}
	}
}

// RequireAlwaysPresent 要求监视的元素每一帧都在场（不闪现）。
func (p *probe) RequireAlwaysPresent() {
	p.t.Helper()
	for _, r := range p.recs {
		if len(r.missing) > 0 {
			p.fail(r.frame, "这些元素缺席：%v", r.missing)
		}
	}
}

// fail 打印帧表后终止：帧号、元素、坐标、像素差，取代截图。
func (p *probe) fail(frame int, format string, args ...any) {
	p.t.Helper()
	p.t.Errorf("帧 %d："+format, append([]any{frame}, args...)...)
	p.t.Logf("帧\t时刻\t元素\tX\tY\tW\tH\t像素差")
	for _, r := range p.recs {
		for _, n := range p.names {
			box, ok := r.boxes[n]
			if !ok {
				p.t.Logf("%d\t%s\t%s\t—\t—\t—\t—\t%d", r.frame, r.at, n, r.changed)
				continue
			}
			p.t.Logf("%d\t%s\t%s\t%.2f\t%.2f\t%.2f\t%.2f\t%d",
				r.frame, r.at, n, box.X, box.Y, box.W, box.H, r.changed)
		}
	}
	p.t.FailNow()
}

// contentRect 取一组可见卡片的外接矩形，作为像素比对的区域。
// 找不到（还没渲染）的元素跳过；一个都没有就说明场景没搭对。
func contentRect(t *testing.T, tt *ui.Tester, names ...string) ui.Rect {
	t.Helper()
	var (
		out   ui.Rect
		found bool
	)
	for _, n := range names {
		r, ok := tt.Find(n)
		if !ok || r.W <= 0 || r.H <= 0 {
			continue
		}
		if !found {
			out, found = r, true
			continue
		}
		x0 := math.Min(float64(out.X), float64(r.X))
		y0 := math.Min(float64(out.Y), float64(r.Y))
		x1 := math.Max(float64(out.X+out.W), float64(r.X+r.W))
		y1 := math.Max(float64(out.Y+out.H), float64(r.Y+r.H))
		out = ui.Rect{X: float32(x0), Y: float32(y0), W: float32(x1 - x0), H: float32(y1 - y0)}
	}
	if !found {
		t.Fatalf("内容层一个元素都没找到：%v", names)
	}
	return out
}

// diffRect 数两张图在 r 之内有多少个像素不同（RGBA 任一通道不等）。
func diffRect(a, b *image.RGBA, r ui.Rect) int {
	if a == nil || b == nil {
		return -1
	}
	x0 := int(math.Floor(float64(r.X)))
	y0 := int(math.Floor(float64(r.Y)))
	x1 := int(math.Ceil(float64(r.X + r.W)))
	y1 := int(math.Ceil(float64(r.Y + r.H)))
	area := image.Rect(x0, y0, x1, y1).Intersect(a.Bounds()).Intersect(b.Bounds())
	n := 0
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			if a.RGBAAt(x, y) != b.RGBAAt(x, y) {
				n++
			}
		}
	}
	return n
}

// diffImages 数两个整帧之间不同的像素数。
func diffImages(a, b *image.RGBA) int {
	if a == nil || b == nil {
		return -1
	}
	bounds := a.Bounds().Intersect(b.Bounds())
	return diffRect(a, b, ui.Rect{
		X: float32(bounds.Min.X), Y: float32(bounds.Min.Y),
		W: float32(bounds.Dx()), H: float32(bounds.Dy()),
	})
}

// cardNames 返回前 n 张卡片的无障碍名字，用来搭 probe 和比对区域。
func cardNames(rooms []Streamer, n int) []string {
	if n > len(rooms) {
		n = len(rooms)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = "直播间:" + rooms[i].Name
	}
	return names
}

// 同一状态连续重绘两帧，字节必须完全相同。
//
// 这是整套像素判据的前提：Image() 由软件光栅器产出，没有随机源和时钟。
// 这条红了说明光栅化不再确定，后面所有「0 像素差」的断言都不成立。
func TestProbePixelsAreDeterministic(t *testing.T) {
	a := stillApp()
	a.Rooms = stillRooms(24)
	tt := ui.NewTester(a.Home, 1391, 845)
	tt.Frame()

	first := tt.Image()
	tt.Frame()
	second := tt.Image()

	if d := diffImages(first, second); d != 0 {
		t.Fatalf("同一状态重绘，两帧差 %d 个像素：像素判据不可用", d)
	}
}

// 探针自己必须能被证明是灵敏的。
//
// 合成一个会动的视图：方块 200ms 内下移 60 DIP、同时淡入。如果 shifts 和
// changesAfter 都抓不到，后面所有「没有位移 / 没有闪」的绿色都是假的。
func TestProbeCatchesShiftAndFlash(t *testing.T) {
	// 目标先不动：MyGo 的弹簧在第一帧把初值设在目标上，直接给 60 不会播。
	var target float32
	view := func(c *ui.Context) {
		box := ui.Box(c).Width(120).Height(80)
		box.Label("探针方块")
		y := box.AnimateTo("y", target, ui.Smooth)
		alpha := box.AnimateTo("alpha", target/60, ui.Smooth)
		box.Translate(20, y).Opacity(alpha).Background(ui.RGB(200, 80, 60))
	}
	tt := ui.NewTester(view, 320, 240)
	tt.Frame()

	// 目标改了，动画才开始跑。
	target = 60
	tt.Frame()

	p := newProbe(t, tt, ui.Rect{X: 0, Y: 0, W: 320, H: 240}, 16*time.Millisecond, "探针方块")
	p.Run(30)

	if len(p.shifts(0.01)) == 0 {
		t.Error("探针没抓住帧间位移")
	}
	if len(p.changesAfter(0)) == 0 {
		t.Error("探针没抓住像素变化")
	}
}
