// Command gofmtcheck 判断哪些 Go 文件需要 gofmt，判据只有这一份。
//
// 为什么不是 `gofmt -l .`：Makefile、CI、pre-commit 钩子三处都要判同一件事。
// 让三处各写一遍排除列表，就是让它们迟早分叉——分叉的表现是「本地 make check
// 绿、CI 红」，而两边都没错，只是问的不是同一个问题。所以排除规则写在这里，
// 三处都调这条命令。
//
// 排除的是不属于本 module 或不入版本管理的树：
//
//	third_party/  独立 module，上游代码，格式由上游决定（见 doc/reference/mygo.md）
//	.tmp/         探针与 e2e 证据，手工维护的草稿
//	.mygo/        沙箱（APPDATA 被指到这里）
//	build/        生成物
//
// 用法：
//
//	go run ./tools/gofmtcheck            # 扫仓库根
//	go run ./tools/gofmtcheck a.go b.go  # 只查这几个（钩子给暂存文件用，排除规则照旧）
//
// 非零退出表示需要 gofmt，或文件解析不了。
package main

import (
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// skipDirs 是不归 gofmt 管的树，按路径第一段匹配。
var skipDirs = map[string]string{
	"third_party": "独立 module，上游代码",
	".tmp":        "探针与证据草稿",
	".mygo":       "沙箱",
	"build":       "生成物",
	".git":        "不是源码",
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	files, skipped, err := collect(root, os.Args[1:])
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	var bad, broken []string
	for _, rel := range files {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			broken = append(broken, rel+": "+err.Error())
			continue
		}
		out, err := format.Source(src)
		if err != nil {
			// 解析不了就是语法错误。go build 也会红，但这里报得更早、指得更准。
			broken = append(broken, rel+": "+err.Error())
			continue
		}
		if string(out) != string(src) {
			bad = append(bad, rel)
		}
	}

	if len(broken) > 0 {
		fmt.Println("解析不了（gofmt 跑不过，先修语法）：")
		for _, b := range broken {
			fmt.Println("  " + b)
		}
	}
	if len(bad) > 0 {
		fmt.Println("needs gofmt（跑 make fmt）：")
		for _, b := range bad {
			fmt.Println("  " + b)
		}
	}
	if len(broken) > 0 || len(bad) > 0 {
		os.Exit(1)
	}

	fmt.Printf("gofmt ok：%d 个文件", len(files))
	if skipped > 0 {
		fmt.Printf("，跳过 %d 个（%s）", skipped, skipList())
	}
	fmt.Println()
}

// collect 返回要检查的相对路径（斜杠形式）。args 为空时扫全树。
func collect(root string, args []string) (files []string, skipped int, err error) {
	if len(args) > 0 {
		for _, a := range args {
			rel := filepath.ToSlash(filepath.Clean(filepath.FromSlash(a)))
			if !strings.HasSuffix(rel, ".go") {
				continue
			}
			if why, skip := skipReason(rel); skip {
				_ = why
				skipped++
				continue
			}
			files = append(files, rel)
		}
		return files, skipped, nil
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if _, skip := skipReason(rel); skip {
				skipped++
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") {
			return nil
		}
		files = append(files, rel)
		return nil
	})
	sort.Strings(files)
	return files, skipped, err
}

// skipReason 只看路径第一段：第三方 module 和沙箱都是整棵子树不归这里管。
func skipReason(rel string) (string, bool) {
	if rel == "." || rel == "" {
		return "", false
	}
	why, ok := skipDirs[strings.SplitN(rel, "/", 2)[0]]
	return why, ok
}

func skipList() string {
	names := make([]string, 0, len(skipDirs))
	for n := range skipDirs {
		if n == ".git" {
			continue
		}
		names = append(names, n+"/")
	}
	sort.Strings(names)
	return strings.Join(names, " ")
}
