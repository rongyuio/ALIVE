package ui

// 清晰度与线路：两个下拉都得能开、能选、选完屏幕上的值跟着变。
//
// 这两条在 mock 数据下**根本没有东西可点**，原因查出来是两个 mock 缺陷，
// 不是功能缺失：
//
//  1. mockStreamers 造的房间 RoomID 是空串，而 fetchStream 第一行就是
//     「RoomID 空就返回」——取流从来没跑过，档位和线路自然为空。
//  2. mock provider 的 Stream 故意不给 Variants（给了会起 ffmpeg 连真实
//     CDN），所以即便取流跑通也没有档位。
//
// 补上 RoomID、再用一个有档位的 provider（只给数据、不碰网络），两个下拉
// 就出来了。界面那条路一行没改。

import (
	"context"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"alive/internal/live"
	"alive/internal/playback"
)

var (
	qualityA = live.Variant{ID: "a", Label: "原画", URL: "fake-a", Width: 1920, Height: 1080, FrameRate: 60}
	qualityB = live.Variant{ID: "b", Label: "高清", URL: "fake-b", Width: 1280, Height: 720, FrameRate: 30}
)

// variantsProvider 有档位、有线路，取流不走网络。
type variantsProvider struct{ live bool }

func (p *variantsProvider) Platform() live.Platform { return live.Douyu }
func (p *variantsProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (p *variantsProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (p *variantsProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }
func (p *variantsProvider) RoomInfo(context.Context, string) (live.Room, error) {
	return live.Room{Platform: live.Douyu, RoomID: "1", Live: p.live}, nil
}
func (p *variantsProvider) StreamForPlayback(context.Context, string, string, string) (live.Stream, live.Variant, error) {
	s := live.Stream{
		Platform: live.Douyu, RoomID: "1", Live: p.live,
		Variants: []live.Variant{qualityA, qualityB},
		Lines:    []live.Line{{ID: "main", Label: "主线"}, {ID: "backup1", Label: "备线1"}},
	}
	return s, qualityA, nil
}

// variantsApp 造一个能验清晰度 / 线路的 App：有档位、有线路，不起真播放器。
func variantsApp(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	// 取流结果经 applyMedia 回主线程；Tester 里 runOnMain 必须桩掉。
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = oldRun })

	// 不起真播放器：只验界面，不验解码。
	oldOpen := openNative
	openNative = func(context.Context, string, playback.Options) (*playback.Engine, error) {
		return nil, playback.ErrUnsupported
	}
	t.Cleanup(func() { openNative = oldOpen })

	useProvider(t, &variantsProvider{live: true})
	app, tt := home(t)
	// mockStreamers 造的房间 RoomID 是空串，而 fetchStream 第一行就是
	// 「RoomID 空就返回」——取流不会跑，档位和线路永远为空。
	//
	// 这里不去改 mockStreamers：给它补 RoomID 会让**所有**走播放页的测试
	// 都开始真取流（我试过，弹幕那 7 条当场红）。只在这一条里换成一间有
	// RoomID 的房，影响面就是这一条。
	// 房间得落在当前选中的一级/二级分区里，否则 filterStreamers 把它滤掉，
	// 屏幕上只剩空态。用 mock 默认选中的「英雄联盟」。
	room := Streamer{
		Platform: "douyu", RoomID: "media-1", Name: "档位房",
		Category: "英雄联盟", Live: true,
	}
	app.Streamers = []Streamer{room}
	tt.Frame()
	// inPlayer 按「直播间:」找第一张卡；房间换了之后要重新点。
	mustClick(t, tt, "直播间:"+room.Name)
	tt.Frame()
	tt.Advance(400e6)
	tt.Frame()
	mustFind(t, tt, "播放画面（双击全屏）")

	// 取流是异步的：等档位真的到手上，再去看屏幕。
	waitFor(t, func() bool {
		tt.Frame()
		return len(app.qualityOptions()) > 0
	})
	return app, tt
}

func TestFeatureQualityDropdown(t *testing.T) {
	_, tt := variantsApp(t)

	mustFind(t, tt, "清晰度")
	// 当前档位名就在控制条上。
	mustFind(t, tt, "原画")

	// 这两个下拉是「指针移上去就开、点一下反而关」（h.hover / h.clicked），
	// 和网页端的 onMouseEnter 一致。所以用 Move，不用 Click。
	mustHover(t, tt, "清晰度")
	mustFind(t, tt, "高清")

	// 选另一档：控制条上的值跟着换。
	mustClick(t, tt, "高清")
	mustFind(t, tt, "高清")
	if _, ok := tt.Find("原画"); ok {
		t.Fatal("切到高清之后控制条上还写着原画")
	}
}

func TestFeatureLineDropdown(t *testing.T) {
	_, tt := variantsApp(t)

	mustFind(t, tt, "线路")
	mustFind(t, tt, "主线")

	mustHover(t, tt, "线路")
	mustFind(t, tt, "备线1")

	mustClick(t, tt, "备线1")
	mustFind(t, tt, "备线1")
	if _, ok := tt.Find("主线"); ok {
		t.Fatal("切到备线1之后控制条上还写着主线")
	}
}

// 没有多线路的平台不该出现「线路」：抖音网页端也没有这种东西。
func TestFeatureNoLineDropdownWithoutLines(t *testing.T) {
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = oldRun })
	oldOpen := openNative
	openNative = func(context.Context, string, playback.Options) (*playback.Engine, error) {
		return nil, playback.ErrUnsupported
	}
	t.Cleanup(func() { openNative = oldOpen })

	useProvider(t, &noLinesProvider{})
	app, tt := home(t)
	room := Streamer{
		Platform: "douyu", RoomID: "media-2", Name: "单线路房",
		Category: "英雄联盟", Live: true,
	}
	app.Streamers = []Streamer{room}
	tt.Frame()
	mustClick(t, tt, "直播间:"+room.Name)
	tt.Frame()
	tt.Advance(400e6)
	tt.Frame()
	waitFor(t, func() bool { tt.Frame(); return tt.HasText("清晰度") })

	mustNotFind(t, tt, "线路")
}

// noLinesProvider 有档位、没有线路。
type noLinesProvider struct{ variantsProvider }

func (p *noLinesProvider) StreamForPlayback(context.Context, string, string, string) (live.Stream, live.Variant, error) {
	s := live.Stream{
		Platform: live.Douyu, RoomID: "1", Live: true,
		Variants: []live.Variant{qualityA, qualityB},
	}
	return s, qualityA, nil
}

// 屏蔽词：加一个词，列表里出现、计数变；删掉，回到 0。
func TestFeatureDanmuFilterAddAndRemove(t *testing.T) {
	app, tt := home(t)
	inPlayer(t, app, tt)

	mustClick(t, tt, "弹幕屏蔽")
	mustFind(t, tt, "屏蔽词")
	mustFind(t, tt, "0 个")

	// 输入框里打字、回车添加。
	mustClick(t, tt, "弹幕屏蔽词")
	tt.Type("广告")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	mustFind(t, tt, "1 个")
	mustFind(t, tt, "删除屏蔽词 广告")

	// 删掉：回到 0 个。
	mustClick(t, tt, "删除屏蔽词 广告")
	mustFind(t, tt, "0 个")
	if _, ok := tt.Find("删除屏蔽词 广告"); ok {
		t.Fatal("删掉之后那一行还在")
	}
}

// 取流失败：屏幕上出现能读的失败原因和重试入口，而不是停在加载层。
func TestFeatureStreamFailureShowsRetry(t *testing.T) {
	oldRun := runOnMain
	runOnMain = func(_ *mygo.Window, fn func()) bool { fn(); return true }
	t.Cleanup(func() { runOnMain = oldRun })

	useProvider(t, failingProvider{})
	app, tt := home(t)
	// 同样要有 RoomID：空的话 fetchStream 第一行就返回，根本不会失败。
	room := Streamer{
		Platform: "douyu", RoomID: "fail-1", Name: "失败房",
		Category: "英雄联盟", Live: true,
	}
	app.Streamers = []Streamer{room}
	tt.Frame()
	mustClick(t, tt, "直播间:"+room.Name)
	tt.Frame()
	tt.Advance(400e6)
	tt.Frame()

	waitFor(t, func() bool {
		tt.Frame()
		return tt.HasText("取流失败，请重试或切换线路")
	})
	mustFind(t, tt, "重试")
	// 失败态不能停在加载层：那条提示必须消失。
	if tt.HasText("正在拉取直播流…") {
		t.Fatal("已经报失败了，屏幕上还挂着「正在拉取直播流」")
	}
}

// failingProvider 取流永远被平台拦下。
type failingProvider struct{}

func (failingProvider) Platform() live.Platform { return live.Douyu }
func (failingProvider) Categories(context.Context) ([]live.CategoryGroup, error) {
	return nil, nil
}
func (failingProvider) Rooms(context.Context, string, int, int) (live.Page, error) {
	return live.Page{}, nil
}
func (failingProvider) Search(context.Context, string) ([]live.Room, error) { return nil, nil }
func (failingProvider) RoomInfo(context.Context, string) (live.Room, error) {
	return live.Room{Platform: live.Douyu, RoomID: "1", Live: true}, nil
}
func (failingProvider) Stream(context.Context, string, string) (live.Stream, error) {
	return live.Stream{}, live.ErrBlocked
}
