package applog

import (
	"strings"
	"testing"

	"alive/internal/diag"
)

// ALIVE_LOG_COMPONENTS 只该收紧 Debug，不该吞掉 warn/error。
//
// 旧实现在这条白名单上不判级别：开了 debug + 指定组件之后，没有 sub 字段的
// warn/error（hang、engine.destroy_timeout、log.dropped）和 run.start 一起
// 消失——排障时恰恰把证据关掉了。
func TestComponentsWhitelistKeepsWarnAndError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LOCALAPPDATA", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("ALIVE_LOG_LEVEL", "debug")
	t.Setenv("ALIVE_LOG_COMPONENTS", "wall")
	path, err := Setup()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Close)

	L().Warn().Str(diag.Event, "hang").Msg("没有 sub 的告警")
	L().Error().Str(diag.Event, "engine.open_fail").Msg("没有 sub 的错误")
	L().Debug().Str(diag.Sub, "wall").Msg("选中组件的调试")

	data := read(t, path)
	if !strings.Contains(data, "没有 sub 的告警") {
		t.Fatalf("warn 被组件白名单吞掉了: %q", data)
	}
	if !strings.Contains(data, "没有 sub 的错误") {
		t.Fatalf("error 被组件白名单吞掉了: %q", data)
	}
	if !strings.Contains(data, "选中组件的调试") {
		t.Fatalf("选中组件的 debug 应该保留: %q", data)
	}
}
