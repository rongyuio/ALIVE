// Package relay 为原生播放器提供本地 HLS：支持的流透明代理，其余转封装或转码。
// 代理保留原始分片；兼容路径仅对 AVC 复制视频，其他编码转换为 H.264。
// 所有服务仅监听 127.0.0.1，传输决策见 transport.go。
package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"alive/internal/ffmpeg"
)

// errCanceled 表示这次启动被调用方取消。取消之后不允许再试别的路径
// （比如硬件转码失败回落软件编码），否则用户切房后还会白起一路 ffmpeg。
var errCanceled = context.Canceled

// Server 是一次传输会话：一个 FFmpeg 子进程或一个 HLS 代理 + 一个本地服务。
type Server struct {
	// startMu 串行化启动与回收。Stop 先取消 context，再等待这把锁，
	// 避免清理读取尚未由 cmd.Start 完成初始化的进程字段。
	startMu       sync.Mutex
	mu            sync.Mutex
	cmd           *exec.Cmd
	server        *http.Server
	proxy         *hlsProxy
	url           string
	dir           string
	token         string
	mode          string
	cancel        context.CancelFunc
	attemptCancel context.CancelFunc
	generation    uint64
	// done 在 ffmpeg 退出（或启动失败）时关闭。Stop 靠它等子进程真正
	// 结束，而不是和 cmd.Wait 抢同一个 Process.Wait。
	done chan struct{}
	// jobRelease 关掉绑定 ffmpeg 的 Job 句柄。非 Windows 上恒为 nil。
	jobRelease func()
}

// New 创建 Server，不启动任何进程。
func New() *Server {
	return &Server{token: randomToken()}
}

// URL 返回本地播放地址；未启动时为空。
func (s *Server) URL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.url
}

// Mode 返回这一次的传输方式：hls-proxy、remux 或 transcode。
// 未启动时返回空字符串。播放层据此识别真实路径并决定故障恢复。
func (s *Server) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// Options 是一次转封装的选项。
type Options struct {
	// Encoder 是转码时的编码器策略；零值按 EncoderAuto 处理。
	Encoder EncoderMode
	// HLSProxy 允许尝试透明代理；应用默认开启，调用方可用零值强制关闭。
	// 开启后明确 H.264 或系统支持的 HEVC / AV1 HLS 才会尝试，
	// HEVC / AV1 限带 MAP 的 fMP4；H.264 也接受 TS，不支持 BYTERANGE。
	HLSProxy bool
}

const transportStartupBudget = 20 * time.Second

// Start 打开一条上游流。会先停掉上一条，避免两个会话并存。
// headers 是回源需要的请求头，例如 "Referer: https://live.bilibili.com/"。
// codec 是上游视频编码（avc / hevc / av1），缺失时限时探测，仍未知则兼容转码。
//
// 打开 HLSProxy 且条件成立时先试透明代理：只补请求头、改写资源地址，
// 分片原样转发，省掉 FFmpeg 启动和重新切片。预检失败时代理已经把自己
// 清理干净，这里直接回到原来的 FFmpeg 路径。
//
// 用硬件编码器时，探测通过不代表真转码一定成功（驱动被别的程序占满、
// 显存不足都会让 ffmpeg 起不来）。这里在失败时回落 libx264 再试一次——
// 慢一点也比让用户看到「无法拉取直播流」强。
func (s *Server) Start(ctx context.Context, upstream string, headers []string, codec string, opts Options) (string, error) {
	return s.startWithBudget(ctx, upstream, headers, codec, opts, transportStartupBudget)
}

// 启动预算覆盖探测、代理预检与兼容回退的总和；成功后撤掉计时，不限制播放寿命。
func (s *Server) startWithBudget(ctx context.Context, upstream string, headers []string, codec string, opts Options, budget time.Duration) (result string, resultErr error) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// 启动期间也发布取消函数；Stop 能中断预检、编码器选择及启动等待。
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	runCtx, cancelCause := context.WithCancelCause(ctx)
	cancel := func() { cancelCause(context.Canceled) }
	startupExpired := make(chan struct{})
	startupTimer := time.AfterFunc(budget, func() {
		cancelCause(context.DeadlineExceeded)
		close(startupExpired)
	})
	s.generation++
	generation := s.generation
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		// 回调已开始时等它发布取消结果，避免刚返回成功就被到期计时器杀掉。
		if !startupTimer.Stop() {
			<-startupExpired
		}
		// 组件未就绪是独立语义，不能在上层被改写成普通启动超时。
		if runCtx.Err() != nil && !errors.Is(resultErr, ffmpeg.ErrPreparing) {
			result, resultErr = "", context.Cause(runCtx)
		}
		if resultErr != nil {
			cancel()
			s.cleanupAttempt()
		}
	}()
	go func() { <-runCtx.Done(); s.stopGeneration(generation) }()
	s.cleanupAttempt()
	if err := runCtx.Err(); err != nil {
		return "", err
	}

	codec = strings.ToLower(strings.TrimSpace(codec))
	if codec == "" {
		codec = probeVideoCodec(runCtx, upstream, headers)
		slog.Info("视频编码探测", "codec", codec, "resolved", codec != "")
		if runCtx.Err() != nil {
			return "", context.Cause(runCtx)
		}
	}
	plan := currentTransportPlan(upstream, codec, opts)
	slog.Info("播放路径决策", "mode", plan.primary, "fallback", plan.fallback, "reason", plan.reason)
	if plan.primary == modeProxy {
		result, resultErr = s.startProxy(runCtx, upstream, headers, codec)
		if runCtx.Err() != nil {
			return "", runCtx.Err()
		}
		if resultErr == nil {
			return result, nil
		}
		slog.Info("HLS 代理不可用，进入兼容路径", "mode", plan.fallback, "reason", redactError(resultErr))
	}
	result, resultErr = s.start(runCtx, upstream, headers, codec, opts)
	if runCtx.Err() != nil {
		return "", runCtx.Err()
	}
	if resultErr == nil {
		return result, nil
	}
	if errors.Is(resultErr, context.Canceled) || errors.Is(resultErr, context.DeadlineExceeded) {
		return "", resultErr
	}
	if needTranscode(codec) {
		encoder, err := pickVideoEncoderContext(runCtx, opts.Encoder)
		if err != nil {
			return "", err
		}
		if encoder != softwareEncoder {
			slog.Warn("硬件转码失败，回落软件编码", "encoder", encoder, "error", redactError(resultErr))
			return s.start(runCtx, upstream, headers, codec, Options{Encoder: EncoderSoftware})
		}
	}
	return result, resultErr
}

// proxyEligible 报告这次是否应该尝试 HLS 透明代理。
//
// 明确 H.264 可直接尝试；HEVC / AV1 必须有系统解码器且通过 fMP4 预检。
// 未知编码先限时探测，不能把空编码当 AVC。
func proxyEligible(opts Options, upstream, codec string) bool {
	return currentTransportPlan(upstream, codec, opts).primary == modeProxy
}

// startProxy 起代理并接管 Server 状态。失败时返回错误，调用方继续走 FFmpeg。
func (s *Server) startProxy(ctx context.Context, upstream string, headers []string, codec string) (string, error) {
	if ctx.Err() != nil {
		return "", errCanceled
	}
	preflightStart := time.Now()
	p, err := newHLSProxy(ctx, upstream, headers, codec)
	if err != nil {
		// 预检失败要能看出耗时：慢在回源还是格式不支持，排障时两条路完全不同。
		slog.Info("HLS 代理预检失败", "ms", time.Since(preflightStart).Milliseconds(), "reason", redactError(err))
		return "", err
	}
	slog.Info("HLS 代理预检完成", "ms", time.Since(preflightStart).Milliseconds())
	s.cleanupAttempt()
	s.mu.Lock()
	if ctx.Err() != nil {
		s.mu.Unlock()
		p.stop()
		return "", ctx.Err()
	}
	s.proxy = p
	s.url = p.url
	s.mode = "hls-proxy"
	s.mu.Unlock()
	slog.Info("传输方式", "mode", "hls-proxy")
	return p.url, nil
}

// start 是 Start 的实际实现；抽出来是为了让硬件失败时能重跑一遍。
func (s *Server) start(ctx context.Context, upstream string, headers []string, codec string, opts Options) (string, error) {
	upstream = trimSpace(upstream)
	if upstream == "" {
		return "", fmt.Errorf("上游地址为空")
	}
	if ctx.Err() != nil {
		return "", errCanceled
	}
	// 播放组件由应用自己准备：用户不需要安装 FFmpeg，也不该去改 PATH。
	// 首次使用可能还在下载，所以这里按本轮启动预算等待，而不是直接报错。
	ffmpegExe, err := ffmpeg.Ready(ctx, ffmpeg.FFmpeg)
	if err != nil {
		return "", fmt.Errorf("播放组件不可用: %w", err)
	}
	s.cleanupAttempt()

	dir, err := os.MkdirTemp("", "dtv-relay-")
	if err != nil {
		return "", fmt.Errorf("创建转封装目录: %w", err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("监听本地端口: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	owned := false
	defer func() {
		if !owned {
			cancel()
			_ = listener.Close()
			_ = os.RemoveAll(dir)
		}
	}()
	mux := http.NewServeMux()
	// 只允许 m3u8 与 ts，且要求 token，避免本机其它页面读文件。
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// token 放在路径里而不是 query：IMFMediaEngine 会按 URL 后缀判断
		// 媒体类型，带 ?t= 的 m3u8 会被判成未知格式。
		if !strings.HasPrefix(r.URL.Path, "/"+s.token+"/") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		name := filepath.Base(r.URL.Path)
		if name != "live.m3u8" && filepath.Ext(name) != ".ts" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch {
		case name == "live.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		case filepath.Ext(name) == ".ts":
			w.Header().Set("Content-Type", "video/mp2t")
		}
		http.ServeFile(w, r, filepath.Join(dir, name))
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// mode 是这次会话的传输方式，Mode() 会把它交给界面日志。
	mode := "remux"
	if needTranscode(codec) {
		mode = "transcode"
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning",
		"-rw_timeout", "10000000",
	}
	if isHLSInput(upstream) {
		// B 站 / 虎牙分片：探测窗口压到最小尽快出首片（HLS 第一个分片就带
		// SPS/PPS，32KB 够用）；同时关掉 HTTP keep-alive——B 站 CDN 会主动
		// 掐长连接，复用连接时每片都要重连，60 秒能刷 40 多次 I/O error。
		args = append(args,
			"-http_persistent", "0",
			// 窗口不能压到极小：从直播流中间切入时收不全 SPS/PPS 和首个 IDR，
			// 转码分支必须解码，缺参考帧就是 `Could not find ref with POC`，成批丢帧。
			// 压小的收益只是 20ms 级别的首帧，代价是整条转码链路的正确性。
			"-probesize", "5000000", "-analyzeduration", "2000000",
			// live_start_index 默认 -3：ffmpeg 会从倒数第三片开始下，白拉
			// 6~9 秒已经播过的内容才等到第一个关键帧。-1 直接取最新一片，
			// 首片落盘能快半秒到一秒半，画质和稳定性都不受影响。
			"-live_start_index", "-1",
		)
	} else {
		// 斗鱼 / 抖音：HTTP-FLV 直连。这里不能用 HLS 那套极小的探测窗口：
		// FLV 的 SPS/PPS 不一定落在前 32KB 里，ffmpeg 找不到流参数会直接
		// 以 EINVAL 退出（虎牙实测就是这个）。留半秒分析窗口。
		//
		// http_persistent 也不能带——它不是 FLV/HTTP 那条路的选项，
		// ffmpeg 会以 "Option http_persistent not found" 立刻退出。
		args = append(args,
			"-probesize", "500000", "-analyzeduration", "500000",
		)
	}
	if len(headers) > 0 {
		args = append(args, "-headers", joinHeaders(headers))
	}
	args = append(args, "-i", upstream)
	if needTranscode(codec) {
		// 原生代理不可用时，把 HEVC / AV1 转成兼容路径需要的 H.264
		// ——直接 -c copy 的话 ffmpeg 会把视频轨整个丢掉，界面上只剩黑屏
		// 加一段音频。这里只转视频、音频照旧直通，2 秒一个关键帧保证
		// 分片边界干净。
		//
		// 编码器由设置项决定：auto 时优先硬编（HEVC 1080p 从占 5-6 个核
		// 降到几乎为零），software 时强制 libx264 换画质。
		enc, err := pickVideoEncoderContext(runCtx, opts.Encoder)
		if err != nil {
			return "", err
		}
		slog.Info("转封装方式", "mode", "transcode", "codec", codec, "encoder", enc)
		args = append(args, "-map", "0:v:0?", "-map", "0:a:0?")
		args = append(args, videoEncodeArgs(enc)...)
		args = append(args,
			"-force_key_frames", "expr:gte(t,n_forced*2)",
			"-c:a", "copy",
		)
	} else {
		// 上游本来就是 H.264，只换容器。这条路 CPU 接近零，分片时长由
		// 上游关键帧决定（B 站是 3 秒），起播与稳定性都和转码路径不同。
		// 打一行日志是为了排查时不用去猜——哪条路一眼能看出来。
		slog.Info("转封装方式", "mode", mode, "codec", codec)
		// 必须显式只取视频和音频。默认选流会把字幕一起带进来——虎牙的
		// HTTP-FLV 里就挂着一条 webvtt 轨，而 MPEG-TS 不支持 webvtt，
		// ffmpeg 会以 EINVAL 直接退出，表现就是「虎牙播不了」。
		args = append(args,
			"-map", "0:v:0?",
			"-map", "0:a:0?",
			"-c", "copy",
		)
	}
	// HLS 输出参数全部是按播放器实测调出来的，逐条说明：
	//
	//   -hls_time 2   只是目标值。-c copy 时只能在关键帧处切分片，B 站
	//                 直播 GOP 是 3 秒，所以实际分片永远是 3 秒。
	//                 转码路径也保持 2：试过降到 1（起播能快 1 秒），但 1 秒
	//                 窗口里 vbr_latency 摊不平 I 帧，分片大小从 346KB 飘到
	//                 1845KB（5 倍差），播放器按大小预估缓冲会失真。稳定优先。
	//   -hls_init_time 1  只有第一个分片用更短的目标时长。起播时用户等的
	//                 就是它；-c copy 仍受上游关键帧限制，但语义从「等满
	//                 2 秒再找关键帧」变成「过了 1 秒遇到关键帧就切」。
	//   -hls_list_size 10  窗口留 10 片 ≈ 20~30 秒。原来只有 5 个（15 秒），
	//                 Media Foundation 预缓冲慢一点就撞上列表滚动，请求到
	//                 已被删掉的分片 —— 表现就是左下角「缓冲中…」常驻、
	//                 画面周期性顿一下。
	//   temp_file     先写 .tmp 再 rename。没有它时播放器可能读到写了一半的
	//                 分片或播放列表，解析失败后重试，也是同一个症状。
	//   omit_endlist  直播，不能有 ENDLIST。
	//   independent_segments  告诉播放器每个分片都能独立解码（-c copy 从
	//                 关键帧切分，成立）。缺这个标记时，部分播放器会从头
	//                 开始要求完整序列，窗口一滚动就缓冲。
	//   不加 program_date_time：本地转封装不需要绝对时间轴，带 PDT 反而
	//                 会让播放器去追某个墙钟时间，触发无谓的 seek。
	// 转码路径的分片再短一档：播放器要等第二个分片才开始出画，1 秒分片让
	// 第二片早 1 秒到。代价是 1 秒窗口里 vbr_latency 摊不平 I 帧、分片大小
	// 波动更大，只用在“系统确实解不了、必须转码”这条兜底路径上。
	hlsTime := "2"
	if mode == modeTranscode {
		hlsTime = "1"
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", hlsTime,
		"-hls_init_time", "1",
		"-hls_list_size", "10",
		"-hls_flags", "delete_segments+temp_file+omit_endlist+independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, "live.m3u8"),
	)
	if err := runCtx.Err(); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(runCtx, ffmpegExe, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = nil
	stderr := &relayLog{token: s.token}
	cmd.Stderr = stderr

	done := make(chan struct{})
	s.mu.Lock()
	s.cmd, s.server, s.dir, s.done = cmd, server, dir, done
	s.attemptCancel = cancel
	s.mode = mode
	s.url = fmt.Sprintf("http://%s/%s/live.m3u8", listener.Addr().String(), s.token)
	url := s.url
	s.mu.Unlock()
	owned = true

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Warn("转封装本地服务退出", "error", err)
		}
	}()
	if err := cmd.Start(); err != nil {
		close(done)
		// Serve 可能尚未开始，显式关闭 listener，不依赖 Server.Close 接管成功。
		_ = listener.Close()
		s.cleanupAttempt()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("启动 ffmpeg: %w", redactError(err))
	}

	// 把 ffmpeg 绑到本进程的生死上。应用被强杀（任务管理器、崩溃）时
	// Go 没有机会执行 Stop，Windows 也不会连带结束子进程——ffmpeg 会变
	// 成孤儿继续拉流转码，残留几个就把 CPU 吃满，之后所有播放都卡。
	// 绑定失败不致命，退回到「只有 Stop 能清理」。
	if release, bindErr := bindProcessLifecycle(cmd.Process.Pid); bindErr != nil {
		slog.Warn("绑定 ffmpeg 生命周期失败，退出时可能残留子进程", "error", bindErr)
	} else {
		s.mu.Lock()
		s.jobRelease = release
		s.mu.Unlock()
	}
	var waitErr error // 由 done 关闭同步到启动检查，不能在收到 done 前读取。
	go func() {
		waitErr = cmd.Wait()
		stderr.flush()
		if waitErr != nil && runCtx.Err() == nil {
			slog.Warn("ffmpeg 失败详情", "message", stderr.summary())
		}
		slog.Info("ffmpeg 转封装结束", "error", waitErr, "ctxCanceled", runCtx.Err() != nil)
		close(done)
	}()

	// 等 FFmpeg 写出第一份播放列表再返回。播放器不会重试一次失败的打开，
	// 所以这里必须等到分片真的可读。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if runCtx.Err() != nil {
			s.cleanupAttempt()
			return "", errCanceled
		}
		// 快速完成的有限流可能先退出再被轮询到；成功退出且输出完整仍然可播。
		finished := false
		select {
		case <-done:
			finished = true
			if waitErr != nil {
				s.cleanupAttempt()
				return "", fmt.Errorf("ffmpeg 启动失败: %w", redactError(waitErr))
			}
		default:
		}
		if localPlaylistReady(dir) {
			return url, nil
		}
		if finished {
			s.cleanupAttempt()
			return "", fmt.Errorf("ffmpeg 已退出，未生成完整播放列表")
		}
		select {
		case <-runCtx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	s.cleanupAttempt()
	if ctx.Err() != nil {
		return "", errCanceled
	}
	return "", fmt.Errorf("等待转封装就绪超时")
}

// Stop 先取消当前会话，再等待启动互斥锁完成资源回收；不会与 cmd.Start
// 并发读取 Process 字段，也不会误停随后创建的新一代会话。
func (s *Server) Stop() {
	s.mu.Lock()
	generation := s.generation
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.stopGeneration(generation)
}

func (s *Server) stopGeneration(generation uint64) {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	matches := s.generation == generation
	if matches {
		if s.cancel != nil {
			s.cancel()
		}
		s.cancel = nil
	}
	s.mu.Unlock()
	if matches {
		s.cleanupAttempt()
	}
}

// cleanupAttempt 必须持有 startMu；一次失败回退只回收该次资源，不取消
// 整个会话。子进程只有 cmd.Wait 一个等待者。
func (s *Server) cleanupAttempt() {
	s.mu.Lock()
	cmd, server, dir, cancel := s.cmd, s.server, s.dir, s.attemptCancel
	proxy, done, release := s.proxy, s.done, s.jobRelease
	s.cmd, s.server, s.dir, s.url, s.attemptCancel = nil, nil, "", "", nil
	s.proxy, s.done, s.jobRelease, s.mode = nil, nil, nil, ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if proxy != nil {
		proxy.stop()
	}
	if server != nil {
		_ = server.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if release != nil {
		release()
	}
	if done != nil {
		<-done
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// needTranscode 只描述兼容 MPEG-TS 输出路径，不代表原生播放器的完整解码能力。
// 未知编码不能当作 AVC 复制，否则可能产出无视频的列表；已知 AVC 才直接复制。
func needTranscode(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "avc", "h264":
		return false
	}
	return true
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\n' || s[0] == '\r' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ' ' || last == '\n' || last == '\r' || last == '\t' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func joinHeaders(headers []string) string {
	out := ""
	for _, h := range headers {
		out += trimSpace(h) + "\r\n"
	}
	return out
}

func randomToken() string {
	// token 保护本地服务，用系统随机源避免时间种子的可预测性。
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		panic("无法生成本地会话标识")
	}
	return hex.EncodeToString(token[:])
}

// NativeDecode 报告系统能不能直接解码这个编码。
//
// 自带解码路径据此决定要不要接手：系统能解就走透明代理（少一层、首帧更快），
// 解不了才自己上。未声明的编码返回 true，让 relay 原有的探测链去判断。
func NativeDecode(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "hevc", "h265":
		return nativeHEVCAvailable()
	case "av1":
		return nativeAV1Available()
	default:
		return true
	}
}
