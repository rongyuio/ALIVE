//go:build !windows

package playback

// cpuSeconds 在非 Windows 上不测量：本地流测试是给播放引擎定基线的，
// 目前只有 Windows 走 libmpv 的 D3D11 路径。
func cpuSeconds() float64 { return 0 }
