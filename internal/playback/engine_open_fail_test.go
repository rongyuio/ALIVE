package playback

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/plugins/video"
)

// fakePlayer 是注入用的播放器，只实现 Engine 用到的那几个方法。
//
// 用它而不是真 mpv：Open 的失败分支（volume / load / play）以前只能靠人工
// 点，计数泄漏和漏登记销毁就是这样一直没人发现的——真 mpv 上根本无法稳定
// 造出这几种失败。
type fakePlayer struct {
	done    chan struct{}
	closed  bool
	volErr  error
	loadErr error
	playErr error
}

func newFakePlayer() *fakePlayer { return &fakePlayer{done: make(chan struct{})} }

func (f *fakePlayer) SetInvalidate(func())           {}
func (f *fakePlayer) Frame() (*video.Frame, uint64)  { return nil, 0 }
func (f *fakePlayer) Size() (int, int)               { return 0, 0 }
func (f *fakePlayer) State() video.State             { return video.State{} }
func (f *fakePlayer) Play() error                    { return f.playErr }
func (f *fakePlayer) Pause() error                   { return nil }
func (f *fakePlayer) Load(string) error              { return f.loadErr }
func (f *fakePlayer) SetRate(float64) error          { return nil }
func (f *fakePlayer) SetOption(string, string) error { return nil }
func (f *fakePlayer) SetVolume(float64) error        { return f.volErr }
func (f *fakePlayer) SetMuted(bool) error            { return nil }
func (f *fakePlayer) SetPresentSize(int, int)        {}
func (f *fakePlayer) Close()                         { f.closed = true }
func (f *fakePlayer) Done() <-chan struct{}          { return f.done }

// injectOpen 替换掉找库与建播放器两步，测试结束自动还原。
func injectOpen(t *testing.T, fp *fakePlayer) {
	t.Helper()
	prevLib, prevOpen := findLibrary, openPlayer
	t.Cleanup(func() { findLibrary, openPlayer = prevLib, prevOpen })
	findLibrary = func() (string, error) { return "libmpv-2.dll", nil }
	openPlayer = func(video.Options) (player, error) { return fp, nil }
}

// pendingDestroy 报告这条 Done 是否已经登记在 destroyCh 里。
func pendingDestroy(done <-chan struct{}) bool {
	destroyMu.Lock()
	defer destroyMu.Unlock()
	for _, c := range destroyCh {
		if c == done {
			return true
		}
	}
	return false
}

// waitDestroyGone 等 noteDestroy 的清理 goroutine 把登记删掉，免得留给下一个
// 用例的 waitDestroys（它最多等 5 秒，会把一整个包拖慢）。
func waitDestroyGone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pendingDestroy(done) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("destroyCh 里那条登记一直没有清掉")
}

// 起不来的 Open 也必须收干净：登记销毁 + 把存活计数减回去。
//
// 三个失败分支（volume / load / play）原来只 p.Close()，既不 noteDestroy
// 也不扣 LiveEngines：一路失败就永久占着一个「活着」的名额，而且下一次
// Open 不等这一路的 AO / mpv 释放完。
func TestOpenLoadFailureRegistersDestroyAndReleasesLive(t *testing.T) {
	fp := newFakePlayer()
	fp.loadErr = errors.New("loadfile 被拒")
	injectOpen(t, fp)

	before := LiveEngines()
	e, err := Open(context.Background(), "https://example.invalid/live.m3u8", Options{})
	if err == nil {
		t.Fatalf("load 失败却返回了引擎：%+v", e)
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("load 失败 err = %v，期望 ErrUnsupported", err)
	}
	if got := LiveEngines(); got != before {
		t.Fatalf("失败的一路把存活计数留在 %d，失败前是 %d", got, before)
	}
	if !fp.closed {
		t.Fatal("失败分支没有关掉播放器")
	}
	if !pendingDestroy(fp.done) {
		t.Fatal("失败分支没有把 Done 登记进 destroyCh：下一次 Open 不会等这一路释放")
	}

	close(fp.done)
	waitDestroyGone(t, fp.done)
}

// 后端起不来（装坏了 / 环境）和这条流解不了，要能分开。
//
// 上层据此决定「换个线路重试」还是「装坏了」；六种失败全包成 ErrUnsupported
// 时，界面只能让用户对着一条永远好不了的流反复点重试。
func TestOpenClassifiesBackendVsUnsupported(t *testing.T) {
	prevLib, prevOpen := findLibrary, openPlayer
	t.Cleanup(func() { findLibrary, openPlayer = prevLib, prevOpen })

	// 空地址：这条流本身不行。
	_, err := Open(context.Background(), "", Options{})
	if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrBackend) {
		t.Fatalf("空地址 err = %v，期望只有 ErrUnsupported", err)
	}

	// 找不到 DLL：后端坏了，换哪条流都一样。
	findLibrary = func() (string, error) { return "", errors.New("找不到 libmpv-2.dll") }
	_, err = Open(context.Background(), "https://example.invalid/x.m3u8", Options{})
	if !errors.Is(err, ErrBackend) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("找不到库 err = %v，期望只有 ErrBackend", err)
	}

	// mpv_create 失败：同样是后端。
	findLibrary = func() (string, error) { return "libmpv-2.dll", nil }
	openPlayer = func(video.Options) (player, error) { return nil, errors.New("mpv_create failed") }
	_, err = Open(context.Background(), "https://example.invalid/x.m3u8", Options{})
	if !errors.Is(err, ErrBackend) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("mpv_create 失败 err = %v，期望只有 ErrBackend", err)
	}
}
