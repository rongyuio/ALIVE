package ui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"alive/internal/theme"
)

// stillApp 是一份固定的首页数据：分组两格、房间 24 条，不发网络请求。
func stillApp() *App {
	a := NewMockApp()
	a.LiveMode = true
	for _, id := range a.PlatformOrder {
		a.liveLoaded[id] = true
	}
	a.Groups = []CategoryGroup{{
		ID: "1", Name: "网游",
		Categories:  []string{"英雄联盟", "DOTA2"},
		CategoryIDs: []string{"86", "87"},
	}}
	a.ListLoading = false
	return a
}

func stillRooms(n int) []Streamer {
	rooms := make([]Streamer, n)
	for i := range rooms {
		rooms[i] = Streamer{
			Platform: "douyu", RoomID: fmt.Sprint(i),
			Name: fmt.Sprintf("主播%d", i), Title: "标题",
		}
	}
	return rooms
}

// 数据到达的那一帧就是最终画面：没有淡入、没有位移。
//
// 用同一个视口的两份构建对比：一份从骨架切到数据，一份一开始就有数据。
// Tester 的时钟不推进时，任何「动画」都停在起点——旧代码里卡片的
// opacity 从 0 开始，这一帧会和参照差出整片像素。
func TestDataArrivalCommitsFinalFrame(t *testing.T) {
	rooms := stillRooms(24)

	live := stillApp()
	live.ListLoading = true
	ttLive := ui.NewTester(live.Home, 1391, 845)
	ttLive.Frame()

	live.Rooms = rooms
	live.ListLoading = false
	ttLive.Frame()
	arrival := ttLive.Image()

	// 时钟推进之后不能再变：没有动画在路上。
	ttLive.Advance(500 * time.Millisecond)
	if d := diffImages(arrival, ttLive.Image()); d != 0 {
		t.Errorf("数据到达后画面还在变：%d 个像素不同", d)
	}

	// 参照必须是「停稳之后」的那一帧：Tester 的时钟不推进时，任何动画都
	// 停在起点。这里多推半秒，让参照脱离任何进场动画。
	warm := stillApp()
	warm.Rooms = rooms
	ttWarm := ui.NewTester(warm.Home, 1391, 845)
	ttWarm.Frame()
	ttWarm.Advance(500 * time.Millisecond)

	if d := diffImages(arrival, ttWarm.Image()); d != 0 {
		t.Errorf("数据到达那一帧不是最终画面：和参照差 %d 个像素", d)
	}
}

// 骨架是静止的：等待期间画面不变，也不请求帧。
func TestSkeletonIsStill(t *testing.T) {
	a := stillApp()
	a.ListLoading = true
	a.Rooms = nil
	tt := ui.NewTester(a.Home, 1391, 845)
	tt.Frame()

	p := newProbe(t, tt, ui.Rect{X: 0, Y: 0, W: 1391, H: 845}, 16*time.Millisecond)
	p.Run(120)
	p.RequireStillAfter(0)

	if tt.FramePending() {
		t.Error("骨架静止时还在请求帧：等待本身不该驱动重绘")
	}
}

// 信息条高度必须等于「两行文字 + 内边距」的公式：
// 骨架按同一个公式撑高，MyGo 的行高一变，这条就红。
func TestCardMetaHeightFormula(t *testing.T) {
	a := stillApp()
	rooms := stillRooms(1)
	tt := ui.NewTester(func(c *ui.Context) {
		a.grid(c, rooms, &a.HomeList, nil)
	}, 600, 400)
	tt.Frame()

	r, ok := tt.Find("直播间:" + rooms[0].Name)
	if !ok {
		t.Fatal("卡片没出现")
	}
	cardW := r.W
	coverH := cardW * 9 / 16
	want := coverH + 13 + 15 + cardLineHeight(theme.TextSM) + 3 + cardLineHeight(theme.TextXS)
	if d := math.Abs(float64(r.H - want)); d > 0.01 {
		t.Errorf("卡片高 %.2f，公式算出 %.2f（差 %.2f）：骨架和真卡会差这一截",
			r.H, want, d)
	}
}

// 内容层不许挂进出场动画：切换时卡片在原位、直接出现。
//
// 这是结构性的守卫，和 label_test.go 一样扫源码：以后谁给卡片或内容层
// 加回 Transition / Translate / Loop，make check 直接红。
func TestContentLayerHasNoMotion(t *testing.T) {
	files := map[string][]string{
		"card.go": {"Card", "coverArt"},
		"home.go": {"Home", "grid", "skeletonGrid"},
	}
	for file, funcs := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("%s 解析失败：%v", file, err)
		}
		wanted := map[string]bool{}
		for _, name := range funcs {
			wanted[name] = true
		}
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || !wanted[fn.Name.Name] {
				return true
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "Transition", "Translate", "Loop":
					t.Errorf("%s:%d %s 里出现 %s：内容层不该有进出场动画",
						file, fset.Position(call.Pos()).Line, fn.Name.Name, sel.Sel.Name)
				}
				return true
			})
			return false
		})
	}
}

// 切平台、切分区：内容层只换代，不位移、不淡入，切完就是稳态。
//
// 这是 0010 那条决定的运行时守卫。用户报的「切过去闪一下 / 一直在抖」，
// 在这里被拆成两条可判定的断言：卡片逐帧不挪（Δ≤0.01 DIP），内容区域
// 从第一帧起像素不再变。任何一个回潮都会红，并打出帧号。
func TestContentLayerStableAcrossSwitches(t *testing.T) {
	a := stillApp()
	a.Platform = "bilibili"
	a.GroupsByPlatform["douyu"] = a.Groups
	a.feedCache["douyu|86|-1"] = feedEntry{rooms: stillRooms(24), cursor: listPageSize, at: time.Now()}
	second := stillRooms(24)
	for i := range second {
		second[i].Name = fmt.Sprintf("二区%02d", i)
	}
	a.feedCache["douyu|87|-1"] = feedEntry{rooms: second, cursor: listPageSize, at: time.Now()}

	tt := ui.NewTester(a.Shell, 1391, 845)
	tt.Frame()

	// 平台切换：缓存命中，数据在新平台上立刻成立。
	a.switchTab("douyu")
	tt.Frame()
	if a.Platform != "douyu" {
		t.Fatalf("平台还是 %s", a.Platform)
	}
	names := cardNames(a.Rooms, 8)
	if len(names) == 0 {
		t.Fatal("切平台后内容层是空的")
	}
	p := newProbe(t, tt, contentRect(t, tt, names...), 16*time.Millisecond, names...)
	p.Run(40)
	p.RequireNoShift(0.01)
	p.RequireAlwaysPresent()
	p.RequireStillAfter(0)

	// 分区切换：同样只换代次。
	a.selectCategory(1)
	tt.Frame()
	if a.CategoryIndex != 1 {
		t.Fatalf("分区没切过去：CategoryIndex=%d", a.CategoryIndex)
	}
	names = cardNames(a.Rooms, 8)
	if len(names) == 0 {
		t.Fatal("切分区后内容层是空的")
	}
	p = newProbe(t, tt, contentRect(t, tt, names...), 16*time.Millisecond, names...)
	p.Run(60)
	p.RequireNoShift(0.01)
	p.RequireAlwaysPresent()
	p.RequireStillAfter(0)

	// 骨架 → 数据：没有缓存的那一次，也是「闪」最容易回来的地方。
	// 前两段走的都是缓存命中，碰不到这条路径；这一段补上，注入一次入场
	// 位移就会红（见 0010 的根因 2）。
	third := stillRooms(24)
	for i := range third {
		third[i].Name = fmt.Sprintf("三区%02d", i)
	}
	// 回第一分区：换掉内容层的 key，这一段才是「新一屏」的到达。
	a.selectCategory(0)
	a.Rooms = nil
	a.ListLoading = true
	tt.Frame()
	a.Rooms = third
	a.ListLoading = false
	tt.Frame()

	names = cardNames(third, 8)
	p = newProbe(t, tt, contentRect(t, tt, names...), 16*time.Millisecond, names...)
	p.Run(40)
	p.RequireNoShift(0.01)
	p.RequireAlwaysPresent()
	p.RequireStillAfter(0)
}
