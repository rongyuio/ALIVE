package ffmpeg

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 固定的版本与校验值：换版本必须同时更新两者，否则旧响应会被拒绝。
// 下载地址使用带版本号的发布包，不用 latest，避免同一 URL 的内容漂移。
// 版本必须与开发机长期使用、验收过的那份完全一致：
// BtbN autobuild-2026-11-02-12-51 的 win64-gpl 静态包。
// 不用 latest、不用“同版本号的新构建”，换版本要重新做播放验收。
const (
	defaultURL   = "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-02-12-51/ffmpeg-n9.0.2-45-g1e65c6cd5d-win64-gpl-9.0.zip"
	defaultSHA   = "20e6537e293473506d79ef4a6b203bc6b9735a9d3d225c28e05ef968b3438116"
	defaultLabel = "n9.0.2-45-g1e65c6cd5d-20261102"
)

const (
	maxArchiveBytes   = 512 << 20
	maxExecutableSize = 400 << 20
	provisionTimeout  = 20 * time.Minute
)

// 安装状态全部由这一把锁保护：后台 goroutine、播放等待、测试替换都要经过它，
// 否则“换一份结果”和“旧 goroutine 收尾”会写同一个变量。
var (
	provisionMu      sync.Mutex
	provisionStarted bool
	provisionDone    = make(chan struct{})
	lastErr          error
	provisionURL     = defaultURL
	provisionSHA256  = defaultSHA
	provisionLabel   = defaultLabel
)

func lastProvisionErr() error {
	provisionMu.Lock()
	defer provisionMu.Unlock()
	return lastErr
}

// ProvisionDone 返回后台安装结束的通道；尚未开始时先启动安装。
// 播放层用它做“装好之后自动重试”，用户不必自己点第二次。
func ProvisionDone() <-chan struct{} {
	_ = StartProvision()
	provisionMu.Lock()
	defer provisionMu.Unlock()
	return provisionDone
}

// provisioning 报告后台安装是否已开始且尚未结束。
func provisioning() bool {
	provisionMu.Lock()
	started, done := provisionStarted, provisionDone
	provisionMu.Unlock()
	if !started {
		return false
	}
	select {
	case <-done:
		return false
	default:
		return true
	}
}

// StartProvision 在后台准备组件，重复调用安全。
// 下载使用自己的 context：一次播放被取消不该把正在进行的安装一起中断，
// 用户下次重试直接复用同一份结果。
func StartProvision() error {
	if _, err := Path(FFmpeg); err == nil {
		return nil
	}
	provisionMu.Lock()
	if provisionStarted {
		provisionMu.Unlock()
		return nil
	}
	provisionStarted = true
	// 抓一份当前配置和完成通道：后台任务只碰自己抓到的值，
	// 测试或后续版本切换时不会被旧任务串写。
	done := provisionDone
	url, sha, label := provisionURL, provisionSHA256, provisionLabel
	provisionMu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), provisionTimeout)
		defer cancel()
		err := install(ctx, url, sha, label)
		provisionMu.Lock()
		// 只有仍是当前这一轮时结果才算数，旧任务的收尾不能污染新状态。
		if done == provisionDone {
			if err != nil {
				lastErr = err
				slog.Warn("播放组件准备失败", "error", err)
			} else {
				slog.Info("播放组件就绪", "version", label, "dir", Dir())
			}
		}
		provisionMu.Unlock()
		close(done)
	}()
	return nil
}

// install 下载、校验并原子落盘。任何一步失败都不留下半份可执行文件，
// 否则下一次 Path 会把损坏副本当成可用。
func install(ctx context.Context, url, sha, label string) error {
	dir := Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建组件目录: %w", err)
	}
	archive, err := download(ctx, url, sha)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	files, err := extract(ctx, archive, dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := os.Chmod(file, 0o700); err != nil {
			return fmt.Errorf("设置组件权限: %w", err)
		}
	}
	// 校验过的哈希只能证明“拿到的是这个包”，不能证明“这就是验收过的那份”。
	// 真跑一次 -version，把版本号也钉住，换包会在这里直接失败而不是悄悄生效。
	if err := verifyBinary(ctx, filepath.Join(dir, exeName(FFmpeg)), label); err != nil {
		for _, file := range files {
			os.Remove(file)
		}
		return err
	}
	marker := filepath.Join(dir, "ffmpeg.version")
	return os.WriteFile(marker, []byte(label+"\n"+sha+"\n"), 0o600)
}

func download(ctx context.Context, url, sha string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("组件下载请求无效: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("组件下载失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("组件下载失败: HTTP %d", resp.StatusCode)
	}
	dir := Dir()
	tmp, err := os.CreateTemp(dir, "ffmpeg-download-*.zip")
	if err != nil {
		return "", fmt.Errorf("组件临时文件: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxArchiveBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件下载中断: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件写入失败: %w", closeErr)
	}
	if written > maxArchiveBytes {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件包超过 %d 字节上限", maxArchiveBytes)
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, sha) {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件校验不一致: %s", got)
	}
	return tmp.Name(), nil
}

// extract 只认包内的 ffmpeg / ffprobe，其他内容一律忽略，避免把发布包里的
// 文档、示例或无关二进制带进用户目录。
func extract(ctx context.Context, archive, dir string) ([]string, error) {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("组件包无法读取: %w", err)
	}
	defer reader.Close()
	wanted := map[string]bool{exeName(FFmpeg): true, exeName(FFprobe): true}
	var written []string
	fail := func(err error) ([]string, error) {
		for _, file := range written {
			os.Remove(file)
		}
		return nil, err
	}
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		name := ""
		if filepath.Base(entry.Name) == exeName(FFmpeg) {
			name = exeName(FFmpeg)
		} else if filepath.Base(entry.Name) == exeName(FFprobe) {
			name = exeName(FFprobe)
		}
		if name == "" || !wanted[name] || !strings.Contains(filepath.ToSlash(entry.Name), "/bin/") {
			continue
		}
		delete(wanted, name)
		target, err := installFile(entry, dir)
		if err != nil {
			return fail(err)
		}
		written = append(written, target)
	}
	if len(wanted) > 0 {
		return fail(errors.New("组件包缺少可执行文件"))
	}
	return written, nil
}

// verifyBinary 可在测试里替换：真实校验要执行二进制，测试包内没有。
var verifyBinary = verifyVersion

func verifyVersion(ctx context.Context, exe, want string) error {
	if want == "" {
		return nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, exe, "-hide_banner", "-version")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("组件无法执行: %w", err)
	}
	if !versionMatches(string(out), want) {
		return fmt.Errorf("组件版本与验收版本不符: 期望 %s", want)
	}
	return nil
}

// versionMatches 只看首行的版本号，不把版权和配置也算进去。
func versionMatches(output, want string) bool {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(line)
	return len(fields) >= 3 && fields[2] == want
}

func installFile(entry *zip.File, dir string) (string, error) {
	if entry.UncompressedSize64 > maxExecutableSize {
		return "", fmt.Errorf("组件 %s 超过大小上限", filepath.Base(entry.Name))
	}
	src, err := entry.Open()
	if err != nil {
		return "", fmt.Errorf("组件解压失败: %w", err)
	}
	defer src.Close()
	tmp, err := os.CreateTemp(dir, "ffmpeg-install-*")
	if err != nil {
		return "", fmt.Errorf("组件临时文件: %w", err)
	}
	written, copyErr := io.Copy(tmp, io.LimitReader(src, maxExecutableSize+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || written > maxExecutableSize {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件写入失败")
	}
	target := filepath.Join(dir, filepath.Base(entry.Name))
	os.Remove(target)
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("组件安装失败: %w", err)
	}
	return target, nil
}
