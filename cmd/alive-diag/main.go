// Command alive-diag 读 alive 的日志，按「症状」回答排障问题。
//
// 日志写得再全，如果没人能在一分钟内从里面拿到答案，它仍然是一份写-only
// 数据库。这个命令就是读侧：timeline 重建一间房/一轮会话的时间线，answer
// 直接跑一组预先定义好的查询。
//
// 用法：
//
//	alive-diag timeline <日志文件或目录>
//	alive-diag answer <症状> <日志文件或目录>
//	alive-diag symptoms
//	alive-diag bundle <日志目录> <输出.zip>
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"alive/internal/diag"
	"alive/internal/support"
)

type entry struct {
	at    time.Time
	level string
	msg   string
	f     map[string]string
	raw   string
}

func (e entry) str(k string) string { return e.f[k] }
func (e entry) event() string       { return e.f[diag.Event] }

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "timeline":
		if len(os.Args) != 3 {
			usage()
			os.Exit(2)
		}
		entries, err := load(os.Args[2])
		if err != nil {
			fatal(err)
		}
		fmt.Print(timeline(entries))
	case "answer":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		entries, err := load(os.Args[3])
		if err != nil {
			fatal(err)
		}
		out, ok := answer(os.Args[2], entries)
		if !ok {
			fmt.Fprintf(os.Stderr, "未知症状 %q；可用：\n%s", os.Args[2], symptomList())
			os.Exit(2)
		}
		fmt.Print(out)
	case "symptoms":
		fmt.Print(symptomList())
	case "bundle":
		if len(os.Args) != 4 {
			usage()
			os.Exit(2)
		}
		if err := bundle(os.Args[2], os.Args[3]); err != nil {
			fatal(err)
		}
		fmt.Printf("已写出 %s\n", os.Args[3])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `alive-diag — 从日志回答排障问题

  alive-diag timeline <日志文件或目录>
  alive-diag answer <症状> <日志文件或目录>
  alive-diag symptoms
  alive-diag bundle <日志目录> <输出.zip>
`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "alive-diag:", err)
	os.Exit(1)
}

// load 读一个日志文件，或一个目录里的全部 alive-*.log（按修改时间排序）。
func load(path string) ([]entry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return readFile(path)
	}
	files, err := filepath.Glob(filepath.Join(path, "alive-*.log"))
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		a, _ := os.Stat(files[i])
		b, _ := os.Stat(files[j])
		return a.ModTime().Before(b.ModTime())
	})
	var all []entry
	for _, f := range files {
		es, err := readFile(f)
		if err != nil {
			return nil, err
		}
		all = append(all, es...)
	}
	return all, nil
}

func readFile(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var out []entry
	for sc.Scan() {
		e, ok := parseLine(sc.Text())
		if ok {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func parseLine(line string) (entry, bool) {
	if !strings.HasPrefix(line, "{") {
		return entry{}, false
	}
	f := parseFields(line)
	if f == nil {
		return entry{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, f[diag.Time])
	if err != nil {
		return entry{}, false
	}
	return entry{at: at, level: f[diag.LevelKey], msg: f[diag.Msg], f: f, raw: line}, true
}

// parseFields 把一行 JSON 摊平成 key→字符串。
//
// 日志是 JSON 之后，这里终于不用自己切引号了——以前那段手写解析器要处理
// Go 的双引号转义、带空格的值、逃逸字符，解析错一处就是排障时少一行证据。
//
// 嵌套对象摊平成点分键（err{class:"upstream"} → "err.class"），读侧那几十处
// 按扁平键取字段的代码一行都不用动。数字用 json.Number 原样保留，不给毫秒
// 和状态码套上浮点的精度误差。
func parseFields(line string) map[string]string {
	dec := json.NewDecoder(strings.NewReader(line))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil
	}
	f := make(map[string]string, len(obj))
	flatten("", obj, f)
	return f
}

func flatten(prefix string, v any, out map[string]string) {
	switch t := v.(type) {
	case map[string]any:
		for k, sub := range t {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flatten(key, sub, out)
		}
	case []any:
		// 数组不摊平：诊断事件里没有按位置取字段的读法。
		if prefix != "" {
			if b, err := json.Marshal(t); err == nil {
				out[prefix] = string(b)
			}
		}
	case string:
		out[prefix] = t
	case json.Number:
		out[prefix] = t.String()
	case bool:
		out[prefix] = strconv.FormatBool(t)
	case nil:
		// 空值不写：读侧拿到的空串和「没有这个字段」应当是同一种含义。
	default:
		out[prefix] = fmt.Sprint(t)
	}
}

// timeline 按 session（没有就按 room）分组，重建每一条时间线。
func timeline(entries []entry) string {
	type group struct {
		key   string
		first time.Time
		list  []entry
	}
	groups := map[string]*group{}
	var order []string
	for _, e := range entries {
		// 同一间房的第 N 次起播都叫 watch#N；只按 session 分组会把三间房
		// 并成一条时间线。room 和 session 必须一起做键。
		room, session := e.str(diag.Room), e.str(diag.Session)
		var key string
		switch {
		case room != "" && session != "":
			key = room + " " + session
		case room != "":
			key = room
		case session != "":
			key = session
		default:
			key = "run=" + e.str(diag.Run)
		}
		g := groups[key]
		if g == nil {
			g = &group{key: key, first: e.at}
			groups[key] = g
			order = append(order, key)
		}
		g.list = append(g.list, e)
	}
	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]].first.Before(groups[order[j]].first)
	})
	var b strings.Builder
	for _, k := range order {
		g := groups[k]
		fmt.Fprintf(&b, "\n== %s  (%d 条) ==\n", g.key, len(g.list))
		for _, e := range g.list {
			fmt.Fprintf(&b, "%s  %-7s %-18s %s",
				e.at.Format("01-02 15:04:05.000"),
				e.level,
				e.event(),
				e.msg,
			)
			for _, k := range []string{diag.MS, diag.Cause, diag.Stage, diag.Attempt, diag.Failures,
				diag.RetryMS, diag.SinceFrameMS, diag.OpenMS, diag.Quality, diag.GPU, diag.Module,
				diag.ErrClassKey, diag.ErrOpKey, diag.ErrStatusKey, diag.ErrHostKey, diag.ErrTextKey} {
				if v := e.str(k); v != "" && v != "0" {
					fmt.Fprintf(&b, " %s=%s", k, v)
				}
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ---------------------------------------------------------------- answer

func answer(symptom string, entries []entry) (string, bool) {
	switch symptom {
	case "startup":
		return ansStartup(entries), true
	case "black-tile":
		return ansBlackTile(entries), true
	case "reconnect-loop":
		return ansReconnectLoop(entries), true
	case "no-audio":
		return ansNoAudio(entries), true
	case "player-fail":
		return ansPlayerFail(entries), true
	case "quality":
		return ansQuality(entries), true
	case "danmaku":
		return ansDanmaku(entries), true
	case "settings":
		return ansSettings(entries), true
	case "freeze":
		return ansFreeze(entries), true
	case "last-run":
		return ansLastRun(entries), true
	}
	return "", false
}

func symptomList() string {
	return `症状 → 查询：
  startup          上次启动是哪一个构建、哪个日志、什么级别
  last-run         上次是否正常退出；最后一条事件是什么
  black-tile       墙上哪些格子失败/未开播，且从未出过首帧
  reconnect-loop   哪些房间在反复重连，重开几次、每次原因
  no-audio         音频时钟停走或音频属性不可用
  player-fail      播放页落到错误终态的原因
  quality          请求档位与实际解出的分辨率
  danmaku          弹幕连接失败/恢复
  settings         状态文件读写失败
  freeze           视频停帧/主线程卡死
`
}

func ansStartup(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		if e.event() == "run.start" {
			fmt.Fprintf(&b, "%s\n", e.raw)
		}
	}
	if b.Len() == 0 {
		return "日志里没有 run.start：这是旧构建或日志被截断。\n"
	}
	return b.String()
}

func ansLastRun(entries []entry) string {
	if len(entries) == 0 {
		return "没有日志。\n"
	}
	var lastStart, lastStop = -1, -1
	for i, e := range entries {
		switch e.event() {
		case "run.start":
			lastStart = i
			lastStop = -1
		case "run.stop":
			lastStop = i
		}
	}
	if lastStart < 0 {
		return "没有 run.start，无法判断进程边界。\n"
	}
	start := entries[lastStart]
	var b strings.Builder
	fmt.Fprintf(&b, "上次启动：%s run=%s\n", start.at.Format("2006-01-02 15:04:05"), start.str(diag.Run))
	if lastStop >= 0 {
		stop := entries[lastStop]
		fmt.Fprintf(&b, "正常退出：%s reason=%s\n", stop.at.Format("15:04:05"), stop.str(diag.Reason))
	} else {
		fmt.Fprintf(&b, "没有 run.stop：上次没有正常退出（崩溃/被杀/仍在运行）。\n")
	}
	// log.stop 是日志系统自己的收尾，不是业务事件；找最后一条真正的事件。
	last := entries[len(entries)-1]
	for _, e := range slices.Backward(entries) {
		if ev := e.event(); ev != "log.stop" && ev != "log.start" {
			last = e
			break
		}
	}
	fmt.Fprintf(&b, "最后一条：%s %s %s\n", last.at.Format("15:04:05"), last.event(), last.msg)
	return b.String()
}

func ansBlackTile(entries []entry) string {
	type state struct {
		opened, first bool
		last          entry
	}
	rooms := map[string]*state{}
	for _, e := range entries {
		room := e.str(diag.Room)
		if room == "" {
			continue
		}
		s := rooms[room]
		if s == nil {
			s = &state{}
			rooms[room] = s
		}
		switch e.event() {
		case "wall.open":
			s.opened = true
			s.first = false
			s.last = e
		case "wall.first_frame":
			s.first = true
		case "wall.fail", "wall.offline":
			s.last = e
		}
	}
	var keys []string
	for k := range rooms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		s := rooms[k]
		if !s.opened || s.first {
			continue
		}
		fmt.Fprintf(&b, "%s  最后一次: %s %s cause=%s stage=%s err=%s\n",
			k, s.last.at.Format("01-02 15:04:05"), s.last.event(),
			s.last.str(diag.Cause), s.last.str(diag.Stage), s.last.str(diag.ErrTextKey))
	}
	if b.Len() == 0 {
		return "没有「开了但从未出首帧」的格子。\n"
	}
	return b.String()
}

func ansReconnectLoop(entries []entry) string {
	type state struct {
		restarts int
		last     entry
	}
	rooms := map[string]*state{}
	for _, e := range entries {
		if e.event() != "wall.restart" {
			continue
		}
		r := e.str(diag.Room)
		s := rooms[r]
		if s == nil {
			s = &state{}
			rooms[r] = s
		}
		s.restarts++
		s.last = e
	}
	var keys []string
	for k := range rooms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return rooms[keys[i]].restarts > rooms[keys[j]].restarts })
	var b strings.Builder
	for _, k := range keys {
		s := rooms[k]
		if s.restarts < 2 {
			continue
		}
		fmt.Fprintf(&b, "%s  重开 %d 次  最后一次 %s cause=%s attempt=%s retry_ms=%s since_frame_ms=%s\n",
			k, s.restarts, s.last.at.Format("01-02 15:04:05"),
			s.last.str(diag.Cause), s.last.str(diag.Attempt), s.last.str(diag.RetryMS), s.last.str(diag.SinceFrameMS))
	}
	if b.Len() == 0 {
		return "没有反复重连的房间。\n"
	}
	return b.String()
}

func ansNoAudio(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "session.audio_stall":
			fmt.Fprintf(&b, "%s %s audio_ok=%s pos_ms=%s audio_ms=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Session),
				e.str(diag.AudioOK), e.str(diag.PositionMS), e.str(diag.AudioMS))
		case "session.buffer":
			// AudioPTSValid=false 说明这一路没有音轨或属性订阅失败；
			// 它不会被 session.audio_stall 记到，必须从缓冲行里看。
			if e.str(diag.AudioOK) == "false" {
				fmt.Fprintf(&b, "%s %s 音频属性不可用（audio_ok=false）\n",
					e.at.Format("01-02 15:04:05"), e.str(diag.Session))
			}
		case "mpv":
			if strings.Contains(strings.ToLower(e.str(diag.Text)), "audio") {
				fmt.Fprintf(&b, "%s %s mpv[%s] %s\n",
					e.at.Format("01-02 15:04:05"), e.str(diag.Session), e.str(diag.Module), e.str(diag.Text))
			}
		}
	}
	if b.Len() == 0 {
		return "没有音频异常事件。\n"
	}
	return b.String()
}

func ansPlayerFail(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "session.fail", "session.fetch_fail", "session.open_fail":
			fmt.Fprintf(&b, "%s %s %s class=%s err=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Room), e.event(),
				e.str(diag.ErrClassKey), e.str(diag.ErrTextKey))
		}
	}
	if b.Len() == 0 {
		return "没有播放页失败事件。\n"
	}
	return b.String()
}

func ansQuality(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "session.fetch":
			fmt.Fprintf(&b, "%s %s 请求档位=%s variants=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Room), e.str(diag.Quality), e.str(diag.Variants))
		case "session.first_frame":
			fmt.Fprintf(&b, "%s %s 解出 %sx%s (声明 %sx%s) gpu=%s gpu_frames=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Room),
				e.str(diag.DecodedW), e.str(diag.DecodedH),
				e.str(diag.DeclaredW), e.str(diag.DeclaredH),
				e.str(diag.GPU), e.str(diag.GPUFrames))
		}
	}
	if b.Len() == 0 {
		return "没有取流/首帧事件。\n"
	}
	return b.String()
}

func ansDanmaku(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "danmaku.connect", "danmaku.fail", "danmaku.recover":
			fmt.Fprintf(&b, "%s %s %s class=%s err=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Room), e.event(),
				e.str(diag.ErrClassKey), e.str(diag.ErrTextKey))
		}
	}
	if b.Len() == 0 {
		return "没有弹幕连接事件。\n"
	}
	return b.String()
}

func ansSettings(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "state.load_fail", "state.save_fail":
			fmt.Fprintf(&b, "%s %s stage=%s path=%s err=%s\n",
				e.at.Format("01-02 15:04:05"), e.event(), e.str(diag.Stage), e.str(diag.Path), e.str(diag.ErrTextKey))
		}
	}
	if b.Len() == 0 {
		return "没有状态读写失败。\n"
	}
	return b.String()
}

func ansFreeze(entries []entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch e.event() {
		case "session.video_stall", "hang":
			fmt.Fprintf(&b, "%s %s %s render_calls=%s new_frames=%s pos_ms=%s\n",
				e.at.Format("01-02 15:04:05"), e.str(diag.Room), e.event(),
				e.str(diag.RenderCalls), e.str(diag.NewFrames), e.str(diag.PositionMS))
		}
	}
	if b.Len() == 0 {
		return "没有停帧/卡死事件。\n"
	}
	return b.String()
}

// ---------------------------------------------------------------- bundle

// bundle 与桌面「导出诊断包」共用 support.Bundle：同一份 zip 结构，
// 同样的 meta.json / metrics.json / 保留策略。
func bundle(dir, out string) error {
	_, err := support.Bundle(support.Options{
		Out:    out,
		LogDir: dir,
		Meta:   map[string]any{"source": "alive-diag"},
	})
	return err
}
