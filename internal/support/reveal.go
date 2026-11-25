package support

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Reveal 在系统文件管理器里选中 path。
//
// Windows 走 `explorer /select,<path>`。explorer 的退出码不可靠——成功也常常
// 返回 1——所以只 Start 不 Wait，不拿退出码当结果。文件不存在先报错，否则
// explorer 会静默打开「我的文档」，用户以为点错了。
func Reveal(path string) error {
	if path == "" {
		return fmt.Errorf("没有可定位的文件")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("文件不在了：%v", err)
	}
	cmd := revealCommand(runtime.GOOS, path)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("打开文件管理器失败：%v", err)
	}
	// 回收子进程，别留僵尸。explorer 是短命进程，立即退出是正常的。
	go func() { _ = cmd.Wait() }()
	return nil
}

// revealCommand 按平台拼命令。单独拆出来是为了能测参数形状：
// Windows 必须是单个 "/select,<path>" 参数，拆成两个 explorer 不认。
func revealCommand(goos, path string) *exec.Cmd {
	switch goos {
	case "windows":
		return exec.Command("explorer", "/select,"+filepath.Clean(path))
	case "darwin":
		return exec.Command("open", "-R", path)
	default:
		return exec.Command("xdg-open", filepath.Dir(path))
	}
}
