package ui

import (
	"testing"

	"alive/internal/live"
)

// 两间房的清晰度 / 线路必须各记各的。
//
// 这个 bug 的形状是：清晰度和线路曾经挂在 App 上，切台或从观测墙接管时
// 就得靠一段「校正」把上一个平台的名字擦掉，漏一处就串台。数据模型改成
// 按房间存之后，不共享的东西就不该有被串的机会。
func TestRoomChoicesStayPerRoom(t *testing.T) {
	bili := live.Stream{
		Variants: []live.Variant{{Label: "1080P 原画"}, {Label: "720P 高清"}},
		Lines:    []live.Line{{ID: "host-a", Label: "主线"}, {ID: "host-b", Label: "备线1"}},
	}
	huya := live.Stream{
		Variants: []live.Variant{{Label: "原画"}, {Label: "高清 2000K"}, {Label: "标清 500K"}},
		Lines:    []live.Line{{ID: "TX", Label: "TX"}, {ID: "AL", Label: "AL"}},
	}

	var app App
	app.learnRoomChoice("bilibili|1", bili, live.Variant{ID: "qn10000", Label: "1080P 原画", URL: "u"})
	app.learnRoomChoice("huya|2", huya, live.Variant{ID: "source", Label: "原画", URL: "u"})

	if got := app.qualityFor("bilibili|1"); got != "1080P 原画" {
		t.Errorf("B站房间的清晰度成了 %q", got)
	}
	if got := app.qualityFor("huya|2"); got != "原画" {
		t.Errorf("虎牙房间的清晰度成了 %q", got)
	}
	if got := app.lineFor("bilibili|1"); got != "主线" {
		t.Errorf("B站房间的线路成了 %q", got)
	}
	if got := app.lineFor("huya|2"); got != "TX" {
		t.Errorf("虎牙房间的线路成了 %q", got)
	}
	// 房间自己的档位表已经不在手上时，取流要靠记下的节点 id。
	if got := app.lineIDFor("bilibili|1", live.Stream{}); got != "host-a" {
		t.Errorf("B站线路 id 成了 %q", got)
	}
	if got := app.lineIDFor("huya|2", live.Stream{}); got != "TX" {
		t.Errorf("虎牙线路 id 成了 %q", got)
	}
}

// 校正只写回它自己的房间，别的房间一个字都不动。
func TestLearnRoomChoiceOnlyTouchesItsRoom(t *testing.T) {
	var app App
	app.setRoomChoice("bilibili|1", roomChoice{Quality: "720P 高清", Line: "备线1", LineID: "host-b"})
	app.setRoomChoice("huya|2", roomChoice{Quality: "高清 2000K", Line: "AL", LineID: "AL"})

	bili := live.Stream{
		Variants: []live.Variant{{Label: "1080P 原画"}, {Label: "720P 高清"}},
		Lines:    []live.Line{{ID: "host-a", Label: "主线"}, {ID: "host-b", Label: "备线1"}},
	}
	// 平台把 B站这一路降成了 1080P 原画；线路「备线1」仍然存在，保持不动。
	app.learnRoomChoice("bilibili|1", bili, live.Variant{ID: "qn", Label: "1080P 原画", URL: "u"})

	if c := app.roomChoiceOf("bilibili|1"); c.Quality != "1080P 原画" || c.Line != "备线1" || c.LineID != "host-b" {
		t.Errorf("B站房间没校正到实际档位：%+v", c)
	}
	if c := app.roomChoiceOf("huya|2"); c.Quality != "高清 2000K" || c.Line != "AL" || c.LineID != "AL" {
		t.Errorf("校正写到了别的房间：%+v", c)
	}
}

// 取流请求带的是当前房间自己的档位，不是别的房间刚选过的那个。
func TestStartStreamUsesRoomOwnQuality(t *testing.T) {
	stubs := newMediaStubs(t, nil)
	prov := &stubStreamer{
		full: live.Stream{Live: true, Variants: []live.Variant{
			{ID: "origin", Label: "原画"},
			{ID: "hd", Label: "高清"},
		}},
		selected: live.Variant{ID: "hd", Label: "高清", URL: "https://example.invalid/hd.flv"},
	}
	useProvider(t, prov)

	a := seededApp(t)
	a.setRoomQuality("douyu|123", "高清") // 当前这一间
	a.setRoomQuality("huya|777", "原画")  // 别的平台、别的房间

	a.startStream(false)
	stubs.pump(t)
	stubs.pump(t)

	if prov.gotQuality != "高清" {
		t.Fatalf("取流请求的档位是 %q，期望当前房间自己的「高清」", prov.gotQuality)
	}
	if got := a.qualityFor("huya|777"); got != "原画" {
		t.Fatalf("当前房间的取流改掉了别的房间：%q", got)
	}
}
