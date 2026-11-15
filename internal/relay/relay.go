// Package relay 把上游直播流转封装成本地 HLS，供 MyGo 的 ui/video 播放。
//
// 为什么需要：Windows Media Foundation 打 B 站 CDN 的 m3u8 会返回
// 0xc00d2ee7（缺 Referer / 相对分片解析），斗鱼与抖音的 HTTP-FLV 更是完全
// 不在 MF 支持的容器里。FFmpeg 默认只做 -c copy 转封装，不解码、不转码，
// 单路 CPU 接近于零；只有上游是 HEVC / AV1（系统解码器不一定有，
// AV1 更是塞不进 MPEG-TS）时才把视频转成 H.264。本地 HTTP 服务只监听
// 127.0.0.1。
package relay

import (
	"context"
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
)

// Server 是一次转封装会话：一个 FFmpeg 子进程 + 一个本地静态服务。
type Server struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	server  *http.Server
	url     string
	dir     string
	token   string
	headers []string
	cancel  context.CancelFunc
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

// Options 是一次转封装的选项。
type Options struct {
	// Encoder 是转码时的编码器策略；零值按 EncoderAuto 处理。
	Encoder EncoderMode
}

// Start 打开一条上游流。会先停掉上一条，避免两个 FFmpeg 并存。
// headers 是回源需要的请求头，例如 "Referer: https://live.bilibili.com/"。
// codec 是上游视频编码（avc / hevc / av1），为空按 avc 处理。
//
// 用硬件编码器时，探测通过不代表真转码一定成功（驱动被别的程序占满、
// 显存不足都会让 ffmpeg 起不来）。这里在失败时回落 libx264 再试一次——
// 慢一点也比让用户看到「无法拉取直播流」强。
func (s *Server) Start(ctx context.Context, upstream string, headers []string, codec string, opts Options) (string, error) {
	url, err := s.start(ctx, upstream, headers, codec, opts)
	if err == nil {
		return url, nil
	}
	// 回落一：硬件编码器不通（驱动被别的程序占满、显存不足）。
	if needTranscode(codec) && pickVideoEncoder(opts.Encoder) != softwareEncoder {
		slog.Warn("硬件转码失败，回落软件编码",
			"encoder", pickVideoEncoder(opts.Encoder), "error", err)
		return s.start(ctx, upstream, headers, codec, Options{Encoder: EncoderSoftware})
	}
	return url, err
}

// start 是 Start 的实际实现；抽出来是为了让硬件失败时能重跑一遍。
func (s *Server) start(ctx context.Context, upstream string, headers []string, codec string, opts Options) (string, error) {
	upstream = trimSpace(upstream)
	if upstream == "" {
		return "", fmt.Errorf("上游地址为空")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return "", fmt.Errorf("找不到 ffmpeg，请安装并加入 PATH: %w", err)
	}
	s.Stop()

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
			"-probesize", "32768", "-analyzeduration", "0",
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
		// 上游是 HEVC / AV1：MF 只吃 H.264，AV1 更是没法封进 MPEG-TS
		// ——直接 -c copy 的话 ffmpeg 会把视频轨整个丢掉，界面上只剩黑屏
		// 加一段音频。这里只转视频、音频照旧直通，2 秒一个关键帧保证
		// 分片边界干净。
		//
		// 编码器由设置项决定：auto 时优先硬编（HEVC 1080p 从占 5-6 个核
		// 降到几乎为零），software 时强制 libx264 换画质。
		enc := pickVideoEncoder(opts.Encoder)
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
		slog.Info("转封装方式", "mode", "copy", "codec", codec)
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
	args = append(args,
		"-f", "hls",
		"-hls_time", "2",
		"-hls_init_time", "1",
		"-hls_list_size", "10",
		"-hls_flags", "delete_segments+temp_file+omit_endlist+independent_segments",
		"-hls_segment_filename", filepath.Join(dir, "seg-%05d.ts"),
		filepath.Join(dir, "live.m3u8"),
	)
	cmd := exec.CommandContext(runCtx, "ffmpeg", args...)
	cmd.Stdout = nil
	cmd.Stderr = &relayLog{}

	s.mu.Lock()
	s.cmd, s.server, s.dir = cmd, server, dir
	s.cancel = cancel
	s.headers = append([]string(nil), headers...)
	s.url = fmt.Sprintf("http://%s/%s/live.m3u8", listener.Addr().String(), s.token)
	url := s.url
	s.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Warn("转封装本地服务退出", "error", err)
		}
	}()
	if err := cmd.Start(); err != nil {
		cancel()
		_ = server.Close()
		_ = os.RemoveAll(dir)
		s.mu.Lock()
		s.cmd, s.server, s.dir, s.url = nil, nil, "", ""
		s.mu.Unlock()
		return "", fmt.Errorf("启动 ffmpeg: %w", err)
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
	exited := make(chan struct{})
	go func() {
		err := cmd.Wait()
		slog.Info("ffmpeg 转封装结束", "error", err, "ctxCanceled", runCtx.Err() != nil)
		close(exited)
	}()

	// 等 FFmpeg 写出第一份播放列表再返回。播放器不会重试一次失败的打开，
	// 所以这里必须等到分片真的可读。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if runCtx.Err() != nil {
			break
		}
		// ffmpeg 自己退了就别再干等满 15 秒：多半是参数问题——这一路要求
		// 输出音频，而这个流里根本没有音频轨。立刻交回 Start 去回退。
		select {
		case <-exited:
			s.Stop()
			return "", fmt.Errorf("ffmpeg 启动后立即退出")
		default:
		}
		if info, err := os.Stat(filepath.Join(dir, "live.m3u8")); err == nil && info.Size() > 0 {
			// 等第一个分片也落盘，否则播放器打开时会拿到空列表。
			entries, _ := os.ReadDir(dir)
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".ts" {
					if fi, err := e.Info(); err == nil && fi.Size() > 0 {
						return url, nil
					}
				}
			}
		}
		// 50ms 而不是 200ms：首片一落盘就能返回，平均少等 75ms，
		// 而这点空转对 CPU 毫无意义——循环里只有两次 stat。
		time.Sleep(50 * time.Millisecond)
	}
	s.Stop()
	return "", fmt.Errorf("等待转封装就绪超时")
}

// Stop 关闭 FFmpeg 与本地服务，删除临时分片。
func (s *Server) Stop() {
	s.mu.Lock()
	cmd, server, dir, cancel := s.cmd, s.server, s.dir, s.cancel
	release := s.jobRelease
	s.cmd, s.server, s.dir, s.url, s.cancel = nil, nil, "", "", nil
	s.jobRelease = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if server != nil {
		ctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(ctx)
		done()
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	// 关掉 Job 句柄。此时子进程已经被 kill 过；这里是给没来得及 kill 的情况
	// 兜底——句柄一关，Job 里还剩的进程会被内核结束。
	if release != nil {
		release()
	}
}

// isHLSInput 报告上游地址是不是 HLS（m3u8）。
//
// 只看路径，别被 query 里的字符串骗了：斗鱼的分片地址带一堆参数，
// 抖音的 FLV 也可能带 .m3u8 字样。
func isHLSInput(u string) bool {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	return strings.HasSuffix(strings.ToLower(u), ".m3u8")
}

// relayLog 把 ffmpeg stderr 限流写进 slog，不把上游 URL 打进日志。
type relayLog struct {
	mu   sync.Mutex
	last time.Time
}

func (l *relayLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if time.Since(l.last) < time.Second {
		return len(p), nil
	}
	l.last = time.Now()
	msg := string(p)
	if len(msg) > 400 {
		msg = msg[:400]
	}
	slog.Warn("ffmpeg", "message", msg)
	return len(p), nil
}

// needTranscode 报告上游视频编码是否必须转码后才能播。
//
// H.264 是唯一能保证被系统解码器接住的：HEVC 要看有没有装「HEVC 视频
// 扩展」，AV1 除了解码器问题，还根本没法封进 MPEG-TS——直接 -c copy 时
// ffmpeg 会静默把视频轨丢掉，界面上就是黑屏加一段音频。
func needTranscode(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "", "avc", "h264":
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
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 24)
	// 用纳秒时间混合，token 只用于本机隔离，不需要密码学强度。
	n := time.Now().UnixNano()
	for i := range b {
		n = n*6364136223846793005 + 1442695040888963407
		b[i] = charset[(n>>32)&int64(len(charset)-1)]
	}
	return string(b)
}
