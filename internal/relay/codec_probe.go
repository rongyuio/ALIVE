package relay

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"alive/internal/ffmpeg"
)

const codecProbeBudget = 3 * time.Second

// probeVideoCodec 只为缺失声明的源补元数据，避免把未知编码当成 H.264 直接复制。
// 已知编码不调用；缺少 ffprobe 或探测失败保留未知，交给保守兼容转码。
func probeVideoCodec(ctx context.Context, upstream string, headers []string) string {
	probeCtx, cancel := context.WithTimeout(ctx, codecProbeBudget)
	defer cancel()
	args := []string{"-v", "error", "-rw_timeout", "2500000", "-analyzeduration", "1000000", "-probesize", "1000000"}
	if len(headers) > 0 {
		args = append(args, "-headers", joinHeaders(headers))
	}
	args = append(args, "-i", upstream, "-select_streams", "v:0", "-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1")
	// 组件未就绪时不猜编码：宁可多一次保守转码，也不把未知流当 AVC 复制。
	exe, err := ffmpeg.Path(ffmpeg.FFprobe)
	if err != nil {
		return ""
	}
	cmd := exec.CommandContext(probeCtx, exe, args...)
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	// stderr 可能带完整签名 URL，不进入日志或返回错误。
	if cmd.Run() != nil {
		return ""
	}
	switch strings.TrimSpace(out.String()) {
	case "h264":
		return "avc"
	case "hevc":
		return "hevc"
	case "av1":
		return "av1"
	default:
		return ""
	}
}
