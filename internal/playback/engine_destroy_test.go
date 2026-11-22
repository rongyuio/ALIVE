package playback

import (
	"testing"
	"time"
)

// 新的 mpv 实例必须在上一路彻底销毁之后才创建。
//
// 两个实例的销毁与初始化交叠时，新实例会打不开音频、video-sync=audio 的
// 时钟直接停住（doc/architecture/playback.md 的「一次性资源与线程」）。
// waitDestroys 是这条时序的唯一闸门。
func TestWaitDestroysWaitsForPendingEngine(t *testing.T) {
	done := make(chan struct{})
	noteDestroy(done)

	returned := make(chan struct{})
	go func() {
		waitDestroys()
		close(returned)
	}()

	select {
	case <-returned:
		t.Fatal("上一路还在销毁，Open 不该继续")
	case <-time.After(50 * time.Millisecond):
	}

	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("销毁完成后 waitDestroys 没有返回")
	}
}
