// Package support 收集诊断证据：日志、最近事件、崩溃现场、环境指纹和指标。
//
// 它只打包已经脱敏的产物，绝不读取 state.json 或登录态文件。support bundle
// 是给用户/支持人员的一份 zip：解开就能看到 run、构建、指标和原始日志。
package support

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"alive/internal/metrics"
)

// Options 是一次打包的输入。
type Options struct {
	Out     string
	LogDir  string
	Meta    map[string]any
	KeepRun int // 保留最近几次运行的 alive-*.log；默认 3
}

// Bundle 把日志目录里的证据打成一个 zip，返回输出路径。
func Bundle(opts Options) (string, error) {
	if opts.Out == "" {
		return "", fmt.Errorf("没有输出路径")
	}
	if opts.LogDir == "" {
		return "", fmt.Errorf("没有日志目录")
	}
	keepRun := opts.KeepRun
	if keepRun <= 0 {
		keepRun = 3
	}

	files, err := collect(opts.LogDir, keepRun)
	if err != nil {
		return "", err
	}

	meta := map[string]any{}
	maps.Copy(meta, opts.Meta)
	meta["generated_at"] = time.Now().Format(time.RFC3339)
	meta["log_dir"] = opts.LogDir
	meta["metrics"] = metrics.Snapshot()
	names := make([]string, 0, len(files))
	for _, f := range files {
		if fi, err := os.Stat(f); err == nil {
			names = append(names, fmt.Sprintf("%s\t%d", filepath.Base(f), fi.Size()))
		}
	}
	meta["files"] = names

	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	manifest := strings.Join(names, "\n") + "\n"

	f, err := os.Create(opts.Out)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	if err := addBytes(zw, "meta.json", metaJSON); err != nil {
		return "", err
	}
	if err := addBytes(zw, "manifest.txt", []byte(manifest)); err != nil {
		return "", err
	}
	for _, path := range files {
		if err := addFile(zw, path); err != nil {
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return opts.Out, nil
}

// collect 选最近几次运行的日志和最新的辅助证据。
func collect(dir string, keepRun int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type item struct {
		path string
		mod  time.Time
	}
	byKind := map[string][]item{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		kind := ""
		switch {
		case strings.HasPrefix(name, "alive-") && strings.HasSuffix(name, ".log"):
			kind = "run"
		case strings.HasPrefix(name, "recent-") && strings.HasSuffix(name, ".log"):
			kind = "recent"
		case strings.HasPrefix(name, "crash-") && strings.HasSuffix(name, ".log"):
			kind = "crash"
		case strings.HasPrefix(name, "hang-") && (strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".dmp")):
			kind = "hang"
		case strings.HasPrefix(name, "trace-") && strings.HasSuffix(name, ".out"):
			kind = "trace"
		case strings.HasPrefix(name, "cpu-") && strings.HasSuffix(name, ".pprof"):
			kind = "profile"
		default:
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		byKind[kind] = append(byKind[kind], item{path: filepath.Join(dir, name), mod: info.ModTime()})
	}
	var out []string
	for kind, list := range byKind {
		sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
		limit := keepRun
		if kind != "run" {
			limit = 5
		}
		if len(list) > limit {
			list = list[:limit]
		}
		for _, it := range list {
			out = append(out, it.path)
		}
	}
	sort.Strings(out)
	return out, nil
}

func addBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func addFile(zw *zip.Writer, path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	info, err := src.Stat()
	if err != nil {
		return err
	}
	h, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	h.Name = filepath.Base(path)
	h.Method = zip.Deflate
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}
