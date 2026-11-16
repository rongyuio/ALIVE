package playback

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"alive/internal/live"
)

// matrixCase 是一个待测组合：某平台某房间的某个清晰度档位。
type matrixCase struct {
	platform live.Platform
	room     string
	title    string
	variant  live.Variant
}

// matrixResult 是一档的采样结果。
type matrixResult struct {
	c       matrixCase
	first   time.Duration // 从 Open 到第一帧上屏
	fps     float64       // 采样窗口内实际换帧的帧率
	decoded float64       // 采样窗口内解码帧率（区分「流本身 30fps」和「我们掉帧」）
	stalls  int           // 音频断供（Position 250ms 内没走够 200ms）
	worst   time.Duration // 最差的一次断供
	dropped uint64
	size    string
	err     string
}

// TestMatrix 是跨平台、跨房间、跨清晰度的播放验收矩阵。
//
// 默认跳过。它真的连公网、按各平台自己的限流节奏串行取流，单轮几分钟，
// 而且直播间本身会换编码、会下播——结果是某一时刻的快照，不是长期保证。
//
//	$env:ALIVE_MATRIX="1"
//	$env:ALIVE_MATRIX_PLATFORMS="bilibili,douyu,huya,douyin"
//	$env:ALIVE_MATRIX_ROOMS="2"        # 每平台取几个在播房间
//	$env:ALIVE_MATRIX_SECONDS="8"      # 每档采样多久
//	$env:ALIVE_MATRIX_VARIANTS="3"     # 每房间最多测几个档位
func TestMatrix(t *testing.T) {
	if os.Getenv("ALIVE_MATRIX") == "" {
		t.Skip("设 ALIVE_MATRIX=1 跑矩阵（会连公网）")
	}
	platforms := strings.Split(envOr("ALIVE_MATRIX_PLATFORMS", "bilibili,douyu,huya,douyin"), ",")
	rooms := envInt("ALIVE_MATRIX_ROOMS", 2)
	secs := envInt("ALIVE_MATRIX_SECONDS", 8)
	perRoom := envInt("ALIVE_MATRIX_VARIANTS", 3)
	areas := envInt("ALIVE_MATRIX_AREAS", 2)

	var all []matrixResult
	for _, name := range platforms {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		cases, err := collectCases(live.Platform(name), areas, rooms, perRoom)
		if err != nil {
			t.Logf("[%s] 取房间失败：%v", name, err)
			continue
		}
		for _, c := range cases {
			r := sampleOne(c, secs)
			all = append(all, r)
			logResult(t, r)
		}
	}

	t.Logf("")
	t.Logf("平台         房间              档位            编码  尺寸        首帧     解码fps  上屏fps  丢帧  断供")
	for _, r := range all {
		label := r.c.variant.Label
		if len([]rune(label)) > 12 {
			label = string([]rune(label)[:12])
		}
		codec := r.c.variant.Codec
		if codec == "" {
			codec = "-"
		}
		if r.err != "" {
			t.Logf("%-12s %-16s %-14s %-5s 失败：%s", r.c.platform, r.c.room, label, codec, r.err)
			continue
		}
		t.Logf("%-12s %-16s %-14s %-5s %-10s %-8s %-8.1f %-8.1f %-5d %-5d",
			r.c.platform, r.c.room, label, codec, r.size,
			r.first.Truncate(time.Millisecond), r.decoded, r.fps, r.dropped, r.stalls)
	}

	// 汇总：只把真正的失败和卡顿挑出来，避免一堆正常数据淹没结论。
	fails, choppy := 0, 0
	for _, r := range all {
		if r.err != "" {
			fails++
			continue
		}
		if r.fps < 24 || r.stalls > 2 {
			choppy++
		}
	}
	t.Logf("")
	t.Logf("共 %d 档：失败 %d，卡顿 %d", len(all), fails, choppy)
	if fails > 0 {
		t.Errorf("%d 档完全起不来", fails)
	}
}

// collectCases 取某平台前几个一级分区下的在播房间，并为每个房间挑几档清晰度。
//
// 为什么要跨分区：同一个分区里的房间常常是同一类内容、同一个 CDN 池，
// 换分区才能覆盖到不同的编码和线路组合。
func collectCases(p live.Platform, areas, rooms, perRoom int) ([]matrixCase, error) {
	prov, ok := live.ProviderFor(p)
	if !ok {
		return nil, fmt.Errorf("没有这个平台")
	}
	st, ok := prov.(live.Streamer)
	if !ok {
		return nil, fmt.Errorf("这个平台不能取流")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	groups, err := prov.Categories(ctx)
	if err != nil {
		return nil, fmt.Errorf("取分区：%w", err)
	}
	// 每个一级分区取一个二级分区，尽量分散。
	var areaIDs []string
	for _, g := range groups {
		if len(g.Categories) == 0 {
			continue
		}
		areaIDs = append(areaIDs, g.Categories[0].ID)
		if len(areaIDs) >= areas {
			break
		}
	}

	var out []matrixCase
	seen := map[string]bool{}
	for _, area := range areaIDs {
		page, err := prov.Rooms(ctx, area, 1, rooms*4)
		if err != nil {
			continue
		}
		taken := 0
		for _, room := range page.Rooms {
			if taken >= rooms {
				break
			}
			if !room.Live || seen[room.RoomID] {
				continue
			}
			stream, err := st.Stream(ctx, room.RoomID, "")
			if err != nil || !stream.Live || len(stream.Variants) == 0 {
				continue
			}
			seen[room.RoomID] = true
			taken++
			for _, v := range pickVariants(stream.Variants, perRoom) {
				out = append(out, matrixCase{platform: p, room: room.RoomID, title: room.Title, variant: v})
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("没有可播的档位")
	}
	return out, nil
}

// pickVariants 挑最高、最低、以及中间均摊的几档，按 Label 去重。
func pickVariants(vs []live.Variant, max int) []live.Variant {
	seen := map[string]bool{}
	var out []live.Variant
	for _, v := range vs {
		if v.URL == "" || seen[v.Label] {
			continue
		}
		seen[v.Label] = true
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Height != out[j].Height {
			return out[i].Height > out[j].Height
		}
		return out[i].Bitrate > out[j].Bitrate
	})
	if len(out) <= max {
		return out
	}
	if max == 1 {
		return out[:1]
	}
	picked := []live.Variant{out[0], out[len(out)-1]}
	if max > 2 {
		mid := len(out) / 2
		if mid != 0 && mid != len(out)-1 {
			picked = append(picked[:1], append([]live.Variant{out[mid]}, picked[1:]...)...)
		}
	}
	for len(picked) > max {
		picked = picked[:max]
	}
	return picked
}

// sampleOne 打开一档并采样。
//
// 采样前静置两秒：退两片起播会先追积压，那一段的高丢帧是设计内的，
// 算进稳态会误判成卡顿。
func sampleOne(c matrixCase, secs int) matrixResult {
	res := matrixResult{c: c}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs+40)*time.Second)
	defer cancel()

	start := time.Now()
	e, err := Open(ctx, c.variant.URL, Options{
		Headers:   c.variant.Headers,
		Width:     c.variant.Width,
		Height:    c.variant.Height,
		FrameRate: c.variant.FrameRate,
	})
	if err != nil {
		res.err = err.Error()
		return res
	}
	// 等彻底关干净再走下一个：Player 的关闭是异步的（不能阻塞 UI 线程），
	// 而上一个实例还占着 WASAPI 的话，下一个会打不开音频设备，于是
	// video-sync=audio 的时钟停住，整路冻在原地。
	defer func() { _ = e.Close(); <-e.Done() }()

	deadline := time.Now().Add(12 * time.Second)
	first := false
	for time.Now().Before(deadline) {
		if _, _, ok := e.Frame(time.Now()); ok {
			first = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !first {
		dbg := e.Debug()
		res.err = fmt.Sprintf("12 秒无首帧（phase=%s pos=%v buffering=%v err=%v）", dbg.Phase, dbg.Position, dbg.Buffering, dbg.Err)
		return res
	}
	res.first = time.Since(start)
	w, h := e.Size()
	res.size = fmt.Sprintf("%dx%d", w, h)

	time.Sleep(2 * time.Second)

	frames := 0
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tk := time.NewTicker(16 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				if _, changed, _ := e.Frame(time.Now()); changed {
					frames++
				}
			}
		}
	}()

	df0, drop0, _ := e.Stats()
	prev := e.Position()
	begin := time.Now()
	for time.Since(begin) < time.Duration(secs)*time.Second {
		time.Sleep(250 * time.Millisecond)
		cur := e.Position()
		if d := cur - prev; d < 200*time.Millisecond {
			res.stalls++
			if res.worst == 0 || d < res.worst {
				res.worst = d
			}
		}
		prev = cur
	}
	elapsed := time.Since(begin)
	close(stop)
	<-done

	res.fps = float64(frames) / elapsed.Seconds()
	df1, drop1, _ := e.Stats()
	res.decoded = float64(df1-df0) / elapsed.Seconds()
	res.dropped = drop1 - drop0
	// 首帧那一刻 mpv 可能还没解析出尺寸（dwidth 是异步推过来的），这时候记的
	// 是兜底值。采样结束时再读一次才是真正渲染的尺寸。
	if w2, h2 := e.Size(); w2 > 0 && h2 > 0 {
		res.size = fmt.Sprintf("%dx%d", w2, h2)
	}
	return res
}

func logResult(t *testing.T, r matrixResult) {
	if r.err != "" {
		t.Logf("[%s %s %s] 失败：%s", r.c.platform, r.c.room, r.c.variant.Label, r.err)
		return
	}
	t.Logf("[%s %s %s %dk] 首帧 %v，解码 %.1f / 上屏 %.1ffps，丢 %d，断供 %d（最差 %v）",
		r.c.platform, r.c.room, r.c.variant.Label, r.c.variant.Bitrate/1000,
		r.first.Truncate(time.Millisecond), r.decoded, r.fps, r.dropped,
		r.stalls, r.worst.Truncate(time.Millisecond))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
