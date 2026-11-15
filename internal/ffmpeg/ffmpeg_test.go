package ffmpeg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// unsetenv 真正移除变量；t.Setenv 的空串仍是“已设置”，会被当成显式目录。
func unsetenv(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			os.Setenv(key, old)
			return
		}
		os.Unsetenv(key)
	})
}

// cleanPATH 模拟没装 FFmpeg 的干净机器，PATH 只留一个空目录。
func cleanPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// prepareCleanState 把用例放到“没有副本、没有 PATH、缓存目录为空”的起点。
// 顺序有意：环境变量先设好，再重置安装状态，这样等待收尾的清理先于环境还原执行。
func prepareCleanState(t *testing.T) {
	t.Helper()
	unsetenv(t, overrideEnv)
	cleanPATH(t)
	t.Setenv("LocalAppData", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	waitProvision(t)
	provisionMu.Lock()
	provisionStarted = false
	provisionDone = make(chan struct{})
	lastErr = nil
	provisionMu.Unlock()
	t.Cleanup(func() { waitProvision(t) })
}

func waitProvision(t *testing.T) {
	t.Helper()
	provisionMu.Lock()
	started, done := provisionStarted, provisionDone
	provisionMu.Unlock()
	if !started {
		return
	}
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("组件安装没有结束")
	}
}

// stubVerify 跳过真实执行，安装流程测试只关心落盘与校验。
func stubVerify(t *testing.T) {
	t.Helper()
	old := verifyBinary
	verifyBinary = func(context.Context, string, string) error { return nil }
	t.Cleanup(func() { verifyBinary = old })
}

func setProvisionConfig(t *testing.T, url, sha, label string) {
	t.Helper()
	provisionMu.Lock()
	provisionURL, provisionSHA256, provisionLabel = url, sha, label
	provisionMu.Unlock()
	t.Cleanup(func() {
		provisionMu.Lock()
		provisionURL, provisionSHA256, provisionLabel = defaultURL, defaultSHA, defaultLabel
		provisionMu.Unlock()
	})
}

// packageZip 构造一个只含指定条目的发布包，用来验证解压筛选。
func packageZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, body := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func serveArchive(t *testing.T, body []byte, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// 显式目录优先；没有文件时不能悄悄回落到机器上的其它副本。
func TestPathUsesOverrideDirOnly(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(overrideEnv, dir)
	if _, err := Path(FFmpeg); err == nil {
		t.Fatal("空目录不应解析到可执行文件")
	}
	file := filepath.Join(dir, exeName(FFmpeg))
	if err := os.WriteFile(file, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := Path(FFmpeg)
	if err != nil || got != file {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

// 应用自己准备副本：校验通过后放进缓存目录，非二进制条目被忽略。
func TestEnsureInstallsVerifiedCopy(t *testing.T) {
	prepareCleanState(t)
	stubVerify(t)
	body := packageZip(t, map[string]string{
		"ffmpeg-8.0/bin/" + exeName(FFmpeg):  "ffmpeg-binary",
		"ffmpeg-8.0/bin/" + exeName(FFprobe): "ffprobe-binary",
		"ffmpeg-8.0/bin/ffplay.exe":          "unwanted",
		"ffmpeg-8.0/README.txt":              "docs",
	})
	var hits atomic.Int32
	srv := serveArchive(t, body, &hits)
	sum := sha256.Sum256(body)
	setProvisionConfig(t, srv.URL, hex.EncodeToString(sum[:]), "test")

	path, err := Ready(context.Background(), FFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(Dir(), exeName(FFmpeg)) {
		t.Fatalf("组件未安装到缓存目录: %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "ffmpeg-binary" {
		t.Fatalf("组件内容 = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "ffplay.exe")); !os.IsNotExist(err) {
		t.Fatal("无关可执行文件被安装")
	}
	if _, err := os.Stat(filepath.Join(Dir(), "ffmpeg.version")); err != nil {
		t.Fatal("缺少版本标记")
	}
	if hits.Load() != 1 {
		t.Fatalf("下载次数 = %d", hits.Load())
	}
}

// 校验不一致必须整体失败，不能留下半个可用副本。
func TestEnsureRejectsWrongChecksum(t *testing.T) {
	prepareCleanState(t)
	stubVerify(t)
	body := packageZip(t, map[string]string{"ffmpeg-8.0/bin/" + exeName(FFmpeg): "ffmpeg-binary"})
	srv := serveArchive(t, body, nil)
	setProvisionConfig(t, srv.URL, strings.Repeat("0", 64), "test")

	if _, err := Ready(context.Background(), FFmpeg); err == nil {
		t.Fatal("错误校验被接受")
	}
	if _, err := Path(FFmpeg); err == nil {
		t.Fatal("失败后仍留下了可执行文件")
	}
}

// 并发起播只能触发一次下载；后来的调用等待同一份结果。
func TestEnsureSingleFlight(t *testing.T) {
	prepareCleanState(t)
	stubVerify(t)
	body := packageZip(t, map[string]string{
		"ffmpeg-8.0/bin/" + exeName(FFmpeg):  "ffmpeg-binary",
		"ffmpeg-8.0/bin/" + exeName(FFprobe): "ffprobe-binary",
	})
	var hits atomic.Int32
	srv := serveArchive(t, body, &hits)
	sum := sha256.Sum256(body)
	setProvisionConfig(t, srv.URL, hex.EncodeToString(sum[:]), "test")

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Ready(context.Background(), FFmpeg)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("并发触发 %d 次下载", hits.Load())
	}
}

// 组件未就绪时，播放要按自己的预算超时，而不是无限等待安装；
// 并且要说明原因，让上层提示“正在准备播放组件”而不是网络故障。
func TestReadyHonorsCallerBudget(t *testing.T) {
	prepareCleanState(t)
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(block)
		srv.Close()
	})
	setProvisionConfig(t, srv.URL, strings.Repeat("0", 64), "test")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Ready(ctx, FFmpeg)
	if !errors.Is(err, ErrPreparing) {
		t.Fatalf("预算耗尽的错误 = %v，期望 ErrPreparing", err)
	}
}

// 版本号是交付的一部分：装到的包必须和验收过的那份一致。
func TestVersionMatchesPinsTheAcceptanceBuild(t *testing.T) {
	line := "ffmpeg version n9.0.2-45-g1e65c6cd5d-20261102 Copyright (c) 2000-2026 the FFmpeg developers\nconfiguration: ..."
	if !versionMatches(line, "n9.0.2-45-g1e65c6cd5d-20261102") {
		t.Fatal("验收版本未被识别")
	}
	for _, other := range []string{
		"ffmpeg version 8.0-essentials_build-www.gyan.dev Copyright",
		"ffmpeg version n9.0.2-46-gdeadbeef-20261103 Copyright",
		"ffmpeg version",
	} {
		if versionMatches(other, "n9.0.2-45-g1e65c6cd5d-20261102") {
			t.Fatalf("错误版本被接受: %q", other)
		}
	}
}

// 版本不符时不能留下可执行文件，否则下一次启动会直接用上错误的构建。
func TestInstallRejectsUnexpectedVersion(t *testing.T) {
	prepareCleanState(t)
	body := packageZip(t, map[string]string{
		"ffmpeg-8.0/bin/" + exeName(FFmpeg):  "ffmpeg-binary",
		"ffmpeg-8.0/bin/" + exeName(FFprobe): "ffprobe-binary",
	})
	srv := serveArchive(t, body, nil)
	sum := sha256.Sum256(body)
	setProvisionConfig(t, srv.URL, hex.EncodeToString(sum[:]), "n9.0.2-45-g1e65c6cd5d-20261102")
	old := verifyBinary
	verifyBinary = func(context.Context, string, string) error { return errors.New("版本不符") }
	t.Cleanup(func() { verifyBinary = old })

	if _, err := Ready(context.Background(), FFmpeg); err == nil {
		t.Fatal("版本不符仍安装成功")
	}
	if _, err := Path(FFmpeg); err == nil {
		t.Fatal("版本不符仍留下可执行文件")
	}
}
