//go:build windows

package video

import (
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/scene"
)

// 共享纹理的身份必须进程内唯一。
//
// 渲染器按 scene.Surface.NativeID 缓存打开的纹理——同一个 ID 只开一次。
// 如果每个生产者各自从 1 数起，墙上的第一块纹理全是 ID 1，渲染器把它们
// 当成同一块，屏幕上就是同一画面铺满整墙。2026-11-23 真出过这个 bug。
func TestSurfaceIDsAreUnique(t *testing.T) {
	seen := map[uint64]bool{}
	for i := range 100 {
		id := scene.NextSurfaceID()
		if id == 0 {
			t.Fatal("Surface 的 ID 不能是 0")
		}
		if seen[id] {
			t.Fatalf("第 %d 次取到的 ID %d 已经用过", i, id)
		}
		seen[id] = true
	}
}

// 静态守卫：建纹理的地方必须用进程级发号，不能自己数。
//
// 这条比上一条重要——上一条测的是发号器本身，而 bug 出在调用点忘了用它。
// 扫源码才挡得住"下一版又改回 t.id++"。
func TestTargetUsesSurfaceIDs(t *testing.T) {
	src, err := os.ReadFile("d3d11_target_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "scene.NextSurfaceID()") {
		t.Fatal("建纹理之后没有换新 ID：渲染器会把十格墙的纹理当成同一块")
	}
	if strings.Contains(body, "t.id++") {
		t.Fatal("还在自己数 ID：每个引擎都从 1 起，十格墙会撞成同一个画面")
	}
}
