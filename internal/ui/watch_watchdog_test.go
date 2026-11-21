package ui

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWatchDeadReason 锁住观测墙的断流判据。
//
// 判据只看时间轴：直播没有「播完」，播放器自己结束、报错，或者时间轴停住
// （缓冲态给 20 秒窗口），才说明这一颗地址不能用了。音频时钟不再参与——
// FLV 单连接断供时 mpv 连音频一起暂停，2026-11-22 实测四种断法与 420 秒
// 自然播放里「画面停、音频走」出现 0 次；原先为 HLS 加的音频分支只会让
// 一张冻住的画面永远不重连。
func TestWatchDeadReason(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name                             string
		ready, playing, buffering, ended bool
		pos, lastPos                     time.Duration
		silent                           time.Duration
		want                             string
	}{
		{"时间轴在走", true, true, false, false,
			10 * time.Second, 9 * time.Second, 0, ""},
		{"HLS 播放列表重排，位置倒退", true, true, false, false,
			2 * time.Second, 120 * time.Second, 10 * time.Second, ""},
		{"停住 7 秒", true, true, false, false,
			5 * time.Second, 5 * time.Second, 7 * time.Second, "画面停住 6 秒"},
		{"缓冲中停 10 秒不判死", true, true, true, false,
			5 * time.Second, 5 * time.Second, 10 * time.Second, ""},
		{"缓冲中停 25 秒判死", true, true, true, false,
			5 * time.Second, 5 * time.Second, 25 * time.Second, "画面停住 20 秒"},
		{"还没出图不判死", false, true, false, false,
			0, 0, 10 * time.Second, ""},
		{"暂停不判死", true, false, false, false,
			5 * time.Second, 5 * time.Second, 30 * time.Second, ""},
		{"播放器报告结束", true, true, false, true,
			5 * time.Second, 5 * time.Second, 0, "播放器报告结束"},
	}
	for _, tc := range cases {
		lastPos := tc.lastPos
		still := now.Add(-tc.silent)
		got := watchDeadReason(tc.ready, tc.playing, tc.buffering, tc.ended,
			tc.pos, &lastPos, &still, now)
		if got != tc.want {
			t.Errorf("%s：得到 %q，期望 %q", tc.name, got, tc.want)
		}
		if tc.name == "HLS 播放列表重排，位置倒退" && lastPos != tc.pos {
			t.Errorf("位置倒退后 lastPos 停在 %v，期望 %v", lastPos, tc.pos)
		}
	}
}

// TestWatchWallNeverRotatesLines 静态检查：观测墙不再自动轮换线路。
//
// 7fa49c6 让「缓冲持续 8 秒」触发整路重建，一次抖动被放大成一次完整重开；
// c4d6b93 删掉了播放页那套回路，墙上的同一套留到 fix/watch-wall-no-failover
// 才清掉。判死之后用房间记录里的线路直接重开，退避由 watchBackoff 管：换线
// 救不了 HLS 的分段连接，换过去的每一条线也都是同一个形态。
func TestWatchWallNeverRotatesLines(t *testing.T) {
	src, err := os.ReadFile("watch_media.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"autoLine", "failoverTried", "failoverExhausted", "healthySince", "nextFailoverLine"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Errorf("watch_media.go 里还有 %s：墙上的自动换线已删除，判死后直接重开", banned)
		}
	}
}

// TestWatchPreviewsStayUnmuted 静态检查：观测墙不对格子调 SetMuted。
//
// MyGo 把静音视频当一张画调度：系统要求减少动效、窗口被遮住、或元素不再
// 显示时会把它按住不播。墙上的非激活格本来就是静音预览，一旦真的
// SetMuted(true)，时间轴就停住，看门狗就会把一路好好的流当断流重连。
// 要静音就走 SetVolume(0)，播放器保持非静音，时间轴才会继续走。
func TestWatchPreviewsStayUnmuted(t *testing.T) {
	// SetMuted 和 StartMuted 都要拦：前者会让 MyGo 把画面按住不播，后者在
	// mpv 上设的是独立的 mute 开关——激活时只把音量调回来，mute 还在，
	// 整面墙（以及从墙接管的播放页）就一直没有声音。
	assertNoMuteCalls(t, "watch_media.go", "SetMuted", "StartMuted")
	// 引擎的起播静音同样只能走音量：Options 里一旦出现 StartMuted，
	// Open 会在 mpv 上设独立的 mute，调用方之后再调音量也解不开。
	assertNoMuteCalls(t, filepath.Join("..", "playback", "engine.go"), "StartMuted")
}

// assertNoMuteCalls 扫一份源码，禁止出现指定的静音入口。
func assertNoMuteCalls(t *testing.T, path string, names ...string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	banned := make(map[string]bool, len(names))
	for _, n := range names {
		banned[n] = true
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.CallExpr:
			if sel, ok := v.Fun.(*ast.SelectorExpr); ok && banned[sel.Sel.Name] {
				found = append(found, sel.Sel.Name+" @"+fset.Position(v.Pos()).String())
			}
		case *ast.CompositeLit:
			for _, elt := range v.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if id, ok := kv.Key.(*ast.Ident); ok && banned[id.Name] {
					found = append(found, id.Name+" @"+fset.Position(kv.Pos()).String())
				}
			}
		}
		return true
	})
	if len(found) > 0 {
		t.Fatalf("%s 里用了静音入口 %v：mute 和 volume 是两个开关，激活时调音量解不开 mute", path, found)
	}
}
