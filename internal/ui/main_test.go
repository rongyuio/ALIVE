package ui

import (
	"os"
	"testing"

	"github.com/rs/zerolog"

	"alive/internal/diag"
)

// TestMain 把整包测试的用户目录换到临时目录。
//
// 这不是洁癖：statePath() 和 authSessionPath() 读的是 %APPDATA%\alive，
// 也就是**这台机器上真实的那份**。开发机上只要用过一次应用，那个
// state.json 里就有"上次停在哪个一级分区"之类的状态，于是 12 个关注页
// 测试在别人机器上绿、在这台机器上红——而红的原因和被测代码无关。
//
// 2026-11-23 就是这么撞上的：TestFollowsTabOpensPage 报"关注页没有渲染"，
// git stash 之后一样红，隔离 APPDATA 之后全绿。这类失败最耗人：它看起来
// 像回归，实际是测试在偷读用户数据。
//
// 反过来也一样重要：测试会写。NewMockApp 的假登录会把会话落到真实的
// %APPDATA%\alive\douyu.json 上——跑一次测试就改一次开发者的登录态。
//
// 用 os.Setenv 而不是 t.Setenv：TestMain 里没有 *testing.T。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "alive-ui-test-")
	if err != nil {
		panic("alive/internal/ui: 建临时用户目录失败: " + err.Error())
	}
	// Windows 读 AppData，其余平台读 XDG_CONFIG_HOME。缓存目录一并隔离：
	// applog 走 UserCacheDir，测试不该往真实日志里写。
	for _, kv := range [][2]string{
		{"AppData", dir}, {"XDG_CONFIG_HOME", dir},
		{"LocalAppData", dir}, {"XDG_CACHE_HOME", dir},
	} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			panic("alive/internal/ui: 隔离 " + kv[0] + " 失败: " + err.Error())
		}
	}

	// 默认丢弃日志：新加的诊断事件会让测试输出变成几千行，真正的失败
	// 反而找不到。需要检查日志的测试自己在用 captureLogger 换一个。
	nop := zerolog.Nop()
	diag.SetLogger(&nop)

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
