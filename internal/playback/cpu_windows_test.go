//go:build windows

package playback

import "golang.org/x/sys/windows"

// cpuSeconds 读本进程累计的 CPU 时间（秒）。
//
// 不能用 runtime/metrics：那套只统计 Go 运行时调度到的 goroutine，libmpv 的
// 解码、渲染线程是 C 线程，不在里面——4K60 软解实测报 0.2 核，明显不成立。
// GetProcessTimes 拿的是整个进程，包含所有线程。
func cpuSeconds() float64 {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0
	}
	return float64(kernel.Nanoseconds()+user.Nanoseconds()) / 1e9
}
