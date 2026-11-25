package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixture = `time=2026-11-25T01:00:00.000+08:00 level=INFO source=main.go:50 msg=进程启动 event=run.start run=r1 pid=42 version=0.1.0 commit=abc
time=2026-11-25T01:00:01.000+08:00 level=INFO source=ui/watch_media.go:592 msg=墙格开始取流 event=wall.open run=r1 sub=wall room=douyu|1 session=watch#1 attempt=0 quality=原画
time=2026-11-25T01:00:02.000+08:00 level=WARN source=ui/watch_media.go:745 msg=墙格失败 event=wall.fail run=r1 sub=wall room=douyu|1 session=watch#1 stage=fetch cause="取流失败" err.class=upstream err.op=stream err.status=403 err.text="forbidden"
time=2026-11-25T01:00:03.000+08:00 level=INFO source=ui/watch_media.go:1126 msg=墙格重连 event=wall.restart run=r1 sub=wall room=douyu|1 session=watch#1 cause=fetch attempt=1 failures=1 retry_ms=2000
time=2026-11-25T01:00:04.000+08:00 level=INFO source=ui/watch_media.go:1126 msg=墙格重连 event=wall.restart run=r1 sub=wall room=douyu|1 session=watch#2 cause=fetch attempt=2 failures=1 retry_ms=2000
time=2026-11-25T01:00:05.000+08:00 level=INFO source=ui/watch_media.go:1126 msg=墙格重连 event=wall.restart run=r1 sub=wall room=douyu|1 session=watch#3 cause=fetch attempt=3 failures=1 retry_ms=2000
time=2026-11-25T01:00:06.000+08:00 level=INFO source=main.go:130 msg=进程退出 event=run.stop run=r1 uptime_ms=6000
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "alive-r1.log")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseQuotedFields(t *testing.T) {
	e, ok := parseLine(strings.Split(fixture, "\n")[2])
	if !ok {
		t.Fatal("解析失败")
	}
	if e.str("cause") != "取流失败" {
		t.Fatalf("cause=%q", e.str("cause"))
	}
	if e.str("err.status") != "403" {
		t.Fatalf("status=%q", e.str("err.status"))
	}
	if e.str("err.text") != "forbidden" {
		t.Fatalf("text=%q", e.str("err.text"))
	}
}

func TestTimelineGroupsBySession(t *testing.T) {
	entries, err := load(writeFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	out := timeline(entries)
	for _, want := range []string{"douyu|1", "wall.open", "wall.fail", "err.status=403"} {
		if !strings.Contains(out, want) {
			t.Fatalf("timeline 缺 %q:\n%s", want, out)
		}
	}
}

func TestAnswerReconnectLoop(t *testing.T) {
	entries, _ := load(writeFixture(t))
	out, ok := answer("reconnect-loop", entries)
	if !ok {
		t.Fatal("查询不存在")
	}
	if !strings.Contains(out, "重开 3 次") {
		t.Fatalf("没数出重连次数:\n%s", out)
	}
	if !strings.Contains(out, "attempt=3") {
		t.Fatalf("没带 attempt:\n%s", out)
	}
}

func TestAnswerLastRun(t *testing.T) {
	entries, _ := load(writeFixture(t))
	out, _ := answer("last-run", entries)
	if !strings.Contains(out, "正常退出") {
		t.Fatalf("没判断出正常退出:\n%s", out)
	}
}

func TestAnswerBlackTile(t *testing.T) {
	entries, _ := load(writeFixture(t))
	out, _ := answer("black-tile", entries)
	if !strings.Contains(out, "douyu|1") {
		t.Fatalf("没找到从未出首帧的格子:\n%s", out)
	}
	if !strings.Contains(out, "upstream") && !strings.Contains(out, "forbidden") {
		// 这一行至少要能看见失败原因；err.text 在 timeline 里，black-tile 里用 stage。
		if !strings.Contains(out, "stage=fetch") {
			t.Fatalf("没带失败阶段:\n%s", out)
		}
	}
}

func TestBundle(t *testing.T) {
	path := writeFixture(t)
	dir := filepath.Dir(path)
	out := filepath.Join(dir, "bundle.zip")
	if err := bundle(dir, out); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("bundle 没生成: %v", err)
	}
}
