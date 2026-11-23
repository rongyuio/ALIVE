package live

import (
	"os"
	"testing"
)

// TestMain 把整包测试的用户目录换到临时目录。
//
// 这个包里的 provider 是**进程级单例**（ProviderFor 里的 sync.Once），而
// newDouyu / newBilibili 在构造时就会 newQRAuth → restore() → 读
// %APPDATA%\alive\douyu.json。开发机上有真实会话时，这条链路会：
//
//  1. 读到真实 cookie，把 provider 直接置成登录态；
//  2. 后台发起 verify，成功之后把会话**写回**真实文件。
//
// 于是跑一次测试就改一次开发者的登录态——2026-11-23 实测 douyu.json 的
// mtime 每跑一次 go test ./internal/live 就更新一次。
//
// 反过来，登录态还会改变被测行为：带 cookie 的请求和不带的走的不是同一
// 条分支。测试不该因为机器上登录过而变绿或变红。
//
// 少数测试（auth_verify_test.go 等）自己用 useTempConfigDir 再换一次，
// 那是在这个基础上更细的隔离，不冲突。
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "alive-live-test-")
	if err != nil {
		panic("alive/internal/live: 建临时用户目录失败: " + err.Error())
	}
	for _, kv := range [][2]string{
		{"AppData", dir}, {"XDG_CONFIG_HOME", dir},
		{"LocalAppData", dir}, {"XDG_CACHE_HOME", dir},
	} {
		if err := os.Setenv(kv[0], kv[1]); err != nil {
			panic("alive/internal/live: 隔离 " + kv[0] + " 失败: " + err.Error())
		}
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
