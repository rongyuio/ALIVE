//go:build !windows

package relay

// 没有 Windows Media Foundation 的平台保守返回 false。
func nativeHEVCAvailable() bool { return false }

func nativeAV1Available() bool { return false }
