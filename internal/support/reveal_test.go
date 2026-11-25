package support

import (
	"path/filepath"
	"strings"
	"testing"
)

// Windows 的 explorer 只认 "/select,<path>" 这样一个参数：拆成
// "/select," 和路径两个参数时它会打开默认文件夹，而不是选中文件。
// 参数形状是 explorer 的约定，不是我们自己定的。
func TestRevealCommandWindowsSelectsFile(t *testing.T) {
	path := `C:\Users\ry\AppData\Local\alive\logs\bundle-20261125-153012.zip`
	cmd := revealCommand("windows", path)

	if len(cmd.Args) != 2 {
		t.Fatalf("explorer 只该收到两个 argv：%q", cmd.Args)
	}
	if cmd.Args[0] != "explorer" {
		t.Fatalf("argv[0]=%q", cmd.Args[0])
	}
	if cmd.Args[1] != "/select,"+path {
		t.Fatalf("argv[1]=%q，期望 /select,<绝对路径>", cmd.Args[1])
	}
}

func TestRevealCommandOtherPlatforms(t *testing.T) {
	path := filepath.Join("/tmp", "logs", "bundle.zip")
	if got := revealCommand("darwin", path).Args; len(got) != 3 || got[0] != "open" || got[1] != "-R" {
		t.Fatalf("darwin=%q", got)
	}
	got := revealCommand("linux", path).Args
	if len(got) != 2 || got[0] != "xdg-open" || !strings.HasSuffix(got[1], "logs") {
		t.Fatalf("linux=%q", got)
	}
}

// 文件不存在时不能去开文件管理器：用户会以为点错了，也不知道该看哪里。
func TestRevealMissingFileDoesNotLaunch(t *testing.T) {
	if err := Reveal(filepath.Join(t.TempDir(), "nope.zip")); err == nil {
		t.Fatal("文件不存在时 Reveal 应该报错")
	}
}
