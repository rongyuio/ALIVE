package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
	"time"
)

// TestWatchDeadReason 锁住观测墙的断流判据。
//
// 墙上一度每十秒重连一次，日志全是「连接结束」。实测两间 B站房都还在播，
// 而 CDN 节点供数是一阵一阵的：mpv 的 demuxer cache 反复归零
// （paused-for-cache=true），画面时钟冻住 5–20 秒，音频时钟照走，两者差出
// 3–17 秒。旧判据只看画面，于是把活流判死、重启、丢掉刚攒的缓冲、再拉
// 同一个慢节点，循环停不下来。
func TestWatchDeadReason(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name                             string
		ready, playing, buffering, ended bool
		pos, lastPos                     time.Duration
		audio, lastAudio                 time.Duration
		silent                           time.Duration
		want                             string
	}{
		{"画面与声音都在走", true, true, false, false,
			10 * time.Second, 9 * time.Second, 10 * time.Second, 9 * time.Second, 0, ""},
		{"HLS 播放列表重排，位置倒退", true, true, false, false,
			2 * time.Second, 120 * time.Second, 2 * time.Second, 119 * time.Second, 10 * time.Second, ""},
		{"画面冻住但音频在走（CDN 供数慢）", true, true, false, false,
			6 * time.Second, 6 * time.Second, 15 * time.Second, 9 * time.Second, 12 * time.Second, ""},
		{"画面与声音都停住 7 秒", true, true, false, false,
			5 * time.Second, 5 * time.Second, 7 * time.Second, 7 * time.Second, 7 * time.Second, "画面与声音都停住 6 秒"},
		{"缓冲中两者都停 10 秒不判死", true, true, true, false,
			5 * time.Second, 5 * time.Second, 7 * time.Second, 7 * time.Second, 10 * time.Second, ""},
		{"缓冲中两者都停 25 秒判死", true, true, true, false,
			5 * time.Second, 5 * time.Second, 7 * time.Second, 7 * time.Second, 25 * time.Second, "画面与声音都停住 20 秒"},
		{"还没出图不判死", false, true, false, false,
			0, 0, 0, 0, 10 * time.Second, ""},
		{"暂停不判死", true, false, false, false,
			5 * time.Second, 5 * time.Second, 7 * time.Second, 7 * time.Second, 30 * time.Second, ""},
		{"播放器报告结束", true, true, false, true,
			5 * time.Second, 5 * time.Second, 7 * time.Second, 7 * time.Second, 0, "播放器报告结束"},
	}
	for _, tc := range cases {
		lastPos, lastAudio := tc.lastPos, tc.lastAudio
		still := now.Add(-tc.silent)
		got := watchDeadReason(tc.ready, tc.playing, tc.buffering, tc.ended,
			tc.pos, tc.audio, &lastPos, &lastAudio, &still, now)
		if got != tc.want {
			t.Errorf("%s：得到 %q，期望 %q", tc.name, got, tc.want)
		}
		if tc.name == "HLS 播放列表重排，位置倒退" && lastPos != tc.pos {
			t.Errorf("位置倒退后 lastPos 停在 %v，期望 %v", lastPos, tc.pos)
		}
		if tc.name == "画面冻住但音频在走（CDN 供数慢）" && lastAudio != tc.audio {
			t.Errorf("音频推进没有记下来：%v，期望 %v", lastAudio, tc.audio)
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
	src, err := os.ReadFile("watch_media.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "watch_media.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "SetMuted" {
			return true
		}
		found = append(found, fset.Position(call.Pos()).String())
		return true
	})
	if len(found) > 0 {
		t.Fatalf("观测墙用了 SetMuted，静音预览会被 MyGo 按住：%v", found)
	}
}
