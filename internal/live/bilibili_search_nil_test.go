package live

import (
	"context"
	"net/http"
	"testing"

	biblib "github.com/rongyuio/bilibili"
	bilibiliSearch "github.com/rongyuio/bilibili/search"
)

// TestBiliSearchNullResultReturnsError 看住搜索的空返回。
//
// 库在 HTTP 200、code 0、data 缺失时返回 (nil, nil)（response.go 用 "null"
// 补齐 data，再 Unmarshal 进 **T 就把指针置空了）。Search 直接读
// result.Result 就是 nil 解引用；它跑在 UI 的后台 goroutine 上，全仓没有
// recover，panic 会直接带走进程。
func TestBiliSearchNullResultReturnsError(t *testing.T) {
	api := biblib.New()
	api.Resty().SetTransport(emptyStreamTransport(func(r *http.Request) (*http.Response, error) {
		// 搜索要先过 WBI：给一份合法的密钥，否则 fillWbi 会先报错，
		// 根本走不到「data 为 null」这条路径。
		if r.URL.Path == "/x/web-interface/nav" {
			return emptyStreamResponse(r, `{"code":0,"data":{"wbi_img":{`+
				`"img_url":"https://i0.hdslb.com/bfs/wbi/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.png",`+
				`"sub_url":"https://i0.hdslb.com/bfs/wbi/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.png"}}}`), nil
		}
		return emptyStreamResponse(r, `{"code":0,"data":null}`), nil
	}))

	p := &bilibiliProvider{
		api: api,
		sc:  bilibiliSearch.New(api),
		lim: newLimiter(1, 0),
	}

	rooms, err := p.Search(context.Background(), "测试")
	if err == nil {
		t.Fatalf("上游没有返回结果时应报错，得到 rooms=%v err=nil", rooms)
	}
	if rooms != nil {
		t.Fatalf("出错时不该返回半成品结果：%v", rooms)
	}
	if _, ok := err.(*UpstreamError); !ok {
		t.Fatalf("应包成 *UpstreamError 给界面识别，得到 %T: %v", err, err)
	}
}
