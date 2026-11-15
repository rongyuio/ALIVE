// Package ffmpeg 定位并准备播放所需的 FFmpeg 可执行文件。
//
// 用户不该为了看直播去安装 FFmpeg 或配置 PATH：应用自己维护一个版本化、
// 校验过的副本，放在用户缓存目录里，缺失时自动取回。PATH 只作为开发环境
// 的兜底，不是交付前提。
package ffmpeg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// 两个可执行文件的逻辑名；Windows 上补 .exe，其他平台保持原名。
const (
	FFmpeg  = "ffmpeg"
	FFprobe = "ffprobe"
)

// overrideEnv 是给测试与排障用的显式目录。设置后只认这个目录，
// 不再回落到缓存副本或 PATH，避免“以为在用指定版本，实际用了另一个”。
const overrideEnv = "ALIVE_FFMPEG_DIR"

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// Dir 是应用自己维护的组件目录；不用 PATH，也不要求用户写任何配置。
func Dir() string {
	if dir := strings.TrimSpace(os.Getenv(overrideEnv)); dir != "" {
		return dir
	}
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "alive", "bin")
}

// Path 返回可执行文件的绝对路径。找不到就是真的没有，调用方据此决定
// 是等自动准备还是走别的路径，不能在这里阻塞播放。
func Path(name string) (string, error) {
	file := exeName(name)
	for _, dir := range searchDirs() {
		candidate := filepath.Join(dir, file)
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	// 显式目录是排障用的唯一来源：设置后不再看 PATH，
	// 否则“指定版本”会被机器上的另一份静默顶替。
	if strings.TrimSpace(os.Getenv(overrideEnv)) == "" {
		if found, err := exec.LookPath(file); err == nil {
			return found, nil
		}
	}
	return "", fmt.Errorf("找不到 %s，应用组件尚未就绪", file)
}

// searchDirs 的顺序即优先级：显式配置 → 应用同目录 → 应用目录下的 bin。
// overrideEnv 设置时只认它，测试与排障才不会被机器上其它副本干扰。
func searchDirs() []string {
	if dir := strings.TrimSpace(os.Getenv(overrideEnv)); dir != "" {
		return []string{dir}
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		dirs = append(dirs, base, filepath.Join(base, "bin"))
	}
	return append(dirs, Dir())
}

// ErrPreparing 表示组件正在后台准备。播放层据此提示“稍候重试”，
// 而不是让用户以为自己的网络或直播间坏了。
var ErrPreparing = errors.New("播放组件正在准备")

// Available 报告组件是否已经可用；不触发下载。
func Available() bool {
	_, ffErr := Path(FFmpeg)
	_, probeErr := Path(FFprobe)
	return ffErr == nil && probeErr == nil
}

// Ready 在必要时加入自动准备并在 ctx 结束前等到结果。
// 播放路径用它，因此“第一次点击播放”不会因为组件还在下载而直接失败；
// 但等待仍受调用方预算约束，不会无限期卡住。
func Ready(ctx context.Context, name string) (string, error) {
	if path, err := Path(name); err == nil {
		return path, nil
	}
	if err := StartProvision(); err != nil {
		return "", err
	}
	select {
	case <-provisionDone:
		if path, err := Path(name); err == nil {
			return path, nil
		}
		if err := lastProvisionErr(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("组件准备完成但仍缺少 %s", exeName(name))
	case <-ctx.Done():
		// 正在自动安装时不能把原因说成流或网络故障：组件只差下载完成。
		if provisioning() {
			return "", fmt.Errorf("%w: %v", ErrPreparing, ctx.Err())
		}
		return "", ctx.Err()
	}
}
