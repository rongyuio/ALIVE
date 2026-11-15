package relay

import (
	"context"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"alive/internal/ffmpeg"
)

// EncoderMode 是转码时挑编码器的策略，由界面设置项决定（后面接设置页）。
type EncoderMode string

const (
	// EncoderAuto 硬件优先：探测到能用的硬编就用，没有就回落软编。
	EncoderAuto EncoderMode = "auto"
	// EncoderSoftware 强制 libx264：画质更好，代价是 2K 转码吃满几个核。
	EncoderSoftware EncoderMode = "software"
)

// softwareEncoder 是兜底编码器，永远可用。
const softwareEncoder = "libx264"

// 转码输出的目标码率。2K 直播转 H.264，8M 够用且留了画质余量；
// 走 -c copy 的房间不经过这里，不受影响。
const (
	relayBitrate = "8M"
	relayBufsize = "16M"
)

// ParseEncoderMode 把界面/落盘的值归一化，认不出的一律按 auto。
func ParseEncoderMode(s string) EncoderMode {
	if EncoderMode(s) == EncoderSoftware {
		return EncoderSoftware
	}
	return EncoderAuto
}

// hardwareEncoders 是按优先级尝试的硬件编码器。
//
// 一个人不会同时有 N 卡和 A 卡可用，顺序只影响多显卡机器；探测命中即止，
// 全都不行就回落 libx264。只列 H.264——MPEG-TS 能装的也就它最稳。
var hardwareEncoders = []string{
	"h264_nvenc",
	"h264_amf",
	"h264_qsv",
	"h264_d3d12va",
}

var (
	probeOnce sync.Once
	probed    string
)

// pickVideoEncoder 决定这次转码用哪个编码器，结果在进程内只探测一次。
//
// 不能读 `ffmpeg -encoders`：那是「编译时带了什么」，和「这台机器现在能不能
// 初始化」是两件事。实测本机列出 5 个 H.264 编码器，只有 2 个真能用——
// h264_nvenc 报 Cannot load nvcuda.dll，h264_qsv 报 MFX session 失败。
func pickVideoEncoder(mode EncoderMode) string {
	if mode == EncoderSoftware {
		return softwareEncoder
	}
	probeOnce.Do(func() {
		for _, name := range hardwareEncoders {
			if probeEncoder(name) {
				probed = name
				break
			}
		}
		if probed == "" {
			slog.Info("转码器选择", "video", softwareEncoder, "reason", "没有可用的硬件编码器")
			return
		}
		slog.Info("转码器选择", "video", probed, "mode", string(EncoderAuto))
	})
	if probed == "" {
		return softwareEncoder
	}
	return probed
}

// probeEncoder 真编 0.2 秒来确认编码器可用。
//
// 参数必须和真实转码一致（像素格式尤其）：之前用 nullsrc 探 h264_d3d12va
// 报的是「格式转换失败」，看着像编码器坏了，其实只是探测命令写错。
func probeEncoder(name string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	exe, err := ffmpeg.Path(ffmpeg.FFmpeg)
	if err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, exe,
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=s=1280x720:d=0.2",
		"-pix_fmt", "yuv420p",
		"-c:v", name,
		"-f", "null", "-",
	)
	return cmd.Run() == nil
}

// videoEncodeArgs 返回某个编码器要用的视频参数。
//
// 参数名全部来自 `ffmpeg -h encoder=<name>` —— ffmpeg.org 的 codecs 文档
// 至今没有 AMD AMF 章节，网上的写法多半是抄来的，靠不住。硬编不能照抄
// libx264 那一套，三个地方必须换：
//
//  1. 关键帧。硬编普遍不认 -force_key_frames（它是 ffmpeg.c 层面的请求：
//     把某些帧标成 I，编码器可以不理）。AMF 实测：只给 force_key_frames
//     时分片 4 秒起跳。改用编码器自己的 -g（GOP 帧数），取 60：60fps 下
//     1 秒、30fps 下 2 秒一个关键帧。试过收到 30（关键帧更密、首片更短），
//     实测反而更慢——IDR 密度翻倍后 AMF 每片体积变大，首片落盘从 1.6 秒
//     涨到 2.4 秒。缩短首帧改在分片目标时长上做，见 relay.go 的 hlsTime。
//  2. 码控。libx264 默认 CRF 就够看，硬编默认是 CQP（画质随内容乱飘）。
//     AMF 上尤其明显：-rc cbr 时分片从 230KB 跳到 3116KB（13 倍），
//     播放器按这种节奏必然反复进出缓冲；换成 -rc vbr_latency
//     （Latency Constrained VBR）后收敛到 1.3~2.3MB。
//  3. 低延迟开关。AMF 的 -usage 是「usecase 预设」，不是低延迟开关；
//     真正的是 -latency <bool>。而 -usage lowlatency 实测会让 AMF
//     静默不输出任何 packet（frame 计数在涨、size 始终 N/A，muxer 等不到
//     关键帧），且 `-f null` 探测发现不了——所以这里不碰 -usage。
//
// 只有 h264_amf 在本机（AMD RX 9070 XT）实测过；nvenc / qsv / d3d12va
// 按各自 help 的选项名写，没有对应硬件验证，改动时请以实测为准。
func videoEncodeArgs(enc string) []string {
	switch enc {
	case "h264_nvenc":
		return []string{
			"-c:v", "h264_nvenc",
			"-preset", "p1", "-tune", "ll",
			"-rc", "cbr", "-b:v", relayBitrate, "-maxrate", relayBitrate, "-bufsize", relayBufsize,
			"-g", "60", "-bf", "0", "-forced-idr", "1",
			"-pix_fmt", "yuv420p",
		}
	case "h264_amf":
		// -quality 与 -preset 在这个编码器上是同一选项的两个名字，写一个就够。
		// -bf 0 去掉 B 帧重排，-forced_idr 保证分片边界是 IDR（注意这里是
		// 下划线的私有选项；连字符的 -forced-idr 是通用选项，AMF 未必读）。
		return []string{
			"-c:v", "h264_amf",
			"-latency", "1", "-quality", "speed",
			"-rc", "vbr_latency", "-b:v", relayBitrate, "-maxrate", relayBitrate, "-bufsize", relayBufsize,
			"-g", "60", "-bf", "0", "-forced_idr", "1",
			"-pix_fmt", "nv12",
		}
	case "h264_qsv":
		// qsv 没有 -low_delay 这个私有选项（别照 AMF 的写法抄），
		// 它的低延迟靠 look_ahead 关掉 + async_depth 压低。
		return []string{
			"-c:v", "h264_qsv",
			"-preset", "veryfast", "-look_ahead", "0",
			"-b:v", relayBitrate, "-maxrate", relayBitrate, "-bufsize", relayBufsize,
			"-g", "60", "-bf", "0",
			"-pix_fmt", "nv12",
		}
	case "h264_d3d12va":
		return []string{
			"-c:v", "h264_d3d12va",
			"-b:v", relayBitrate, "-maxrate", relayBitrate, "-bufsize", relayBufsize,
			"-g", "60", "-bf", "0",
			"-pix_fmt", "yuv420p",
		}
	default:
		return []string{
			"-c:v", softwareEncoder,
			"-preset", "veryfast", "-tune", "zerolatency",
			"-profile:v", "high", "-pix_fmt", "yuv420p",
		}
	}
}

// WarmUpEncoder 提前把硬件编码器探测跑掉。
//
// 探测要起一个 ffmpeg 真编 0.2 秒来确认能不能初始化（约 0.5~1 秒），
// 而一个进程里只做一次。如果等用户第一次进转码房间才做，那一次起播就
// 白等这一秒；放在应用启动后的启动画面期间跑，用户看不到。
//
// 探测的是硬件可不可用，和设置项无关，所以这里固定按 auto 探。
func WarmUpEncoder() {
	go func() {
		// 干净机器上第一次启动时组件还在后台安装；先等它就绪再探测，
		// 否则这次探测会把“没有 ffmpeg”误记成“没有硬件编码器”。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := ffmpeg.Ready(ctx, ffmpeg.FFmpeg); err != nil {
			return
		}
		pickVideoEncoder(EncoderAuto)
	}()
}

// pickVideoEncoderContext 让会话取消不必等共享探测结束；探测本身只启动
// 一次，预热与播放共用结果，取消不会触发软件编码重试。
func pickVideoEncoderContext(ctx context.Context, mode EncoderMode) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if mode == EncoderSoftware {
		return softwareEncoder, nil
	}
	result := make(chan string, 1)
	go func() { result <- pickVideoEncoder(mode) }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case encoder := <-result:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return encoder, nil
	}
}
