//go:build !windows

package support

import "fmt"

// Minidump 只在 Windows 上有实现；其它平台由 crash-<run>.log 的文本栈负责。
func Minidump(string) error {
	return fmt.Errorf("当前平台不支持 minidump")
}
