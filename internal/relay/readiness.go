package relay

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// localPlaylistReady 只检查本模块产出的 TS 列表，不能用目录里不相关的旧分片判就绪。
// FFmpeg 原子替换列表后，列表引用的每片都必须已落盘，播放器才不会首次打开即失败。
func localPlaylistReady(dir string) bool {
	body, err := os.ReadFile(filepath.Join(dir, "live.m3u8"))
	if err != nil {
		return false
	}
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "#EXTM3U" {
		return false
	}
	duration, segments := false, 0
	for _, raw := range lines[1:] {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#EXTINF:") {
			if duration {
				return false
			}
			value, _, _ := strings.Cut(strings.TrimPrefix(line, "#EXTINF:"), ",")
			seconds, err := strconv.ParseFloat(value, 64)
			if err != nil || seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
				return false
			}
			duration = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !duration || strings.ContainsAny(line, "/\\:?#") || filepath.Ext(line) != ".ts" {
			return false
		}
		info, err := os.Stat(filepath.Join(dir, line))
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return false
		}
		duration = false
		segments++
	}
	return segments > 0 && !duration
}
