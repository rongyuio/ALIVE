//go:build windows

package support

import (
	"fmt"
	"os"
	"syscall"
)

var (
	dbghelp               = syscall.NewLazyDLL("dbghelp.dll")
	procMiniDumpWriteDump = dbghelp.NewProc("MiniDumpWriteDump")
)

// MiniDumpNormal 是 minidumpapiset.h 的 MINIDUMP_TYPE 默认值：线程、栈、
// 模块列表，不含完整内存。几十 MB 以内，足够看调用栈和线程状态。
const miniDumpNormal = 0x00000000

// Minidump 给当前进程写一份 minidump。
//
// 它和解剖用的 crash-<run>.log 互补：crash 日志是 Go 的文本栈，minidump
// 能进 WinDbg/Visual Studio 看线程、寄存器和 D3D 调用栈。失败不是致命错误，
// 调用方应保留栈文本。
func Minidump(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	proc, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}
	r, _, callErr := procMiniDumpWriteDump.Call(
		uintptr(proc),
		uintptr(syscall.Getpid()),
		f.Fd(),
		miniDumpNormal,
		0, 0, 0,
	)
	if r == 0 {
		if callErr == nil || callErr == syscall.Errno(0) {
			return fmt.Errorf("MiniDumpWriteDump 失败")
		}
		return fmt.Errorf("MiniDumpWriteDump: %w", callErr)
	}
	return nil
}
