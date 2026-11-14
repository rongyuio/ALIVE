//go:build !windows

package relay

// bindProcessLifecycle 在非 Windows 上是空实现。
//
// 这里只为了跨平台编译：应用本体只跑 Windows（见 AGENTS.md），
// 真正的子进程生命周期管理在 job_windows.go。
func bindProcessLifecycle(_ int) (func(), error) { return func() {}, nil }
