package diag

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// 事件写入的基准。跑法：
//
//	go test ./internal/diag -run xxx -bench BenchmarkEmit -benchmem
//
// 目标不是零分配（slog 的属性本来就会分配），而是让「每条事件多少钱」有数，
// 改 handler 或字段时能看出回退。
func BenchmarkEmit(b *testing.B) {
	l := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Emit(ctx, l, slog.LevelInfo, EvSessionFetch, "取流完成",
			Live, true, Quality, "原画", "variants", 3)
	}
}

func BenchmarkErr(b *testing.B) {
	err := &benchErr{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Err(err)
	}
}

type benchErr struct{}

func (*benchErr) Error() string {
	return "upstream 403: forbidden https://cdn.example.com/x?token=SECRET"
}
