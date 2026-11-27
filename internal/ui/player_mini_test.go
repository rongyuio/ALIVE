package ui

import (
	"reflect"
	"testing"

	"github.com/egoist/mygo"
)

// TestMiniOpen 钉住"小窗是否真的开着"这条判据。
//
// 只看 pipOn 不够：模式开着、但窗口还没建出来（或建失败）时，屏幕上没有
// 任何东西可看，报"开着"会给用户一颗连到空气的"退出小窗"按钮。
func TestMiniOpen(t *testing.T) {
	alive := new(mygo.Window) // 零值窗口：IsDestroyed 为假，当作活着
	cases := []struct {
		name  string
		pipOn bool
		mini  *mygo.Window
		want  bool
	}{
		{"没开过小窗", false, nil, false},
		{"模式开着但没有真窗口", true, nil, false},
		{"窗口在", true, alive, true},
		{"模式关了指针还留着", false, alive, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &App{pipOn: c.pipOn, Mini: c.mini}
			if got := a.miniOpen(); got != c.want {
				t.Errorf("miniOpen() = %v, want %v", got, c.want)
			}
		})
	}
}

// TestTogglePipWithoutRealWindow 是 newMiniWindow 这条缝的用武之地。
//
// ui.Tester 里开不出真窗口（mygo.NewWindow 会一直等平台就绪），把
// newMiniWindow 换成返回 nil 的假实现，就能断言：模式确实开了、但窗口为 nil
// 时 miniOpen 报"没开"——于是再点一次是重新开窗，不是去关一个不存在的窗口。
func TestTogglePipWithoutRealWindow(t *testing.T) {
	orig := newMiniWindow
	defer func() { newMiniWindow = orig }()

	calls := 0
	newMiniWindow = func(mygo.WindowOptions) *mygo.Window {
		calls++
		return nil
	}

	a := &App{}
	a.togglePip()

	if !a.pipOn {
		t.Fatal("togglePip 之后 pipOn 应为真")
	}
	if a.Mini != nil {
		t.Fatalf("假实现返回 nil，a.Mini 应为 nil，实际 %v", a.Mini)
	}
	if a.miniOpen() {
		t.Error("pipOn 为真但窗口为 nil，miniOpen 应报没开")
	}

	// 报"没开"，所以第二次点击是重新开窗，不是关窗。
	a.togglePip()
	if calls != 2 {
		t.Errorf("第二次 togglePip 没有重新开窗：newMiniWindow 被调用 %d 次，想要 2", calls)
	}
	if !a.pipOn {
		t.Error("重新开窗之后 pipOn 应为真")
	}
}

// TestTogglePipRestoresFake 确认那条缝用完就换回真实现：换了假窗口的测试
// 一旦忘了 defer，往后所有开小窗的路径都会静默开出一个 nil。
func TestTogglePipRestoresFake(t *testing.T) {
	got := reflect.ValueOf(newMiniWindow).Pointer()
	want := reflect.ValueOf(mygo.NewWindow).Pointer()
	if got != want {
		t.Error("newMiniWindow 没有还原成 mygo.NewWindow")
	}
}
