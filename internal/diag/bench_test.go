package diag

import (
	"io"
	"testing"

	"github.com/rs/zerolog"
)

// 事件写入的基准。跑法：
//
//	go test ./internal/diag -run xxx -bench BenchmarkEmit -benchmem
//
// 目标不是零分配，而是让「每条事件多少钱」有数，改字段或换 handler 时能看出
// 回退。zerolog 号称零分配，但我们的字段是逐个 Str/Bool 写的，而且事件要经过
// applog 的 JSON 往返，真实数字只有这里能给出。
func BenchmarkEmit(b *testing.B) {
	l := zerolog.New(io.Discard).Level(LevelInfo)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Info().
			Str(Event, EvSessionFetch).
			Bool(Live, true).
			Str(Quality, "原画").
			Int("variants", 3).
			Msg("取流完成")
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
