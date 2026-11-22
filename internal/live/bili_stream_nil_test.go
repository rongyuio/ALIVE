package live

import (
	"context"
	"testing"

	biblib "github.com/rongyuio/bilibili"
	bilibiliLive "github.com/rongyuio/bilibili/live"
)

// TestBiliNullPlayInfoReturnsError 看住「库返回 (nil, nil)」这一形状。
//
// biblib.Execute 在 HTTP 200 且响应体缺少 data（或 data 为 null）时不会报错，
// 而是把 Out 设成 nil 原样返回。取流跑在 UI 后台 goroutine 上，全仓没有
// recover：少一个 nil 判断就是整个客户端被 nil 解引用带走。
func TestBiliNullPlayInfoReturnsError(t *testing.T) {
	old := biliRequestFn
	biliRequestFn = func(context.Context, *biblib.Client, int64, int64) (*bilibiliLive.GetRoomPlayInfoResult, error) {
		return nil, nil
	}
	t.Cleanup(func() { biliRequestFn = old })

	p := &bilibiliProvider{}
	ctx := context.Background()

	info, err := p.biliFetchPlayInfo(ctx, 123)
	if err == nil {
		t.Fatalf("probe 为 nil 时应返回错误，got info=%+v err=nil", info)
	}
	if info != nil {
		t.Fatalf("probe 为 nil 时不该返回半成品骨架，got %+v", info)
	}

	urls, err := p.biliTierURLs(ctx, 123, qnOrigin)
	if err == nil {
		t.Fatalf("res 为 nil 时应返回错误，got urls=%v err=nil", urls)
	}
	if urls != nil {
		t.Fatalf("res 为 nil 时不该返回地址，got %v", urls)
	}
}
