package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 一条 fixture 覆盖十个症状。每个查询都必须能从同一份日志里给出矩阵要求的
// 那条事实——这是「诊断时间」验收的可执行版本。
//
// 形状是 zerolog 写出来的：一行一个 JSON 对象，嵌套的 err 是对象，level 小写。
// 旧样本是 slog 的 logfmt，换框架之后它不再代表任何一条真实日志。
const symptomFixture = `{"time":"2026-11-25T03:00:00.000+08:00","level":"info","source":"main.go:1","msg":"进程启动","event":"run.start","run":"r1","commit":"abc"}
{"time":"2026-11-25T03:00:01.000+08:00","level":"info","source":"ui/player_media.go:1","msg":"取流完成","event":"session.fetch","run":"r1","room":"douyu|1","session":"取流#1","quality":"蓝光4M","variants":3}
{"time":"2026-11-25T03:00:02.000+08:00","level":"info","source":"ui/player_media.go:1","msg":"首帧可用","event":"session.first_frame","run":"r1","room":"douyu|1","session":"取流#1","decoded_width":640,"decoded_height":360,"declared_width":1920,"declared_height":1080}
{"time":"2026-11-25T03:00:03.000+08:00","level":"warn","source":"ui/player_media.go:1","msg":"播放失败","event":"session.fail","run":"r1","room":"douyu|1","session":"取流#1","err":{"class":"upstream","status":403,"text":"forbidden"}}
{"time":"2026-11-25T03:00:04.000+08:00","level":"info","source":"ui/player_media.go:1","msg":"缓冲开始","event":"session.buffer","run":"r1","room":"douyu|1","session":"取流#1","state":"start","audio_ok":false}
{"time":"2026-11-25T03:00:05.000+08:00","level":"warn","source":"ui/player_media.go:1","msg":"音频停走","event":"session.audio_stall","run":"r1","room":"douyu|1","session":"取流#1","audio_ok":true,"pos_ms":1000,"audio_ms":0}
{"time":"2026-11-25T03:00:06.000+08:00","level":"warn","source":"ui/player_media.go:1","msg":"视频停帧","event":"session.video_stall","run":"r1","room":"douyu|1","session":"取流#1","render_calls":0,"new_frames":0,"pos_ms":1000}
{"time":"2026-11-25T03:00:07.000+08:00","level":"warn","source":"ui/player_media.go:1","msg":"弹幕连接失败","event":"danmaku.fail","run":"r1","room":"douyu|1","session":"danmaku#1","err":{"class":"blocked"}}
{"time":"2026-11-25T03:00:08.000+08:00","level":"info","source":"ui/player_media.go:1","msg":"弹幕已连接","event":"danmaku.connect","run":"r1","room":"douyu|1","session":"danmaku#1"}
{"time":"2026-11-25T03:00:09.000+08:00","level":"warn","source":"ui/state.go:1","msg":"状态文件损坏","event":"state.load_fail","run":"r1","stage":"parse","path":"state.json","err":{"text":"unexpected end"}}
{"time":"2026-11-25T03:00:10.000+08:00","level":"info","source":"ui/watch_media.go:1","msg":"墙格开始取流","event":"wall.open","run":"r1","room":"huya|2","session":"watch#1"}
{"time":"2026-11-25T03:00:11.000+08:00","level":"warn","source":"ui/watch_media.go:1","msg":"墙格失败","event":"wall.fail","run":"r1","room":"huya|2","session":"watch#1","stage":"fetch","cause":"取流失败","err":{"class":"upstream"}}
{"time":"2026-11-25T03:00:12.000+08:00","level":"info","source":"ui/watch_media.go:1","msg":"墙格重连","event":"wall.restart","run":"r1","room":"huya|2","session":"watch#1","attempt":1,"retry_ms":2000}
{"time":"2026-11-25T03:00:13.000+08:00","level":"info","source":"ui/watch_media.go:1","msg":"墙格重连","event":"wall.restart","run":"r1","room":"huya|2","session":"watch#1","attempt":2,"retry_ms":8000}
{"time":"2026-11-25T03:00:14.000+08:00","level":"info","source":"main.go:1","msg":"进程退出","event":"run.stop","run":"r1","reason":"quit","uptime_ms":14000}
`

func symptomLog(t *testing.T) []entry {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "alive-r1.log")
	if err := os.WriteFile(path, []byte(symptomFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestAllSymptomsAnswerable(t *testing.T) {
	entries := symptomLog(t)
	cases := []struct {
		symptom string
		want    string
	}{
		{"startup", "run.start"},
		{"last-run", "正常退出"},
		{"black-tile", "huya|2"},
		{"reconnect-loop", "重开 2 次"},
		{"no-audio", "音频属性不可用"},
		{"player-fail", "upstream"},
		{"quality", "640"},
		{"danmaku", "blocked"},
		{"settings", "parse"},
		{"freeze", "video_stall"},
	}
	for _, tc := range cases {
		out, ok := answer(tc.symptom, entries)
		if !ok {
			t.Fatalf("%s：查询不存在", tc.symptom)
		}
		if !strings.Contains(out, tc.want) {
			t.Fatalf("%s：输出里没有 %q\n%s", tc.symptom, tc.want, out)
		}
	}
}
