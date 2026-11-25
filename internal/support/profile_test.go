package support

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCPUProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	if _, err := CPUProfile(context.Background(), path, 150*time.Millisecond); err != nil {
		t.Fatalf("CPUProfile: %v", err)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() == 0 {
		t.Fatal("profile 是空文件")
	}
}

// 中途停掉：返回 ErrProfileCanceled，而且不能等满整个窗口。
// 文件里是取消前采到的样本，照样能用——所以「停了」不等于「没东西」。
func TestCPUProfileCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cpu.pprof")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	began := time.Now()
	took, err := CPUProfile(ctx, path, 30*time.Second)
	elapsed := time.Since(began)

	if !errors.Is(err, ErrProfileCanceled) {
		t.Fatalf("中途取消返回 %v，期望 ErrProfileCanceled", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("取消后还等了 %v，ctx 没有打断采样", elapsed)
	}
	if took <= 0 || took > 5*time.Second {
		t.Fatalf("返回的采样时长 %v 不合理", took)
	}
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Size() == 0 {
		t.Fatal("取消时没有把已采到的样本写出去")
	}
}

// 同一时刻只允许一份：第二份直接报错，不覆盖第一份的文件。
func TestCPUProfileRejectsSecond(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	done := make(chan error, 1)
	go func() {
		_, err := CPUProfile(ctx, filepath.Join(dir, "first.pprof"), 30*time.Second)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)

	if _, err := CPUProfile(context.Background(), filepath.Join(dir, "second.pprof"), 100*time.Millisecond); err == nil {
		t.Fatal("第二份剖析应该被拒绝")
	}
	if err := <-done; !errors.Is(err, ErrProfileCanceled) {
		t.Fatalf("第一份返回 %v", err)
	}
}
